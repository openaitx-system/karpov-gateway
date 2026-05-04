package jce

import (
	"fmt"

	"github.com/TarsCloud/TarsGo/tars/protocol/codec"
)

// Encode 把 JceRequest 序列化为 wire 字节。
//
// Wire 顶层无 StructBegin/End；直接是 root struct 的 fields：
//   - tag=0: MAP comm
//   - tag=1: MAP data
func (r *JceRequest) Encode() ([]byte, error) {
	buf := codec.NewBuffer()
	if err := writeStringMap(buf, 0, r.Comm); err != nil {
		return nil, err
	}
	if err := writeRequestDataMap(buf, 1, r.Data); err != nil {
		return nil, err
	}
	return buf.ToBytes(), nil
}

// Encode 把 JceResponse 序列化为 wire 字节。
//
// Root struct 字段：
//   - tag=0: code (int32)
//   - tag=1: MAP data
//
// 注：Python qqmusic_api/models/request.py 写的 `field(tag=4)` 在 tarsio 新版本
// 中实际被忽略；tag 由字段声明顺序自动分配（code=0, data=1）。
func (r *JceResponse) Encode() ([]byte, error) {
	buf := codec.NewBuffer()
	if err := buf.WriteInt32(r.Code, 0); err != nil {
		return nil, err
	}
	if err := writeResponseDataMap(buf, 1, r.Data); err != nil {
		return nil, err
	}
	return buf.ToBytes(), nil
}

// ----- helpers -----

func writeStringMap(buf *codec.Buffer, tag byte, kvs []KV) error {
	if err := buf.WriteHead(WireMap, tag); err != nil {
		return err
	}
	if err := buf.WriteInt32(int32(len(kvs)), 0); err != nil {
		return err
	}
	for _, kv := range kvs {
		if err := buf.WriteString(kv.K, 0); err != nil {
			return err
		}
		if err := buf.WriteString(kv.V, 1); err != nil {
			return err
		}
	}
	return nil
}

func writeRequestDataMap(buf *codec.Buffer, tag byte, items []DataItem) error {
	if err := buf.WriteHead(WireMap, tag); err != nil {
		return err
	}
	if err := buf.WriteInt32(int32(len(items)), 0); err != nil {
		return err
	}
	for i := range items {
		if err := buf.WriteString(items[i].Key, 0); err != nil {
			return err
		}
		// value at tag=1 是 JceRequestItem（Struct）
		if err := writeRequestItem(buf, 1, &items[i].Item); err != nil {
			return err
		}
	}
	return nil
}

func writeResponseDataMap(buf *codec.Buffer, tag byte, items []DataResponseItem) error {
	if err := buf.WriteHead(WireMap, tag); err != nil {
		return err
	}
	if err := buf.WriteInt32(int32(len(items)), 0); err != nil {
		return err
	}
	for i := range items {
		if err := buf.WriteString(items[i].Key, 0); err != nil {
			return err
		}
		if err := writeResponseItem(buf, 1, &items[i].Item); err != nil {
			return err
		}
	}
	return nil
}

func writeRequestItem(buf *codec.Buffer, tag byte, it *JceRequestItem) error {
	if err := buf.WriteHead(WireStructBegin, tag); err != nil {
		return err
	}
	if err := buf.WriteString(it.Module, 0); err != nil {
		return err
	}
	if err := buf.WriteString(it.Method, 1); err != nil {
		return err
	}
	if err := writeParamStruct(buf, 2, it.Param); err != nil {
		return err
	}
	return buf.WriteHead(WireStructEnd, 0)
}

func writeResponseItem(buf *codec.Buffer, tag byte, it *JceResponseItem) error {
	if err := buf.WriteHead(WireStructBegin, tag); err != nil {
		return err
	}
	if err := buf.WriteInt32(it.Code, 0); err != nil {
		return err
	}
	// 同 Encode 备注：Python field(tag=3) 在 tarsio 新版本被忽略，实际为 tag=1。
	if err := writeParamStruct(buf, 1, it.Data); err != nil {
		return err
	}
	return buf.WriteHead(WireStructEnd, 0)
}

// writeParamStruct 把 TarsDict（[]ParamEntry）编为一个 nested Struct，
// 每个 entry 用 entry.Tag 作为字段 tag；按切片顺序写入（=Python dict 插入顺序）。
func writeParamStruct(buf *codec.Buffer, tag byte, entries []ParamEntry) error {
	if err := buf.WriteHead(WireStructBegin, tag); err != nil {
		return err
	}
	for i := range entries {
		e := &entries[i]
		switch e.Kind {
		case KindString:
			if err := buf.WriteString(e.Str, e.Tag); err != nil {
				return err
			}
		case KindInt:
			if err := buf.WriteInt64(e.Int, e.Tag); err != nil {
				return err
			}
		case KindBytes:
			if err := writeSimpleList(buf, e.Tag, e.Bytes); err != nil {
				return err
			}
		default:
			return fmt.Errorf("jce: unsupported ParamKind %d (tag=%d)", e.Kind, e.Tag)
		}
	}
	return buf.WriteHead(WireStructEnd, 0)
}

// writeSimpleList wrap_simplelist=True 时 bytes 的编码：
//
//	WireSimpleList tag=N
//	  WriteHead(WireByte, 0)  // inner-type 描述符
//	  WriteInt32(len, 0)
//	  raw bytes
//
// 注意 length 用 INT32 自适应（≤127 走 BYTE，BYTE-SHORT-INT 三档）。
func writeSimpleList(buf *codec.Buffer, tag byte, data []byte) error {
	if err := buf.WriteHead(WireSimpleList, tag); err != nil {
		return err
	}
	if err := buf.WriteHead(WireByte, 0); err != nil {
		return err
	}
	if err := buf.WriteInt32(int32(len(data)), 0); err != nil {
		return err
	}
	return buf.WriteBytes(data)
}
