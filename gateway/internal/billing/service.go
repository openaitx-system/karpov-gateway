package billing

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// OrderRepo 是订单存储抽象（PG 化适配器实现同接口）。
type OrderRepo interface {
	Create(ctx context.Context, o *Order) error
	Get(ctx context.Context, id string) (*Order, error)
	GetByIdempotency(ctx context.Context, idem string) (*Order, error)
	Update(ctx context.Context, o *Order) error
	ListExpired(ctx context.Context, now time.Time) ([]*Order, error)
	ListByUser(ctx context.Context, userID string) ([]*Order, error)
	CreateRefund(ctx context.Context, orderID string, amount decimal.Decimal, reason, refundType, operatorID string) error
}

// Service 是 Billing 业务入口。
type Service struct {
	repo  OrderRepo
	clock func() time.Time
}

// Options 控制 Service 构造。
type Options struct {
	Clock func() time.Time
}

// NewService 构造 Billing。
func NewService(repo OrderRepo, opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repo, clock: opts.Clock}
}

// GetOrder 按 ID 查订单（导出，供 gRPC adapter 用）。
func (s *Service) GetOrder(ctx context.Context, id string) (*Order, error) {
	return s.repo.Get(ctx, id)
}

// CreateOrder 创建订单（带 idempotency 检查）。
//
// 同 idempotency_key 重复创建直接返回原订单（与 Plan §8.5 幂等要求一致）。
func (s *Service) CreateOrder(ctx context.Context, o *Order) (*Order, error) {
	if o.IdempotencyKey != "" {
		if exist, err := s.repo.GetByIdempotency(ctx, o.IdempotencyKey); err == nil && exist != nil {
			return exist, nil
		}
	}
	if err := s.repo.Create(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

// Transition 推进订单状态。
//
// 调用方语义：
//   - 用户点击支付：Transition(orderID, EvtPayStart)
//   - 收到支付网关回调：Transition(orderID, EvtCallbackOK / EvtCallbackFail)
//   - 履约完成：Transition(orderID, EvtFulfill)
//   - 超时扫描：Transition(orderID, EvtTimeout)
//
// 状态机规则在 order.go 中定义；不允许的转移返回 ErrInvalidTransition。
func (s *Service) Transition(ctx context.Context, orderID, event string) (*Order, error) {
	o, err := s.repo.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	to, err := nextStatus(o.Status, event)
	if err != nil {
		return nil, err
	}
	o.Status = to
	if to == OrderPaid {
		o.PaidAt = s.clock()
	}
	if err := s.repo.Update(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

// SweepExpired 把所有 ExpiresAt 已过且仍在 Pending/Paying 的订单转为 Expired。
//
// 该方法由 worker 周期性调用（每分钟），与 Plan §8.4 timeout 路径对齐。
func (s *Service) SweepExpired(ctx context.Context) (int, error) {
	now := s.clock()
	orders, err := s.repo.ListExpired(ctx, now)
	if err != nil {
		return 0, err
	}
	swept := 0
	for _, o := range orders {
		if o.Status != OrderPending && o.Status != OrderPaying {
			continue
		}
		if !CanTransition(o.Status, EvtTimeout) {
			continue
		}
		to, _ := nextStatus(o.Status, EvtTimeout)
		o.Status = to
		if err := s.repo.Update(ctx, o); err != nil {
			continue
		}
		swept++
	}
	return swept, nil
}

// ListByUser 按用户 ID 查订单。
func (s *Service) ListByUser(ctx context.Context, userID string) ([]*Order, error) {
	return s.repo.ListByUser(ctx, userID)
}

// CreateRefund 创建退款并直接标记完成（触发 DB trigger 降级）。
func (s *Service) CreateRefund(ctx context.Context, orderID string, amount decimal.Decimal, reason, refundType, operatorID string) error {
	return s.repo.CreateRefund(ctx, orderID, amount, reason, refundType, operatorID)
}

// MemRepo 是 in-memory 仓储；测试与 e2e 启动期用。
type MemRepo struct {
	mu    sync.RWMutex
	store map[string]*Order
	idemx map[string]string // idem_key -> order_id
}

// NewMemRepo 构造空内存仓库。
func NewMemRepo() *MemRepo {
	return &MemRepo{store: map[string]*Order{}, idemx: map[string]string{}}
}

// Create 实现 OrderRepo.Create；同 idem_key 重复返回 ErrDuplicateOrder。
func (r *MemRepo) Create(_ context.Context, o *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if o.IdempotencyKey != "" {
		if _, dup := r.idemx[o.IdempotencyKey]; dup {
			return ErrDuplicateOrder
		}
		r.idemx[o.IdempotencyKey] = o.ID
	}
	r.store[o.ID] = o
	return nil
}

// Get 实现 OrderRepo.Get。
func (r *MemRepo) Get(_ context.Context, id string) (*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.store[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	cp := *o // 拷贝防外部 mutate
	return &cp, nil
}

// GetByIdempotency 实现 OrderRepo.GetByIdempotency。
func (r *MemRepo) GetByIdempotency(_ context.Context, idem string) (*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.idemx[idem]
	if !ok {
		return nil, ErrOrderNotFound
	}
	o := r.store[id]
	cp := *o
	return &cp, nil
}

// Update 实现 OrderRepo.Update。
func (r *MemRepo) Update(_ context.Context, o *Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.store[o.ID]; !ok {
		return ErrOrderNotFound
	}
	cp := *o
	r.store[o.ID] = &cp
	return nil
}

// ListExpired 返回 ExpiresAt < now 的订单。
func (r *MemRepo) ListExpired(_ context.Context, now time.Time) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Order
	for _, o := range r.store {
		if !o.ExpiresAt.IsZero() && o.ExpiresAt.Before(now) {
			cp := *o
			out = append(out, &cp)
		}
	}
	return out, nil
}

// CreateRefund 内存版：直接标记订单为 refunded。
func (r *MemRepo) CreateRefund(_ context.Context, orderID string, _ decimal.Decimal, _, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.store[orderID]
	if !ok {
		return ErrOrderNotFound
	}
	o.Status = "refunded"
	return nil
}

// ListByUser 返回用户的所有订单。
func (r *MemRepo) ListByUser(_ context.Context, userID string) ([]*Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Order
	for _, o := range r.store {
		if o.UserID == userID {
			cp := *o
			out = append(out, &cp)
		}
	}
	return out, nil
}
