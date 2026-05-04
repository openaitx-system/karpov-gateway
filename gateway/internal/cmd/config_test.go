package cmd

import (
	"testing"
)

func TestLoader_PrecedenceCLIOverEnv(t *testing.T) {
	t.Setenv("MGW_AUTH_REDIS", "from-svc-env:6379")
	l := NewLoader("auth")
	l.String("redis", "127.0.0.1:6379", "")
	if _, err := l.Parse([]string{"-redis", "from-cli:6379"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := l.GetString("redis"); got != "from-cli:6379" {
		t.Errorf("CLI must beat env, got %q", got)
	}
}

func TestLoader_ServiceEnvOverGlobalEnv(t *testing.T) {
	t.Setenv("MGW_REDIS", "global:6379")
	t.Setenv("MGW_AUTH_REDIS", "auth-svc:6379")
	l := NewLoader("auth")
	l.String("redis", "default:6379", "")
	_, _ = l.Parse(nil)
	if got := l.GetString("redis"); got != "auth-svc:6379" {
		t.Errorf("service env must beat global env, got %q", got)
	}
}

func TestLoader_GlobalEnvOverDefault(t *testing.T) {
	t.Setenv("MGW_REDIS", "global:6379")
	l := NewLoader("auth")
	l.String("redis", "default:6379", "")
	_, _ = l.Parse(nil)
	if got := l.GetString("redis"); got != "global:6379" {
		t.Errorf("global env must beat default, got %q", got)
	}
}

func TestLoader_DefaultWhenNothingSet(t *testing.T) {
	l := NewLoader("auth")
	l.String("redis", "default:6379", "")
	_, _ = l.Parse(nil)
	if got := l.GetString("redis"); got != "default:6379" {
		t.Errorf("default fallback, got %q", got)
	}
}

func TestLoader_LegacyEnvFallback(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "legacy-pwd")
	l := NewLoader("auth")
	l.String("redis-password", "", "")
	l.LegacyEnv("redis-password", "REDIS_PASSWORD")
	_, _ = l.Parse(nil)
	if got := l.GetString("redis-password"); got != "legacy-pwd" {
		t.Errorf("legacy env fallback, got %q", got)
	}
}

func TestLoader_LegacyEnvLosesToServiceEnv(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "legacy")
	t.Setenv("MGW_AUTH_REDIS_PASSWORD", "svc")
	l := NewLoader("auth")
	l.String("redis-password", "", "")
	l.LegacyEnv("redis-password", "REDIS_PASSWORD")
	_, _ = l.Parse(nil)
	if got := l.GetString("redis-password"); got != "svc" {
		t.Errorf("service env must beat legacy, got %q", got)
	}
}

func TestLoader_BoolDuration(t *testing.T) {
	t.Setenv("MGW_AUTH_HIBP", "false")
	t.Setenv("MGW_AUTH_SESSION_TTL", "30m")
	l := NewLoader("auth")
	l.Bool("hibp", true, "")
	l.Duration("session-ttl", 0, "")
	_, _ = l.Parse(nil)
	if l.GetBool("hibp") != false {
		t.Error("bool from env failed")
	}
	if l.GetDuration("session-ttl").Minutes() != 30 {
		t.Errorf("duration from env: %s", l.GetDuration("session-ttl"))
	}
}

func TestLoader_FlagDashesToEnvUnderscores(t *testing.T) {
	t.Setenv("MGW_AUTH_BOOTSTRAP_EMAIL", "ops@example.com")
	l := NewLoader("auth")
	l.String("bootstrap-email", "default@example.com", "")
	_, _ = l.Parse(nil)
	if got := l.GetString("bootstrap-email"); got != "ops@example.com" {
		t.Errorf("dash→underscore mapping: got %q", got)
	}
}
