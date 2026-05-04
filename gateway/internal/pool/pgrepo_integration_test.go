//go:build integration
// +build integration

package pool

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store"
	"github.com/MiChongs/QQMusicApi/gateway/migrations"
)

func startPostgres(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgC, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("musicgw_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	if err := store.MigrateUp(ctx, migrations.FS, dsn, "pool"); err != nil {
		t.Fatalf("migrate pool: %v", err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	}
	return pool, cleanup
}

func TestPgRepo_AddGetUpdateRemove(t *testing.T) {
	pool, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	repo, err := NewPgRepo(pool)
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	ctx := context.Background()

	in := Credential{
		ID: "c1", Provider: "qqmusic", Label: "qq-acct-1",
		Payload:      []byte(`{"musickey":"W_X_TEST","musicid":12345}`),
		Capabilities: []provider.Capability{provider.CapGetSong, provider.CapSearchSongs},
		Status:       StatusActive,
		HealthScore:  0.95,
	}
	if err := repo.Add(ctx, in); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := repo.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Payload) != string(in.Payload) {
		t.Errorf("payload round-trip: %q vs %q", got.Payload, in.Payload)
	}
	if got.HealthScore != in.HealthScore {
		t.Errorf("health: %v", got.HealthScore)
	}
	if len(got.Capabilities) != 2 {
		t.Errorf("caps: %+v", got.Capabilities)
	}

	got.HealthScore = 0.5
	got.Status = StatusBanned
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := repo.Get(ctx, "c1")
	if got2.HealthScore != 0.5 || got2.Status != StatusBanned {
		t.Errorf("update mismatch: %+v", got2)
	}

	if err := repo.Remove(ctx, "c1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := repo.Get(ctx, "c1"); err == nil {
		t.Errorf("expected not-found after remove")
	}
}

func TestPgRepo_List_CapabilityFilter(t *testing.T) {
	pool, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	repo, _ := NewPgRepo(pool)
	ctx := context.Background()

	for _, c := range []Credential{
		{ID: "c1", Provider: "qqmusic", Payload: []byte("a"),
			Capabilities: []provider.Capability{provider.CapGetSong},
			Status:       StatusActive, HealthScore: 1.0},
		{ID: "c2", Provider: "qqmusic", Payload: []byte("b"),
			Capabilities: []provider.Capability{provider.CapSearchSongs},
			Status:       StatusActive, HealthScore: 1.0},
		{ID: "c3", Provider: "qqmusic", Payload: []byte("c"),
			Capabilities: []provider.Capability{provider.CapGetSong, provider.CapSearchSongs},
			Status:       StatusActive, HealthScore: 1.0},
	} {
		if err := repo.Add(ctx, c); err != nil {
			t.Fatalf("add %s: %v", c.ID, err)
		}
	}

	got, err := repo.List(ctx, "qqmusic", provider.CapGetSong)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("GetSong filter: got %d, want 2", len(got))
	}

	all, _ := repo.List(ctx, "qqmusic", provider.CapNone)
	if len(all) != 3 {
		t.Errorf("CapNone filter: got %d, want 3", len(all))
	}

	none, _ := repo.List(ctx, "spotify", provider.CapNone)
	if len(none) != 0 {
		t.Errorf("unknown provider: got %d, want 0", len(none))
	}
}

func TestPgRepo_AAD_RejectsTamperedID(t *testing.T) {
	pool, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	repo, _ := NewPgRepo(pool)
	ctx := context.Background()

	_ = repo.Add(ctx, Credential{
		ID: "c1", Provider: "qqmusic", Payload: []byte("secret"),
		Status: StatusActive, HealthScore: 1.0,
	})
	var enc []byte
	if err := pool.QueryRow(ctx, `SELECT payload_enc FROM pool.credentials WHERE id='c1'`).Scan(&enc); err != nil {
		t.Fatalf("read enc: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pool.credentials(id, provider, payload_enc, status, health_score)
         VALUES ('c2', 'qqmusic', $1, 'active', 1.0)`, enc); err != nil {
		t.Fatalf("insert tampered: %v", err)
	}
	if _, err := repo.Get(ctx, "c2"); err == nil {
		t.Errorf("expected decrypt error for AAD-mismatched copy")
	}
}
