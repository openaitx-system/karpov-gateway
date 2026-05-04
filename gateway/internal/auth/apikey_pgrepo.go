package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgAPIKeyRepo 是 APIKeyRepo 的 PG 实现，落库到 auth.api_keys。
type PgAPIKeyRepo struct {
	pool *pgxpool.Pool
}

// NewPgAPIKeyRepo 构造 PG API Key 仓库。
func NewPgAPIKeyRepo(pool *pgxpool.Pool) *PgAPIKeyRepo {
	return &PgAPIKeyRepo{pool: pool}
}

func (r *PgAPIKeyRepo) Create(ctx context.Context, k *APIKeyRecord) error {
	const q = `
		INSERT INTO api_keys (id, user_id, key_hash, prefix, name, description, plan_id, scopes, ip_allowlist,
		                       rate_limit_rpm, rate_limit_daily, enabled, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`
	var expiresAt any
	if !k.ExpiresAt.IsZero() {
		expiresAt = k.ExpiresAt
	}
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	planID := k.PlanID
	if planID == "" {
		planID = "free"
	}
	if _, err := r.pool.Exec(ctx, q,
		k.ID, k.UserID, k.Hash, k.Prefix, k.Name, k.Description, planID,
		scopes, cidrArray(k.IPAllow),
		k.RateLimitRPM, k.RateLimitDaily, k.Enabled,
		expiresAt, k.CreatedAt,
	); err != nil {
		return fmt.Errorf("auth.PgAPIKeyRepo.Create: %w", err)
	}
	return nil
}

func (r *PgAPIKeyRepo) GetByID(ctx context.Context, id string) (*APIKeyRecord, error) {
	const q = `
		SELECT id, user_id, key_hash, prefix, COALESCE(name,''), COALESCE(description,''),
		       COALESCE(plan_id,'free'), scopes, COALESCE(ip_allowlist::text[], '{}'),
		       rate_limit_rpm, rate_limit_daily, enabled, total_requests,
		       created_at, COALESCE(expires_at, 'epoch'::timestamptz),
		       COALESCE(last_used_at, 'epoch'::timestamptz),
		       COALESCE(revoked_at, 'epoch'::timestamptz)
		FROM api_keys WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	return scanSingleAPIKey(row)
}

func (r *PgAPIKeyRepo) Update(ctx context.Context, k *APIKeyRecord) error {
	const q = `
		UPDATE api_keys
		SET name = $2, description = $3, plan_id = $4, scopes = $5, ip_allowlist = $6,
		    rate_limit_rpm = $7, rate_limit_daily = $8, expires_at = $9
		WHERE id = $1 AND user_id = $10 AND revoked_at IS NULL
	`
	var expiresAt any
	if !k.ExpiresAt.IsZero() {
		expiresAt = k.ExpiresAt
	}
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	planID := k.PlanID
	if planID == "" {
		planID = "free"
	}
	tag, err := r.pool.Exec(ctx, q,
		k.ID, k.Name, k.Description, planID, scopes, cidrArray(k.IPAllow),
		k.RateLimitRPM, k.RateLimitDaily, expiresAt, k.UserID,
	)
	if err != nil {
		return fmt.Errorf("auth.PgAPIKeyRepo.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

func (r *PgAPIKeyRepo) SetEnabled(ctx context.Context, id, userID string, enabled bool) error {
	const q = `UPDATE api_keys SET enabled = $3 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, id, userID, enabled)
	if err != nil {
		return fmt.Errorf("auth.PgAPIKeyRepo.SetEnabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

func (r *PgAPIKeyRepo) IncrTotalRequests(ctx context.Context, id string, delta int64) error {
	const q = `UPDATE api_keys SET total_requests = total_requests + $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id, delta)
	return err
}

func (r *PgAPIKeyRepo) GetByPrefix(ctx context.Context, prefix string) ([]*APIKeyRecord, error) {
	const q = `
		SELECT id, user_id, key_hash, prefix, COALESCE(name,''), COALESCE(description,''),
		       COALESCE(plan_id,'free'), scopes, COALESCE(ip_allowlist::text[], '{}'),
		       rate_limit_rpm, rate_limit_daily, enabled, total_requests,
		       created_at, COALESCE(expires_at, 'epoch'::timestamptz),
		       COALESCE(last_used_at, 'epoch'::timestamptz),
		       COALESCE(revoked_at, 'epoch'::timestamptz)
		FROM api_keys WHERE prefix = $1 AND revoked_at IS NULL
	`
	rows, err := r.pool.Query(ctx, q, prefix)
	if err != nil {
		return nil, fmt.Errorf("auth.PgAPIKeyRepo.GetByPrefix: %w", err)
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

func (r *PgAPIKeyRepo) ListByUser(ctx context.Context, userID string) ([]*APIKeyRecord, error) {
	const q = `
		SELECT id, user_id, key_hash, prefix, COALESCE(name,''), COALESCE(description,''),
		       COALESCE(plan_id,'free'), scopes, COALESCE(ip_allowlist::text[], '{}'),
		       rate_limit_rpm, rate_limit_daily, enabled, total_requests,
		       created_at, COALESCE(expires_at, 'epoch'::timestamptz),
		       COALESCE(last_used_at, 'epoch'::timestamptz),
		       COALESCE(revoked_at, 'epoch'::timestamptz)
		FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("auth.PgAPIKeyRepo.ListByUser: %w", err)
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

func (r *PgAPIKeyRepo) Revoke(ctx context.Context, id, userID string, at time.Time) error {
	const q = `UPDATE api_keys SET revoked_at = $3 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, id, userID, at)
	if err != nil {
		return fmt.Errorf("auth.PgAPIKeyRepo.Revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

func (r *PgAPIKeyRepo) Touch(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE api_keys SET last_used_at = $2, total_requests = total_requests + 1 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id, at)
	return err
}

func scanAPIKeys(rows pgx.Rows) ([]*APIKeyRecord, error) {
	var out []*APIKeyRecord
	for rows.Next() {
		var (
			k                          APIKeyRecord
			expiresAt, lastUsed, revAt time.Time
			scopes, ipAllow            []string
		)
		if err := rows.Scan(
			&k.ID, &k.UserID, &k.Hash, &k.Prefix, &k.Name, &k.Description,
			&k.PlanID, &scopes, &ipAllow,
			&k.RateLimitRPM, &k.RateLimitDaily, &k.Enabled, &k.TotalRequests,
			&k.CreatedAt, &expiresAt, &lastUsed, &revAt,
		); err != nil {
			return nil, err
		}
		k.Scopes = scopes
		k.IPAllow = ipAllow
		if expiresAt.Unix() > 0 {
			k.ExpiresAt = expiresAt
		}
		if lastUsed.Unix() > 0 {
			k.LastUsedAt = lastUsed
		}
		if revAt.Unix() > 0 {
			k.RevokedAt = revAt
		}
		out = append(out, &k)
	}
	return out, rows.Err()
}

func scanSingleAPIKey(row pgx.Row) (*APIKeyRecord, error) {
	var (
		k                          APIKeyRecord
		expiresAt, lastUsed, revAt time.Time
		scopes, ipAllow            []string
	)
	if err := row.Scan(
		&k.ID, &k.UserID, &k.Hash, &k.Prefix, &k.Name, &k.Description,
		&k.PlanID, &scopes, &ipAllow,
		&k.RateLimitRPM, &k.RateLimitDaily, &k.Enabled, &k.TotalRequests,
		&k.CreatedAt, &expiresAt, &lastUsed, &revAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, err
	}
	k.Scopes = scopes
	k.IPAllow = ipAllow
	if expiresAt.Unix() > 0 {
		k.ExpiresAt = expiresAt
	}
	if lastUsed.Unix() > 0 {
		k.LastUsedAt = lastUsed
	}
	if revAt.Unix() > 0 {
		k.RevokedAt = revAt
	}
	return &k, nil
}

func cidrArray(ss []string) any {
	if len(ss) == 0 {
		return []string{}
	}
	return ss
}
