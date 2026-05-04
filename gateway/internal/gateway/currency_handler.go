package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
	"github.com/MiChongs/QQMusicApi/gateway/internal/billing"
)

// CurrencyHandler 是全局货币 + 汇率配置的 admin REST 入口。
//
//	GET  /v1/admin/settings/currency  当前配置（基准币 + 汇率列表）
//	PUT  /v1/admin/settings/currency  整体替换；body 见 CurrencyConfigInput
type CurrencyHandler struct {
	store *billing.CurrencyStore
}

func NewCurrencyHandler(store *billing.CurrencyStore) *CurrencyHandler {
	return &CurrencyHandler{store: store}
}

func (h *CurrencyHandler) Mount(r *gin.Engine) {
	r.GET("/v1/admin/settings/currency", h.get)
	r.PUT("/v1/admin/settings/currency", h.put)
}

// currencyResponseRate 是单条汇率的响应体（rate 用 string 序列化避免 float 精度坑）。
type currencyResponseRate struct {
	Code string `json:"code"`
	Rate string `json:"rate"`
}

type currencyResponse struct {
	DefaultCurrency string                 `json:"defaultCurrency"`
	Rates           []currencyResponseRate `json:"rates"`
	UpdatedAt       string                 `json:"updatedAt,omitempty"`
}

func toResponse(cfg billing.CurrencyConfig) currencyResponse {
	out := currencyResponse{
		DefaultCurrency: cfg.DefaultCurrency,
	}
	if !cfg.UpdatedAt.IsZero() {
		out.UpdatedAt = cfg.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	// 按 code 字典序导出，让前端列表稳定
	codes := make([]string, 0, len(cfg.Rates))
	for k := range cfg.Rates {
		codes = append(codes, k)
	}
	sortStringsInPlace(codes)
	for _, c := range codes {
		out.Rates = append(out.Rates, currencyResponseRate{
			Code: c,
			Rate: cfg.Rates[c].String(),
		})
	}
	if out.Rates == nil {
		out.Rates = []currencyResponseRate{}
	}
	return out
}

// sortStringsInPlace 局部使用，避免引入 sort 包污染（与文件其他处保持简洁）。
func sortStringsInPlace(a []string) {
	// 简单的插入排序，列表通常 < 50 元素
	for i := 1; i < len(a); i++ {
		j := i
		for j > 0 && a[j-1] > a[j] {
			a[j-1], a[j] = a[j], a[j-1]
			j--
		}
	}
}

func (h *CurrencyHandler) get(c *gin.Context) {
	cfg := h.store.Get()
	OK(c, map[string]any{"config": toResponse(cfg)})
}

func (h *CurrencyHandler) put(c *gin.Context) {
	if role, _ := c.Get("auth.role"); role != auth.RoleSuperAdmin {
		Fail(c, http.StatusForbidden, CodeForbidden, "superadmin role required")
		return
	}
	var body billing.CurrencyConfigInput
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body: "+err.Error())
		return
	}
	cfg, err := h.store.Update(body)
	if err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, err.Error())
		return
	}
	OK(c, map[string]any{"config": toResponse(cfg)})
}

