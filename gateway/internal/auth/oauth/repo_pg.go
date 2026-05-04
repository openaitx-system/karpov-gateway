package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiChongs/QQMusicApi/gateway/internal/store/crypto"
)

// PgIdentityRepo 实现 IdentityRepo, 落库到 auth.oauth_identities.
//
// 加密策略:
//   - access_token / refresh_token 用 AES-256-GCM 加密入库;
//     KEK 由 caller 传入 (复用 pool 的 32 字节 KEK).
//   - AAD = "oauth:"+provider+":"+sub, 防止把 identity A 的密文塞给 B 解密.
//   - KEK 为 nil 时退化为明文存储 (仅 dev / 测试时允许; 生产装配时必须传).
type PgIdentityRepo struct {
	pool *pgxpool.Pool
	kek  []byte // 32-byte; nil = 明文模式 (仅 dev)
}

// NewPgIdentityRepo 构造 PG 仓库; kek 长度必须为 32 字节或 nil.
func NewPgIdentityRepo(pool *pgxpool.Pool, kek []byte) (*PgIdentityRepo, error) {
	if pool == nil {
		return nil, errors.New("oauth.PgIdentityRepo: nil pool")
	}
	if kek != nil && len(kek) != crypto.KeySize {
		return nil, fmt.Errorf("oauth.PgIdentityRepo: kek must be %d bytes, got %d", crypto.KeySize, len(kek))
	}
	return &PgIdentityRepo{pool: pool, kek: kek}, nil
}

const pgIdentitySelectCols = `
	id::text, user_id::text, provider, provider_sub,
	COALESCE(provider_login, ''), COALESCE(provider_email::text, ''),
	COALESCE(provider_name, ''), COALESCE(provider_avatar, ''),
	COALESCE(trust_level, 0),
	scopes,
	COALESCE(access_token_enc, ''::bytea),
	COALESCE(refresh_token_enc, ''::bytea),
	COALESCE(expires_at, '0001-01-01 00:00:00+00'::timestamptz),
	raw_profile,
	created_at, updated_at,
	COALESCE(last_login_at, '0001-01-01 00:00:00+00'::timestamptz)
`

// scanIdentity 解码一行; 自动解密 token (kek 非空时); raw_profile 直接保留 JSON 字节.
func (r *PgIdentityRepo) scanIdentity(row pgx.Row) (*Identity, error) {
	var (
		id        Identity
		atEnc     []byte
		rtEnc     []byte
		rawProf   []byte
		expiresAt time.Time
		lastLogin time.Time
	)
	err := row.Scan(
		&id.ID, &id.UserID, &id.Provider, &id.ProviderSub,
		&id.ProviderLogin, &id.ProviderEmail,
		&id.ProviderName, &id.ProviderAvatar,
		&id.TrustLevel,
		&id.Scopes,
		&atEnc, &rtEnc,
		&expiresAt,
		&rawProf,
		&id.CreatedAt, &id.UpdatedAt,
		&lastLogin,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("oauth.PgIdentityRepo: scan: %w", err)
	}
	if !expiresAt.IsZero() && expiresAt.Year() > 1 {
		id.ExpiresAt = expiresAt
	}
	if !lastLogin.IsZero() && lastLogin.Year() > 1 {
		id.LastLoginAt = lastLogin
	}
	id.RawProfile = rawProf
	if r.kek != nil {
		aad := []byte("oauth:" + id.Provider + ":" + id.ProviderSub)
		if len(atEnc) > 0 {
			pt, derr := crypto.Decrypt(r.kek, atEnc, aad)
			if derr != nil {
				return nil, fmt.Errorf("oauth.PgIdentityRepo: decrypt access_token: %w", derr)
			}
			id.AccessToken = string(pt)
		}
		if len(rtEnc) > 0 {
			pt, derr := crypto.Decrypt(r.kek, rtEnc, aad)
			if derr != nil {
				return nil, fmt.Errorf("oauth.PgIdentityRepo: decrypt refresh_token: %w", derr)
			}
			id.RefreshToken = string(pt)
		}
	} else {
		id.AccessToken = string(atEnc)
		id.RefreshToken = string(rtEnc)
	}
	return &id, nil
}

// GetByProviderSub 实现 IdentityRepo.
func (r *PgIdentityRepo) GetByProviderSub(ctx context.Context, provider, sub string) (*Identity, error) {
	q := `SELECT ` + pgIdentitySelectCols + ` FROM oauth_identities WHERE provider = $1 AND provider_sub = $2 LIMIT 1`
	return r.scanIdentity(r.pool.QueryRow(ctx, q, provider, sub))
}

// GetByUserAndProvider 实现 IdentityRepo.
func (r *PgIdentityRepo) GetByUserAndProvider(ctx context.Context, userID, provider string) (*Identity, error) {
	q := `SELECT ` + pgIdentitySelectCols + ` FROM oauth_identities WHERE user_id = $1::uuid AND provider = $2 LIMIT 1`
	return r.scanIdentity(r.pool.QueryRow(ctx, q, userID, provider))
}

// ListByUser 实现 IdentityRepo.
func (r *PgIdentityRepo) ListByUser(ctx context.Context, userID string) ([]*Identity, error) {
	q := `SELECT ` + pgIdentitySelectCols + ` FROM oauth_identities WHERE user_id = $1::uuid ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("oauth.PgIdentityRepo.ListByUser: %w", err)
	}
	defer rows.Close()
	var out []*Identity
	for rows.Next() {
		id, scanErr := r.scanIdentity(rows)
		if scanErr != nil {
			if errors.Is(scanErr, ErrIdentityNotFound) {
				continue
			}
			return nil, scanErr
		}
		out = append(out, id)
	}
	return out, nil
}

// Upsert 实现 IdentityRepo.
//
// 行为:
//   - provider+sub 命中: 检查 user_id 是否一致, 不一致返回 ErrIdentityAlreadyBound;
//     一致则更新 token / profile / last_login_at, 不动 user_id / created_at.
//   - 不命中: INSERT.
//
// 安全: token 加密前生成新 nonce; raw_profile 大小由 caller 保证 (一般 <4KB).
func (r *PgIdentityRepo) Upsert(ctx context.Context, id *Identity) error {
	if id == nil || id.UserID == "" || id.Provider == "" || id.ProviderSub == "" {
		return errors.New("oauth.PgIdentityRepo.Upsert: missing required fields")
	}
	if id.ID == "" {
		id.ID = uuid.NewString()
	}
	if id.RawProfile == nil {
		id.RawProfile = []byte("{}")
	} else if !json.Valid(id.RawProfile) {
		return errors.New("oauth.PgIdentityRepo.Upsert: raw_profile not valid json")
	}
	if id.Scopes == nil {
		id.Scopes = []string{}
	}

	var atEnc, rtEnc []byte
	if r.kek != nil {
		aad := []byte("oauth:" + id.Provider + ":" + id.ProviderSub)
		if id.AccessToken != "" {
			ct, err := crypto.Encrypt(r.kek, []byte(id.AccessToken), aad)
			if err != nil {
				return fmt.Errorf("oauth.PgIdentityRepo: encrypt access_token: %w", err)
			}
			atEnc = ct
		}
		if id.RefreshToken != "" {
			ct, err := crypto.Encrypt(r.kek, []byte(id.RefreshToken), aad)
			if err != nil {
				return fmt.Errorf("oauth.PgIdentityRepo: encrypt refresh_token: %w", err)
			}
			rtEnc = ct
		}
	} else {
		atEnc = []byte(id.AccessToken)
		rtEnc = []byte(id.RefreshToken)
	}

	var expiresAtArg any
	if !id.ExpiresAt.IsZero() {
		expiresAtArg = id.ExpiresAt
	}
	var lastLoginArg any
	if !id.LastLoginAt.IsZero() {
		lastLoginArg = id.LastLoginAt
	}

	// ON CONFLICT (provider, provider_sub) DO UPDATE; 同时检查 user_id 一致.
	// 如果命中 user_id 不同, EXCLUDED.user_id != oauth_identities.user_id 时
	// 显式 RAISE 一个特定错误码 (用 CASE/WHERE 过滤更新), 这里用更稳的两阶段:
	//   1. 先查现有 row 判断 user_id 一致性
	//   2. 不存在或一致则 upsert
	existing, err := r.GetByProviderSub(ctx, id.Provider, id.ProviderSub)
	if err != nil && !errors.Is(err, ErrIdentityNotFound) {
		return err
	}
	if existing != nil && existing.UserID != id.UserID {
		return ErrIdentityAlreadyBound
	}

	q := `
		INSERT INTO oauth_identities (
			id, user_id, provider, provider_sub, provider_login, provider_email,
			provider_name, provider_avatar, trust_level, scopes,
			access_token_enc, refresh_token_enc, expires_at, raw_profile, last_login_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, NULLIF($5,''), NULLIF($6,'')::citext,
			NULLIF($7,''), NULLIF($8,''), $9, $10,
			NULLIF($11,''::bytea), NULLIF($12,''::bytea), $13, $14::jsonb, $15
		)
		ON CONFLICT (provider, provider_sub) DO UPDATE SET
			provider_login    = EXCLUDED.provider_login,
			provider_email    = EXCLUDED.provider_email,
			provider_name     = EXCLUDED.provider_name,
			provider_avatar   = EXCLUDED.provider_avatar,
			trust_level       = EXCLUDED.trust_level,
			scopes            = EXCLUDED.scopes,
			access_token_enc  = EXCLUDED.access_token_enc,
			refresh_token_enc = EXCLUDED.refresh_token_enc,
			expires_at        = EXCLUDED.expires_at,
			raw_profile       = EXCLUDED.raw_profile,
			last_login_at     = EXCLUDED.last_login_at,
			updated_at        = now()
		RETURNING id::text, created_at, updated_at`
	row := r.pool.QueryRow(ctx, q,
		id.ID, id.UserID, id.Provider, id.ProviderSub,
		id.ProviderLogin, id.ProviderEmail,
		id.ProviderName, id.ProviderAvatar,
		id.TrustLevel, id.Scopes,
		atEnc, rtEnc, expiresAtArg, string(id.RawProfile), lastLoginArg,
	)
	if scanErr := row.Scan(&id.ID, &id.CreatedAt, &id.UpdatedAt); scanErr != nil {
		// user_id+provider 唯一索引冲突 (用户已绑过同 provider 的另一个 sub):
		// PG 23505 unique_violation. 翻成 ErrIdentityAlreadyBound 给 caller.
		var pgErr *pgconn.PgError
		if errors.As(scanErr, &pgErr) && pgErr.Code == "23505" {
			return ErrIdentityAlreadyBound
		}
		return fmt.Errorf("oauth.PgIdentityRepo.Upsert: %w", scanErr)
	}
	return nil
}

// Delete 实现 IdentityRepo.
func (r *PgIdentityRepo) Delete(ctx context.Context, userID, provider string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM oauth_identities WHERE user_id = $1::uuid AND provider = $2`,
		userID, provider)
	if err != nil {
		return fmt.Errorf("oauth.PgIdentityRepo.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIdentityNotFound
	}
	return nil
}
