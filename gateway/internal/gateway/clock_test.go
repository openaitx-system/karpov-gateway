package gateway

import (
	"testing"
	"time"
)

// withLocal 临时把 time.Local 切到 loc，测试结束恢复。
func withLocal(t *testing.T, loc *time.Location, fn func()) {
	t.Helper()
	prev := time.Local
	time.Local = loc
	defer func() { time.Local = prev }()
	fn()
}

func TestLocalDate_FormatsInLocal(t *testing.T) {
	cn, _ := time.LoadLocation("Asia/Shanghai") // UTC+8
	la, _ := time.LoadLocation("America/Los_Angeles")

	// 一个边界点：UTC 2026-05-03 23:30，CN 已经是 2026-05-04 07:30
	probe := time.Date(2026, 5, 3, 23, 30, 0, 0, time.UTC)

	withLocal(t, time.UTC, func() {
		if got := localDate(probe); got != "2026-05-03" {
			t.Errorf("UTC localDate = %q, want 2026-05-03", got)
		}
	})
	withLocal(t, cn, func() {
		if got := localDate(probe); got != "2026-05-04" {
			t.Errorf("CN localDate = %q, want 2026-05-04", got)
		}
	})
	withLocal(t, la, func() {
		// LA 是 UTC-7（PDT），UTC 23:30 → LA 16:30 同日
		if got := localDate(probe); got != "2026-05-03" {
			t.Errorf("LA localDate = %q, want 2026-05-03", got)
		}
	})
}

func TestLocalMonth_FormatsInLocal(t *testing.T) {
	cn, _ := time.LoadLocation("Asia/Shanghai")
	// UTC 2026-04-30 23:30 → CN 2026-05-01 07:30（跨月）
	probe := time.Date(2026, 4, 30, 23, 30, 0, 0, time.UTC)

	withLocal(t, time.UTC, func() {
		if got := localMonth(probe); got != "2026-04" {
			t.Errorf("UTC localMonth = %q", got)
		}
	})
	withLocal(t, cn, func() {
		if got := localMonth(probe); got != "2026-05" {
			t.Errorf("CN localMonth = %q (跨月边界)", got)
		}
	})
}

func TestLocalStartOfNextDay(t *testing.T) {
	cn, _ := time.LoadLocation("Asia/Shanghai")
	withLocal(t, cn, func() {
		// CN 2026-05-04 22:00（= UTC 14:00）
		t0 := time.Date(2026, 5, 4, 22, 0, 0, 0, cn)
		next := localStartOfNextDay(t0)
		if next.Year() != 2026 || next.Month() != 5 || next.Day() != 5 {
			t.Errorf("nextDay = %v, want 2026-05-05", next)
		}
		if next.Hour() != 0 || next.Minute() != 0 || next.Location() != cn {
			t.Errorf("nextDay should be 00:00 in CN: %v loc=%v", next, next.Location())
		}
	})
}

func TestLocalStartOfNextMonth(t *testing.T) {
	cn, _ := time.LoadLocation("Asia/Shanghai")
	withLocal(t, cn, func() {
		// CN 2026-12-15 → next month should be 2027-01-01
		t0 := time.Date(2026, 12, 15, 10, 0, 0, 0, cn)
		next := localStartOfNextMonth(t0)
		if next.Year() != 2027 || next.Month() != 1 || next.Day() != 1 {
			t.Errorf("nextMonth = %v, want 2027-01-01", next)
		}
		if next.Hour() != 0 || next.Minute() != 0 || next.Location() != cn {
			t.Errorf("nextMonth should be 00:00 in CN: %v loc=%v", next, next.Location())
		}
	})
}

func TestLocalStartOfNextDay_AcrossDST(t *testing.T) {
	// 2026-03-08 是美东进入夏令时（02:00 跳到 03:00）；nextDay 应该正确返回 03-09 00:00
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("America/New_York unavailable")
	}
	withLocal(t, ny, func() {
		t0 := time.Date(2026, 3, 8, 12, 0, 0, 0, ny)
		next := localStartOfNextDay(t0)
		if next.Year() != 2026 || next.Month() != 3 || next.Day() != 9 || next.Hour() != 0 {
			t.Errorf("DST nextDay = %v, want 2026-03-09 00:00", next)
		}
	})
}
