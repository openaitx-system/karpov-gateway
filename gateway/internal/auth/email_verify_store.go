package auth

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// EmailVerificationStore 是邮箱验证码 + 频控记账的抽象。
//
// 语义：
//   - PutCode 写入或覆盖最新验证码（同一邮箱同时只允许 1 个 code 待验证）。
//   - GetCode 读最新有效 code；不存在 / 已过期返回 ErrEmailCodeMissing。
//   - DeleteCode 校验通过后调用，让该 code 无法再被使用（防重放）。
//   - IncrSendCount 在每次 SendEmailVerification 调用时记一次：
//       * 第一参数 key 用 "email:<addr>" 区分 per-email；调用方再算 per-IP。
//       * window 是计数滚动窗口（小时级 / 天级，按调用方需要）。
//       * 返回当前窗口内累计次数；调用方据此判定是否触发限速。
//   - GetLastSentAt 用于实现"最近发送时间"+ 冷却（cooldown）：60s 内不可重发。
type EmailVerificationStore interface {
	PutCode(ctx context.Context, email, code string, ttl time.Duration) error
	GetCode(ctx context.Context, email string) (string, error)
	DeleteCode(ctx context.Context, email string) error
	IncrSendCount(ctx context.Context, key string, window time.Duration) (int, error)
	GetLastSentAt(ctx context.Context, email string) (time.Time, error)
	MarkSentAt(ctx context.Context, email string, at time.Time, cooldown time.Duration) error
}

// 已知错误。
var (
	// ErrEmailCodeMissing 表示该邮箱没有进行中的验证码（从未发送 / 已过期 / 已消费）。
	ErrEmailCodeMissing = errors.New("auth: email verification code missing or expired")
)

// ---- Redis 实现 ----

// RedisEmailVerificationStore 用 Redis 做存储。
//
// Key 布局（默认前缀 auth:emailcode）：
//   - {prefix}:code:{email}      String，TTL = code 有效期（默认 10min），值 = 6 位 code
//   - {prefix}:rl:{key}          INCR + EXPIRE，window 内累计发送次数（key 由调用方传）
//   - {prefix}:lastsent:{email}  String，TTL = cooldown，存最近一次发送 unix 秒
type RedisEmailVerificationStore struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewRedisEmailVerificationStore 构造 Redis 实现。空 prefix 用默认值。
func NewRedisEmailVerificationStore(rdb *redis.Client, keyPrefix string) *RedisEmailVerificationStore {
	if keyPrefix == "" {
		keyPrefix = "auth:emailcode"
	}
	return &RedisEmailVerificationStore{rdb: rdb, keyPrefix: keyPrefix}
}

func (s *RedisEmailVerificationStore) keyCode(email string) string {
	return s.keyPrefix + ":code:" + strings.ToLower(strings.TrimSpace(email))
}

func (s *RedisEmailVerificationStore) keyRL(k string) string { return s.keyPrefix + ":rl:" + k }

func (s *RedisEmailVerificationStore) keyLast(email string) string {
	return s.keyPrefix + ":lastsent:" + strings.ToLower(strings.TrimSpace(email))
}

// PutCode 实现 EmailVerificationStore.PutCode。
func (s *RedisEmailVerificationStore) PutCode(ctx context.Context, email, code string, ttl time.Duration) error {
	if email == "" || code == "" {
		return errors.New("auth.EmailCode: empty email/code")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return s.rdb.Set(ctx, s.keyCode(email), code, ttl).Err()
}

// GetCode 实现 EmailVerificationStore.GetCode。
func (s *RedisEmailVerificationStore) GetCode(ctx context.Context, email string) (string, error) {
	v, err := s.rdb.Get(ctx, s.keyCode(email)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrEmailCodeMissing
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// DeleteCode 实现 EmailVerificationStore.DeleteCode。
func (s *RedisEmailVerificationStore) DeleteCode(ctx context.Context, email string) error {
	return s.rdb.Del(ctx, s.keyCode(email)).Err()
}

// IncrSendCount 实现 EmailVerificationStore.IncrSendCount。
//
// INCR 后再 EXPIRE：第一次创建 key 时 EXPIRE 才会生效；后续 INCR 不重置 TTL，
// 自然形成"滚动 window"行为。Redis 7+ 有 SETEX/INCRBY 原语但此处够用。
func (s *RedisEmailVerificationStore) IncrSendCount(ctx context.Context, key string, window time.Duration) (int, error) {
	if key == "" {
		return 0, errors.New("auth.EmailCode: empty rl key")
	}
	if window <= 0 {
		window = time.Hour
	}
	pipe := s.rdb.Pipeline()
	incr := pipe.Incr(ctx, s.keyRL(key))
	pipe.Expire(ctx, s.keyRL(key), window)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int(incr.Val()), nil
}

// GetLastSentAt 实现 EmailVerificationStore.GetLastSentAt。
func (s *RedisEmailVerificationStore) GetLastSentAt(ctx context.Context, email string) (time.Time, error) {
	v, err := s.rdb.Get(ctx, s.keyLast(email)).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var sec int64
	if _, err := fmt.Sscanf(v, "%d", &sec); err != nil {
		return time.Time{}, nil
	}
	return time.Unix(sec, 0), nil
}

// MarkSentAt 实现 EmailVerificationStore.MarkSentAt。
//
// cooldown <= 0 时不写入（caller 关闭了冷却）。
func (s *RedisEmailVerificationStore) MarkSentAt(ctx context.Context, email string, at time.Time, cooldown time.Duration) error {
	if cooldown <= 0 {
		return nil
	}
	return s.rdb.Set(ctx, s.keyLast(email), fmt.Sprintf("%d", at.Unix()), cooldown).Err()
}

// ---- 内存实现 ----

// MemEmailVerificationStore 进程内实现，单测用。
type MemEmailVerificationStore struct {
	mu     sync.Mutex
	codes  map[string]memTTLItem
	rl     map[string]memTTLItem // value = int counter
	last   map[string]memTTLItem // value = time.Time
}

// NewMemEmailVerificationStore 构造空仓库。
func NewMemEmailVerificationStore() *MemEmailVerificationStore {
	return &MemEmailVerificationStore{
		codes: map[string]memTTLItem{},
		rl:    map[string]memTTLItem{},
		last:  map[string]memTTLItem{},
	}
}

// PutCode 实现接口。
func (s *MemEmailVerificationStore) PutCode(_ context.Context, email, code string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[strings.ToLower(email)] = memTTLItem{value: code, expiresAt: time.Now().Add(ttl)}
	return nil
}

// GetCode 实现接口。
func (s *MemEmailVerificationStore) GetCode(_ context.Context, email string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.codes[strings.ToLower(email)]
	if !ok {
		return "", ErrEmailCodeMissing
	}
	if time.Now().After(it.expiresAt) {
		delete(s.codes, strings.ToLower(email))
		return "", ErrEmailCodeMissing
	}
	v, _ := it.value.(string)
	return v, nil
}

// DeleteCode 实现接口。
func (s *MemEmailVerificationStore) DeleteCode(_ context.Context, email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.codes, strings.ToLower(email))
	return nil
}

// IncrSendCount 实现接口。
func (s *MemEmailVerificationStore) IncrSendCount(_ context.Context, key string, window time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.rl[key]
	now := time.Now()
	if !ok || now.After(it.expiresAt) {
		s.rl[key] = memTTLItem{value: 1, expiresAt: now.Add(window)}
		return 1, nil
	}
	n, _ := it.value.(int)
	n++
	it.value = n
	s.rl[key] = it
	return n, nil
}

// GetLastSentAt 实现接口。
func (s *MemEmailVerificationStore) GetLastSentAt(_ context.Context, email string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.last[strings.ToLower(email)]
	if !ok || time.Now().After(it.expiresAt) {
		return time.Time{}, nil
	}
	t, _ := it.value.(time.Time)
	return t, nil
}

// MarkSentAt 实现接口。
func (s *MemEmailVerificationStore) MarkSentAt(_ context.Context, email string, at time.Time, cooldown time.Duration) error {
	if cooldown <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last[strings.ToLower(email)] = memTTLItem{value: at, expiresAt: at.Add(cooldown)}
	return nil
}

// ---- 验证码生成 ----

// GenerateNumericCode 生成 n 位数字验证码（默认 6 位）。
//
// 用 crypto/rand 而非 math/rand：单条码 1e6 空间，攻击者每分钟最多猜 N 次（受限速保护），
// 仍要保证单次猜中概率服从均匀分布——伪随机码会让"前缀热点"被高频字典攻击命中。
func GenerateNumericCode(digits int) (string, error) {
	if digits <= 0 || digits > 12 {
		digits = 6
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("auth.GenerateNumericCode: %w", err)
	}
	v := binary.BigEndian.Uint64(buf[:])
	mod := uint64(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	n := v % mod
	return fmt.Sprintf("%0*d", digits, n), nil
}
