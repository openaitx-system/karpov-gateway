package algorithms

import (
	"encoding/json"
	"os"
	"testing"
)

// TestSignFromDigest_Golden 跨语言对拍 sign_golden.json：
// 60 个摘要 → expected 与 Python _sign_from_digest_python 一致。
func TestSignFromDigest_Golden(t *testing.T) {
	raw, err := os.ReadFile("../testdata/sign_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []struct {
		Digest   string `json:"digest"`
		Expected string `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(cases) < 50 {
		t.Fatalf("fixture too small: %d (need ≥50 per plan §7.3)", len(cases))
	}

	for i, c := range cases {
		got, err := SignFromDigest(c.Digest)
		if err != nil {
			t.Errorf("case[%d] digest=%s err: %v", i, c.Digest, err)
			continue
		}
		if got != c.Expected {
			t.Errorf("case[%d] digest=%s\n  got  %q\n  want %q", i, c.Digest, got, c.Expected)
		}
	}
}

func TestSignFromDigest_BadInput(t *testing.T) {
	if _, err := SignFromDigest(""); err == nil {
		t.Errorf("expected error for empty digest")
	}
	if _, err := SignFromDigest("0123ABC"); err == nil {
		t.Errorf("expected error for short digest")
	}
	// 含非 hex 字符
	if _, err := SignFromDigest("ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"); err == nil {
		t.Errorf("expected error for non-hex digest")
	}
}

func TestSignRequestFromBytes_E2E(t *testing.T) {
	// 端到端：固定 payload 字节 → SHA1 → SignFromDigest
	// 与 fixture 中 SHA1("abc") 摘要的对应签名做对照。
	raw, err := os.ReadFile("../testdata/sign_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []struct {
		Digest   string `json:"digest"`
		Expected string `json:"expected"`
	}
	_ = json.Unmarshal(raw, &cases)

	// 找 SHA1("abc")=A9993E364706816ABA3E25717850C26C9CD0D89D 的样本
	const expectedDigest = "A9993E364706816ABA3E25717850C26C9CD0D89D"
	var expected string
	for _, c := range cases {
		if c.Digest == expectedDigest {
			expected = c.Expected
			break
		}
	}
	if expected == "" {
		t.Skip("expected digest not in fixture; skipping")
	}
	got, err := SignRequestFromBytes([]byte("abc"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got != expected {
		t.Errorf("e2e sign for 'abc'\n  got  %q\n  want %q", got, expected)
	}
}
