package payment

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func mustLDC(t *testing.T, baseURL string, withPlatformPub bool) (*LDC, ed25519.PublicKey, ed25519.PrivateKey, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	merchantPub, merchantPriv, _ := ed25519.GenerateKey(rand.Reader)
	platformPub, platformPriv, _ := ed25519.GenerateKey(rand.Reader)
	cfg := LDCConfig{
		BaseURL:            baseURL,
		ClientID:           "client-id",
		ClientSecret:       "secret-xyz",
		MerchantPrivateKey: merchantPriv,
	}
	if withPlatformPub {
		cfg.PlatformPublicKey = platformPub
	}
	p, err := NewLDC(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p, merchantPub, merchantPriv, platformPub, platformPriv
}

func TestNewLDC_Validation(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	cases := []struct {
		name string
		cfg  LDCConfig
	}{
		{"missing client_id", LDCConfig{ClientSecret: "s", MerchantPrivateKey: priv}},
		{"missing secret", LDCConfig{ClientID: "id", MerchantPrivateKey: priv}},
		{"bad priv length", LDCConfig{ClientID: "id", ClientSecret: "s", MerchantPrivateKey: ed25519.PrivateKey{1, 2, 3}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewLDC(c.cfg); err == nil {
				t.Fatalf("%s: expected error", c.name)
			}
		})
	}
}

func TestLDC_CreatePayment_Success(t *testing.T) {
	var got struct {
		method      string
		contentType string
		params      url.Values
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.contentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		got.params = r.PostForm
		// 模拟官方成功响应：302 跳到认证页面
		w.Header().Set("Location", "https://credit.linux.do/paying?order_no=LDC123")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	p, merchantPub, _, _, _ := mustLDC(t, srv.URL, false)

	res, err := p.CreatePayment(context.Background(), CreateRequest{
		OrderID:   "M2025001",
		Amount:    decimal.RequireFromString("12.34"),
		Subject:   "Test 商品",
		NotifyURL: "https://shop.example.com/notify",
		ReturnURL: "https://shop.example.com/done",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if res.PayURL != "https://credit.linux.do/paying?order_no=LDC123" {
		t.Errorf("unexpected PayURL: %q", res.PayURL)
	}

	// 验证我们发出去的请求体
	if got.method != http.MethodPost {
		t.Errorf("method: got %s want POST", got.method)
	}
	if !strings.HasPrefix(got.contentType, "application/x-www-form-urlencoded") {
		t.Errorf("content-type: %s", got.contentType)
	}
	if got.params.Get("client_id") != "client-id" ||
		got.params.Get("type") != "ldcpay" ||
		got.params.Get("out_trade_no") != "M2025001" ||
		got.params.Get("money") != "12.34" ||
		got.params.Get("order_name") != "Test 商品" ||
		got.params.Get("notify_url") != "https://shop.example.com/notify" ||
		got.params.Get("return_url") != "https://shop.example.com/done" {
		t.Errorf("missing field(s): %+v", got.params)
	}
	sig := got.params.Get("sign")
	if sig == "" {
		t.Fatal("sign missing")
	}
	// 用商户公钥反向验证签名（自签自验）
	verifyParams := map[string]string{}
	for k := range got.params {
		verifyParams[k] = got.params.Get(k)
	}
	if !VerifyEd25519LDC(verifyParams, "secret-xyz", merchantPub, sig) {
		t.Fatal("signature does not verify with merchant public key")
	}
}

func TestLDC_CreatePayment_GatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"error_msg":"签名验证失败","data":null}`)
	}))
	defer srv.Close()

	p, _, _, _, _ := mustLDC(t, srv.URL, false)
	_, err := p.CreatePayment(context.Background(), CreateRequest{
		OrderID: "M1",
		Amount:  decimal.RequireFromString("1.00"),
		Subject: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "签名验证失败") {
		t.Fatalf("expected gateway error, got %v", err)
	}
}

func TestLDC_CreatePayment_302WithoutLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound) // 302 但没 Location
	}))
	defer srv.Close()

	p, _, _, _, _ := mustLDC(t, srv.URL, false)
	_, err := p.CreatePayment(context.Background(), CreateRequest{
		OrderID: "M1",
		Amount:  decimal.RequireFromString("1.00"),
		Subject: "x",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLDC_CreatePayment_MissingField(t *testing.T) {
	p, _, _, _, _ := mustLDC(t, "http://nope.invalid", false)
	if _, err := p.CreatePayment(context.Background(), CreateRequest{
		Amount: decimal.RequireFromString("1.00"),
	}); !errors.Is(err, ErrMissingField) {
		t.Fatalf("expected ErrMissingField, got %v", err)
	}
}

func TestLDC_VerifyCallback_WithPlatformKey_Success(t *testing.T) {
	p, _, _, _, platformPriv := mustLDC(t, "", true)

	params := map[string]string{
		"pid":          "client-id",
		"trade_no":     "T1",
		"out_trade_no": "M1",
		"type":         "ldcpay",
		"name":         "X",
		"money":        "10.00",
		"trade_status": "TRADE_SUCCESS",
	}
	params["sign"] = SignEd25519LDC(params, "secret-xyz", platformPriv)

	res, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params})
	if err != nil {
		t.Fatalf("VerifyCallback: %v", err)
	}
	if !res.Success {
		t.Error("Success should be true for TRADE_SUCCESS")
	}
	if res.OrderID != "M1" || res.ExternalTxnID != "T1" {
		t.Errorf("ids wrong: %+v", res)
	}
	if !res.Amount.Equal(decimal.RequireFromString("10.00")) {
		t.Errorf("amount: %s", res.Amount)
	}
	if !strings.EqualFold(res.GatewayResponse, "success") {
		t.Errorf("ack wrong: %q", res.GatewayResponse)
	}
}

func TestLDC_VerifyCallback_WithPlatformKey_BadSig(t *testing.T) {
	p, _, _, _, _ := mustLDC(t, "", true)
	// 签名是用错误的 priv 生成的
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	params := map[string]string{
		"pid":          "client-id",
		"trade_no":     "T1",
		"out_trade_no": "M1",
		"type":         "ldcpay",
		"money":        "10.00",
		"trade_status": "TRADE_SUCCESS",
	}
	params["sign"] = SignEd25519LDC(params, "secret-xyz", otherPriv)

	_, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params})
	if !errors.Is(err, ErrInvalidSign) {
		t.Fatalf("expected ErrInvalidSign, got %v", err)
	}
}

func TestLDC_VerifyCallback_BadMoney(t *testing.T) {
	// money 字段非法 → 必须先签名通过才会到金额校验，所以这里用合法 MD5 签
	p, _, _, _, _ := mustLDC(t, "", false)
	params := map[string]string{
		"out_trade_no": "M1",
		"money":        "not-a-number",
		"trade_status": "TRADE_SUCCESS",
	}
	params["sign"] = SignMD5Yipay(params, "secret-xyz")
	if _, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params}); err == nil {
		t.Fatal("expected error for bad money")
	}
}

func TestLDC_VerifyCallback_EmptyParams(t *testing.T) {
	p, _, _, _, _ := mustLDC(t, "", false)
	if _, err := p.VerifyCallback(context.Background(), CallbackRequest{}); !errors.Is(err, ErrMissingField) {
		t.Fatalf("expected ErrMissingField, got %v", err)
	}
}

func TestLDC_VerifyCallback_FailureStatus(t *testing.T) {
	// MD5 路径下，bad-sign 也会被识别为合法 hex 但验签失败。改用合法 MD5 让测试聚焦 status。
	p, _, _, _, _ := mustLDC(t, "", false)
	params := map[string]string{
		"out_trade_no": "M1",
		"money":        "10.00",
		"trade_status": "TRADE_FAILED",
	}
	params["sign"] = SignMD5Yipay(params, "secret-xyz")
	res, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Error("non-TRADE_SUCCESS must yield Success=false")
	}
}

func TestLDC_VerifyCallback_MD5_Success(t *testing.T) {
	// 默认 LDC 实际签法：MD5 + client_secret，无需平台公钥
	p, _, _, _, _ := mustLDC(t, "", false)
	params := map[string]string{
		"pid":          "client-id",
		"trade_no":     "T1",
		"out_trade_no": "M1",
		"type":         "ldcpay",
		"name":         "X",
		"money":        "10.00",
		"trade_status": "TRADE_SUCCESS",
	}
	params["sign"] = SignMD5Yipay(params, "secret-xyz")
	res, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params})
	if err != nil {
		t.Fatalf("MD5 callback: %v", err)
	}
	if !res.Success || res.OrderID != "M1" || res.ExternalTxnID != "T1" {
		t.Errorf("got %+v", res)
	}
}

func TestLDC_VerifyCallback_MD5_BadSig(t *testing.T) {
	p, _, _, _, _ := mustLDC(t, "", false)
	params := map[string]string{
		"out_trade_no": "M1",
		"money":        "10.00",
		"trade_status": "TRADE_SUCCESS",
		// 32 个 hex 字符但是错的
		"sign": "00000000000000000000000000000000",
	}
	if _, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params}); !errors.Is(err, ErrInvalidSign) {
		t.Fatalf("expected ErrInvalidSign for bad MD5, got %v", err)
	}
}

// 收到 Ed25519 形式签名（典型 88 字符 base64）但平台公钥没配置 → 应当拒绝且报错信息可定位
func TestLDC_VerifyCallback_Ed25519_NoPlatformKey_Rejects(t *testing.T) {
	p, _, _, _, _ := mustLDC(t, "", false) // false = 没配置 PlatformPublicKey
	params := map[string]string{
		"pid":          "client-id",
		"out_trade_no": "M1",
		"money":        "10.00",
		"trade_status": "TRADE_SUCCESS",
		// 88 个字符 base64（不是 32 位 hex），强制走 Ed25519 路径
		"sign": "iEJyf3kKJWxAyQymphGM44dZAuSL5UpxnFsJ8eTVCt5LE0M9qd1kTjIOmEHGM/X3vICUnDNTl8b1fkW6X+OjBA==",
	}
	_, err := p.VerifyCallback(context.Background(), CallbackRequest{Params: params})
	if err == nil {
		t.Fatal("must reject Ed25519 callback when no platform pub")
	}
	// 错误消息要含可定位关键词，便于运维排查
	if msg := err.Error(); !strings.Contains(msg, "platform public key not configured") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestLDC_isMD5Hex32(t *testing.T) {
	cases := map[string]bool{
		"d41d8cd98f00b204e9800998ecf8427e":          true,  // 标准 MD5
		"D41D8CD98F00B204E9800998ECF8427E":          true,  // 大写也接受
		"d41d8cd98f00b204e9800998ecf8427":           false, // 31 字符
		"d41d8cd98f00b204e9800998ecf8427ee":         false, // 33 字符
		"d41d8cd98f00b204e9800998ecf8427g":          false, // 含 g
		"":                                          false,
		"iEJyf3kKJWxAyQymphGM44dZAuSL5UpxnFsJ8eTV":  false, // 一段 base64，非 32 hex
	}
	for in, want := range cases {
		if got := isMD5Hex32(in); got != want {
			t.Errorf("isMD5Hex32(%q) = %v, want %v", in, got, want)
		}
	}
}
