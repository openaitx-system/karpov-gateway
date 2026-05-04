package gateway

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
)

// AuthGRPCService 把 *auth.Service 适配为 gRPC AuthService 实现。
//
// 当前 M16 walking-skeleton 范围：Register / Login / Logout / Me。
// 其它 RPC（TOTP / API Key / 改密 / 重置）保留 UnimplementedAuthServiceServer 默认实现，
// 在后续里程碑按需补齐。
type AuthGRPCService struct {
	authv1.UnimplementedAuthServiceServer
	svc *auth.Service
}

// NewAuthGRPCService 构造 gRPC adapter。
func NewAuthGRPCService(svc *auth.Service) *AuthGRPCService {
	return &AuthGRPCService{svc: svc}
}

// Register 实现 authv1.AuthServiceServer.Register。
//
// clientIP 从 grpc metadata 解出（与 Login 相同来源），交给 Service 做"一 IP 一账号"
// 去重。空 IP 由 Service 自行处理（默认行为：不去重）。
//
// verification_code：当后端启用邮箱验证码注册时必填；服务端会按邮箱反查存储的码并 1 次性消费。
func (s *AuthGRPCService) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	if req.GetEmail() == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password required")
	}
	clientIP, _ := extractClientMeta(ctx)
	u, err := s.svc.Register(ctx, req.GetEmail(), req.GetPassword(), clientIP, req.GetVerificationCode())
	if err != nil {
		return nil, mapAuthError(err)
	}
	emailStatus := s.svc.EmailVerificationStatus()
	activationStatus := s.svc.AccountActivationStatus()
	return &authv1.RegisterResponse{
		UserId:                    u.ID,
		Email:                     u.Email,
		EmailVerificationRequired: emailStatus.Required,
		ActivationEmailSent:       activationStatus.Required, // Required = sender + tokens 全到位时才会真发；和"账号是否 pending"一致
	}, nil
}

// VerifyEmail 实现 authv1.AuthServiceServer.VerifyEmail。
//
// 前端 /activate?token=xxx 落地后 POST 到本接口；成功返回的 user_id + email 用于
// "账号 X 已激活" 的成功页文案。
func (s *AuthGRPCService) VerifyEmail(ctx context.Context, req *authv1.VerifyEmailRequest) (*authv1.VerifyEmailResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token required")
	}
	u, err := s.svc.ActivateAccount(ctx, req.GetToken())
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &authv1.VerifyEmailResponse{UserId: u.ID, Email: u.Email}, nil
}

// ResendActivation 实现 authv1.AuthServiceServer.ResendActivation。
//
// 静默成功：当邮箱不存在 / 已激活时 Service 已经返回 nil；本层照样返回 200 + cooldown，
// 防止"该邮箱是否注册"被探测。
func (s *AuthGRPCService) ResendActivation(ctx context.Context, req *authv1.ResendActivationRequest) (*authv1.ResendActivationResponse, error) {
	if req.GetEmail() == "" {
		return nil, status.Error(codes.InvalidArgument, "email required")
	}
	clientIP, _ := extractClientMeta(ctx)
	if err := s.svc.ResendActivation(ctx, req.GetEmail(), clientIP); err != nil {
		return nil, mapAuthError(err)
	}
	st := s.svc.EmailVerificationStatus()
	return &authv1.ResendActivationResponse{CooldownSeconds: int32(st.CooldownSeconds)}, nil
}

// SendEmailCode 实现 authv1.AuthServiceServer.SendEmailCode。
//
// 透传 client IP / UA 给 Service.SendEmailVerification 做 per-IP 频控。
// 成功返回当前 cooldown / TTL；前端按此渲染倒计时。
func (s *AuthGRPCService) SendEmailCode(ctx context.Context, req *authv1.SendEmailCodeRequest) (*authv1.SendEmailCodeResponse, error) {
	if req.GetEmail() == "" {
		return nil, status.Error(codes.InvalidArgument, "email required")
	}
	clientIP, _ := extractClientMeta(ctx)
	if err := s.svc.SendEmailVerification(ctx, req.GetEmail(), clientIP); err != nil {
		return nil, mapAuthError(err)
	}
	st := s.svc.EmailVerificationStatus()
	return &authv1.SendEmailCodeResponse{
		CooldownSeconds:  int32(st.CooldownSeconds),
		ExpiresInSeconds: int32(st.CodeTTLSeconds),
	}, nil
}

// Login 实现 authv1.AuthServiceServer.Login。
//
// IP / UA 从 grpc metadata 提取（grpc-gateway 自动把 HTTP Header 转 metadata，
// 见 forwardResponseMessage / IncomingContext 协议）。
//
// 当账号开启 2FA 且未携带 totp_code 时，返回 totp_required=true + challenge_id；
// 前端再调 VerifyTOTP(challenge_id, code) 完成第二步。
func (s *AuthGRPCService) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	if req.GetEmail() == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password required")
	}
	ip, ua := extractClientMeta(ctx)
	res, err := s.svc.Login(ctx, req.GetEmail(), req.GetPassword(), req.GetTotpCode(), ip, ua)
	if err != nil {
		return nil, mapAuthError(err)
	}
	if res.Challenge != "" {
		// 2FA 第一步：未签发 session，前端要继续走 VerifyTOTP
		return &authv1.LoginResponse{
			TotpRequired: true,
			ChallengeId:  res.Challenge,
		}, nil
	}
	return &authv1.LoginResponse{
		Sid:       res.Session.SID,
		ExpiresAt: timestamppb.New(res.Session.ExpiresAt),
	}, nil
}

// EnableTOTP 实现 authv1.AuthServiceServer.EnableTOTP（启用第一步）。
func (s *AuthGRPCService) EnableTOTP(ctx context.Context, _ *authv1.EnableTOTPRequest) (*authv1.TOTPSecret, error) {
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	secret, otpurl, err := s.svc.BeginEnableTOTP(ctx, u.ID)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &authv1.TOTPSecret{
		SecretBase32: secret,
		OtpauthUrl:   otpurl,
	}, nil
}

// ConfirmEnableTOTP 实现 authv1.AuthServiceServer.ConfirmEnableTOTP（启用第二步）。
func (s *AuthGRPCService) ConfirmEnableTOTP(ctx context.Context, req *authv1.ConfirmEnableTOTPRequest) (*authv1.User, error) {
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "code required")
	}
	updated, err := s.svc.ConfirmEnableTOTP(ctx, u.ID, req.GetCode())
	if err != nil {
		return nil, mapAuthError(err)
	}
	return userToProto(updated), nil
}

// VerifyTOTP 实现 authv1.AuthServiceServer.VerifyTOTP（登录第二步）。
func (s *AuthGRPCService) VerifyTOTP(ctx context.Context, req *authv1.VerifyTOTPRequest) (*authv1.TOTPResult, error) {
	if req.GetChallengeId() == "" || req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "challenge_id and code required")
	}
	ip, ua := extractClientMeta(ctx)
	sess, _, err := s.svc.CompleteLoginTOTP(ctx, req.GetChallengeId(), req.GetCode(), ip, ua)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &authv1.TOTPResult{
		Sid:       sess.SID,
		ExpiresAt: timestamppb.New(sess.ExpiresAt),
	}, nil
}

// DisableTOTP 实现 authv1.AuthServiceServer.DisableTOTP。
func (s *AuthGRPCService) DisableTOTP(ctx context.Context, req *authv1.DisableTOTPRequest) (*emptypb.Empty, error) {
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "code required")
	}
	if err := s.svc.DisableTOTP(ctx, u.ID, req.GetCode()); err != nil {
		return nil, mapAuthError(err)
	}
	return &emptypb.Empty{}, nil
}

// Logout 实现 authv1.AuthServiceServer.Logout。
//
// 优先用 req.sid（兼容客户端显式传），否则从 cookie/header 中取。
func (s *AuthGRPCService) Logout(ctx context.Context, req *authv1.LogoutRequest) (*emptypb.Empty, error) {
	sid := req.GetSid()
	if sid == "" {
		sid = sidFromContext(ctx)
	}
	if sid == "" {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	if err := s.svc.Logout(ctx, sid); err != nil {
		return nil, mapAuthError(err)
	}
	return &emptypb.Empty{}, nil
}

// Me 实现 authv1.AuthServiceServer.Me：查当前 session 对应用户。
func (s *AuthGRPCService) Me(ctx context.Context, _ *authv1.MeRequest) (*authv1.User, error) {
	sid := sidFromContext(ctx)
	if sid == "" {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	u, _, err := s.svc.VerifySession(ctx, sid)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return userToProto(u), nil
}

// ChangePassword 实现 authv1.AuthServiceServer.ChangePassword。
func (s *AuthGRPCService) ChangePassword(ctx context.Context, req *authv1.ChangePasswordRequest) (*emptypb.Empty, error) {
	sid := sidFromContext(ctx)
	if sid == "" {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	u, _, err := s.svc.VerifySession(ctx, sid)
	if err != nil {
		return nil, mapAuthError(err)
	}
	if req.GetOldPassword() == "" || req.GetNewPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "old_password and new_password required")
	}
	if err := s.svc.ChangePassword(ctx, u.ID, req.GetOldPassword(), req.GetNewPassword()); err != nil {
		return nil, mapAuthError(err)
	}
	return &emptypb.Empty{}, nil
}

// CreateAPIKey 实现 authv1.AuthServiceServer.CreateAPIKey。
func (s *AuthGRPCService) CreateAPIKey(ctx context.Context, req *authv1.CreateAPIKeyRequest) (*authv1.APIKey, error) {
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	var expiresAt time.Time
	if req.GetExpiresAt() != nil {
		expiresAt = req.GetExpiresAt().AsTime()
	}
	input := auth.CreateAPIKeyInput{
		Name:           req.GetName(),
		Description:    req.GetDescription(),
		Scopes:         req.GetScopes(),
		IPAllow:        req.GetIpAllowlist(),
		RateLimitRPM:   int(req.GetRateLimitRpm()),
		RateLimitDaily: req.GetRateLimitDaily(),
		ExpiresAt:      expiresAt,
	}
	plain, rec, err := s.svc.CreateAPIKey(ctx, u.ID, input)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotEnabled) {
			return nil, status.Error(codes.Unimplemented, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := apiKeyToProto(rec)
	out.Plaintext = plain
	return out, nil
}

// GetAPIKey 实现 authv1.AuthServiceServer.GetAPIKey。
func (s *AuthGRPCService) GetAPIKey(ctx context.Context, req *authv1.GetAPIKeyRequest) (*authv1.APIKey, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := s.svc.GetAPIKey(ctx, u.ID, req.GetId())
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return apiKeyToProto(rec), nil
}

// UpdateAPIKey 实现 authv1.AuthServiceServer.UpdateAPIKey。
func (s *AuthGRPCService) UpdateAPIKey(ctx context.Context, req *authv1.UpdateAPIKeyRequest) (*authv1.APIKey, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	name := req.GetName()
	desc := req.GetDescription()
	rpm := int(req.GetRateLimitRpm())
	daily := req.GetRateLimitDaily()
	input := auth.UpdateAPIKeyInput{
		Name:           &name,
		Description:    &desc,
		Scopes:         req.GetScopes(),
		IPAllow:        req.GetIpAllowlist(),
		RateLimitRPM:   &rpm,
		RateLimitDaily: &daily,
	}
	if req.GetExpiresAt() != nil {
		t := req.GetExpiresAt().AsTime()
		input.ExpiresAt = &t
	}
	rec, err := s.svc.UpdateAPIKey(ctx, u.ID, req.GetId(), input)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return apiKeyToProto(rec), nil
}

// SetAPIKeyEnabled 实现 authv1.AuthServiceServer.SetAPIKeyEnabled。
func (s *AuthGRPCService) SetAPIKeyEnabled(ctx context.Context, req *authv1.SetAPIKeyEnabledRequest) (*emptypb.Empty, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.SetAPIKeyEnabled(ctx, u.ID, req.GetId(), req.GetEnabled()); err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// ListAPIKeys 实现 authv1.AuthServiceServer.ListAPIKeys。
func (s *AuthGRPCService) ListAPIKeys(ctx context.Context, _ *authv1.ListAPIKeysRequest) (*authv1.APIKeyList, error) {
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.svc.ListAPIKeys(ctx, u.ID)
	if err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotEnabled) {
			return nil, status.Error(codes.Unimplemented, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &authv1.APIKeyList{Items: make([]*authv1.APIKey, 0, len(keys))}
	for _, k := range keys {
		out.Items = append(out.Items, apiKeyToProto(k))
	}
	return out, nil
}

// RevokeAPIKey 实现 authv1.AuthServiceServer.RevokeAPIKey。
func (s *AuthGRPCService) RevokeAPIKey(ctx context.Context, req *authv1.RevokeAPIKeyRequest) (*emptypb.Empty, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	u, err := s.userFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.RevokeAPIKey(ctx, u.ID, req.GetId()); err != nil {
		if errors.Is(err, auth.ErrAPIKeyNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if errors.Is(err, auth.ErrAPIKeyNotEnabled) {
			return nil, status.Error(codes.Unimplemented, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// userFromCtx 走 session 解出当前用户；其它 RPC 复用。
func (s *AuthGRPCService) userFromCtx(ctx context.Context) (*auth.User, error) {
	sid := sidFromContext(ctx)
	if sid == "" {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	u, _, err := s.svc.VerifySession(ctx, sid)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return u, nil
}

// userToProto 转 *auth.User → wire User（含 role）。
func userToProto(u *auth.User) *authv1.User {
	return &authv1.User{
		Id:          u.ID,
		Email:       u.Email,
		Status:      u.Status,
		Role:        u.EffectiveRole(),
		TotpEnabled: u.TOTPEnabled,
		CreatedAt:   timestamppb.New(u.CreatedAt),
	}
}

// apiKeyToProto 转 *auth.APIKeyRecord → wire APIKey。Plaintext 由 caller 决定填充。
func apiKeyToProto(r *auth.APIKeyRecord) *authv1.APIKey {
	out := &authv1.APIKey{
		Id:             r.ID,
		Prefix:         r.Prefix,
		Name:           r.Name,
		Description:    r.Description,
		Scopes:         append([]string(nil), r.Scopes...),
		IpAllowlist:    append([]string(nil), r.IPAllow...),
		RateLimitRpm:   int32(r.RateLimitRPM),
		RateLimitDaily: r.RateLimitDaily,
		Enabled:        r.Enabled,
		Status:         r.Status(time.Now().UTC()),
		TotalRequests:  r.TotalRequests,
		CreatedAt:      timestamppb.New(r.CreatedAt),
	}
	if !r.ExpiresAt.IsZero() {
		out.ExpiresAt = timestamppb.New(r.ExpiresAt)
	}
	if !r.LastUsedAt.IsZero() {
		out.LastUsedAt = timestamppb.New(r.LastUsedAt)
	}
	return out
}

// extractClientMeta 从 grpc context 解出 client IP & UA。
//
// grpc-gateway 默认转发 X-Forwarded-For / X-Real-IP / User-Agent。
func extractClientMeta(ctx context.Context) (ip, ua string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ""
	}
	if v := md.Get("x-forwarded-for"); len(v) > 0 {
		ip = v[0]
	} else if v := md.Get("x-real-ip"); len(v) > 0 {
		ip = v[0]
	}
	if v := md.Get("user-agent"); len(v) > 0 {
		ua = v[0]
	}
	return ip, ua
}

// sidFromContext 从 grpc metadata 解 session id。
//
// 优先级：自定义 grpcgateway-cookie 头里的 sid → x-session-id → grpcgateway-authorization
// （Bearer 形式仅用于 API Key，不在这里处理）。
func sidFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get("x-session-id"); len(v) > 0 {
		return v[0]
	}
	// grpc-gateway 把 HTTP Cookie 头转成 grpcgateway-cookie metadata
	if v := md.Get("grpcgateway-cookie"); len(v) > 0 {
		return parseCookie(v[0], "sid")
	}
	if v := md.Get("cookie"); len(v) > 0 {
		return parseCookie(v[0], "sid")
	}
	return ""
}

// parseCookie 从 cookie 字符串提取指定名称的 value。
//
// 不引 net/http.Request 仅为解一个值；手写解析更轻。
func parseCookie(header, name string) string {
	for {
		// 跳过前导空格 / 分号
		for len(header) > 0 && (header[0] == ' ' || header[0] == ';') {
			header = header[1:]
		}
		if header == "" {
			return ""
		}
		// 找下一个 ";" 边界
		semi := -1
		for i := 0; i < len(header); i++ {
			if header[i] == ';' {
				semi = i
				break
			}
		}
		var pair string
		if semi >= 0 {
			pair = header[:semi]
			header = header[semi+1:]
		} else {
			pair = header
			header = ""
		}
		// pair = "name=value"
		eq := -1
		for i := 0; i < len(pair); i++ {
			if pair[i] == '=' {
				eq = i
				break
			}
		}
		if eq <= 0 {
			continue
		}
		if pair[:eq] == name {
			return pair[eq+1:]
		}
	}
}

// mapAuthError 把 auth 包错误映射为 gRPC status。
func mapAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredential):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, auth.ErrUserExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, auth.ErrIPAlreadyRegistered):
		// PermissionDenied → grpc-gateway 翻成 HTTP 403。
		// 不用 ResourceExhausted（429），因为这不是限流而是"该 IP 永久无法再注册"。
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, auth.ErrEmailDomainNotAllowed):
		// 域名未被允许：FailedPrecondition → 412，让前端显著提示"请用 @xxx 邮箱"。
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, auth.ErrEmailNotConfigured):
		// SMTP 子系统未配置：Unimplemented → 501，前端应当隐藏发送按钮。
		return status.Error(codes.Unimplemented, err.Error())
	case errors.Is(err, auth.ErrEmailCodeRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, auth.ErrEmailCodeInvalid):
		// 验证码错 / 已过期 / 已消费：统一翻成 InvalidArgument → 400，前端表单提示码错误。
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, auth.ErrEmailCodeCooldown):
		// 冷却中：ResourceExhausted → 429；客户端按响应里的 cooldown 重试。
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, auth.ErrEmailCodeRateLimit):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, auth.ErrAccountNotActivated):
		// 登录路径专用：FailedPrecondition → 412，前端展示"未激活"提示 + 重发按钮。
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, auth.ErrActivationTokenInvalid):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, auth.ErrActivationTokenExpired):
		// Expired 与 Invalid 用不同 code（404 vs 410）方便前端区分文案：
		// 410 → "链接已过期，可重新发送"；404 → "链接无效"。
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, auth.ErrActivationTokenUsed):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, auth.ErrAccountAlreadyActive):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, auth.ErrUserNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, auth.ErrLocked):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, auth.ErrSessionNotFound):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, auth.ErrWeakPassword):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, auth.ErrPwnedPassword):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, auth.ErrTOTPInvalid):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, auth.ErrTOTPNotEnabled):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, auth.ErrTOTPAlreadyEnabled):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, auth.ErrPendingTOTPNotFound):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, auth.ErrTOTPChallengeNotFound):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, auth.ErrTOTPNotConfigured):
		return status.Error(codes.Unimplemented, err.Error())
	case errors.Is(err, auth.ErrInvalidScope):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, auth.ErrInvalidIPAllow):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, auth.ErrAPIKeyIPNotAllowed):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, auth.ErrAPIKeyScopeDenied):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
