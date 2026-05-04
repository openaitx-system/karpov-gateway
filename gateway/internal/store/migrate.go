package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"github.com/pressly/goose/v3"
)

// MigrateUp 对指定 schema 应用所有 up 迁移。
//
// fsys 含 schema 子目录（auth/quota/billing/pool），典型来自 migrations.FS。
// schema 用于定位子目录、设置 search_path、创建 goose_db_version 表。
// 每个 schema 使用独立的 goose Provider 实例，支持多 schema 并发迁移。
func MigrateUp(ctx context.Context, fsys fs.FS, dsn, schema string) error {
	if err := EnsureSchema(ctx, dsn, schema); err != nil {
		return fmt.Errorf("store: ensure schema [%s]: %w", schema, err)
	}
	provider, db, err := newGooseProvider(fsys, dsn, schema)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: migrate up [%s]: %w", schema, err)
	}
	return nil
}

// MigrateDown 回滚 N 步。
func MigrateDown(ctx context.Context, fsys fs.FS, dsn, schema string, steps int) error {
	if steps <= 0 {
		return errors.New("store: down steps must be > 0")
	}
	provider, db, err := newGooseProvider(fsys, dsn, schema)
	if err != nil {
		return err
	}
	defer db.Close()
	for i := 0; i < steps; i++ {
		if _, err := provider.Down(ctx); err != nil {
			return fmt.Errorf("store: migrate down [%s] step %d: %w", schema, i+1, err)
		}
	}
	return nil
}

func newGooseProvider(fsys fs.FS, dsn, schema string) (*goose.Provider, *sql.DB, error) {
	if fsys == nil {
		return nil, nil, errors.New("store: migrations fs is nil")
	}
	if schema == "" {
		return nil, nil, errors.New("store: schema is empty")
	}
	sub, err := fs.Sub(fsys, schema)
	if err != nil {
		return nil, nil, fmt.Errorf("store: locate migrations [%s]: %w", schema, err)
	}
	dsnWithSchema, err := AppendSearchPath(dsn, schema)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("pgx", dsnWithSchema)
	if err != nil {
		return nil, nil, fmt.Errorf("store: open [%s]: %w", schema, err)
	}
	db.SetMaxOpenConns(1)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("store: goose provider [%s]: %w", schema, err)
	}
	return provider, db, nil
}

var schemaIdentRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)

// EnsureSchema 在迁移前创建 schema（幂等）。
func EnsureSchema(ctx context.Context, dsn, schema string) error {
	if !schemaIdentRE.MatchString(schema) {
		return fmt.Errorf("invalid schema identifier %q", schema)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %q`, schema)); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

// AppendSearchPath 把 search_path=<schema> 注入 PG DSN 的查询串。
func AppendSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("store: parse dsn: %w", err)
	}
	q := u.Query()
	if existing := q.Get("search_path"); existing != "" {
		if !strings.Contains(existing, schema) {
			q.Set("search_path", schema+","+existing)
		}
	} else {
		q.Set("search_path", schema)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// AppendTimeZone 把 timezone=<tz> 注入 PG DSN 的查询串。
//
// pgx v5 在解析 URL DSN 时会把未识别参数收进 RuntimeParams，PostgreSQL 会
// 在 StartupMessage 阶段读取 timezone 并设到 SESSION 级别——等价于连接后立刻
// 执行 `SET TIME ZONE '<tz>'`。
//
// 语义：
//   - tz="" 视为"不改"，原样返回 dsn（包括它已有的 timezone 参数）。
//   - tz 非空：覆盖（不是合并）已有的 timezone 参数，避免歧义。
//   - dsn 不可解析：返回错误，调用方决定回退策略。
//
// 注意：业务 SQL（quota.go 的窗口边界、迁移里的 default now() 等）仍应使用
// `now() AT TIME ZONE 'UTC'` 或 Go 侧 `.UTC()` 后再下传，session 时区只影响
// 显示（psql 直接 SELECT 出来的人类可读时间）和不带显式 zone 的 `now()` 取值。
func AppendTimeZone(dsn, tz string) (string, error) {
	if tz == "" {
		return dsn, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("store: parse dsn: %w", err)
	}
	q := u.Query()
	q.Set("timezone", tz)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
