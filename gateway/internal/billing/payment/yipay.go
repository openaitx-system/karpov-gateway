package payment

import (
	"context"
	"errors"
	"net/url"

	"github.com/shopspring/decimal"
)

// YipayConfig 是易支付商户配置。
type YipayConfig struct {
	BaseURL     string // 网关 base，如 "https://pay.example.com"
	MerchantID  string // pid
	Key         string // 商户密钥（用于签名）
	PaymentType string // "alipay" / "wxpay" / "qqpay"，默认 alipay
}

// Yipay 实现 Provider。
type Yipay struct {
	cfg YipayConfig
}

// NewYipay 构造易支付适配器。
func NewYipay(cfg YipayConfig) *Yipay {
	if cfg.PaymentType == "" {
		cfg.PaymentType = "alipay"
	}
	return &Yipay{cfg: cfg}
}

// Name 实现 Provider.Name。
func (y *Yipay) Name() string { return "yipay" }

// CreatePayment 构造跳转支付的 URL（GET 方式提交）。
//
// 字段对照（易支付通用文档）：
//
//	pid          商户 ID
//	type         支付方式
//	out_trade_no 商户订单号
//	notify_url   异步回调
//	return_url   同步跳转
//	name         商品名称
//	money        金额（精确到分以下两位）
//	sign         MD5 签名
//	sign_type    "MD5"
func (y *Yipay) CreatePayment(_ context.Context, req CreateRequest) (*CreateResponse, error) {
	if req.OrderID == "" || req.Amount.IsZero() {
		return nil, ErrMissingField
	}
	params := map[string]string{
		"pid":          y.cfg.MerchantID,
		"type":         y.cfg.PaymentType,
		"out_trade_no": req.OrderID,
		"notify_url":   req.NotifyURL,
		"return_url":   req.ReturnURL,
		"name":         req.Subject,
		"money":        req.Amount.StringFixed(2),
	}
	for k, v := range req.ExtraQuery {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	params["sign"] = SignMD5Yipay(params, y.cfg.Key)
	params["sign_type"] = "MD5"

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	pay := y.cfg.BaseURL + "/submit.php?" + q.Encode()
	return &CreateResponse{
		PayURL: pay,
		Raw:    params,
	}, nil
}

// VerifyCallback 验证易支付异步通知 (notify) 签名。
//
// 五步校验（IP / 状态 / 幂等由 billing.Service 完成）的第 (2) 步在这里：
//   - 重算 sign 与 params["sign"] 比较（constant-time）
//   - 解析 trade_status；只有 "TRADE_SUCCESS" 视为支付成功
//   - 提取 out_trade_no / trade_no / money
//
// ACK：成功必须回 "success"（注意是字符串字面量），否则网关会重复回调。
func (y *Yipay) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	if len(req.Params) == 0 {
		return nil, ErrMissingField
	}
	gotSign := req.Params["sign"]
	if gotSign == "" {
		return nil, ErrMissingField
	}
	wantSign := SignMD5Yipay(req.Params, y.cfg.Key)
	if !VerifyMD5(wantSign, gotSign) {
		return nil, ErrInvalidSign
	}
	out := &CallbackResult{
		OrderID:         req.Params["out_trade_no"],
		ExternalTxnID:   req.Params["trade_no"],
		GatewayResponse: "success",
	}
	if amt, err := decimal.NewFromString(req.Params["money"]); err == nil {
		out.Amount = amt
	} else {
		return nil, errors.New("payment.yipay: invalid money: " + req.Params["money"])
	}
	out.Success = req.Params["trade_status"] == "TRADE_SUCCESS"
	return out, nil
}
