package payment

import (
	"context"

	"github.com/shopspring/decimal"
)

// Mock 是开发用支付适配器：CreatePayment 永远成功并返回内部模拟 PayURL；
// VerifyCallback 永远验签通过，回调 amount = 入参 amount。
//
// 不要在生产环境注册本 provider；测试 / 烟测专用。
type Mock struct{}

// NewMock 构造 Mock provider。
func NewMock() *Mock { return &Mock{} }

// Name 返回 "mock"。
func (m *Mock) Name() string { return "mock" }

// CreatePayment 永远成功；PayURL = "mock://pay/<order_id>"。
func (m *Mock) CreatePayment(_ context.Context, req CreateRequest) (*CreateResponse, error) {
	return &CreateResponse{
		PayURL:          "mock://pay/" + req.OrderID,
		ExternalOrderID: "mock-" + req.OrderID,
		Raw:             map[string]string{"provider": "mock", "order_id": req.OrderID},
	}, nil
}

// VerifyCallback 永远验签通过；从 Params 读 order_id / amount，缺则返回错误。
func (m *Mock) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	orderID := req.Params["order_id"]
	if orderID == "" {
		return nil, ErrMissingField
	}
	amount, _ := decimal.NewFromString(req.Params["amount"])
	return &CallbackResult{
		OrderID:         orderID,
		ExternalTxnID:   "mock-txn-" + orderID,
		Amount:          amount,
		Currency:        "CNY",
		Success:         true,
		GatewayResponse: "ok",
	}, nil
}
