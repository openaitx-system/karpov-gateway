package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/auth/v1"
)

func TestAuthCookieResponseWriter_LoginSetsSidCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	w := AuthCookieResponseWriter(DefaultAuthCookieOptions())
	exp := time.Now().Add(7 * 24 * time.Hour)
	err := w(context.Background(), rec, &authv1.LoginResponse{
		Sid:       "yVf3yLDfbaW-RZ_bxDPJV_sqkPFcMDsWm1F2sF_5ryA",
		ExpiresAt: timestamppb.New(exp),
	})
	if err != nil {
		t.Fatalf("hook err: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d (%v)", len(cookies), rec.Header())
	}
	c := cookies[0]
	if c.Name != "sid" || c.Value == "" {
		t.Errorf("cookie name/value wrong: %+v", c)
	}
	if !c.HttpOnly {
		t.Error("must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite want Lax, got %v", c.SameSite)
	}
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge expected positive, got %d", c.MaxAge)
	}
}

func TestAuthCookieResponseWriter_TOTPRequiredSkipsCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	w := AuthCookieResponseWriter(DefaultAuthCookieOptions())
	_ = w(context.Background(), rec, &authv1.LoginResponse{
		Sid:           "",
		TotpRequired:  true,
		ChallengeId:   "ch1",
	})
	if cs := rec.Result().Cookies(); len(cs) != 0 {
		t.Errorf("totp_required must not set cookie, got %v", cs)
	}
}

func TestAuthCookieResponseWriter_TOTPResultSetsCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	w := AuthCookieResponseWriter(DefaultAuthCookieOptions())
	err := w(context.Background(), rec, &authv1.TOTPResult{
		Sid:       "after-totp",
		ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("hook err: %v", err)
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Value != "after-totp" {
		t.Errorf("totp result must set sid cookie: %v", cs)
	}
}

func TestAuthCookieResponseWriter_DisabledNoOp(t *testing.T) {
	rec := httptest.NewRecorder()
	opts := AuthCookieOptions{Disabled: true}
	w := AuthCookieResponseWriter(opts)
	_ = w(context.Background(), rec, &authv1.LoginResponse{
		Sid:       "x",
		ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
	})
	if cs := rec.Result().Cookies(); len(cs) != 0 {
		t.Errorf("Disabled must not write cookie, got %v", cs)
	}
}

func TestAuthCookieResponseWriter_NonAuthMessageIgnored(t *testing.T) {
	rec := httptest.NewRecorder()
	w := AuthCookieResponseWriter(DefaultAuthCookieOptions())
	_ = w(context.Background(), rec, &emptypb.Empty{})
	if cs := rec.Result().Cookies(); len(cs) != 0 {
		t.Errorf("non-auth message must not set cookie, got %v", cs)
	}
}

func TestAuthCookieClearMiddleware_LogoutClearsSid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthCookieClearMiddleware(DefaultAuthCookieOptions()))
	r.POST("/v1/auth/logout", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	r.ServeHTTP(rec, req)

	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "sid" || cs[0].MaxAge != -1 {
		t.Errorf("logout must clear sid cookie (Max-Age=-1), got %v", cs)
	}
}

func TestAuthCookieClearMiddleware_LogoutFailureNoClear(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthCookieClearMiddleware(DefaultAuthCookieOptions()))
	r.POST("/v1/auth/logout", func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	r.ServeHTTP(rec, req)

	if cs := rec.Result().Cookies(); len(cs) != 0 {
		t.Errorf("non-2xx logout must not clear cookie, got %v", cs)
	}
}

func TestAuthCookieClearMiddleware_OtherPathUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthCookieClearMiddleware(DefaultAuthCookieOptions()))
	r.POST("/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	r.ServeHTTP(rec, req)

	if cs := rec.Result().Cookies(); len(cs) != 0 {
		t.Errorf("login path must not be touched by clear middleware, got %v", cs)
	}
}

func TestAuthCookieResponseWriter_SecureFlag(t *testing.T) {
	rec := httptest.NewRecorder()
	opts := AuthCookieOptions{Secure: true, SameSite: http.SameSiteNoneMode}
	w := AuthCookieResponseWriter(opts)
	_ = w(context.Background(), rec, &authv1.LoginResponse{
		Sid:       "x",
		ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
	})
	cs := rec.Result().Cookies()
	if len(cs) != 1 || !cs[0].Secure || cs[0].SameSite != http.SameSiteNoneMode {
		t.Errorf("Secure/SameSite=None propagation: %+v", cs)
	}
}
