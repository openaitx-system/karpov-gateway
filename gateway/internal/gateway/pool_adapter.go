package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// defaultLeaseTTL 是远程 lease 默认存活时间；超时未 Release：
//
//   - 进程内 registry：自动按 NetworkError 释放（守护 goroutine）
//   - Redis registry：TTL 自然过期，credential 状态保持原样（v0.4 用 asynq
//     扫描 banned/idle 凭据的清理工作器代替）
//
// 30s 来自 Plan §8.1（"Redis SETEX(lease:<id>, 30s)"）。
const defaultLeaseTTL = 30 * time.Second

// PoolGRPCService 把 *pool.Service 适配为 gRPC PoolServiceServer。
//
// v0.3 范围（M30/M33/M35/M36）：管理面 RPC + Acquire/Release 跨进程化 +
// 可插拔 LeaseRegistry（默认进程内，可注入 Redis 用于多副本部署）。
//
// 跨进程 lease 生命周期：
//  1. Acquire RPC → 调本机 *pool.Service.Acquire → 拿到本地 *CredentialLease
//  2. 注册 (leaseID → credentialID) 到 LeaseRegistry，发回 lease_id+payload
//  3. Release RPC → 用 leaseID 在 registry 中 GetDel → 回调
//     svc.ReleaseByCredentialID(credID, result)
//  4. M36 后多副本部署：Acquire/Release 可以落到不同 Pod，因为状态全在 PG
//     + Redis，不依赖本机闭包
// CapabilityTester 测试凭据的指定能力。
type CapabilityTester interface {
	TestCapability(ctx context.Context, lease *provider.CredentialLease, cap provider.Capability) error
}

type PoolGRPCService struct {
	poolv1.UnimplementedPoolServiceServer
	svc        *pool.Service
	reg        LeaseRegistry
	refreshFn  pool.RefreshFunc            // 默认（兼容旧逻辑）
	refreshMap map[string]pool.RefreshFunc // provider → refreshFn
	tester     CapabilityTester            // 可选
}

// NewPoolGRPCService 构造 PoolGRPCService（默认 in-memory LeaseRegistry）。
//
// 多副本部署用 NewPoolGRPCServiceWithRegistry 注入 Redis registry。
func NewPoolGRPCService(svc *pool.Service) *PoolGRPCService {
	return NewPoolGRPCServiceWithRegistry(svc, NewMemLeaseRegistry(defaultLeaseTTL, func(credID string, result provider.PoolResult) {
		svc.ReleaseByCredentialID(context.Background(), credID, result)
	}))
}

// NewPoolGRPCServiceWithRegistry 注入自定义 registry（M36 Redis 用）。
func NewPoolGRPCServiceWithRegistry(svc *pool.Service, reg LeaseRegistry) *PoolGRPCService {
	return &PoolGRPCService{svc: svc, reg: reg}
}

// Acquire 实现 poolv1.PoolServiceServer.Acquire。
//
// 选凭据 → 注册 lease 映射 → 发回 lease_id+payload。
func (s *PoolGRPCService) Acquire(ctx context.Context, req *poolv1.AcquireRequest) (*poolv1.CredentialLease, error) {
	if req.GetProvider() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider required")
	}
	cap := pool.ParseCapName(req.GetCapability())
	lease, err := s.svc.Acquire(ctx, req.GetProvider(), pool.AcquireOptions{Capability: cap})
	if err != nil {
		if errors.Is(err, pool.ErrNoCandidate) {
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	leaseID, err := newCredentialID()
	if err != nil {
		// 拿不到 ID 视为 NetworkError 释放，保持 health 不被冒升。
		lease.Release(provider.PoolResultNetworkError)
		return nil, status.Error(codes.Internal, err.Error())
	}
	expires, err := s.reg.Put(ctx, leaseID, lease.ID)
	if err != nil {
		// registry 写失败也要把已 Acquire 的 lease 还回去。
		lease.Release(provider.PoolResultNetworkError)
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &poolv1.CredentialLease{
		LeaseId:      leaseID,
		CredentialId: lease.ID,
		Provider:     lease.Provider,
		Payload:      lease.Payload,
		ExpiresAt:    timestamppb.New(expires),
	}, nil
}

// Release 实现 poolv1.PoolServiceServer.Release。
//
// 按 lease_id 在 registry 中 GetDel → 拿到 credID → 应用状态机。lease 不存在
// （已 TTL 超时或重复 Release）静默返回成功——幂等：调用方重试不会扣健康分。
func (s *PoolGRPCService) Release(ctx context.Context, req *poolv1.ReleaseRequest) (*emptypb.Empty, error) {
	if req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id required")
	}
	result := protoToPoolResult(req.GetResult())
	if _, err := s.reg.TakeAndApply(ctx, req.GetLeaseId(), func(credID string) {
		s.svc.ReleaseByCredentialID(ctx, credID, result)
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// AddCredential 实现 poolv1.PoolServiceServer.AddCredential。
//
// 入参的明文 payload 在 *pool.Service.AddCredential 路径上经过 Repo（PgRepo
// 时落库前会被 AES-256-GCM 加密；MemRepo 时仅内存保存）。
func (s *PoolGRPCService) AddCredential(ctx context.Context, req *poolv1.AddCredentialRequest) (*poolv1.Credential, error) {
	if req.GetProvider() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider required")
	}
	if len(req.GetPayload()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "payload required")
	}
	caps := stringsToCapabilities(req.GetCapabilities())
	id, err := newCredentialID()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	c := pool.Credential{
		ID:           id,
		Provider:     req.GetProvider(),
		Label:        req.GetLabel(),
		Payload:      req.GetPayload(),
		Capabilities: caps,
		Status:       pool.StatusActive,
		HealthScore:  1.0,
	}
	if err := s.svc.AddCredential(ctx, c); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return credentialToProto(c), nil
}

// RemoveCredential 实现 poolv1.PoolServiceServer.RemoveCredential。
func (s *PoolGRPCService) RemoveCredential(ctx context.Context, req *poolv1.RemoveCredentialRequest) (*emptypb.Empty, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	if err := s.svc.RemoveCredential(ctx, req.GetId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// HealthSummary 实现 poolv1.PoolServiceServer.HealthSummary。
//
// 入参 provider 为空时返回所有 provider 的概览（v0.3 单 provider 用 *pool.Service.HealthSummary
// 返回单条；多 provider 由调用方多次询问）。
func (s *PoolGRPCService) HealthSummary(ctx context.Context, req *poolv1.HealthSummaryRequest) (*poolv1.PoolHealth, error) {
	if req.GetProvider() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider required (v0.3: single-provider summary)")
	}
	sum, err := s.svc.HealthSummary(ctx, req.GetProvider())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &poolv1.PoolHealth{
		Pools: []*poolv1.ProviderPool{{
			Provider:       sum.Provider,
			Active:         int32(sum.ActiveCount),
			Banned:         int32(sum.BannedCount),
			AvgHealthScore: sum.AverageScore,
		}},
	}, nil
}

// ListCredentials 实现 poolv1.PoolServiceServer.ListCredentials。
//
// 管理面分页查询；前端 /admin/pool 列表用。
func (s *PoolGRPCService) ListCredentials(ctx context.Context, req *poolv1.ListCredentialsRequest) (*poolv1.ListCredentialsResponse, error) {
	items, total, err := s.svc.ListCredentials(ctx, pool.ListCredentialsOptions{
		Provider:     req.GetProvider(),
		StatusFilter: req.GetStatusFilter(),
		Limit:        int(req.GetLimit()),
		Offset:       int(req.GetOffset()),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &poolv1.ListCredentialsResponse{
		Items: make([]*poolv1.Credential, 0, len(items)),
		Total: int32(total),
	}
	for _, c := range items {
		out.Items = append(out.Items, credentialToProto(c))
	}
	return out, nil
}

// SetCredentialStatus 实现 poolv1.PoolServiceServer.SetCredentialStatus。
//
// 仅接受 "active" / "disabled"；"banned" 由 healthworker 熔断使用，人工拒绝。
func (s *PoolGRPCService) SetCredentialStatus(ctx context.Context, req *poolv1.SetCredentialStatusRequest) (*poolv1.Credential, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	st := pool.Status(req.GetStatus())
	if err := s.svc.SetCredentialStatus(ctx, req.GetId(), st); err != nil {
		if errors.Is(err, pool.ErrInvalidStatus) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	c, err := s.svc.GetCredential(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return credentialToProto(c), nil
}

// SetRefreshFunc 注入默认凭据刷新函数（兼容旧逻辑）。
func (s *PoolGRPCService) SetRefreshFunc(fn pool.RefreshFunc) { s.refreshFn = fn }

// RegisterRefreshFunc 为指定 provider 注册刷新函数。
func (s *PoolGRPCService) RegisterRefreshFunc(provider string, fn pool.RefreshFunc) {
	if s.refreshMap == nil {
		s.refreshMap = make(map[string]pool.RefreshFunc)
	}
	s.refreshMap[provider] = fn
}

// SetTester 注入能力测试器（启动时由 runner 调用）。
func (s *PoolGRPCService) SetTester(t CapabilityTester) { s.tester = t }

// RefreshCredential 主动刷新凭据并返回更新后快照。
// 根据凭证的 provider 字段路由到对应的刷新函数。
func (s *PoolGRPCService) RefreshCredential(ctx context.Context, req *poolv1.RefreshCredentialRequest) (*poolv1.Credential, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	// 先获取凭证查其 provider
	cred, err := s.svc.GetCredential(ctx, req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "credential not found: %v", err)
	}
	// 按 provider 选择刷新函数
	fn := s.refreshFn
	if s.refreshMap != nil {
		if provFn, ok := s.refreshMap[cred.Provider]; ok {
			fn = provFn
		}
	}
	if fn == nil {
		return nil, status.Errorf(codes.Unimplemented, "refresh not configured for provider %q", cred.Provider)
	}
	c, err := s.svc.RefreshCredential(ctx, req.GetId(), fn)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "refresh: %v", err)
	}
	return credentialToProto(c), nil
}

// TestCredential 用指定能力测试凭据可用性。
func (s *PoolGRPCService) TestCredential(ctx context.Context, req *poolv1.TestCredentialRequest) (*poolv1.TestCredentialResponse, error) {
	if s.tester == nil {
		return nil, status.Error(codes.Unimplemented, "tester not configured")
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	capName := req.GetCapability()
	if capName == "" {
		capName = "HealthCheck"
	}
	cap := pool.ParseCapName(capName)

	c, err := s.svc.GetCredential(ctx, req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "credential: %v", err)
	}
	lease := &provider.CredentialLease{
		ID:       c.ID,
		Provider: c.Provider,
		Payload:  c.Payload,
		Release:  func(_ provider.PoolResult) {},
	}

	start := time.Now()
	testErr := s.tester.TestCapability(ctx, lease, cap)
	latency := time.Since(start).Milliseconds()

	resp := &poolv1.TestCredentialResponse{
		Capability: capName,
		LatencyMs:  int32(latency),
	}
	if testErr != nil {
		resp.Success = false
		resp.Error = testErr.Error()
	} else {
		resp.Success = true
	}
	return resp, nil
}

// Close 关闭 lease registry 守护资源（生产用 graceful shutdown 调）。
func (s *PoolGRPCService) Close() { s.reg.Close() }

// credentialToProto 把 pool.Credential 转 poolv1.Credential。
func credentialToProto(c pool.Credential) *poolv1.Credential {
	out := &poolv1.Credential{
		Id:          c.ID,
		Provider:    c.Provider,
		Label:       c.Label,
		Status:      string(c.Status),
		HealthScore: c.HealthScore,
		FailCount:   int32(c.FailCount),
	}
	if !c.LastUsedAt.IsZero() {
		out.LastUsedAt = timestamppb.New(c.LastUsedAt)
	}
	if !c.LastFailedAt.IsZero() {
		out.LastFailedAt = timestamppb.New(c.LastFailedAt)
	}
	if !c.CooldownUntil.IsZero() {
		out.CooldownUntil = timestamppb.New(c.CooldownUntil)
	}
	if !c.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(c.CreatedAt)
	}
	if expiresAt := pool.ParseExpiresAt(c.Payload); !expiresAt.IsZero() {
		out.ExpiresAt = timestamppb.New(expiresAt)
	}
	if len(c.Capabilities) > 0 {
		out.Capabilities = make([]string, 0, len(c.Capabilities))
		for _, cap := range c.Capabilities {
			if name := pool.CapName(cap); name != "" {
				out.Capabilities = append(out.Capabilities, name)
			}
		}
	}
	return out
}

// stringsToCapabilities 把 proto 字符串数组转成 provider.Capability 切片。
//
// 委托 pool.ParseCapName，避免与 internal/pool/pgrepo.go 双份同步压力。
func stringsToCapabilities(ss []string) []provider.Capability {
	out := make([]provider.Capability, 0, len(ss))
	for _, s := range ss {
		if c := pool.ParseCapName(s); c != provider.CapNone {
			out = append(out, c)
		}
	}
	return out
}

// newCredentialID 生成 16 字节 hex ID（与 PG schema UUID 兼容；MemRepo 也通用）。
//
// 不用 uuid 包是为了减少依赖：32 hex chars 已经足够给凭据做主键。
func newCredentialID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// protoToPoolResult 把 wire 枚举映射回 provider.PoolResult。
//
// UNSPECIFIED 落到 NetworkError——调用方应总是显式赋值，但安全侧偏保守。
func protoToPoolResult(r poolv1.PoolResult) provider.PoolResult {
	switch r {
	case poolv1.PoolResult_POOL_RESULT_OK:
		return provider.PoolResultOK
	case poolv1.PoolResult_POOL_RESULT_RATE_LIMITED:
		return provider.PoolResultRateLimited
	case poolv1.PoolResult_POOL_RESULT_AUTH_FAILED:
		return provider.PoolResultAuthFailed
	default:
		return provider.PoolResultNetworkError
	}
}
