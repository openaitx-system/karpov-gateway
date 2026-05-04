package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// stubResolver 实现 SessionResolver；按 sid 返回固定 user 或错误。
type stubResolver struct {
	users map[string]*auth.User
	err   error
}

func (s *stubResolver) VerifySession(_ context.Context, sid string) (*auth.User, *auth.Session, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	if u, ok := s.users[sid]; ok {
		return u, &auth.Session{SID: sid, UserID: u.ID}, nil
	}
	return nil, nil, auth.ErrSessionNotFound
}

func TestSessionMiddleware_Cookie(t *testing.T) {
	r := gin.New()
	resolver := &stubResolver{users: map[string]*auth.User{
		"valid-sid": {ID: "u_1", Email: "a@b.c"},
	}}
	r.Use(SessionMiddleware(SessionMiddlewareOptions{Resolver: resolver}))
	r.GET("/x", func(c *gin.Context) {
		c.String(200, c.Request.Header.Get("X-User-Id"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "valid-sid"})
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status: %d", rec.Code)
	}
	if rec.Body.String() != "u_1" {
		t.Errorf("body: %q want u_1", rec.Body.String())
	}
}

func TestSessionMiddleware_HeaderFallback(t *testing.T) {
	r := gin.New()
	resolver := &stubResolver{users: map[string]*auth.User{
		"hdr-sid": {ID: "u_2"},
	}}
	r.Use(SessionMiddleware(SessionMiddlewareOptions{Resolver: resolver}))
	r.GET("/x", func(c *gin.Context) {
		c.String(200, c.Request.Header.Get("X-User-Id"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Session-Id", "hdr-sid")
	r.ServeHTTP(rec, req)

	if rec.Body.String() != "u_2" {
		t.Errorf("body: %q", rec.Body.String())
	}
}

func TestSessionMiddleware_NoSessionRequired_401(t *testing.T) {
	r := gin.New()
	r.Use(SessionMiddleware(SessionMiddlewareOptions{
		Resolver: &stubResolver{}, Required: true,
	}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 401 {
		t.Errorf("status: %d, want 401", rec.Code)
	}
}

func TestSessionMiddleware_NoSessionOptional_PassThrough(t *testing.T) {
	r := gin.New()
	r.Use(SessionMiddleware(SessionMiddlewareOptions{
		Resolver: &stubResolver{}, Required: false,
	}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 200 {
		t.Errorf("status: %d, want 200", rec.Code)
	}
}

func TestSessionMiddleware_BadSessionRequired_401(t *testing.T) {
	r := gin.New()
	r.Use(SessionMiddleware(SessionMiddlewareOptions{
		Resolver: &stubResolver{}, Required: true,
	}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "unknown-sid"})
	r.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status: %d, want 401", rec.Code)
	}
}

func TestSessionMiddleware_SkipPaths(t *testing.T) {
	r := gin.New()
	r.Use(SessionMiddleware(SessionMiddlewareOptions{
		Resolver: &stubResolver{}, Required: true,
		SkipPaths: []string{"/v1/auth/login", "/healthz"},
	}))
	r.POST("/v1/auth/login", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })

	for _, p := range []struct{ method, path string }{
		{"POST", "/v1/auth/login"},
		{"GET", "/healthz"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(p.method, p.path, nil))
		if rec.Code != 200 {
			t.Errorf("%s %s should be skipped: %d", p.method, p.path, rec.Code)
		}
	}
}

func TestSessionMiddleware_ResolverError(t *testing.T) {
	r := gin.New()
	r.Use(SessionMiddleware(SessionMiddlewareOptions{
		Resolver: &stubResolver{err: errors.New("redis fail")},
		Required: true,
	}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "any"})
	r.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status: %d, want 401 (Required=true 时 Redis 故障也是 401)", rec.Code)
	}
}

func TestAdminAuthMiddleware_Match(t *testing.T) {
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{
		AdminTokens: []string{"secret-token-1", "secret-token-2"},
	}))
	r.GET("/v1/admin/pool/health/qq", func(c *gin.Context) { c.String(200, "ok") })

	for _, tok := range []string{"secret-token-1", "secret-token-2"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/admin/pool/health/qq", nil)
		req.Header.Set("X-Admin-Key", tok)
		r.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("token %q: %d", tok, rec.Code)
		}
	}
}

func TestAdminAuthMiddleware_Missing_401(t *testing.T) {
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{
		AdminTokens: []string{"x"},
	}))
	r.GET("/v1/admin/anything", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/admin/anything", nil))
	if rec.Code != 401 {
		t.Errorf("status: %d, want 401", rec.Code)
	}
}

func TestAdminAuthMiddleware_Wrong_403(t *testing.T) {
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{
		AdminTokens: []string{"correct"},
	}))
	r.GET("/v1/admin/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/x", nil)
	req.Header.Set("X-Admin-Key", "wrong")
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("status: %d, want 403", rec.Code)
	}
}

func TestAdminAuthMiddleware_NoTokens_403(t *testing.T) {
	// AdminTokens 空 → admin 路由全部 403（disabled）
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{}))
	r.GET("/v1/admin/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/x", nil)
	req.Header.Set("X-Admin-Key", "any")
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("status: %d, want 403 (admin disabled)", rec.Code)
	}
}

func TestAdminAuthMiddleware_NotAdminPath_PassThrough(t *testing.T) {
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{}))
	r.GET("/v1/auth/me", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/auth/me", nil))
	if rec.Code != 200 {
		t.Errorf("non-admin path should pass: %d", rec.Code)
	}
}

func TestAdminAuthMiddleware_SessionRole_Admin(t *testing.T) {
	// X-User-Role=admin（来自 SessionMiddleware）应 200，无需 X-Admin-Key。
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{
		AdminTokens:       []string{"svc-token"},
		AcceptSessionRole: true,
	}))
	r.GET("/v1/admin/x", func(c *gin.Context) { c.String(200, "ok") })

	for _, role := range []string{"admin", "superadmin"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/admin/x", nil)
		req.Header.Set("X-User-Role", role)
		r.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("role %s: %d", role, rec.Code)
		}
	}
}

func TestAdminAuthMiddleware_SessionRole_RegularUser_403(t *testing.T) {
	r := gin.New()
	r.Use(AdminAuthMiddleware(AdminAuthOptions{
		AdminTokens:       []string{"svc-token"},
		AcceptSessionRole: true,
	}))
	r.GET("/v1/admin/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/x", nil)
	req.Header.Set("X-User-Role", "user")
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("user role should be 403, got %d", rec.Code)
	}
}

// admin token 命中 → 强制注入 superadmin 角色，让下游 SuperadminPathMiddleware
// 也认这条认证路径（部署级共享密钥 = 最高权限）。
func TestAdminAuthMiddleware_TokenSetsSuperadminRole(t *testing.T) {
	r := gin.New()
	var capturedRole string
	var capturedHeader string
	var capturedVia string
	r.Use(AdminAuthMiddleware(AdminAuthOptions{
		AdminTokens: []string{"svc-token"},
	}))
	r.GET("/v1/admin/x", func(c *gin.Context) {
		if v, ok := c.Get("auth.role"); ok {
			capturedRole, _ = v.(string)
		}
		capturedHeader = c.Request.Header.Get("X-User-Role")
		if v, ok := c.Get("auth.via"); ok {
			capturedVia, _ = v.(string)
		}
		c.String(200, "ok")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/admin/x", nil)
	req.Header.Set("X-Admin-Key", "svc-token")
	// 故意先放一个低于 superadmin 的角色，验证 admin token 命中后会覆盖它
	req.Header.Set("X-User-Role", "user")
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("admin token should pass: %d", rec.Code)
	}
	if capturedRole != "superadmin" {
		t.Errorf("auth.role should be superadmin, got %q", capturedRole)
	}
	if capturedHeader != "superadmin" {
		t.Errorf("X-User-Role header should be superadmin, got %q", capturedHeader)
	}
	if capturedVia != "admin_token" {
		t.Errorf("auth.via should be admin_token, got %q", capturedVia)
	}
}

func TestRequireRole_Allow(t *testing.T) {
	r := gin.New()
	r.Use(RequireRole(RequireRoleOptions{AllowedRoles: []string{"admin", "superadmin"}}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-User-Role", "admin")
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestRequireRole_Forbidden(t *testing.T) {
	r := gin.New()
	r.Use(RequireRole(RequireRoleOptions{AllowedRoles: []string{"admin"}}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-User-Role", "user")
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("expected 403 for non-admin, got %d", rec.Code)
	}
}

func TestRequireRole_NoRole_401(t *testing.T) {
	r := gin.New()
	r.Use(RequireRole(RequireRoleOptions{AllowedRoles: []string{"admin"}}))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 401 {
		t.Errorf("expected 401 without role, got %d", rec.Code)
	}
}

func TestSessionMiddleware_InjectsUserAndRole(t *testing.T) {
	r := gin.New()
	resolver := &stubResolver{users: map[string]*auth.User{
		"valid-sid": {ID: "u_admin", Email: "a@b.c", Role: auth.RoleAdmin},
	}}
	r.Use(SessionMiddleware(SessionMiddlewareOptions{Resolver: resolver}))
	r.GET("/x", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"id":   c.Request.Header.Get("X-User-Id"),
			"role": c.Request.Header.Get("X-User-Role"),
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "valid-sid"})
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	if !contains(rec.Body.String(), "u_admin") || !contains(rec.Body.String(), "admin") {
		t.Errorf("body missing id+role: %s", rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})())
}
