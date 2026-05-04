// Package algorithms 提供 QQ 音乐 API 请求所需的纯算法（签名/3DES）。
//
// 1:1 移植自 qqmusic_api/algorithms/。
package algorithms

import (
	"crypto/sha1" //nolint:gosec // QQ 音乐协议明确要求 SHA1
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// part1Indexes / part2Indexes / scrambleValues 与
// qqmusic_api/algorithms/sign.py:9-32 完全一致。
//
// part1Indexes 在 Python 端用 `tuple(i for i in (23,14,6,36,16,40,7,19) if i < 40)`
// 计算；其中 40 被过滤掉，故实际只有 7 个索引。
var part1Indexes = [...]int{23, 14, 6, 36, 16, 7, 19}

var part2Indexes = [...]int{16, 1, 32, 12, 19, 27, 8, 5}

var scrambleValues = [20]byte{
	89, 39, 179, 150, 218, 82, 58, 252, 177, 52,
	186, 123, 120, 64, 242, 133, 143, 161, 121, 179,
}

// SignFromDigest 基于 40 字符的大写 hex SHA1 摘要计算 zzc 签名。
//
// 与 _sign_from_digest_python 完全等价（详见 sign.py:35-45）：
//  1. 按 part1Indexes / part2Indexes 取摘要字符
//  2. 对 20 个 scramble 字节与摘要每两字符的 hex 整数 XOR
//  3. base64 编码 20 字节 → 去除 \\/+= → 与 part1/part2 拼成 "zzc{p1}{b64}{p2}" → 转小写
//
// 入参必须是 40 字符全大写 hex；其它情况返回错误。
func SignFromDigest(digest string) (string, error) {
	if len(digest) != 40 {
		return "", fmt.Errorf("algorithms: digest length must be 40, got %d", len(digest))
	}

	var p1, p2 strings.Builder
	p1.Grow(len(part1Indexes))
	p2.Grow(len(part2Indexes))
	for _, i := range part1Indexes {
		p1.WriteByte(digest[i])
	}
	for _, i := range part2Indexes {
		p2.WriteByte(digest[i])
	}

	var part3 [20]byte
	for i, sv := range scrambleValues {
		v, err := strconv.ParseUint(digest[i*2:i*2+2], 16, 8)
		if err != nil {
			return "", fmt.Errorf("algorithms: parse digest hex pair %q: %w", digest[i*2:i*2+2], err)
		}
		part3[i] = sv ^ byte(v)
	}

	b64 := base64.StdEncoding.EncodeToString(part3[:])
	b64 = stripURLUnsafe(b64)

	var out strings.Builder
	out.Grow(3 + p1.Len() + len(b64) + p2.Len())
	out.WriteString("zzc")
	out.WriteString(p1.String())
	out.WriteString(b64)
	out.WriteString(p2.String())
	return strings.ToLower(out.String()), nil
}

// SignRequestFromBytes 计算 sign，输入是已序列化的 JSON payload 字节。
//
// 调用方负责保证 marshaler 顺序与 Python orjson.dumps 一致（dict 插入顺序、
// 不转义 HTML、不排序 key），否则两端 SHA1 摘要会不同；
// 上层 transport 应使用受控的 ordered marshaler。
func SignRequestFromBytes(payload []byte) (string, error) {
	sum := sha1.Sum(payload) //nolint:gosec
	digest := strings.ToUpper(hex.EncodeToString(sum[:]))
	return SignFromDigest(digest)
}

// stripURLUnsafe 去除 base64 输出中的 "/", "+", "=", "\\" 四种字符，
// 与 Python `re.sub(rb"[\\/+=]", b"", b64encode(...))` 等价。
func stripURLUnsafe(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' || c == '+' || c == '=' || c == '\\' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
