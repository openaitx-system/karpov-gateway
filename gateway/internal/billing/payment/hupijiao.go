package payment

import (
	"context"
	"errors"
	"net/url"

	"github.com/shopspring/decimal"
)

// HupijiaoConfig 是虎皮椒商户配置。
type HupijiaoConfig struct {
	BaseURL string // 网关 base，如 "https://api.xunhupay.com/payment/do.html"
	AppID   string // appid
	Key     string // appsecret（签名用）
	WapName string // wap_name 应用名（虎皮椒要求字段）
}

// Hupijiao 实现 Provider。
type Hupijiao struct {
	cfg HupijiaoConfig
}

// NewHupijiao 构造虎皮椒适配器。
func NewHupijiao(cfg HupijiaoConfig) *Hupijiao {
	return &Hupijiao{cfg: cfg}
}

// Name 实现 Provider.Name。
func (h *Hupijiao) Name() string { return "hupijiao" }

// CreatePayment 构造支付 URL（虎皮椒返回的是 JSON，但适配器内对外封装一致接口）。
//
// 字段（虎皮椒 v1.1 文档）：
//
//	version       "1.1"
//	appid         应用 ID
//	trade_order_id 商户订单号
//	total_fee      金额（元）
//	title          订单标题
//	notify_url     异步回调
//	return_url     同步跳转
//	wap_name       应用名
//	hash           签名
//
// 实际生产中 CreatePayment 应当 POST 到 BaseURL 拿到 url 字段；MVP 阶段
// 只构造同步参数串，由调用方决定是 POST 还是把签名直接交给前端 SDK。
func (h *Hupijiao) CreatePayment(_ context.Context, req CreateRequest) (*CreateResponse, error) {
	if req.OrderID == "" || req.Amount.IsZero() {
		return nil, ErrMissingField
	}
	params := map[string]string{
		"version":        "1.1",
		"appid":          h.cfg.AppID,
		"trade_order_id": req.OrderID,
		"total_fee":      req.Amount.StringFixed(2),
		"title":          req.Subject,
		"notify_url":     req.NotifyURL,
		"return_url":     req.ReturnURL,
		"wap_name":       h.cfg.WapName,
	}
	for k, v := range req.ExtraQuery {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	params["hash"] = SignMD5Hupijiao(params, h.cfg.Key)

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return &CreateResponse{
		PayURL: h.cfg.BaseURL + "?" + q.Encode(),
		Raw:    params,
	}, nil
}

// VerifyCallback 验证虎皮椒异步回调签名。
//
// ACK：虎皮椒要求返回 JSON `{"return_code": "SUCCESS"}`。
func (h *Hupijiao) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	if len(req.Params) == 0 {
		return nil, ErrMissingField
	}
	gotSign := req.Params["hash"]
	if gotSign == "" {
		return nil, ErrMissingField
	}
	wantSign := SignMD5Hupijiao(req.Params, h.cfg.Key)
	if !VerifyMD5(wantSign, gotSign) {
		return nil, ErrInvalidSign
	}
	out := &CallbackResult{
		OrderID:         req.Params["trade_order_id"],
		ExternalTxnID:   req.Params["transaction_id"],
		GatewayResponse: `{"return_code":"SUCCESS"}`,
	}
	if amt, err := decimal.NewFromString(req.Params["total_fee"]); err == nil {
		out.Amount = amt
	} else {
		return nil, errors.New("payment.hupijiao: invalid total_fee: " + req.Params["total_fee"])
	}
	out.Success = req.Params["status"] == "OD"
	return out, nil
}
