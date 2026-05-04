package gateway

import (
	"errors"
	"strings"
	"testing"
)

func TestDiagnosePgError_Cases(t *testing.T) {
	dsn := "postgres://mgw:secret@127.0.0.1:5432/mgw?sslmode=disable"
	cases := []struct {
		name string
		err  error
		want string // substring
	}{
		{
			name: "auth_failed",
			err:  errors.New(`pq: password authentication failed for user "mgw"`),
			want: "POSTGRES_PASSWORD",
		},
		{
			name: "role_missing",
			err:  errors.New(`pq: role "mgw" does not exist`),
			want: "POSTGRES_USER",
		},
		{
			name: "db_missing",
			err:  errors.New(`pq: database "musicgw" does not exist`),
			want: "POSTGRES_DB",
		},
		{
			name: "conn_refused",
			err:  errors.New(`dial tcp 127.0.0.1:5432: connect: connection refused`),
			want: "docker compose ps",
		},
		{
			name: "timeout",
			err:  errors.New(`dial tcp 1.2.3.4:5432: i/o timeout`),
			want: "PG 不可达",
		},
		{
			name: "ssl_handshake",
			err:  errors.New(`tls: handshake failure`),
			want: "sslmode=disable",
		},
		{
			name: "unknown",
			err:  errors.New(`mystery error`),
			want: "PG 连接失败",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diagnosePgError(dsn, tc.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("expected hint to contain %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDiagnosePgError_NoPasswordLeak(t *testing.T) {
	dsn := "postgres://mgw:SuperSecret123@127.0.0.1:5432/mgw?sslmode=disable"
	hint := diagnosePgError(dsn, errors.New("pq: password authentication failed"))
	if strings.Contains(hint, "SuperSecret123") {
		t.Errorf("hint must not echo password, got %q", hint)
	}
}

func TestParsePgDSN(t *testing.T) {
	user, host, db := parsePgDSN("postgres://mgw:secret@db.example.com:5432/myapp?sslmode=disable")
	if user != "mgw" {
		t.Errorf("user: %q", user)
	}
	if host != "db.example.com:5432" {
		t.Errorf("host: %q", host)
	}
	if db != "myapp" {
		t.Errorf("db: %q", db)
	}
}

func TestParsePgDSN_Malformed(t *testing.T) {
	user, host, db := parsePgDSN("not a url")
	if user != "" || host != "" || db != "" {
		t.Errorf("malformed must return empty: %q %q %q", user, host, db)
	}
}
