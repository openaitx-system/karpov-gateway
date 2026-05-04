package payment

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// LDCConfig 是 Linux Credit (linux.do) 官方 LDC 支付协议的商户配置。
//
// 在 LDC 控制台创建应用时：
//   - ClientID / ClientSecret：控制台直接展示
//   - MerchantPrivateKey：商户本地生成密钥对，把 *公钥* 上传到 LDC 控制台，
//     私钥留在我们这边参与请求签名
//   - PlatformPublicKey：从 LDC 控制台下载的"平台公钥"，用于回调验签。
//     若官方现阶段未提供平台公钥下载入口，可暂时留空 ——
//     此时回调验签会被跳过并打印 WARN 日志（详见 VerifyCallback）。
type LDCConfig struct {
	BaseURL            string // 网关 base，默认 "https://credit.linux.do/epay"
	ClientID           string
	ClientSecret       string
	MerchantPrivateKey ed25519.PrivateKey // 已解析为 64 字节
	PlatformPublicKey  ed25519.PublicKey  // 已解析为 32 字节；可能为 nil
	HTTPClient         *http.Client       // 可选；不传走 default
}

// LDC 实现 Provider。Name 固定 "ldcpay" 与文档 §1.4 type 字段对齐。
type LDC struct {
	cfg LDCConfig
}

// NewLDC 构造适配器。会做最小校验；私钥必填，平台公钥可空。
func NewLDC(cfg LDCConfig) (*LDC, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("payment.ldc: client_id / client_secret required")
	}
	if len(cfg.MerchantPrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("payment.ldc: merchant private key must be %d bytes, got %d",
			ed25519.PrivateKeySize, len(cfg.MerchantPrivateKey))
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://credit.linux.do/epay"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 15 * time.Second,
			// 关键：不让 HTTP 客户端自动 follow 302。LDC 成功响应是
			//   302 Location: https://credit.linux.do/paying?order_no=...
			// 我们要把这个 Location 当作 PayURL 返回给前端，让浏览器自己跳。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &LDC{cfg: cfg}, nil
}

// Name 实现 Provider.Name。
func (l *LDC) Name() string { return "ldcpay" }

// CreatePayment 实现 LDC 文档 §1.4：POST /pay/submit.php，捕获 302 Location 作为 PayURL。
func (l *LDC) CreatePayment(ctx context.Context, req CreateRequest) (*CreateResponse, error) {
	if req.OrderID == "" || req.Amount.IsZero() {
		return nil, ErrMissingField
	}

	params := map[string]string{
		"client_id":    l.cfg.ClientID,
		"type":         "ldcpay",
		"out_trade_no": req.OrderID,
		"money":        req.Amount.StringFixed(2),
		"order_name":   req.Subject,
	}
	if req.NotifyURL != "" {
		params["notify_url"] = req.NotifyURL
	}
	if req.ReturnURL != "" {
		params["return_url"] = req.ReturnURL
	}
	for k, v := range req.ExtraQuery {
		if len(v) > 0 && v[0] != "" {
			params[k] = v[0]
		}
	}
	params["sign"] = SignEd25519LDC(params, l.cfg.ClientSecret, l.cfg.MerchantPrivateKey)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.cfg.BaseURL+"/pay/submit.php", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := l.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: post submit: %w", err)
	}
	defer resp.Body.Close()

	// 成功路径：302 跳转到认证页面
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return nil, errors.New("payment.ldc: 302 without Location header")
		}
		return &CreateResponse{
			PayURL: loc,
			Raw:    params,
		}, nil
	}

	// 失败路径：JSON {"error_msg":"...", "data":null}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	if resp.StatusCode == http.StatusOK {
		var fail struct {
			ErrorMsg string `json:"error_msg"`
		}
		if json.Unmarshal(body, &fail) == nil && fail.ErrorMsg != "" {
			return nil, fmt.Errorf("payment.ldc: gateway rejected: %s", fail.ErrorMsg)
		}
	}
	return nil, fmt.Errorf("payment.ldc: unexpected response %d: %s",
		resp.StatusCode, truncate(string(body), 200))
}

// VerifyCallback 验证 LDC 异步通知（文档 §3.3，HTTP GET，字段含 sign）。
//
// LDC 文档没明确指出 ldcpay 协议下回调使用什么签名方式，§3.3 的描述同时服务
// epay 兼容协议和 ldcpay 原生协议。本实现"自适应"识别签名格式：
//
//   - 32 位 hex（MD5）：与 epay 兼容协议一致，使用 client_secret 直接验
//     （payload + secret → md5）。这是 LDC 默认实际使用的方式。
//   - 其它（典型 88 字符 base64）：按 Ed25519 处理，需要 PlatformPublicKey 才能验。
//     如未配置，**直接拒签**（比"静默放行"更安全），同一行 WARN 给出可定位信息。
//
// ACK：必须返回 "success"（大小写不敏感），否则 LDC 会重试 5 次。
func (l *LDC) VerifyCallback(_ context.Context, req CallbackRequest) (*CallbackResult, error) {
	if len(req.Params) == 0 {
		return nil, ErrMissingField
	}
	gotSign := req.Params["sign"]
	if gotSign == "" {
		return nil, ErrMissingField
	}

	if isMD5Hex32(gotSign) {
		// MD5 路径：复用 epay 协议同款拼接（payload + client_secret）。
		wantSign := SignMD5Yipay(req.Params, l.cfg.ClientSecret)
		if !VerifyMD5(wantSign, gotSign) {
			return nil, ErrInvalidSign
		}
	} else {
		// Ed25519 路径：必须有平台公钥才能验。
		if len(l.cfg.PlatformPublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf(
				"payment.ldc: callback signed with Ed25519 but platform public key not configured "+
					"(sign_len=%d). 请在管理面板填入 LDC 平台公钥后重启网关，或确认 LDC 实际是否使用 MD5 签回调",
				len(gotSign))
		}
		if !VerifyEd25519LDC(req.Params, l.cfg.ClientSecret, l.cfg.PlatformPublicKey, gotSign) {
			return nil, ErrInvalidSign
		}
	}

	// 校验关键字段
	tradeNo := req.Params["trade_no"]
	outTradeNo := req.Params["out_trade_no"]
	moneyStr := req.Params["money"]
	if outTradeNo == "" || moneyStr == "" {
		return nil, ErrMissingField
	}
	amt, err := decimal.NewFromString(moneyStr)
	if err != nil {
		return nil, fmt.Errorf("payment.ldc: invalid money: %s", moneyStr)
	}

	out := &CallbackResult{
		OrderID:         outTradeNo,
		ExternalTxnID:   tradeNo,
		Amount:          amt,
		Success:         req.Params["trade_status"] == "TRADE_SUCCESS",
		GatewayResponse: "success",
	}
	return out, nil
}

// isMD5Hex32 判断字符串是否为 32 位小写/大写 hex（典型 MD5 摘要长度）。
func isMD5Hex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
