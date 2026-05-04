package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTrip_NoAAD(t *testing.T) {
	kek, err := GenerateKEK()
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	plain := []byte(`{"musickey":"W_X_TEST","musicid":12345}`)
	env, err := Encrypt(kek, plain, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(env) <= len(plain) {
		t.Errorf("envelope must be longer than plaintext (nonce+tag): %d vs %d", len(env), len(plain))
	}
	got, err := Decrypt(kek, env, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("plaintext mismatch")
	}
}

func TestRoundTrip_WithAAD(t *testing.T) {
	kek, _ := GenerateKEK()
	plain := []byte("secret")
	aad := []byte("credential_id=abc123")

	env, err := Encrypt(kek, plain, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := Decrypt(kek, env, aad)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("plaintext mismatch")
	}
}

func TestAAD_Mismatch(t *testing.T) {
	kek, _ := GenerateKEK()
	plain := []byte("secret")
	env, _ := Encrypt(kek, plain, []byte("credential_id=A"))

	if _, err := Decrypt(kek, env, []byte("credential_id=B")); err == nil {
		t.Errorf("expected decrypt failure with wrong AAD")
	}
}

func TestWrongKEK(t *testing.T) {
	plain := []byte("hello")
	a, _ := GenerateKEK()
	b, _ := GenerateKEK()
	env, _ := Encrypt(a, plain, nil)
	if _, err := Decrypt(b, env, nil); err == nil {
		t.Errorf("expected decrypt failure with wrong KEK")
	}
}

func TestInvalidKEK(t *testing.T) {
	if _, err := Encrypt([]byte("short"), []byte("x"), nil); !errors.Is(err, ErrInvalidKEK) {
		t.Errorf("encrypt: expected ErrInvalidKEK, got %v", err)
	}
	if _, err := Decrypt([]byte("short"), make([]byte, 100), nil); !errors.Is(err, ErrInvalidKEK) {
		t.Errorf("decrypt: expected ErrInvalidKEK, got %v", err)
	}
}

func TestShortCiphertext(t *testing.T) {
	kek, _ := GenerateKEK()
	if _, err := Decrypt(kek, []byte{0, 1, 2}, nil); !errors.Is(err, ErrCipherTooShort) {
		t.Errorf("expected ErrCipherTooShort, got %v", err)
	}
}

func TestNoncesAreFresh(t *testing.T) {
	// 同明文 + 同 KEK + 同 AAD，连加密 100 次，所有 envelope 必须互不相同
	// （随机 nonce 保证语义安全）。
	kek, _ := GenerateKEK()
	plain := []byte("idempotent-input")
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		env, err := Encrypt(kek, plain, nil)
		if err != nil {
			t.Fatalf("encrypt #%d: %v", i, err)
		}
		key := string(env)
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate envelope at iteration %d — nonce reused?", i)
		}
		seen[key] = struct{}{}
	}
}

func TestTamperedCiphertextRejected(t *testing.T) {
	kek, _ := GenerateKEK()
	env, _ := Encrypt(kek, []byte("payload"), nil)
	tampered := append([]byte(nil), env...)
	tampered[len(tampered)-1] ^= 0xFF // 篡改 tag 末字节
	if _, err := Decrypt(kek, tampered, nil); err == nil {
		t.Errorf("expected decrypt failure on tampered ciphertext")
	}
}

func TestGenerateKEK_Length(t *testing.T) {
	k, err := GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	if len(k) != KeySize {
		t.Errorf("KEK length: %d want %d", len(k), KeySize)
	}
}
