package util

import (
	"encoding/json"
	"os"
	"testing"
)

// TestHash33_Golden 与 Python qqmusic_api.utils.common.hash33 的 30 个 fixture 对拍。
func TestHash33_Golden(t *testing.T) {
	raw, err := os.ReadFile("../testdata/hash33_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []struct {
		S        string `json:"s"`
		H        int64  `json:"h"`
		Expected int64  `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(cases) < 20 {
		t.Fatalf("fixture too small: %d", len(cases))
	}
	for i, c := range cases {
		got := Hash33(c.S, c.H)
		if got != c.Expected {
			t.Errorf("case[%d] s=%q h=%d\n  got  %d\n  want %d", i, c.S, c.H, got, c.Expected)
		}
	}
}
