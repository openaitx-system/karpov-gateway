package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// UserPlanHandler 暴露当前登录用户的"当前套餐"+ 套餐详情（QPS / 每日 / 每月限额 / 按量价格）。
//
//	GET /v1/billing/me/plan
//
// 与 /v1/auth/me 互补：me 返回身份字段（来自 auth.proto），本端点返回与 plan 相关的
// 计费/配额详情，避免动 proto。
type UserPlanHandler struct {
	authSvc  *auth.Service
	planRepo *PlanRepo
}

// NewUserPlanHandler 构造 handler。
func NewUserPlanHandler(authSvc *auth.Service, planRepo *PlanRepo) *UserPlanHandler {
	return &UserPlanHandler{authSvc: authSvc, planRepo: planRepo}
}

// Mount 挂载路由。
func (h *UserPlanHandler) Mount(r *gin.Engine) {
	r.GET("/v1/billing/me/plan", h.getMyPlan)
}

type myPlanResp struct {
	UserID            string `json:"userId"`
	PlanID            string `json:"planId"`
	PlanName          string `json:"planName,omitempty"`
	PriceCents        int64  `json:"priceCents"`
	Currency          string `json:"currency,omitempty"`
	Period            string `json:"period,omitempty"`
	QPS               int    `json:"qps"`
	DailyLimit        int64  `json:"dailyLimit"`
	MonthlyLimit      int64  `json:"monthlyLimit"`
	SoftLimitPct      int    `json:"softLimitPct"`
	PayAsYouGo        bool   `json:"payAsYouGo"`
	OveragePricePer1k int64  `json:"overagePricePer_1k"`
}

func (h *UserPlanHandler) getMyPlan(c *gin.Context) {
	uid := authUserID(c)
	if uid == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "login required")
		return
	}
	u, err := h.authSvc.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	out := myPlanResp{UserID: u.ID, PlanID: u.EffectivePlanID()}
	if h.planRepo != nil {
		if p, err := h.planRepo.Get(c.Request.Context(), out.PlanID); err == nil {
			out.PlanName = p.Name
			out.PriceCents = p.PriceCents
			out.Currency = p.Currency
			out.Period = p.Period
			out.QPS = p.QPS
			out.DailyLimit = p.DailyLimit
			out.MonthlyLimit = p.MonthlyLimit
			out.SoftLimitPct = p.SoftLimitPct
			out.PayAsYouGo = p.PayAsYouGo
			out.OveragePricePer1k = p.OveragePricePer1k
		}
	}
	OK(c, out)
}
