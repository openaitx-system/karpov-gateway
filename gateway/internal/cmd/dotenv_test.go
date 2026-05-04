package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempDotEnvDir 把 t.TempDir() 设为当前工作目录，并写入指定 .env 文件。
// 返回 cleanup 不需要：t.Cleanup 自动恢复 cwd。
func withTempDotEnvDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestLoadDotEnv_LoadsValuesFromFile(t *testing.T) {
	t.Setenv("MGW_REDIS", "")
	_ = os.Unsetenv("MGW_REDIS")
	withTempDotEnvDir(t, map[string]string{
		".env": "MGW_REDIS=fromfile:6379\nMGW_REDIS_PASSWORD=filepwd\n",
	})
	ResetDotEnvForTest()
	loaded := LoadDotEnv()
	if len(loaded) == 0 {
		t.Fatal("LoadDotEnv must return at least one path")
	}
	if got := os.Getenv("MGW_REDIS"); got != "fromfile:6379" {
		t.Errorf("MGW_REDIS = %q, want fromfile:6379", got)
	}
	if got := os.Getenv("MGW_REDIS_PASSWORD"); got != "filepwd" {
		t.Errorf("MGW_REDIS_PASSWORD = %q, want filepwd", got)
	}
}

func TestLoadDotEnv_DoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("MGW_REDIS", "from-os:6379")
	withTempDotEnvDir(t, map[string]string{
		".env": "MGW_REDIS=fromfile:6379\n",
	})
	ResetDotEnvForTest()
	LoadDotEnv()
	if got := os.Getenv("MGW_REDIS"); got != "from-os:6379" {
		t.Errorf("system env must win, got %q", got)
	}
}

func TestLoadDotEnv_LocalOverridesEnv(t *testing.T) {
	_ = os.Unsetenv("MGW_REDIS")
	withTempDotEnvDir(t, map[string]string{
		".env":       "MGW_REDIS=fromfile:6379\n",
		".env.local": "MGW_REDIS=fromlocal:6379\n",
	})
	ResetDotEnvForTest()
	LoadDotEnv()
	if got := os.Getenv("MGW_REDIS"); got != "fromlocal:6379" {
		t.Errorf(".env.local must win over .env, got %q", got)
	}
}

func TestLoadDotEnv_RunsOnlyOnce(t *testing.T) {
	_ = os.Unsetenv("MGW_REDIS")
	withTempDotEnvDir(t, map[string]string{
		".env": "MGW_REDIS=first\n",
	})
	ResetDotEnvForTest()
	LoadDotEnv()
	if got := os.Getenv("MGW_REDIS"); got != "first" {
		t.Fatalf("first load = %q, want first", got)
	}
	// 即便文件改了，sync.Once 拒绝再读
	if err := os.WriteFile(".env", []byte("MGW_REDIS=second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("MGW_REDIS")
	LoadDotEnv()
	if got := os.Getenv("MGW_REDIS"); got != "" {
		t.Errorf("second LoadDotEnv re-loaded file (got %q); should be no-op", got)
	}
}

func TestNewLoader_PicksUpDotEnv(t *testing.T) {
	_ = os.Unsetenv("MGW_AUTH_REDIS")
	_ = os.Unsetenv("MGW_REDIS")
	withTempDotEnvDir(t, map[string]string{
		".env": "MGW_REDIS=auto:6379\n",
	})
	ResetDotEnvForTest()
	l := NewLoader("auth")
	l.String("redis", "127.0.0.1:6379", "")
	if _, err := l.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := l.GetString("redis"); got != "auto:6379" {
		t.Errorf("Loader must read .env via NewLoader, got %q", got)
	}
}

func TestNewLoader_LegacyEnvFromDotEnv(t *testing.T) {
	_ = os.Unsetenv("REDIS_PASSWORD")
	_ = os.Unsetenv("MGW_REDIS_PASSWORD")
	_ = os.Unsetenv("MGW_AUTH_REDIS_PASSWORD")
	withTempDotEnvDir(t, map[string]string{
		".env": "REDIS_PASSWORD=fromdotenv\n",
	})
	ResetDotEnvForTest()
	l := NewLoader("auth")
	l.String("redis-password", "", "")
	l.LegacyEnv("redis-password", "REDIS_PASSWORD")
	if _, err := l.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := l.GetString("redis-password"); got != "fromdotenv" {
		t.Errorf("legacy env from .env: got %q", got)
	}
}

func TestLoadDotEnv_ReturnsAbsolutePaths(t *testing.T) {
	withTempDotEnvDir(t, map[string]string{".env": "FOO=bar\n"})
	ResetDotEnvForTest()
	loaded := LoadDotEnv()
	for _, p := range loaded {
		if !filepath.IsAbs(p) {
			t.Errorf("%q is not absolute", p)
		}
		if !strings.HasSuffix(p, ".env") && !strings.HasSuffix(p, ".env.local") {
			t.Errorf("%q does not look like a dotenv file", p)
		}
	}
}
