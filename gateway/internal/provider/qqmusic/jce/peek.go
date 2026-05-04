package jce

import (
	"bytes"
	"errors"
	"reflect"
	"unsafe"

	"github.com/TarsCloud/TarsGo/tars/protocol/codec"
)

// readHeadOrStop 读取下一个 wire head；如果是 StructEnd 则 unread 后返回 ok=false。
//
// 实现：通过 unsafe 反射访问 *codec.Reader 的私有 buf *bytes.Reader 字段，
// 用 bytes.Reader.Seek 回退一字节实现 peek 语义。
// 这是 spike 阶段的取巧实现；后续如有性能或可移植问题，
// 改为完全 fork TarsGo codec 自己实现 head 读取。
func readHeadOrStop(rd *codec.Reader) (ty, tag byte, ok bool, err error) {
	br, err := readerBytesReader(rd)
	if err != nil {
		return 0, 0, false, err
	}
	pos, err := br.Seek(0, 1) // current
	if err != nil {
		return 0, 0, false, err
	}
	b, err := br.ReadByte()
	if err != nil {
		return 0, 0, false, err
	}
	// 高 4 bit = tag(0..14), 低 4 bit = type
	hi := b >> 4
	lo := b & 0x0F
	tag = hi
	ty = lo
	if hi == 0x0F {
		// 双字节头
		nb, e := br.ReadByte()
		if e != nil {
			return 0, 0, false, e
		}
		tag = nb
	}
	// peek 完了：seek 回原位
	if _, err := br.Seek(pos, 0); err != nil {
		return 0, 0, false, err
	}
	if ty == WireStructEnd {
		return ty, tag, false, nil
	}
	return ty, tag, true, nil
}

// readerBytesReader 反射拿 *codec.Reader.buf （*bytes.Reader），用于 peek/seek。
func readerBytesReader(rd *codec.Reader) (*bytes.Reader, error) {
	if rd == nil {
		return nil, errors.New("jce: nil reader")
	}
	v := reflect.ValueOf(rd).Elem()
	f := v.FieldByName("buf")
	if !f.IsValid() {
		return nil, errors.New("jce: codec.Reader has no field 'buf'; TarsGo version mismatch")
	}
	// 通过 unsafe 取私有字段值
	rv := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	br, ok := rv.Interface().(*bytes.Reader)
	if !ok {
		return nil, errors.New("jce: codec.Reader.buf is not *bytes.Reader")
	}
	return br, nil
}
