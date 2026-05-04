package crypto

import (
	"encoding/hex"
	"testing"
)

func TestParseKEKHex_OK(t *testing.T) {
	raw := make([]byte, KeySize)
	for i := range raw {
		raw[i] = byte(i)
	}
	s := hex.EncodeToString(raw)
	got, err := ParseKEKHex(s)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != KeySize {
		t.Errorf("len: %d want %d", len(got), KeySize)
	}
}

func TestParseKEKHex_BadLength(t *testing.T) {
	if _, err := ParseKEKHex("DEADBEEF"); err == nil {
		t.Errorf("expected error")
	}
}

func TestParseKEKHex_BadChar(t *testing.T) {
	bad := make([]byte, KeySize*2)
	for i := range bad {
		bad[i] = 'X' // 非 hex
	}
	if _, err := ParseKEKHex(string(bad)); err == nil {
		t.Errorf("expected error")
	}
}

func TestLoadKEKFromEnv_Missing(t *testing.T) {
	t.Setenv("KEK_TEST_MISSING", "")
	if _, err := LoadKEKFromEnv("KEK_TEST_MISSING"); err == nil {
		t.Errorf("expected error for missing env")
	}
}

func TestLoadKEKFromEnv_OK(t *testing.T) {
	raw := make([]byte, KeySize)
	t.Setenv("KEK_TEST_OK", hex.EncodeToString(raw))
	got, err := LoadKEKFromEnv("KEK_TEST_OK")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != KeySize {
		t.Errorf("len: %d want %d", len(got), KeySize)
	}
}
