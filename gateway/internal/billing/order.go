// Package billing 实现订单 / 订阅 / 支付回调状态机。
//
// 状态图（Plan §8.4）：
//
//	PENDING -- pay_start --> PAYING
//	                              |--callback_ok-->  PAID --fulfill--> COMPLETED
//	                              |--callback_fail-> FAILED
//	                              `--timeout-->     EXPIRED
//	PENDING --user_cancel-->  CANCELED
//
// 当前包仅做内存版骨架（NewOrderRepo + Service）；PG 化在 M16 落地，
// 实现 OrderRepo 接口的 PG 适配器即可，业务代码零改动。
package billing

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/shopspring/decimal"
)

// OrderStatus 是订单状态枚举（与 PG 表 enum 对齐）。
type OrderStatus string

const (
	OrderPending   OrderStatus = "pending"
	OrderPaying    OrderStatus = "paying"
	OrderPaid      OrderStatus = "paid"
	OrderCompleted OrderStatus = "completed"
	OrderFailed    OrderStatus = "failed"
	OrderExpired   OrderStatus = "expired"
	OrderCanceled  OrderStatus = "canceled"
)

// 状态机事件。
const (
	EvtPayStart     = "pay_start"
	EvtCallbackOK   = "callback_ok"
	EvtCallbackFail = "callback_fail"
	EvtTimeout      = "timeout"
	EvtUserCancel   = "user_cancel"
	EvtFulfill      = "fulfill"
)

// 订单 Purpose 枚举：决定支付成功后的履约动作。
const (
	OrderPurposeSubscription = "subscription" // 默认：升级套餐
	OrderPurposeTopup        = "topup"        // 充值余额
)

// Order 是订单领域模型。
type Order struct {
	ID              string // uuid v7
	UserID          string
	PlanID          string
	Amount          decimal.Decimal // 已换算到 Currency 后的应付金额
	Currency        string          // ISO 4217 / 平台 token：实际结算币（如 LDC 渠道则为 "LDC"）
	Status          OrderStatus
	Purpose         string // OrderPurposeSubscription / OrderPurposeTopup；空值视为 subscription
	PaymentProvider string // "yipay" / "hupijiao" / "ldcpay" / ...
	ExternalOrderID string
	IdempotencyKey  string // UNIQUE，防重提
	CreatedAt       time.Time
	PaidAt          time.Time
	ExpiresAt       time.Time // 通常 = CreatedAt + 30min

	// ---- 跨币种换算快照（FX snapshot） ----
	// 用于审计与跨币履约（如 LDC 渠道支付 CNY 套餐）：
	//   - 三个字段同时为零值 ⇒ 未发生跨币换算，Amount/Currency 即为原始值
	//   - 否则记录"原币种、原金额、下单瞬间的换算比例"
	OriginalAmount   decimal.Decimal // 换算前金额（plan 原始定价）
	OriginalCurrency string          // 换算前币种
	FXRate           decimal.Decimal // 1 OriginalCurrency = FXRate Currency；下单瞬间快照
}

// 已知错误。
var (
	ErrInvalidTransition = errors.New("billing: invalid status transition")
	ErrOrderNotFound     = errors.New("billing: order not found")
	ErrDuplicateOrder    = errors.New("billing: duplicate idempotency_key")
)

// allowedTransitions 是显式声明的合法状态转移图。
//
// 故意不引入 looplab/fsm 整套对象图：本服务的 FSM 极简，map 表查询比建图开销低。
// 设计意图：所有 Transition 必须命中 (from, event) → to；未命中即 ErrInvalidTransition。
var allowedTransitions = map[OrderStatus]map[string]OrderStatus{
	OrderPending: {
		EvtPayStart:   OrderPaying,
		EvtUserCancel: OrderCanceled,
		EvtTimeout:    OrderExpired,
	},
	OrderPaying: {
		EvtCallbackOK:   OrderPaid,
		EvtCallbackFail: OrderFailed,
		EvtTimeout:      OrderExpired,
	},
	OrderPaid: {
		EvtFulfill: OrderCompleted,
	},
}

// NewOrderID 生成订单 UUID。
func NewOrderID() string {
	var b [16]byte
	_, _ = io.ReadFull(rand.Reader, b[:])
	// UUID v4 格式
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CanTransition 判断给定事件是否合法。
func CanTransition(from OrderStatus, event string) bool {
	row, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	_, ok = row[event]
	return ok
}

// nextStatus 返回事件触发后的目标状态。
func nextStatus(from OrderStatus, event string) (OrderStatus, error) {
	row, ok := allowedTransitions[from]
	if !ok {
		return "", ErrInvalidTransition
	}
	to, ok := row[event]
	if !ok {
		return "", ErrInvalidTransition
	}
	return to, nil
}

// NewOrder 构造一张新订单（status=Pending，ID=uuid.NewV7）。
//
// 调用方应负责 IdempotencyKey 的稳定性（如 sha256(user_id + plan_id + nonce)）。
func NewOrder(userID, planID string, amount decimal.Decimal, currency, idem string, ttl time.Duration) (*Order, error) {
	if userID == "" || planID == "" {
		return nil, errors.New("billing.NewOrder: empty user_id / plan_id")
	}
	if currency == "" {
		currency = "CNY"
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	id := NewOrderID()
	now := time.Now().UTC()
	return &Order{
		ID:             id,
		UserID:         userID,
		PlanID:         planID,
		Amount:         amount,
		Currency:       currency,
		Status:         OrderPending,
		IdempotencyKey: idem,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}, nil
}
