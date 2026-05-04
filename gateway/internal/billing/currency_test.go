package billing

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func mustDecimal(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("decimal %q: %v", s, err)
	}
	return d
}

func TestNormalizeCode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"USD", "USD", false},
		{"usd", "USD", false},
		{"  EUR  ", "EUR", false},
		{"", "", true},
		{"US", "", true},
		{"USDT", "", true},
		{"US1", "", true},
		{"US$", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeCode(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeCode(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidate_Happy(t *testing.T) {
	in := CurrencyConfigInput{
		DefaultCurrency: "cny",
		Rates: []CurrencyRateInput{
			{Code: "USD", Rate: "7.20"},
			{Code: "eur", Rate: "7.85"},
		},
	}
	cfg, err := in.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultCurrency != "CNY" {
		t.Errorf("default = %q", cfg.DefaultCurrency)
	}
	if got := cfg.Rates["USD"]; !got.Equal(mustDecimal(t, "7.20")) {
		t.Errorf("USD rate = %s", got)
	}
	if got := cfg.Rates["EUR"]; !got.Equal(mustDecimal(t, "7.85")) {
		t.Errorf("EUR rate = %s", got)
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := map[string]CurrencyConfigInput{
		"empty default": {
			DefaultCurrency: "",
			Rates:           []CurrencyRateInput{{Code: "USD", Rate: "1"}},
		},
		"bad default": {
			DefaultCurrency: "ABC1",
			Rates:           []CurrencyRateInput{{Code: "USD", Rate: "1"}},
		},
		"self rate": {
			DefaultCurrency: "CNY",
			Rates:           []CurrencyRateInput{{Code: "CNY", Rate: "1"}},
		},
		"duplicate": {
			DefaultCurrency: "CNY",
			Rates: []CurrencyRateInput{
				{Code: "USD", Rate: "7.2"},
				{Code: "usd", Rate: "7.3"},
			},
		},
		"non-numeric rate": {
			DefaultCurrency: "CNY",
			Rates:           []CurrencyRateInput{{Code: "USD", Rate: "abc"}},
		},
		"zero rate": {
			DefaultCurrency: "CNY",
			Rates:           []CurrencyRateInput{{Code: "USD", Rate: "0"}},
		},
		"negative rate": {
			DefaultCurrency: "CNY",
			Rates:           []CurrencyRateInput{{Code: "USD", Rate: "-1"}},
		},
		"bad currency code": {
			DefaultCurrency: "CNY",
			Rates:           []CurrencyRateInput{{Code: "US", Rate: "1"}},
		},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := in.Validate(); err == nil {
				t.Fatalf("%s: expected error", name)
			}
		})
	}
}

func sampleConfig(t *testing.T) CurrencyConfig {
	t.Helper()
	cfg, err := CurrencyConfigInput{
		DefaultCurrency: "CNY",
		Rates: []CurrencyRateInput{
			{Code: "USD", Rate: "7.20"},
			{Code: "EUR", Rate: "7.85"},
		},
	}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestConvert_SameCurrency(t *testing.T) {
	cfg := sampleConfig(t)
	got, err := cfg.Convert(mustDecimal(t, "100"), "USD", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(mustDecimal(t, "100")) {
		t.Errorf("got %s", got)
	}
}

func TestConvert_BaseToForeign(t *testing.T) {
	cfg := sampleConfig(t)
	// 100 CNY = 100 / 7.2 USD ≈ 13.888888...
	got, err := cfg.Convert(mustDecimal(t, "100"), "CNY", "USD")
	if err != nil {
		t.Fatal(err)
	}
	want := mustDecimal(t, "100").Div(mustDecimal(t, "7.20"))
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestConvert_ForeignToBase(t *testing.T) {
	cfg := sampleConfig(t)
	// 100 USD = 100 * 7.2 CNY = 720
	got, err := cfg.Convert(mustDecimal(t, "100"), "USD", "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(mustDecimal(t, "720.00")) {
		t.Errorf("got %s want 720", got)
	}
}

func TestConvert_ForeignToForeign(t *testing.T) {
	cfg := sampleConfig(t)
	// 100 USD → CNY → EUR：100 * 7.2 = 720 CNY → 720 / 7.85 ≈ 91.7197...
	got, err := cfg.Convert(mustDecimal(t, "100"), "USD", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	want := mustDecimal(t, "100").Mul(mustDecimal(t, "7.20")).Div(mustDecimal(t, "7.85"))
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestConvert_RoundTripCloseEnough(t *testing.T) {
	// shopspring/decimal 的 Div 是有限精度（默认 16 位），
	// 100 / 7.2 * 7.2 ≠ 100 严格相等。这里只断言"误差 < 1 分钱"。
	cfg := sampleConfig(t)
	mid, err := cfg.Convert(mustDecimal(t, "1234.56"), "CNY", "USD")
	if err != nil {
		t.Fatal(err)
	}
	back, err := cfg.Convert(mid, "USD", "CNY")
	if err != nil {
		t.Fatal(err)
	}
	diff := back.Sub(mustDecimal(t, "1234.56")).Abs()
	if diff.GreaterThan(mustDecimal(t, "0.01")) {
		t.Errorf("round-trip drift too big: got %s, |diff|=%s", back, diff)
	}
}

func TestConvert_UnknownCurrency(t *testing.T) {
	cfg := sampleConfig(t)
	_, err := cfg.Convert(mustDecimal(t, "1"), "USD", "JPY")
	if !errors.Is(err, ErrCurrencyUnknown) {
		t.Fatalf("got %v, want ErrCurrencyUnknown", err)
	}
	if !strings.Contains(err.Error(), "JPY") {
		t.Errorf("error message should contain currency code: %v", err)
	}
}

func TestConvert_BadCode(t *testing.T) {
	cfg := sampleConfig(t)
	if _, err := cfg.Convert(mustDecimal(t, "1"), "USDT", "CNY"); err == nil {
		t.Fatal("expected error for bad code")
	}
}

func TestConvert_NoDefaultSet(t *testing.T) {
	cfg := CurrencyConfig{} // 空配置
	if _, err := cfg.Convert(mustDecimal(t, "1"), "USD", "EUR"); err == nil {
		t.Fatal("expected error when default currency unset")
	}
}

func TestStore_DefaultsWhenNoPG(t *testing.T) {
	s := NewCurrencyStore(nil)
	if s.Default() != "CNY" {
		t.Errorf("expected CNY default, got %q", s.Default())
	}
	cfg := s.Get()
	if len(cfg.Rates) != 0 {
		t.Errorf("expected empty rates, got %d", len(cfg.Rates))
	}
}

func TestStore_UpdateAndGet(t *testing.T) {
	s := NewCurrencyStore(nil)
	cfg, err := s.Update(CurrencyConfigInput{
		DefaultCurrency: "CNY",
		Rates: []CurrencyRateInput{
			{Code: "USD", Rate: "7.2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultCurrency != "CNY" {
		t.Errorf("default %q", cfg.DefaultCurrency)
	}
	if !cfg.Rates["USD"].Equal(mustDecimal(t, "7.2")) {
		t.Errorf("USD %s", cfg.Rates["USD"])
	}
	// Get 的拷贝改了不影响 store
	g := s.Get()
	g.Rates["XXX"] = mustDecimal(t, "1")
	if _, ok := s.Get().Rates["XXX"]; ok {
		t.Error("store mutated through Get's returned map")
	}
}

func TestStore_UpdateValidationRollsBack(t *testing.T) {
	s := NewCurrencyStore(nil)
	// 先成功一次
	_, err := s.Update(CurrencyConfigInput{
		DefaultCurrency: "CNY",
		Rates:           []CurrencyRateInput{{Code: "USD", Rate: "7.2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 失败的更新不应该污染 store（"BA" 只有 2 字母，会被 ISO 4217 校验拒）
	_, err = s.Update(CurrencyConfigInput{
		DefaultCurrency: "BA",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !s.Get().Rates["USD"].Equal(mustDecimal(t, "7.2")) {
		t.Error("store was mutated despite invalid update")
	}
}

func TestStore_ConvertViaStore(t *testing.T) {
	s := NewCurrencyStore(nil)
	_, err := s.Update(CurrencyConfigInput{
		DefaultCurrency: "CNY",
		Rates:           []CurrencyRateInput{{Code: "USD", Rate: "7.2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Convert(mustDecimal(t, "100"), "USD", "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(mustDecimal(t, "720.0")) {
		t.Errorf("got %s want 720.0", got)
	}
}

// ApplyCurrency

func newStoreWithLDC(t *testing.T) *CurrencyStore {
	t.Helper()
	s := NewCurrencyStore(nil)
	if _, err := s.Update(CurrencyConfigInput{
		DefaultCurrency: "CNY",
		// 1 LDC = 0.5 CNY（示意：100 CNY 套餐 → 200 LDC）
		Rates: []CurrencyRateInput{{Code: "LDC", Rate: "0.5"}},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyCurrency_NilOrder(t *testing.T) {
	s := NewCurrencyStore(nil)
	if err := s.ApplyCurrency(nil, "USD"); err == nil {
		t.Fatal("expected error on nil order")
	}
}

func TestApplyCurrency_EmptyTargetIsNoop(t *testing.T) {
	s := newStoreWithLDC(t)
	o := &Order{Amount: mustDecimal(t, "100"), Currency: "CNY"}
	if err := s.ApplyCurrency(o, ""); err != nil {
		t.Fatal(err)
	}
	// 仅写快照
	if !o.Amount.Equal(mustDecimal(t, "100")) || o.Currency != "CNY" {
		t.Errorf("Amount/Currency mutated: %s %s", o.Amount, o.Currency)
	}
	if !o.OriginalAmount.Equal(mustDecimal(t, "100")) || o.OriginalCurrency != "CNY" {
		t.Errorf("snapshot wrong: %s %s", o.OriginalAmount, o.OriginalCurrency)
	}
	if !o.FXRate.Equal(decimal.NewFromInt(1)) {
		t.Errorf("FXRate = %s", o.FXRate)
	}
}

func TestApplyCurrency_TargetSameAsCurrent(t *testing.T) {
	s := newStoreWithLDC(t)
	o := &Order{Amount: mustDecimal(t, "100"), Currency: "CNY"}
	if err := s.ApplyCurrency(o, "cny"); err != nil { // 大小写不敏感
		t.Fatal(err)
	}
	if o.Currency != "CNY" || !o.Amount.Equal(mustDecimal(t, "100")) {
		t.Errorf("should be no-op: %s %s", o.Amount, o.Currency)
	}
}

func TestApplyCurrency_CrossCurrency(t *testing.T) {
	s := newStoreWithLDC(t)
	o := &Order{Amount: mustDecimal(t, "100"), Currency: "CNY"}
	if err := s.ApplyCurrency(o, "LDC"); err != nil {
		t.Fatal(err)
	}
	// 100 CNY → 100 / 0.5 = 200 LDC
	if !o.Amount.Equal(mustDecimal(t, "200")) {
		t.Errorf("Amount = %s, want 200", o.Amount)
	}
	if o.Currency != "LDC" {
		t.Errorf("Currency = %s, want LDC", o.Currency)
	}
	if !o.OriginalAmount.Equal(mustDecimal(t, "100")) {
		t.Errorf("OriginalAmount = %s, want 100", o.OriginalAmount)
	}
	if o.OriginalCurrency != "CNY" {
		t.Errorf("OriginalCurrency = %s, want CNY", o.OriginalCurrency)
	}
	// FXRate = 200 / 100 = 2 （1 CNY = 2 LDC）
	if !o.FXRate.Equal(mustDecimal(t, "2")) {
		t.Errorf("FXRate = %s, want 2", o.FXRate)
	}
}

func TestApplyCurrency_MissingRateRollsBack(t *testing.T) {
	s := newStoreWithLDC(t)
	o := &Order{Amount: mustDecimal(t, "100"), Currency: "CNY"}
	err := s.ApplyCurrency(o, "USD") // USD 未配置
	if err == nil {
		t.Fatal("expected ErrCurrencyUnknown")
	}
	// 失败时快照应该被清空
	if !o.OriginalAmount.IsZero() || o.OriginalCurrency != "" || !o.FXRate.IsZero() {
		t.Errorf("snapshot leaked: %s %s %s", o.OriginalAmount, o.OriginalCurrency, o.FXRate)
	}
	// Amount/Currency 也应保持原状
	if !o.Amount.Equal(mustDecimal(t, "100")) || o.Currency != "CNY" {
		t.Errorf("Amount/Currency mutated despite error: %s %s", o.Amount, o.Currency)
	}
}
