// Package quota 实现配额服务（双层窗口 + 端点权重 + 软/硬限）。
//
// 与 Plan §8.3 对齐：
//
//	CheckAndConsume(user, provider, endpoint) → Redis Lua 原子操作：
//	  1. 取 endpoint_weight[endpoint]
//	  2. INCRBY today_key weight + INCRBY month_key weight
//	  3. 任何超限 → DECRBY 撤回 → HARD_LIMIT(429)
//	  4. month_used > month_limit * soft_pct(80%) → SOFT_LIMIT(retry_after)
//
// 退款（下游 provider 调用失败非用户原因）→ 原子 DECRBY 防负数。
//
// usage_events 异步落库属于 worker 范畴，本包不做。
package quota

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Decision 是 CheckAndConsume 的三态结果（与 pool.proto 对齐）。
type Decision int

const (
	DecisionAllow     Decision = iota // 通过
	DecisionSoftLimit                 // 软限：仍可调用，但应限速 / 提示用户
	DecisionHardLimit                 // 硬限：拒绝（HTTP 429）
)

// CheckResult 是 CheckAndConsume 的完整返回。
type CheckResult struct {
	Decision     Decision
	DayUsed      int64 // 当前日已用（含本次）
	MonthUsed    int64 // 当前月已用（含本次）
	DayLimit     int64
	MonthLimit   int64
	RetryAfterMs int // SoftLimit 时建议的退避；HardLimit 时给到下一窗口
}

// Rules 是单次 Check 时的配额规则快照。
//
// 由调用方（Music Service middleware）从 Plan + Subscription 解析得到，
// 然后传给本包；本包不直接读 PG。这样 quota.Service 可以做纯 Redis Lua 测试。
type Rules struct {
	UserID           string
	Provider         string
	Endpoint         string
	Weight           int   // endpoint_weights[endpoint]，默认 1
	DayLimit         int64 // 0 表示无日限
	MonthLimit       int64 // 0 表示无月限
	SoftLimitPct     int   // 0..100；0 表示禁用软限
	WindowDayStart   time.Time
	WindowMonthStart time.Time
}

// Service 是 Quota 的业务入口。
type Service struct {
	rdb       *redis.Client
	clock     func() time.Time
	keyPrefix string
}

// NewService 构造 Quota 服务。
func NewService(rdb *redis.Client, opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "quota"
	}
	return &Service{rdb: rdb, clock: opts.Clock, keyPrefix: opts.KeyPrefix}
}

// Options 控制 Service 构造。
type Options struct {
	Clock     func() time.Time
	KeyPrefix string
}

// dayKey / monthKey 派生 Redis 键。窗口边界以 UTC 切日/切月（避免时区漂移）。
func (s *Service) dayKey(r Rules) string {
	t := r.WindowDayStart
	if t.IsZero() {
		t = s.clock().UTC().Truncate(24 * time.Hour)
	}
	return fmt.Sprintf("%s:d:{%s}:%s:%s:%s", s.keyPrefix, r.UserID, r.Provider, r.Endpoint, t.Format("20060102"))
}

func (s *Service) monthKey(r Rules) string {
	t := r.WindowMonthStart
	if t.IsZero() {
		now := s.clock().UTC()
		t = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return fmt.Sprintf("%s:m:{%s}:%s:%s:%s", s.keyPrefix, r.UserID, r.Provider, r.Endpoint, t.Format("200601"))
}

// checkLuaScript 是原子 INCR + 撤回 + 软限判定。
//
// 入参 KEYS：
//
//	[1] day_key
//	[2] month_key
//
// 入参 ARGV：
//
//	[1] weight
//	[2] day_limit (0 = no limit)
//	[3] month_limit (0 = no limit)
//	[4] soft_pct (0 = disabled)
//	[5] day_ttl_seconds
//	[6] month_ttl_seconds
//
// 返回数组 [decision, day_used, month_used]，
// decision: 0=Allow, 1=SoftLimit, 2=HardLimit。
var checkLuaScript = redis.NewScript(`
local day_key = KEYS[1]
local month_key = KEYS[2]
local weight = tonumber(ARGV[1])
local day_limit = tonumber(ARGV[2])
local month_limit = tonumber(ARGV[3])
local soft_pct = tonumber(ARGV[4])
local day_ttl = tonumber(ARGV[5])
local month_ttl = tonumber(ARGV[6])

local day_used = tonumber(redis.call('INCRBY', day_key, weight))
if day_ttl > 0 then redis.call('EXPIRE', day_key, day_ttl) end

local month_used = tonumber(redis.call('INCRBY', month_key, weight))
if month_ttl > 0 then redis.call('EXPIRE', month_key, month_ttl) end

-- hard limits: 任一超限即撤回并报 HardLimit
if (day_limit > 0 and day_used > day_limit) or (month_limit > 0 and month_used > month_limit) then
  redis.call('DECRBY', day_key, weight)
  redis.call('DECRBY', month_key, weight)
  return {2, day_used - weight, month_used - weight}
end

-- soft limit: 月用量超过 month_limit * soft_pct% 但未超 hard
if soft_pct > 0 and month_limit > 0 and (month_used * 100) > (month_limit * soft_pct) then
  return {1, day_used, month_used}
end

return {0, day_used, month_used}
`)

// refundLuaScript 把日/月计数器各 DECRBY weight，但下界保护到 0（避免并发竞争把
// 计数减成负值，导致下个用户白嫖）。
var refundLuaScript = redis.NewScript(`
local day_key = KEYS[1]
local month_key = KEYS[2]
local weight = tonumber(ARGV[1])

local day_used = tonumber(redis.call('GET', day_key) or '0')
local month_used = tonumber(redis.call('GET', month_key) or '0')
local day_dec = math.min(weight, day_used)
local month_dec = math.min(weight, month_used)
if day_dec > 0 then redis.call('DECRBY', day_key, day_dec) end
if month_dec > 0 then redis.call('DECRBY', month_key, month_dec) end
return {day_used - day_dec, month_used - month_dec}
`)

// CheckAndConsume 执行一次配额扣减。
//
// 错误：仅 Redis 不可达时返回 error；业务上的拒绝通过 CheckResult.Decision 表示。
func (s *Service) CheckAndConsume(ctx context.Context, r Rules) (*CheckResult, error) {
	if r.UserID == "" {
		return nil, errors.New("quota: empty user_id")
	}
	weight := r.Weight
	if weight <= 0 {
		weight = 1
	}
	dayKey := s.dayKey(r)
	monthKey := s.monthKey(r)

	// TTL 设置：day=2 天（跨日仍能查），month=35 天。
	dayTTL := int64(2 * 24 * 3600)
	monthTTL := int64(35 * 24 * 3600)

	res, err := checkLuaScript.Run(ctx, s.rdb,
		[]string{dayKey, monthKey},
		weight, r.DayLimit, r.MonthLimit, r.SoftLimitPct, dayTTL, monthTTL,
	).Slice()
	if err != nil {
		return nil, fmt.Errorf("quota.CheckAndConsume: %w", err)
	}
	if len(res) != 3 {
		return nil, fmt.Errorf("quota.CheckAndConsume: unexpected lua return: %v", res)
	}
	out := &CheckResult{
		Decision:   Decision(toInt64(res[0])),
		DayUsed:    toInt64(res[1]),
		MonthUsed:  toInt64(res[2]),
		DayLimit:   r.DayLimit,
		MonthLimit: r.MonthLimit,
	}
	if out.Decision == DecisionHardLimit {
		// 简单退避：到下个日窗（次日 0:00 UTC）。
		now := s.clock().UTC()
		nextDay := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		out.RetryAfterMs = int(nextDay.Sub(now).Milliseconds())
	} else if out.Decision == DecisionSoftLimit {
		// 软限：自适应退避（占用率越高越长，5 ~ 30 秒）。
		ratio := 1.0
		if r.MonthLimit > 0 {
			ratio = float64(out.MonthUsed) / float64(r.MonthLimit)
		}
		out.RetryAfterMs = int(5000 + (ratio * 25000))
	}
	return out, nil
}

// Refund 把 weight 退还到用户的当前日/月窗口。
func (s *Service) Refund(ctx context.Context, r Rules) (*CheckResult, error) {
	weight := r.Weight
	if weight <= 0 {
		weight = 1
	}
	dayKey := s.dayKey(r)
	monthKey := s.monthKey(r)
	res, err := refundLuaScript.Run(ctx, s.rdb,
		[]string{dayKey, monthKey},
		weight,
	).Slice()
	if err != nil {
		return nil, fmt.Errorf("quota.Refund: %w", err)
	}
	if len(res) != 2 {
		return nil, fmt.Errorf("quota.Refund: unexpected return: %v", res)
	}
	return &CheckResult{
		Decision:  DecisionAllow,
		DayUsed:   toInt64(res[0]),
		MonthUsed: toInt64(res[1]),
	}, nil
}

// GetUsage 只读查询当前窗口用量（不扣减）。
func (s *Service) GetUsage(ctx context.Context, r Rules) (*CheckResult, error) {
	dayKey := s.dayKey(r)
	monthKey := s.monthKey(r)
	day, err := s.rdb.Get(ctx, dayKey).Int64()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	month, err := s.rdb.Get(ctx, monthKey).Int64()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	return &CheckResult{Decision: DecisionAllow, DayUsed: day, MonthUsed: month, DayLimit: r.DayLimit, MonthLimit: r.MonthLimit}, nil
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case string:
		var n int64
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	default:
		return 0
	}
}
