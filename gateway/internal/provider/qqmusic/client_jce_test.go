package qqmusic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/jce"
)

// 构造一个能 echo Decode 后返回 JceResponse 的 mock：
// 解码请求 → 构造 ResponseItem(code=0, data=copy of param) → encode 写回。
func newJCEEchoServer(t *testing.T, captured *jce.JceRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req jce.JceRequest
		if err := req.Decode(body); err != nil {
			http.Error(w, "decode req: "+err.Error(), 400)
			return
		}
		if captured != nil {
			*captured = req
		}
		// echo: req_0 -> resp_0 with same param tags
		respItems := make([]jce.DataResponseItem, 0, len(req.Data))
		for _, di := range req.Data {
			respItems = append(respItems, jce.DataResponseItem{
				Key: di.Key,
				Item: jce.JceResponseItem{
					Code: 0,
					Data: di.Item.Param,
				},
			})
		}
		out := jce.JceResponse{Code: 0, Data: respItems}
		raw, err := out.Encode()
		if err != nil {
			http.Error(w, "encode resp: "+err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newJCETestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	httpc := NewHTTPClient(HTTPClientOptions{
		MaxRetries: 0, BackoffBase: 5 * time.Millisecond, MaxConnections: 4,
		Timeout: 2 * time.Second, HTTP2: false,
	})
	c := NewClient(ClientOptions{
		HTTP: httpc, Platform: PlatformAndroid, MaxConcurrency: 4,
		Credential: &Credential{MusicID: 99, MusicKey: "K"},
		// JCE 使用独立 URL（client_jce.go 的 JCEURL 默认值），这里通过 opts.URL 注入
	})
	_ = srv
	return c
}

func TestRequestJCE_RoundTrip(t *testing.T) {
	var seen jce.JceRequest
	srv := newJCEEchoServer(t, &seen)
	c := newJCETestClient(t, srv)

	items := []JCERequestItem{{
		Module: "music.vkey.GetVkey",
		Method: "UrlGetVkey",
		Param: []jce.ParamEntry{
			{Tag: 0, Kind: jce.KindString, Str: "songmid_xxx"},
			{Tag: 1, Kind: jce.KindInt, Int: 320000},
			{Tag: 2, Kind: jce.KindBytes, Bytes: []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		},
	}}
	resp, err := c.RequestJCE(context.Background(), items, JCEOptions{URL: srv.URL})
	if err != nil {
		t.Fatalf("RequestJCE: %v", err)
	}
	if resp.Code != 0 {
		t.Errorf("resp.Code: %d", resp.Code)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("resp.Data len: %d", len(resp.Data))
	}
	got := resp.Data[0]
	if got.Key != "req_0" {
		t.Errorf("key: %q", got.Key)
	}
	if len(got.Item.Data) != 3 {
		t.Fatalf("echoed entries: %d", len(got.Item.Data))
	}
	// 校验三种 Kind 均正确穿越编/解码
	tagToEntry := map[byte]jce.ParamEntry{}
	for _, p := range got.Item.Data {
		tagToEntry[p.Tag] = p
	}
	if tagToEntry[0].Str != "songmid_xxx" {
		t.Errorf("tag0 string lost: %+v", tagToEntry[0])
	}
	if tagToEntry[1].Int != 320000 {
		t.Errorf("tag1 int lost: %+v", tagToEntry[1])
	}
	if string(tagToEntry[2].Bytes) != string([]byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Errorf("tag2 bytes lost: %+v", tagToEntry[2])
	}

	// 校验 server 收到的 comm 含 Android 字段（tmeAppID）
	commMap := map[string]string{}
	for _, kv := range seen.Comm {
		commMap[kv.K] = kv.V
	}
	if commMap["tmeAppID"] != "qqmusic" {
		t.Errorf("comm missing tmeAppID: %+v", commMap)
	}
	if commMap["authst"] != "K" {
		t.Errorf("authst: %q", commMap["authst"])
	}
}

func TestRequestJCE_HeadersAndCookies(t *testing.T) {
	var seenHeaders http.Header
	var seenCookies map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		seenCookies = map[string]string{}
		for _, ck := range r.Cookies() {
			seenCookies[ck.Name] = ck.Value
		}
		// 返回最小可解码的 JceResponse
		out := jce.JceResponse{Code: 0}
		raw, _ := out.Encode()
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	c := newJCETestClient(t, srv)
	_, err := c.RequestJCE(context.Background(),
		[]JCERequestItem{{Module: "m", Method: "x", Param: nil}},
		JCEOptions{URL: srv.URL},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if seenHeaders.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("content-type: %q", seenHeaders.Get("Content-Type"))
	}
	if seenHeaders.Get("X-Sign-Data-Type") != "jce" {
		t.Errorf("x-sign-data-type missing: %q", seenHeaders.Get("X-Sign-Data-Type"))
	}
	if seenCookies["qm_keyst"] != "K" {
		t.Errorf("cookies missing qm_keyst: %+v", seenCookies)
	}
}

func TestRequestJCE_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	c := newJCETestClient(t, srv)
	_, err := c.RequestJCE(context.Background(),
		[]JCERequestItem{{Module: "m", Method: "x", Param: nil}},
		JCEOptions{URL: srv.URL},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*HTTPStatusError); !ok {
		t.Fatalf("wrong type: %T %v", err, err)
	}
}

func TestRequestJCE_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}))
	t.Cleanup(srv.Close)
	c := newJCETestClient(t, srv)
	_, err := c.RequestJCE(context.Background(),
		[]JCERequestItem{{Module: "m", Method: "x", Param: nil}},
		JCEOptions{URL: srv.URL},
	)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if _, ok := err.(*JCEParseError); !ok {
		t.Fatalf("wrong type: %T %v", err, err)
	}
}
