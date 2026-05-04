package store

import (
	"testing"
	"time"
)

func TestNonZeroInt32(t *testing.T) {
	cases := []struct {
		v, def, want int32
	}{
		{0, 8, 8},
		{-1, 8, 8},
		{5, 8, 5},
	}
	for _, c := range cases {
		if got := nonZeroInt32(c.v, c.def); got != c.want {
			t.Errorf("nonZeroInt32(%d,%d)=%d want %d", c.v, c.def, got, c.want)
		}
	}
}

func TestNonZeroDuration(t *testing.T) {
	cases := []struct {
		v, def, want time.Duration
	}{
		{0, time.Second, time.Second},
		{-1, time.Second, time.Second},
		{2 * time.Second, time.Second, 2 * time.Second},
	}
	for _, c := range cases {
		if got := nonZeroDuration(c.v, c.def); got != c.want {
			t.Errorf("nonZeroDuration(%v,%v)=%v want %v", c.v, c.def, got, c.want)
		}
	}
}

func TestAppendSearchPath(t *testing.T) {
	cases := []struct {
		name, dsn, schema string
		wantContains      string
	}{
		{
			name:         "empty query",
			dsn:          "postgres://u:p@h:5432/db?sslmode=disable",
			schema:       "auth",
			wantContains: "search_path=auth",
		},
		{
			name:         "preserve existing search_path",
			dsn:          "postgres://u:p@h:5432/db?sslmode=disable&search_path=public",
			schema:       "billing",
			wantContains: "search_path=billing%2Cpublic",
		},
		{
			name:         "schema already present",
			dsn:          "postgres://u:p@h:5432/db?search_path=quota",
			schema:       "quota",
			wantContains: "search_path=quota",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := AppendSearchPath(c.dsn, c.schema)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !contains(got, c.wantContains) {
				t.Errorf("got %q want substring %q", got, c.wantContains)
			}
		})
	}
}

func TestAppendSearchPath_BadDSN(t *testing.T) {
	if _, err := AppendSearchPath("\x00://bad", "auth"); err == nil {
		t.Errorf("expected error on malformed dsn")
	}
}

func TestAppendTimeZone(t *testing.T) {
	cases := []struct {
		name, dsn, tz string
		wantSubstr    string
		wantUnchanged bool
	}{
		{
			name:       "empty tz keeps dsn unchanged",
			dsn:        "postgres://u:p@h:5432/db?sslmode=disable",
			tz:         "",
			wantUnchanged: true,
		},
		{
			name:       "set tz on dsn with sslmode",
			dsn:        "postgres://u:p@h:5432/db?sslmode=disable",
			tz:         "Asia/Shanghai",
			wantSubstr: "timezone=Asia%2FShanghai",
		},
		{
			name:       "UTC short name",
			dsn:        "postgres://u:p@h:5432/db",
			tz:         "UTC",
			wantSubstr: "timezone=UTC",
		},
		{
			name:       "override existing timezone",
			dsn:        "postgres://u:p@h:5432/db?timezone=UTC",
			tz:         "America/Los_Angeles",
			wantSubstr: "timezone=America%2FLos_Angeles",
		},
		{
			name:       "preserve other params",
			dsn:        "postgres://u:p@h:5432/db?sslmode=disable&search_path=auth",
			tz:         "Europe/Paris",
			wantSubstr: "timezone=Europe%2FParis",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := AppendTimeZone(c.dsn, c.tz)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if c.wantUnchanged {
				if got != c.dsn {
					t.Errorf("expected unchanged, got %q", got)
				}
				return
			}
			if !contains(got, c.wantSubstr) {
				t.Errorf("got %q want substring %q", got, c.wantSubstr)
			}
			// 保持原有参数不丢
			if c.dsn != "" && contains(c.dsn, "sslmode=") && !contains(got, "sslmode=") {
				t.Errorf("sslmode param lost: %q", got)
			}
			if c.dsn != "" && contains(c.dsn, "search_path=") && !contains(got, "search_path=") {
				t.Errorf("search_path param lost: %q", got)
			}
		})
	}
}

func TestAppendTimeZone_BadDSN(t *testing.T) {
	if _, err := AppendTimeZone("\x00://bad", "Asia/Shanghai"); err == nil {
		t.Errorf("expected error on malformed dsn")
	}
}

// AppendTimeZone 与 AppendSearchPath 可叠加使用，互不破坏。
func TestAppendTimeZone_StacksWithSearchPath(t *testing.T) {
	dsn := "postgres://u:p@h:5432/db?sslmode=disable"
	withSchema, err := AppendSearchPath(dsn, "auth")
	if err != nil {
		t.Fatal(err)
	}
	withTZ, err := AppendTimeZone(withSchema, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sslmode=disable",
		"search_path=auth",
		"timezone=Asia%2FShanghai",
	} {
		if !contains(withTZ, want) {
			t.Errorf("missing %q in stacked dsn: %q", want, withTZ)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
