// Package gateway 是 Edge Gateway 的入口装配。
//
// 双协议栈：
//   - 内部 gRPC：监听 cfg.GRPCAddr（默认 :9000），承载所有内部 RPC
//   - 外部 REST：监听 cfg.HTTPAddr（默认 :8080），由 grpc-gateway 自动从
//     .proto annotation 生成路由，转发到本机 gRPC server
//   - REST 端额外通过 gin 中间件注入 auth / quota / csrf 等横切关注点
//
// 当前是 M4 骨架；M16 时把所有 service 通过 RegisterFunc 注入。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Config 是 Gateway 启动配置。
type Config struct {
	GRPCAddr      string        // 内部 gRPC 监听地址，默认 ":9000"
	HTTPAddr      string        // REST 监听地址，默认 ":8080"
	ShutdownGrace time.Duration // 优雅关停超时，默认 15s
	GinMode       string        // gin.ReleaseMode / DebugMode；默认 release

	// DisableSecurityHeaders=true 时不挂 SecurityHeaders middleware（仅测试用）。
	DisableSecurityHeaders bool
	SecurityHeaders        *SecurityHeadersOptions

	// DisableCSRF=true 关闭 CSRF（默认开；测试可关）。
	DisableCSRF bool
	CSRFOptions *CSRFOptions

	// SessionMiddleware（可选）：非 nil 时挂载 session→user 解析。
	// 设置 SkipPaths 应至少包含 /v1/auth/login / /v1/auth/register 等无凭据路径。
	SessionMiddleware *SessionMiddlewareOptions

	// AdminAuth（可选）：非 nil 时为 /v1/admin/* 强制 token 校验。
	AdminAuth *AdminAuthOptions

	// AuthCookie 控制登录后是否由后端自动签发 sid HttpOnly Cookie。
	AuthCookie *AuthCookieOptions

	// UsageRecorder 可选：非 nil 时自动记录每个 /v1/ 请求的 RPM/TPM。
	UsageRecorder *UsageHandler

	// KnownProviders 是合法 provider 名（如 ["qqmusic", "netease"]）。
	// UsageRecorder 启用时，只有路径首段命中此列表才记录用量；空列表 = 关闭录制
	// （防御性默认，避免无意中把 "v1"/"api" 等噪声写进 usage_aggregates）。
	KnownProviders []string
}

// Defaults 填充未设置字段。
func (c *Config) Defaults() {
	if c.GRPCAddr == "" {
		c.GRPCAddr = ":9000"
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
	}
	if c.ShutdownGrace == 0 {
		c.ShutdownGrace = 15 * time.Second
	}
	if c.GinMode == "" {
		c.GinMode = gin.ReleaseMode
	}
}

// RegisterGRPC 注入 gRPC service 实现到 gRPC server。
type RegisterGRPC func(s *grpc.Server)

// RegisterGateway 注入 grpc-gateway HTTP handler 到 ServeMux。
//
// 调用方通常这样写：
//
//	musicv1.RegisterMusicServiceHandler(ctx, mux, conn)
type RegisterGateway func(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error

// Server 是双协议 Server 的复合体。
type Server struct {
	cfg          Config
	grpcServer   *grpc.Server
	httpServer   *http.Server
	registerHTTP RegisterGateway
}

// NewServer 构造 Server。
//
// regGRPC 注册到内部 gRPC server；regGW 注册到 grpc-gateway ServeMux（在 Start
// 时按相同 process 内 dial）。
func NewServer(cfg Config, regGRPC RegisterGRPC, regGW RegisterGateway) *Server {
	cfg.Defaults()
	gin.SetMode(cfg.GinMode)

	gs := grpc.NewServer()
	if regGRPC != nil {
		regGRPC(gs)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	if !cfg.DisableSecurityHeaders {
		opts := DefaultSecurityHeadersOptions()
		if cfg.SecurityHeaders != nil {
			opts = *cfg.SecurityHeaders
		}
		r.Use(SecurityHeaders(opts))
	}
	if !cfg.DisableCSRF {
		opts := DefaultCSRFOptions()
		if cfg.CSRFOptions != nil {
			opts = *cfg.CSRFOptions
		}
		r.Use(CSRF(opts))
	}
	if cfg.SessionMiddleware != nil {
		r.Use(SessionMiddleware(*cfg.SessionMiddleware))
	}
	if cfg.AdminAuth != nil {
		r.Use(AdminAuthMiddleware(*cfg.AdminAuth))
	}
	if cfg.UsageRecorder != nil {
		recorder := cfg.UsageRecorder
		// 把 known providers 收敛成 set 一次构造；空 set 时下面跳过录制。
		// 这套白名单是 usage_aggregates 数据质量的最后一道闸——之前没有它，
		// /v1/v1/... 这类异常路径会被误记成 provider="v1"。
		known := make(map[string]struct{}, len(cfg.KnownProviders))
		for _, n := range cfg.KnownProviders {
			if n != "" {
				known[n] = struct{}{}
			}
		}
		r.Use(func(c *gin.Context) {
			start := time.Now()
			c.Next()
			if len(known) == 0 {
				return
			}
			p := c.Request.URL.Path
			// 只记录 MusicService 端点，排除 admin/auth/billing/usage 等管理面
			if !strings.HasPrefix(p, "/v1/") ||
				strings.HasPrefix(p, "/v1/admin/") ||
				strings.HasPrefix(p, "/v1/auth/") ||
				strings.HasPrefix(p, "/v1/billing/") ||
				strings.HasPrefix(p, "/v1/usage/") ||
				strings.HasPrefix(p, "/v1/config/") ||
				strings.HasPrefix(p, "/v1/docs/") {
				return
			}
			// 从路径提取 provider: /v1/{provider}/...
			parts := strings.SplitN(strings.TrimPrefix(p, "/v1/"), "/", 2)
			if len(parts) == 0 || parts[0] == "" {
				return
			}
			prov := parts[0]
			if _, ok := known[prov]; !ok {
				// 未注册 provider（例如路径错配 / 老调用）→ 静默丢弃，
				// 不污染 usage_aggregates。
				return
			}
			userID := c.Request.Header.Get("X-User-Id")
			latency := time.Since(start).Milliseconds()
			recorder.RecordRequest(userID, prov, 1, latency)
			if c.Writer.Status() >= 400 {
				recorder.RecordError()
			}
		})
	}
	{
		// Cookie 写入由 ForwardResponseOption 处理；登出清理由 gin middleware 兜底。
		ack := DefaultAuthCookieOptions()
		if cfg.AuthCookie != nil {
			ack = *cfg.AuthCookie
		}
		if !ack.Disabled {
			r.Use(AuthCookieClearMiddleware(ack))
		}
	}
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/readyz", func(c *gin.Context) { c.String(http.StatusOK, "ready") })

	hs := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return &Server{
		cfg: cfg, grpcServer: gs, httpServer: hs,
		registerHTTP: regGW,
	}
}

// Start 启动 gRPC + HTTP；阻塞直到 ctx 取消或任一 server 失败。
func (s *Server) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("gateway.Listen gRPC: %w", err)
	}
	errCh := make(chan error, 2)

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()

	// 同进程 dial：让 grpc-gateway 把 REST 转回本机 gRPC server。
	if s.registerHTTP != nil {
		dialTarget := "passthrough:///" + s.cfg.GRPCAddr
		slog.Info("[gateway] grpc-gateway dialing self", "target", dialTarget)
		conn, err := grpc.NewClient(dialTarget,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			slog.Error("[gateway] grpc dial failed", "target", dialTarget, "err", err)
			_ = lis.Close()
			return fmt.Errorf("gateway dial self: %w", err)
		}
		slog.Info("[gateway] grpc dial ok", "target", dialTarget)

		muxOpts := []runtime.ServeMuxOption{}
		ack := DefaultAuthCookieOptions()
		if s.cfg.AuthCookie != nil {
			ack = *s.cfg.AuthCookie
		}
		if !ack.Disabled {
			muxOpts = append(muxOpts, runtime.WithForwardResponseOption(AuthCookieResponseWriter(ack)))
		}
		mux := runtime.NewServeMux(muxOpts...)
		if err := s.registerHTTP(ctx, mux, conn); err != nil {
			slog.Error("[gateway] register handlers failed", "err", err)
			_ = lis.Close()
			return fmt.Errorf("gateway register handlers: %w", err)
		}
		slog.Info("[gateway] grpc-gateway handlers registered")

		eng := s.httpServer.Handler.(*gin.Engine)
		eng.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path
			method := c.Request.Method
			if strings.HasPrefix(path, "/v1/") {
				slog.Info("[gateway] NoRoute → grpc-gateway", "method", method, "path", path)
				c.Writer.WriteHeader(http.StatusOK)
				mux.ServeHTTP(c.Writer, c.Request)
				slog.Info("[gateway] grpc-gateway done", "path", path, "status", c.Writer.Status())
				return
			}
			slog.Info("[gateway] NoRoute → 404", "path", path)
			Fail(c, http.StatusNotFound, CodeNotFound, "not found")
		})
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		_ = s.shutdown()
		return err
	}
}

// shutdown 优雅关停内部使用。
func (s *Server) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace)
	defer cancel()
	s.grpcServer.GracefulStop()
	return s.httpServer.Shutdown(ctx)
}

// HTTPAddr 返回当前 HTTP 监听地址（测试便利）。
func (s *Server) HTTPAddr() string { return s.cfg.HTTPAddr }

// GRPCAddr 返回当前 gRPC 监听地址（测试便利）。
func (s *Server) GRPCAddr() string { return s.cfg.GRPCAddr }

// Engine 暴露 gin Engine，调用方可以追加自定义路由 / middleware。
func (s *Server) Engine() *gin.Engine {
	return s.httpServer.Handler.(*gin.Engine)
}
