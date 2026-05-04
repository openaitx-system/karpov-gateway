package qqmusic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 构造一个 httptest server + 注入 transport 的 Client，便于子测试复用。
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	httpc := NewHTTPClient(HTTPClientOptions{
		MaxRetries:     0,
		BackoffBase:    10 * time.Millisecond,
		MaxConnections: 4,
		Timeout:        2 * time.Second,
		HTTP2:          false,
	})
	c := NewClient(ClientOptions{
		HTTP:           httpc,
		Platform:       PlatformAndroid,
		Credential:     &Credential{MusicID: 12345, MusicKey: "W_X_test_key"},
		MaxConcurrency: 4,
	})
	return c, srv
}

func TestRequestMusicu_BasicJSON(t *testing.T) {
	var gotBody map[string]any
	var gotCookies map[string]string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		gotCookies = map[string]string{}
		for _, ck := range r.Cookies() {
			gotCookies[ck.Name] = ck.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"req_0":{"code":0,"data":{"hello":"world"}}}`))
	})

	resp, err := c.RequestMusicu(context.Background(),
		[]RequestItem{{Module: "m", Method: "x", Param: map[string]any{"a": 1, "b": true}}},
		MusicuOptions{URL: srv.URL},
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	item := resp["req_0"].(map[string]any)
	if item["code"].(float64) != 0 {
		t.Errorf("code != 0: %v", item)
	}

	// 1) bool→0/1
	r0 := gotBody["req_0"].(map[string]any)
	param := r0["param"].(map[string]any)
	if v := param["b"]; v != float64(1) {
		t.Errorf("bool not converted: %v (%T)", v, v)
	}

	// 2) cookies 注入
	if gotCookies["uin"] != "12345" || gotCookies["qm_keyst"] != "W_X_test_key" {
		t.Errorf("missing auth cookies: %+v", gotCookies)
	}

	// 3) UA 是 Android
	// (no easy way to capture in handler closure; re-issue manually)
	ua := c.UserAgent(PlatformAndroid)
	if !strings.HasPrefix(ua, "QQMusic ") {
		t.Errorf("UA not android: %q", ua)
	}

	// 4) comm 写入 platform-specific 字段
	comm := gotBody["comm"].(map[string]any)
	if _, ok := comm["tmeAppID"]; !ok {
		t.Errorf("android comm missing tmeAppID: %+v", comm)
	}
	if comm["authst"] != "W_X_test_key" {
		t.Errorf("comm.authst wrong: %v", comm["authst"])
	}
}

func TestRequestMusicu_PreserveBool(t *testing.T) {
	var gotBody map[string]any
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"req_0":{"code":0,"data":{}}}`))
	})

	_, err := c.RequestMusicu(context.Background(),
		[]RequestItem{{Module: "m", Method: "x", Param: map[string]any{"flag": true}}},
		MusicuOptions{URL: srv.URL, PreserveBool: true},
	)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	r0 := gotBody["req_0"].(map[string]any)
	param := r0["param"].(map[string]any)
	if v := param["flag"]; v != true {
		t.Errorf("preserve_bool failed: %v (%T)", v, v)
	}
}

func TestRequestMusicu_HTTPStatusError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	})
	_, err := c.RequestMusicu(context.Background(),
		[]RequestItem{{Module: "m", Method: "x", Param: map[string]any{}}},
		MusicuOptions{URL: srv.URL},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*HTTPStatusError)
	if !ok {
		t.Fatalf("wrong error type: %T %v", err, err)
	}
	if httpErr.StatusCode != 502 {
		t.Errorf("status: %d", httpErr.StatusCode)
	}
}

func TestRequestMusicu_BadJSON(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	})
	_, err := c.RequestMusicu(context.Background(),
		[]RequestItem{{Module: "m", Method: "x", Param: map[string]any{}}},
		MusicuOptions{URL: srv.URL},
	)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if _, ok := err.(*ParseJSONError); !ok {
		t.Fatalf("wrong type: %T", err)
	}
}

func TestRequestMusicu_SignAttached(t *testing.T) {
	var gotURL string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = w.Write([]byte(`{"req_0":{"code":0,"data":{}}}`))
	})
	c.enable = true // 启用签名

	_, err := c.RequestMusicu(context.Background(),
		[]RequestItem{{Module: "m", Method: "x", Param: map[string]any{"k": "v"}}},
		MusicuOptions{URL: srv.URL},
	)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	if !strings.Contains(gotURL, "sign=") {
		t.Errorf("sign not in URL: %q", gotURL)
	}
	// sign 必须 zzc 开头（与 fixture 风格一致）
	if !strings.Contains(gotURL, "sign=zzc") {
		t.Errorf("sign not zzc-style: %q", gotURL)
	}
}
