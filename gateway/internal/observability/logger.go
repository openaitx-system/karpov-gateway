// Package observability 提供 slog/Prometheus/OpenTelemetry 装配。
// M17 完整实现；当前仅给出基础 logger 工厂，便于其他包引用占位。
package observability

import (
	"log/slog"
	"os"
)

// SensitiveKeys 标记永远不打日志的敏感字段；slog Handler 会替换为 "[REDACTED]"。
var SensitiveKeys = map[string]struct{}{
	"password":        {},
	"password_hash":   {},
	"api_key":         {},
	"musickey":        {},
	"refresh_token":   {},
	"access_token":    {},
	"sid":             {},
	"cookie":          {},
	"authorization":   {},
	"x-api-key":       {},
	"payload_enc":     {},
	"totp_secret":     {},
	"totp_secret_enc": {},
}

// NewLogger 返回带敏感字段脱敏的 JSON slog logger。
//
// TODO(M17): 接入 OpenTelemetry trace_id / span_id / 服务级 attribute。
func NewLogger(service string, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if _, ok := SensitiveKeys[a.Key]; ok {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	}
	h := slog.NewJSONHandler(os.Stdout, opts)
	return slog.New(h).With("service", service)
}
