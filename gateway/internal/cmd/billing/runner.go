// Package billing 是 Billing Service 的 wiring runner（M39 接入真实业务）。
//
// **配置优先级**：CLI flag > MGW_BILLING_<KEY> > MGW_<KEY> > legacy env > default
//
// 架构关系：
//   - 业务实现：internal/billing.Service（订单 FSM + idempotency）
//   - 支付 adapter：internal/billing/payment.Registry（mock / yipay / hupijiao）
//   - gRPC adapter：internal/gateway.BillingGRPCService
package billing

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"

	"github.com/MiChongs/QQMusicApi/gateway/internal/billing"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing/payment"
	cmdpkg "github.com/MiChongs/QQMusicApi/gateway/internal/cmd"
	"github.com/MiChongs/QQMusicApi/gateway/internal/gateway"
	billingv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/billing/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/observability"
)

// Version 由 ldflags 注入。
var Version = "v0.3.0-dev"

// Run 启动 Billing Service。
func Run(ctx context.Context, args []string) error {
	l := cmdpkg.NewLoader("billing")
	l.String("grpc", ":9005", "gRPC listen address")
	l.String("yipay-api", "", "易支付 API 基址（空 = 不启用）")
	l.String("yipay-pid", "", "易支付商户 PID")
	l.String("yipay-key", "", "易支付商户密钥")
	l.String("hupijiao-api", "", "虎皮椒 API 基址（空 = 不启用）")
	l.String("hupijiao-appid", "", "虎皮椒 appid")
	l.String("hupijiao-secret", "", "虎皮椒 appsecret")
	l.String("tls-cert", "", "PEM cert (mTLS if set)")
	l.String("tls-key", "", "PEM key")
	l.String("tls-client-ca", "", "PEM client CA bundle")
	l.Bool("version", false, "print version and exit")
	if stop, err := l.Parse(args); err != nil {
		return err
	} else if stop {
		return nil
	}
	if l.GetBool("version") {
		fmt.Println(Version)
		return nil
	}

	grpcAddr := l.GetString("grpc")
	yipayAPI, yipayPID, yipayKey := l.GetString("yipay-api"), l.GetString("yipay-pid"), l.GetString("yipay-key")
	hpAPI, hpAppID, hpSecret := l.GetString("hupijiao-api"), l.GetString("hupijiao-appid"), l.GetString("hupijiao-secret")

	logger := observability.NewLogger("billing-service", slog.LevelInfo)
	logger.Info("billing service starting", "version", Version, "grpc", grpcAddr)

	// ---- 业务 ----
	repo := billing.NewMemRepo()
	svc := billing.NewService(repo, billing.Options{})

	// ---- 套餐目录 ----
	catalog := gateway.NewDefaultPlanCatalog()

	// ---- Payment Registry ----
	payments := payment.NewRegistry()
	// mock provider 永远在线（开发友好；生产部署时显式 -no-mock）。
	if err := payments.Register(payment.NewMock()); err != nil {
		return fmt.Errorf("register mock payment: %w", err)
	}
	if yipayAPI != "" && yipayPID != "" && yipayKey != "" {
		if err := payments.Register(payment.NewYipay(payment.YipayConfig{
			BaseURL: yipayAPI, MerchantID: yipayPID, Key: yipayKey,
		})); err != nil {
			return fmt.Errorf("register yipay: %w", err)
		}
		logger.Info("payment provider: yipay registered", "pid", yipayPID)
	}
	if hpAPI != "" && hpAppID != "" && hpSecret != "" {
		if err := payments.Register(payment.NewHupijiao(payment.HupijiaoConfig{
			BaseURL: hpAPI, AppID: hpAppID, Key: hpSecret,
		})); err != nil {
			return fmt.Errorf("register hupijiao: %w", err)
		}
		logger.Info("payment provider: hupijiao registered", "appid", hpAppID)
	}

	// ---- mTLS ----
	var mtls *observability.MTLSConfig
	if cert := l.GetString("tls-cert"); cert != "" {
		mtls = &observability.MTLSConfig{
			CertFile:     cert,
			KeyFile:      l.GetString("tls-key"),
			ClientCAFile: l.GetString("tls-client-ca"),
		}
	}

	if err := gateway.RunStandalone(ctx, gateway.StandaloneConfig{
		GRPCAddr: grpcAddr,
		MTLS:     mtls,
		Logger:   logger,
		Register: func(s *grpc.Server) {
			billingv1.RegisterBillingServiceServer(s, gateway.NewBillingGRPCService(svc, catalog, payments, nil))
		},
	}); err != nil {
		return fmt.Errorf("billing service: %w", err)
	}
	logger.Info("billing service shutdown complete")
	return nil
}
