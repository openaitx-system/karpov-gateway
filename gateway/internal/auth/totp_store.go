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

// ---------- 待确认 TOTP secret ----------
//
// 用户点"启用 TOTP"时后端生成 secret 并先暂存到此处；用户在 Authenticator 里
// 扫码 + 输入 6 位码 → ConfirmEnableTOTP 才把 secret 落库。10 分钟后自动失效。
//
// 只在内存层使用 string；不主动加密——TTL 短 + Redis ACL 即够；与 User.TOTPSecret
// 落库后的明文存储一致（同一改进位点：M16+ 改 AES-256-GCM 时一起改）。

// PendingTOTPStore 抽象待确认 secret 的读/写/删。
type PendingTOTPStore interface {
	Put(ctx context.Context, userID, secret string, ttl time.Duration) error
	Get(ctx context.Context, userID string) (string, error)
	Delete(ctx context.Context, userID string) error
}

// ErrPendingTOTPNotFound 用户没有进行中的"启用 TOTP"流程，或已超时。
var ErrPendingTOTPNotFound = errors.New("auth: no pending TOTP enable session")

// RedisPendingTOTPStore Redis 实现。
type RedisPendingTOTPStore struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewRedisPendingTOTPStore 构造。keyPrefix 默认 "auth:totp:pending"。
func NewRedisPendingTOTPStore(rdb *redis.Client, keyPrefix string) *RedisPendingTOTPStore {
	if keyPrefix == "" {
		keyPrefix = "auth:totp:pending"
	}
	return &RedisPendingTOTPStore{rdb: rdb, keyPrefix: keyPrefix}
}

func (s *RedisPendingTOTPStore) key(userID string) string {
	return s.keyPrefix + ":" + userID
}

// Put 写入或覆盖 pending secret。
func (s *RedisPendingTOTPStore) Put(ctx context.Context, userID, secret string, ttl time.Duration) error {
	if userID == "" || secret == "" {
		return errors.New("auth.PendingTOTP: empty user/secret")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return s.rdb.Set(ctx, s.key(userID), secret, ttl).Err()
}

// Get 读 pending secret；不存在返回 ErrPendingTOTPNotFound。
func (s *RedisPendingTOTPStore) Get(ctx context.Context, userID string) (string, error) {
	v, err := s.rdb.Get(ctx, s.key(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrPendingTOTPNotFound
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// Delete 删除 pending secret（成功 confirm 或用户主动取消）。
func (s *RedisPendingTOTPStore) Delete(ctx context.Context, userID string) error {
	return s.rdb.Del(ctx, s.key(userID)).Err()
}

// ---------- 登录第二步 challenge ----------
//
// 用户输完密码后，如果账号已开 TOTP，本进程会创建一个 challenge：
//   - 生成短 ID，存 {userID, ip, ua} JSON 到 Redis，TTL 5 分钟
//   - 把 ID 通过 LoginResponse.challenge_id 返回给前端
//   - 前端再调 VerifyTOTP(challenge_id, code) 完成第二步登录

// TOTPChallenge 是登录中间态。
type TOTPChallenge struct {
	UserID    string    `json:"user_id"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"ua"`
	IssuedAt  time.Time `json:"issued_at"`
}

// TOTPChallengeStore 抽象 challenge 持久化。
type TOTPChallengeStore interface {
	Put(ctx context.Context, id string, ch *TOTPChallenge, ttl time.Duration) error
	Get(ctx context.Context, id string) (*TOTPChallenge, error)
	Delete(ctx context.Context, id string) error
}

// ErrTOTPChallengeNotFound challenge 已过期或不存在。
var ErrTOTPChallengeNotFound = errors.New("auth: totp challenge not found or expired")

// RedisTOTPChallengeStore Redis 实现。
type RedisTOTPChallengeStore struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewRedisTOTPChallengeStore 构造。keyPrefix 默认 "auth:totp:challenge"。
func NewRedisTOTPChallengeStore(rdb *redis.Client, keyPrefix string) *RedisTOTPChallengeStore {
	if keyPrefix == "" {
		keyPrefix = "auth:totp:challenge"
	}
	return &RedisTOTPChallengeStore{rdb: rdb, keyPrefix: keyPrefix}
}

func (s *RedisTOTPChallengeStore) key(id string) string {
	return s.keyPrefix + ":" + id
}

func (s *RedisTOTPChallengeStore) Put(ctx context.Context, id string, ch *TOTPChallenge, ttl time.Duration) error {
	if id == "" || ch == nil {
		return errors.New("auth.TOTPChallenge: empty id/ch")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	data, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.key(id), data, ttl).Err()
}

func (s *RedisTOTPChallengeStore) Get(ctx context.Context, id string) (*TOTPChallenge, error) {
	raw, err := s.rdb.Get(ctx, s.key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrTOTPChallengeNotFound
	}
	if err != nil {
		return nil, err
	}
	var ch TOTPChallenge
	if err := json.Unmarshal(raw, &ch); err != nil {
		return nil, fmt.Errorf("auth.TOTPChallenge: decode: %w", err)
	}
	return &ch, nil
}

func (s *RedisTOTPChallengeStore) Delete(ctx context.Context, id string) error {
	return s.rdb.Del(ctx, s.key(id)).Err()
}

// NewChallengeID 生成 challenge id（128-bit 随机，base64url）。
//
// 比 SID 短：仅作 5 分钟内的临时关联，无须 256-bit。
func NewChallengeID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth.NewChallengeID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// ---------- 内存实现（仅测试 / 单进程开发用） ----------

// MemPendingTOTPStore 进程内 map 实现，含 TTL（懒清除）。
type MemPendingTOTPStore struct {
	items map[string]memTTLItem
}

func NewMemPendingTOTPStore() *MemPendingTOTPStore {
	return &MemPendingTOTPStore{items: map[string]memTTLItem{}}
}

func (s *MemPendingTOTPStore) Put(_ context.Context, userID, secret string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s.items[userID] = memTTLItem{value: secret, expiresAt: time.Now().Add(ttl)}
	return nil
}

func (s *MemPendingTOTPStore) Get(_ context.Context, userID string) (string, error) {
	it, ok := s.items[userID]
	if !ok {
		return "", ErrPendingTOTPNotFound
	}
	if time.Now().After(it.expiresAt) {
		delete(s.items, userID)
		return "", ErrPendingTOTPNotFound
	}
	v, _ := it.value.(string)
	return v, nil
}

func (s *MemPendingTOTPStore) Delete(_ context.Context, userID string) error {
	delete(s.items, userID)
	return nil
}

// MemTOTPChallengeStore 进程内 map 实现。
type MemTOTPChallengeStore struct {
	items map[string]memTTLItem // value 是 *TOTPChallenge
}

func NewMemTOTPChallengeStore() *MemTOTPChallengeStore {
	return &MemTOTPChallengeStore{items: map[string]memTTLItem{}}
}

func (s *MemTOTPChallengeStore) Put(_ context.Context, id string, ch *TOTPChallenge, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	s.items[id] = memTTLItem{value: ch, expiresAt: time.Now().Add(ttl)}
	return nil
}

func (s *MemTOTPChallengeStore) Get(_ context.Context, id string) (*TOTPChallenge, error) {
	it, ok := s.items[id]
	if !ok {
		return nil, ErrTOTPChallengeNotFound
	}
	if time.Now().After(it.expiresAt) {
		delete(s.items, id)
		return nil, ErrTOTPChallengeNotFound
	}
	ch, ok := it.value.(*TOTPChallenge)
	if !ok {
		return nil, ErrTOTPChallengeNotFound
	}
	return ch, nil
}

func (s *MemTOTPChallengeStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

type memTTLItem struct {
	value     any
	expiresAt time.Time
}
