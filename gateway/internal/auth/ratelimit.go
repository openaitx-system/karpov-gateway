package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyRateLimiter 基于 Redis 滑动窗口实现 Per-API-Key 限速。
type KeyRateLimiter struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewKeyRateLimiter 构造限速器。
func NewKeyRateLimiter(rdb *redis.Client) *KeyRateLimiter {
	return &KeyRateLimiter{rdb: rdb, keyPrefix: "rl:apikey"}
}

// RateLimitResult 限速检查结果。
type RateLimitResult struct {
	Allowed    bool
	Remaining  int64 // 当前窗口剩余
	Limit      int64 // 窗口总限
	RetryAfter time.Duration
}

// slidingWindowLua 实现滑动窗口限速：
// KEYS[1] = rate limit key
// ARGV[1] = window size in seconds
// ARGV[2] = max requests in window
// ARGV[3] = current timestamp (seconds)
// 返回 [allowed(0/1), current_count, ttl_ms]
var slidingWindowLua = redis.NewScript(`
local key = KEYS[1]
local window = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local clear_before = now - window

redis.call('ZREMRANGEBYSCORE', key, '-inf', clear_before)
local count = redis.call('ZCARD', key)

if count < limit then
  redis.call('ZADD', key, now, now .. ':' .. math.random(1000000))
  redis.call('EXPIRE', key, window + 1)
  return {1, count + 1, 0}
end

local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
local retry_after = 0
if #oldest >= 2 then
  retry_after = math.ceil((tonumber(oldest[2]) + window - now) * 1000)
end
return {0, count, retry_after}
`)

// CheckRPM 检查每分钟限速。rpm=0 表示不限。
func (l *KeyRateLimiter) CheckRPM(ctx context.Context, keyID string, rpm int) (*RateLimitResult, error) {
	if rpm <= 0 {
		return &RateLimitResult{Allowed: true, Remaining: -1, Limit: 0}, nil
	}
	rkey := fmt.Sprintf("%s:rpm:%s", l.keyPrefix, keyID)
	now := time.Now().Unix()
	res, err := slidingWindowLua.Run(ctx, l.rdb, []string{rkey}, 60, rpm, now).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("auth.KeyRateLimiter.CheckRPM: %w", err)
	}
	return &RateLimitResult{
		Allowed:    res[0] == 1,
		Remaining:  int64(rpm) - res[1],
		Limit:      int64(rpm),
		RetryAfter: time.Duration(res[2]) * time.Millisecond,
	}, nil
}

// CheckDaily 检查每日限速。daily=0 表示不限。
func (l *KeyRateLimiter) CheckDaily(ctx context.Context, keyID string, daily int64) (*RateLimitResult, error) {
	if daily <= 0 {
		return &RateLimitResult{Allowed: true, Remaining: -1, Limit: 0}, nil
	}
	now := time.Now().UTC()
	dayKey := fmt.Sprintf("%s:day:%s:%s", l.keyPrefix, keyID, now.Format("20060102"))
	count, err := l.rdb.Incr(ctx, dayKey).Result()
	if err != nil {
		return nil, fmt.Errorf("auth.KeyRateLimiter.CheckDaily: %w", err)
	}
	if count == 1 {
		nextDay := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		l.rdb.ExpireAt(ctx, dayKey, nextDay)
	}
	if count > daily {
		l.rdb.Decr(ctx, dayKey)
		nextDay := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		return &RateLimitResult{
			Allowed:    false,
			Remaining:  0,
			Limit:      daily,
			RetryAfter: nextDay.Sub(now),
		}, nil
	}
	return &RateLimitResult{
		Allowed:   true,
		Remaining: daily - count,
		Limit:     daily,
	}, nil
}
