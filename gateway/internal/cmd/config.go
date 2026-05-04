package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Loader 把 pflag 与 viper 缝合：
//
// **优先级**（高 → 低）：
//  1. CLI flag（用户显式传 `-redis 1.2.3.4`）
//  2. service 专属 env：`MGW_<SERVICE>_<FLAG>`，例如 `MGW_AUTH_REDIS`
//  3. 全局共享 env：`MGW_<FLAG>`，例如 `MGW_REDIS`、`MGW_REDIS_PASSWORD`
//  4. 历史兼容 env（无 MGW_ 前缀），例如 `REDIS_PASSWORD`、`POOL_KEK_HEX`
//  5. flag default
//
// flag 名 `-redis-password` → env key `REDIS_PASSWORD`（横线转下划线 + 大写）。
//
// 使用：
//
//	v := cmd.NewLoader("auth")
//	fs := v.FlagSet()
//	v.String(fs, "redis", "127.0.0.1:6379", "Redis address")
//	v.String(fs, "redis-password", "", "Redis password")
//	if stop, err := v.Parse(args); err != nil { ... } else if stop { return nil }
//	addr := v.GetString("redis")
//	password := v.GetString("redis-password")
type Loader struct {
	service string
	v       *viper.Viper
	fs      *pflag.FlagSet
	// legacyEnvKeys 是只读 env 名（如 POOL_KEK_HEX），用于 flag → env 反查
	// 无 MGW_ 前缀也能被 GetString 取到。
	legacyEnvKeys map[string][]string
}

// NewLoader 构造一个 service 专属配置加载器。
//
// service 名仅作为 env prefix（如 "auth" → MGW_AUTH_*）和 FlagSet 名。
//
// 副作用：第一次调用时会触发一次性 .env 文件加载（详见 LoadDotEnv），
// 让 ./.env 中的键值对成为系统 env 兜底。系统 env 永远优先于文件。
func NewLoader(service string) *Loader {
	// 在 viper.AutomaticEnv 之前装载 .env，确保 BindEnv 能命中。
	LoadDotEnv()

	v := viper.New()
	v.SetTypeByDefaultValue(true)
	// 标准 env 解析：MGW_<SERVICE>_<KEY>
	v.SetEnvPrefix("MGW_" + strings.ToUpper(service))
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	fs := pflag.NewFlagSet(service, pflag.ContinueOnError)
	return &Loader{
		service:       service,
		v:             v,
		fs:            fs,
		legacyEnvKeys: map[string][]string{},
	}
}

// FlagSet 返回内部 pflag.FlagSet（用于 fs.Usage 等定制）。
func (l *Loader) FlagSet() *pflag.FlagSet { return l.fs }

// String 注册一个字符串 flag，并把它绑定到 viper。
func (l *Loader) String(name, def, usage string) {
	l.fs.String(name, def, usage)
}

// Bool 注册 bool flag。
func (l *Loader) Bool(name string, def bool, usage string) {
	l.fs.Bool(name, def, usage)
}

// Int 注册 int flag。
func (l *Loader) Int(name string, def int, usage string) {
	l.fs.Int(name, def, usage)
}

// Duration 注册 duration flag。
func (l *Loader) Duration(name string, def time.Duration, usage string) {
	l.fs.Duration(name, def, usage)
}

// LegacyEnv 让 flag 在解析 env 时额外查找若干"老"环境变量（无 MGW_ 前缀），
// 比如 POOL_KEK_HEX。仅在 service 专属 env 与 MGW_<KEY> 都没设值时生效。
//
// 多个 envName 时按顺序查找，先命中的胜出。
func (l *Loader) LegacyEnv(flagName string, envNames ...string) {
	if len(envNames) == 0 {
		return
	}
	l.legacyEnvKeys[flagName] = append(l.legacyEnvKeys[flagName], envNames...)
}

// Parse 解析 args 并把 flag 绑定到 viper。
//
// 返回 (stop, err)：stop=true 表示遇到 -h/-help（调用方 return nil）。
//
// 兼容性：args 中以单短横线开头且 key 长度 > 1（如 `-redis`）会被自动规范化为
// 双短横线（`--redis`），保持与 stdlib `flag` 调用习惯一致。
func (l *Loader) Parse(args []string) (stop bool, err error) {
	if err := l.fs.Parse(normalizeLongFlagDashes(args)); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return true, nil
		}
		return false, err
	}
	if err := l.v.BindPFlags(l.fs); err != nil {
		return false, fmt.Errorf("bind pflags: %w", err)
	}
	// 全局共享 env（无 service 前缀）：MGW_REDIS / MGW_REDIS_PASSWORD 等。
	// 用 BindEnv 的多参数形式让 viper 在原 prefix 命中失败时再查全局。
	l.fs.VisitAll(func(f *pflag.Flag) {
		key := f.Name
		envName := strings.ToUpper(strings.NewReplacer("-", "_").Replace(key))
		// 顺序：MGW_<SERVICE>_<KEY> → MGW_<KEY> → 法定 legacy
		envChain := []string{
			"MGW_" + strings.ToUpper(l.service) + "_" + envName,
			"MGW_" + envName,
		}
		envChain = append(envChain, l.legacyEnvKeys[key]...)
		_ = l.v.BindEnv(append([]string{key}, envChain...)...)
	})
	return false, nil
}

// GetString 读字符串配置（按优先级链）。
func (l *Loader) GetString(name string) string { return l.v.GetString(name) }

// GetBool 读 bool 配置。
func (l *Loader) GetBool(name string) bool { return l.v.GetBool(name) }

// GetInt 读 int 配置。
func (l *Loader) GetInt(name string) int { return l.v.GetInt(name) }

// GetDuration 读 duration 配置。
func (l *Loader) GetDuration(name string) time.Duration { return l.v.GetDuration(name) }

// Viper 返回底层 viper 实例（高级用途，如读 yaml）。
func (l *Loader) Viper() *viper.Viper { return l.v }

// Getenv 在 Loader 之外也提供一个轻量 fallback，用于 cli main 入口
// （如 cmd/qqmusic-gateway 自身的 -redis-password）也能兜底读 env。
func Getenv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// normalizeLongFlagDashes 把 `-name`（单短横线 + 长键）转成 `--name`，
// 让 pflag 不把它当成多个 short flag 序列。
//
// 不处理：单字符 short flag（`-v`、`-h`）、`--`、值参数（紧跟 flag 后的 token）。
func normalizeLongFlagDashes(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--" || !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			out = append(out, a)
			continue
		}
		// a 以 "-" 开头但不是 "--..."；解析 name 是否长于一字符
		key := strings.TrimPrefix(a, "-")
		if eq := strings.IndexByte(key, '='); eq >= 0 {
			key = key[:eq]
		}
		if len(key) > 1 {
			out = append(out, "-"+a) // "-redis" → "--redis"
			continue
		}
		out = append(out, a)
	}
	return out
}
