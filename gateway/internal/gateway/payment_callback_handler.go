package gateway

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing/payment"
)

// PaymentCallbackHandler 处理支付网关的异步回调。
// 这些端点绕过 Session/CSRF 认证（支付网关直接 POST）。
type PaymentCallbackHandler struct {
	billingSvc  *billing.Service
	authSvc     *auth.Service
	payRegistry *payment.Registry
	planRepo    *PlanRepo
	baseURL     string

	// balanceHandler 可选：非 nil 时支付成功的"充值订单"会调它的 FulfillTopup 入账。
	balanceHandler *BalanceHandler
	// currency 可选：跨币种渠道（如 ldcpay）下单前的换算。
	currency *billing.CurrencyStore
}

func NewPaymentCallbackHandler(billingSvc *billing.Service, authSvc *auth.Service, payReg *payment.Registry, planRepo *PlanRepo, baseURL string) *PaymentCallbackHandler {
	return &PaymentCallbackHandler{
		billingSvc:  billingSvc,
		authSvc:     authSvc,
		payRegistry: payReg,
		planRepo:    planRepo,
		baseURL:     baseURL,
	}
}

// SetBalanceHandler 注入 BalanceHandler 以支持充值订单履约。
func (h *PaymentCallbackHandler) SetBalanceHandler(b *BalanceHandler) { h.balanceHandler = b }

// SetCurrencyStore 注入 CurrencyStore；createOrder 用它把 CNY 套餐换算到渠道结算币。
func (h *PaymentCallbackHandler) SetCurrencyStore(s *billing.CurrencyStore) { h.currency = s }

func (h *PaymentCallbackHandler) Mount(e *gin.Engine) {
	// 回调端点（GET + POST，因不同支付协议方法不同）
	e.POST("/v1/billing/callback/yipay", h.yipayCallback)
	e.GET("/v1/billing/callback/yipay", h.yipayCallback)
	e.POST("/v1/billing/callback/hupijiao", h.hupijiaoCallback)
	// LDC 文档 §3.3 异步通知是 GET，但同时接受 POST 以防方向调整
	e.GET("/v1/billing/callback/ldcpay", h.ldcCallback)
	e.POST("/v1/billing/callback/ldcpay", h.ldcCallback)

	// 订单管理端点
	e.POST("/v1/billing/orders", h.createOrder)
	e.GET("/v1/billing/orders/:id", h.getOrder)
	e.GET("/v1/billing/orders", h.listOrders)
	e.GET("/v1/billing/plans", h.listPlans)
	// 退款（仅管理员）
	e.POST("/v1/billing/orders/:id/refund", h.refundOrder)
	// 套餐管理（管理员）
	e.PUT("/v1/billing/plans/:id", h.upsertPlan)
	e.DELETE("/v1/billing/plans/:id", h.deletePlan)
}

// NotifyURL 构造给支付网关的回调地址。
func (h *PaymentCallbackHandler) NotifyURL(provider string) string {
	base := strings.TrimRight(h.baseURL, "/")
	return base + "/v1/billing/callback/" + provider
}

func (h *PaymentCallbackHandler) yipayCallback(c *gin.Context) {
	params := extractParams(c)
	rawBody, _ := io.ReadAll(c.Request.Body)
	slog.Info("[payment] yipay callback received", "params", params, "ip", c.ClientIP())

	prov, _ := h.payRegistry.Get("yipay")
	if prov == nil {
		slog.Error("[payment] yipay provider not registered")
		c.String(http.StatusOK, "fail")
		return
	}

	result, err := prov.VerifyCallback(c.Request.Context(), payment.CallbackRequest{
		Params:  params,
		RawBody: rawBody,
		IP:      c.ClientIP(),
	})
	if err != nil {
		slog.Error("[payment] yipay verify failed", "err", err, "params", params)
		c.String(http.StatusOK, "fail")
		return
	}

	if err := h.processCallback(c, result); err != nil {
		slog.Error("[payment] yipay process failed", "err", err, "orderID", result.OrderID)
		c.String(http.StatusOK, "fail")
		return
	}

	// 必须回 "success"，否则网关会持续重试
	c.String(http.StatusOK, result.GatewayResponse)
}

func (h *PaymentCallbackHandler) hupijiaoCallback(c *gin.Context) {
	params := extractParams(c)
	rawBody, _ := io.ReadAll(c.Request.Body)
	slog.Info("[payment] hupijiao callback received", "params", params, "ip", c.ClientIP())

	prov, _ := h.payRegistry.Get("hupijiao")
	if prov == nil {
		slog.Error("[payment] hupijiao provider not registered")
		c.String(http.StatusOK, "fail")
		return
	}

	result, err := prov.VerifyCallback(c.Request.Context(), payment.CallbackRequest{
		Params:  params,
		RawBody: rawBody,
		IP:      c.ClientIP(),
	})
	if err != nil {
		slog.Error("[payment] hupijiao verify failed", "err", err, "params", params)
		c.String(http.StatusOK, "fail")
		return
	}

	if err := h.processCallback(c, result); err != nil {
		slog.Error("[payment] hupijiao process failed", "err", err, "orderID", result.OrderID)
		c.String(http.StatusOK, "fail")
		return
	}

	c.String(http.StatusOK, result.GatewayResponse)
}

// ldcCallback 处理 Linux Credit (linux.do) 异步通知。
// LDC 文档 §3.3：HTTP GET，必须返回 "success"（大小写不敏感），否则会重试 5 次。
func (h *PaymentCallbackHandler) ldcCallback(c *gin.Context) {
	params := extractParams(c)
	rawBody, _ := io.ReadAll(c.Request.Body)
	slog.Info("[payment] ldcpay callback received", "params", params, "ip", c.ClientIP())

	prov, _ := h.payRegistry.Get("ldcpay")
	if prov == nil {
		slog.Error("[payment] ldcpay provider not registered")
		c.String(http.StatusOK, "fail")
		return
	}

	result, err := prov.VerifyCallback(c.Request.Context(), payment.CallbackRequest{
		Params:  params,
		RawBody: rawBody,
		IP:      c.ClientIP(),
	})
	if err != nil {
		slog.Error("[payment] ldcpay verify failed", "err", err, "params", params)
		c.String(http.StatusOK, "fail")
		return
	}

	if err := h.processCallback(c, result); err != nil {
		slog.Error("[payment] ldcpay process failed", "err", err, "orderID", result.OrderID)
		c.String(http.StatusOK, "fail")
		return
	}

	c.String(http.StatusOK, result.GatewayResponse)
}

func (h *PaymentCallbackHandler) processCallback(c *gin.Context, result *payment.CallbackResult) error {
	if !result.Success {
		// 支付失败，转状态
		_, _ = h.billingSvc.Transition(c.Request.Context(), result.OrderID, billing.EvtCallbackFail)
		return nil
	}

	// 校验订单存在且状态正确
	order, err := h.billingSvc.GetOrder(c.Request.Context(), result.OrderID)
	if err != nil {
		return fmt.Errorf("order not found: %s", result.OrderID)
	}
	if order.Status != billing.OrderPaying && order.Status != billing.OrderPending {
		// 已处理过（幂等），直接成功
		return nil
	}

	// 金额校验
	if !result.Amount.Equal(order.Amount) {
		slog.Error("[payment] amount mismatch",
			"order", order.ID, "expected", order.Amount.String(), "got", result.Amount.String())
		return fmt.Errorf("amount mismatch: expected %s got %s", order.Amount.String(), result.Amount.String())
	}

	// 状态推进：paying → paid → completed
	if order.Status == billing.OrderPending {
		_, _ = h.billingSvc.Transition(c.Request.Context(), order.ID, billing.EvtPayStart)
	}
	_, err = h.billingSvc.Transition(c.Request.Context(), order.ID, billing.EvtCallbackOK)
	if err != nil {
		return err
	}
	// 拿到推进后的最新对象（含 PaidAt）
	if updated, gerr := h.billingSvc.GetOrder(c.Request.Context(), order.ID); gerr == nil {
		order = updated
	}
	_, err = h.billingSvc.Transition(c.Request.Context(), order.ID, billing.EvtFulfill)
	if err != nil {
		return err
	}
	if updated, gerr := h.billingSvc.GetOrder(c.Request.Context(), order.ID); gerr == nil {
		order = updated
	}

	// 按 Purpose 分支履约
	purpose := order.Purpose
	if purpose == "" {
		purpose = billing.OrderPurposeSubscription
	}
	switch purpose {
	case billing.OrderPurposeTopup:
		// 充值订单 → 钱包入账
		if h.balanceHandler != nil {
			if _, _, err := h.balanceHandler.FulfillTopup(c.Request.Context(), order); err != nil {
				slog.Error("[payment] topup fulfillment failed", "orderID", order.ID, "err", err)
				return fmt.Errorf("topup fulfill: %w", err)
			}
			slog.Info("[payment] topup credited",
				"orderID", order.ID, "userID", order.UserID,
				"amount", order.Amount.String())
		} else {
			slog.Warn("[payment] balance handler not wired; topup order paid but balance NOT credited",
				"orderID", order.ID)
		}
	default:
		// 升级用户所有 API Keys 的套餐
		if h.authSvc != nil && order.UserID != "" && order.PlanID != "" {
			h.upgradeUserKeys(c.Request.Context(), order.UserID, order.PlanID)
		}
		slog.Info("[payment] subscription order fulfilled",
			"orderID", order.ID, "txnID", result.ExternalTxnID,
			"amount", result.Amount.String(), "planID", order.PlanID)
	}
	return nil
}

type createOrderBody struct {
	PlanID          string `json:"planId" binding:"required"`
	PaymentProvider string `json:"paymentProvider" binding:"required"`
	SuccessURL      string `json:"successUrl"`
	// IdempotencyKey 可选：用于 client 安全重试同一逻辑下单（网络抖动场景）。
	// 不传则后端按 userID:planID:provider:<random> 生成全新 key，保证每次调用唯一。
	// 详见 docs：industry standard idempotency (Stripe / GitHub / AWS) — 同一 key 在 24h
	// 内复用返回相同订单；不同 key 视为独立请求。
	IdempotencyKey string `json:"idempotencyKey"`
}

// buildIdempotencyKey 校验 / 构造订单幂等键。
//
// 规则：
//   - clientKey 非空：trim 后必须 1..128 字符 ASCII 可打印（[\x21-\x7e]）。直接当作
//     idem 键使用。重复同 key 命中老订单（CreateOrder 层 dedup）。
//   - clientKey 空：返回 fmt.Sprintf("%s:%s:%s:%s", userID, planID, provider, randHex)
//     —— 有 user/plan/provider 上下文便于审计，randHex 保证全局唯一不与历史订单冲突。
//
// 这是修复 LDC duplicate-key bug 的关键：旧实现 idem=userID:planID:provider 没有 nonce，
// 第二次订阅同一 plan 时 CreateOrder 会返回**已 paid 的老订单**，再把它的 ID 喂给
// LDC → LDC 端 (client_id, merchant_order_no) 唯一约束爆炸。
func buildIdempotencyKey(clientKey, userID, planID, provider string) (string, error) {
	if k := strings.TrimSpace(clientKey); k != "" {
		if len(k) > 128 {
			return "", fmt.Errorf("idempotencyKey too long (max 128 chars, got %d)", len(k))
		}
		for i := 0; i < len(k); i++ {
			c := k[i]
			if c < 0x21 || c > 0x7e {
				return "", fmt.Errorf("idempotencyKey contains non-printable ASCII at offset %d", i)
			}
		}
		return k, nil
	}
	var b [8]byte
	if _, err := io.ReadFull(cryptorand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("idempotencyKey nonce: %w", err)
	}
	return fmt.Sprintf("%s:%s:%s:%x", userID, planID, provider, b[:]), nil
}

func (h *PaymentCallbackHandler) createOrder(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	var body createOrderBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "planId and paymentProvider required")
		return
	}

	plan, err := h.planRepo.Get(c.Request.Context(), body.PlanID)
	if err != nil || plan == nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid plan")
		return
	}
	planName := plan.Name
	planPrice := decimal.NewFromInt(plan.PriceCents).Div(decimal.NewFromInt(100))

	idemKey, err := buildIdempotencyKey(body.IdempotencyKey, userID, body.PlanID, body.PaymentProvider)
	if err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, err.Error())
		return
	}

	order := &billing.Order{
		ID:              billing.NewOrderID(),
		UserID:          userID,
		PlanID:          body.PlanID,
		Amount:          planPrice,
		Currency:        "CNY",
		Status:          billing.OrderPending,
		PaymentProvider: body.PaymentProvider,
		IdempotencyKey:  idemKey,
	}
	if target := resolveTargetCurrency(body.PaymentProvider); target != "" && h.currency != nil {
		if err := h.currency.ApplyCurrency(order, target); err != nil {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "currency: "+err.Error())
			return
		}
	}
	order, createErr := h.billingSvc.CreateOrder(c.Request.Context(), order)
	if createErr != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, createErr.Error())
		return
	}
	// 防御：CreateOrder 在 idem 命中时会返回已存在的订单。若该订单已离开 PENDING
	// 状态（paying/paid/completed/...），说明 client 用了同一个 idempotencyKey 重放
	// 一笔早已下单的请求。再把它的 ID 喂给 LDC 一定会触发 duplicate key violation，
	// 所以这里直接 409 让 client 决定（继续支付旧订单 / 换 idempotencyKey）。
	if order.Status != billing.OrderPending {
		Fail(c, http.StatusConflict, CodeConflict,
			fmt.Sprintf("order already exists (id=%s, status=%s); 如需新下单请用新的 idempotencyKey，或继续支付该订单",
				order.ID, order.Status))
		return
	}

	prov, _ := h.payRegistry.Get(body.PaymentProvider)
	if prov == nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "unsupported payment provider: "+body.PaymentProvider)
		return
	}
	payResp, err := prov.CreatePayment(c.Request.Context(), payment.CreateRequest{
		OrderID:   order.ID,
		Amount:    order.Amount,
		Currency:  order.Currency,
		Subject:   fmt.Sprintf("Music Gateway - %s", planName),
		NotifyURL: h.NotifyURL(body.PaymentProvider),
		ReturnURL: body.SuccessURL,
		UserIP:    c.ClientIP(),
	})
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, "创建支付失败: "+err.Error())
		return
	}

	_, _ = h.billingSvc.Transition(c.Request.Context(), order.ID, billing.EvtPayStart)

	OK(c, map[string]any{
		"orderId":  order.ID,
		"payUrl":   payResp.PayURL,
		"amount":   order.Amount.StringFixed(2),
		"currency": order.Currency,
		"status":   "paying",
		"planName": planName,
	})
}

func (h *PaymentCallbackHandler) getOrder(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	order, err := h.billingSvc.GetOrder(c.Request.Context(), c.Param("id"))
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "order not found")
		return
	}
	if order.UserID != userID {
		Fail(c, http.StatusNotFound, CodeNotFound, "order not found")
		return
	}
	OK(c, orderToResponse(order))
}

func (h *PaymentCallbackHandler) listOrders(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	orders, err := h.billingSvc.ListByUser(c.Request.Context(), userID)
	if err != nil {
		OK(c, map[string]any{"items": []any{}, "total": 0})
		return
	}
	items := make([]map[string]any, 0, len(orders))
	for _, o := range orders {
		items = append(items, orderToResponse(o))
	}
	OK(c, map[string]any{"items": items, "total": len(items)})
}

func (h *PaymentCallbackHandler) listPlans(c *gin.Context) {
	plans, err := h.planRepo.ListActive(c.Request.Context())
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	OK(c, map[string]any{"plans": plans})
}

type refundBody struct {
	Reason     string `json:"reason" binding:"required"`
	RefundType string `json:"refundType"` // full / partial，默认 full
	Amount     string `json:"amount"`     // partial 时必填
}

func (h *PaymentCallbackHandler) refundOrder(c *gin.Context) {
	// 仅管理员可操作
	role, _ := c.Get("auth.role")
	if role != "admin" && role != "superadmin" {
		Fail(c, http.StatusForbidden, CodeForbidden, "admin required")
		return
	}
	orderID := c.Param("id")
	var body refundBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "reason required")
		return
	}
	order, err := h.billingSvc.GetOrder(c.Request.Context(), orderID)
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "order not found")
		return
	}
	if order.Status != billing.OrderCompleted {
		Fail(c, http.StatusBadRequest, CodeBadRequest, fmt.Sprintf("order status is %s, only completed orders can be refunded", order.Status))
		return
	}

	refundType := body.RefundType
	if refundType == "" {
		refundType = "full"
	}
	refundAmount := order.Amount
	if refundType == "partial" && body.Amount != "" {
		if amt, err := decimal.NewFromString(body.Amount); err == nil {
			refundAmount = amt
		}
	}

	// 创建退款记录并触发降级（由 DB trigger 处理）
	refundErr := h.billingSvc.CreateRefund(c.Request.Context(), orderID, refundAmount, body.Reason, refundType, authUserID(c))
	if refundErr != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, refundErr.Error())
		return
	}

	OK(c, map[string]any{
		"orderId":      orderID,
		"refundType":   refundType,
		"refundAmount": refundAmount.StringFixed(2),
		"status":       "completed",
		"message":      "退款已处理，套餐将自动降级",
	})
}

func orderToResponse(o *billing.Order) map[string]any {
	resp := map[string]any{
		"id":       o.ID,
		"userId":   o.UserID,
		"planId":   o.PlanID,
		"amount":   o.Amount.StringFixed(2),
		"currency": o.Currency,
		"status":   string(o.Status),
	}
	if !o.PaidAt.IsZero() {
		resp["paidAt"] = o.PaidAt.Format("2006-01-02T15:04:05Z")
	}
	if o.OriginalCurrency != "" && !o.OriginalAmount.IsZero() {
		resp["originalAmount"] = o.OriginalAmount.StringFixed(2)
		resp["originalCurrency"] = o.OriginalCurrency
		resp["fxRate"] = o.FXRate.StringFixed(8)
	}
	return resp
}

func (h *PaymentCallbackHandler) upgradeUserKeys(ctx context.Context, userID, planID string) {
	keys, err := h.authSvc.ListAPIKeys(ctx, userID)
	if err != nil {
		slog.Error("[payment] list keys for upgrade failed", "userID", userID, "err", err)
		return
	}
	upgraded := 0
	for _, k := range keys {
		if k.RevokedAt.IsZero() && k.PlanID != planID {
			k.PlanID = planID
			if err := h.authSvc.SetAPIKeyPlan(ctx, userID, k.ID, planID); err != nil {
				slog.Error("[payment] upgrade key plan failed", "keyID", k.ID, "err", err)
				continue
			}
			upgraded++
		}
	}
	slog.Info("[payment] upgraded user keys", "userID", userID, "planID", planID, "count", upgraded)
}

func extractParams(c *gin.Context) map[string]string {
	_ = c.Request.ParseForm()
	params := make(map[string]string)
	for k, v := range c.Request.Form {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	return params
}

func (h *PaymentCallbackHandler) upsertPlan(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	id := c.Param("id")
	var body DynamicPlan
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid plan data")
		return
	}
	body.ID = id
	if body.Currency == "" {
		body.Currency = "CNY"
	}
	if body.Period == "" {
		body.Period = "monthly"
	}
	body.IsActive = true
	if err := h.planRepo.Upsert(c.Request.Context(), &body); err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	OK(c, body)
}

func (h *PaymentCallbackHandler) deletePlan(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	id := c.Param("id")
	if id == "free" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "cannot delete free plan")
		return
	}
	if err := h.planRepo.Delete(c.Request.Context(), id); err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	OK(c, nil)
}

