package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig Redis 连接配置。
type RedisConfig struct {
	Addr         string        // host:port
	Username     string        // ACL，可空
	Password     string        // 可空
	DB           int           // 默认 0
	PoolSize     int           // 默认 = 10*NumCPU()
	MinIdleConns int           // 默认 0
	DialTimeout  time.Duration // 默认 3s
	ReadTimeout  time.Duration // 默认 2s
	WriteTimeout time.Duration // 默认 2s
}

// RedisConnectError 携带分类信息，便于 runner 输出友好提示。
type RedisConnectError struct {
	Addr        string
	HasPassword bool
	Kind        RedisErrorKind
	Hint        string
	Err         error
}

func (e *RedisConnectError) Error() string {
	return fmt.Sprintf("redis %s: %s — %s", e.Kind, e.Err, e.Hint)
}

func (e *RedisConnectError) Unwrap() error { return e.Err }

// RedisErrorKind 错误分类。
type RedisErrorKind string

const (
	RedisErrAuth        RedisErrorKind = "auth-failed"     // NOAUTH / WRONGPASS / 用户/密码不匹配
	RedisErrUnreachable RedisErrorKind = "unreachable"     // connection refused / no route / EOF
	RedisErrTimeout     RedisErrorKind = "timeout"         // dial / ping deadline
	RedisErrTLS         RedisErrorKind = "tls-handshake"   // TLS 不匹配
	RedisErrUnknown     RedisErrorKind = "unknown"
)

// NewRedisClient 构造 *redis.Client 并 Ping 校验。
//
// 失败时返回 *RedisConnectError，runner 应直接打印 .Error() 后 fail-fast。
// 不再隐藏 Ping 错误为 warn —— 早暴露好过运行时随机失败。
func NewRedisClient(ctx context.Context, cfg RedisConfig) (*redis.Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("store: redis addr is empty")
	}
	dialTO := nonZeroDuration(cfg.DialTimeout, 3*time.Second)
	cli := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  dialTO,
		ReadTimeout:  nonZeroDuration(cfg.ReadTimeout, 2*time.Second),
		WriteTimeout: nonZeroDuration(cfg.WriteTimeout, 2*time.Second),
	})
	pingCtx, cancel := context.WithTimeout(ctx, dialTO+time.Second)
	defer cancel()
	if err := cli.Ping(pingCtx).Err(); err != nil {
		_ = cli.Close()
		return nil, classifyRedisErr(cfg, err)
	}
	return cli, nil
}

// classifyRedisErr 把 go-redis 的 error 归类成 RedisConnectError，给出修复建议。
func classifyRedisErr(cfg RedisConfig, err error) *RedisConnectError {
	out := &RedisConnectError{
		Addr:        cfg.Addr,
		HasPassword: cfg.Password != "",
		Err:         err,
		Kind:        RedisErrUnknown,
		Hint:        "请检查 redis 容器是否启动 (docker compose ps redis) 与端口绑定",
	}
	msg := err.Error()
	low := strings.ToLower(msg)

	switch {
	case strings.Contains(msg, "NOAUTH"):
		out.Kind = RedisErrAuth
		if cfg.Password == "" {
			out.Hint = "Redis 启用了 requirepass，但 runner 没拿到密码。" +
				"传 -redis-password 或 export REDIS_PASSWORD"
		} else {
			out.Hint = "传入了密码但 NOAUTH 仍报。检查密码是否与 .env 一致；" +
				"或 docker compose exec redis redis-cli -a $REDIS_PASSWORD ping"
		}
	case strings.Contains(msg, "WRONGPASS"):
		out.Kind = RedisErrAuth
		out.Hint = "密码不匹配 redis requirepass 配置；多半是 -redis-password 与 .env 中 REDIS_PASSWORD 不一致"
	case strings.Contains(low, "connection refused"):
		out.Kind = RedisErrUnreachable
		out.Hint = "TCP 拒绝连接：redis 未启动 / 端口未监听 / -redis 地址写错（应为 host:port）"
	case strings.Contains(low, "no route to host"), strings.Contains(low, "i/o timeout"),
		strings.Contains(low, "context deadline exceeded"):
		out.Kind = RedisErrTimeout
		out.Hint = "网络超时：检查防火墙 / docker 网络 / -redis 地址；compose 内仅 127.0.0.1 暴露"
	case strings.Contains(low, "tls"):
		out.Kind = RedisErrTLS
		out.Hint = "TLS 握手失败：redis 端开启 TLS 但 client 未启用，或证书不匹配"
	case strings.Contains(low, "eof"), strings.Contains(low, "broken pipe"):
		out.Kind = RedisErrUnreachable
		out.Hint = "连接被中断：可能 redis 进程刚崩溃或 protected-mode 拒绝（缺密码）"
	}
	return out
}

