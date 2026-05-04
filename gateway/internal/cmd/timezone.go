package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ResolveTimeZone 把用户给的 timezone 字符串解析成 *time.Location。
//
// 语义：
//   - 空字符串 → 跟随系统（time.Local，受 $TZ env 与 /etc/localtime 控制）。
//   - "local" / "system" / "auto"（任何大小写）→ 同上，等价空字符串；显式表达"我要系统默认"。
//   - "UTC" → time.UTC（time.LoadLocation("UTC") 也行，但这条短路省掉一次 IANA 查表）。
//   - 其它非空 → time.LoadLocation(value)；失败把原值带回错误，便于 UI / log 反馈具体哪条不识别。
//
// 返回的 *time.Location 永远非 nil；err 非 nil 时 *time.Location 为 nil。
func ResolveTimeZone(value string) (*time.Location, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Local, nil
	}
	switch strings.ToLower(v) {
	case "local", "system", "auto":
		return time.Local, nil
	case "utc":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(v)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w (expected IANA name like Asia/Shanghai or UTC)", value, err)
	}
	return loc, nil
}

// ApplyProcessTimeZone 把进程级 time.Local 与 $TZ env 同步到 loc。
//
// 副作用：
//   - time.Local = loc：所有不带 .UTC()/.In() 的 time.Now() 都会用 loc；
//     代码库里业务时间都已显式 .UTC()，业务语义不会变。
//   - os.Setenv("TZ", loc.String())：让任何依赖 $TZ 的系统调用（subprocess、libc）
//     与 Go runtime 一致。loc.String() 返回 IANA 名（如 "Asia/Shanghai"）；
//     loc==time.Local 时返回 "Local"，此时跳过 os.Setenv 以免污染父环境。
//
// 传 nil 安全：no-op。
func ApplyProcessTimeZone(loc *time.Location) {
	if loc == nil {
		return
	}
	time.Local = loc
	if loc == time.Local {
		// 极端情况：调用方传进来的就是当前 time.Local，跳过 env 写入。
		return
	}
	name := loc.String()
	if name == "" || name == "Local" {
		return
	}
	_ = os.Setenv("TZ", name)
}

// TimeZoneName 返回 *time.Location 的 IANA 名（Asia/Shanghai / UTC / Local）。
// nil → "Local"（和 time.Local.String() 行为一致）。
func TimeZoneName(loc *time.Location) string {
	if loc == nil {
		return "Local"
	}
	return loc.String()
}
