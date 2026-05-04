package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// helper：构造一个 gin engine，预先用 SetContextValue 模拟"已经过 SessionMiddleware"，
// 然后挂 ScopeMiddleware，最后注册一个 catch-all 200 handler。
func newScopeEngine(t *testing.T, via string, keyRec *auth.APIKeyRecord) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if via != "" {
			c.Set("auth.via", via)
		}
		if keyRec != nil {
			c.Set("auth.apikey_record", keyRec)
		}
		c.Next()
	})
	r.Use(ScopeMiddleware(ScopeMiddlewareOptions{}))
	r.NoRoute(func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func do(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestScopeMiddleware_SessionUserBypass(t *testing.T) {
	// session 用户没有 keyRec；不应做 scope 检查
	r := newScopeEngine(t, "session", nil)
	if rec := do(r, "POST", "/v1/billing/orders"); rec.Code != 200 {
		t.Errorf("session user blocked at scope mw: %d %s", rec.Code, rec.Body)
	}
}

func TestScopeMiddleware_NoScopesIsWildcard(t *testing.T) {
	// API Key 但没设 scope ⇒ 通配
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: nil})
	for _, path := range []string{
		"/v1/billing/orders",
		"/v1/qqmusic/songs/123",
		"/v1/usage/realtime",
	} {
		if rec := do(r, "POST", path); rec.Code != 200 {
			t.Errorf("nil-scope key blocked at %s: %d", path, rec.Code)
		}
	}
}

func TestScopeMiddleware_RestrictedScope(t *testing.T) {
	// 仅 music:read 的 key
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"music:read"}})

	// 命中 music:read → 通过
	if rec := do(r, "GET", "/v1/qqmusic/songs/abc"); rec.Code != 200 {
		t.Errorf("music:read should pass /v1/qqmusic: %d %s", rec.Code, rec.Body)
	}
	// 命中 billing:write → 拒
	if rec := do(r, "POST", "/v1/billing/orders"); rec.Code != 403 {
		t.Errorf("billing write expected 403, got %d %s", rec.Code, rec.Body)
	}
	// 命中 billing:read → 拒（key 没这 scope）
	if rec := do(r, "GET", "/v1/billing/orders"); rec.Code != 403 {
		t.Errorf("billing read expected 403, got %d", rec.Code)
	}
	// 不在规则表里的路径（例如 /v1/auth/me） → 直接放行（自服务）
	if rec := do(r, "GET", "/v1/auth/me"); rec.Code != 200 {
		t.Errorf("/v1/auth/me should pass (skip), got %d", rec.Code)
	}
}

func TestScopeMiddleware_MultipleScopesGrant(t *testing.T) {
	// 同时具备 billing:read + billing:write → 两边都通过
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{
		Scopes: []string{"billing:read", "billing:write"},
	})
	if rec := do(r, "GET", "/v1/billing/orders"); rec.Code != 200 {
		t.Errorf("billing:read should pass: %d", rec.Code)
	}
	if rec := do(r, "POST", "/v1/billing/orders"); rec.Code != 200 {
		t.Errorf("billing:write should pass: %d", rec.Code)
	}
	// 但是 /v1/qqmusic/* 需要 music:read，没 → 拒
	if rec := do(r, "GET", "/v1/qqmusic/songs/x"); rec.Code != 403 {
		t.Errorf("music:read expected 403, got %d", rec.Code)
	}
}

// 测试 module:* 通配在 middleware 上的端到端效果。
func TestScopeMiddleware_ModuleWildcardGrants(t *testing.T) {
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{
		Scopes: []string{"music:*", "billing:read"},
	})
	// music:* 应通过任何 music 路径
	for _, path := range []string{"/v1/qqmusic/songs/x", "/v1/qqmusic/search/songs", "/v1/netease/songs/y"} {
		if rec := do(r, "GET", path); rec.Code != 200 {
			t.Errorf("music:* should grant %s, got %d", path, rec.Code)
		}
	}
	// billing:read 通过 GET /v1/billing/orders 但不通过 POST
	if rec := do(r, "GET", "/v1/billing/orders"); rec.Code != 200 {
		t.Errorf("billing:read GET should pass, got %d", rec.Code)
	}
	if rec := do(r, "POST", "/v1/billing/orders"); rec.Code != 403 {
		t.Errorf("billing:write should be denied (only have :read), got %d", rec.Code)
	}
}

// "*" 全局通配。
func TestScopeMiddleware_GlobalWildcardGrants(t *testing.T) {
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"*"}})
	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/v1/qqmusic/songs/x"},
		{"POST", "/v1/billing/orders"},
		{"GET", "/v1/usage/realtime"},
		{"GET", "/v1/billing/invoices"},
	} {
		if rec := do(r, tc.method, tc.path); rec.Code != 200 {
			t.Errorf("* should grant %s %s, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestScopeMiddleware_SkipPaths(t *testing.T) {
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"none:none"}})
	// 公开 / 自服务路径无视 scope
	for _, path := range []string{
		"/v1/auth/me",
		"/v1/auth/totp/enable",
		"/v1/billing/callback/yipay",
		"/v1/docs/openapi.yaml",
		"/healthz",
	} {
		if rec := do(r, "GET", path); rec.Code != 200 {
			t.Errorf("%s should be skipped: %d", path, rec.Code)
		}
	}
}

// admin scope 控制 /v1/admin/* 与 /v1/billing/{plans,balance/adjust}。
func TestScopeMiddleware_AdminScopeRules(t *testing.T) {
	// admin:read 只能读，不能写
	readOnly := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"admin:read"}})
	if rec := do(readOnly, "GET", "/v1/admin/users"); rec.Code != 200 {
		t.Errorf("admin:read should pass GET /v1/admin/users: %d", rec.Code)
	}
	if rec := do(readOnly, "GET", "/v1/admin/settings/payment"); rec.Code != 200 {
		t.Errorf("admin:read should pass GET /v1/admin/settings/payment: %d", rec.Code)
	}
	if rec := do(readOnly, "POST", "/v1/admin/users/u1/password"); rec.Code != 403 {
		t.Errorf("admin:read should NOT pass POST: %d", rec.Code)
	}
	if rec := do(readOnly, "PUT", "/v1/admin/settings/currency"); rec.Code != 403 {
		t.Errorf("admin:read should NOT pass PUT: %d", rec.Code)
	}
	if rec := do(readOnly, "DELETE", "/v1/billing/plans/pro"); rec.Code != 403 {
		t.Errorf("admin:read should NOT pass DELETE /v1/billing/plans/:id: %d", rec.Code)
	}

	// admin:write 写方法通过；GET 也通过（admin:write 隐含 admin:read？不！是独立 scope）
	writeOnly := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"admin:write"}})
	if rec := do(writeOnly, "POST", "/v1/admin/users/u1/password"); rec.Code != 200 {
		t.Errorf("admin:write should pass POST: %d", rec.Code)
	}
	if rec := do(writeOnly, "PUT", "/v1/admin/settings/payment"); rec.Code != 200 {
		t.Errorf("admin:write should pass PUT: %d", rec.Code)
	}
	if rec := do(writeOnly, "DELETE", "/v1/billing/plans/pro"); rec.Code != 200 {
		t.Errorf("admin:write should pass DELETE /v1/billing/plans/:id: %d", rec.Code)
	}
	if rec := do(writeOnly, "POST", "/v1/billing/balance/adjust"); rec.Code != 200 {
		t.Errorf("admin:write should pass POST /v1/billing/balance/adjust: %d", rec.Code)
	}
	// admin:write 不含 read（独立的精确 scope）
	if rec := do(writeOnly, "GET", "/v1/admin/users"); rec.Code != 403 {
		t.Errorf("admin:write should NOT pass GET (need admin:read): %d", rec.Code)
	}
}

// admin:* 模块通配覆盖 read + write。
func TestScopeMiddleware_AdminModuleWildcard(t *testing.T) {
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"admin:*"}})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/admin/users"},
		{"GET", "/v1/admin/settings/payment"},
		{"POST", "/v1/admin/users/u1/password"},
		{"PUT", "/v1/admin/settings/currency"},
		{"PATCH", "/v1/admin/users/u1"},
		{"DELETE", "/v1/billing/plans/pro"},
		{"PUT", "/v1/billing/plans/pro"},
		{"POST", "/v1/billing/balance/adjust"},
	} {
		if rec := do(r, tc.method, tc.path); rec.Code != 200 {
			t.Errorf("admin:* should grant %s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

// 没有 admin scope 的 key 不能访问管理面。
func TestScopeMiddleware_NoAdminScope_Denied(t *testing.T) {
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{
		Scopes: []string{"music:*", "billing:read"},
	})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/admin/users"},
		{"POST", "/v1/admin/users/u1/password"},
		{"PUT", "/v1/admin/settings/payment"},
		{"DELETE", "/v1/billing/plans/pro"},
		{"POST", "/v1/billing/balance/adjust"},
	} {
		if rec := do(r, tc.method, tc.path); rec.Code != 403 {
			t.Errorf("non-admin scope key on %s %s expected 403, got %d",
				tc.method, tc.path, rec.Code)
		}
	}
}

// "*" 全局通配也能覆盖 admin 操作（这是 * 的本意）。
func TestScopeMiddleware_GlobalWildcardCoversAdmin(t *testing.T) {
	r := newScopeEngine(t, "apikey", &auth.APIKeyRecord{Scopes: []string{"*"}})
	for _, tc := range []struct{ method, path string }{
		{"POST", "/v1/admin/users/u1/password"},
		{"PUT", "/v1/admin/settings/currency"},
		{"DELETE", "/v1/billing/plans/pro"},
	} {
		if rec := do(r, tc.method, tc.path); rec.Code != 200 {
			t.Errorf("* should grant %s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

// session 用户访问管理面不受 scope 限制（API Key 才查 scope）。
func TestScopeMiddleware_SessionUserHitsAdmin_Bypass(t *testing.T) {
	r := newScopeEngine(t, "session", nil)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/admin/users"},
		{"POST", "/v1/admin/users/u1/password"},
		{"PUT", "/v1/admin/settings/currency"},
	} {
		if rec := do(r, tc.method, tc.path); rec.Code != 200 {
			t.Errorf("session user should bypass scope on %s %s: %d",
				tc.method, tc.path, rec.Code)
		}
	}
}
