// Package util 提供 qqmusic provider 的小型工具函数
// （MD5 链式拼接、hash33、guid、bool→int 等），与 Python qqmusic_api/utils/common.py 对齐。
package util

import (
	"crypto/md5" //nolint:gosec // QQ 音乐协议明确要求 MD5
	"encoding/hex"
	"fmt"
)

// CalcMD5 把所有参数顺序 update 进同一个 md5 上下文，返回小写 hex 摘要。
//
// 等价于 Python qqmusic_api.utils.common.calc_md5：
// - string → utf8 bytes
// - []byte → 直接 update
// 任何其它类型会 panic（与 Python TypeError 对齐）。
//
// 调用示例：CalcMD5("k", "p", "1700000000000", "n", "secret", `{"appKey":"x"}`)
func CalcMD5(parts ...any) string {
	h := md5.New() //nolint:gosec
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			_, _ = h.Write([]byte(v))
		case []byte:
			_, _ = h.Write(v)
		default:
			panic(fmt.Sprintf("util.CalcMD5: unsupported type %T", p))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
