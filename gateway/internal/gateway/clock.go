package gateway

import "time"

// 业务"今天 / 本月"语义的时区入口。
//
// 设计：
//   - usage_aggregates_daily.date 是 PG 的 DATE（无 TZ），按"日历天"聚合。
//     如果用 UTC 截日，亚洲早上 8 点之前所有请求都会被算成"昨天"，与用户预期不符。
//   - 部署侧通过 --timezone（gateway runner）配置 time.Local；这里所有调用统一
//     从 time.Now() 拿当前时间（自动落到 Local），让"今天"= 部署 TZ 的今天。
//   - quota 窗口的"下一日 / 下一月"重置点也用同一时区，retry-after 才与用户预期一致。
//
// 业务时间值（订单 created_at / token 过期等）仍应保持 UTC 写入；这套 helper
// 只用于聚合 / 配额边界这些"日历天"语义的场景。

// localNow 返回当前 wall-clock；等价 time.Now()，命名是为了表达意图：
// "我要日历语义而非单调时间"。
func localNow() time.Time { return time.Now() }

// localDate 把任意 time 在 time.Local 下格式化成 YYYY-MM-DD。
func localDate(t time.Time) string {
	return t.In(time.Local).Format("2006-01-02")
}

// localMonth 把任意 time 在 time.Local 下格式化成 YYYY-MM。
func localMonth(t time.Time) string {
	return t.In(time.Local).Format("2006-01")
}

// localStartOfNextDay 给定 t，返回它在本地时区的"明天 00:00:00"。
// 用于配额满时计算 retry-after 秒数。
func localStartOfNextDay(t time.Time) time.Time {
	loc := time.Local
	lt := t.In(loc)
	y, m, d := lt.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}

// localStartOfNextMonth 给定 t，返回它在本地时区的"下月 1 号 00:00:00"。
func localStartOfNextMonth(t time.Time) time.Time {
	loc := time.Local
	lt := t.In(loc)
	y, m, _ := lt.Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
}
