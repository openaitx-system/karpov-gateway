package modules

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// 登录相关错误（业务码映射，Python `_COMMON_LOGIN_ERROR_SPECS` 子集）。
var (
	// ErrLoginRateLimited 等价 Python `LoginRateLimitedError`（code=104604/100001）。
	ErrLoginRateLimited = errors.New("qqmusic: login rate limited")
	// ErrLoginCaptcha 等价 Python `LoginSecurityRequiredError`（code=20276）。
	ErrLoginCaptcha = errors.New("qqmusic: captcha required")
	// ErrLoginCredentialExpired 等价 Python `LoginCredentialExpiredError`（code=1000/104400/104401）。
	ErrLoginCredentialExpired = errors.New("qqmusic: credential expired")
	// ErrLoginAuthCode 等价 Python `LoginAuthCodeError`（code=20271）。
	ErrLoginAuthCode = errors.New("qqmusic: invalid auth code")
	// ErrLoginAccountBanned 等价 Python `LoginAccountBannedError`（code=20450）。
	ErrLoginAccountBanned = errors.New("qqmusic: account banned")
	// ErrLoginDeviceLimit 等价 Python `LoginDeviceLimitError`（code=20279）。
	ErrLoginDeviceLimit = errors.New("qqmusic: device limit exceeded")

	// ErrMobileQRStreamNotImplemented 表示 MQTT 二维码登录尚未在 Go 侧落地。
	//
	// MQTT 5.0 + WebSocket + USER_PROPERTY/SERVER_REFERENCE 重定向需要 paho.golang/autopaho；
	// 留 v0.2 实现，对应 plan §4.4 + §10 风险登记。
	ErrMobileQRStreamNotImplemented = errors.New("qqmusic: mobile QR MQTT stream not implemented (v0.2)")
)

// PhoneLoginEvent 等价 Python `PhoneLoginEvents`：手机验证码发送结果状态。
type PhoneLoginEvent int

const (
	PhoneLoginEventUnknown   PhoneLoginEvent = -1 // OTHER
	PhoneLoginEventSend      PhoneLoginEvent = 0  // SEND
	PhoneLoginEventCaptcha   PhoneLoginEvent = 20276
	PhoneLoginEventFrequency PhoneLoginEvent = 100001
)

// PhoneAuthCodeResult 等价 Python `PhoneAuthCodeResult` dataclass。
type PhoneAuthCodeResult struct {
	Event PhoneLoginEvent
	Info  string // CAPTCHA 时为 securityURL
}

// SendPhoneAuthCodeOptions 控制 SendPhoneAuthCode 行为。
//
// EncryptedPhoneNo 与 PhoneNo 二选一：前者优先（兼容客户端预加密手机号场景）。
type SendPhoneAuthCodeOptions struct {
	PhoneNo          int64
	EncryptedPhoneNo string
	CountryCode      int // 默认 86
}

// SendPhoneAuthCode 等价 Python `LoginApi.send_authcode`。
//
// 调用 musicu.fcg `music.login.LoginServer/SendPhoneAuthCode`，platform=Android，
// comm 注入 `tmeLoginMethod=3`。返回 PhoneAuthCodeResult；只有真正的请求/参数错误
// 才返回 error。20276/100001 这类业务限制走 PhoneLoginEvent。
func SendPhoneAuthCode(ctx context.Context, c *qqmusic.Client, opts SendPhoneAuthCodeOptions) (PhoneAuthCodeResult, error) {
	if opts.PhoneNo == 0 && opts.EncryptedPhoneNo == "" {
		return PhoneAuthCodeResult{}, errors.New("qqmusic: phoneNo or encryptedPhoneNo required")
	}
	cc := opts.CountryCode
	if cc <= 0 {
		cc = 86
	}
	param := map[string]any{
		"tmeAppid": "qqmusic",
		"areaCode": fmt.Sprintf("%d", cc),
	}
	if opts.EncryptedPhoneNo != "" {
		param["encryptedPhoneNo"] = opts.EncryptedPhoneNo
	} else {
		param["phoneNo"] = fmt.Sprintf("%d", opts.PhoneNo)
	}

	resp, err := c.RequestMusicu(ctx, []qqmusic.RequestItem{{
		Module: "music.login.LoginServer",
		Method: "SendPhoneAuthCode",
		Param:  param,
	}}, qqmusic.MusicuOptions{
		Comm:     map[string]any{"tmeLoginMethod": 3},
		Platform: qqmusic.PlatformAndroid,
	})
	if err != nil {
		return PhoneAuthCodeResult{}, err
	}
	r0, ok := resp["req_0"].(map[string]any)
	if !ok {
		return PhoneAuthCodeResult{}, fmt.Errorf("qqmusic: missing req_0: %v", resp)
	}
	code, _ := r0["code"].(float64)
	switch int(code) {
	case 0:
		return PhoneAuthCodeResult{Event: PhoneLoginEventSend}, nil
	case int(PhoneLoginEventCaptcha):
		data, _ := r0["data"].(map[string]any)
		url, _ := data["securityURL"].(string)
		return PhoneAuthCodeResult{Event: PhoneLoginEventCaptcha, Info: url}, nil
	case int(PhoneLoginEventFrequency):
		return PhoneAuthCodeResult{Event: PhoneLoginEventFrequency}, ErrLoginRateLimited
	default:
		return PhoneAuthCodeResult{Event: PhoneLoginEventUnknown}, mapLoginCode(int(code))
	}
}

// PhoneAuthorizeOptions 控制 PhoneAuthorize 行为。
type PhoneAuthorizeOptions struct {
	PhoneNo          int64
	EncryptedPhoneNo string
	AuthCode         int
}

// PhoneAuthorize 等价 Python `LoginApi.phone_authorize`：用验证码登录。
//
// musicu.fcg `music.login.LoginServer/Login`，comm={tmeLoginMethod:3, tmeLoginType:0}，
// platform=Android。成功返回 *Credential，否则返回业务错误。
func PhoneAuthorize(ctx context.Context, c *qqmusic.Client, opts PhoneAuthorizeOptions) (*qqmusic.Credential, error) {
	if opts.PhoneNo == 0 && opts.EncryptedPhoneNo == "" {
		return nil, errors.New("qqmusic: phoneNo or encryptedPhoneNo required")
	}
	if opts.AuthCode == 0 {
		return nil, errors.New("qqmusic: authCode required")
	}
	param := map[string]any{
		"code":      fmt.Sprintf("%d", opts.AuthCode),
		"loginMode": 1,
	}
	if opts.EncryptedPhoneNo != "" {
		param["encryptedPhoneNo"] = opts.EncryptedPhoneNo
	} else {
		param["phoneNo"] = fmt.Sprintf("%d", opts.PhoneNo)
	}

	data, err := callJSONLogin(ctx, c, "music.login.LoginServer", "Login", param, qqmusic.MusicuOptions{
		Comm:     map[string]any{"tmeLoginMethod": 3, "tmeLoginType": 0},
		Platform: qqmusic.PlatformAndroid,
	})
	if err != nil {
		return nil, err
	}
	return decodeCredential(data)
}

// RefreshCredential 等价 Python `LoginApi.refresh_credential`：用现有凭据刷新。
//
// 根据 cred.LoginType 选择 param 形态（QQ=1 / 微信=2 / 手机=fallback）。
// musicu.fcg `music.login.LoginServer/Login`，comm 注入 tmeLoginType=cred.LoginType。
func RefreshCredential(ctx context.Context, c *qqmusic.Client, cred *qqmusic.Credential) (*qqmusic.Credential, error) {
	if cred == nil {
		cred = c.Credential()
	}
	if cred == nil {
		return nil, errors.New("qqmusic: refresh requires non-nil credential")
	}
	param := buildRefreshParam(cred)
	// 不传 Credential 到 MusicuOptions：避免过期的 authst/musickey 被放入 comm 导致
	// 服务端直接返回 1000(credential expired)。refresh 靠 param 里的 refresh_token/
	// refresh_key 鉴权，不依赖 comm.authst。
	data, err := callJSONLogin(ctx, c, "music.login.LoginServer", "Login", param, qqmusic.MusicuOptions{
		Comm: map[string]any{"tmeLoginType": cred.LoginType},
	})
	if err != nil {
		return nil, err
	}
	return decodeCredential(data)
}

// CheckExpired 等价 Python `LoginApi.check_expired`：探测凭据是否仍有效。
//
// 调用 `music.UserInfo.userInfoServer/GetLoginUserInfo`；若返回 1000/104400/104401
// 视为过期（true），其他错误透传。
func CheckExpired(ctx context.Context, c *qqmusic.Client, cred *qqmusic.Credential) (bool, error) {
	if cred == nil {
		cred = c.Credential()
	}
	_, err := callJSONLogin(ctx, c, "music.UserInfo.userInfoServer", "GetLoginUserInfo",
		map[string]any{}, qqmusic.MusicuOptions{Credential: cred})
	if err == nil {
		return false, nil
	}
	if errors.Is(err, ErrLoginCredentialExpired) {
		return true, nil
	}
	return false, err
}

// MobileQR 等价 Python `QR`（QRLoginType.MOBILE 子集）。
//
// MQTT 流式订阅尚未实现（见 ErrMobileQRStreamNotImplemented）。
type MobileQR struct {
	Image      []byte // PNG 二进制
	Identifier string // qrcodeID（MQTT topic 用）
	MIME       string // image/png
}

// GetMobileQR 等价 Python `LoginApi._get_mobile_qr`：拿手机客户端登录二维码图片。
//
// 仅完成「申请二维码」步骤，无需 MQTT；用户扫码后的状态轮询/凭据落地由
// CheckMobileQRMQTT 完成（暂未实现）。
func GetMobileQR(ctx context.Context, c *qqmusic.Client) (*MobileQR, error) {
	param := map[string]any{
		"tmeAppID": "qqmusic",
	}
	mergeMap(param, queryCommon(c))

	plat := qqmusic.PlatformAndroid
	if c.Platform() == qqmusic.PlatformWeb {
		// Python: platform=Platform.ANDROID if client.platform==WEB else None。
		// Go 侧默认就是 client.platform，这里仅 web→android 强制切换。
	} else {
		plat = c.Platform()
	}

	data, err := callJSONLogin(ctx, c, "music.login.LoginServer", "CreateQRCode", param,
		qqmusic.MusicuOptions{
			Comm:     map[string]any{"ct": 23, "cv": 0},
			Platform: plat,
		})
	if err != nil {
		return nil, err
	}
	rawQR, _ := data["qrcode"].(string)
	id, _ := data["qrcodeID"].(string)
	if rawQR == "" || id == "" {
		return nil, errors.New("qqmusic: empty qrcode payload")
	}
	// "data:image/png;base64,xxxx" → 取末段
	encoded := rawQR
	if idx := strings.LastIndex(rawQR, ","); idx >= 0 {
		encoded = rawQR[idx+1:]
	}
	img, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: decode qrcode base64: %w", err)
	}
	return &MobileQR{Image: img, Identifier: id, MIME: "image/png"}, nil
}

// CheckMobileQRMQTTLegacy 已废弃，完整实现在 login_mobile_mqtt.go 的 CheckMobileQRMQTT 中。
// 保留此 alias 以兼容可能的外部调用。
func CheckMobileQRMQTTLegacy(_ context.Context, _ *qqmusic.Client, _ *MobileQR) error {
	return ErrMobileQRStreamNotImplemented
}

// callJSONLogin 是 callJSON 的登录场景特化：把 req_0.code 业务错误映射为
// ErrLogin* 哨兵，便于上层判断。
func callJSONLogin(ctx context.Context, c *qqmusic.Client, module, method string, param map[string]any, opts qqmusic.MusicuOptions) (map[string]any, error) {
	resp, err := c.RequestMusicu(ctx, []qqmusic.RequestItem{{Module: module, Method: method, Param: param}}, opts)
	if err != nil {
		return nil, err
	}
	r0, ok := resp["req_0"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("qqmusic: missing req_0: %v", resp)
	}
	code, _ := r0["code"].(float64)
	if int(code) != 0 {
		return nil, mapLoginCode(int(code))
	}
	data, _ := r0["data"].(map[string]any)
	if data == nil {
		return nil, ErrMissingData
	}
	return data, nil
}

// mapLoginCode 等价 Python `_COMMON_LOGIN_ERROR_SPECS`。
//
// 返回未知码时统一为 fmt.Errorf 含 code 上下文，方便日志检索。
func mapLoginCode(code int) error {
	switch code {
	case 1000, 104400, 104401:
		return fmt.Errorf("%w (code=%d)", ErrLoginCredentialExpired, code)
	case 20271:
		return fmt.Errorf("%w (code=%d)", ErrLoginAuthCode, code)
	case 20276, 2001, 20254:
		return fmt.Errorf("%w (code=%d)", ErrLoginCaptcha, code)
	case 20279:
		return fmt.Errorf("%w (code=%d)", ErrLoginDeviceLimit, code)
	case 20450:
		return fmt.Errorf("%w (code=%d)", ErrLoginAccountBanned, code)
	case 104604, 100001:
		return fmt.Errorf("%w (code=%d)", ErrLoginRateLimited, code)
	default:
		return fmt.Errorf("qqmusic: login api error code=%d", code)
	}
}

// buildRefreshParam 等价 Python `_build_refresh_param`：按 login_type 构造刷新参数。
func buildRefreshParam(cred *qqmusic.Credential) map[string]any {
	switch cred.LoginType {
	case 1: // QQ
		strID := cred.StrMusicID
		if strID == "" {
			strID = fmt.Sprintf("%d", cred.MusicID)
		}
		return map[string]any{
			"openid":        cred.OpenID,
			"refresh_token": cred.RefreshToken,
			"str_musicid":   strID,
			"musickey":      cred.MusicKey,
			"unionid":       cred.UnionID,
			"refresh_key":   cred.RefreshKey,
			"loginMode":     2,
		}
	case 2: // 微信
		return map[string]any{
			"openid":        cred.OpenID,
			"access_token":  cred.AccessToken,
			"refresh_token": cred.RefreshToken,
			"expired_in":    cred.ExpiredAt,
			"musicid":       cred.MusicID,
			"musickey":      cred.MusicKey,
			"refresh_key":   cred.RefreshKey,
			"loginMode":     2,
		}
	default: // 手机/其他：通用参数
		strID := cred.StrMusicID
		if strID == "" {
			strID = fmt.Sprintf("%d", cred.MusicID)
		}
		return map[string]any{
			"openid":        cred.OpenID,
			"access_token":  cred.AccessToken,
			"refresh_token": cred.RefreshToken,
			"expired_in":    cred.ExpiredAt,
			"str_musicid":   strID,
			"musicid":       cred.MusicID,
			"musickey":      cred.MusicKey,
			"unionid":       cred.UnionID,
			"refresh_key":   cred.RefreshKey,
			"loginMode":     2,
		}
	}
}

// decodeCredential 把 map[string]any 转 *Credential（复用 Credential.UnmarshalJSON）。
func decodeCredential(data map[string]any) (*qqmusic.Credential, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: marshal credential payload: %w", err)
	}
	slog.Info("[qqmusic] decodeCredential raw", "json", string(raw))
	cred := &qqmusic.Credential{}
	if err := json.Unmarshal(raw, cred); err != nil {
		return nil, fmt.Errorf("qqmusic: unmarshal credential: %w", err)
	}
	slog.Info("[qqmusic] decodeCredential result",
		"musicid", cred.MusicID, "musickey_len", len(cred.MusicKey),
		"login_type", cred.LoginType,
		"create_time", cred.MusicKeyCreateTime, "expires_in", cred.KeyExpiresIn,
		"refresh_token_len", len(cred.RefreshToken), "refresh_key_len", len(cred.RefreshKey),
		"is_expired", cred.IsExpired())
	return cred, nil
}
