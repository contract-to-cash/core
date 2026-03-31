package shared

import (
	"encoding/json"
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
