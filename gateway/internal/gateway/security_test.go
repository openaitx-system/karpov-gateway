package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestSecurityHeaders_Defaults(t *testing.T) {
	r := gin.New()
	r.Use(SecurityHeaders(DefaultSecurityHeadersOptions()))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(rec, req)

	wantHeaders := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"Content-Security-Policy":   "default-src 'self'",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Permissions-Policy":        "geolocation=(), microphone=(), camera=()",
	}
	for k, want := range wantHeaders {
		got := rec.Header().Get(k)
		if got != want {
			t.Errorf("%s: got %q, want %q", k, got, want)
		}
	}
}

func TestSecurityHeaders_HSTSDisable(t *testing.T) {
	opts := DefaultSecurityHeadersOptions()
	opts.HSTSDisable = true

	r := gin.New()
	r.Use(SecurityHeaders(opts))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Errorf("HSTS should be empty when disabled, got %q", rec.Header().Get("Strict-Transport-Security"))
	}
	// 其他头应仍写入
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("XFO missing")
	}
}

func TestSecurityHeaders_HSTSPreload(t *testing.T) {
	opts := DefaultSecurityHeadersOptions()
	opts.HSTSPreload = true

	r := gin.New()
	r.Use(SecurityHeaders(opts))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	got := rec.Header().Get("Strict-Transport-Security")
	if !strings.Contains(got, "preload") {
		t.Errorf("preload missing: %q", got)
	}
}

func TestCSRF_GET_SetsCookie(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(DefaultCSRFOptions()))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if rec.Code != 200 {
		t.Errorf("status: %d", rec.Code)
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "csrf_token=") {
		t.Errorf("cookie missing: %q", cookie)
	}
}

func TestCSRF_POST_NoToken_403(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(DefaultCSRFOptions()))
	r.POST("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))

	if rec.Code != 403 {
		t.Errorf("status: %d, want 403", rec.Code)
	}
}

func TestCSRF_POST_MatchedToken_OK(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(DefaultCSRFOptions()))
	r.POST("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "MYTOK"})
	req.Header.Set("X-CSRF-Token", "MYTOK")
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestCSRF_POST_TokenMismatch_403(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(DefaultCSRFOptions()))
	r.POST("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", nil)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "AAA"})
	req.Header.Set("X-CSRF-Token", "BBB")
	r.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("status: %d, want 403", rec.Code)
	}
}

func TestCSRF_SkipsHealthz(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(DefaultCSRFOptions()))
	r.POST("/healthz", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/healthz", nil))

	if rec.Code != 200 {
		t.Errorf("/healthz POST should be allowed: %d", rec.Code)
	}
}

func TestCSRF_APIKey_Bypass(t *testing.T) {
	r := gin.New()
	r.Use(CSRF(DefaultCSRFOptions()))
	r.POST("/x", func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("X-API-Key", "mk_abc")
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("API Key holder should bypass CSRF: %d", rec.Code)
	}
}

func TestCSRF_TokenIsRandom(t *testing.T) {
	tokens := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		tok, err := newCSRFToken()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, dup := tokens[tok]; dup {
			t.Errorf("duplicate token at iter %d: %q", i, tok)
		}
		tokens[tok] = struct{}{}
	}
}

func TestItoa_BasicCases(t *testing.T) {
	cases := map[int]string{
		0:        "0",
		1:        "1",
		31536000: "31536000",
		-7:       "-7",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d): %q, want %q", in, got, want)
		}
	}
}
