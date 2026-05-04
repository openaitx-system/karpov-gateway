package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgUserRepo 是基于 pgx/pgxpool 的用户仓库实现，落库到 auth.users。
//
// 字段映射（与 migrations/auth/0001_init.up.sql + 0002_role.up.sql 对齐）：
//
//	users.id                 ↔ User.ID                (UUID 字符串)
//	users.email              ↔ User.Email             (CITEXT)
//	users.password_hash      ↔ User.PasswordHash      (argon2id)
//	users.status             ↔ User.Status            (active/locked/disabled/pending_email)
//	users.role               ↔ User.Role              (user/admin/superadmin)
//	users.totp_enabled       ↔ User.TOTPEnabled
//	users.totp_secret_enc    ↔ User.TOTPSecret        (M16 后改 AES-256-GCM)
//	users.created_at         ↔ User.CreatedAt
//
// 注意：
//   - id 由 PG `gen_random_uuid()` DEFAULT 自动生成；Create 用 RETURNING id 回填。
//   - email 唯一索引在 PG 端，重复返回 23505 → 翻成 ErrUserExists。
//   - search_path 由 caller 注入（DSN 末尾带 `search_path=auth`，与 migrate 一致）。
type PgUserRepo struct {
	pool *pgxpool.Pool
}

// NewPgUserRepo 构造仓库。pool 由 caller 维护生命周期。
func NewPgUserRepo(pool *pgxpool.Pool) *PgUserRepo {
	return &PgUserRepo{pool: pool}
}

const pgUserSelectCols = `
	id::text, email, password_hash, status,
	COALESCE(role, 'user') AS role,
	COALESCE(plan_id, 'free') AS plan_id,
	COALESCE(totp_enabled, false) AS totp_enabled,
	COALESCE(totp_secret_enc, ''::bytea) AS totp_secret_enc,
	created_at,
	COALESCE(host(register_ip), '') AS register_ip
`

// GetByEmail 实现 UserRepo.GetByEmail。
func (r *PgUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	q := `SELECT ` + pgUserSelectCols + ` FROM users WHERE email = $1 LIMIT 1`
	row := r.pool.QueryRow(ctx, q, email)
	return scanPgUser(row)
}

// GetByID 实现 UserRepo.GetByID。
func (r *PgUserRepo) GetByID(ctx context.Context, id string) (*User, error) {
	q := `SELECT ` + pgUserSelectCols + ` FROM users WHERE id = $1::uuid LIMIT 1`
	row := r.pool.QueryRow(ctx, q, id)
	return scanPgUser(row)
}

// Create 实现 UserRepo.Create。
//
// 行为：
//   - id 缺省由 PG 生成；外部传 ID 时显式 INSERT（用于迁移导入）。
//   - status 缺省 "active"（bootstrap 路径），与 0001 schema 默认值不同（pending_email）；
//     外部 Register 路径仍可显式传 "pending_email"。
func (r *PgUserRepo) Create(ctx context.Context, u *User) error {
	if u == nil {
		return errors.New("auth.PgUserRepo: nil user")
	}
	status := u.Status
	if status == "" {
		status = "active"
	}
	role := u.Role
	if role == "" {
		role = RoleUser
	}
	planID := u.PlanID
	if planID == "" {
		planID = "free"
	}
	createdAt := u.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	// register_ip：空字符串 ⇒ 写 NULL；非空写为 INET。
	// 用 NULL 而不是空串能让"白名单不入库"和"老用户回填前"共用同一表达，
	// 同时让部分索引 WHERE register_ip IS NOT NULL 起作用。
	var registerIP any
	if u.RegisterIP != "" {
		registerIP = u.RegisterIP
	}

	var (
		row pgx.Row
		q   string
	)
	if u.ID == "" {
		q = `INSERT INTO users (email, password_hash, status, role, plan_id, totp_enabled, totp_secret_enc, created_at, register_ip)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::inet)
			 RETURNING id::text`
		row = r.pool.QueryRow(ctx, q,
			u.Email, u.PasswordHash, status, role, planID, u.TOTPEnabled, []byte(u.TOTPSecret), createdAt, registerIP)
	} else {
		q = `INSERT INTO users (id, email, password_hash, status, role, plan_id, totp_enabled, totp_secret_enc, created_at, register_ip)
			 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10::inet)
			 RETURNING id::text`
		row = r.pool.QueryRow(ctx, q,
			u.ID, u.Email, u.PasswordHash, status, role, planID, u.TOTPEnabled, []byte(u.TOTPSecret), createdAt, registerIP)
	}

	var newID string
	if err := row.Scan(&newID); err != nil {
		return mapPgUniqueViolation(err)
	}
	u.ID = newID
	u.Status = status
	u.Role = role
	u.PlanID = planID
	u.CreatedAt = createdAt
	return nil
}

// Update 实现 UserRepo.Update。
func (r *PgUserRepo) Update(ctx context.Context, u *User) error {
	if u == nil || u.ID == "" {
		return ErrUserNotFound
	}
	planID := u.PlanID
	if planID == "" {
		planID = "free"
	}
	q := `UPDATE users
		  SET email = $1, password_hash = $2, status = $3, role = $4, plan_id = $5,
		      totp_enabled = $6, totp_secret_enc = $7, updated_at = now()
		  WHERE id = $8::uuid`
	tag, err := r.pool.Exec(ctx, q,
		u.Email, u.PasswordHash, u.Status, u.Role, planID,
		u.TOTPEnabled, []byte(u.TOTPSecret), u.ID)
	if err != nil {
		return mapPgUniqueViolation(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ListUsers 实现 UserLister；按 created_at 倒序分页。
//
// 模糊匹配 email 用 ILIKE '%xxx%'；空字符串视为无过滤。
// 返回 (users, total, err)；total 是过滤后的总数（不分页）。
func (r *PgUserRepo) ListUsers(ctx context.Context, f ListUserFilter) ([]*User, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	idx := 1
	where := "WHERE TRUE"
	if f.EmailLike != "" {
		where += fmt.Sprintf(" AND email ILIKE $%d", idx)
		args = append(args, "%"+f.EmailLike+"%")
		idx++
	}
	if f.Role != "" {
		where += fmt.Sprintf(" AND COALESCE(role,'user') = $%d", idx)
		args = append(args, f.Role)
		idx++
	}
	if f.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, f.Status)
		idx++
	}

	countQ := `SELECT COUNT(*) FROM users ` + where
	var total int
	if err := r.pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("auth.PgUserRepo.ListUsers count: %w", err)
	}

	args = append(args, limit, offset)
	listQ := `SELECT ` + pgUserSelectCols + ` FROM users ` + where +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	rows, err := r.pool.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("auth.PgUserRepo.ListUsers query: %w", err)
	}
	defer rows.Close()

	var out []*User
	for rows.Next() {
		u, scanErr := scanPgUser(rows)
		if scanErr != nil {
			continue
		}
		out = append(out, u)
	}
	return out, total, nil
}

// scanPgUser 把 pgx.Row 解码为 *User；no rows 返 ErrUserNotFound。
func scanPgUser(row pgx.Row) (*User, error) {
	var (
		u            User
		totpSecret   []byte
		passwordHash string
		registerIP   string
	)
	err := row.Scan(
		&u.ID,
		&u.Email,
		&passwordHash,
		&u.Status,
		&u.Role,
		&u.PlanID,
		&u.TOTPEnabled,
		&totpSecret,
		&u.CreatedAt,
		&registerIP,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth.PgUserRepo: scan: %w", err)
	}
	u.PasswordHash = passwordHash
	u.TOTPSecret = string(totpSecret)
	u.RegisterIP = registerIP
	return &u, nil
}

// CountByRegisterIP 实现 UserRepo.CountByRegisterIP。
//
// 用 register_ip = $1::inet 走部分索引；空 IP 直接返回 0（避免无谓查询）。
func (r *PgUserRepo) CountByRegisterIP(ctx context.Context, ip string) (int, error) {
	if ip == "" {
		return 0, nil
	}
	q := `SELECT COUNT(*) FROM users WHERE register_ip = $1::inet`
	var n int
	if err := r.pool.QueryRow(ctx, q, ip).Scan(&n); err != nil {
		return 0, fmt.Errorf("auth.PgUserRepo.CountByRegisterIP: %w", err)
	}
	return n, nil
}

// mapPgUniqueViolation 把 pg unique_violation (SQLSTATE 23505) 翻成 ErrUserExists。
func mapPgUniqueViolation(err error) error {
	if err == nil {
		return nil
	}
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
		return ErrUserExists
	}
	return err
}
