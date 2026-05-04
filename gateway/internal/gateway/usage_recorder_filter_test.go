package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

// recordingTestEngine 复刻 server.go 里 UsageRecorder 中间件的过滤逻辑，
// 但用一个易测的 fake recorder 取代真正的 UsageHandler。这样可以专门验证
// "哪些路径会被记录，哪些被丢弃"，而不需要 PG / Redis。
type fakeRecorder struct {
	mu      sync.Mutex
	calls   atomic.Int64
	lastRec recordEntry
}

type recordEntry struct {
	UserID   string
	Provider string
}

func (f *fakeRecorder) RecordRequest(userID, prov string, _ int, _ int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Add(1)
	f.lastRec = recordEntry{UserID: userID, Provider: prov}
}

// recorderFilterMiddleware 是 server.go 里 UsageRecorder use block 的可测拷贝。
// 我们没法直接调那块匿名 closure，所以这里用同样语义重写一份做单元测试。
func recorderFilterMiddleware(rec *fakeRecorder, knownProviders []string) gin.HandlerFunc {
	known := make(map[string]struct{}, len(knownProviders))
	for _, n := range knownProviders {
		if n != "" {
			known[n] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		c.Next()
		if len(known) == 0 {
			return
		}
		p := c.Request.URL.Path
		if !strings.HasPrefix(p, "/v1/") ||
			strings.HasPrefix(p, "/v1/admin/") ||
			strings.HasPrefix(p, "/v1/auth/") ||
			strings.HasPrefix(p, "/v1/billing/") ||
			strings.HasPrefix(p, "/v1/usage/") ||
			strings.HasPrefix(p, "/v1/config/") ||
			strings.HasPrefix(p, "/v1/docs/") {
			return
		}
		parts := strings.SplitN(strings.TrimPrefix(p, "/v1/"), "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			return
		}
		prov := parts[0]
		if _, ok := known[prov]; !ok {
			return
		}
		userID := c.Request.Header.Get("X-User-Id")
		rec.RecordRequest(userID, prov, 1, 0)
	}
}

func newFilterEngine(t *testing.T, providers []string) (*gin.Engine, *fakeRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := &fakeRecorder{}
	r := gin.New()
	r.Use(recorderFilterMiddleware(rec, providers))
	r.NoRoute(func(c *gin.Context) { c.String(200, "ok") })
	return r, rec
}

func reqAt(r *gin.Engine, path string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-User-Id", "u1")
	r.ServeHTTP(w, req)
}

// 已注册 provider 应该被记录。
func TestUsageFilter_KnownProviderRecorded(t *testing.T) {
	r, rec := newFilterEngine(t, []string{"qqmusic", "netease"})
	reqAt(r, "/v1/qqmusic/songs/123")
	reqAt(r, "/v1/netease/songs/abc")
	if got := rec.calls.Load(); got != 2 {
		t.Fatalf("expected 2 records, got %d", got)
	}
	if rec.lastRec.Provider != "netease" || rec.lastRec.UserID != "u1" {
		t.Errorf("last rec = %+v", rec.lastRec)
	}
}

// /v1/v1/foo 不应该被记成 provider="v1"。
func TestUsageFilter_RejectsDuplicatedV1Prefix(t *testing.T) {
	r, rec := newFilterEngine(t, []string{"qqmusic", "netease"})
	reqAt(r, "/v1/v1/songs/123")
	reqAt(r, "/v1/api/somepath")
	reqAt(r, "/v1/foo/bar")
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("garbage providers should be filtered, got %d records", got)
	}
}

// 管理面 / 自服务路径必须跳过。
func TestUsageFilter_AdminAuthBillingSkipped(t *testing.T) {
	r, rec := newFilterEngine(t, []string{"qqmusic", "netease"})
	for _, p := range []string{
		"/v1/admin/users",
		"/v1/auth/me",
		"/v1/billing/orders",
		"/v1/usage/history",
		"/v1/config/runtime",
		"/v1/docs/openapi.yaml",
	} {
		reqAt(r, p)
	}
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("management paths should not record, got %d", got)
	}
}

// 非 /v1/ 前缀（healthz / api 等）不记录。
func TestUsageFilter_NonV1NotRecorded(t *testing.T) {
	r, rec := newFilterEngine(t, []string{"qqmusic"})
	reqAt(r, "/healthz")
	reqAt(r, "/readyz")
	reqAt(r, "/api/proxy/qqmusic/songs/x") // 这种不该出现，但即使出现也不要记
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("non-/v1 should not record, got %d", got)
	}
}

// 空 provider list = 关闭录制（防御默认）。
func TestUsageFilter_EmptyProvidersDisablesRecording(t *testing.T) {
	r, rec := newFilterEngine(t, nil)
	reqAt(r, "/v1/qqmusic/songs/1")
	reqAt(r, "/v1/netease/songs/2")
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("empty providers should disable, got %d", got)
	}
}

// 路径段为空（/v1/ 后什么也没有）不记录。
func TestUsageFilter_EmptyPathSegment(t *testing.T) {
	r, rec := newFilterEngine(t, []string{"qqmusic"})
	reqAt(r, "/v1/")
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("empty path segment should not record, got %d", got)
	}
}
