package qqmusic

import (
	"encoding/json"
	"testing"
)

func TestCredential_UnmarshalSnake(t *testing.T) {
	raw := []byte(`{"musicid":111,"musickey":"abc","musickey_create_time":1000,"key_expires_in":60}`)
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.MusicID != 111 || c.MusicKey != "abc" || c.MusicKeyCreateTime != 1000 || c.KeyExpiresIn != 60 {
		t.Errorf("snake parse failed: %+v", c)
	}
	if c.LoginType != 2 {
		t.Errorf("login_type infer != 2: %d", c.LoginType)
	}
}

func TestCredential_UnmarshalCamel(t *testing.T) {
	raw := []byte(`{"musicid":222,"musickey":"W_X_qq","musickeyCreateTime":2000,"keyExpiresIn":3600,"encryptUin":"e","loginType":7}`)
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.MusicKeyCreateTime != 2000 || c.KeyExpiresIn != 3600 {
		t.Errorf("camel parse failed: %+v", c)
	}
	if c.LoginType != 7 {
		t.Errorf("explicit loginType lost: %d", c.LoginType)
	}
	if c.EncryptUin != "e" {
		t.Errorf("encryptUin: %q", c.EncryptUin)
	}
}

func TestCredential_LoginTypeInferWX(t *testing.T) {
	raw := []byte(`{"musickey":"W_X_payload"}`)
	var c Credential
	_ = json.Unmarshal(raw, &c)
	if c.LoginType != 1 {
		t.Errorf("W_X prefix should infer login_type=1, got %d", c.LoginType)
	}
}

func TestCredential_IsExpired(t *testing.T) {
	c := Credential{MusicKeyCreateTime: 0, KeyExpiresIn: 0}
	if c.IsExpired() {
		t.Errorf("zero expiry should not be expired")
	}
	c2 := Credential{MusicKeyCreateTime: 1, KeyExpiresIn: 1}
	if !c2.IsExpired() {
		t.Errorf("ancient credential should be expired")
	}
}
