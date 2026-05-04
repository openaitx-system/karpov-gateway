package payment

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strings"
)

// SignMD5Yipay 是易支付 (epay) 风格签名：
//
//  1. 排除 sign / sign_type 与值为空的字段
//  2. 按 key 字典序升序
//  3. 拼成 k1=v1&k2=v2&... 字符串
//  4. 末尾直接拼接商户密钥（不加 &）
//  5. MD5 → 32 位小写 hex
//
// 该签名方案被绝大多数易支付分叉使用；具体商户文档可能有细节差异，
// 商家集成前必须用沙箱验证一次（用 fixture_test.go 录一份 golden）。
func SignMD5Yipay(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
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
	sb.WriteString(key)

	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// SignMD5Hupijiao 是虎皮椒 (hupijiao) 风格签名：
//
//  1. 排除 hash 与值为空的字段
//  2. 按 key 字典序升序（注意：虎皮椒区分大小写，且包含数字时数字前置）
//  3. 拼成 k1=v1&k2=v2&...&appsecret=KEY
//  4. MD5 → 32 位小写 hex
//
// 与 yipay 的差别：(a) 排除字段名是 "hash" 而非 "sign"；(b) 密钥
// 用 "&appsecret=KEY" 形式拼接而非裸密钥。
func SignMD5Hupijiao(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "hash" || v == "" {
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
	sb.WriteString("&appsecret=")
	sb.WriteString(key)

	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// VerifyMD5 是 constant-time 比较两个 hex sign（避免时序攻击）。
func VerifyMD5(want, got string) bool {
	if len(want) != len(got) {
		return false
	}
	var diff byte
	for i := 0; i < len(want); i++ {
		a := want[i]
		b := got[i]
		// 大小写归一
		if a >= 'A' && a <= 'Z' {
			a += 32
		}
		if b >= 'A' && b <= 'Z' {
			b += 32
		}
		diff |= a ^ b
	}
	return diff == 0
}
