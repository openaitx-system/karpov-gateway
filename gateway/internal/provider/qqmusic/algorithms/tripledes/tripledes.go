// SPDX-FileCopyrightText: Copyright (c) 2024 沉默の金 <cmzj@cmzj.org>
// SPDX-License-Identifier: GPL-3.0-only
//
// 1:1 移植自 qqmusic_api/algorithms/tripledes.py。
package tripledes

import "fmt"

// Schedule 16 轮 round key（每轮 6 字节）。
type Schedule [16][6]byte

// TripleSchedule 3DES 三段 schedule：
//
//	ENCRYPT 模式 = [E(k0..7), D(k8..15), E(k16..23)]   // EDE
//	DECRYPT 模式 = [D(k16..23), E(k8..15), D(k0..7)]   // DED
type TripleSchedule [3]Schedule

// KeySetup 从 24 字节主 key 推 TripleSchedule。
//
// 等价于 Python tripledes_key_setup（tripledes.py:571-583）。
func KeySetup(key []byte, mode Mode) (TripleSchedule, error) {
	var ts TripleSchedule
	if len(key) != 24 {
		return ts, fmt.Errorf("tripledes: key length must be 24, got %d", len(key))
	}
	if mode == Encrypt {
		ts[0] = keySchedule(key[0:8], Encrypt)
		ts[1] = keySchedule(key[8:16], Decrypt)
		ts[2] = keySchedule(key[16:24], Encrypt)
		return ts, nil
	}
	ts[0] = keySchedule(key[16:24], Decrypt)
	ts[1] = keySchedule(key[8:16], Encrypt)
	ts[2] = keySchedule(key[0:8], Decrypt)
	return ts, nil
}

// Crypt 处理单个 8 字节 block，应用三轮 DES（EDE 或 DED 由 sched 决定）。
//
// 等价于 Python tripledes_crypt（tripledes.py:586-598）。
func Crypt(block [8]byte, sched TripleSchedule) [8]byte {
	out := block
	for i := 0; i < 3; i++ {
		out = singleCrypt(out, sched[i])
	}
	return out
}

// ---------- 内部实现 ----------

// bitnum 与 tripledes.py:67-78 等价。
//
// 从字节数组 a 中按位索引 b 取出一位，然后左移 c 后返回。
// b 范围：0..63（对应 8 字节 = 64 位）。
func bitnum(a [8]byte, b, c uint) uint32 {
	idx := (b/32)*4 + 3 - (b%32)/8
	return uint32((a[idx]>>(7-b%8))&1) << c
}

// bitnumIntR 与 bitnum_intr 等价。从 32-bit 整数 a 中按 MSB 索引 b 取一位，左移 c。
func bitnumIntR(a uint32, b, c uint) uint32 {
	return ((a >> (31 - b)) & 1) << c
}

// bitnumIntL 与 bitnum_intl 等价。
//
// Python 公式 ((a << b) & 0x80000000) >> c：把第 b 位（自高位起编号 0..31）
// 提取到 bit 31，再右移 c。Go 中 a 是 uint32，左移 b 后高位被截掉，行为与 Python
// 等价（因为掩码 0x80000000 只取 bit 31）。
func bitnumIntL(a uint32, b, c uint) uint32 {
	return ((a << b) & 0x80000000) >> c
}

// sboxBit 与 sbox_bit 等价：(a&32) | ((a&31)>>1) | ((a&1)<<4)
func sboxBit(a byte) byte {
	return (a & 32) | ((a & 31) >> 1) | ((a & 1) << 4)
}

// initialPermutation 等价 tripledes.py:121-199。
func initialPermutation(in [8]byte) (uint32, uint32) {
	s1 := bitnum(in, 57, 31) | bitnum(in, 49, 30) | bitnum(in, 41, 29) | bitnum(in, 33, 28) |
		bitnum(in, 25, 27) | bitnum(in, 17, 26) | bitnum(in, 9, 25) | bitnum(in, 1, 24) |
		bitnum(in, 59, 23) | bitnum(in, 51, 22) | bitnum(in, 43, 21) | bitnum(in, 35, 20) |
		bitnum(in, 27, 19) | bitnum(in, 19, 18) | bitnum(in, 11, 17) | bitnum(in, 3, 16) |
		bitnum(in, 61, 15) | bitnum(in, 53, 14) | bitnum(in, 45, 13) | bitnum(in, 37, 12) |
		bitnum(in, 29, 11) | bitnum(in, 21, 10) | bitnum(in, 13, 9) | bitnum(in, 5, 8) |
		bitnum(in, 63, 7) | bitnum(in, 55, 6) | bitnum(in, 47, 5) | bitnum(in, 39, 4) |
		bitnum(in, 31, 3) | bitnum(in, 23, 2) | bitnum(in, 15, 1) | bitnum(in, 7, 0)

	s2 := bitnum(in, 56, 31) | bitnum(in, 48, 30) | bitnum(in, 40, 29) | bitnum(in, 32, 28) |
		bitnum(in, 24, 27) | bitnum(in, 16, 26) | bitnum(in, 8, 25) | bitnum(in, 0, 24) |
		bitnum(in, 58, 23) | bitnum(in, 50, 22) | bitnum(in, 42, 21) | bitnum(in, 34, 20) |
		bitnum(in, 26, 19) | bitnum(in, 18, 18) | bitnum(in, 10, 17) | bitnum(in, 2, 16) |
		bitnum(in, 60, 15) | bitnum(in, 52, 14) | bitnum(in, 44, 13) | bitnum(in, 36, 12) |
		bitnum(in, 28, 11) | bitnum(in, 20, 10) | bitnum(in, 12, 9) | bitnum(in, 4, 8) |
		bitnum(in, 62, 7) | bitnum(in, 54, 6) | bitnum(in, 46, 5) | bitnum(in, 38, 4) |
		bitnum(in, 30, 3) | bitnum(in, 22, 2) | bitnum(in, 14, 1) | bitnum(in, 6, 0)

	return s1, s2
}

// inversePermutation 等价 tripledes.py:202-300。
func inversePermutation(s0, s1 uint32) [8]byte {
	var data [8]byte
	data[3] = byte(
		bitnumIntR(s1, 7, 7) | bitnumIntR(s0, 7, 6) |
			bitnumIntR(s1, 15, 5) | bitnumIntR(s0, 15, 4) |
			bitnumIntR(s1, 23, 3) | bitnumIntR(s0, 23, 2) |
			bitnumIntR(s1, 31, 1) | bitnumIntR(s0, 31, 0),
	)
	data[2] = byte(
		bitnumIntR(s1, 6, 7) | bitnumIntR(s0, 6, 6) |
			bitnumIntR(s1, 14, 5) | bitnumIntR(s0, 14, 4) |
			bitnumIntR(s1, 22, 3) | bitnumIntR(s0, 22, 2) |
			bitnumIntR(s1, 30, 1) | bitnumIntR(s0, 30, 0),
	)
	data[1] = byte(
		bitnumIntR(s1, 5, 7) | bitnumIntR(s0, 5, 6) |
			bitnumIntR(s1, 13, 5) | bitnumIntR(s0, 13, 4) |
			bitnumIntR(s1, 21, 3) | bitnumIntR(s0, 21, 2) |
			bitnumIntR(s1, 29, 1) | bitnumIntR(s0, 29, 0),
	)
	data[0] = byte(
		bitnumIntR(s1, 4, 7) | bitnumIntR(s0, 4, 6) |
			bitnumIntR(s1, 12, 5) | bitnumIntR(s0, 12, 4) |
			bitnumIntR(s1, 20, 3) | bitnumIntR(s0, 20, 2) |
			bitnumIntR(s1, 28, 1) | bitnumIntR(s0, 28, 0),
	)
	data[7] = byte(
		bitnumIntR(s1, 3, 7) | bitnumIntR(s0, 3, 6) |
			bitnumIntR(s1, 11, 5) | bitnumIntR(s0, 11, 4) |
			bitnumIntR(s1, 19, 3) | bitnumIntR(s0, 19, 2) |
			bitnumIntR(s1, 27, 1) | bitnumIntR(s0, 27, 0),
	)
	data[6] = byte(
		bitnumIntR(s1, 2, 7) | bitnumIntR(s0, 2, 6) |
			bitnumIntR(s1, 10, 5) | bitnumIntR(s0, 10, 4) |
			bitnumIntR(s1, 18, 3) | bitnumIntR(s0, 18, 2) |
			bitnumIntR(s1, 26, 1) | bitnumIntR(s0, 26, 0),
	)
	data[5] = byte(
		bitnumIntR(s1, 1, 7) | bitnumIntR(s0, 1, 6) |
			bitnumIntR(s1, 9, 5) | bitnumIntR(s0, 9, 4) |
			bitnumIntR(s1, 17, 3) | bitnumIntR(s0, 17, 2) |
			bitnumIntR(s1, 25, 1) | bitnumIntR(s0, 25, 0),
	)
	data[4] = byte(
		bitnumIntR(s1, 0, 7) | bitnumIntR(s0, 0, 6) |
			bitnumIntR(s1, 8, 5) | bitnumIntR(s0, 8, 4) |
			bitnumIntR(s1, 16, 3) | bitnumIntR(s0, 16, 2) |
			bitnumIntR(s1, 24, 1) | bitnumIntR(s0, 24, 0),
	)
	return data
}

// f Triple-DES F 函数，等价 tripledes.py:303-403。
func f(state uint32, key [6]byte) uint32 {
	t1 := bitnumIntL(state, 31, 0) |
		((state & 0xF0000000) >> 1) |
		bitnumIntL(state, 4, 5) |
		bitnumIntL(state, 3, 6) |
		((state & 0x0F000000) >> 3) |
		bitnumIntL(state, 8, 11) |
		bitnumIntL(state, 7, 12) |
		((state & 0x00F00000) >> 5) |
		bitnumIntL(state, 12, 17) |
		bitnumIntL(state, 11, 18) |
		((state & 0x000F0000) >> 7) |
		bitnumIntL(state, 16, 23)

	t2 := bitnumIntL(state, 15, 0) |
		((state & 0x0000F000) << 15) |
		bitnumIntL(state, 20, 5) |
		bitnumIntL(state, 19, 6) |
		((state & 0x00000F00) << 13) |
		bitnumIntL(state, 24, 11) |
		bitnumIntL(state, 23, 12) |
		((state & 0x000000F0) << 11) |
		bitnumIntL(state, 28, 17) |
		bitnumIntL(state, 27, 18) |
		((state & 0x0000000F) << 9) |
		bitnumIntL(state, 0, 23)

	lrgstate := [6]byte{
		byte(t1>>24) ^ key[0],
		byte(t1>>16) ^ key[1],
		byte(t1>>8) ^ key[2],
		byte(t2>>24) ^ key[3],
		byte(t2>>16) ^ key[4],
		byte(t2>>8) ^ key[5],
	}

	state = (uint32(sbox[0][sboxBit(lrgstate[0]>>2)]) << 28) |
		(uint32(sbox[1][sboxBit(((lrgstate[0]&0x03)<<4)|(lrgstate[1]>>4))]) << 24) |
		(uint32(sbox[2][sboxBit(((lrgstate[1]&0x0F)<<2)|(lrgstate[2]>>6))]) << 20) |
		(uint32(sbox[3][sboxBit(lrgstate[2]&0x3F)]) << 16) |
		(uint32(sbox[4][sboxBit(lrgstate[3]>>2)]) << 12) |
		(uint32(sbox[5][sboxBit(((lrgstate[3]&0x03)<<4)|(lrgstate[4]>>4))]) << 8) |
		(uint32(sbox[6][sboxBit(((lrgstate[4]&0x0F)<<2)|(lrgstate[5]>>6))]) << 4) |
		uint32(sbox[7][sboxBit(lrgstate[5]&0x3F)])

	return bitnumIntL(state, 15, 0) |
		bitnumIntL(state, 6, 1) |
		bitnumIntL(state, 19, 2) |
		bitnumIntL(state, 20, 3) |
		bitnumIntL(state, 28, 4) |
		bitnumIntL(state, 11, 5) |
		bitnumIntL(state, 27, 6) |
		bitnumIntL(state, 16, 7) |
		bitnumIntL(state, 0, 8) |
		bitnumIntL(state, 14, 9) |
		bitnumIntL(state, 22, 10) |
		bitnumIntL(state, 25, 11) |
		bitnumIntL(state, 4, 12) |
		bitnumIntL(state, 17, 13) |
		bitnumIntL(state, 30, 14) |
		bitnumIntL(state, 9, 15) |
		bitnumIntL(state, 1, 16) |
		bitnumIntL(state, 7, 17) |
		bitnumIntL(state, 23, 18) |
		bitnumIntL(state, 13, 19) |
		bitnumIntL(state, 31, 20) |
		bitnumIntL(state, 26, 21) |
		bitnumIntL(state, 2, 22) |
		bitnumIntL(state, 8, 23) |
		bitnumIntL(state, 18, 24) |
		bitnumIntL(state, 12, 25) |
		bitnumIntL(state, 29, 26) |
		bitnumIntL(state, 5, 27) |
		bitnumIntL(state, 21, 28) |
		bitnumIntL(state, 10, 29) |
		bitnumIntL(state, 3, 30) |
		bitnumIntL(state, 24, 31)
}

// singleCrypt 单段 DES（16 轮 Feistel），等价 tripledes.py:406-424 的 crypt。
func singleCrypt(in [8]byte, sched Schedule) [8]byte {
	s0, s1 := initialPermutation(in)
	for idx := 0; idx < 15; idx++ {
		prev := s1
		s1 = f(s1, sched[idx]) ^ s0
		s0 = prev
	}
	s0 = f(s1, sched[15]) ^ s0
	return inversePermutation(s0, s1)
}

// keySchedule 等价 tripledes.py:427-568。
//
// key: 8 字节子 key；mode: Encrypt/Decrypt 决定 round 写入次序。
func keySchedule(key []byte, mode Mode) Schedule {
	var sch Schedule
	var k8 [8]byte
	copy(k8[:], key)

	var c, d uint32
	for i := 0; i < 28; i++ {
		c |= bitnum(k8, uint(keyPermC[i]), uint(31-i))
		d |= bitnum(k8, uint(keyPermD[i]), uint(31-i))
	}
	for i := 0; i < 16; i++ {
		shift := uint(keyRndShift[i])
		c = ((c << shift) | (c >> (28 - shift))) & 0xFFFFFFF0
		d = ((d << shift) | (d >> (28 - shift))) & 0xFFFFFFF0

		togen := i
		if mode == Decrypt {
			togen = 15 - i
		}
		// schedule 重置（已是零值，无需操作）
		var row [6]byte
		for j := 0; j < 24; j++ {
			row[j/8] |= byte(bitnumIntR(c, uint(keyCompression[j]), uint(7-(j%8))))
		}
		for j := 24; j < 48; j++ {
			row[j/8] |= byte(bitnumIntR(d, uint(keyCompression[j]-27), uint(7-(j%8))))
		}
		sch[togen] = row
	}
	return sch
}
