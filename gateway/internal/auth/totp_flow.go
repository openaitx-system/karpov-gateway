package auth

import (
	"context"
	"time"
)

// BeginEnableTOTP 启用 TOTP 第一步：生成新 secret + otpauth URL，缓存为待确认。
//
// 返回的 secret/otpauth 在 ConfirmEnableTOTP 前 *不* 写入 PG；如果用户离开页面或
// pending TTL（10 分钟）耗尽，secret 自动失效，账号仍处于"未启用"。
//
// 已启用的用户重复调用：返回 ErrTOTPAlreadyEnabled，避免一次操作把现有 Authenticator
// 凭据替换掉（用户必须先 Disable 再 Enable）。
func (s *Service) BeginEnableTOTP(ctx context.Context, userID string) (secretBase32, otpauthURL string, err error) {
	if s.totpPending == nil {
		return "", "", ErrTOTPNotConfigured
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if u.TOTPEnabled {
		return "", "", ErrTOTPAlreadyEnabled
	}
	secret, url, err := TOTPGenerate(s.totpIssuer, u.Email)
	if err != nil {
		return "", "", err
	}
	if err := s.totpPending.Put(ctx, userID, secret, 10*time.Minute); err != nil {
		return "", "", err
	}
	return secret, url, nil
}

// ConfirmEnableTOTP 启用 TOTP 第二步：验证用户输入的 6 位码 → 落库激活。
//
// 安全：
//   - 同样走 replay blocker，防止用户在 30s 内成功 confirm 后重放该码做敏感操作
//   - 失败时不消耗 pending（让用户再试），但 pending 仍受 10 分钟 TTL 限制
//   - 成功后立即 Delete pending，避免遗留可重放的 secret
func (s *Service) ConfirmEnableTOTP(ctx context.Context, userID, code string) (*User, error) {
	if s.totpPending == nil {
		return nil, ErrTOTPNotConfigured
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, ErrTOTPAlreadyEnabled
	}
	secret, err := s.totpPending.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !TOTPVerify(secret, code) {
		return nil, ErrTOTPInvalid
	}
	if s.totpReplay != nil {
		ok, rerr := s.totpReplay.CheckAndMark(ctx, userID, code)
		if rerr != nil || !ok {
			return nil, ErrTOTPInvalid
		}
	}
	u.TOTPSecret = secret
	u.TOTPEnabled = true
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	_ = s.totpPending.Delete(ctx, userID)
	return u, nil
}

// CancelEnableTOTP 用户在第一步后取消（关闭对话框）：清掉 pending。
//
// 不必非调不可（10 分钟自动 TTL）；用作"用户主动取消时尽早释放"。
func (s *Service) CancelEnableTOTP(ctx context.Context, userID string) error {
	if s.totpPending == nil {
		return nil
	}
	return s.totpPending.Delete(ctx, userID)
}

// DisableTOTP 关闭 TOTP：要求用户提供当前 6 位码二次确认。
//
//   - 验证当前 secret + code，并通过 replay blocker
//   - 通过后清空 User.TOTPSecret + TOTPEnabled = false
//   - 无 pending session 副作用
func (s *Service) DisableTOTP(ctx context.Context, userID, code string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return ErrTOTPNotEnabled
	}
	if err := s.verifyAndConsumeTOTP(ctx, u, code); err != nil {
		return err
	}
	u.TOTPEnabled = false
	u.TOTPSecret = ""
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	// 同时把可能残留的 pending（不该有，但保险）清掉
	if s.totpPending != nil {
		_ = s.totpPending.Delete(ctx, userID)
	}
	return nil
}
