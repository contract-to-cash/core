package shared

import (
	"encoding/json"
	"math"
	"math/big"
	"testing"
)

func TestNewMoney(t *testing.T) {
	m := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	if m.Amount().Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("expected 100, got %s", m.Amount().RatString())
	}
	if m.Currency() != CurrencyJPY {
		t.Errorf("expected JPY, got %s", m.Currency())
	}
}

func TestNewMoney_NilAmount(t *testing.T) {
	m := NewMoney(nil, CurrencyUSD)
	if !m.IsZero() {
		t.Error("expected zero for nil amount")
	}
}

func TestMoney_Add(t *testing.T) {
	a := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	b := NewMoney(big.NewRat(200, 1), CurrencyJPY)
	result, err := a.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Amount().Cmp(big.NewRat(300, 1)) != 0 {
		t.Errorf("expected 300, got %s", result.Amount().RatString())
	}
}

func TestMoney_Add_CurrencyMismatch(t *testing.T) {
	a := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	b := NewMoney(big.NewRat(200, 1), CurrencyUSD)
	_, err := a.Add(b)
	if err == nil {
		t.Error("expected currency mismatch error")
	}
}

func TestMoney_Subtract(t *testing.T) {
	a := NewMoney(big.NewRat(300, 1), CurrencyJPY)
	b := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	result, err := a.Subtract(b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Amount().Cmp(big.NewRat(200, 1)) != 0 {
		t.Errorf("expected 200, got %s", result.Amount().RatString())
	}
}

func TestMoney_Multiply(t *testing.T) {
	m := NewMoney(big.NewRat(1000, 1), CurrencyJPY)
	result := m.Multiply(big.NewRat(10, 100)) // 10%
	if result.Amount().Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("expected 100, got %s", result.Amount().RatString())
	}
}

func TestMoney_Negate(t *testing.T) {
	m := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	neg := m.Negate()
	if neg.Amount().Cmp(big.NewRat(-100, 1)) != 0 {
		t.Errorf("expected -100, got %s", neg.Amount().RatString())
	}
	if !neg.IsNegative() {
		t.Error("expected negative")
	}
}

func TestMoney_IsZero(t *testing.T) {
	z := Zero(CurrencyJPY)
	if !z.IsZero() {
		t.Error("expected zero")
	}
	nz := NewMoney(big.NewRat(1, 1), CurrencyJPY)
	if nz.IsZero() {
		t.Error("expected non-zero")
	}
}

func TestMoney_GreaterThan(t *testing.T) {
	a := NewMoney(big.NewRat(200, 1), CurrencyJPY)
	b := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	if !a.GreaterThan(b) {
		t.Error("expected 200 > 100")
	}
	if b.GreaterThan(a) {
		t.Error("expected 100 < 200")
	}
}

func TestMoney_Min(t *testing.T) {
	a := NewMoney(big.NewRat(200, 1), CurrencyJPY)
	b := NewMoney(big.NewRat(100, 1), CurrencyJPY)
	result, err := a.Min(b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Amount().Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("expected 100, got %s", result.Amount().RatString())
	}
}

func TestMoney_JSONRoundTrip(t *testing.T) {
	original := NewMoney(big.NewRat(12345, 100), CurrencyUSD)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored Money
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Amount().Cmp(original.Amount()) != 0 {
		t.Errorf("amount mismatch: %s vs %s", original.Amount().RatString(), restored.Amount().RatString())
	}
	if restored.Currency() != original.Currency() {
		t.Errorf("currency mismatch: %s vs %s", original.Currency(), restored.Currency())
	}
}

func TestMoney_Int64(t *testing.T) {
	tests := []struct {
		name  string
		money Money
		want  int64
	}{
		{"integer amount", NewMoney(big.NewRat(1000, 1), CurrencyJPY), 1000},
		{"zero", Zero(CurrencyJPY), 0},
		{"negative", NewMoney(big.NewRat(-500, 1), CurrencyJPY), -500},
		{"fractional truncates", NewMoney(big.NewRat(1999, 100), CurrencyUSD), 19}, // 19.99 -> 19
		// Truncation is toward zero, NOT floor: -1.5 -> -1 (was -2 under the old
		// big.Int.Div floor division), -1.99 -> -1 (issue #189).
		{"negative fractional truncates toward zero", NewMoney(big.NewRat(-3, 2), CurrencyJPY), -1},
		{"negative fractional near-two truncates toward zero", NewMoney(big.NewRat(-199, 100), CurrencyUSD), -1},
		{"nil amount (zero value)", Money{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.money.Int64()
			if got != tt.want {
				t.Errorf("Int64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMoney_Int64Checked(t *testing.T) {
	// In range: truncates toward zero, no error.
	inRange := []struct {
		name  string
		money Money
		want  int64
	}{
		{"integer", NewMoney(big.NewRat(1000, 1), CurrencyJPY), 1000},
		{"negative fractional toward zero", NewMoney(big.NewRat(-3, 2), CurrencyJPY), -1},
		{"nil amount", Money{}, 0},
		{"max int64", NewMoney(new(big.Rat).SetInt64(math.MaxInt64), CurrencyJPY), math.MaxInt64},
		{"min int64", NewMoney(new(big.Rat).SetInt64(math.MinInt64), CurrencyJPY), math.MinInt64},
	}
	for _, tt := range inRange {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.money.Int64Checked()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Int64Checked() = %d, want %d", got, tt.want)
			}
		})
	}

	// Overflow: MaxInt64 + 1 does not fit in int64.
	over := new(big.Rat).SetInt64(math.MaxInt64)
	over.Add(over, big.NewRat(1, 1))
	if _, err := NewMoney(over, CurrencyJPY).Int64Checked(); err == nil {
		t.Error("expected overflow error for MaxInt64+1, got nil")
	}

	// Overflow below MinInt64.
	under := new(big.Rat).SetInt64(math.MinInt64)
	under.Sub(under, big.NewRat(1, 1))
	if _, err := NewMoney(under, CurrencyJPY).Int64Checked(); err == nil {
		t.Error("expected overflow error for MinInt64-1, got nil")
	}
}

func TestMoney_RoundToMinorUnit(t *testing.T) {
	tests := []struct {
		name     string
		amount   *big.Rat
		currency Currency
		mode     RoundingMode
		want     *big.Rat
	}{
		// JPY has 0 minor-unit digits: round to whole yen.
		{"jpy 10.1 down", big.NewRat(101, 10), CurrencyJPY, RoundDown, big.NewRat(10, 1)},
		{"jpy 10.1 half_up", big.NewRat(101, 10), CurrencyJPY, RoundHalfUp, big.NewRat(10, 1)},
		{"jpy 10.5 half_up", big.NewRat(21, 2), CurrencyJPY, RoundHalfUp, big.NewRat(11, 1)},
		{"jpy 10.9 up", big.NewRat(109, 10), CurrencyJPY, RoundUp, big.NewRat(11, 1)},
		{"jpy negative 10.5 half_up ties away", big.NewRat(-21, 2), CurrencyJPY, RoundHalfUp, big.NewRat(-11, 1)},
		{"jpy negative 10.9 down toward zero", big.NewRat(-109, 10), CurrencyJPY, RoundDown, big.NewRat(-10, 1)},
		// USD/EUR have 2 minor-unit digits: round to cents.
		{"usd 1.005 half_up", big.NewRat(1005, 1000), CurrencyUSD, RoundHalfUp, big.NewRat(101, 100)},
		{"usd 1.009 down", big.NewRat(1009, 1000), CurrencyUSD, RoundDown, big.NewRat(100, 100)},
		{"usd 1.001 up", big.NewRat(1001, 1000), CurrencyUSD, RoundUp, big.NewRat(101, 100)},
		{"eur negative 1.005 down toward zero", big.NewRat(-1005, 1000), CurrencyEUR, RoundDown, big.NewRat(-100, 100)},
		// Already integral in minor units: unchanged.
		{"jpy integral", big.NewRat(100, 1), CurrencyJPY, RoundHalfUp, big.NewRat(100, 1)},
		{"usd integral cents", big.NewRat(150, 100), CurrencyUSD, RoundDown, big.NewRat(150, 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewMoney(tt.amount, tt.currency).RoundToMinorUnit(tt.mode)
			if got.Amount().Cmp(tt.want) != 0 {
				t.Errorf("RoundToMinorUnit(%s) = %s, want %s", tt.mode, got.Amount().RatString(), tt.want.RatString())
			}
			if got.Currency() != tt.currency {
				t.Errorf("currency changed: got %s, want %s", got.Currency(), tt.currency)
			}
			if !got.IsIntegralMinorUnit() {
				t.Errorf("result %s is not integral in minor units for %s", got.Amount().RatString(), tt.currency)
			}
		})
	}
}

func TestMoney_IsIntegralMinorUnit(t *testing.T) {
	tests := []struct {
		name     string
		amount   *big.Rat
		currency Currency
		want     bool
	}{
		{"jpy whole yen", big.NewRat(100, 1), CurrencyJPY, true},
		{"jpy fractional yen", big.NewRat(101, 10), CurrencyJPY, false},
		{"usd whole cents", big.NewRat(150, 100), CurrencyUSD, true},
		{"usd sub-cent", big.NewRat(1005, 1000), CurrencyUSD, false},
		{"usd whole dollars", big.NewRat(5, 1), CurrencyUSD, true},
		{"zero", new(big.Rat), CurrencyJPY, true},
		{"unregistered currency defaults to 2 digits, whole cents", big.NewRat(150, 100), Currency("GBP"), true},
		{"unregistered currency defaults to 2 digits, sub-cent", big.NewRat(1005, 1000), Currency("GBP"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewMoney(tt.amount, tt.currency).IsIntegralMinorUnit(); got != tt.want {
				t.Errorf("IsIntegralMinorUnit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCurrency_MinorUnitExponent(t *testing.T) {
	if got := CurrencyJPY.MinorUnitExponent(); got != 0 {
		t.Errorf("JPY exponent = %d, want 0", got)
	}
	if got := CurrencyUSD.MinorUnitExponent(); got != 2 {
		t.Errorf("USD exponent = %d, want 2", got)
	}
	if got := CurrencyEUR.MinorUnitExponent(); got != 2 {
		t.Errorf("EUR exponent = %d, want 2", got)
	}
	// Unregistered currency falls back to the default.
	if got := Currency("XYZ").MinorUnitExponent(); got != DefaultMinorUnitExponent {
		t.Errorf("unregistered exponent = %d, want %d", got, DefaultMinorUnitExponent)
	}
	// Registration overrides / adds (e.g. a 3-digit currency like KWD).
	RegisterCurrencyMinorUnit(Currency("KWD"), 3)
	if got := Currency("KWD").MinorUnitExponent(); got != 3 {
		t.Errorf("KWD exponent = %d, want 3", got)
	}
	// Negative exponents are clamped to 0.
	RegisterCurrencyMinorUnit(Currency("CLAMP"), -5)
	if got := Currency("CLAMP").MinorUnitExponent(); got != 0 {
		t.Errorf("clamped exponent = %d, want 0", got)
	}
}

func TestMoney_Float64(t *testing.T) {
	tests := []struct {
		name  string
		money Money
		want  float64
	}{
		{"integer amount", NewMoney(big.NewRat(1000, 1), CurrencyJPY), 1000.0},
		{"zero", Zero(CurrencyJPY), 0.0},
		{"negative", NewMoney(big.NewRat(-500, 1), CurrencyJPY), -500.0},
		{"fractional", NewMoney(big.NewRat(1999, 100), CurrencyUSD), 19.99},
		{"nil amount (zero value)", Money{}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.money.Float64()
			if got != tt.want {
				t.Errorf("Float64() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestZeroMoney_JSONRoundTrip(t *testing.T) {
	original := Zero(CurrencyJPY)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored Money
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.IsZero() {
		t.Error("expected zero after round trip")
	}
}
