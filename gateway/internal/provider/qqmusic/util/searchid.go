package util

import (
	"crypto/rand"
	"encoding/binary"
	"strconv"
	"time"
)

// SearchID 等价 Python qqmusic_api.utils.common.get_searchID。
//
// 公式：t = e * 2^54 + n * 2^32 + r
//
//	e = randint(1, 20)
//	n = randint(0, 2^22)
//	r = round(now_ms) % (24*60*60*1000)
//
// 结果为 64-bit 正整数的十进制字符串。Go 端用 crypto/rand 取随机以
// 避免非线程安全的 math/rand 默认源；分布与 Python 等价。
func SearchID() string {
	e := randIntInRange(1, 20)
	n := randIntInRange(0, 1<<22)
	const dayMs = int64(24 * 60 * 60 * 1000)
	r := time.Now().UnixMilli() % dayMs
	v := int64(e)*(1<<54) + int64(n)*(1<<32) + r
	return strconv.FormatInt(v, 10)
}

// randIntInRange 返回 [lo, hi]（含两端）内的均匀随机整数；hi >= lo。
func randIntInRange(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	span := uint64(hi - lo + 1)
	var b [8]byte
	_, _ = rand.Read(b[:])
	u := binary.BigEndian.Uint64(b[:])
	return lo + int(u%span)
}
