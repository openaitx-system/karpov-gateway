// qqmusic-gateway 是统一入口：一个进程并发拉起全部 service。
//
// 默认行为：直接 `./qqmusic-gateway` → gateway + auth + music + pool + worker
// 同进程并发启动；任一退出 / SIGINT / SIGTERM 全部一起退出。
//
// 默认监听端口（确保不冲突）：
//
//	gateway   HTTP :8080  +  gRPC :9000  （承载 /v1/* REST 路由）
//	auth      gRPC :9001
//	music     gRPC :9002
//	pool      gRPC :9003
//	quota     gRPC :9004
//	billing   gRPC :9005
//	worker    无监听端口（凭据健康探测后台）
//
// 选项（CLI flag / 环境变量；优先级 CLI > MGW_QQMUSIC_GATEWAY_<K> > MGW_<K> > legacy > default）：
//
//	-redis            127.0.0.1:6379    sessions / lease registry 共享 Redis
//	-redis-password   ""                Redis 密码；兜底读 $REDIS_PASSWORD
//	-without          worker,billing    用逗号分隔的服务名禁用启动；默认全部启动
//	-version                            打印版本退出
//
// 各子 service 也支持各自的 MGW_<SVC>_<KEY> env，例如 MGW_AUTH_REDIS_PASSWORD。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	auth "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/auth"
	billing "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/billing"
	gw "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/gateway"
	music "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/music"
	pool "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/pool"
	quota "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/quota"
	worker "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/worker"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
)

// Version 由 ldflags 注入。
var Version = "v0.3.0-dev"

// service 是一个并发启动单元。
type service struct {
	name string
	args []string
	run  cmdpkg.Runner
}

func main() {
	l := cmdpkg.NewLoader("qqmusic-gateway")
	l.String("redis", "127.0.0.1:6379", "Redis address (sessions / lease registry)")
	l.String("redis-password", "", "Redis password")
	l.String("without", "", "comma-separated services to skip (e.g. \"billing,quota\")")
	l.Bool("version", false, "print version and exit")
	l.LegacyEnv("redis-password", "REDIS_PASSWORD")
	if stop, err := l.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	} else if stop {
		return
	}
	if l.GetBool("version") {
		fmt.Println(Version)
		return
	}

	redisAddr := l.GetString("redis")
	redisPassword := l.GetString("redis-password")
	skip := parseSkip(l.GetString("without"))

	logger := observability.NewLogger("qqmusic-gateway", slog.LevelInfo)

	// 让所有子 runner 也能拾取密码：通过 MGW_REDIS / MGW_REDIS_PASSWORD 注入到
	// 进程级 env，子 runner 的 viper 自动从这里兜底读。
	_ = os.Setenv("MGW_REDIS", redisAddr)
	if redisPassword != "" {
		_ = os.Setenv("MGW_REDIS_PASSWORD", redisPassword)
	}

	// v0.3 in-memory 阶段：每个 wiring runner 各自持有独立 MemUserRepo（数据不共享），
	// 因此 auth 与 gateway 两个子 service 都跑 Bootstrap 会创建两份不同的 superadmin。
	// 用户实际登录走 edge gateway 进程内本地 authSvc，所以让 auth 子 service 在一键模式
	// 下显式禁用 bootstrap，仅由 gateway 触发；cmd/auth 单跑时仍按默认开启。
	// v0.4 全部 service 共享 PG repo 后该问题自然消失，可恢复对称配置。
	all := []service{
		{"pool", []string{"-grpc", ":9003"}, pool.Run},
		{"auth", []string{"-grpc", ":9001", "-bootstrap-disable=true"}, auth.Run},
		{"music", []string{"-grpc", ":9002", "-pool-grpc", "127.0.0.1:9003"}, music.Run},
		{"quota", []string{"-grpc", ":9004"}, quota.Run},
		{"billing", []string{"-grpc", ":9005"}, billing.Run},
		{"gateway", []string{}, gw.Run},
		{"worker", []string{}, worker.Run},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("qqmusic-gateway starting (multi-service all-in-one)",
		"version", Version, "redis", redisAddr, "redis_auth", redisPassword != "",
		"skip", strings.Join(skip, ","))

	eg, egCtx := errgroup.WithContext(ctx)
	for _, s := range all {
		if contains(skip, s.name) {
			logger.Info("skip service", "name", s.name)
			continue
		}
		s := s // capture
		eg.Go(func() error {
			logger.Info("service starting", "name", s.name)
			err := s.run(egCtx, s.args)
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("service exited with error", "name", s.name, "err", err)
				return fmt.Errorf("%s: %w", s.name, err)
			}
			logger.Info("service stopped", "name", s.name)
			return nil
		})
	}

	if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("qqmusic-gateway exited with error", "err", err)
		os.Exit(1)
	}
	logger.Info("qqmusic-gateway shutdown complete")
}

func parseSkip(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
