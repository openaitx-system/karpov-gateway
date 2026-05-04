package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ExtraUsageHandler 提供"超额使用 / Extra Usage"的 REST 端点：
//
//	GET    /v1/billing/extra-usage              - 当前用户的开关与上限
//	PUT    /v1/billing/extra-usage              - 更新开关 / cap / 通知阈值
//	GET    /v1/billing/extra-usage/report       - 当前账期的汇总（已用量 + 估算金额 + 剩余 + 余额）
//	GET    /v1/billing/extra-usage/charges      - 历史账单（最近 N 个月）
//	GET    /v1/billing/extra-usage/events       - 单次超额事件流水
//
// 与 PlanQuotaMiddleware 的协作：中间件先查 plan_repo 取月度限额，超出后调用
// CheckAndCharge 扣余额 + cap 校验；handler 是查询 + 用户配置入口。
type ExtraUsageHandler struct {
	repo     ExtraUsageRepo
	planRepo *PlanRepo
	balance  BalanceRepo
}

// NewExtraUsageHandler 构造 handler。balance 可为 nil（不展示余额）。
func NewExtraUsageHandler(repo ExtraUsageRepo, planRepo *PlanRepo, balance BalanceRepo) *ExtraUsageHandler {
	return &ExtraUsageHandler{repo: repo, planRepo: planRepo, balance: balance}
}

// Mount 挂载 REST 路由。SessionMiddleware 已在更外层强制登录，此处直接信任 X-User-Id。
func (h *ExtraUsageHandler) Mount(r *gin.Engine) {
	g := r.Group("/v1/billing/extra-usage")
	g.GET("", h.getSettings)
	g.PUT("", h.updateSettings)
	g.GET("/report", h.report)
	g.GET("/charges", h.listCharges)
	g.GET("/events", h.listEvents)
}

// getSettings 返回当前用户的 Extra Usage 配置。
func (h *ExtraUsageHandler) getSettings(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	s, err := h.repo.GetSettings(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "load settings failed: "+err.Error())
		return
	}
	OK(c, s)
}

// updateSettingsBody 请求体。
type updateSettingsBody struct {
	Enabled            *bool  `json:"enabled"`
	MonthlyCapCents    *int64 `json:"monthlyCapCents"`
	NotifyThresholdPct *int   `json:"notifyThresholdPct"`
}

// updateSettings PUT /v1/billing/extra-usage
func (h *ExtraUsageHandler) updateSettings(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	var body updateSettingsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	cur, err := h.repo.GetSettings(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "load settings failed: "+err.Error())
		return
	}
	if body.Enabled != nil {
		cur.Enabled = *body.Enabled
	}
	if body.MonthlyCapCents != nil {
		if *body.MonthlyCapCents < 0 {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "monthlyCapCents must be >= 0")
			return
		}
		cur.MonthlyCapCents = *body.MonthlyCapCents
	}
	if body.NotifyThresholdPct != nil {
		if *body.NotifyThresholdPct < 0 || *body.NotifyThresholdPct > 100 {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "notifyThresholdPct out of range")
			return
		}
		cur.NotifyThresholdPct = *body.NotifyThresholdPct
	}
	cur.UserID = uid
	if err := h.repo.UpsertSettings(c.Request.Context(), cur); err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "save failed: "+err.Error())
		return
	}
	OK(c, cur)
}

// ExtraUsageReport 是单账期的汇总报告。
type ExtraUsageReport struct {
	UserID             string  `json:"userId"`
	YearMonth          string  `json:"yearMonth"`
	Enabled            bool    `json:"enabled"`
	MonthlyCapCents    int64   `json:"monthlyCapCents"`
	NotifyThresholdPct int     `json:"notifyThresholdPct"`
	PlanID             string  `json:"planId"`
	PlanName           string  `json:"planName"`
	OveragePricePer1k  int64   `json:"overagePricePer_1k"`
	BaseMonthlyLimit   int64   `json:"baseMonthlyLimit"`
	BaseMonthlyUsed    int64   `json:"baseMonthlyUsed"`
	OverageCount       int64   `json:"overageCount"`
	OverageWeight      int64   `json:"overageWeight"`
	OverageCents       int64   `json:"overageCents"`
	RemainingCapCents  int64   `json:"remainingCapCents"`
	UsedCapPct         float64 `json:"usedCapPct"`
	NearLimit          bool    `json:"nearLimit"`
	BalanceCents       int64   `json:"balanceCents"`
	BalanceCurrency    string  `json:"balanceCurrency"`
	UpdatedAt          string  `json:"updatedAt"`
}

// report GET /v1/billing/extra-usage/report
func (h *ExtraUsageHandler) report(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	now := time.Now().UTC()
	yearMonth := now.Format("2006-01")
	if v := c.Query("month"); v != "" {
		yearMonth = v
	}

	settings, err := h.repo.GetSettings(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "load settings failed: "+err.Error())
		return
	}
	charge, err := h.repo.GetCharge(c.Request.Context(), uid, yearMonth)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "load charge failed: "+err.Error())
		return
	}

	// 解析计划信息（用于展示）
	report := ExtraUsageReport{
		UserID:             uid,
		YearMonth:          yearMonth,
		Enabled:            settings.Enabled,
		MonthlyCapCents:    settings.MonthlyCapCents,
		NotifyThresholdPct: settings.NotifyThresholdPct,
		OverageCount:       charge.Count,
		OverageWeight:      charge.WeightSum,
		OverageCents:       charge.AmountCents,
	}
	if h.planRepo != nil && charge.PlanID != "" {
		if p, err := h.planRepo.Get(c.Request.Context(), charge.PlanID); err == nil {
			report.PlanID = p.ID
			report.PlanName = p.Name
			report.OveragePricePer1k = p.OveragePricePer1k
			report.BaseMonthlyLimit = p.MonthlyLimit
		}
	}
	if h.balance != nil {
		if bal, err := h.balance.Get(c.Request.Context(), uid); err == nil {
			report.BalanceCents = bal.BalanceCents
			report.BalanceCurrency = bal.Currency
		}
	}
	if !charge.UpdatedAt.IsZero() {
		report.UpdatedAt = charge.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if settings.MonthlyCapCents > 0 {
		report.RemainingCapCents = settings.MonthlyCapCents - charge.AmountCents
		if report.RemainingCapCents < 0 {
			report.RemainingCapCents = 0
		}
		report.UsedCapPct = float64(charge.AmountCents) / float64(settings.MonthlyCapCents)
		if settings.NotifyThresholdPct > 0 &&
			report.UsedCapPct*100 >= float64(settings.NotifyThresholdPct) {
			report.NearLimit = true
		}
	}
	OK(c, report)
}

// listCharges GET /v1/billing/extra-usage/charges?limit=12
func (h *ExtraUsageHandler) listCharges(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "12"))
	items, err := h.repo.ListCharges(c.Request.Context(), uid, limit)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "list failed: "+err.Error())
		return
	}
	if items == nil {
		items = []*OverageCharge{}
	}
	OK(c, gin.H{"items": items, "total": len(items)})
}

// listEvents GET /v1/billing/extra-usage/events?limit=100
func (h *ExtraUsageHandler) listEvents(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := h.repo.ListEvents(c.Request.Context(), uid, limit)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "list failed: "+err.Error())
		return
	}
	if items == nil {
		items = []*OverageEvent{}
	}
	OK(c, gin.H{"items": items, "total": len(items)})
}

// ---- 中间件协作辅助 ----

// ExtraUsageDecision 是配额超出后由 ExtraUsageService 给中间件的判定结果。
type ExtraUsageDecision struct {
	Allow            bool   `json:"allow"`            // 是否放行
	Charged          bool   `json:"charged"`          // 是否在本次扣费
	PriceCents       int64  `json:"priceCents"`       // 本次扣费金额（分）
	UsedCents        int64  `json:"usedCents"`        // 当月累计已扣费（含本次）
	CapCents         int64  `json:"capCents"`         // 用户设置的月度 cap（0=无上限）
	BalanceAfterCents int64 `json:"balanceAfterCents"` // 扣费后的余额（仅 Charged 时有意义）
	Reason           string `json:"reason"`           // 拒绝原因（用户可读）
}

// ExtraUsageService 把 repo + balance + plan_repo 封装为 PlanQuotaMiddleware 直接可用的服务。
//
// 计费来源：永远从用户余额（钱包）扣，扣不出（余额不足）则拒绝。
// 因此用户必须先充值，然后开 Extra Usage 才能享受按量。
type ExtraUsageService struct {
	repo    ExtraUsageRepo
	balance BalanceRepo
}

// NewExtraUsageService 构造。balance 可为 nil（仅做开发调试，不做扣费）。
func NewExtraUsageService(repo ExtraUsageRepo, balance BalanceRepo) *ExtraUsageService {
	return &ExtraUsageService{repo: repo, balance: balance}
}

// CheckAndCharge 在月度配额耗尽时被中间件调用：
//
//	1) Extra Usage 开关关闭 → reject(extra_usage_disabled)
//	2) 当前套餐 overage_price_per_1k <= 0 → reject(plan_not_pay_as_you_go)
//	3) 月度 cap 已被本次预扣触发超出 → reject(extra_usage_cap_exceeded)
//	4) 用户余额不足以扣本次 → reject(insufficient_balance)
//	5) 否则原子 Debit 余额 → 记账 → 写流水 → allow
//
// 单价：price_cents_per_1k * weight / 1000，向上取整且最少 1 分。
func (s *ExtraUsageService) CheckAndCharge(
	ctx context.Context,
	userID, planID, provider, endpoint string,
	weight int,
	pricePer1k int64,
) (*ExtraUsageDecision, error) {
	if weight <= 0 {
		weight = 1
	}
	settings, err := s.repo.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return &ExtraUsageDecision{Allow: false, Reason: "extra_usage_disabled"}, nil
	}
	if pricePer1k <= 0 {
		return &ExtraUsageDecision{Allow: false, Reason: "plan_not_pay_as_you_go"}, nil
	}

	priceCents := (pricePer1k*int64(weight) + 999) / 1000
	if priceCents <= 0 {
		priceCents = 1
	}

	now := time.Now().UTC()
	yearMonth := now.Format("2006-01")
	cur, err := s.repo.GetCharge(ctx, userID, yearMonth)
	if err != nil {
		return nil, err
	}
	projected := cur.AmountCents + priceCents
	if settings.MonthlyCapCents > 0 && projected > settings.MonthlyCapCents {
		return &ExtraUsageDecision{
			Allow:      false,
			PriceCents: priceCents,
			UsedCents:  cur.AmountCents,
			CapCents:   settings.MonthlyCapCents,
			Reason:     "extra_usage_cap_exceeded",
		}, nil
	}

	// 余额扣费（原子）
	var balanceAfter int64
	if s.balance != nil {
		bal, _, derr := s.balance.Debit(ctx, DebitRequest{
			UserID:      userID,
			AmountCents: priceCents,
			Kind:        BalanceKindOverage,
			Reference:   fmt.Sprintf("%s/%s", provider, strings.TrimPrefix(endpoint, "/")),
			Description: fmt.Sprintf("Extra usage: %s/%s × %d", provider, endpoint, weight),
		})
		if derr != nil {
			if errors.Is(derr, ErrInsufficientBalance) {
				return &ExtraUsageDecision{
					Allow:      false,
					PriceCents: priceCents,
					UsedCents:  cur.AmountCents,
					CapCents:   settings.MonthlyCapCents,
					Reason:     "insufficient_balance",
				}, nil
			}
			return nil, derr
		}
		balanceAfter = bal.BalanceCents
	}

	updated, err := s.repo.AddCharge(ctx, userID, yearMonth, planID, weight, priceCents)
	if err != nil {
		// 已扣余额但记账失败：尽力回退（best-effort，错误不传播给调用方）
		if s.balance != nil {
			_, _, _ = s.balance.Credit(ctx, CreditRequest{
				UserID: userID, AmountCents: priceCents, Kind: BalanceKindRefund,
				Description: "compensate failed extra usage charge",
			})
		}
		return nil, err
	}
	_ = s.repo.RecordEvent(ctx, &OverageEvent{
		UserID:     userID,
		Provider:   provider,
		Endpoint:   strings.TrimPrefix(endpoint, "/"),
		PlanID:     planID,
		Weight:     weight,
		PriceCents: priceCents,
		Timestamp:  now,
	})
	return &ExtraUsageDecision{
		Allow:             true,
		Charged:           true,
		PriceCents:        priceCents,
		UsedCents:         updated.AmountCents,
		CapCents:          settings.MonthlyCapCents,
		BalanceAfterCents: balanceAfter,
	}, nil
}
