package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

type PlanQPSConfig struct {
	Plans      map[string]int
	PlanRepo   *PlanRepo
	QuotaPG    *pgxpool.Pool
	DefaultQPS int

	// ExtraUsage 可选：非 nil 时支持"用户开启 Extra Usage 后超额放行 + 计费"。
	// 当 monthly_limit 已耗尽且本字段非空时，调用 CheckAndCharge 决定是否放行。
	ExtraUsage *ExtraUsageService
}

func (cfg PlanQPSConfig) pgDayUsage(ctx context.Context, userID, date string) int64 {
	if cfg.QuotaPG == nil || userID == "" {
		return 0
	}
	var n int64
	_ = cfg.QuotaPG.QueryRow(ctx,
		`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_daily WHERE user_id = $1 AND date = $2::date`,
		userID, date).Scan(&n)
	return n
}

func (cfg PlanQPSConfig) pgMonthUsage(ctx context.Context, userID, month string) int64 {
	if cfg.QuotaPG == nil || userID == "" {
		return 0
	}
	var n int64
	_ = cfg.QuotaPG.QueryRow(ctx,
		`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_monthly WHERE user_id = $1 AND year_month = $2`,
		userID, month).Scan(&n)
	return n
}

// QPS 用 Redis Lua（纯秒级计数，不涉及 PG）
var qpsCheckLua = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local current = tonumber(redis.call('GET', key) or '0')
if current >= limit then
  return -1
end
redis.call('INCR', key)
if ttl > 0 then
  redis.call('EXPIRE', key, ttl)
end
return current + 1
`)

// PlanQuotaMiddleware: QPS 用 Redis，日/月配额直接查 PG（唯一数据源，无双重计数）。
func PlanQuotaMiddleware(rdb *redis.Client, cfg PlanQPSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/v1/") ||
			strings.HasPrefix(path, "/v1/auth/") ||
			strings.HasPrefix(path, "/v1/admin/") ||
			strings.HasPrefix(path, "/v1/billing/") ||
			strings.HasPrefix(path, "/v1/usage/") ||
			strings.HasPrefix(path, "/v1/netease/auth/") {
			c.Next()
			return
		}

		var (
			qps               = cfg.DefaultQPS
			dailyLimit        int64
			monthLimit        int64
			prefix            string
			userID            string
			planID            string
			overagePricePer1k int64
		)

		if rec, exists := c.Get("auth.apikey_record"); exists {
			if keyRec, ok := rec.(*auth.APIKeyRecord); ok && keyRec != nil {
				prefix = "quota:key:" + keyRec.ID
				userID = keyRec.UserID
				planID = keyRec.PlanID
				if keyRec.PlanID != "" && cfg.PlanRepo != nil {
					if p, err := cfg.PlanRepo.Get(c.Request.Context(), keyRec.PlanID); err == nil {
						qps = p.QPS
						dailyLimit = p.DailyLimit
						monthLimit = p.MonthlyLimit
						if p.PayAsYouGo {
							overagePricePer1k = p.OveragePricePer1k
						}
					}
				} else if keyRec.PlanID != "" {
					if pqps, ok := cfg.Plans[keyRec.PlanID]; ok {
						qps = pqps
					}
				}
			}
		}

		if prefix == "" {
			userID = c.Request.Header.Get("X-User-Id")
			if userID == "" {
				c.Next()
				return
			}
			prefix = "quota:user:" + userID
		}

		ctx := c.Request.Context()
		// QPS 用单调时间，单位不依赖时区；日 / 月窗口用本地时区，让用户的"今日 / 本月"
		// 与 dashboard 上看到的一致。
		now := time.Now()

		// 1. QPS（Redis）
		if qps > 0 {
			secKey := fmt.Sprintf("%s:s:%d", prefix, now.Unix())
			val, err := qpsCheckLua.Run(ctx, rdb, []string{secKey}, qps, 2).Int64()
			if err == nil && val == -1 {
				c.Header("X-RateLimit-QPS-Limit", strconv.Itoa(qps))
				c.Header("X-RateLimit-QPS-Remaining", "0")
				c.Header("Retry-After", "1")
				FailData(c, http.StatusTooManyRequests, CodeRateLimited, "QPS limit exceeded", map[string]any{
					"limit": qps, "retryAfter": 1,
				})
				return
			}
			if err == nil {
				c.Header("X-RateLimit-QPS-Limit", strconv.Itoa(qps))
				c.Header("X-RateLimit-QPS-Remaining", strconv.FormatInt(max64(int64(qps)-val, 0), 10))
			}
		}

		// 2. 日限额（PG 唯一数据源）
		if dailyLimit > 0 {
			used := cfg.pgDayUsage(ctx, userID, localDate(now))
			log.Printf("[quota] daily: user=%s used=%d limit=%d", userID, used, dailyLimit)
			c.Header("X-Quota-Daily-Limit", strconv.FormatInt(dailyLimit, 10))
			c.Header("X-Quota-Daily-Remaining", strconv.FormatInt(max64(dailyLimit-used, 0), 10))
			if used >= dailyLimit {
				// retry-after：本地时区的明天 0 点，让"明天解锁"语义跟用户日历一致。
				nextDay := localStartOfNextDay(now)
				retryAfter := int(nextDay.Sub(now).Seconds())
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				FailData(c, http.StatusTooManyRequests, CodeQuotaExceeded, "daily quota exceeded", map[string]any{
					"dailyLimit": dailyLimit, "dailyUsed": used, "retryAfter": retryAfter,
				})
				return
			}
		}

		// 3. 月限额（PG 唯一数据源）
		if monthLimit > 0 {
			used := cfg.pgMonthUsage(ctx, userID, localMonth(now))
			log.Printf("[quota] monthly: user=%s used=%d limit=%d", userID, used, monthLimit)
			c.Header("X-Quota-Monthly-Limit", strconv.FormatInt(monthLimit, 10))
			c.Header("X-Quota-Monthly-Remaining", strconv.FormatInt(max64(monthLimit-used, 0), 10))
			if used >= monthLimit {
				// Extra Usage：用户开启了"超额放行"开关时，只要 cap 没耗尽就放行 + 计费。
				if cfg.ExtraUsage != nil && overagePricePer1k > 0 {
					provider, endpoint := splitProviderEndpoint(path)
					weight := 1
					dec, err := cfg.ExtraUsage.CheckAndCharge(ctx, userID, planID, provider, endpoint, weight, overagePricePer1k)
					if err == nil && dec != nil && dec.Allow {
						c.Header("X-Extra-Usage", "1")
						c.Header("X-Extra-Usage-Charged-Cents", strconv.FormatInt(dec.PriceCents, 10))
						c.Header("X-Extra-Usage-Used-Cents", strconv.FormatInt(dec.UsedCents, 10))
						if dec.CapCents > 0 {
							c.Header("X-Extra-Usage-Cap-Cents", strconv.FormatInt(dec.CapCents, 10))
						}
						c.Next()
						return
					}
					// dec.Reason 决定具体错误码
					reason := "monthly quota exceeded"
					if dec != nil {
						switch dec.Reason {
						case "extra_usage_disabled":
							reason = "monthly quota exceeded; enable extra usage to continue"
						case "plan_not_pay_as_you_go":
							reason = "monthly quota exceeded; current plan does not support extra usage"
						case "extra_usage_cap_exceeded":
							reason = "extra usage monthly cap reached"
						}
					}
					nextMonth := localStartOfNextMonth(now)
					retryAfter := int(nextMonth.Sub(now).Seconds())
					c.Header("Retry-After", strconv.Itoa(retryAfter))
					FailData(c, http.StatusTooManyRequests, CodeQuotaExceeded, reason, map[string]any{
						"monthlyLimit":  monthLimit,
						"monthlyUsed":   used,
						"retryAfter":    retryAfter,
						"extraUsage":    dec,
					})
					return
				}
				nextMonth := localStartOfNextMonth(now)
				retryAfter := int(nextMonth.Sub(now).Seconds())
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				FailData(c, http.StatusTooManyRequests, CodeQuotaExceeded, "monthly quota exceeded", map[string]any{
					"monthlyLimit": monthLimit, "monthlyUsed": used, "retryAfter": retryAfter,
				})
				return
			}
		}

		c.Next()
	}
}

// splitProviderEndpoint 从 /v1/{provider}/{...} 解析出 provider 与 endpoint。
// endpoint 是去掉 /v1/{provider}/ 前缀后的剩余部分（用于审计展示）。
func splitProviderEndpoint(path string) (provider, endpoint string) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/v1/"), "/", 2)
	if len(parts) >= 1 {
		provider = parts[0]
	}
	if len(parts) >= 2 {
		endpoint = parts[1]
	}
	return
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
