package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// PgRepo 是 pool.Repo 的 PG 适配器。
// 与 Python 对齐：payload 以明文 JSON 存储（payload_enc 列），不做加密。
type PgRepo struct {
	pool *pgxpool.Pool
}

// NewPgRepo 构造 PgRepo 并清理历史遗留的加密数据（payload 非 JSON 的行）。
func NewPgRepo(pool *pgxpool.Pool) (*PgRepo, error) {
	if pool == nil {
		return nil, errors.New("pool.NewPgRepo: nil pgxpool")
	}
	r := &PgRepo{pool: pool}
	r.cleanupLegacyEncrypted(context.Background())
	return r, nil
}

// cleanupLegacyEncrypted 删除 payload_enc 不是合法 JSON 的行（历史加密遗留）。
func (r *PgRepo) cleanupLegacyEncrypted(ctx context.Context) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, payload_enc FROM pool.credentials`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			continue
		}
		if !json.Valid(payload) {
			slog.Warn("pool: removing legacy encrypted credential (not valid JSON)", "id", id)
			_, _ = r.pool.Exec(ctx, `DELETE FROM pool.credentials WHERE id = $1`, id)
		}
	}
}

// Add 实现 Repo.Add。
func (r *PgRepo) Add(ctx context.Context, c Credential) error {
	if c.ID == "" {
		return errors.New("pool.PgRepo.Add: empty id")
	}
	caps := capsToStrings(c.Capabilities)
	const q = `
		INSERT INTO pool.credentials
			(id, provider, label, payload_enc, capabilities, status,
			 health_score, fail_count, last_used_at, last_failed_at, cooldown_until)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	if _, err := r.pool.Exec(ctx, q,
		c.ID, c.Provider, nullableStr(c.Label), c.Payload, caps, string(c.Status),
		c.HealthScore, c.FailCount,
		nullableTime(c.LastUsedAt), nullableTime(c.LastFailedAt), nullableTime(c.CooldownUntil),
	); err != nil {
		return fmt.Errorf("pool.PgRepo.Add: %w", err)
	}
	return nil
}

// Remove 实现 Repo.Remove。
func (r *PgRepo) Remove(ctx context.Context, id string) error {
	const q = `DELETE FROM pool.credentials WHERE id = $1`
	if _, err := r.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("pool.PgRepo.Remove: %w", err)
	}
	return nil
}

// Get 实现 Repo.Get。
func (r *PgRepo) Get(ctx context.Context, id string) (Credential, error) {
	const q = `
		SELECT id, provider, COALESCE(label,''), payload_enc, capabilities, status,
		       health_score, fail_count,
		       COALESCE(last_used_at, 'epoch'::timestamptz),
		       COALESCE(last_failed_at, 'epoch'::timestamptz),
		       COALESCE(cooldown_until, 'epoch'::timestamptz)
		FROM pool.credentials WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	c, err := scanCredential(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Credential{}, fmt.Errorf("pool.PgRepo.Get: not found %s", id)
		}
		return Credential{}, fmt.Errorf("pool.PgRepo.Get: %w", err)
	}
	return c, nil
}

// Update 实现 Repo.Update。
func (r *PgRepo) Update(ctx context.Context, c Credential) error {
	const q = `
		UPDATE pool.credentials SET
			provider       = $2,
			label          = $3,
			payload_enc    = $4,
			capabilities   = $5,
			status         = $6,
			health_score   = $7,
			fail_count     = $8,
			last_used_at   = $9,
			last_failed_at = $10,
			cooldown_until = $11,
			updated_at     = now()
		WHERE id = $1
	`
	tag, err := r.pool.Exec(ctx, q,
		c.ID, c.Provider, nullableStr(c.Label), c.Payload, capsToStrings(c.Capabilities), string(c.Status),
		c.HealthScore, c.FailCount,
		nullableTime(c.LastUsedAt), nullableTime(c.LastFailedAt), nullableTime(c.CooldownUntil),
	)
	if err != nil {
		return fmt.Errorf("pool.PgRepo.Update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pool.PgRepo.Update: not found %s", c.ID)
	}
	return nil
}

// List 实现 Repo.List。providerName 为空时返回所有 provider 的凭证。
func (r *PgRepo) List(ctx context.Context, providerName string, cap provider.Capability) ([]Credential, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if providerName == "" && cap == provider.CapNone {
		const q = `
			SELECT id, provider, COALESCE(label,''), payload_enc, capabilities, status,
			       health_score, fail_count,
			       COALESCE(last_used_at, 'epoch'::timestamptz),
			       COALESCE(last_failed_at, 'epoch'::timestamptz),
			       COALESCE(cooldown_until, 'epoch'::timestamptz)
			FROM pool.credentials
		`
		rows, err = r.pool.Query(ctx, q)
	} else if cap == provider.CapNone {
		const q = `
			SELECT id, provider, COALESCE(label,''), payload_enc, capabilities, status,
			       health_score, fail_count,
			       COALESCE(last_used_at, 'epoch'::timestamptz),
			       COALESCE(last_failed_at, 'epoch'::timestamptz),
			       COALESCE(cooldown_until, 'epoch'::timestamptz)
			FROM pool.credentials WHERE provider = $1
		`
		rows, err = r.pool.Query(ctx, q, providerName)
	} else {
		const q = `
			SELECT id, provider, COALESCE(label,''), payload_enc, capabilities, status,
			       health_score, fail_count,
			       COALESCE(last_used_at, 'epoch'::timestamptz),
			       COALESCE(last_failed_at, 'epoch'::timestamptz),
			       COALESCE(cooldown_until, 'epoch'::timestamptz)
			FROM pool.credentials WHERE provider = $1
			  AND (capabilities = '{}' OR $2 = ANY(capabilities))
		`
		rows, err = r.pool.Query(ctx, q, providerName, capName(cap))
	}
	if err != nil {
		return nil, fmt.Errorf("pool.PgRepo.List: %w", err)
	}
	defer rows.Close()

	var out []Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("pool.PgRepo.List: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pool.PgRepo.List: rows: %w", err)
	}
	return out, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanCredential(s rowScanner) (Credential, error) {
	var (
		c                              Credential
		caps                           []string
		status                         string
		lastUsed, lastFailed, cooldown time.Time
	)
	if err := s.Scan(
		&c.ID, &c.Provider, &c.Label, &c.Payload, &caps, &status,
		&c.HealthScore, &c.FailCount,
		&lastUsed, &lastFailed, &cooldown,
	); err != nil {
		return Credential{}, err
	}
	c.Status = Status(status)
	c.Capabilities = stringsToCaps(caps)
	if !lastUsed.IsZero() && lastUsed.Unix() > 0 {
		c.LastUsedAt = lastUsed
	}
	if !lastFailed.IsZero() && lastFailed.Unix() > 0 {
		c.LastFailedAt = lastFailed
	}
	if !cooldown.IsZero() && cooldown.Unix() > 0 {
		c.CooldownUntil = cooldown
	}
	return c, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func CapName(c provider.Capability) string { return capName(c) }

func ParseCapName(s string) provider.Capability { return parseCapName(s) }

func capName(c provider.Capability) string {
	switch c {
	case provider.CapGetSong:
		return "GetSong"
	case provider.CapSearchSongs:
		return "SearchSongs"
	case provider.CapGetSongURL:
		return "GetSongURL"
	case provider.CapGetLyric:
		return "GetLyric"
	case provider.CapGetAlbum:
		return "GetAlbum"
	case provider.CapGetAlbumSongs:
		return "GetAlbumSongs"
	case provider.CapGetSinger:
		return "GetSinger"
	case provider.CapGetSingerSongs:
		return "GetSingerSongs"
	case provider.CapGetSongList:
		return "GetSongList"
	case provider.CapGetMV:
		return "GetMV"
	case provider.CapGetMVURL:
		return "GetMVURL"
	case provider.CapGetTopList:
		return "GetTopList"
	case provider.CapGetUser:
		return "GetUser"
	case provider.CapGetRecommend:
		return "GetRecommend"
	case provider.CapGetComments:
		return "GetComments"
	case provider.CapLoginQR:
		return "LoginQR"
	case provider.CapLoginPhone:
		return "LoginPhone"
	default:
		return ""
	}
}

//nolint:gocyclo
func parseCapName(s string) provider.Capability {
	switch strings.TrimSpace(s) {
	case "GetSong":
		return provider.CapGetSong
	case "SearchSongs":
		return provider.CapSearchSongs
	case "GetSongURL":
		return provider.CapGetSongURL
	case "GetLyric":
		return provider.CapGetLyric
	case "GetAlbum":
		return provider.CapGetAlbum
	case "GetAlbumSongs":
		return provider.CapGetAlbumSongs
	case "GetSinger":
		return provider.CapGetSinger
	case "GetSingerSongs":
		return provider.CapGetSingerSongs
	case "GetSongList":
		return provider.CapGetSongList
	case "GetMV":
		return provider.CapGetMV
	case "GetMVURL":
		return provider.CapGetMVURL
	case "GetTopList":
		return provider.CapGetTopList
	case "GetUser":
		return provider.CapGetUser
	case "GetRecommend":
		return provider.CapGetRecommend
	case "GetComments":
		return provider.CapGetComments
	case "LoginQR":
		return provider.CapLoginQR
	case "LoginPhone":
		return provider.CapLoginPhone
	default:
		return provider.CapNone
	}
}

func capsToStrings(caps []provider.Capability) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		if name := capName(c); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func stringsToCaps(ss []string) []provider.Capability {
	out := make([]provider.Capability, 0, len(ss))
	for _, s := range ss {
		if c := parseCapName(s); c != provider.CapNone {
			out = append(out, c)
		}
	}
	return out
}
