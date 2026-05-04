package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// TestAESEncryptCBC_Golden 与 qimei_golden.json 的 aes_cbc_pkcs7_iv_eq_key 部分对拍。
func TestAESEncryptCBC_Golden(t *testing.T) {
	raw, err := os.ReadFile("../testdata/qimei_golden.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cases []struct {
		Kind          string `json:"kind"`
		KeyASCII      string `json:"key_ascii"`
		PlaintextHex  string `json:"plaintext_hex"`
		CiphertextHex string `json:"ciphertext_hex"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	count := 0
	for i, c := range cases {
		if c.Kind != "aes_cbc_pkcs7_iv_eq_key" {
			continue
		}
		count++
		pt, err := hex.DecodeString(c.PlaintextHex)
		if err != nil {
			t.Errorf("case[%d] decode pt: %v", i, err)
			continue
		}
		want, err := hex.DecodeString(c.CiphertextHex)
		if err != nil {
			t.Errorf("case[%d] decode ct: %v", i, err)
			continue
		}
		got, err := AESEncryptCBC([]byte(c.KeyASCII), pt)
		if err != nil {
			t.Errorf("case[%d] encrypt: %v", i, err)
			continue
		}
		if !bytesEqual(got, want) {
			t.Errorf("case[%d] key=%s pt_hex=%s\n  got  %x\n  want %x",
				i, c.KeyASCII, c.PlaintextHex, got, want)
		}
	}
	if count == 0 {
		t.Fatalf("no AES cases in fixture")
	}
}

// TestAESEncryptCBC_RoundTrip 用标准 AES-CBC 解密再去 padding，验证可逆。
func TestAESEncryptCBC_RoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	plains := [][]byte{
		nil, []byte("a"), []byte("hello world"),
		[]byte("123456789012345"),
		[]byte("1234567890123456"),
		[]byte("12345678901234567"),
	}
	for _, p := range plains {
		ct, err := AESEncryptCBC(key, p)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		// 用同一 key 做 IV 解密
		block, _ := aes.NewCipher(key)
		decrypted := make([]byte, len(ct))
		cipher.NewCBCDecrypter(block, key).CryptBlocks(decrypted, ct)
		unpadded, err := PKCS7Unpad(decrypted, aes.BlockSize)
		if err != nil {
			t.Fatalf("unpad: %v", err)
		}
		if !bytesEqual(unpadded, p) {
			t.Errorf("round trip\n  pt   %x\n  back %x", p, unpadded)
		}
	}
}

func TestAESEncryptCBC_BadKey(t *testing.T) {
	if _, err := AESEncryptCBC(make([]byte, 8), []byte("hi")); err == nil {
		t.Errorf("expected error for 8-byte key")
	}
}

// TestRSAEncrypt_LengthAndDecryptable 验证：
// 1. 输出长度 = 128 字节 = RSA-1024 模长
// 2. 用同一个公钥的对应私钥可解密（这里我们没有私钥，只断言长度与不抛错）
func TestRSAEncrypt_LengthAndNonZero(t *testing.T) {
	plaintexts := [][]byte{
		[]byte("0123456789abcdef"),
		[]byte("fedcba9876543210"),
		[]byte("a"),
	}
	for _, pt := range plaintexts {
		ct, err := RSAEncrypt(pt)
		if err != nil {
			t.Fatalf("RSAEncrypt(%q): %v", pt, err)
		}
		if len(ct) != 128 {
			t.Errorf("RSAEncrypt(%q) length = %d, want 128", pt, len(ct))
		}
		// 两次加密应不同（PKCS1v15 含随机 padding）
		ct2, _ := RSAEncrypt(pt)
		if bytesEqual(ct, ct2) {
			t.Errorf("RSAEncrypt(%q) deterministic — padding 没有随机性", pt)
		}
	}
}

func bytesEqual(a, b []byte) bool {
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
