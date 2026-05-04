package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newRuntimeConfigEngine(t *testing.T, cfg RuntimeConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewRuntimeConfigHandler(cfg).Mount(r)
	return r
}

func TestRuntimeConfigHandler_ReturnsConfiguredTZ(t *testing.T) {
	r := newRuntimeConfigEngine(t, RuntimeConfig{TimeZone: "Asia/Shanghai"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/config/runtime", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	body := decodeOK(t, rec)
	tz, _ := body["timezone"].(string)
	if tz != "Asia/Shanghai" {
		t.Errorf("timezone=%q want Asia/Shanghai", tz)
	}
}

func TestRuntimeConfigHandler_DefaultsToLocal(t *testing.T) {
	// 空配置应回填 "Local"，避免前端拿到空串后误判
	r := newRuntimeConfigEngine(t, RuntimeConfig{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/config/runtime", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	body := decodeOK(t, rec)
	if tz, _ := body["timezone"].(string); tz != "Local" {
		t.Errorf("timezone=%q want Local", tz)
	}
}

// 响应体形如 {"code":0,"message":"...","data":{...}}；这里把 data 拆出来。
func decodeOK(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("envelope missing data: %s", rec.Body.String())
	}
	return data
}
