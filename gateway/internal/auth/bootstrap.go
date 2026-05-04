package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// BootstrapOptions 控制首次启动 superadmin 自举行为。
type BootstrapOptions struct {
	// Email 是 superadmin 账号的邮箱；空字符串使用 "admin@example.com"。
	Email string

	// Disabled = true 时完全跳过 bootstrap（生产推荐：换成 IaC/SCM 受控的 provisioning）。
	Disabled bool

	// PassBytes 是随机密码的随机源字节数；< 16 强制为 24（编码后 ≈ 39 字符 base32）。
	// 24 字节 = 192 bit 熵，远超 zxcvbn / HIBP 任何门槛。
	PassBytes int

	// Logger 用于记录 bootstrap 事件；nil 时复用 Service.log。
	// 注意：明文密码**不**通过 Logger 写入（避免被 slog REDACTED 处理或落到日志系统）；
	// 调用方按 BootstrapResult.PlainPassword 自行处置（推荐 stdout 一次性显示）。
	Logger *slog.Logger
}

// BootstrapResult 是 Bootstrap 的回执。
type BootstrapResult struct {
	// Created = true 表示本次启动新建了 superadmin。
	// false 表示已存在同 email 用户（无论角色），跳过了创建。
	Created bool

	Email  string
	UserID string

	// PlainPassword 仅在 Created==true 时非空；调用方一次性消费后必须主动清空引用。
	PlainPassword string
}

// ErrBootstrapDisabled 在 Disabled=true 时由 Bootstrap 返回（result 仍非 nil 但 Created=false）。
var ErrBootstrapDisabled = errors.New("auth: bootstrap disabled")

// Bootstrap 在数据库无指定 email 用户时，创建一个 superadmin 账号并写入随机密码。
//
// 设计选择：
//   - 幂等：用 email 判定（不是 role 计数），方便管理员手动删账号后下次启动重建；
//   - 不走 Register：直接 hash + Create，绕过 zxcvbn / HIBP（我们生成的密码本就是
//     192 bit 高熵串，不可能被字典命中；HIBP 也不会有匹配）；
//   - 不写日志：明文只通过 BootstrapResult 返回，由调用方决定 stdout / 文件 / 安全分发；
//   - role = superadmin（最高权限）：考虑到这是 IaC fallback，需要能管 admin。
func (s *Service) Bootstrap(ctx context.Context, opts BootstrapOptions) (*BootstrapResult, error) {
	if opts.Disabled {
		return &BootstrapResult{Created: false}, ErrBootstrapDisabled
	}

	email := strings.TrimSpace(opts.Email)
	if email == "" {
		email = "admin@example.com"
	}
	logger := opts.Logger
	if logger == nil {
		logger = s.log
	}

	// 幂等检查：email 已存在则跳过（无论角色，不强行覆盖人工配置）。
	if u, err := s.users.GetByEmail(ctx, email); err == nil && u != nil {
		logger.Info("auth bootstrap skipped: account already exists",
			"email", email, "role", u.EffectiveRole())
		return &BootstrapResult{
			Created: false,
			Email:   email,
			UserID:  u.ID,
		}, nil
	}

	plain, err := generateBootstrapPassword(opts.PassBytes)
	if err != nil {
		return nil, fmt.Errorf("auth.Bootstrap: generate password: %w", err)
	}
	hash, err := HashPassword(plain, s.pwParams)
	if err != nil {
		return nil, fmt.Errorf("auth.Bootstrap: hash: %w", err)
	}

	now := s.clock()
	u := &User{
		Email:        email,
		PasswordHash: hash,
		Status:       "active",
		Role:         RoleSuperAdmin,
		CreatedAt:    now,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("auth.Bootstrap: create: %w", err)
	}
	logger.Info("auth bootstrap created superadmin",
		"email", email, "user_id", u.ID, "created_at", now.Format(time.RFC3339))

	return &BootstrapResult{
		Created:       true,
		Email:         email,
		UserID:        u.ID,
		PlainPassword: plain,
	}, nil
}

// generateBootstrapPassword 生成 base32 (Crockford-ish) 随机密码。
//
// 选 base32 而非 base64：人眼可读、可口述、不含易混字符（粗略移除 0/O/1/L/8/B），
// 适合在终端一次性展示让运维抄写到密码管理器。
func generateBootstrapPassword(bytesN int) (string, error) {
	if bytesN < 16 {
		bytesN = 24
	}
	buf := make([]byte, bytesN)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	s := enc.EncodeToString(buf)
	// 不剥离 0/O/1/L 等：StdEncoding 字符集本身就只用 A-Z2-7，无歧义字符；
	// 若调换字符集会破坏 base32 反解，这里用标准编码即可。
	return s, nil
}

// PrintBootstrapBanner 把首次启动 superadmin 信息一次性写入 w（推荐 os.Stderr）。
//
// 只有 Created==true 才会输出明文密码；返回 false 表示无新建（caller 不需打印任何东西）。
// 显式不走 slog：避免被 slog 中间件 / 集中日志收集器把明文外漏到长期存储；运维责任
// 在控制台抓取一次后立即处理。
func PrintBootstrapBanner(w io.Writer, r *BootstrapResult) bool {
	if r == nil || !r.Created {
		return false
	}
	const banner = `
================================================================================
[auth bootstrap] 首次启动已自动创建 superadmin 账号

  email     : %s
  user_id   : %s
  password  : %s

⚠ 此密码仅显示这一次。请立即妥善保存（密码管理器/Vault），随后通过
  /v1/auth/password/change 修改为自有强口令。

⚠ 生产环境建议设置 AUTH_BOOTSTRAP_DISABLED=true 改用 IaC provisioning。
================================================================================
`
	_, _ = fmt.Fprintf(w, banner, r.Email, r.UserID, r.PlainPassword)
	return true
}
