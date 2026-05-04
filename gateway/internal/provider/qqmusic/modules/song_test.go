package modules

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

func newCli(t *testing.T, h http.HandlerFunc) (*qqmusic.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	httpc := qqmusic.NewHTTPClient(qqmusic.HTTPClientOptions{
		MaxRetries: 0, BackoffBase: 5 * time.Millisecond, MaxConnections: 4,
		Timeout: 2 * time.Second, HTTP2: false,
	})
	c := qqmusic.NewClient(qqmusic.ClientOptions{
		HTTP: httpc, Platform: qqmusic.PlatformAndroid, MaxConcurrency: 4,
		Credential: &qqmusic.Credential{},
		MusicuURL:  srv.URL,
	})
	return c, srv
}

func TestQuerySongByMID(t *testing.T) {
	var seenPayload map[string]any
	c, _ := newCli(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seenPayload)
		_, _ = w.Write([]byte(`{"req_0":{"code":0,"data":{"tracks":[{"mid":"M1"},{"mid":"M2"}]}}}`))
	})
	resp, err := QuerySongByMID(context.Background(), c, []string{"M1", "M2"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Tracks) != 2 {
		t.Errorf("tracks len: %d", len(resp.Tracks))
	}
	r0 := seenPayload["req_0"].(map[string]any)
	if r0["module"] != "music.trackInfo.UniformRuleCtrl" {
		t.Errorf("module: %v", r0["module"])
	}
	param := r0["param"].(map[string]any)
	if mids, ok := param["mids"].([]any); !ok || len(mids) != 2 {
		t.Errorf("mids: %v", param["mids"])
	}
}

func TestSearchByType(t *testing.T) {
	var seen map[string]any
	c, _ := newCli(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seen)
		_, _ = w.Write([]byte(`{"req_0":{"code":0,"data":{"total_num":42,"nextpage":2,"body":{"song":{"list":[{"id":1},{"id":2}]}}}}}`))
	})
	resp, err := SearchByType(context.Background(), c, "周杰伦", SearchByTypeOptions{Page: 1, Num: 20})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Total != 42 {
		t.Errorf("total: %d", resp.Total)
	}
	if !resp.HasMore {
		t.Errorf("has_more should be true")
	}
	if len(resp.List) != 2 {
		t.Errorf("list: %d", len(resp.List))
	}
	r0 := seen["req_0"].(map[string]any)
	param := r0["param"].(map[string]any)
	if param["query"] != "周杰伦" {
		t.Errorf("query: %v", param["query"])
	}
	if param["search_type"] != float64(0) {
		t.Errorf("search_type: %v", param["search_type"])
	}
	// 强制 platform=android 时 comm 应含 tmeAppID
	comm := seen["comm"].(map[string]any)
	if _, ok := comm["tmeAppID"]; !ok {
		t.Errorf("comm missing tmeAppID; comm=%v", comm)
	}
}
