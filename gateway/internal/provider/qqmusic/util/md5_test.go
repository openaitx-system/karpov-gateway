package util

import (
	"encoding/json"
	"os"
	"testing"
)

// TestCalcMD5_Golden 与 qimei_golden.json 的 md5_chain 部分对拍。
func TestCalcMD5_Golden(t *testing.T) {
	raw, err := os.ReadFile("../testdata/qimei_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []struct {
		Kind        string   `json:"kind"`
		Parts       []string `json:"parts"`
		ExpectedHex string   `json:"expected_hex"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	count := 0
	for i, c := range cases {
		if c.Kind != "md5_chain" {
			continue
		}
		count++
		args := make([]any, len(c.Parts))
		for j, p := range c.Parts {
			args[j] = p
		}
		got := CalcMD5(args...)
		if got != c.ExpectedHex {
			t.Errorf("case[%d] parts=%v\n  got  %s\n  want %s", i, c.Parts, got, c.ExpectedHex)
		}
	}
	if count == 0 {
		t.Fatalf("no md5_chain cases in fixture")
	}
}

func TestCalcMD5_Empty(t *testing.T) {
	// MD5 空输入 = d41d8cd98f00b204e9800998ecf8427e
	if got := CalcMD5(); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("CalcMD5() = %s", got)
	}
}

func TestCalcMD5_Bytes(t *testing.T) {
	got := CalcMD5([]byte("abc"))
	if got != "900150983cd24fb0d6963f7d28e17f72" {
		t.Errorf("CalcMD5([]byte('abc')) = %s", got)
	}
}

func TestCalcMD5_BadType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on unsupported type")
		}
	}()
	_ = CalcMD5(123)
}
