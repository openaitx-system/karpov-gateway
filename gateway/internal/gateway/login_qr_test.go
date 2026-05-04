package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	qqmodules "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/modules"
)

func TestNewSessionID_LengthAndUnique(t *testing.T) {
	a := newSessionID()
	b := newSessionID()
	if len(a) != 64 || len(b) != 64 {
		t.Errorf("session ID must be 64 hex chars, got %d/%d", len(a), len(b))
	}
	if a == b {
		t.Error("two session IDs collided")
	}
}

func TestQRLoginAdminHandler_StartRejectsBadPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mgr := NewQRLoginManager(qqmusic.NewClient(qqmusic.ClientOptions{}))
	NewQRLoginAdminHandler(mgr).Mount(r)

	for _, p := range []string{"", "google", "weibo", "QQ", "WX"} {
		body, _ := json.Marshal(qrStartRequest{Platform: p})
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/login/qr/start", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("platform %q: want 400, got %d", p, rec.Code)
		}
	}
}

func TestQRLoginAdminHandler_PollUnknownSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	mgr := NewQRLoginManager(qqmusic.NewClient(qqmusic.ClientOptions{}))
	NewQRLoginAdminHandler(mgr).Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/login/qr/deadbeef", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown session: want 404, got %d", rec.Code)
	}
}

func TestMapModuleEvent_AllValues(t *testing.T) {
	cases := []struct {
		in   qqmodules.QRLoginEvent
		want QRLoginEvent
	}{
		{qqmodules.QREventDone, QREventDone},
		{qqmodules.QREventScan, QREventWaiting},
		{qqmodules.QREventConf, QREventScanned},
		{qqmodules.QREventTimeout, QREventTimeout},
		{qqmodules.QREventRefuse, QREventRefuse},
		{qqmodules.QREventOther, QREventOther},
	}
	for _, tc := range cases {
		if got := mapModuleEvent(tc.in); got != tc.want {
			t.Errorf("event %v → %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestQRStartResponse_Schema(t *testing.T) {
	body, _ := json.Marshal(qrStartResponse{
		SessionID: "abc", Image: "data:...", ExpiresInSec: 180, Platform: "qq",
	})
	want := []byte(`{"session_id":"abc","image":"data:...","expires_in_sec":180,"platform":"qq"}`)
	if !bytes.Equal(body, want) {
		t.Errorf("schema drift:\n got=%s\nwant=%s", body, want)
	}
}

func TestQRPollResponse_OmitsEmptyCredential(t *testing.T) {
	body, _ := json.Marshal(qrPollResponse{Event: "scan"})
	if bytes.Contains(body, []byte("credential")) {
		t.Errorf("scan event must omit credential, got %s", body)
	}
	body2, _ := json.Marshal(qrPollResponse{Event: "done", Credential: map[string]any{"musickey": "X"}})
	if !bytes.Contains(body2, []byte(`"musickey":"X"`)) {
		t.Errorf("done event must include credential, got %s", body2)
	}
}
