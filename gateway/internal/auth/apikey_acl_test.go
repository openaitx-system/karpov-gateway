package auth

import (
	"context"
	"errors"
	"testing"
)

func TestAPIKey_IsIPAllowed(t *testing.T) {
	cases := []struct {
		name    string
		allow   []string
		client  string
		want    bool
	}{
		{"empty allowlist passes", nil, "1.2.3.4", true},
		{"empty allowlist passes empty IP", nil, "", true},

		{"single ipv4 hit", []string{"10.0.0.1"}, "10.0.0.1", true},
		{"single ipv4 miss", []string{"10.0.0.1"}, "10.0.0.2", false},

		{"cidr /24 hit", []string{"10.0.0.0/24"}, "10.0.0.42", true},
		{"cidr /24 miss", []string{"10.0.0.0/24"}, "10.0.1.42", false},

		{"multiple any hits", []string{"10.0.0.0/24", "192.168.1.5"}, "192.168.1.5", true},
		{"multiple all miss", []string{"10.0.0.0/24", "192.168.1.5"}, "8.8.8.8", false},

		{"ipv6 single hit", []string{"2001:db8::1"}, "2001:db8::1", true},
		{"ipv6 single miss", []string{"2001:db8::1"}, "2001:db8::2", false},
		{"ipv6 cidr hit", []string{"2001:db8::/32"}, "2001:db8:abcd::1", true},

		// IP 解析坏数据：客户端 IP 解析失败 ⇒ 拒
		{"bad client ip rejected", []string{"10.0.0.0/8"}, "not-an-ip", false},
		// 规则解析失败 ⇒ 跳过该条；其它仍可命中
		{"bad rule skipped, other hits", []string{"bogus", "10.0.0.0/8"}, "10.1.2.3", true},
		// 全部规则坏 ⇒ 没人能匹配 ⇒ 拒
		{"all bad rules reject", []string{"bogus", "????"}, "10.1.2.3", false},

		// host:port 形式应剥离端口
		{"host port stripped", []string{"10.0.0.1"}, "10.0.0.1:54321", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &APIKeyRecord{IPAllow: c.allow}
			if got := r.IsIPAllowed(c.client); got != c.want {
				t.Errorf("IPAllow=%v client=%q got=%v want=%v", c.allow, c.client, got, c.want)
			}
		})
	}
}

func TestAPIKey_HasScope(t *testing.T) {
	wildcard := &APIKeyRecord{Scopes: nil}
	if !wildcard.HasScope("anything:goes") {
		t.Error("nil scopes should be wildcard")
	}
	if !wildcard.HasAllScopes([]string{"a:b", "c:d"}) {
		t.Error("nil scopes should pass HasAllScopes too")
	}

	limited := &APIKeyRecord{Scopes: []string{"music:read", "billing:read"}}
	if !limited.HasScope("music:read") {
		t.Error("listed scope should hit")
	}
	if limited.HasScope("music:write") {
		t.Error("unlisted scope should miss")
	}
	if !limited.HasAllScopes([]string{"music:read"}) {
		t.Error("subset should pass HasAllScopes")
	}
	if !limited.HasAllScopes([]string{"music:read", "billing:read"}) {
		t.Error("exact list should pass")
	}
	if limited.HasAllScopes([]string{"music:read", "music:write"}) {
		t.Error("any missing should fail HasAllScopes")
	}
	if !limited.HasAllScopes(nil) {
		t.Error("empty required should pass")
	}
}

// 测试 module:* 模块级通配。
func TestAPIKey_HasScope_ModuleWildcard(t *testing.T) {
	k := &APIKeyRecord{Scopes: []string{"music:*", "billing:read"}}

	// music:* 涵盖 music namespace 下任意 action
	for _, s := range []string{"music:read", "music:write", "music:download"} {
		if !k.HasScope(s) {
			t.Errorf("music:* should grant %s", s)
		}
	}
	// billing:read 是精确而非通配
	if !k.HasScope("billing:read") {
		t.Error("exact billing:read should pass")
	}
	if k.HasScope("billing:write") {
		t.Error("exact billing:read should NOT cover billing:write")
	}
	// 跨模块不应通过
	if k.HasScope("quota:read") {
		t.Error("music:* should NOT cover quota:read")
	}
}

// 测试 "*" 全局通配。
func TestAPIKey_HasScope_GlobalWildcard(t *testing.T) {
	k := &APIKeyRecord{Scopes: []string{"*"}}
	for _, s := range []string{"music:read", "billing:write", "quota:read", "anything:goes"} {
		if !k.HasScope(s) {
			t.Errorf("* should grant %s", s)
		}
	}
}

// 攻击面：请求侧不允许通配（防止上层把外部参数透传到 HasScope 制造绕过）。
func TestAPIKey_HasScope_RequestSideWildcardRejected(t *testing.T) {
	k := &APIKeyRecord{Scopes: []string{"music:read"}}
	for _, s := range []string{"*", "music:*", "*:read", "*:*", ""} {
		if k.HasScope(s) {
			t.Errorf("request scope %q should be rejected (no wildcards in request)", s)
		}
	}
}

func TestAPIKey_HasAllScopes_Wildcard(t *testing.T) {
	k := &APIKeyRecord{Scopes: []string{"music:*"}}
	if !k.HasAllScopes([]string{"music:read", "music:write"}) {
		t.Error("module wildcard should satisfy multi-action HasAllScopes")
	}
	if k.HasAllScopes([]string{"music:read", "billing:read"}) {
		t.Error("cross-module should fail")
	}
}

func TestIsValidGrantedScope(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"*", true},
		{"music:*", true},
		{"music:read", true},
		{"billing:write", true},

		{"", false},
		{":", false},
		{":read", false},
		{"music:", false},
		{"Music:read", false}, // 大写
		{"music:Read", false},
		{"123:read", false},
		{"music:r e a d", false},
		{"a:b:c", false},
		{"*:*", false}, // 不允许超级通配的滥写
		{"*:read", false},
	}
	for _, c := range cases {
		if got := IsValidGrantedScope(c.in); got != c.want {
			t.Errorf("IsValidGrantedScope(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 集成测试：VerifyAPIKey 在 IP 不匹配时应返回 ErrAPIKeyIPNotAllowed。
func TestService_VerifyAPIKey_IPAllowlist(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	plain, rec, _ := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{
		Name:    "ip-restricted",
		IPAllow: []string{"10.0.0.0/8"},
	})

	// IP 不在白名单 → ErrAPIKeyIPNotAllowed
	if _, _, err := svc.VerifyAPIKey(context.Background(), plain, "8.8.8.8"); !errors.Is(err, ErrAPIKeyIPNotAllowed) {
		t.Fatalf("expected ErrAPIKeyIPNotAllowed, got %v", err)
	}

	// IP 在白名单 → 成功
	gotU, gotR, err := svc.VerifyAPIKey(context.Background(), plain, "10.1.2.3")
	if err != nil {
		t.Fatalf("verify with allowed ip: %v", err)
	}
	if gotU.ID != user.ID || gotR.ID != rec.ID {
		t.Errorf("verify mismatch")
	}

	// 空 clientIP 但有白名单 → 拒（无法证明在内）
	if _, _, err := svc.VerifyAPIKey(context.Background(), plain, ""); !errors.Is(err, ErrAPIKeyIPNotAllowed) {
		t.Errorf("empty client ip with allowlist should be rejected: %v", err)
	}
}

// CreateAPIKey 应拒非法 scope；并应去重 + 修剪空格。
func TestService_CreateAPIKey_ScopeNormalization(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	ctx := context.Background()

	// 非法 scope 拒绝
	if _, _, err := svc.CreateAPIKey(ctx, user.ID, CreateAPIKeyInput{
		Name:   "bad",
		Scopes: []string{"BadCase:read"},
	}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("expected ErrInvalidScope, got %v", err)
	}
	if _, _, err := svc.CreateAPIKey(ctx, user.ID, CreateAPIKeyInput{
		Name:   "bad2",
		Scopes: []string{"music:"}, // 缺 action
	}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("expected ErrInvalidScope for empty action, got %v", err)
	}

	// 合法 + 重复 + 空白：去重 + 去 trim
	_, rec, err := svc.CreateAPIKey(ctx, user.ID, CreateAPIKeyInput{
		Name:   "good",
		Scopes: []string{"music:read", "  music:read ", "billing:*", ""},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(rec.Scopes) != 2 {
		t.Errorf("dedup failed: %v", rec.Scopes)
	}
}

// CreateAPIKey 应拒非法 IP/CIDR；并去重。
func TestService_CreateAPIKey_IPAllowNormalization(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	ctx := context.Background()

	if _, _, err := svc.CreateAPIKey(ctx, user.ID, CreateAPIKeyInput{
		Name:    "bad-ip",
		IPAllow: []string{"not-an-ip"},
	}); !errors.Is(err, ErrInvalidIPAllow) {
		t.Fatalf("expected ErrInvalidIPAllow, got %v", err)
	}
	if _, _, err := svc.CreateAPIKey(ctx, user.ID, CreateAPIKeyInput{
		Name:    "bad-cidr",
		IPAllow: []string{"10.0.0.0/99"},
	}); !errors.Is(err, ErrInvalidIPAllow) {
		t.Fatalf("expected ErrInvalidIPAllow for bad CIDR, got %v", err)
	}

	_, rec, err := svc.CreateAPIKey(ctx, user.ID, CreateAPIKeyInput{
		Name:    "good-ip",
		IPAllow: []string{"10.0.0.0/8", " 10.0.0.0/8 ", "192.168.1.1", ""},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(rec.IPAllow) != 2 {
		t.Errorf("dedup failed: %v", rec.IPAllow)
	}
}

func TestService_VerifyAPIKey_NoIPAllow_AnyClient(t *testing.T) {
	svc, user := newSvcWithAPIKeys(t)
	plain, _, _ := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{
		Name: "no-ip-restrict",
	})
	for _, ip := range []string{"", "8.8.8.8", "192.168.1.1", "2001:db8::1"} {
		if _, _, err := svc.VerifyAPIKey(context.Background(), plain, ip); err != nil {
			t.Errorf("client %q should pass without allowlist: %v", ip, err)
		}
	}
}
