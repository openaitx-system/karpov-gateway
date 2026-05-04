package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// AdminUsersHandler 提供超级管理员的用户管理 REST 端点：
//
//	GET    /v1/admin/users                          - 列举用户（分页 + 邮箱模糊搜索）
//	GET    /v1/admin/users/:id                      - 用户详情（含余额、API Keys 摘要）
//	PATCH  /v1/admin/users/:id                      - 更新角色 / 状态
//	POST   /v1/admin/users/:id/password             - 强制重置密码（管理员设新密码）
//	GET    /v1/admin/users/:id/balance              - 查用户余额
//	POST   /v1/admin/users/:id/balance              - 调整余额（credit / debit / set）
//	GET    /v1/admin/users/:id/balance/transactions - 用户余额流水
//	GET    /v1/admin/users/:id/keys                 - 用户 API Keys
//	POST   /v1/admin/users/:id/plan                 - 把用户的全部 API Key 套餐切到 planId
//
// 路由前缀 /v1/admin/* 由 AdminAuthMiddleware 强制鉴权（admin token 或 session role admin/superadmin）。
// 高敏感子集（密码强重置 / 余额调整 / 套餐强切 / 角色升降）由 SuperadminPathMiddleware
// 在路径级把门槛提到 superadmin；个别 handler 内仍保留 inline 校验做 defense-in-depth。
type AdminUsersHandler struct {
	authSvc  *auth.Service
	balance  BalanceRepo
	planRepo *PlanRepo
}

// NewAdminUsersHandler 构造 handler。
func NewAdminUsersHandler(authSvc *auth.Service, balance BalanceRepo, planRepo *PlanRepo) *AdminUsersHandler {
	return &AdminUsersHandler{authSvc: authSvc, balance: balance, planRepo: planRepo}
}

// Mount 挂载 REST 路由。
func (h *AdminUsersHandler) Mount(r *gin.Engine) {
	g := r.Group("/v1/admin/users")
	g.GET("", h.list)
	g.GET("/:id", h.detail)
	g.PATCH("/:id", h.update)
	g.POST("/:id/password", h.resetPassword)

	g.GET("/:id/balance", h.getBalance)
	g.POST("/:id/balance", h.adjustBalance)
	g.GET("/:id/balance/transactions", h.listBalanceTxns)

	g.GET("/:id/keys", h.listKeys)
	g.POST("/:id/plan", h.setPlan)
}

// userView 是用户列表/详情的 JSON 视图（不含密码 hash）。
type userView struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Status      string    `json:"status"`
	Role        string    `json:"role"`
	PlanID      string    `json:"planId"`
	TOTPEnabled bool      `json:"totpEnabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toUserView(u *auth.User) userView {
	return userView{
		ID: u.ID, Email: u.Email, Status: u.Status, Role: u.EffectiveRole(),
		PlanID:      u.EffectivePlanID(),
		TOTPEnabled: u.TOTPEnabled, CreatedAt: u.CreatedAt,
	}
}

// list GET /v1/admin/users?email=xx&role=user&status=active&limit=50&offset=0
func (h *AdminUsersHandler) list(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	users, total, err := h.authSvc.ListUsers(c.Request.Context(), auth.ListUserFilter{
		EmailLike: c.Query("email"),
		Role:      c.Query("role"),
		Status:    c.Query("status"),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, "list users: "+err.Error())
		return
	}
	items := make([]userView, 0, len(users))
	for _, u := range users {
		items = append(items, toUserView(u))
	}
	OK(c, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// userDetail 是 /v1/admin/users/:id 的响应：包含余额 + API Key 摘要。
type userDetail struct {
	User         userView                    `json:"user"`
	Balance      *Balance                    `json:"balance,omitempty"`
	APIKeys      []apiKeySummary             `json:"apiKeys"`
	PlanCounts   map[string]int              `json:"planCounts"`
}

type apiKeySummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Prefix      string    `json:"prefix"`
	PlanID      string    `json:"planId"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt,omitempty"`
	LastUsedAt  time.Time `json:"lastUsedAt,omitempty"`
}

// detail GET /v1/admin/users/:id
func (h *AdminUsersHandler) detail(c *gin.Context) {
	uid := c.Param("id")
	u, err := h.authSvc.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "user not found")
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	out := userDetail{User: toUserView(u), PlanCounts: map[string]int{}}
	if h.balance != nil {
		if bal, berr := h.balance.Get(c.Request.Context(), uid); berr == nil {
			out.Balance = bal
		}
	}
	if keys, kerr := h.authSvc.ListAPIKeys(c.Request.Context(), uid); kerr == nil {
		out.APIKeys = make([]apiKeySummary, 0, len(keys))
		for _, k := range keys {
			s := "active"
			if !k.RevokedAt.IsZero() {
				s = "revoked"
			} else if !k.ExpiresAt.IsZero() && time.Now().UTC().After(k.ExpiresAt) {
				s = "expired"
			}
			out.APIKeys = append(out.APIKeys, apiKeySummary{
				ID: k.ID, Name: k.Name, Prefix: k.Prefix, PlanID: k.PlanID,
				Status: s, CreatedAt: k.CreatedAt,
				ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
			})
			if s == "active" {
				out.PlanCounts[k.PlanID]++
			}
		}
	}
	OK(c, out)
}

// updateBody PATCH 用户。仅允许改 status / role；其他字段不暴露给管理员（密码改用 /password 接口）。
type updateBody struct {
	Status *string `json:"status,omitempty"`
	Role   *string `json:"role,omitempty"`
}

func (h *AdminUsersHandler) update(c *gin.Context) {
	uid := c.Param("id")
	var body updateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	operator, _ := c.Get("auth.role")
	operatorRole, _ := operator.(string)

	if body.Role != nil {
		// 修改角色仅 superadmin
		if operatorRole != auth.RoleSuperAdmin {
			Fail(c, http.StatusForbidden, CodeForbidden, "only superadmin can change role")
			return
		}
		if err := h.authSvc.SetRole(c.Request.Context(), uid, *body.Role); err != nil {
			Fail(c, http.StatusBadRequest, CodeBadRequest, err.Error())
			return
		}
	}
	if body.Status != nil {
		if err := h.authSvc.SetStatus(c.Request.Context(), uid, *body.Status); err != nil {
			Fail(c, http.StatusBadRequest, CodeBadRequest, err.Error())
			return
		}
	}
	u, err := h.authSvc.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	slog.Info("[admin] user updated", "userID", uid, "operator", authUserID(c),
		"role", body.Role, "status", body.Status)
	OK(c, toUserView(u))
}

// resetPasswordBody POST /v1/admin/users/:id/password
type resetPasswordBody struct {
	NewPassword string `json:"newPassword" binding:"required,min=6"`
}

func (h *AdminUsersHandler) resetPassword(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	uid := c.Param("id")
	var body resetPasswordBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := h.authSvc.AdminResetPassword(c.Request.Context(), uid, body.NewPassword); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "user not found")
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	slog.Info("[admin] password reset", "userID", uid, "operator", authUserID(c))
	OK(c, gin.H{"ok": true})
}

// getBalance GET /v1/admin/users/:id/balance
func (h *AdminUsersHandler) getBalance(c *gin.Context) {
	if h.balance == nil {
		Fail(c, http.StatusServiceUnavailable, CodeInternal, "balance not configured")
		return
	}
	bal, err := h.balance.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	OK(c, bal)
}

// adjustBalanceBody POST /v1/admin/users/:id/balance
type adjustBalanceBody struct {
	Operation   string `json:"operation"   binding:"required"` // credit / debit / set
	AmountCents int64  `json:"amountCents" binding:"required"` // credit/debit: >0；set: 新余额，>=0
	Description string `json:"description"`
	Reference   string `json:"reference"`
}

func (h *AdminUsersHandler) adjustBalance(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	if h.balance == nil {
		Fail(c, http.StatusServiceUnavailable, CodeInternal, "balance not configured")
		return
	}
	uid := c.Param("id")
	var body adjustBalanceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	if _, err := h.authSvc.GetUserByID(c.Request.Context(), uid); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "user not found")
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	op := authUserID(c)
	desc := body.Description
	ref := body.Reference
	if ref == "" {
		ref = "operator:" + op
	}
	switch body.Operation {
	case "credit":
		if body.AmountCents <= 0 {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "amountCents must be > 0")
			return
		}
		if desc == "" {
			desc = "admin credit"
		}
		bal, _, err := h.balance.Credit(c.Request.Context(), CreditRequest{
			UserID: uid, AmountCents: body.AmountCents, Kind: BalanceKindAdjust,
			Reference: ref, Description: desc,
		})
		if err != nil {
			Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		slog.Info("[admin] balance credit", "userID", uid, "amountCents", body.AmountCents, "operator", op)
		OK(c, bal)
	case "debit":
		if body.AmountCents <= 0 {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "amountCents must be > 0")
			return
		}
		if desc == "" {
			desc = "admin debit"
		}
		bal, _, err := h.balance.Debit(c.Request.Context(), DebitRequest{
			UserID: uid, AmountCents: body.AmountCents, Kind: BalanceKindAdjust,
			Reference: ref, Description: desc,
		})
		if err != nil {
			if errors.Is(err, ErrInsufficientBalance) {
				Fail(c, http.StatusBadRequest, CodeBadRequest, "insufficient balance")
				return
			}
			Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		slog.Info("[admin] balance debit", "userID", uid, "amountCents", body.AmountCents, "operator", op)
		OK(c, bal)
	case "set":
		if body.AmountCents < 0 {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "amountCents must be >= 0 for set")
			return
		}
		if desc == "" {
			desc = "admin set balance"
		}
		bal, _, err := h.balance.SetBalance(c.Request.Context(), SetRequest{
			UserID: uid, NewBalanceCents: body.AmountCents,
			Reference: ref, Description: desc,
		})
		if err != nil {
			Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		slog.Info("[admin] balance set", "userID", uid, "newBalanceCents", body.AmountCents, "operator", op)
		OK(c, bal)
	default:
		Fail(c, http.StatusBadRequest, CodeBadRequest, "operation must be credit / debit / set")
	}
}

// listBalanceTxns GET /v1/admin/users/:id/balance/transactions
func (h *AdminUsersHandler) listBalanceTxns(c *gin.Context) {
	if h.balance == nil {
		Fail(c, http.StatusServiceUnavailable, CodeInternal, "balance not configured")
		return
	}
	uid := c.Param("id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	items, total, err := h.balance.ListTransactions(c.Request.Context(), uid, limit, offset)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if items == nil {
		items = []*BalanceTransaction{}
	}
	OK(c, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// listKeys GET /v1/admin/users/:id/keys
func (h *AdminUsersHandler) listKeys(c *gin.Context) {
	uid := c.Param("id")
	keys, err := h.authSvc.ListAPIKeys(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	out := make([]apiKeySummary, 0, len(keys))
	for _, k := range keys {
		s := "active"
		if !k.RevokedAt.IsZero() {
			s = "revoked"
		} else if !k.ExpiresAt.IsZero() && time.Now().UTC().After(k.ExpiresAt) {
			s = "expired"
		}
		out = append(out, apiKeySummary{
			ID: k.ID, Name: k.Name, Prefix: k.Prefix, PlanID: k.PlanID,
			Status: s, CreatedAt: k.CreatedAt,
			ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
		})
	}
	OK(c, gin.H{"items": out, "total": len(out)})
}

// setPlanBody POST /v1/admin/users/:id/plan
type setPlanBody struct {
	PlanID string `json:"planId" binding:"required"`
}

// setPlan 把用户的"当前套餐"和所有活跃 API Key 套餐都切到 PlanID。
//
// 关键：先改 users.plan_id，这样新建的 API Key 才会继承新套餐；
// 然后批量把已有未吊销 Key 的 plan_id 也切过去。
func (h *AdminUsersHandler) setPlan(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	uid := c.Param("id")
	var body setPlanBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	if h.planRepo != nil {
		if _, err := h.planRepo.Get(c.Request.Context(), body.PlanID); err != nil {
			Fail(c, http.StatusBadRequest, CodeBadRequest, "plan not found: "+body.PlanID)
			return
		}
	}
	// 1) 先把用户级套餐切过去——新建的 API Key 会从这里继承
	if err := h.authSvc.SetUserPlan(c.Request.Context(), uid, body.PlanID); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			Fail(c, http.StatusNotFound, CodeNotFound, "user not found")
			return
		}
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	// 2) 批量切已有 Key
	keys, err := h.authSvc.ListAPIKeys(c.Request.Context(), uid)
	if err != nil {
		Fail(c, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	updated := 0
	for _, k := range keys {
		if !k.RevokedAt.IsZero() {
			continue
		}
		if k.PlanID == body.PlanID {
			continue
		}
		if err := h.authSvc.SetAPIKeyPlan(c.Request.Context(), uid, k.ID, body.PlanID); err != nil {
			slog.Warn("[admin] setPlan failed for key", "userID", uid, "keyID", k.ID, "err", err)
			continue
		}
		updated++
	}
	slog.Info("[admin] plan set", "userID", uid, "planID", body.PlanID,
		"updated", updated, "totalKeys", len(keys), "operator", authUserID(c))
	OK(c, gin.H{"planId": body.PlanID, "updatedKeys": updated, "totalKeys": len(keys)})
}
