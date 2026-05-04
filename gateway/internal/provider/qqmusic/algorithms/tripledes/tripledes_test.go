package tripledes

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// TestTripledes_Golden 与 Python tripledes_crypt 的 30 个 fixture 对拍。
func TestTripledes_Golden(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/tripledes_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []struct {
		KeyHex string `json:"key_hex"`
		Mode   string `json:"mode"`
		InHex  string `json:"in_hex"`
		OutHex string `json:"out_hex"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(cases) < 10 {
		t.Fatalf("fixture too small: %d (need ≥10)", len(cases))
	}

	for i, c := range cases {
		key, _ := hex.DecodeString(c.KeyHex)
		in, _ := hex.DecodeString(c.InHex)
		want, _ := hex.DecodeString(c.OutHex)
		var mode Mode
		switch c.Mode {
		case "ENCRYPT":
			mode = Encrypt
		case "DECRYPT":
			mode = Decrypt
		default:
			t.Errorf("case[%d] unknown mode %q", i, c.Mode)
			continue
		}

		ts, err := KeySetup(key, mode)
		if err != nil {
			t.Errorf("case[%d] KeySetup: %v", i, err)
			continue
		}
		var blk [8]byte
		copy(blk[:], in)
		got := Crypt(blk, ts)

		if !equalBytes(got[:], want) {
			t.Errorf("case[%d] mode=%s key=%s in=%s\n  got  %x\n  want %x",
				i, c.Mode, c.KeyHex, c.InHex, got[:], want)
		}
	}
}

// TestTripledes_RoundTrip 端到端对称性自检：encrypt(plain) → decrypt → plain。
func TestTripledes_RoundTrip(t *testing.T) {
	keys := [][]byte{
		[]byte("!@#)(NHLiuy*$%^&5dsAFKJU"),
		[]byte("abcdefghijklmnopqrstuvwx"),
	}
	plains := [][]byte{
		[]byte("01234567"),
		[]byte("ABCDEFGH"),
		{0, 0, 0, 0, 0, 0, 0, 0},
		{0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0xF9, 0xF8},
	}
	for _, key := range keys {
		encSched, err := KeySetup(key, Encrypt)
		if err != nil {
			t.Fatalf("KeySetup(enc): %v", err)
		}
		decSched, err := KeySetup(key, Decrypt)
		if err != nil {
			t.Fatalf("KeySetup(dec): %v", err)
		}
		for _, p := range plains {
			var blk [8]byte
			copy(blk[:], p)
			ct := Crypt(blk, encSched)
			pt := Crypt(ct, decSched)
			if !equalBytes(pt[:], p) {
				t.Errorf("round-trip failed\n key=%x plain=%x\n got=%x", key, p, pt[:])
			}
		}
	}
}

func TestKeySetup_BadKey(t *testing.T) {
	_, err := KeySetup(make([]byte, 16), Encrypt)
	if err == nil {
		t.Errorf("expected error for 16-byte key")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
