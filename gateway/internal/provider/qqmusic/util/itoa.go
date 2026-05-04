package util

import "strconv"

// ItoA 等价 strconv.FormatInt(n, 10)；保持单一入口便于 IDE 跳转。
func ItoA(n int64) string {
	return strconv.FormatInt(n, 10)
}
