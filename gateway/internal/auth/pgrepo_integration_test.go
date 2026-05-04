//go:build integration
// +build integration

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MiChongs/QQMusicApi/gateway/internal/store"
	"github.com/MiChongs/QQMusicApi/gateway/migrations"
)

func startPostgres(t *testing.T) (*pgxpool.Pool, string, func()) {
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
		t.Fatalf("start postgres: %v", err)
	}
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	if err := store.MigrateUp(ctx, migrations.FS, dsn, "auth"); err != nil {
		t.Fatalf("migrate auth: %v", err)
	}
	poolDSN, _ := store.AppendSearchPath(dsn, "auth")
	cfg, _ := pgxpool.ParseConfig(poolDSN)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cleanup := func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	}
	return pool, dsn, cleanup
}

func TestPgUserRepo_CreateGetUpdate(t *testing.T) {
	pool, _, cleanup := startPostgres(t)
	t.Cleanup(cleanup)
	repo := NewPgUserRepo(pool)
	ctx := context.Background()

	u := &User{
		Email:        "admin@example.com",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$xxxxx",
		Status:       "active",
		Role:         RoleSuperAdmin,
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if u.ID == "" {
		t.Fatal("expected RETURNING id")
	}

	got, err := repo.GetByEmail(ctx, "admin@example.com")
	if err != nil || got == nil {
		t.Fatalf("getByEmail: %v", err)
	}
	if got.ID != u.ID || got.Role != RoleSuperAdmin {
		t.Errorf("roundtrip: %+v", got)
	}

	got.Role = RoleAdmin
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got2, _ := repo.GetByID(ctx, got.ID)
	if got2.Role != RoleAdmin {
		t.Errorf("update did not persist: %+v", got2)
	}
}

func TestPgUserRepo_DuplicateEmailRejected(t *testing.T) {
	pool, _, cleanup := startPostgres(t)
	t.Cleanup(cleanup)
	repo := NewPgUserRepo(pool)
	ctx := context.Background()
	_ = repo.Create(ctx, &User{Email: "dup@a.com", PasswordHash: "h"})
	err := repo.Create(ctx, &User{Email: "dup@a.com", PasswordHash: "h2"})
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("dup must return ErrUserExists, got %v", err)
	}
}

func TestPgUserRepo_SurvivesPoolReopen(t *testing.T) {
	pool, dsn, cleanup := startPostgres(t)
	t.Cleanup(cleanup)
	r1 := NewPgUserRepo(pool)
	ctx := context.Background()
	if err := r1.Create(ctx, &User{
		Email:        "admin@example.com",
		PasswordHash: "h",
		Status:       "active",
		Role:         RoleSuperAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	poolDSN, _ := store.AppendSearchPath(dsn, "auth")
	cfg, _ := pgxpool.ParseConfig(poolDSN)
	pool2, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool2.Close()
	r2 := NewPgUserRepo(pool2)
	got, err := r2.GetByEmail(ctx, "admin@example.com")
	if err != nil || got == nil {
		t.Fatalf("superadmin missing across pool reopen: %v", err)
	}
	if got.Role != RoleSuperAdmin {
		t.Errorf("role drift: %s", got.Role)
	}
}

func TestPgUserRepo_BootstrapIdempotent(t *testing.T) {
	pool, _, cleanup := startPostgres(t)
	t.Cleanup(cleanup)
	repo := NewPgUserRepo(pool)
	ctx := context.Background()

	const email = "admin@example.com"

	if u, err := repo.GetByEmail(ctx, email); !errors.Is(err, ErrUserNotFound) || u != nil {
		t.Fatalf("first launch must NOT have superadmin: %v / %+v", err, u)
	}
	if err := repo.Create(ctx, &User{
		Email:        email,
		PasswordHash: "argon2id-fake",
		Role:         RoleSuperAdmin,
		Status:       "active",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := repo.GetByEmail(ctx, email)
		if err != nil || got == nil {
			t.Fatalf("restart #%d: superadmin missing: %v", i, err)
		}
		if got.Role != RoleSuperAdmin {
			t.Errorf("restart #%d: role drift: %s", i, got.Role)
		}
	}
}
