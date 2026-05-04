package payment

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

// LDC 文档 §1.3.1 的官方示例：
//
//	data = "client_id=1&money=10.00&order_name=Test&out_trade_no=M1&type=ldcpay" + client_secret
func TestLDCCanonical_DocFixture(t *testing.T) {
	params := map[string]string{
		"client_id":    "1",
		"money":        "10.00",
		"order_name":   "Test",
		"out_trade_no": "M1",
		"type":         "ldcpay",
	}
	got := ldcCanonical(params, "SECRET")
	want := "client_id=1&money=10.00&order_name=Test&out_trade_no=M1&type=ldcpaySECRET"
	if got != want {
		t.Fatalf("ldcCanonical mismatch:\n  got=  %q\n  want= %q", got, want)
	}
}

// 空值字段应该被剔除；sign 字段不参与
func TestLDCCanonical_SkipsEmptyAndSign(t *testing.T) {
	params := map[string]string{
		"client_id":    "abc",
		"type":         "ldcpay",
		"out_trade_no": "M1",
		"money":        "1.00",
		"order_name":   "X",
		"notify_url":   "", // 空：剔除
		"return_url":   "", // 空：剔除
		"sign":         "should-be-ignored",
	}
	got := ldcCanonical(params, "K")
	want := "client_id=abc&money=1.00&order_name=X&out_trade_no=M1&type=ldcpayK"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

// notify_url / return_url 非空时必须参与签名
func TestLDCCanonical_NotifyAndReturnIncluded(t *testing.T) {
	params := map[string]string{
		"client_id":    "1",
		"type":         "ldcpay",
		"out_trade_no": "M1",
		"money":        "10.00",
		"order_name":   "Test",
		"notify_url":   "https://example.com/notify",
		"return_url":   "https://example.com/return",
	}
	got := ldcCanonical(params, "S")
	// 字典序：client_id < money < notify_url < order_name < out_trade_no < return_url < type
	want := "client_id=1&money=10.00&notify_url=https://example.com/notify&order_name=Test&out_trade_no=M1&return_url=https://example.com/return&type=ldcpayS"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestSignVerifyEd25519_RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]string{
		"client_id":    "1",
		"money":        "10.00",
		"order_name":   "Test",
		"out_trade_no": "M1",
		"type":         "ldcpay",
	}
	sig := SignEd25519LDC(params, "SECRET", priv)
	if !VerifyEd25519LDC(params, "SECRET", pub, sig) {
		t.Fatal("VerifyEd25519LDC failed for round-trip signature")
	}
}

func TestVerifyEd25519_WrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	params := map[string]string{"client_id": "1", "type": "ldcpay"}
	sig := SignEd25519LDC(params, "S", priv)
	if VerifyEd25519LDC(params, "S", otherPub, sig) {
		t.Fatal("Verify must fail with unrelated public key")
	}
}

func TestVerifyEd25519_TamperedParams(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	params := map[string]string{"client_id": "1", "money": "10.00", "type": "ldcpay"}
	sig := SignEd25519LDC(params, "S", priv)

	tampered := map[string]string{"client_id": "1", "money": "9999.00", "type": "ldcpay"}
	if VerifyEd25519LDC(tampered, "S", pub, sig) {
		t.Fatal("Verify must fail when params change")
	}
}

func TestVerifyEd25519_TamperedSecret(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	params := map[string]string{"client_id": "1", "type": "ldcpay"}
	sig := SignEd25519LDC(params, "GOOD", priv)
	if VerifyEd25519LDC(params, "BAD", pub, sig) {
		t.Fatal("Verify must fail when client_secret differs")
	}
}

func TestVerifyEd25519_BadBase64(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	params := map[string]string{"client_id": "1"}
	if VerifyEd25519LDC(params, "S", pub, "!!!not-base64!!!") {
		t.Fatal("Verify must reject non-base64 signature")
	}
}

func TestVerifyEd25519_BadKeyLength(t *testing.T) {
	params := map[string]string{"client_id": "1"}
	if VerifyEd25519LDC(params, "S", ed25519.PublicKey{1, 2, 3}, "AAA") {
		t.Fatal("Verify must reject malformed public key")
	}
}

func TestParseEd25519PrivateKey_Seed(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	seed := priv.Seed()
	encoded := base64.StdEncoding.EncodeToString(seed)
	parsed, err := ParseEd25519PrivateKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Equal(parsed) {
		t.Fatal("Parsed seed key does not equal original")
	}
}

func TestParseEd25519PrivateKey_FullPriv(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	encoded := base64.StdEncoding.EncodeToString(priv)
	parsed, err := ParseEd25519PrivateKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Equal(parsed) {
		t.Fatal("Parsed full priv does not equal original")
	}
}

func TestParseEd25519PrivateKey_PEM(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	parsed, err := ParseEd25519PrivateKey(string(pemBytes))
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Equal(parsed) {
		t.Fatal("Parsed PEM key does not equal original")
	}
}

func TestParseEd25519PrivateKey_Errors(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"bad-base64":   "@@@",
		"wrong-length": base64.StdEncoding.EncodeToString([]byte("too-short")),
		"bad-pem":      "-----BEGIN PRIVATE KEY-----\nnot-valid\n-----END PRIVATE KEY-----",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEd25519PrivateKey(in); err == nil {
				t.Fatalf("%s: expected error, got nil", name)
			}
		})
	}
}

func TestParseEd25519PublicKey_Seed(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	encoded := base64.StdEncoding.EncodeToString(pub)
	parsed, err := ParseEd25519PublicKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(parsed) {
		t.Fatal("Parsed pub does not equal original")
	}
}

func TestParseEd25519PublicKey_PEM(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	parsed, err := ParseEd25519PublicKey(string(pemBytes))
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(parsed) {
		t.Fatal("Parsed PEM pub does not equal original")
	}
}

// 用户的实际场景：从 openssl pkey -pubout 生成的 PEM 文件中"只贴 base64 主体"
// （不带 BEGIN/END 头）。典型 44 字节 SPKI = 12 字节 ASN.1 前缀 + 32 字节公钥。
func TestParseEd25519PublicKey_BareDER(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	// 不加 PEM 头，只给 base64
	bare := base64.StdEncoding.EncodeToString(der)
	parsed, err := ParseEd25519PublicKey(bare)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(parsed) {
		t.Fatal("Parsed bare DER pub does not equal original")
	}
}

// LDC 文档示例风格：openssl 的真实输出，校验已知前缀 30 2a 30 05 06 03 2b 65 70 03 21 00。
func TestParseEd25519PublicKey_BareDER_KnownPrefix(t *testing.T) {
	const sample = "MCowBQYDK2VwAyEAzfTNosqk++QUcwfy4QdYOxH70OFGy8AbusZPXinbt5k="
	pub, err := ParseEd25519PublicKey(sample)
	if err != nil {
		t.Fatalf("ParseEd25519PublicKey(sample): %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("expected 32 bytes, got %d", len(pub))
	}
}

// PEM 主体经常有内嵌换行；解析器应当容忍。
func TestParseEd25519PublicKey_BareDER_WithNewlines(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	bare := base64.StdEncoding.EncodeToString(der)
	// 在 base64 中间插入一个换行 + 空格
	wrapped := bare[:20] + "\n  " + bare[20:]
	parsed, err := ParseEd25519PublicKey(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(parsed) {
		t.Fatal("Parsed multi-line bare DER pub does not equal original")
	}
}

// openssl genpkey -algorithm ed25519 产生的 PEM 主体；不带头粘贴也要能识别。
func TestParseEd25519PrivateKey_BareDER(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	bare := base64.StdEncoding.EncodeToString(der)
	parsed, err := ParseEd25519PrivateKey(bare)
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Equal(parsed) {
		t.Fatal("Parsed bare DER priv does not equal original")
	}
}

func TestParseEd25519PublicKey_Errors(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"bad-base64":   "@@@",
		"wrong-length": base64.StdEncoding.EncodeToString([]byte("short")),
		"bad-pem":      "-----BEGIN PUBLIC KEY-----\nnope\n-----END PUBLIC KEY-----",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseEd25519PublicKey(in); err == nil {
				t.Fatalf("%s: expected error, got nil", name)
			}
		})
	}
}

