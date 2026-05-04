// Package store 提供数据库与缓存基础设施：pgxpool / Redis / 迁移 / 事务。
package store

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGConfig pg 连接池配置。
type PGConfig struct {
	DSN             string        // postgres://user:pass@host:port/db?sslmode=...
	MaxConns        int32         // 默认 = 4 * runtime.NumCPU()
	MinConns        int32         // 默认 2
	MaxConnLifetime time.Duration // 默认 30m
	MaxConnIdleTime time.Duration // 默认 5m
	ConnectTimeout  time.Duration // 默认 5s
}

// NewPGPool 构造 *pgxpool.Pool 并 Ping 校验。
func NewPGPool(ctx context.Context, cfg PGConfig) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, errors.New("store: PG DSN is empty")
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("store: parse pg dsn: %w", err)
	}
	pcfg.MaxConns = nonZeroInt32(cfg.MaxConns, int32(4*runtime.NumCPU()))
	pcfg.MinConns = nonZeroInt32(cfg.MinConns, 2)
	pcfg.MaxConnLifetime = nonZeroDuration(cfg.MaxConnLifetime, 30*time.Minute)
	pcfg.MaxConnIdleTime = nonZeroDuration(cfg.MaxConnIdleTime, 5*time.Minute)

	connectCtx, cancel := context.WithTimeout(ctx,
		nonZeroDuration(cfg.ConnectTimeout, 5*time.Second))
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("store: new pgxpool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping pg: %w", err)
	}
	return pool, nil
}

func nonZeroInt32(v, def int32) int32 {
	if v <= 0 {
		return def
	}
	return v
}

func nonZeroDuration(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
