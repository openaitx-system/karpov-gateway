package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newSuperadminEngine 构造一个 gin engine：
//   - 模拟 SessionMiddleware 已经把 role 注入 c.Set("auth.role", role)
//     和 X-User-Role header（与生产链路对齐）
//   - 挂 SuperadminPathMiddleware
//   - 注册 catch-all 200 handler 验证放行
func newSuperadminEngine(t *testing.T, role string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if role != "" {
			c.Set("auth.role", role)
			c.Request.Header.Set("X-User-Role", role)
		}
		c.Next()
	})
	r.Use(SuperadminPathMiddleware(SuperadminPathMiddlewareOptions{}))
	r.NoRoute(func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func runReq(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// 路径不在保护列表里，任何角色都通过。
func TestSuperadminPathMiddleware_OutOfScopePathPasses(t *testing.T) {
	for _, role := range []string{"", "user", "admin", "superadmin"} {
		r := newSuperadminEngine(t, role)
		for _, p := range []string{
			"/v1/qqmusic/songs/1",
			"/v1/billing/orders",
			"/v1/billing/balance",
			"/v1/billing/balance/topup",   // 非 adjust，通过
			"/v1/billing/plans",           // GET 列表非保护
			"/v1/admin/users",             // 非高敏感（GET 列表）
			"/v1/admin/users/abc",         // 非高敏感（GET / PATCH）
			"/v1/admin/pool/credentials",  // 号池增删走 admin/superadmin role 由其他位置控制
			"/v1/admin/login/qr/start",
		} {
			method := http.MethodGet
			if p == "/v1/billing/balance/topup" {
				method = http.MethodPost
			}
			rec := runReq(r, method, p)
			if rec.Code != 200 {
				t.Errorf("role=%q path=%s expected 200, got %d body=%s",
					role, p, rec.Code, rec.Body.String())
			}
		}
	}
}

// 命中保护规则但角色未注入 → 401。
func TestSuperadminPathMiddleware_NoRole_401(t *testing.T) {
	r := newSuperadminEngine(t, "")
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/users/u1/password"},
		{http.MethodPost, "/v1/admin/users/u1/balance"},
		{http.MethodPost, "/v1/admin/users/u1/plan"},
		{http.MethodPut, "/v1/admin/settings/payment"},
		{http.MethodPut, "/v1/admin/settings/currency"},
		{http.MethodPut, "/v1/billing/plans/pro"},
		{http.MethodDelete, "/v1/billing/plans/pro"},
		{http.MethodPost, "/v1/billing/balance/adjust"},
	} {
		rec := runReq(r, tc.method, tc.path)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s expected 401, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

// 命中规则、角色为 user / admin → 403。
func TestSuperadminPathMiddleware_NonSuperadmin_403(t *testing.T) {
	for _, role := range []string{"user", "admin"} {
		r := newSuperadminEngine(t, role)
		for _, tc := range []struct {
			method, path string
		}{
			{http.MethodPost, "/v1/admin/users/u1/password"},
			{http.MethodPost, "/v1/admin/users/u1/balance"},
			{http.MethodPost, "/v1/admin/users/u1/plan"},
			{http.MethodPut, "/v1/admin/settings/payment"},
			{http.MethodPut, "/v1/admin/settings/currency"},
			{http.MethodPut, "/v1/billing/plans/pro"},
			{http.MethodDelete, "/v1/billing/plans/pro"},
			{http.MethodPost, "/v1/billing/balance/adjust"},
		} {
			rec := runReq(r, tc.method, tc.path)
			if rec.Code != http.StatusForbidden {
				t.Errorf("role=%q %s %s expected 403, got %d",
					role, tc.method, tc.path, rec.Code)
			}
		}
	}
}

// 命中规则、角色为 superadmin → 200。
func TestSuperadminPathMiddleware_Superadmin_200(t *testing.T) {
	r := newSuperadminEngine(t, "superadmin")
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/admin/users/u1/password"},
		{http.MethodPost, "/v1/admin/users/u1/balance"},
		{http.MethodPost, "/v1/admin/users/u1/plan"},
		{http.MethodPut, "/v1/admin/settings/payment"},
		{http.MethodPut, "/v1/admin/settings/currency"},
		{http.MethodPut, "/v1/billing/plans/pro"},
		{http.MethodDelete, "/v1/billing/plans/pro"},
		{http.MethodPost, "/v1/billing/balance/adjust"},
	} {
		rec := runReq(r, tc.method, tc.path)
		if rec.Code != 200 {
			t.Errorf("superadmin %s %s expected 200, got %d body=%s",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// PATCH /v1/admin/users/:id 不在路径保护表里（admin 改 status / superadmin 改 role 在 handler 内分支）。
func TestSuperadminPathMiddleware_PatchUserPasses(t *testing.T) {
	r := newSuperadminEngine(t, "admin")
	rec := runReq(r, http.MethodPatch, "/v1/admin/users/abc")
	if rec.Code != 200 {
		t.Errorf("PATCH /v1/admin/users/:id should pass at path-mw level for admin: %d", rec.Code)
	}
}

// /v1/billing/plans GET（列表）不被保护；只有 PUT/DELETE 受限。
func TestSuperadminPathMiddleware_PlansGetPasses(t *testing.T) {
	r := newSuperadminEngine(t, "user")
	rec := runReq(r, http.MethodGet, "/v1/billing/plans")
	if rec.Code != 200 {
		t.Errorf("GET /v1/billing/plans should not be gated by superadmin: %d", rec.Code)
	}
	rec = runReq(r, http.MethodGet, "/v1/billing/plans/pro")
	if rec.Code != 200 {
		t.Errorf("GET /v1/billing/plans/:id should not be gated: %d", rec.Code)
	}
}

// 角色仅在 c.Set 而非 header 时也能识别（fallback 路径）。
func TestSuperadminPathMiddleware_RoleFromContextOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth.role", "superadmin")
		// 故意不设 header
		c.Next()
	})
	r.Use(SuperadminPathMiddleware(SuperadminPathMiddlewareOptions{}))
	r.NoRoute(func(c *gin.Context) { c.String(200, "ok") })

	rec := runReq(r, http.MethodPut, "/v1/admin/settings/payment")
	if rec.Code != 200 {
		t.Errorf("role from c.Set fallback should work: %d body=%s", rec.Code, rec.Body.String())
	}
}

// 自定义 Rules：能精确控制保护范围。
func TestSuperadminPathMiddleware_CustomRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth.role", "admin")
		c.Request.Header.Set("X-User-Role", "admin")
		c.Next()
	})
	r.Use(SuperadminPathMiddleware(SuperadminPathMiddlewareOptions{
		Rules: []SuperadminRule{
			{Method: http.MethodGet, Prefix: "/v1/secret/"},
		},
	}))
	r.NoRoute(func(c *gin.Context) { c.String(200, "ok") })

	if rec := runReq(r, http.MethodGet, "/v1/secret/data"); rec.Code != http.StatusForbidden {
		t.Errorf("custom rule should block admin: %d", rec.Code)
	}
	// 自定义规则替换默认规则；默认保护路径不再被拦
	if rec := runReq(r, http.MethodPost, "/v1/admin/users/x/password"); rec.Code != 200 {
		t.Errorf("custom rules should replace defaults: %d", rec.Code)
	}
}
