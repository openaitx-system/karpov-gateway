package observability

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics 是全栈共享的核心指标集合。
//
// 命名规范：<service>_<subject>_<unit>，与 Prometheus 官方推荐对齐。
// 所有指标都按 service / endpoint / status 维度切分；高基数维度（user_id 等）
// 不进 metric label（写入 OTEL trace span attribute 即可）。
type Metrics struct {
	HTTPRequests     *prometheus.CounterVec
	HTTPDuration     *prometheus.HistogramVec
	ProviderCalls    *prometheus.CounterVec
	PoolAcquires     *prometheus.CounterVec
	PoolReleases     *prometheus.CounterVec
	QuotaDecisions   *prometheus.CounterVec
	OrderTransitions *prometheus.CounterVec
}

var (
	globalMetrics     *Metrics
	globalMetricsOnce sync.Once
)

// Global 返回全局唯一 Metrics（懒注册到 default Registerer）。
//
// 提供 Global() 单例是为了避免在多个 cmd/* 二进制内重复手动 wire；
// 测试需要隔离的场景请用 NewMetrics(reg)。
func Global() *Metrics {
	globalMetricsOnce.Do(func() {
		globalMetrics = NewMetrics(prometheus.DefaultRegisterer)
	})
	return globalMetrics
}

// NewMetrics 注册一组新 metrics 到给定 Registerer（便于测试隔离）。
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total HTTP requests by service / method / path / status.",
		}, []string{"service", "method", "path", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_http_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service", "method", "path"}),
		ProviderCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_provider_calls_total",
			Help: "Total provider RPC calls by provider / method / outcome.",
		}, []string{"provider", "method", "outcome"}),
		PoolAcquires: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_pool_acquires_total",
			Help: "Total pool.Acquire calls by provider / outcome.",
		}, []string{"provider", "outcome"}),
		PoolReleases: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_pool_releases_total",
			Help: "Total pool.Release calls by provider / result.",
		}, []string{"provider", "result"}),
		QuotaDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_quota_decisions_total",
			Help: "Total quota CheckAndConsume decisions by provider / endpoint / decision.",
		}, []string{"provider", "endpoint", "decision"}),
		OrderTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_order_transitions_total",
			Help: "Total order state transitions by from / to.",
		}, []string{"from", "to"}),
	}
	if reg != nil {
		// 用 MustRegister 在重复注册时 panic（暴露 wiring 错误）；测试用 NewRegistry 自动隔离。
		reg.MustRegister(
			m.HTTPRequests, m.HTTPDuration, m.ProviderCalls,
			m.PoolAcquires, m.PoolReleases, m.QuotaDecisions, m.OrderTransitions,
		)
	}
	return m
}
