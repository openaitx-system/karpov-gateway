package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDocsHandler_ServesYAML(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewDocsHandler().Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/v1/docs/openapi.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "yaml") {
		t.Fatalf("content-type = %q, want yaml", ct)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "openapi:") {
		head := body
		if len(head) > 60 {
			head = head[:60]
		}
		t.Fatalf("body should start with 'openapi:', got prefix %q", head)
	}
	// 几个关键路径必须出现，证明完整规格已经被 embed 进来
	for _, want := range []string{
		"/v1/auth/login",
		"/v1/billing/balance",
		"/v1/billing/extra-usage",
		"/v1/admin/users",
		"/v1/{provider}/songs/{id}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("openapi.yaml missing path %q", want)
		}
	}
}

