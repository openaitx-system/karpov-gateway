package payment

import (
	"crypto/ed25519"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ldcCanonical 按 LDC 官方文档 §1.3.1 构造待签名字符串：
//
//  1. 取除 sign 以外的所有"非空"请求参数
//  2. 按参数名 ASCII 升序（字典序）
//  3. 用 k1=v1&k2=v2&... 拼接
//  4. 末尾"直接"追加 client_secret（不带 & 也不带 &key=，与易支付不同）
//
// 这一段同时用于两个方向：
//   - 商户 → LDC（用商户 ed25519 私钥签）
//   - LDC → 商户（用 LDC 平台 ed25519 私钥签，商户用平台公钥验）
func ldcCanonical(params map[string]string, clientSecret string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params[k])
	}
	sb.WriteString(clientSecret)
	return sb.String()
}

// SignEd25519LDC 用商户私钥按 LDC 协议签名，输出 StdEncoding base64 串。
func SignEd25519LDC(params map[string]string, clientSecret string, priv ed25519.PrivateKey) string {
	sig := ed25519.Sign(priv, []byte(ldcCanonical(params, clientSecret)))
	return base64.StdEncoding.EncodeToString(sig)
}

// VerifyEd25519LDC 用平台/对端公钥验签。constant-time 比较由 ed25519.Verify 保证。
func VerifyEd25519LDC(params map[string]string, clientSecret string, pub ed25519.PublicKey, sigBase64 string) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, []byte(ldcCanonical(params, clientSecret)), sig)
}

// EqualBase64 是 constant-time 比较两段 base64 串（用于诊断/日志，不直接用于安全决策）。
func EqualBase64(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ParseEd25519PrivateKey 自动识别以下 4 种私钥编码：
//
//   - PEM (PKCS#8)        ：标准 -----BEGIN PRIVATE KEY----- 块
//   - 裸 DER (PKCS#8)     ：把 PEM body 的 base64 单独贴进来（无 BEGIN/END 头）
//   - base64(32)          ：原始 seed
//   - base64(64)          ：完整的 ed25519 私钥（seed || pub）
//
// 同时容忍 StdEncoding 与 RawStdEncoding（带不带 = 填充）。
// 返回的 PrivateKey 长度始终为 ed25519.PrivateKeySize (64)。
func ParseEd25519PrivateKey(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("payment.ldc: empty private key")
	}

	// 1) PEM
	if strings.HasPrefix(s, "-----BEGIN") {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return nil, errors.New("payment.ldc: invalid PEM block")
		}
		return parsePKCS8Ed25519(block.Bytes)
	}

	// 2) 裸 base64（去掉所有内部空白符，方便兼容多行粘贴）
	raw, err := decodeBase64Lenient(s)
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: base64 decode private key: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize: // 32：原始 seed
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize: // 64：完整私钥
		return ed25519.PrivateKey(raw), nil
	}
	// 3) 兜底当作裸 DER PKCS#8 试一次（openssl genpkey 产生的 PEM body 就是这个，
	//    长度典型 48 字节）
	if priv, derErr := parsePKCS8Ed25519(raw); derErr == nil {
		return priv, nil
	}
	return nil, fmt.Errorf(
		"payment.ldc: unsupported private key encoding (got %d bytes after base64; "+
			"expected 32 seed / 64 full priv / PEM PKCS8 / 裸 DER PKCS8)", len(raw))
}

// ParseEd25519PublicKey 自动识别以下 3 种公钥编码：
//
//   - PEM (SPKI)         ：标准 -----BEGIN PUBLIC KEY----- 块
//   - 裸 DER (SPKI)      ：把 PEM body 的 base64 单独贴进来（无 BEGIN/END 头）
//                          典型 44 字节 = 12 字节 ASN.1 前缀 + 32 字节公钥
//   - base64(32)         ：原始 32 字节公钥
func ParseEd25519PublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("payment.ldc: empty public key")
	}

	// 1) PEM
	if strings.HasPrefix(s, "-----BEGIN") {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return nil, errors.New("payment.ldc: invalid PEM block")
		}
		return parsePKIXEd25519(block.Bytes)
	}

	// 2) 裸 base64
	raw, err := decodeBase64Lenient(s)
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: base64 decode public key: %w", err)
	}
	if len(raw) == ed25519.PublicKeySize { // 32：raw
		return ed25519.PublicKey(raw), nil
	}
	// 3) 兜底当作裸 DER PKIX（44 字节是 Ed25519 SPKI 的标准长度）
	if pub, derErr := parsePKIXEd25519(raw); derErr == nil {
		return pub, nil
	}
	return nil, fmt.Errorf(
		"payment.ldc: unsupported public key encoding (got %d bytes after base64; "+
			"expected 32 raw / PEM SPKI / 裸 DER SPKI)", len(raw))
}

// parsePKCS8Ed25519 把 DER 里 PKCS#8 包装的 ed25519 私钥提出来。
func parsePKCS8Ed25519(der []byte) (ed25519.PrivateKey, error) {
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: parse PKCS8: %w", err)
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("payment.ldc: DER key is not Ed25519")
	}
	return ed, nil
}

// parsePKIXEd25519 把 DER 里 SPKI 包装的 ed25519 公钥提出来。
func parsePKIXEd25519(der []byte) (ed25519.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: parse PKIX: %w", err)
	}
	ed, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("payment.ldc: DER key is not Ed25519")
	}
	return ed, nil
}

// decodeBase64Lenient 容忍 std / raw / url 三种 base64 编码，并在解码前先剔除内部空白
// （换行、空格、tab、CR），方便用户从带换行的 PEM 主体直接粘贴。
func decodeBase64Lenient(s string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
	if raw, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return raw, nil
	}
	if raw, err := base64.RawStdEncoding.DecodeString(cleaned); err == nil {
		return raw, nil
	}
	if raw, err := base64.URLEncoding.DecodeString(cleaned); err == nil {
		return raw, nil
	}
	if raw, err := base64.RawURLEncoding.DecodeString(cleaned); err == nil {
		return raw, nil
	}
	return nil, errors.New("not valid base64")
}
