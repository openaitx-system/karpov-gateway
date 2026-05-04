package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// CurrencyConfig 是平台的全局货币 + 汇率配置。
//
// 汇率方向（FX 牌价惯例）：
//
//	Rates["USD"] = 7.20  ⇒  1 美元 = 7.20 单位的 DefaultCurrency
//
// DefaultCurrency 自身的"汇率"恒为 1，无需也不允许出现在 Rates 里。
type CurrencyConfig struct {
	DefaultCurrency string                     `json:"defaultCurrency"`
	Rates           map[string]decimal.Decimal `json:"rates"` // key: 外币 ISO 4217；value: 1 外币 = X 基准币
	UpdatedAt       time.Time                  `json:"updatedAt"`
}

// CurrencyRateInput 是管理 API 接受的单条汇率输入。
type CurrencyRateInput struct {
	Code string `json:"code"` // ISO 4217 三大写字母
	Rate string `json:"rate"` // 用字符串避免 float 精度坑；解析为 decimal.Decimal
}

// CurrencyConfigInput 是 PUT /admin/settings/currency 的请求体。
type CurrencyConfigInput struct {
	DefaultCurrency string              `json:"defaultCurrency"`
	Rates           []CurrencyRateInput `json:"rates"`
}

// 已知错误。
var (
	ErrCurrencyInvalidCode = errors.New("currency: invalid ISO 4217 code (need 3 uppercase letters)")
	ErrCurrencyDuplicate   = errors.New("currency: duplicate rate code")
	ErrCurrencySelfRate    = errors.New("currency: default currency cannot appear in rates")
	ErrCurrencyRateInvalid = errors.New("currency: rate must be positive decimal")
	ErrCurrencyUnknown     = errors.New("currency: rate not configured for this code")
)

var iso4217Re = regexp.MustCompile(`^[A-Z]{3}$`)

// NormalizeCode 把"usd"/" usd "/"USD" 都规整为 "USD" 并校验。
func NormalizeCode(s string) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(s))
	if !iso4217Re.MatchString(c) {
		return "", fmt.Errorf("%w: %q", ErrCurrencyInvalidCode, s)
	}
	return c, nil
}

// Validate 把 input 校验后落成 CurrencyConfig；不修改 input。
func (in CurrencyConfigInput) Validate() (CurrencyConfig, error) {
	def, err := NormalizeCode(in.DefaultCurrency)
	if err != nil {
		return CurrencyConfig{}, fmt.Errorf("defaultCurrency: %w", err)
	}
	out := CurrencyConfig{
		DefaultCurrency: def,
		Rates:           make(map[string]decimal.Decimal, len(in.Rates)),
	}
	for i, r := range in.Rates {
		code, err := NormalizeCode(r.Code)
		if err != nil {
			return CurrencyConfig{}, fmt.Errorf("rates[%d]: %w", i, err)
		}
		if code == def {
			return CurrencyConfig{}, fmt.Errorf("rates[%d]: %w", i, ErrCurrencySelfRate)
		}
		if _, dup := out.Rates[code]; dup {
			return CurrencyConfig{}, fmt.Errorf("rates[%d]: %w (%s)", i, ErrCurrencyDuplicate, code)
		}
		rate, err := decimal.NewFromString(strings.TrimSpace(r.Rate))
		if err != nil {
			return CurrencyConfig{}, fmt.Errorf("rates[%d].rate: %w (%v)", i, ErrCurrencyRateInvalid, err)
		}
		if !rate.IsPositive() {
			return CurrencyConfig{}, fmt.Errorf("rates[%d].rate: %w", i, ErrCurrencyRateInvalid)
		}
		out.Rates[code] = rate
	}
	return out, nil
}

// Convert 把 amt 从 from 货币换算到 to 货币。
//
//   - from == to ⇒ 原样返回
//   - 其它情况：先把 amt 化到基准币，再化到 to
//   - 任一货币不在 rates / default 中时返回 ErrCurrencyUnknown
func (c CurrencyConfig) Convert(amt decimal.Decimal, from, to string) (decimal.Decimal, error) {
	f, err := NormalizeCode(from)
	if err != nil {
		return decimal.Zero, err
	}
	t, err := NormalizeCode(to)
	if err != nil {
		return decimal.Zero, err
	}
	if f == t {
		return amt, nil
	}

	def := c.DefaultCurrency
	if def == "" {
		return decimal.Zero, fmt.Errorf("%w: default currency not set", ErrCurrencyUnknown)
	}

	// amt → 基准币
	var inBase decimal.Decimal
	switch f {
	case def:
		inBase = amt
	default:
		rate, ok := c.Rates[f]
		if !ok {
			return decimal.Zero, fmt.Errorf("%w: %s", ErrCurrencyUnknown, f)
		}
		inBase = amt.Mul(rate)
	}

	// 基准币 → t
	switch t {
	case def:
		return inBase, nil
	default:
		rate, ok := c.Rates[t]
		if !ok {
			return decimal.Zero, fmt.Errorf("%w: %s", ErrCurrencyUnknown, t)
		}
		// inBase / rate
		return inBase.Div(rate), nil
	}
}

// ============== 持久化 ==============

const currencySettingsKey = "currency_config"

// CurrencyStore 把 CurrencyConfig 持久化到 billing.settings 表，并支持热更新。
//
// 与 PaymentConfigStore 同模式：内存里持有当前配置，PG 保存 JSON，
// Update() 时同时保存并刷新内存。
type CurrencyStore struct {
	mu  sync.RWMutex
	pg  *pgxpool.Pool
	cur CurrencyConfig
}

// NewCurrencyStore 构造 store；从 PG 加载，pg=nil 时退化为纯内存（测试用）。
func NewCurrencyStore(pg *pgxpool.Pool) *CurrencyStore {
	s := &CurrencyStore{pg: pg, cur: defaultCurrencyConfig()}
	s.loadFromPG()
	return s
}

func defaultCurrencyConfig() CurrencyConfig {
	return CurrencyConfig{
		DefaultCurrency: "CNY",
		Rates:           map[string]decimal.Decimal{},
		UpdatedAt:       time.Time{},
	}
}

func (s *CurrencyStore) loadFromPG() {
	if s.pg == nil {
		return
	}
	var raw []byte
	err := s.pg.QueryRow(context.Background(),
		`SELECT value FROM billing.settings WHERE key = $1`, currencySettingsKey).Scan(&raw)
	if err != nil {
		// 行不存在或读失败：保留 default
		return
	}
	var cur CurrencyConfig
	if json.Unmarshal(raw, &cur) != nil {
		return
	}
	if cur.DefaultCurrency == "" {
		cur.DefaultCurrency = "CNY"
	}
	if cur.Rates == nil {
		cur.Rates = map[string]decimal.Decimal{}
	}
	s.cur = cur
}

func (s *CurrencyStore) saveToPG() error {
	if s.pg == nil {
		return nil
	}
	data, err := json.Marshal(s.cur)
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO billing.settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = now()
	`
	_, err = s.pg.Exec(context.Background(), q, currencySettingsKey, data)
	return err
}

// Get 返回当前生效配置（拷贝；外部修改不影响 store）。
func (s *CurrencyStore) Get() CurrencyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := CurrencyConfig{
		DefaultCurrency: s.cur.DefaultCurrency,
		Rates:           make(map[string]decimal.Decimal, len(s.cur.Rates)),
		UpdatedAt:       s.cur.UpdatedAt,
	}
	for k, v := range s.cur.Rates {
		out.Rates[k] = v
	}
	return out
}

// Default 当前基准币（便利方法）。
func (s *CurrencyStore) Default() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur.DefaultCurrency
}

// Convert 按当前 store 的配置换算。
func (s *CurrencyStore) Convert(amt decimal.Decimal, from, to string) (decimal.Decimal, error) {
	cfg := s.Get()
	return cfg.Convert(amt, from, to)
}

// ApplyCurrency 把订单从当前 Currency 换算到 target，并把 OriginalAmount/
// OriginalCurrency/FXRate 三个快照字段填好。
//
// 行为：
//   - target 为空 ⇒ 不换算；OriginalAmount=Amount, OriginalCurrency=Currency, FXRate=1
//   - target == 当前 Currency ⇒ 同上（仅写快照）
//   - 其它 ⇒ 用 store 当前汇率换算；任一货币缺汇率会返回 ErrCurrencyUnknown
//
// 始终先写快照、后改 Amount/Currency；上层即使忽略错误也能保证"快照 = 原状态"。
func (s *CurrencyStore) ApplyCurrency(order *Order, target string) error {
	if order == nil {
		return errors.New("currency: nil order")
	}
	target = strings.ToUpper(strings.TrimSpace(target))
	// 先把当前状态拍下作"原始"
	order.OriginalAmount = order.Amount
	order.OriginalCurrency = order.Currency
	order.FXRate = decimal.NewFromInt(1)

	if target == "" || target == order.Currency {
		return nil
	}

	converted, err := s.Convert(order.Amount, order.Currency, target)
	if err != nil {
		// 失败时回滚快照（保持订单干净）
		order.OriginalAmount = decimal.Zero
		order.OriginalCurrency = ""
		order.FXRate = decimal.Zero
		return err
	}
	// fx_rate = converted / original；除以 0 不会发生，因为 ApplyCurrency 不允许零金额订单
	if !order.OriginalAmount.IsZero() {
		order.FXRate = converted.Div(order.OriginalAmount)
	}
	order.Amount = converted
	order.Currency = target
	return nil
}

// Update 校验 input、写 PG、刷新内存；任何一步出错都不会落库。
func (s *CurrencyStore) Update(in CurrencyConfigInput) (CurrencyConfig, error) {
	cfg, err := in.Validate()
	if err != nil {
		return CurrencyConfig{}, err
	}
	cfg.UpdatedAt = time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.cur
	s.cur = cfg
	if err := s.saveToPG(); err != nil {
		s.cur = prev // 回滚内存
		return CurrencyConfig{}, fmt.Errorf("currency: persist failed: %w", err)
	}
	return cfg, nil
}
