package gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/quota"
)

// quotaTimeout 是 quota.CheckAndConsume 的最大等待时间（Redis 通常 < 5ms）。
// 超时后 middleware fail-open，避免 Redis 故障阻断整个网关。
const quotaTimeout = 2 * time.Second

// QuotaResolver 把 gin Request 解析成 (userID, provider, endpoint, rules)。
//
// 不同部署可能有不同的策略（cookie session / API key / mTLS subject 解 user；
// 静态 plan / DB plan 派生 limits），因此 middleware 不内置。
//
// 实现示例见 NewSimpleQuotaResolver。
type QuotaResolver interface {
	Resolve(c *gin.Context) (rules quota.Rules, ok bool)
}

// QuotaMiddlewareOptions 控制 QuotaMiddleware 行为。
type QuotaMiddlewareOptions struct {
	// Service 必填：调用 CheckAndConsume / Refund 的实例。
	Service *quota.Service

	// Resolver 必填：从 Request 提取 user / provider / endpoint。
	Resolver QuotaResolver

	// SkipPaths 是不走 quota 的路径前缀（健康检查、认证流等）。
	SkipPaths []string

	// SoftLimitHeader 命中软限时写到响应（默认 "X-Quota-Soft-Limit: 1"）。
	// 客户端可据此提示用户，但请求仍放行。
	SoftLimitHeader string
}

// QuotaMiddleware 是 gin middleware：在路由进入时调 Quota.CheckAndConsume，
// 拒绝时返回 429 + Retry-After；通过时把 rules 塞进 ctx 让 handler 失败时退款。
//
// HardLimit → 429 + Retry-After 秒数（毫秒向上取整成秒）
// SoftLimit → 200 + X-Quota-Soft-Limit + X-Quota-Retry-After-Ms（不阻断）
// Allow      → 透传
//
// Refund 路径：handler 失败（5xx / provider error）时调用 c.Get("quota.rules")
// 加 quota.Service.Refund 即可。本 middleware 不自动做 refund，因为"哪种错误算
// 用户原因 vs provider 原因"是业务逻辑，应由 handler 自行决定。
func QuotaMiddleware(opts QuotaMiddlewareOptions) gin.HandlerFunc {
	if opts.SoftLimitHeader == "" {
		opts.SoftLimitHeader = "X-Quota-Soft-Limit"
	}
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		for _, skip := range opts.SkipPaths {
			if strings.HasPrefix(path, skip) {
				c.Next()
				return
			}
		}

		rules, ok := opts.Resolver.Resolve(c)
		if !ok {
			// 无法解析（未登录 / 无 API key）→ 不强制走 quota，让后续 auth
			// middleware 决定是否拒绝。这样未登录访问 /v1/auth/login 不会被
			// 误命中 quota（因为 SkipPaths 已经过滤但保险起见仍然此路径）。
			c.Next()
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), quotaTimeout)
		defer cancel()

		result, err := opts.Service.CheckAndConsume(ctx, rules)
		if err != nil {
			// Redis 故障 fail-open（不阻断业务）；具体降级策略可由调用方定制。
			c.Header("X-Quota-Error", "1")
			c.Next()
			return
		}

		switch result.Decision {
		case quota.DecisionHardLimit:
			retryAfterSec := (result.RetryAfterMs + 999) / 1000
			if retryAfterSec < 1 {
				retryAfterSec = 1
			}
			c.Header("Retry-After", strconv.Itoa(retryAfterSec))
			c.Header("X-Quota-Day-Used", strconv.FormatInt(result.DayUsed, 10))
			c.Header("X-Quota-Month-Used", strconv.FormatInt(result.MonthUsed, 10))
			FailData(c, http.StatusTooManyRequests, CodeQuotaExceeded, "quota exceeded", map[string]any{
				"dayUsed":       result.DayUsed,
				"monthUsed":     result.MonthUsed,
				"dayLimit":      result.DayLimit,
				"monthLimit":    result.MonthLimit,
				"retryAfterSec": retryAfterSec,
			})
			return
		case quota.DecisionSoftLimit:
			c.Header(opts.SoftLimitHeader, "1")
			c.Header("X-Quota-Retry-After-Ms", strconv.Itoa(result.RetryAfterMs))
			c.Header("X-Quota-Month-Used", strconv.FormatInt(result.MonthUsed, 10))
		}

		// 透传 quota.Rules 给 handler；失败时可调 Refund。
		c.Set("quota.rules", rules)
		c.Set("quota.result", result)
		c.Next()
	}
}

// 下面是默认实现，部署可以直接用或替换。

// SimpleQuotaResolver 是基于 URL 路径前缀 + 头部用户 ID 的最小 Resolver。
//
// 路径形如 /v1/{provider}/{endpoint}/...，用户从 X-User-Id 头取（mTLS 场景下
// 由 Edge Gateway 在 Auth middleware 完成 session 校验后注入）。
//
// 生产建议自实现 QuotaResolver：从 session cookie / API key 解出 user，
// 从 plan / subscription DB 派生 day/month/soft 配额。
type SimpleQuotaResolver struct {
	UserHeader   string // 默认 "X-User-Id"
	DayLimit     int64
	MonthLimit   int64
	SoftLimitPct int
	Weight       int
	// EndpointMap：(method, path-suffix) → endpoint 名（用于 quota.Rules.Endpoint）。
	EndpointMap map[string]string
}

// NewSimpleQuotaResolver 构造默认 Resolver。
//
// EndpointMap 形如 {"GET /v1/qqmusic/songs/": "GetSong"}。匹配最长前缀。
func NewSimpleQuotaResolver(dayLimit, monthLimit int64, softPct, weight int) *SimpleQuotaResolver {
	return &SimpleQuotaResolver{
		UserHeader:   "X-User-Id",
		DayLimit:     dayLimit,
		MonthLimit:   monthLimit,
		SoftLimitPct: softPct,
		Weight:       weight,
		EndpointMap:  map[string]string{},
	}
}

// Resolve 实现 QuotaResolver。
func (r *SimpleQuotaResolver) Resolve(c *gin.Context) (quota.Rules, bool) {
	uid := c.GetHeader(r.UserHeader)
	if uid == "" {
		return quota.Rules{}, false
	}
	provider, endpoint := r.parsePath(c.Request.Method, c.Request.URL.Path)
	if provider == "" || endpoint == "" {
		return quota.Rules{}, false
	}
	weight := r.Weight
	if weight <= 0 {
		weight = 1
	}
	return quota.Rules{
		UserID:       uid,
		Provider:     provider,
		Endpoint:     endpoint,
		Weight:       weight,
		DayLimit:     r.DayLimit,
		MonthLimit:   r.MonthLimit,
		SoftLimitPct: r.SoftLimitPct,
	}, true
}

// parsePath 从 /v1/{provider}/... 提取 provider；endpoint 由 EndpointMap 派发。
//
// 不命中 EndpointMap 时返回 ("", "")，让 middleware 跳过该请求（避免误扣）。
func (r *SimpleQuotaResolver) parsePath(method, path string) (provider, endpoint string) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) < 2 || parts[0] != "v1" {
		return "", ""
	}
	provider = parts[1]

	// 找最长 prefix 匹配的 endpoint
	prefix := method + " " + path
	var bestKey string
	for k := range r.EndpointMap {
		if strings.HasPrefix(prefix, k) && len(k) > len(bestKey) {
			bestKey = k
		}
	}
	if bestKey == "" {
		return "", ""
	}
	return provider, r.EndpointMap[bestKey]
}
