package gateway

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/MiChongs/QQMusicApi/gateway/internal/billing"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing/payment"
	billingv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/billing/v1"
)

// BillingGRPCService 把 *billing.Service + 套餐目录 + 支付 Registry 适配为
// gRPC BillingService 实现。
//
// 设计要点：
//   - PlanCatalog：套餐目录，由本 adapter 持有（生产应接 PG）；ListPlans / Subscribe
//     需要查它
//   - InvoiceStore：发票尚未实现持久化层，Invoice list 当前返回空（PaymentCallback
//     成功 → fulfill 触发；invoice 由后续 worker 生成）
//   - PaymentCallback：拿到 raw_body 后按 provider 名 dispatch；适配器解析签名/金额
//     验证后通过 billing.Service.Transition 推进状态
type BillingGRPCService struct {
	billingv1.UnimplementedBillingServiceServer
	svc      *billing.Service
	catalog  *PlanCatalog
	payments *payment.Registry
	currency *billing.CurrencyStore // 可选：跨币种渠道（如 ldcpay）需要的换算
}

// NewBillingGRPCService 构造 BillingGRPCService。
//
// currency 允许 nil（兼容老调用 / 测试）；为 nil 时跨币种支付会走原币种，
// 并在订单上不留 FX 快照。生产路径应始终注入。
func NewBillingGRPCService(svc *billing.Service, catalog *PlanCatalog, payments *payment.Registry, currency *billing.CurrencyStore) *BillingGRPCService {
	return &BillingGRPCService{svc: svc, catalog: catalog, payments: payments, currency: currency}
}

// providerSettlementCurrency 返回支付渠道实际结算的币种。
//
// 默认假设渠道按订单 Currency 结算（人民币系），但 ldcpay 必须用平台 token "LDC"。
// 后续接入更多跨币种渠道（PayPal USD / Stripe USD 等）时在此扩展。
var providerSettlementCurrency = map[string]string{
	"ldcpay": "LDC",
}

// resolveTargetCurrency 给定渠道返回应换算到的目标币；返回空串表示无需换算。
func resolveTargetCurrency(provider string) string {
	return providerSettlementCurrency[provider]
}

// newSubscribeIdemKey 为一次 Subscribe 调用生成全局唯一 idempotency key。
//
// 格式: "<userID>|<planCode>|<provider>|<8B-hex-nonce>"。前三字段做审计/分析用，
// 末尾 nonce 保证不同次调用一定生成不同 key —— 这是修复 LDC duplicate-key 的关键：
// CreateOrder 内部按 idem dedup，老 idem 复用会导致老 order.ID 被再次提交给 LDC。
func newSubscribeIdemKey(userID, planCode, provider string) (string, error) {
	var b [8]byte
	if _, err := io.ReadFull(cryptorand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("subscribe idem nonce: %w", err)
	}
	return fmt.Sprintf("%s|%s|%s|%x", userID, planCode, provider, b[:]), nil
}

// ListPlans 实现 billingv1.BillingServiceServer.ListPlans。
func (s *BillingGRPCService) ListPlans(_ context.Context, _ *billingv1.ListPlansRequest) (*billingv1.PlanList, error) {
	plans := s.catalog.List()
	out := &billingv1.PlanList{Items: make([]*billingv1.Plan, 0, len(plans))}
	for _, p := range plans {
		out.Items = append(out.Items, planToProto(p))
	}
	return out, nil
}

// Subscribe 实现 billingv1.BillingServiceServer.Subscribe。
//
// 逻辑：(1) 查 plan → (2) CreateOrder → (3) 调 payment.CreatePayment 拿 PayURL
// → (4) 状态 Pending→Paying → (5) 返回 redirect_url。
func (s *BillingGRPCService) Subscribe(ctx context.Context, req *billingv1.SubscribeRequest) (*billingv1.SubscribeResponse, error) {
	if req.GetPlanCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "plan_code required")
	}
	if req.GetPaymentProvider() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_provider required")
	}
	plan, ok := s.catalog.GetByCode(req.GetPlanCode())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "plan not found: %s", req.GetPlanCode())
	}
	prov, err := s.payments.Get(req.GetPaymentProvider())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	userID := userIDFromCtx(ctx) // 默认 anonymous（v0.4 接 SessionMiddleware）
	// 每次 Subscribe 生成全新 idem nonce —— 之前用 "userID|plan|YYYYMMDDHHMM" 会让
	// 同一分钟内重复点击命中 CreateOrder 的 dedup 路径，老订单 ID 又被重新提交给
	// LDC，触发 LDC 端唯一约束 idx_orders_client_merchant_order 报错。
	idem, err := newSubscribeIdemKey(userID, plan.Code, req.GetPaymentProvider())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	order, err := billing.NewOrder(userID, plan.ID,
		decimal.NewFromInt(plan.PriceCents).Div(decimal.NewFromInt(100)),
		plan.Currency, idem, 30*time.Minute)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	order.PaymentProvider = req.GetPaymentProvider()
	// 跨币种渠道：把 Amount/Currency 换算到渠道结算币（如 ldcpay → LDC），
	// 同时把 plan 原币种 + 金额留作 FX 快照。CreateOrder 之前完成换算，
	// 让 PG 落库的 Amount 已经是结算币。
	if target := resolveTargetCurrency(req.GetPaymentProvider()); target != "" && s.currency != nil {
		if err := s.currency.ApplyCurrency(order, target); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "currency: %v", err)
		}
	}
	saved, err := s.svc.CreateOrder(ctx, order)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// 防御：CreateOrder 在 idem 命中时会返回老订单。新生成的 idem 理论上不会命中,
	// 但仍兜底 —— 老订单非 PENDING 时再喂给 LDC 必定 duplicate-key。
	order = saved
	if order.Status != billing.OrderPending {
		return nil, status.Errorf(codes.AlreadyExists,
			"order already exists (id=%s, status=%s)", order.ID, order.Status)
	}
	if _, err := s.svc.Transition(ctx, order.ID, billing.EvtPayStart); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	resp, err := prov.CreatePayment(ctx, payment.CreateRequest{
		OrderID:   order.ID,
		Amount:    order.Amount,
		Currency:  order.Currency,
		Subject:   plan.Name,
		ReturnURL: req.GetSuccessUrl(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &billingv1.SubscribeResponse{
		OrderId:     order.ID,
		RedirectUrl: resp.PayURL,
	}, nil
}

// CreateOrder 实现 billingv1.BillingServiceServer.CreateOrder。
//
// 比 Subscribe 简单：不调支付网关，仅生成 PENDING 订单（适合 API 集成方自己
// 选择支付时机）。同 idempotency_key 重复返回原订单。
func (s *BillingGRPCService) CreateOrder(ctx context.Context, req *billingv1.CreateOrderRequest) (*billingv1.Order, error) {
	if req.GetPlanCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "plan_code required")
	}
	plan, ok := s.catalog.GetByCode(req.GetPlanCode())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "plan not found: %s", req.GetPlanCode())
	}
	userID := userIDFromCtx(ctx)
	o, err := billing.NewOrder(userID, plan.ID,
		decimal.NewFromInt(plan.PriceCents).Div(decimal.NewFromInt(100)),
		plan.Currency, req.GetIdempotencyKey(), 30*time.Minute)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	o.PaymentProvider = req.GetPaymentProvider()
	if target := resolveTargetCurrency(req.GetPaymentProvider()); target != "" && s.currency != nil {
		if err := s.currency.ApplyCurrency(o, target); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "currency: %v", err)
		}
	}
	o, err = s.svc.CreateOrder(ctx, o)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return orderToProto(o), nil
}

// GetOrder 实现 billingv1.BillingServiceServer.GetOrder。
func (s *BillingGRPCService) GetOrder(ctx context.Context, req *billingv1.GetOrderRequest) (*billingv1.Order, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	// billing.Service 没暴露 Get；直接 fallback 到 Transition error 不对。
	// 通过 SweepExpired 之外的路径：当前用 Transition(EvtFulfill) 尝试不可行——
	// 简单做法：在 Service 上加 GetOrder（或直接通过 repo 访问）。这里走 repo
	// 的话又破坏封装。先给 Service 加导出 GetOrder。
	o, err := s.svc.GetOrder(ctx, req.GetId())
	if err != nil {
		if errors.Is(err, billing.ErrOrderNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return orderToProto(o), nil
}

// CancelSubscription 实现：当前用户没有"订阅"概念，直接返回（v0.4 落地）。
func (s *BillingGRPCService) CancelSubscription(_ context.Context, _ *billingv1.CancelSubscriptionRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

// ListInvoices 当前返回空列表（worker 写入 invoice 后再补强）。
func (s *BillingGRPCService) ListInvoices(_ context.Context, _ *billingv1.ListInvoicesRequest) (*billingv1.InvoiceList, error) {
	return &billingv1.InvoiceList{Items: nil, Total: 0}, nil
}

// PaymentCallback 实现 billingv1.BillingServiceServer.PaymentCallback。
//
// 五步校验（Plan §8.5）：
//  1. provider 在 Registry → ✅
//  2. VerifyCallback 验签 / 提取 amount / external_txn_id → ✅
//  3. 订单存在 + status == Paying → svc.GetOrder + 状态判断
//  4. 金额匹配 → result.Amount.Equal(order.Amount)
//  5. 幂等：已有 ExternalOrderID 不重复处理（这里 Transition 本身幂等防御）
func (s *BillingGRPCService) PaymentCallback(ctx context.Context, req *billingv1.PaymentCallbackRequest) (*billingv1.PaymentCallbackResult, error) {
	prov, err := s.payments.Get(req.GetProvider())
	if err != nil {
		return &billingv1.PaymentCallbackResult{Accepted: false, Reason: err.Error()}, nil
	}
	cbResult, err := prov.VerifyCallback(ctx, payment.CallbackRequest{
		RawBody: req.GetRawBody(),
		IP:      req.GetClientIp(),
		Params:  headersToParams(req.GetHeaders()),
	})
	if err != nil {
		return &billingv1.PaymentCallbackResult{Accepted: false, Reason: err.Error()}, nil
	}
	o, err := s.svc.GetOrder(ctx, cbResult.OrderID)
	if err != nil {
		return &billingv1.PaymentCallbackResult{Accepted: false, Reason: "order_not_found"}, nil
	}
	if o.Status != billing.OrderPaying {
		return &billingv1.PaymentCallbackResult{Accepted: false, OrderId: o.ID, Reason: "order_not_in_paying"}, nil
	}
	if !cbResult.Amount.Equal(o.Amount) {
		return &billingv1.PaymentCallbackResult{Accepted: false, OrderId: o.ID, Reason: "amount_mismatch"}, nil
	}
	event := billing.EvtCallbackOK
	if !cbResult.Success {
		event = billing.EvtCallbackFail
	}
	if _, err := s.svc.Transition(ctx, o.ID, event); err != nil {
		return &billingv1.PaymentCallbackResult{Accepted: false, OrderId: o.ID, Reason: err.Error()}, nil
	}
	if event == billing.EvtCallbackOK {
		// 履约：直接推进 PAID → COMPLETED（worker 模式可后续拆出来）。
		_, _ = s.svc.Transition(ctx, o.ID, billing.EvtFulfill)
	}
	return &billingv1.PaymentCallbackResult{Accepted: true, OrderId: o.ID}, nil
}

// ---- 帮助函数 ----

func planToProto(p Plan) *billingv1.Plan {
	return &billingv1.Plan{
		Id:                 p.ID,
		Code:               p.Code,
		Name:               p.Name,
		PriceCents:         p.PriceCents,
		Currency:           p.Currency,
		Period:             p.Period,
		Qps:                int32(p.QPS),
		SoftLimitPct:       int32(p.SoftLimitPct),
		DailyLimit:         p.DailyLimit,
		MonthlyLimit:       p.MonthlyLimit,
		PayAsYouGo:         p.PayAsYouGo,
		OveragePricePer_1K: p.OveragePricePer1k,
	}
}

func orderToProto(o *billing.Order) *billingv1.Order {
	out := &billingv1.Order{
		Id:              o.ID,
		UserId:          o.UserID,
		PlanId:          o.PlanID,
		AmountCents:     o.Amount.Mul(decimal.NewFromInt(100)).IntPart(),
		Currency:        o.Currency,
		PaymentProvider: o.PaymentProvider,
		ExternalOrderId: o.ExternalOrderID,
		Status:          orderStatusToProto(o.Status),
	}
	if !o.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(o.CreatedAt)
	}
	if !o.PaidAt.IsZero() {
		out.PaidAt = timestamppb.New(o.PaidAt)
	}
	if !o.ExpiresAt.IsZero() {
		out.ExpiresAt = timestamppb.New(o.ExpiresAt)
	}
	return out
}

func orderStatusToProto(s billing.OrderStatus) billingv1.OrderStatus {
	switch s {
	case billing.OrderPending:
		return billingv1.OrderStatus_ORDER_STATUS_PENDING
	case billing.OrderPaying:
		return billingv1.OrderStatus_ORDER_STATUS_PAYING
	case billing.OrderPaid:
		return billingv1.OrderStatus_ORDER_STATUS_PAID
	case billing.OrderCompleted:
		return billingv1.OrderStatus_ORDER_STATUS_COMPLETED
	case billing.OrderFailed:
		return billingv1.OrderStatus_ORDER_STATUS_FAILED
	case billing.OrderExpired:
		return billingv1.OrderStatus_ORDER_STATUS_EXPIRED
	case billing.OrderCanceled:
		return billingv1.OrderStatus_ORDER_STATUS_CANCELED
	default:
		return billingv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

func headersToParams(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

// userIDFromCtx 从 grpc metadata 的 X-User-Id 取登录用户；无则 anonymous。
//
// SessionMiddleware（M34）会写 X-User-Id 到 HTTP header；grpc-gateway 自动转
// 成 grpc metadata。生产应在此处校验未登录用户禁止下单。
func userIDFromCtx(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "anonymous"
	}
	for _, key := range []string{"x-user-id", "grpcgateway-x-user-id"} {
		if vs := md.Get(key); len(vs) > 0 && vs[0] != "" {
			return vs[0]
		}
	}
	return "anonymous"
}

// ---- 套餐目录 ----

// Plan 是 PlanCatalog 中的套餐定义。
type Plan struct {
	ID                 string
	Code               string
	Name               string
	PriceCents         int64
	Currency           string
	Period             string // monthly / yearly
	QPS                int
	SoftLimitPct       int
	DailyLimit         int64
	MonthlyLimit       int64
	PayAsYouGo         bool  // 是否支持按量计费
	OveragePricePer1k  int64 // 超额每千次价格（分）
}

// PlanCatalog 持有套餐目录。
type PlanCatalog struct {
	mu    sync.RWMutex
	store map[string]Plan // code → Plan
	order []string
}

// NewDefaultPlanCatalog 给 v0.3 起步用：free / pro / enterprise 三档。
//
// 价格 / qps / soft_pct 与 Plan §6.2 example 一致。
func NewDefaultPlanCatalog() *PlanCatalog {
	c := &PlanCatalog{store: map[string]Plan{}}
	for _, p := range []Plan{
		{ID: "plan_free", Code: "free", Name: "Free", PriceCents: 0, Currency: "CNY", Period: "monthly",
			QPS: 1, SoftLimitPct: 80, DailyLimit: 1000, MonthlyLimit: 30000,
			PayAsYouGo: false, OveragePricePer1k: 0},
		{ID: "plan_pro", Code: "pro", Name: "Pro", PriceCents: 9900, Currency: "CNY", Period: "monthly",
			QPS: 10, SoftLimitPct: 80, DailyLimit: 10000, MonthlyLimit: 300000,
			PayAsYouGo: true, OveragePricePer1k: 100}, // 超额 ¥1.00/千次
		{ID: "plan_ent", Code: "enterprise", Name: "Enterprise", PriceCents: 99900, Currency: "CNY", Period: "monthly",
			QPS: 100, SoftLimitPct: 90, DailyLimit: 100000, MonthlyLimit: 3000000,
			PayAsYouGo: true, OveragePricePer1k: 50}, // 超额 ¥0.50/千次
	} {
		c.store[p.Code] = p
		c.order = append(c.order, p.Code)
	}
	return c
}

// List 列出所有套餐（按注册顺序）。
func (c *PlanCatalog) List() []Plan {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Plan, 0, len(c.order))
	for _, code := range c.order {
		out = append(out, c.store[code])
	}
	return out
}

// GetByCode 按 code 查套餐。
func (c *PlanCatalog) GetByCode(code string) (Plan, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.store[code]
	return p, ok
}
