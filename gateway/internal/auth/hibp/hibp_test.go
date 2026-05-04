package hibp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// "password" 的 SHA1（大写）= 5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8
//
// HIBP range 接口给 prefix=5BAA6，本地匹配 suffix=1E4C9B93F3F0682250B6CF8331B7EE68FD8。
const passwordSHA1 = "5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8"

func TestSHA1Hex(t *testing.T) {
	if got := sha1Hex("password"); got != passwordSHA1 {
		t.Errorf("sha1: %q want %q", got, passwordSHA1)
	}
}

func TestLookup_FoundPwned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/5BAA6") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// 真实 HIBP 响应是 \r\n 分隔；测试中模拟两行
		w.Write([]byte("00000000000000000000000000000000000:1\r\n"))
		w.Write([]byte("1E4C9B93F3F0682250B6CF8331B7EE68FD8:99999\r\n"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	count, err := c.Lookup(context.Background(), "password")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if count != 99999 {
		t.Errorf("count: %d", count)
	}
}

func TestLookup_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意只返回不匹配的 suffix
		w.Write([]byte("ABCDEF1234567890ABCDEF1234567890ABC:5\r\n"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	count, err := c.Lookup(context.Background(), "password")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if count != 0 {
		t.Errorf("count: %d, expected 0", count)
	}
}

func TestIsPwned_True(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1E4C9B93F3F0682250B6CF8331B7EE68FD8:1\r\n"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	pwned, err := c.IsPwned(context.Background(), "password")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !pwned {
		t.Errorf("expected pwned=true")
	}
}

func TestIsPwned_False(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("\r\n")) // 空响应
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	pwned, err := c.IsPwned(context.Background(), "password")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if pwned {
		t.Errorf("expected pwned=false")
	}
}

func TestLookup_Empty(t *testing.T) {
	c := NewClient("http://localhost:9999/range/", nil)
	if _, err := c.Lookup(context.Background(), ""); err == nil {
		t.Errorf("expected error for empty password")
	}
}

func TestLookup_NetworkError(t *testing.T) {
	// 端口 1 几乎肯定 connection refused
	c := NewClient("http://127.0.0.1:1/range/", nil)
	_, err := c.Lookup(context.Background(), "password")
	if !errors.Is(err, ErrNetwork) {
		t.Errorf("expected ErrNetwork, got %v", err)
	}
}

func TestLookup_BadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	_, err := c.Lookup(context.Background(), "password")
	if !errors.Is(err, ErrNetwork) {
		t.Errorf("expected ErrNetwork on 429, got %v", err)
	}
}

func TestLookup_BadCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1E4C9B93F3F0682250B6CF8331B7EE68FD8:not_a_number\r\n"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	_, err := c.Lookup(context.Background(), "password")
	if !errors.Is(err, ErrParse) {
		t.Errorf("expected ErrParse, got %v", err)
	}
}

func TestLookup_AddPaddingHeader(t *testing.T) {
	gotHeader := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Add-Padding")
		w.Write([]byte("\r\n"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL+"/range/", nil)
	_, _ = c.Lookup(context.Background(), "password")
	if gotHeader != "true" {
		t.Errorf("Add-Padding header missing/wrong: %q", gotHeader)
	}
}

func TestParseRangeResponse_SkipsBadLines(t *testing.T) {
	body := strings.NewReader("\r\nbogusline\r\nABCDEF...:5\r\n1E4C9B93F3F0682250B6CF8331B7EE68FD8:42\r\n")
	count, err := parseRangeResponse(body, "1E4C9B93F3F0682250B6CF8331B7EE68FD8")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if count != 42 {
		t.Errorf("count: %d", count)
	}
}

func TestParseRangeResponse_CaseInsensitive(t *testing.T) {
	// 服务端可能返回小写
	body := strings.NewReader("1e4c9b93f3f0682250b6cf8331b7ee68fd8:7\r\n")
	count, err := parseRangeResponse(body, "1E4C9B93F3F0682250B6CF8331B7EE68FD8")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if count != 7 {
		t.Errorf("count: %d", count)
	}
}

func TestNewClient_Defaults(t *testing.T) {
	c := NewClient("", nil)
	if c.endpoint != DefaultEndpoint {
		t.Errorf("endpoint: %q", c.endpoint)
	}
	if c.http == nil {
		t.Errorf("http nil")
	}
}
