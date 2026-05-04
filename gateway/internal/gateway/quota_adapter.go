package gateway

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	quotav1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/quota/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/quota"
)

// QuotaGRPCService 把 *quota.Service 适配为 gRPC QuotaService 实现。
//
// 设计要点：
//   - quota.Service 只做 Redis 计数，不知道 endpoint_weight / day_limit / month_limit
//     等业务规则；本 adapter 持有 RuleStore 接口，从中拿规则后传给 Service
//   - GetUsage：尽力查当前窗口；不返回历史聚合（usage_aggregates_daily/monthly
//     由 worker 后续接入）
//   - UpdateRule：管理面，写到内存 RuleStore；持久化（PG）由实现 RuleStore 的
//     PG 适配器完成
type QuotaGRPCService struct {
	quotav1.UnimplementedQuotaServiceServer
	svc   *quota.Service
	rules RuleStore
}

// RuleStore 是配额规则的存储抽象（PG 实现可后续接入）。
//
// Resolve 从用户/provider/endpoint 推出本次 Check 所需规则；UpdateRule 修改
// 规则并立刻生效（管理员热更新）。
type RuleStore interface {
	Resolve(ctx context.Context, userID, providerName, endpoint string) (quota.Rules, error)
	UpdateRule(ctx context.Context, userID, providerName string, dayLimit, monthLimit int64, softPct int) error
}

// NewQuotaGRPCService 构造 QuotaGRPCService。
func NewQuotaGRPCService(svc *quota.Service, rules RuleStore) *QuotaGRPCService {
	return &QuotaGRPCService{svc: svc, rules: rules}
}

// CheckAndConsume 实现 quotav1.QuotaServiceServer.CheckAndConsume。
func (s *QuotaGRPCService) CheckAndConsume(ctx context.Context, req *quotav1.CheckAndConsumeRequest) (*quotav1.Decision, error) {
	if req.GetUserId() == "" || req.GetProvider() == "" || req.GetEndpoint() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id / provider / endpoint required")
	}
	r, err := s.rules.Resolve(ctx, req.GetUserId(), req.GetProvider(), req.GetEndpoint())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if w := req.GetWeightOverride(); w > 0 {
		r.Weight = int(w)
	}
	res, err := s.svc.CheckAndConsume(ctx, r)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return decisionToProto(res, r), nil
}

// Refund 实现 quotav1.QuotaServiceServer.Refund。
func (s *QuotaGRPCService) Refund(ctx context.Context, req *quotav1.RefundRequest) (*emptypb.Empty, error) {
	if req.GetUserId() == "" || req.GetEndpoint() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id / endpoint required")
	}
	r, err := s.rules.Resolve(ctx, req.GetUserId(), req.GetProvider(), req.GetEndpoint())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if w := req.GetWeight(); w > 0 {
		r.Weight = int(w)
	}
	if _, err := s.svc.Refund(ctx, r); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// GetUsage 实现 quotav1.QuotaServiceServer.GetUsage。
//
// 当前仅返回当前日/月窗口的 count；历史聚合（按 from/to 分日累加）留待 worker
// 把 usage_events → usage_aggregates 写好后再补强。
func (s *QuotaGRPCService) GetUsage(ctx context.Context, req *quotav1.GetUsageRequest) (*quotav1.UsageReport, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id required")
	}
	r, err := s.rules.Resolve(ctx, req.GetUserId(), req.GetProvider(), "")
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	res, err := s.svc.GetUsage(ctx, r)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	now := time.Now().UTC()
	return &quotav1.UsageReport{
		Days: []*quotav1.UsageDay{{
			Date:     now.Format("2006-01-02"),
			Provider: req.GetProvider(),
			Count:    res.DayUsed,
			Weight:   res.DayUsed,
		}},
		TotalCount:  res.MonthUsed,
		TotalWeight: res.MonthUsed,
	}, nil
}

// UpdateRule 实现 quotav1.QuotaServiceServer.UpdateRule。
func (s *QuotaGRPCService) UpdateRule(ctx context.Context, req *quotav1.UpdateRuleRequest) (*emptypb.Empty, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id required")
	}
	if err := s.rules.UpdateRule(ctx, req.GetUserId(), req.GetProvider(),
		req.GetMonthlyLimit(), req.GetDailyLimit(), int(req.GetSoftLimitPct()),
	); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// decisionToProto 把 quota.CheckResult 翻成 wire 类型。
func decisionToProto(res *quota.CheckResult, r quota.Rules) *quotav1.Decision {
	out := &quotav1.Decision{
		RetryAfterMs: int32(res.RetryAfterMs),
		UsedToday:    res.DayUsed,
		UsedMonth:    res.MonthUsed,
		LimitToday:   r.DayLimit,
		LimitMonth:   r.MonthLimit,
	}
	switch res.Decision {
	case quota.DecisionAllow:
		out.Result = quotav1.Result_RESULT_ALLOW
	case quota.DecisionSoftLimit:
		out.Result = quotav1.Result_RESULT_SOFT_LIMIT
		out.Reason = "soft_limit_pct exceeded"
	case quota.DecisionHardLimit:
		out.Result = quotav1.Result_RESULT_HARD_LIMIT
		out.Reason = "day or month limit exceeded"
	default:
		out.Result = quotav1.Result_RESULT_UNSPECIFIED
	}
	return out
}

// MemRuleStore 是 RuleStore 的进程内实现。
//
// 默认规则：daily=1000, monthly=30000, soft=80%（与 free plan 一致；开发起步用）。
// 通过 UpdateRule 可热更新；生产应替换为 PG 适配器。
type MemRuleStore struct {
	mu      sync.RWMutex
	rules   map[string]quota.Rules // key = userID + "|" + provider
	weights map[string]int         // endpoint → weight，全局默认
	defs    quota.Rules            // 默认规则
}

// NewMemRuleStore 构造默认规则容器。
func NewMemRuleStore() *MemRuleStore {
	return &MemRuleStore{
		rules: make(map[string]quota.Rules),
		weights: map[string]int{
			"GetSong":     1,
			"SearchSongs": 1,
			"GetSongURL":  3, // 与 Plan §8.3 example 一致
			"GetLyric":    1,
			"GetAlbum":    1,
			"GetSinger":   1,
			"GetSongList": 1,
		},
		defs: quota.Rules{
			DayLimit:     1000,
			MonthLimit:   30000,
			SoftLimitPct: 80,
		},
	}
}

func (m *MemRuleStore) ruleKey(uid, prov string) string { return uid + "|" + prov }

// Resolve 实现 RuleStore.Resolve。
func (m *MemRuleStore) Resolve(_ context.Context, userID, providerName, endpoint string) (quota.Rules, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	base := m.defs
	if r, ok := m.rules[m.ruleKey(userID, providerName)]; ok {
		base = r
	}
	base.UserID = userID
	base.Provider = providerName
	base.Endpoint = endpoint
	if endpoint != "" {
		if w, ok := m.weights[endpoint]; ok && base.Weight == 0 {
			base.Weight = w
		}
	}
	return base, nil
}

// UpdateRule 实现 RuleStore.UpdateRule。
func (m *MemRuleStore) UpdateRule(_ context.Context, userID, providerName string, dayLimit, monthLimit int64, softPct int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rules[m.ruleKey(userID, providerName)]
	if r.UserID == "" {
		r = m.defs
	}
	r.UserID = userID
	r.Provider = providerName
	if dayLimit > 0 {
		r.DayLimit = dayLimit
	}
	if monthLimit > 0 {
		r.MonthLimit = monthLimit
	}
	if softPct > 0 {
		r.SoftLimitPct = softPct
	}
	m.rules[m.ruleKey(userID, providerName)] = r
	return nil
}
