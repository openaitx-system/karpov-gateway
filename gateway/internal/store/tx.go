package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTx 在事务中执行 fn；错误自动回滚，否则提交。
//
// 使用场景：跨多张表的写操作（创建用户 + email_verification、订单状态机转换 + transactions 入库）。
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	if pool == nil {
		return errors.New("store: pool is nil")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer func() {
		// 即便 Commit 成功，Rollback 也是安全的 no-op；
		// 但若 fn panic，这里能保证回滚。
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
