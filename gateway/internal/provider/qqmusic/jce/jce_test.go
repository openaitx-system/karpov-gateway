package jce

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// fixtureCase 与 tools/dump_python_golden.py:gen_jce_golden 输出 schema 对齐。
type fixtureCase struct {
	Kind       string            `json:"kind"`
	EncodedHex string            `json:"encoded_hex"`
	Comm       map[string]string `json:"comm"`
	Items      []fixtureItem     `json:"items"`
	Code       int32             `json:"code"` // response 时存在
}

type fixtureItem struct {
	Key    string         `json:"key"`
	Module string         `json:"module"` // request 时存在
	Method string         `json:"method"` // request 时存在
	Code   int32          `json:"code"`   // response 时存在
	Param  []fixtureParam `json:"param,omitempty"`
	Data   []fixtureParam `json:"data,omitempty"`
}

type fixtureParam struct {
	Tag      byte   `json:"tag"`
	Kind     string `json:"kind"`
	Str      string `json:"str,omitempty"`
	Int      int64  `json:"int,omitempty"`
	BytesHex string `json:"bytes_hex,omitempty"`
}

func loadFixture(t *testing.T) []fixtureCase {
	t.Helper()
	raw, err := os.ReadFile("../testdata/jce_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(cases) < 5 {
		t.Fatalf("fixture too small: %d (need ≥5)", len(cases))
	}
	return cases
}

func paramFromFixture(fp fixtureParam) ParamEntry {
	switch fp.Kind {
	case "string":
		return ParamEntry{Tag: fp.Tag, Kind: KindString, Str: fp.Str}
	case "int":
		return ParamEntry{Tag: fp.Tag, Kind: KindInt, Int: fp.Int}
	case "bytes":
		b, _ := hex.DecodeString(fp.BytesHex)
		return ParamEntry{Tag: fp.Tag, Kind: KindBytes, Bytes: b}
	}
	return ParamEntry{Tag: fp.Tag, Kind: KindNone}
}

// TestJceRequest_DecodeFixture：解码 Python tarsio 编出的字节，验证字段值正确。
func TestJceRequest_DecodeFixture(t *testing.T) {
	cases := loadFixture(t)
	for _, c := range cases {
		if c.Kind == "response_basic" {
			continue
		}
		t.Run(c.Kind, func(t *testing.T) {
			raw, _ := hex.DecodeString(c.EncodedHex)
			var req JceRequest
			if err := req.Decode(raw); err != nil {
				t.Fatalf("decode: %v", err)
			}
			// comm
			if len(req.Comm) != len(c.Comm) {
				t.Fatalf("comm len got %d want %d", len(req.Comm), len(c.Comm))
			}
			for _, kv := range req.Comm {
				if c.Comm[kv.K] != kv.V {
					t.Errorf("comm[%q]=%q want %q", kv.K, kv.V, c.Comm[kv.K])
				}
			}
			// data
			if len(req.Data) != len(c.Items) {
				t.Fatalf("data len got %d want %d", len(req.Data), len(c.Items))
			}
			for i, di := range req.Data {
				want := c.Items[i]
				if di.Key != want.Key || di.Item.Module != want.Module || di.Item.Method != want.Method {
					t.Errorf("item[%d] meta got (%s,%s,%s) want (%s,%s,%s)",
						i, di.Key, di.Item.Module, di.Item.Method,
						want.Key, want.Module, want.Method)
				}
				if len(di.Item.Param) != len(want.Param) {
					t.Fatalf("item[%d] param len got %d want %d",
						i, len(di.Item.Param), len(want.Param))
				}
				for j, p := range di.Item.Param {
					exp := paramFromFixture(want.Param[j])
					if p.Tag != exp.Tag || p.Kind != exp.Kind ||
						p.Str != exp.Str || p.Int != exp.Int ||
						!bytes.Equal(p.Bytes, exp.Bytes) {
						t.Errorf("item[%d].param[%d] got %+v want %+v", i, j, p, exp)
					}
				}
			}
		})
	}
}

// TestJceResponse_DecodeFixture：解码 response 类样本。
func TestJceResponse_DecodeFixture(t *testing.T) {
	cases := loadFixture(t)
	for _, c := range cases {
		if c.Kind != "response_basic" {
			continue
		}
		raw, _ := hex.DecodeString(c.EncodedHex)
		var rsp JceResponse
		if err := rsp.Decode(raw); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if rsp.Code != c.Code {
			t.Errorf("code got %d want %d", rsp.Code, c.Code)
		}
		if len(rsp.Data) != len(c.Items) {
			t.Fatalf("data len got %d want %d", len(rsp.Data), len(c.Items))
		}
		for i, di := range rsp.Data {
			want := c.Items[i]
			if di.Key != want.Key || di.Item.Code != want.Code {
				t.Errorf("response item[%d] meta got (%s,%d) want (%s,%d)",
					i, di.Key, di.Item.Code, want.Key, want.Code)
			}
			if len(di.Item.Data) != len(want.Data) {
				t.Fatalf("response item[%d] data len got %d want %d",
					i, len(di.Item.Data), len(want.Data))
			}
			for j, p := range di.Item.Data {
				exp := paramFromFixture(want.Data[j])
				if p.Tag != exp.Tag || p.Kind != exp.Kind ||
					p.Str != exp.Str || p.Int != exp.Int ||
					!bytes.Equal(p.Bytes, exp.Bytes) {
					t.Errorf("response item[%d].data[%d] got %+v want %+v", i, j, p, exp)
				}
			}
		}
	}
}

// TestJceRequest_RoundTrip：解码 Python 字节 → Go encode → 与 Python 字节 hex 完全一致。
//
// 这是 spike 的核心目标：证明 Go 端编码与 Python tarsio 字节级一致。
func TestJceRequest_RoundTrip(t *testing.T) {
	cases := loadFixture(t)
	for _, c := range cases {
		if c.Kind == "response_basic" {
			continue
		}
		t.Run(c.Kind, func(t *testing.T) {
			raw, _ := hex.DecodeString(c.EncodedHex)
			var req JceRequest
			if err := req.Decode(raw); err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := req.Encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if !bytes.Equal(out, raw) {
				t.Errorf("round-trip byte mismatch\n  got  %x\n  want %x", out, raw)
			}
		})
	}
}

// TestJceResponse_RoundTrip：response 同上。
func TestJceResponse_RoundTrip(t *testing.T) {
	cases := loadFixture(t)
	for _, c := range cases {
		if c.Kind != "response_basic" {
			continue
		}
		raw, _ := hex.DecodeString(c.EncodedHex)
		var rsp JceResponse
		if err := rsp.Decode(raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out, err := rsp.Encode()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if !bytes.Equal(out, raw) {
			t.Errorf("response round-trip byte mismatch\n  got  %x\n  want %x", out, raw)
		}
	}
}
