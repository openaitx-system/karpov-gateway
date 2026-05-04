// Package jce 是 QQ 音乐 musicw.fcg 二进制协议（JCE/TARS wire）的最小编/解码层。
//
// 复用 github.com/TarsCloud/TarsGo/tars/protocol/codec 的字节级原语，
// 在其上实现 JceRequest / JceRequestItem / JceResponse / JceResponseItem
// 与 Python qqmusic_api/models/request.py 中 tarsio.Struct 完全等价的 wire format。
//
// 设计要点：
//
//   - dict 字段（comm / data）使用切片表示以保留插入顺序（Python tarsio 顺序敏感）。
//   - JceRequestItem.Param 与 JceResponseItem.Data 是 TarsDict 语义：
//     Python 端 dict[int, Any]，**实际编码为 nested Struct**（每条 entry 用其 int key 作 tag），
//     而不是 MAP。
//   - wrap_simplelist=True：当 Param/Data entry 的 value 是 bytes 时，
//     编码为 SimpleList(0x0D) 而非 List(0x09)。
//   - 当前 spike 仅覆盖三种 ParamEntry 类型：String / Int / Bytes，
//     对应 musicw.fcg 真实流量里 90% 以上场景；
//     Float / nested Struct / 真 List 留待 M8 实施时按需补充。
package jce

// JCE wire type 常量（与 TarsGo codec.go iota 顺序、tarsio 一致）。
const (
	WireByte        byte = 0
	WireShort       byte = 1
	WireInt         byte = 2
	WireLong        byte = 3
	WireFloat       byte = 4
	WireDouble      byte = 5
	WireString1     byte = 6
	WireString4     byte = 7
	WireMap         byte = 8
	WireList        byte = 9
	WireStructBegin byte = 10
	WireStructEnd   byte = 11
	WireZeroTag     byte = 12
	WireSimpleList  byte = 13
)

// ParamKind 描述 ParamEntry 的实际值类型。
type ParamKind int

const (
	KindNone ParamKind = iota
	KindString
	KindInt
	KindBytes
)

// ParamEntry 是 TarsDict 中一个 (int_key, value) 对在 Go 侧的表达。
//
// 编码时：根据 Kind 选用不同 wire 路径（String / Int / SimpleList）。
// 解码时：按读到的 wire type 反向填充对应字段。
type ParamEntry struct {
	Tag   byte
	Kind  ParamKind
	Str   string
	Int   int64
	Bytes []byte
}

// KV 表示一个保序的字符串 key-value 对。
type KV struct {
	K, V string
}

// DataItem 为 JceRequest.data / JceResponse.data 中一项（保序）。
type DataItem struct {
	Key  string
	Item JceRequestItem // 仅在 JceRequest 时使用
}

// DataResponseItem 为 JceResponse.data 中一项（保序）。
type DataResponseItem struct {
	Key  string
	Item JceResponseItem
}

// JceRequest 等价 qqmusic_api.models.request.JceRequest（tarsio.Struct）。
type JceRequest struct {
	Comm []KV       // tag=0 map<string,string>
	Data []DataItem // tag=1 map<string,JceRequestItem>
}

// JceRequestItem 等价 JceRequestItem。
type JceRequestItem struct {
	Module string       // tag=0
	Method string       // tag=1
	Param  []ParamEntry // tag=2 (nested Struct，wrap_simplelist=True)
}

// JceResponse 等价 JceResponse。
//
// **重要**：Python qqmusic_api/models/request.py 中 `field(tag=4)` 在
// tarsio 新版本（≥0.5）已失效；wire 上 tag 实际按字段声明顺序分配，
// 因此 data 在 wire 上是 tag=1（不是 4）。
type JceResponse struct {
	Code int32              // tag=0
	Data []DataResponseItem // tag=1 map<string,JceResponseItem>（见上方注释）
}

// JceResponseItem 等价 JceResponseItem。
//
// 同样 Python `field(tag=3)` 失效；data 在 wire 上是 tag=1。
type JceResponseItem struct {
	Code int32        // tag=0
	Data []ParamEntry // tag=1 (nested Struct，wrap_simplelist=True)
}
