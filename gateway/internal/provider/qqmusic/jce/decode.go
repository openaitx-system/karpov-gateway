package jce

import (
	"errors"
	"fmt"

	"github.com/TarsCloud/TarsGo/tars/protocol/codec"
)

// Decode 反序列化 wire 字节为 JceRequest。
//
// 与 Encode 对称：root 没有 StructBegin/End，直接读字段。
func (r *JceRequest) Decode(data []byte) error {
	rd := codec.NewReader(data)

	if _, err := rd.SkipTo(WireMap, 0, true); err != nil {
		return fmt.Errorf("jce: locate comm map: %w", err)
	}
	kvs, err := readStringMap(rd)
	if err != nil {
		return fmt.Errorf("jce: read comm: %w", err)
	}
	r.Comm = kvs

	if _, err := rd.SkipTo(WireMap, 1, true); err != nil {
		return fmt.Errorf("jce: locate data map: %w", err)
	}
	items, err := readRequestDataMap(rd)
	if err != nil {
		return fmt.Errorf("jce: read data: %w", err)
	}
	r.Data = items
	return nil
}

// Decode 反序列化 wire 字节为 JceResponse。
func (r *JceResponse) Decode(data []byte) error {
	rd := codec.NewReader(data)

	var code int32
	if err := rd.ReadInt32(&code, 0, true); err != nil {
		return fmt.Errorf("jce: read code: %w", err)
	}
	r.Code = code

	if _, err := rd.SkipTo(WireMap, 1, true); err != nil {
		return fmt.Errorf("jce: locate response data map: %w", err)
	}
	items, err := readResponseDataMap(rd)
	if err != nil {
		return fmt.Errorf("jce: read response data: %w", err)
	}
	r.Data = items
	return nil
}

// ----- helpers -----

func readStringMap(rd *codec.Reader) ([]KV, error) {
	var n int32
	if err := rd.ReadInt32(&n, 0, true); err != nil {
		return nil, fmt.Errorf("read map length: %w", err)
	}
	if n < 0 {
		return nil, errors.New("negative map length")
	}
	out := make([]KV, 0, n)
	for i := int32(0); i < n; i++ {
		var k, v string
		if err := rd.ReadString(&k, 0, true); err != nil {
			return nil, fmt.Errorf("read map key[%d]: %w", i, err)
		}
		if err := rd.ReadString(&v, 1, true); err != nil {
			return nil, fmt.Errorf("read map val[%d]: %w", i, err)
		}
		out = append(out, KV{K: k, V: v})
	}
	return out, nil
}

func readRequestDataMap(rd *codec.Reader) ([]DataItem, error) {
	var n int32
	if err := rd.ReadInt32(&n, 0, true); err != nil {
		return nil, fmt.Errorf("read data length: %w", err)
	}
	if n < 0 {
		return nil, errors.New("negative data length")
	}
	out := make([]DataItem, 0, n)
	for i := int32(0); i < n; i++ {
		var k string
		if err := rd.ReadString(&k, 0, true); err != nil {
			return nil, fmt.Errorf("read data key[%d]: %w", i, err)
		}
		if _, err := rd.SkipTo(WireStructBegin, 1, true); err != nil {
			return nil, fmt.Errorf("locate item[%d]: %w", i, err)
		}
		var item JceRequestItem
		if err := readRequestItemBody(rd, &item); err != nil {
			return nil, fmt.Errorf("read item[%d]: %w", i, err)
		}
		if err := rd.SkipToStructEnd(); err != nil {
			return nil, fmt.Errorf("close item[%d]: %w", i, err)
		}
		out = append(out, DataItem{Key: k, Item: item})
	}
	return out, nil
}

func readResponseDataMap(rd *codec.Reader) ([]DataResponseItem, error) {
	var n int32
	if err := rd.ReadInt32(&n, 0, true); err != nil {
		return nil, fmt.Errorf("read data length: %w", err)
	}
	if n < 0 {
		return nil, errors.New("negative data length")
	}
	out := make([]DataResponseItem, 0, n)
	for i := int32(0); i < n; i++ {
		var k string
		if err := rd.ReadString(&k, 0, true); err != nil {
			return nil, fmt.Errorf("read data key[%d]: %w", i, err)
		}
		if _, err := rd.SkipTo(WireStructBegin, 1, true); err != nil {
			return nil, fmt.Errorf("locate response item[%d]: %w", i, err)
		}
		var item JceResponseItem
		if err := readResponseItemBody(rd, &item); err != nil {
			return nil, fmt.Errorf("read response item[%d]: %w", i, err)
		}
		if err := rd.SkipToStructEnd(); err != nil {
			return nil, fmt.Errorf("close response item[%d]: %w", i, err)
		}
		out = append(out, DataResponseItem{Key: k, Item: item})
	}
	return out, nil
}

func readRequestItemBody(rd *codec.Reader, item *JceRequestItem) error {
	if err := rd.ReadString(&item.Module, 0, true); err != nil {
		return fmt.Errorf("read module: %w", err)
	}
	if err := rd.ReadString(&item.Method, 1, true); err != nil {
		return fmt.Errorf("read method: %w", err)
	}
	if _, err := rd.SkipTo(WireStructBegin, 2, true); err != nil {
		return fmt.Errorf("locate param struct: %w", err)
	}
	entries, err := readParamStructBody(rd)
	if err != nil {
		return fmt.Errorf("read param: %w", err)
	}
	item.Param = entries
	return rd.SkipToStructEnd()
}

func readResponseItemBody(rd *codec.Reader, item *JceResponseItem) error {
	if err := rd.ReadInt32(&item.Code, 0, true); err != nil {
		return fmt.Errorf("read code: %w", err)
	}
	if _, err := rd.SkipTo(WireStructBegin, 1, true); err != nil {
		return fmt.Errorf("locate data struct: %w", err)
	}
	entries, err := readParamStructBody(rd)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}
	item.Data = entries
	return rd.SkipToStructEnd()
}

// readParamStructBody 读 TarsDict-as-Struct 内部各 entry，直到遇到 StructEnd。
//
// 调用方负责调用 SkipToStructEnd 推进 reader 越过 StructEnd 标记。
// 这里只读 fields；遇到 StructEnd 则停止（通过 SkipTo 返回 found=false 探测）。
func readParamStructBody(rd *codec.Reader) ([]ParamEntry, error) {
	var entries []ParamEntry
	for {
		// 先尝试 peek 下一个 head；若是 StructEnd 则退出。
		// codec.Reader 没有公开的 readHead 暴露，但 SkipToNoCheck 行为合适：
		// 它会 read head；若不是目标 tag 会 unread——但我们没有具体目标 tag。
		// 替代方案：用 unsafe peek 一字节。
		// 这里采取更保守的实现：依次按 spike 已知的 tag 列表尝试，
		// 但我们不知道 entry tag。
		// 工程实践：手动 read head + 分发。codec.Reader 没有公开 ReadHead，
		// 退化做法：用 SkipToNoCheck 走遍每种类型不可行。
		//
		// **简化策略**：因 spike 仅支持 String/Int/Bytes，且 Tag 数量有限，
		// 遍历 0..255 逐个 SkipToNoCheck(tag, false) 找下一个非 StructEnd 字段。
		// 但这会重复读字段。
		//
		// 终极方案：复用 codec 的 readField 内部能力——使用 reflection
		// 或者通过另一种 wire-level 解析。这里采用"提前用 cap 策略"：
		// 多次 SkipToNoCheck(t, false)，但我们需要 head/tag 信息。
		//
		// 折中：用 codec.Reader 自带的 peekHead 方法。
		ty, tag, ok, err := peekHead(rd)
		if err != nil {
			return nil, err
		}
		if !ok || ty == WireStructEnd {
			return entries, nil
		}
		// 消费这个 head（peekHead 已 unread；通过 SkipTo 重新读取）
		switch ty {
		case WireString1, WireString4:
			var s string
			if err := rd.ReadString(&s, tag, true); err != nil {
				return nil, err
			}
			entries = append(entries, ParamEntry{Tag: tag, Kind: KindString, Str: s})
		case WireByte, WireShort, WireInt, WireLong:
			var v int64
			if err := rd.ReadInt64(&v, tag, true); err != nil {
				return nil, err
			}
			entries = append(entries, ParamEntry{Tag: tag, Kind: KindInt, Int: v})
		case WireSimpleList:
			b, err := readSimpleList(rd, tag)
			if err != nil {
				return nil, err
			}
			entries = append(entries, ParamEntry{Tag: tag, Kind: KindBytes, Bytes: b})
		case WireZeroTag:
			// ZeroTag 表示该 tag 的整数值为 0，使用 ReadInt64 行为兼容
			var v int64
			if err := rd.ReadInt64(&v, tag, true); err != nil {
				return nil, err
			}
			entries = append(entries, ParamEntry{Tag: tag, Kind: KindInt, Int: v})
		default:
			return nil, fmt.Errorf("jce: unsupported wire type %d at tag %d (spike: only String/Int/Bytes)", ty, tag)
		}
	}
}

func readSimpleList(rd *codec.Reader, tag byte) ([]byte, error) {
	if _, err := rd.SkipTo(WireSimpleList, tag, true); err != nil {
		return nil, fmt.Errorf("locate SimpleList tag=%d: %w", tag, err)
	}
	// inner type descriptor
	if _, err := rd.SkipTo(WireByte, 0, true); err != nil {
		return nil, fmt.Errorf("SimpleList inner-type: %w", err)
	}
	// 直接读出 BYTE value（虽然是 type marker，但作为 byte 值读出会得到 0 — 不需要它）
	// codec 的 SkipTo 已消费 head 但下一个字节是 BYTE 的 value。
	// 真实流量里 inner-type 描述符是 (tag=0, type=BYTE) 后续没 value！
	// 但 codec.SkipTo 不会消费 value，只消费头。
	// 让我们仔细处理：在 tarsio 编码里 inner-type 描述符就是 head only，没有 value。
	// 所以 SkipTo 读完 head 即可。
	var n int32
	if err := rd.ReadInt32(&n, 0, true); err != nil {
		return nil, fmt.Errorf("SimpleList length: %w", err)
	}
	if n < 0 {
		return nil, errors.New("negative SimpleList length")
	}
	out := make([]byte, n)
	if err := rd.ReadSliceUint8(&out, n, true); err != nil {
		return nil, fmt.Errorf("SimpleList body: %w", err)
	}
	return out, nil
}

// peekHead 读取下一个 head，但在不消耗的情况下返回。
//
// 实现方式：尝试 SkipToNoCheck(tag=0xFF, require=false)，遇到 StructEnd 则返回 ok=false；
// 否则 unreadHead 后返回 (ty, tag, true)。
//
// 但 codec.Reader 没有公开 peek/unread 接口，不得不用 trick：
// 先记录当前位置，read 一字节，分析 head，再 seek 回来。
//
// codec.Reader 内部用 *bytes.Reader，没有 Seek 公开。
// 折中：使用 SkipToNoCheck 取得 (found, currentTag, err)；
// 若 found=false（=遇到 StructEnd），返回 ok=false。
// 若 found=true，意味着 head 已被消费，且 tag 与 0xFF 匹配——但我们传 0xFF。
//
// 最干净的方案是反射访问 *codec.Reader.buf 直接 *bytes.Reader.Seek。
// 见 unsafe_peek.go。
func peekHead(rd *codec.Reader) (ty, tag byte, ok bool, err error) {
	return readHeadOrStop(rd)
}
