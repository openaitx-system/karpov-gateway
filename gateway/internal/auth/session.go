package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Session 是服务端登录态。
//
// 持有的字段尽量精简：仅 user_id + meta；任何业务实体（角色 / 权限 / quota）都
// 应在每次请求时从 PG/Redis 实时读取，避免 Session 大膨胀。
type Session struct {
	SID       string // 服务端会话 ID（256-bit, base64url 无填充）
	UserID    string
	IP        string
	UserAgent string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// SessionStore 是 Session 存取抽象。
type SessionStore interface {
	Save(ctx context.Context, s *Session) error
	Get(ctx context.Context, sid string) (*Session, error)
	Delete(ctx context.Context, sid string) error
}

// RedisSessionStore 把 Session 序列化为 JSON 存到 Redis（按 SID 做 key，TTL = ExpiresAt - now）。
//
// 与 gorilla/sessions 的差异：直接存最小字段而非 cookie-encoded blob，
// 减少序列化开销并允许后端直接 Get session.UserID 做审计。
type RedisSessionStore struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewRedisSessionStore 构造 RedisSessionStore。
func NewRedisSessionStore(rdb *redis.Client, keyPrefix string) *RedisSessionStore {
	if keyPrefix == "" {
		keyPrefix = "auth:sess"
	}
	return &RedisSessionStore{rdb: rdb, keyPrefix: keyPrefix}
}

// key 派生 Redis 键。
func (s *RedisSessionStore) key(sid string) string {
	return s.keyPrefix + ":" + sid
}

// Save 写入 Session（按 ExpiresAt 自动 EXPIRE）。
func (s *RedisSessionStore) Save(ctx context.Context, sess *Session) error {
	if sess.SID == "" {
		return errors.New("auth.Session: empty SID")
	}
	if sess.ExpiresAt.IsZero() {
		return errors.New("auth.Session: zero ExpiresAt")
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	ttl := time.Until(sess.ExpiresAt)
	if ttl <= 0 {
		return errors.New("auth.Session: already expired")
	}
	return s.rdb.Set(ctx, s.key(sess.SID), data, ttl).Err()
}

// Get 读取 Session；不存在返回 (nil, ErrSessionNotFound)。
func (s *RedisSessionStore) Get(ctx context.Context, sid string) (*Session, error) {
	raw, err := s.rdb.Get(ctx, s.key(sid)).Bytes()
	if err == redis.Nil {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Delete 删除 Session（用于 Logout / 固化重生成）。
func (s *RedisSessionStore) Delete(ctx context.Context, sid string) error {
	return s.rdb.Del(ctx, s.key(sid)).Err()
}

// ErrSessionNotFound 表示 SID 不存在或已过期。
var ErrSessionNotFound = errors.New("auth: session not found")

// NewSID 生成 256-bit 随机 SID（base64url，无填充）。
//
// 256 位足以抵抗在线猜测（>2^128 等价熵）；base64url 避免 Cookie value 转义问题。
func NewSID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth.NewSID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
