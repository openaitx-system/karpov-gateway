package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/MiChongs/QQMusicApi/gateway/internal/quota"
)

func newQuotaTestStack(t *testing.T) (*gin.Engine, *quota.Service, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	qsvc := quota.NewService(rdb, quota.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC) },
	})
	return gin.New(), qsvc, mr
}

func TestQuotaMiddleware_Allow(t *testing.T) {
	r, qsvc, _ := newQuotaTestStack(t)

	resolver := NewSimpleQuotaResolver(100, 1000, 80, 1)
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"

	r.Use(QuotaMiddleware(QuotaMiddlewareOptions{
		Service:  qsvc,
		Resolver: resolver,
	}))
	r.GET("/v1/qqmusic/songs/:id", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/qqmusic/songs/M_demo", nil)
	req.Header.Set("X-User-Id", "user-1")
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status: %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Quota-Soft-Limit") != "" {
		t.Errorf("soft-limit header should be absent on Allow")
	}
}

func TestQuotaMiddleware_HardLimit(t *testing.T) {
	r, qsvc, _ := newQuotaTestStack(t)

	// month_limit=2，第三次必然 HardLimit
	resolver := NewSimpleQuotaResolver(100, 2, 0, 1)
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"

	r.Use(QuotaMiddleware(QuotaMiddlewareOptions{Service: qsvc, Resolver: resolver}))
	r.GET("/v1/qqmusic/songs/:id", func(c *gin.Context) { c.String(200, "ok") })

	doReq := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/qqmusic/songs/M_x", nil)
		req.Header.Set("X-User-Id", "user-hard")
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := doReq(); rec.Code != 200 {
		t.Errorf("first: %d", rec.Code)
	}
	if rec := doReq(); rec.Code != 200 {
		t.Errorf("second: %d", rec.Code)
	}
	rec3 := doReq()
	if rec3.Code != 429 {
		t.Errorf("third: %d, want 429", rec3.Code)
	}
	retryAfter := rec3.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Errorf("Retry-After missing")
	}
	if n, _ := strconv.Atoi(retryAfter); n < 1 {
		t.Errorf("Retry-After non-positive: %q", retryAfter)
	}
}

func TestQuotaMiddleware_SoftLimit(t *testing.T) {
	r, qsvc, _ := newQuotaTestStack(t)

	// month_limit=10，soft_pct=80。第 9 次开始软限。
	resolver := NewSimpleQuotaResolver(100, 10, 80, 1)
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"

	r.Use(QuotaMiddleware(QuotaMiddlewareOptions{Service: qsvc, Resolver: resolver}))
	r.GET("/v1/qqmusic/songs/:id", func(c *gin.Context) { c.String(200, "ok") })

	for i := 0; i < 8; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/qqmusic/songs/M", nil)
		req.Header.Set("X-User-Id", "user-soft")
		r.ServeHTTP(rec, req)
		if rec.Header().Get("X-Quota-Soft-Limit") != "" {
			t.Errorf("iter %d should not be soft-limited yet", i)
		}
	}
	// 第 9 次：month_used=9 → 9*100 > 10*80 → SoftLimit
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/qqmusic/songs/M", nil)
	req.Header.Set("X-User-Id", "user-soft")
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("soft limit should pass through (200), got %d", rec.Code)
	}
	if rec.Header().Get("X-Quota-Soft-Limit") != "1" {
		t.Errorf("soft-limit header missing: %v", rec.Header())
	}
	if rec.Header().Get("X-Quota-Retry-After-Ms") == "" {
		t.Errorf("retry-after-ms missing")
	}
}

func TestQuotaMiddleware_NoUserSkip(t *testing.T) {
	r, qsvc, _ := newQuotaTestStack(t)

	resolver := NewSimpleQuotaResolver(100, 1, 0, 1) // month_limit=1，但无 user 应跳过
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"

	r.Use(QuotaMiddleware(QuotaMiddlewareOptions{Service: qsvc, Resolver: resolver}))
	r.GET("/v1/qqmusic/songs/:id", func(c *gin.Context) { c.String(200, "ok") })

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		// 不传 X-User-Id → resolver 返回 (_, false) → middleware 跳过
		req := httptest.NewRequest("GET", "/v1/qqmusic/songs/M", nil)
		r.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("iter %d: status=%d, want 200 (skip when no user)", i, rec.Code)
		}
	}
}

func TestQuotaMiddleware_SkipPaths(t *testing.T) {
	r, qsvc, _ := newQuotaTestStack(t)

	resolver := NewSimpleQuotaResolver(100, 1, 0, 1)
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"

	r.Use(QuotaMiddleware(QuotaMiddlewareOptions{
		Service:   qsvc,
		Resolver:  resolver,
		SkipPaths: []string{"/healthz", "/v1/auth/"},
	}))
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.POST("/v1/auth/login", func(c *gin.Context) { c.String(200, "ok") })

	for _, p := range []struct{ method, path string }{
		{"GET", "/healthz"},
		{"POST", "/v1/auth/login"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(p.method, p.path, nil)
		req.Header.Set("X-User-Id", "user-skip")
		r.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s %s: %d", p.method, p.path, rec.Code)
		}
	}
}

func TestQuotaMiddleware_RedisFailFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr() // 先记下地址
	mr.Close()        // 拔 Redis 模拟故障

	rdb := redis.NewClient(&redis.Options{
		Addr: addr, DialTimeout: 100 * time.Millisecond,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	qsvc := quota.NewService(rdb, quota.Options{})

	r := gin.New()
	resolver := NewSimpleQuotaResolver(100, 1, 0, 1)
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"
	r.Use(QuotaMiddleware(QuotaMiddlewareOptions{Service: qsvc, Resolver: resolver}))
	r.GET("/v1/qqmusic/songs/:id", func(c *gin.Context) { c.String(200, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/qqmusic/songs/M", nil)
	req.Header.Set("X-User-Id", "user-fail")
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("Redis failure should fail-open (200), got %d", rec.Code)
	}
	if rec.Header().Get("X-Quota-Error") != "1" {
		t.Errorf("expected X-Quota-Error header, got %q", rec.Header().Get("X-Quota-Error"))
	}
}

func TestSimpleQuotaResolver_PathParse(t *testing.T) {
	resolver := NewSimpleQuotaResolver(100, 1000, 80, 1)
	resolver.EndpointMap["GET /v1/qqmusic/songs/"] = "GetSong"
	resolver.EndpointMap["GET /v1/qqmusic/search/"] = "SearchSongs"

	cases := []struct {
		method, path     string
		wantProv, wantEp string
	}{
		{"GET", "/v1/qqmusic/songs/M_demo", "qqmusic", "GetSong"},
		{"GET", "/v1/qqmusic/search/jay", "qqmusic", "SearchSongs"},
		{"GET", "/v1/qqmusic/unknown/x", "", ""},
		{"POST", "/v1/qqmusic/songs/x", "", ""}, // 方法不匹配
		{"GET", "/health", "", ""},
	}
	for _, c := range cases {
		prov, ep := resolver.parsePath(c.method, c.path)
		if prov != c.wantProv || ep != c.wantEp {
			t.Errorf("%s %s → (%q,%q), want (%q,%q)", c.method, c.path, prov, ep, c.wantProv, c.wantEp)
		}
	}
}

// 确保编译时 *http.Request 被使用（避免 import 误删）
var _ = (*http.Request)(nil)
