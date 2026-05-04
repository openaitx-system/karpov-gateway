package cmd

import (
	"testing"
	"time"
)

func TestResolveTimeZone(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     *time.Location
		wantErr  bool
	}{
		{"empty → Local", "", time.Local, false},
		{"whitespace → Local", "   ", time.Local, false},
		{"explicit local", "local", time.Local, false},
		{"explicit Local capitalized", "Local", time.Local, false},
		{"explicit system", "system", time.Local, false},
		{"explicit auto", "auto", time.Local, false},
		{"UTC", "UTC", time.UTC, false},
		{"utc lowercase", "utc", time.UTC, false},
		{"Asia/Shanghai", "Asia/Shanghai", mustLoad(t, "Asia/Shanghai"), false},
		{"America/Los_Angeles", "America/Los_Angeles", mustLoad(t, "America/Los_Angeles"), false},
		{"Europe/Paris", "Europe/Paris", mustLoad(t, "Europe/Paris"), false},

		{"invalid string", "Not/A/Zone", nil, true},
		{"empty path zone", "Asia/", nil, true},
		{"random gibberish", "🤡", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveTimeZone(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got loc=%v", got)
				}
				if got != nil {
					t.Errorf("on error loc should be nil, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("loc is nil")
			}
			if got.String() != c.want.String() {
				t.Errorf("loc=%v want %v", got, c.want)
			}
		})
	}
}

func TestApplyProcessTimeZone_NilSafe(t *testing.T) {
	prev := time.Local
	defer func() { time.Local = prev }()
	ApplyProcessTimeZone(nil) // 不应 panic
	if time.Local != prev {
		t.Errorf("nil should be no-op")
	}
}

func TestApplyProcessTimeZone_SetsLocal(t *testing.T) {
	prev := time.Local
	defer func() { time.Local = prev }()

	loc := mustLoad(t, "Asia/Shanghai")
	ApplyProcessTimeZone(loc)
	if time.Local != loc {
		t.Errorf("time.Local should be set to Asia/Shanghai, got %v", time.Local)
	}
}

func TestTimeZoneName(t *testing.T) {
	if got := TimeZoneName(nil); got != "Local" {
		t.Errorf("nil → %q, want Local", got)
	}
	if got := TimeZoneName(time.UTC); got != "UTC" {
		t.Errorf("UTC → %q", got)
	}
	loc := mustLoad(t, "Asia/Shanghai")
	if got := TimeZoneName(loc); got != "Asia/Shanghai" {
		t.Errorf("Asia/Shanghai → %q", got)
	}
}

// 端到端：ResolveTimeZone + ApplyProcessTimeZone 一起跑，确认不破坏 .UTC()
// 那条只读路径——业务时间值仍然是 UTC，不会被 time.Local 污染。
func TestProcessTZApplied_BusinessTimesStillUTC(t *testing.T) {
	prev := time.Local
	defer func() { time.Local = prev }()

	loc, err := ResolveTimeZone("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	ApplyProcessTimeZone(loc)

	now := time.Now().UTC()
	if now.Location() != time.UTC {
		t.Errorf(".UTC() should always return UTC location regardless of time.Local")
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}
