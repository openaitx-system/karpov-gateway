package crypto

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
)

func TestStaticKeyProvider_OK(t *testing.T) {
	raw := make([]byte, KeySize)
	for i := range raw {
		raw[i] = byte(i)
	}
	p, err := NewStaticKeyProvider(raw)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got, err := p.Get(context.Background(), "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != KeySize {
		t.Errorf("len: %d", len(got))
	}
	// 修改 raw 不应影响 provider 内部副本
	raw[0] = 0xFF
	got2, _ := p.Get(context.Background(), "")
	if got2[0] == 0xFF {
		t.Errorf("provider should hold its own copy")
	}
}

func TestStaticKeyProvider_BadLength(t *testing.T) {
	if _, err := NewStaticKeyProvider([]byte("short")); !errors.Is(err, ErrInvalidKEK) {
		t.Errorf("expected ErrInvalidKEK, got %v", err)
	}
}

func TestEnvKeyProvider_OK(t *testing.T) {
	raw := make([]byte, KeySize)
	t.Setenv("KEK_TEST_PROVIDER", hex.EncodeToString(raw))

	p, err := NewEnvKeyProvider("KEK_TEST_PROVIDER")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.EnvName() != "KEK_TEST_PROVIDER" {
		t.Errorf("env name: %q", p.EnvName())
	}
	kek, err := p.Get(context.Background(), "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(kek) != KeySize {
		t.Errorf("len: %d", len(kek))
	}
}

func TestEnvKeyProvider_Missing(t *testing.T) {
	if _, err := NewEnvKeyProvider("KEK_DEFINITELY_MISSING_ZZZ"); err == nil {
		t.Errorf("expected error")
	}
}

func TestEnvKeyProvider_BadHex(t *testing.T) {
	t.Setenv("KEK_BAD", "not-hex")
	if _, err := NewEnvKeyProvider("KEK_BAD"); err == nil {
		t.Errorf("expected error for bad hex")
	}
}

// 验证 KeyProvider 接口可被 Static / Env 共同满足
func TestKeyProvider_InterfaceConformance(t *testing.T) {
	raw := make([]byte, KeySize)
	t.Setenv("KEK_IFACE", hex.EncodeToString(raw))

	var providers []KeyProvider

	sp, err := NewStaticKeyProvider(raw)
	if err != nil {
		t.Fatalf("static: %v", err)
	}
	providers = append(providers, sp)

	ep, err := NewEnvKeyProvider("KEK_IFACE")
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	providers = append(providers, ep)

	for i, p := range providers {
		kek, err := p.Get(context.Background(), "")
		if err != nil || len(kek) != KeySize {
			t.Errorf("provider[%d] failed: kek=%d err=%v", i, len(kek), err)
		}
	}
}
