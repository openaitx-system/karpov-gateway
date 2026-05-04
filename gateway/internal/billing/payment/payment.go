// Package payment 是支付网关适配器层。
//
// 接口设计原则（Plan §4.5 + §8.5）：
//   - 不引入第三方 SDK（易支付 / 虎皮椒均无可信 Go SDK），全部自实现 HTTP + 签名
//   - 五步回调安全校验：(1) IP 白名单 (2) 签名 (3) 订单存在且 status=PAYING
//     (4) 金额匹配 (5) external_txn_id 幂等
//   - 适配器只做 (2)+(4)+(5) 中签名/金额/参数解析；IP 白名单与状态校验由
//     billing.Service 统一处理（避免每家 provider 重复实现）。
package payment

import (
	"context"
	"errors"
	"net/url"

	"github.com/shopspring/decimal"
)

// CreateRequest 是创建支付订单的输入。
type CreateRequest struct {
	OrderID    string          // 内部订单号（=billing.Order.ID）
	Amount     decimal.Decimal // 金额（非分；适配器内部按需 *100）
	Currency   string          // ISO 4217
	Subject    string          // 订单标题
	NotifyURL  string          // 异步回调 URL
	ReturnURL  string          // 同步跳转 URL
	UserIP     string          // 用户 IP（部分网关风控字段）
	ExtraQuery url.Values      // 网关特有的扩展字段
}

// CreateResponse 是创建支付订单的输出。
type CreateResponse struct {
	PayURL          string // 跳转支付的完整 URL（用户浏览器打开）
	ExternalOrderID string // 网关侧订单号（如易支付的 trade_no）
	Raw             map[string]string
}

// CallbackRequest 是网关回调的输入（解析后的参数 + 原始 body）。
type CallbackRequest struct {
	Params  map[string]string // 解析后的回调字段
	RawBody []byte            // 原始 body，用于审计 / 签名重算
	IP      string            // 网关源 IP
}

// CallbackResult 是回调校验结果。
type CallbackResult struct {
	OrderID         string          // 内部订单号
	ExternalTxnID   string          // 网关侧交易号
	Amount          decimal.Decimal // 实付金额
	Currency        string
	Success         bool   // 网关业务状态：是否支付成功
	GatewayResponse string // 给网关的 ACK 响应（必须按指定格式回写，否则会重复回调）
}

// Provider 是各家支付网关必须实现的统一接口。
type Provider interface {
	Name() string
	CreatePayment(ctx context.Context, req CreateRequest) (*CreateResponse, error)
	VerifyCallback(ctx context.Context, req CallbackRequest) (*CallbackResult, error)
}

// 已知错误。
var (
	ErrInvalidSign     = errors.New("payment: invalid sign")
	ErrAmountMismatch  = errors.New("payment: amount mismatch")
	ErrUnknownProvider = errors.New("payment: unknown provider")
	ErrMissingField    = errors.New("payment: missing required field")
)

// Registry 把 provider 名映射到 Provider 实例。
type Registry struct {
	store map[string]Provider
}

// NewRegistry 构造空 Registry。
func NewRegistry() *Registry {
	return &Registry{store: map[string]Provider{}}
}

// Register 注册 provider。
func (r *Registry) Register(p Provider) error {
	if p == nil || p.Name() == "" {
		return errors.New("payment.Registry: nil or empty provider")
	}
	r.store[p.Name()] = p
	return nil
}

// Get 按名查找。
func (r *Registry) Get(name string) (Provider, error) {
	if p, ok := r.store[name]; ok {
		return p, nil
	}
	return nil, ErrUnknownProvider
}
