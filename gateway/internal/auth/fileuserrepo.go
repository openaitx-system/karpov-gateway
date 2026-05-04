package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileUserRepo 是一个把全部用户落盘到单 JSON 文件的 UserRepo 实现。
//
// 设计目标：让 v0.3 仅有 in-memory 仓库的部署也具备**跨进程重启幸存**能力——
// 修复"superadmin 每次重启都被重建"这一致命问题。生产环境仍应换 PG 适配器（M30+）。
//
// 写策略：所有变更走 sync.Mutex 串行 → 整体序列化到 tmp 文件 → 原子 rename。
// 失败 fallback：log warning 但不阻断；下一次写入会再尝试，避免单次磁盘 ENOSPC
// 把整个登录链路打死。
//
// 并发：读用 RWMutex.RLock 只读；写用 Lock 串行 + 落盘。10ms 量级延迟可接受。
//
// 文件格式：
//
//	{
//	  "version": 1,
//	  "next_id": 42,
//	  "users": [
//	    { "id":"1", "email":"admin@example.com", "password_hash":"...", "role":"superadmin", ... }
//	  ]
//	}
//
// **不入 git**：默认路径 ./data/users.json；.gitignore 已排除 data/。
type FileUserRepo struct {
	mu      sync.RWMutex
	path    string
	byEmail map[string]*User
	byID    map[string]*User
	nextID  int64
	// onWriteErr 在持久化失败时被调用；nil 时静默丢弃。
	// 注入路径：构造时由调用方传 logger.Warn。
	onWriteErr func(error)
}

// fileUserSnapshot 是落盘文件的 JSON schema。
//
// 字段名与 internal user 模型解耦——v1 后字段增删都通过 version 字段升级。
type fileUserSnapshot struct {
	Version int             `json:"version"`
	NextID  int64           `json:"next_id"`
	Users   []fileUserEntry `json:"users"`
}

type fileUserEntry struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	Status       string    `json:"status,omitempty"`
	Role         string    `json:"role,omitempty"`
	TOTPEnabled  bool      `json:"totp_enabled,omitempty"`
	TOTPSecret   string    `json:"totp_secret,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	// RegisterIP 自 v0.5.x 引入；老快照（v0.4 及以前）解出来是空串，
	// CountByRegisterIP 不会反查到 ⇒ 自动等价于"未启用 IP 限制时注册的老用户"。
	RegisterIP string `json:"register_ip,omitempty"`
}

// NewFileUserRepo 打开（或新建）位于 path 的用户仓库。
//
// path 为空 → 退化为 in-memory（与 MemUserRepo 等价但仍走本类型，便于注入 onWriteErr）。
// 父目录不存在会被自动创建（perm 0o700，避免被 group/other 读到密码 hash）。
func NewFileUserRepo(path string, onWriteErr func(error)) (*FileUserRepo, error) {
	r := &FileUserRepo{
		path:       path,
		byEmail:    map[string]*User{},
		byID:       map[string]*User{},
		onWriteErr: onWriteErr,
	}
	if path == "" {
		return r, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("auth.FileUserRepo: mkdir %q: %w", dir, err)
		}
	}
	if err := r.loadLocked(); err != nil {
		return nil, err
	}
	return r, nil
}

// loadLocked 从磁盘加载快照。文件不存在视为空仓库（首次启动）。
func (r *FileUserRepo) loadLocked() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("auth.FileUserRepo: read %q: %w", r.path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var snap fileUserSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("auth.FileUserRepo: parse %q: %w", r.path, err)
	}
	r.nextID = snap.NextID
	for i := range snap.Users {
		e := &snap.Users[i]
		u := &User{
			ID:           e.ID,
			Email:        e.Email,
			PasswordHash: e.PasswordHash,
			Status:       e.Status,
			Role:         e.Role,
			TOTPEnabled:  e.TOTPEnabled,
			TOTPSecret:   e.TOTPSecret,
			CreatedAt:    e.CreatedAt,
			RegisterIP:   e.RegisterIP,
		}
		r.byEmail[u.Email] = u
		r.byID[u.ID] = u
	}
	return nil
}

// persistLocked 把当前内存状态原子写入磁盘。
//
// 失败时把错误传给 onWriteErr 但不返回 — 避免登录/注册被磁盘故障击穿。
// 调用方持有 r.mu 写锁；本函数不再加锁。
func (r *FileUserRepo) persistLocked() {
	if r.path == "" {
		return
	}
	snap := fileUserSnapshot{
		Version: 1,
		NextID:  r.nextID,
		Users:   make([]fileUserEntry, 0, len(r.byID)),
	}
	for _, u := range r.byID {
		snap.Users = append(snap.Users, fileUserEntry{
			ID:           u.ID,
			Email:        u.Email,
			PasswordHash: u.PasswordHash,
			Status:       u.Status,
			Role:         u.Role,
			TOTPEnabled:  u.TOTPEnabled,
			TOTPSecret:   u.TOTPSecret,
			CreatedAt:    u.CreatedAt,
			RegisterIP:   u.RegisterIP,
		})
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		r.reportWriteErr(fmt.Errorf("marshal: %w", err))
		return
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		r.reportWriteErr(fmt.Errorf("write tmp: %w", err))
		return
	}
	if err := os.Rename(tmp, r.path); err != nil {
		r.reportWriteErr(fmt.Errorf("rename: %w", err))
		return
	}
}

func (r *FileUserRepo) reportWriteErr(err error) {
	if r.onWriteErr != nil {
		r.onWriteErr(err)
	}
}

// GetByEmail 实现 UserRepo.GetByEmail；不存在返回 (nil, ErrUserNotFound)。
func (r *FileUserRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byEmail[email]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

// GetByID 实现 UserRepo.GetByID。
func (r *FileUserRepo) GetByID(_ context.Context, id string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.byID[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

// Create 实现 UserRepo.Create；自动赋递增 ID，写盘后返回。
func (r *FileUserRepo) Create(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byEmail[u.Email]; dup {
		return ErrUserExists
	}
	r.nextID++
	if u.ID == "" {
		u.ID = formatID(r.nextID)
	}
	cp := *u
	r.byEmail[u.Email] = &cp
	r.byID[u.ID] = &cp
	r.persistLocked()
	return nil
}

// Update 实现 UserRepo.Update。
func (r *FileUserRepo) Update(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[u.ID]; !ok {
		return ErrUserNotFound
	}
	cp := *u
	r.byID[u.ID] = &cp
	r.byEmail[u.Email] = &cp
	r.persistLocked()
	return nil
}

// Path 返回当前持久化文件路径（"" 表示纯内存）。便于运维诊断 / 单测。
func (r *FileUserRepo) Path() string { return r.path }

// CountByRegisterIP 实现 UserRepo.CountByRegisterIP。
//
// 文件仓库走线性扫描；规模小（运维管理用），不需要二级索引。
func (r *FileUserRepo) CountByRegisterIP(_ context.Context, ip string) (int, error) {
	if ip == "" {
		return 0, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, u := range r.byID {
		if u.RegisterIP == ip {
			n++
		}
	}
	return n, nil
}

// ListUsers 实现 UserLister。
func (r *FileUserRepo) ListUsers(_ context.Context, f ListUserFilter) ([]*User, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	matches := make([]*User, 0)
	for _, u := range r.byID {
		if f.EmailLike != "" && !containsFold(u.Email, f.EmailLike) {
			continue
		}
		if f.Role != "" && u.EffectiveRole() != f.Role {
			continue
		}
		if f.Status != "" && u.Status != f.Status {
			continue
		}
		matches = append(matches, u)
	}
	total := len(matches)
	sortUsersByCreatedDesc(matches)
	if offset >= len(matches) {
		return []*User{}, total, nil
	}
	end := offset + limit
	if end > len(matches) {
		end = len(matches)
	}
	out := make([]*User, 0, end-offset)
	for _, u := range matches[offset:end] {
		cp := *u
		out = append(out, &cp)
	}
	return out, total, nil
}
