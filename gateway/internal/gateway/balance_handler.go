package gateway

import (
	cryptorand "crypto/rand"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/MiChongs/QQMusicApi/gateway/internal/billing"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing/payment"
)

// buildTopupIdemKey 为一次充值生成全局唯一 idem key。每次调用都注入随机 nonce —
// 同 user 同金额双击不会再命中老订单 dedup。
func buildTopupIdemKey(userID string, amountCents int64) (string, error) {
	var b [8]byte
	if _, err := io.ReadFull(cryptorand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("topup idem nonce: %w", err)
	}
	return fmt.Sprintf("topup:%s:%d:%x", userID, amountCents, b[:]), nil
}

// BalanceHandler 提供"用户余额（钱包）"REST 端点：
//
//	GET    /v1/billing/balance               - 当前余额
//	GET    /v1/billing/balance/transactions  - 流水（带分页）
//	POST   /v1/billing/balance/topup         - 创建充值订单（返回 PayURL）
//	POST   /v1/billing/balance/adjust        - 管理员手动调整（superadmin only）
type BalanceHandler struct {
	repo        BalanceRepo
	billingSvc  *billing.Service
	payRegistry *payment.Registry
	currency    *billing.CurrencyStore // 可选：跨币种渠道（如 ldcpay）的换算
	notifyURL   func(provider string) string
}

// NewBalanceHandler 构造 handler。notifyURL 用于回调地址；通常 = `<base>/v1/billing/callback/<provider>`。
//
// currency 允许 nil（兼容老调用 / 测试）；为 nil 时跨币种渠道直接用 CNY 创建订单，
// FulfillTopup 走 Amount 直接 credit 的旧路径。
func NewBalanceHandler(repo BalanceRepo, billingSvc *billing.Service, payReg *payment.Registry, currency *billing.CurrencyStore, notifyURL func(string) string) *BalanceHandler {
	return &BalanceHandler{
		repo:        repo,
		billingSvc:  billingSvc,
		payRegistry: payReg,
		currency:    currency,
		notifyURL:   notifyURL,
	}
}

// Mount 挂载 REST 路由。
func (h *BalanceHandler) Mount(r *gin.Engine) {
	g := r.Group("/v1/billing/balance")
	g.GET("", h.getBalance)
	g.GET("/transactions", h.listTransactions)
	g.POST("/topup", h.createTopup)
	g.POST("/adjust", h.adjust)
}

// getBalance GET /v1/billing/balance
func (h *BalanceHandler) getBalance(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	bal, err := h.repo.Get(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "load balance failed: "+err.Error())
		return
	}
	OK(c, bal)
}

// listTransactions GET /v1/billing/balance/transactions?limit=50&offset=0
func (h *BalanceHandler) listTransactions(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	items, total, err := h.repo.ListTransactions(c.Request.Context(), uid, limit, offset)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "list failed: "+err.Error())
		return
	}
	if items == nil {
		items = []*BalanceTransaction{}
	}
	OK(c, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// createTopupBody 充值请求体。
type createTopupBody struct {
	AmountCents     int64  `json:"amountCents"     binding:"required"` // 必须 >= 100（1 元起充）
	PaymentProvider string `json:"paymentProvider" binding:"required"`
	SuccessURL      string `json:"successUrl"`
}

// createTopup POST /v1/billing/balance/topup
//
// 流程：
//  1. 验证金额 >= 1 元
//  2. CreateOrder（purpose=topup, plan_id="topup"）
//  3. payment.CreatePayment 拿 PayURL
//  4. 状态推进到 paying
//  5. 返回订单 + PayURL；前端跳转支付，回调成功后由 PaymentCallbackHandler 充值
func (h *BalanceHandler) createTopup(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	var body createTopupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.AmountCents < 100 {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "minimum top-up amount is ¥1.00 (100 cents)")
		return
	}
	if body.AmountCents > 100*10000*100 { // 单笔上限 100 万元
		Fail(c, http.StatusBadRequest, CodeBadRequest, "single top-up cannot exceed ¥1,000,000")
		return
	}
	prov, _ := h.payRegistry.Get(body.PaymentProvider)
	if prov == nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "unsupported payment provider: "+body.PaymentProvider)
		return
	}

	amount := decimal.NewFromInt(body.AmountCents).Div(decimal.NewFromInt(100))
	// 充值订单也加 nonce 防止同秒同金额双击命中 CreateOrder dedup（症状: 老订单 ID
	// 二次提交给 LDC 触发 idx_orders_client_merchant_order 唯一约束爆炸）。
	idem, idemErr := buildTopupIdemKey(uid, body.AmountCents)
	if idemErr != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, idemErr.Error())
		return
	}
	order := &billing.Order{
		ID:              billing.NewOrderID(),
		UserID:          uid,
		PlanID:          "topup",
		Amount:          amount,
		Currency:        "CNY",
		Status:          billing.OrderPending,
		Purpose:         billing.OrderPurposeTopup,
		PaymentProvider: body.PaymentProvider,
		IdempotencyKey:  idem,
	}
	// 跨币种渠道：用户输入是 CNY 金额，但渠道结算币不同（ldcpay → LDC）。
	// 在 CreateOrder 之前换算：原 CNY 金额留做 OriginalAmount（FulfillTopup 用它 credit 用户钱包），
	// 实际向支付网关下单的 Amount/Currency 改成结算币。
	if target := resolveTargetCurrency(body.PaymentProvider); target != "" && h.currency != nil {
		if err := h.currency.ApplyCurrency(order, target); err != nil {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "currency: "+err.Error())
			return
		}
	}
	created, err := h.billingSvc.CreateOrder(c.Request.Context(), order)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "create order failed: "+err.Error())
		return
	}
	if created.Status != billing.OrderPending {
		Fail(c, http.StatusConflict, CodeConflict,
			fmt.Sprintf("topup order already exists (id=%s, status=%s)", created.ID, created.Status))
		return
	}

	notify := ""
	if h.notifyURL != nil {
		notify = h.notifyURL(body.PaymentProvider)
	}
	resp, err := prov.CreatePayment(c.Request.Context(), payment.CreateRequest{
		OrderID:   created.ID,
		Amount:    created.Amount,
		Currency:  created.Currency,
		Subject:   fmt.Sprintf("钱包充值 ¥%s", created.Amount.StringFixed(2)),
		NotifyURL: notify,
		ReturnURL: body.SuccessURL,
		UserIP:    c.ClientIP(),
	})
	if err != nil {
		Fail(c, http.StatusBadGateway, 50200, "create payment failed: "+err.Error())
		return
	}
	_, _ = h.billingSvc.Transition(c.Request.Context(), created.ID, billing.EvtPayStart)

	OK(c, gin.H{
		"orderId":     created.ID,
		"payUrl":      resp.PayURL,
		"amount":      created.Amount.StringFixed(2),
		"amountCents": body.AmountCents,
		"currency":    created.Currency,
		"status":      "paying",
		"purpose":     "topup",
	})
}

// adjustBody 管理员调账请求体。
type adjustBody struct {
	UserID      string `json:"userId"      binding:"required"`
	AmountCents int64  `json:"amountCents" binding:"required"` // 可正可负
	Description string `json:"description"`
}

// adjust POST /v1/billing/balance/adjust（手动调整任意用户余额：superadmin only）
//
// 路径级 superadmin 角色检查由 SuperadminPathMiddleware 提供；这里再做一次内联
// 校验是 defense-in-depth：即使中间件被未来重构误移除，handler 也不会落库。
func (h *BalanceHandler) adjust(c *gin.Context) {
	role, _ := c.Get("auth.role")
	if role != "superadmin" {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	var body adjustBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.AmountCents == 0 {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "amount must be non-zero")
		return
	}
	desc := body.Description
	if desc == "" {
		desc = "admin adjust"
	}
	if body.AmountCents > 0 {
		bal, _, err := h.repo.Credit(c.Request.Context(), CreditRequest{
			UserID:      body.UserID,
			AmountCents: body.AmountCents,
			Kind:        BalanceKindAdjust,
			Description: desc,
			Reference:   "operator:" + authUserID(c),
		})
		if err != nil {
			Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		OK(c, bal)
		return
	}
	bal, _, err := h.repo.Debit(c.Request.Context(), DebitRequest{
		UserID:      body.UserID,
		AmountCents: -body.AmountCents,
		Kind:        BalanceKindAdjust,
		Description: desc,
		Reference:   "operator:" + authUserID(c),
	})
	if err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "insufficient balance")
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	OK(c, bal)
}

// ---- 内部供 PaymentCallbackHandler 调用：充值订单履约 ----

// FulfillTopup 在支付回调成功后由 callback handler 调用：把订单金额计入用户余额。
//
// 跨币种语义：用户充值的"心理金额"是 OriginalAmount（CNY），渠道实际结算的是 Amount（如 LDC）。
// 余额账本以 CNY 记账，所以优先用 OriginalAmount；只有未发生跨币换算（OriginalCurrency 为空）时
// 才退化到 Amount。Description 会带上换算细节方便对账。
//
// 该方法本身幂等：以 order_id 在 balance_transactions 唯一性保证（PG 实现里 INSERT 冲突会失败）。
func (h *BalanceHandler) FulfillTopup(ctx context.Context, order *billing.Order) (*Balance, *BalanceTransaction, error) {
	if order == nil {
		return nil, nil, errors.New("balance: nil order")
	}
	creditAmount := order.Amount
	desc := fmt.Sprintf("Top-up via %s", order.PaymentProvider)
	if order.OriginalCurrency != "" && !order.OriginalAmount.IsZero() {
		creditAmount = order.OriginalAmount
		desc = fmt.Sprintf("Top-up via %s (paid %s %s, fx %s)",
			order.PaymentProvider,
			order.Amount.StringFixed(2), order.Currency,
			order.FXRate.StringFixed(8))
	}
	cents := creditAmount.Mul(decimal.NewFromInt(100)).IntPart()
	if cents <= 0 {
		return nil, nil, ErrInvalidAmount
	}
	return h.repo.Credit(ctx, CreditRequest{
		UserID:      order.UserID,
		AmountCents: cents,
		Kind:        BalanceKindTopup,
		OrderID:     order.ID,
		Reference:   order.ExternalOrderID,
		Description: desc,
	})
}
