package gateway

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

// UsageSummary 是仪表盘用量摘要。
type UsageSummary struct {
	DayUsed    int64  `json:"dayUsed"`
	MonthUsed  int64  `json:"monthUsed"`
	DayLimit   int64  `json:"dayLimit"`
	MonthLimit int64  `json:"monthLimit"`
	Date       string `json:"date"`
	Month      string `json:"month"`
}

// UsageDayRecord 是单日历史记录。
type UsageDayRecord struct {
	Date      string `json:"date"`
	Provider  string `json:"provider"`
	Count     int64  `json:"count"`
	WeightSum int64  `json:"weightSum"`
}

// RealtimeMetrics 是实时指标。
type RealtimeMetrics struct {
	// 速率
	QPM int64 `json:"qpm"` // 每分钟请求数
	QPS int64 `json:"qps"` // 每秒请求数（最近 1 秒）

	// 延迟
	AvgLatencyMs float64 `json:"avgLatencyMs"` // 平均响应时间
	MaxLatencyMs int64   `json:"maxLatencyMs"` // 最大响应时间
	MinLatencyMs int64   `json:"minLatencyMs"` // 最小响应时间

	// 吞吐
	TotalToday   int64 `json:"totalToday"`   // 今日累计请求数
	TotalMonth   int64 `json:"totalMonth"`   // 本月累计请求数
	SuccessRate  float64 `json:"successRate"`  // 成功率（0~1）
	ErrorCount   int64   `json:"errorCount"`   // 最近 1 分钟错误数

	// 配额
	DayUsed    int64 `json:"dayUsed"`
	DayLimit   int64 `json:"dayLimit"`
	MonthUsed  int64 `json:"monthUsed"`
	MonthLimit int64 `json:"monthLimit"`

	Timestamp string `json:"timestamp"`
}

// UsageHandler 提供用量查询、记录和实时指标。
type UsageHandler struct {
	rdb        *redis.Client
	pg         *pgxpool.Pool
	planRepo   *PlanRepo
	keyPrefix  string
	dayLimit   int64
	monthLimit int64

	// QPM 滑动窗口（60 秒环形缓冲）
	buckets    [60]bucket
	bucketIdx  atomic.Int64
	lastTick   atomic.Int64
}

type bucket struct {
	requests   atomic.Int64
	errors     atomic.Int64
	latencySum atomic.Int64
	latencyMax atomic.Int64
	latencyMin atomic.Int64
}

// NewUsageHandler 构造用量 handler 并启动 QPM tick。
func NewUsageHandler(rdb *redis.Client, pg *pgxpool.Pool) *UsageHandler {
	h := &UsageHandler{
		rdb:        rdb,
		pg:         pg,
		keyPrefix:  "quota",
		dayLimit:   100,
		monthLimit: 1000,
	}
	h.lastTick.Store(time.Now().Unix())
	go h.tickLoop()
	return h
}

// Mount 挂载路由。
func (h *UsageHandler) Mount(r *gin.Engine) {
	r.GET("/v1/usage/summary", h.summary)
	r.GET("/v1/usage/history", h.history)
	r.GET("/v1/usage/realtime", h.realtime)
}

// RecordRequest 记录一次请求到 QPM 滑动窗口 + PG 持久化。
// isError=true 时同时递增错误计数（由调用方根据 HTTP status 判定）。
func (h *UsageHandler) RecordRequest(userID, prov string, weight int, latencyMs int64) {
	h.advance()
	idx := h.bucketIdx.Load() % 60
	h.buckets[idx].requests.Add(1)
	h.buckets[idx].latencySum.Add(latencyMs)
	// 更新最大延迟
	for {
		cur := h.buckets[idx].latencyMax.Load()
		if latencyMs <= cur || h.buckets[idx].latencyMax.CompareAndSwap(cur, latencyMs) {
			break
		}
	}
	// 更新最小延迟
	for {
		cur := h.buckets[idx].latencyMin.Load()
		if cur != 0 && latencyMs >= cur {
			break
		}
		if h.buckets[idx].latencyMin.CompareAndSwap(cur, latencyMs) {
			break
		}
	}

	// PG 持久化
	if h.pg == nil || userID == "" {
		return
	}
	go func() {
		// 按"部署时区下的日历天"聚合：UTC 截日会让 UTC+8 用户在凌晨前的请求
		// 落到"昨天"，与 dashboard 上展示的"今日累计"对不上。
		now := localNow()
		date := localDate(now)
		yearMonth := localMonth(now)

		const dailyQ = `
			INSERT INTO quota.usage_aggregates_daily (user_id, provider, date, count, weight_sum)
			VALUES ($1, $2, $3::date, 1, $4)
			ON CONFLICT (user_id, provider, date)
			DO UPDATE SET count = usage_aggregates_daily.count + 1,
			              weight_sum = usage_aggregates_daily.weight_sum + $4
		`
		_, _ = h.pg.Exec(context.Background(), dailyQ, userID, prov, date, weight) //nolint:staticcheck

		const monthlyQ = `
			INSERT INTO quota.usage_aggregates_monthly (user_id, provider, year_month, count, weight_sum)
			VALUES ($1, $2, $3, 1, $4)
			ON CONFLICT (user_id, provider, year_month)
			DO UPDATE SET count = usage_aggregates_monthly.count + 1,
			              weight_sum = usage_aggregates_monthly.weight_sum + $4
		`
		_, _ = h.pg.Exec(context.Background(), monthlyQ, userID, prov, yearMonth, weight) //nolint:staticcheck
	}()
}

// advance 推进滑动窗口到当前秒。
func (h *UsageHandler) advance() {
	now := time.Now().Unix()
	last := h.lastTick.Load()
	if now <= last {
		return
	}
	if h.lastTick.CompareAndSwap(last, now) {
		gap := now - last
		if gap > 60 {
			gap = 60
		}
		oldIdx := last % 60
		for i := int64(1); i <= gap; i++ {
			idx := (oldIdx + i) % 60
			h.buckets[idx].requests.Store(0)
			h.buckets[idx].errors.Store(0)
			h.buckets[idx].latencySum.Store(0)
			h.buckets[idx].latencyMax.Store(0)
			h.buckets[idx].latencyMin.Store(0)
		}
		h.bucketIdx.Store(now)
	}
}

func (h *UsageHandler) tickLoop() {
	ticker := time.NewTicker(time.Second)
	for range ticker.C {
		h.advance()
	}
}

// RecordError 记录一次失败请求。
func (h *UsageHandler) RecordError() {
	h.advance()
	idx := h.bucketIdx.Load() % 60
	h.buckets[idx].errors.Add(1)
}

type minuteStats struct {
	qpm        int64
	qps        int64
	errors     int64
	avgLatency float64
	maxLatency int64
	minLatency int64
}

func (h *UsageHandler) computeStats() minuteStats {
	h.advance()
	var totalReq, totalErr, totalLat, maxLat int64
	minLat := int64(1<<63 - 1)
	// 前一秒的 bucket（当前秒刚被 advance 清零，没有数据）
	prevIdx := (h.bucketIdx.Load() - 1 + 60) % 60
	var lastSecReq int64

	for i := 0; i < 60; i++ {
		req := h.buckets[i].requests.Load()
		totalReq += req
		totalErr += h.buckets[i].errors.Load()
		totalLat += h.buckets[i].latencySum.Load()
		mx := h.buckets[i].latencyMax.Load()
		if mx > maxLat {
			maxLat = mx
		}
		mn := h.buckets[i].latencyMin.Load()
		if mn > 0 && mn < minLat {
			minLat = mn
		}
		if int64(i) == prevIdx {
			lastSecReq = req
		}
	}
	if minLat == int64(1<<63-1) {
		minLat = 0
	}
	var avg float64
	if totalReq > 0 {
		avg = float64(totalLat) / float64(totalReq)
	}
	return minuteStats{
		qpm:        totalReq,
		qps:        lastSecReq,
		errors:     totalErr,
		avgLatency: avg,
		maxLatency: maxLat,
		minLatency: minLat,
	}
}

// SetPlanRepo 注入套餐仓库（动态读取限额）。
func (h *UsageHandler) SetPlanRepo(r *PlanRepo) { h.planRepo = r }

// getUserLimits 从用户绑定的套餐动态获取限额，fallback 到默认值。
func (h *UsageHandler) getUserLimits(c *gin.Context) (dayLimit, monthLimit int64) {
	dayLimit, monthLimit = h.dayLimit, h.monthLimit
	if h.planRepo == nil {
		return
	}
	// 优先从 API Key record 读 planID
	if rec, exists := c.Get("auth.apikey_record"); exists {
		if keyRec, ok := rec.(*auth.APIKeyRecord); ok && keyRec != nil && keyRec.PlanID != "" {
			if p, err := h.planRepo.Get(c.Request.Context(), keyRec.PlanID); err == nil {
				return p.DailyLimit, p.MonthlyLimit
			}
		}
	}
	// 无 API Key 时尝试从 quota_rules 查
	userID, _ := h.authFromCtx(c)
	if userID != "" && h.pg != nil {
		var dl, ml int64
		err := h.pg.QueryRow(c.Request.Context(),
			`SELECT COALESCE(daily_limit,0), COALESCE(monthly_limit,0) FROM quota.quota_rules WHERE user_id = $1 LIMIT 1`, userID).Scan(&dl, &ml)
		if err == nil && dl > 0 {
			return dl, ml
		}
	}
	return
}

func (h *UsageHandler) authFromCtx(c *gin.Context) (userID, role string) {
	if r, ok := c.Get("auth.role"); ok {
		role, _ = r.(string)
	}
	// X-User-Id 是中间件在认证成功后 Set 的，客户端发送的伪造值已被清除
	userID = c.Request.Header.Get("X-User-Id")
	return
}

func (h *UsageHandler) isSuperAdmin(c *gin.Context) bool {
	_, role := h.authFromCtx(c)
	return role == "superadmin"
}

func (h *UsageHandler) realtime(c *gin.Context) {
	userID, _ := h.authFromCtx(c)
	isAdmin := h.isSuperAdmin(c)
	s := h.computeStats()
	now := localNow()
	date := localDate(now)
	yearMonth := localMonth(now)

	var dayUsed, monthUsed int64
	if h.pg != nil {
		ctx := c.Request.Context()
		if isAdmin {
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_daily WHERE date = $1::date`,
				date).Scan(&dayUsed)
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_monthly WHERE year_month = $1`,
				yearMonth).Scan(&monthUsed)
		} else if userID != "" {
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_daily WHERE user_id = $1 AND date = $2::date`,
				userID, date).Scan(&dayUsed)
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_monthly WHERE user_id = $1 AND year_month = $2`,
				userID, yearMonth).Scan(&monthUsed)
		}
	}

	var successRate float64
	if s.qpm > 0 {
		successRate = float64(s.qpm-s.errors) / float64(s.qpm)
	} else {
		successRate = 1.0
	}

	dl, ml := h.getUserLimits(c)
	OK(c, RealtimeMetrics{
		QPM:          s.qpm,
		QPS:          s.qps,
		AvgLatencyMs: s.avgLatency,
		MaxLatencyMs: s.maxLatency,
		MinLatencyMs: s.minLatency,
		TotalToday:   dayUsed,
		TotalMonth:   monthUsed,
		SuccessRate:  successRate,
		ErrorCount:   s.errors,
		DayUsed:      dayUsed,
		DayLimit:     dl,
		MonthUsed:    monthUsed,
		MonthLimit:   ml,
		Timestamp:    now.Format(time.RFC3339),
	})
}

func (h *UsageHandler) summary(c *gin.Context) {
	userID, _ := h.authFromCtx(c)
	isAdmin := h.isSuperAdmin(c)
	now := localNow()
	date := localDate(now)
	yearMonth := localMonth(now)

	var dayUsed, monthUsed int64
	if h.pg != nil {
		ctx := c.Request.Context()
		if isAdmin {
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_daily WHERE date = $1::date`,
				date).Scan(&dayUsed)
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_monthly WHERE year_month = $1`,
				yearMonth).Scan(&monthUsed)
		} else if userID != "" {
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_daily WHERE user_id = $1 AND date = $2::date`,
				userID, date).Scan(&dayUsed)
			_ = h.pg.QueryRow(ctx,
				`SELECT COALESCE(SUM(count),0) FROM quota.usage_aggregates_monthly WHERE user_id = $1 AND year_month = $2`,
				userID, yearMonth).Scan(&monthUsed)
		}
	}

	dl, ml := h.getUserLimits(c)
	OK(c, UsageSummary{
		DayUsed:    dayUsed,
		MonthUsed:  monthUsed,
		DayLimit:   dl,
		MonthLimit: ml,
		Date:       date,
		Month:      yearMonth,
	})
}

func (h *UsageHandler) history(c *gin.Context) {
	userID, _ := h.authFromCtx(c)
	isAdmin := h.isSuperAdmin(c)
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days <= 0 || days > 365 {
		days = 30
	}
	if h.pg != nil {
		h.historyFromPG(c, userID, isAdmin, days)
		return
	}
	OK(c, map[string]any{"days": []UsageDayRecord{}, "total": 0})
}

func (h *UsageHandler) historyFromPG(c *gin.Context, userID string, isAdmin bool, days int) {
	// 历史窗口的起点也按本地时区算："最近 7 天"对用户是 7 个日历天而非 7×24h。
	from := localDate(localNow().AddDate(0, 0, -days))
	ctx := c.Request.Context()

	var q string
	var args []any
	if isAdmin {
		q = `SELECT date::text, provider, count, weight_sum
			FROM quota.usage_aggregates_daily
			WHERE date >= $1::date ORDER BY date ASC`
		args = []any{from}
	} else {
		q = `SELECT date::text, provider, count, weight_sum
			FROM quota.usage_aggregates_daily
			WHERE user_id = $1 AND date >= $2::date ORDER BY date ASC`
		args = []any{userID, from}
	}

	rows, err := h.pg.Query(ctx, q, args...)
	if err != nil {
		OK(c, map[string]any{"days": []UsageDayRecord{}, "total": 0})
		return
	}
	defer rows.Close()
	var out []UsageDayRecord
	var total int64
	for rows.Next() {
		var r UsageDayRecord
		if err := rows.Scan(&r.Date, &r.Provider, &r.Count, &r.WeightSum); err != nil {
			continue
		}
		total += r.Count
		out = append(out, r)
	}
	if out == nil {
		out = []UsageDayRecord{}
	}
	OK(c, map[string]any{"days": out, "total": total})
}

func (h *UsageHandler) sumKeys(c *gin.Context, pattern string) int64 {
	ctx := c.Request.Context()
	var total int64
	var cursor uint64
	for {
		keys, next, err := h.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			break
		}
		for _, key := range keys {
			val, err := h.rdb.Get(ctx, key).Int64()
			if err == nil {
				total += val
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	_ = fmt.Sprintf("") // keep fmt import
	return total
}
