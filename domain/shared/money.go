package shared

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Currency represents a currency code.
type Currency string

const (
	CurrencyJPY Currency = "JPY"
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
)

// Money represents a monetary value with currency.
// Uses big.Rat for precise arithmetic without floating-point errors.
type Money struct {
	amount   *big.Rat
	currency Currency
}

// NewMoney creates a new Money value.
func NewMoney(amount *big.Rat, currency Currency) Money {
	if amount == nil {
		amount = new(big.Rat)
	}
	return Money{
		amount:   new(big.Rat).Set(amount),
		currency: currency,
	}
}

// Zero returns a zero Money value for the given currency.
func Zero(currency Currency) Money {
	return Money{
		amount:   new(big.Rat),
		currency: currency,
	}
}

// Amount returns the monetary amount.
func (m Money) Amount() *big.Rat {
	if m.amount == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(m.amount)
}

// Currency returns the currency.
func (m Money) Currency() Currency {
	return m.currency
}

// Add adds two Money values. Returns error if currencies differ.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, NewDomainError(ErrCodeCurrencyMismatch,
			fmt.Sprintf("cannot add %s to %s", other.currency, m.currency))
	}
	result := new(big.Rat).Add(m.safeAmount(), other.safeAmount())
	return NewMoney(result, m.currency), nil
}

// Subtract subtracts other from m. Returns error if currencies differ.
func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, NewDomainError(ErrCodeCurrencyMismatch,
			fmt.Sprintf("cannot subtract %s from %s", other.currency, m.currency))
	}
	result := new(big.Rat).Sub(m.safeAmount(), other.safeAmount())
	return NewMoney(result, m.currency), nil
}

// Multiply multiplies the amount by a factor.
func (m Money) Multiply(factor *big.Rat) Money {
	if factor == nil {
		factor = new(big.Rat)
	}
	result := new(big.Rat).Mul(m.safeAmount(), factor)
	return NewMoney(result, m.currency)
}

// Negate returns the negated Money value.
func (m Money) Negate() Money {
	result := new(big.Rat).Neg(m.safeAmount())
	return NewMoney(result, m.currency)
}

// IsNegative returns true if the amount is negative.
func (m Money) IsNegative() bool {
	return m.safeAmount().Sign() < 0
}

// IsZero returns true if the amount is zero.
func (m Money) IsZero() bool {
	return m.safeAmount().Sign() == 0
}

// GreaterThan returns true if m > other. Returns false if currencies differ.
func (m Money) GreaterThan(other Money) bool {
	if m.currency != other.currency {
		return false
	}
	return m.safeAmount().Cmp(other.safeAmount()) > 0
}

// Min returns the smaller of m and other. Returns error if currencies differ.
func (m Money) Min(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, NewDomainError(ErrCodeCurrencyMismatch,
			fmt.Sprintf("cannot compare %s with %s", m.currency, other.currency))
	}
	if m.safeAmount().Cmp(other.safeAmount()) <= 0 {
		return NewMoney(m.safeAmount(), m.currency), nil
	}
	return NewMoney(other.safeAmount(), m.currency), nil
}

// Int64 returns the amount as int64, truncating any fractional part.
// Useful for zero-decimal currencies like JPY, KRW, etc.
func (m Money) Int64() int64 {
	if m.amount == nil {
		return 0
	}
	return new(big.Int).Div(m.amount.Num(), m.amount.Denom()).Int64()
}

// Float64 returns the amount as float64.
func (m Money) Float64() float64 {
	if m.amount == nil {
		return 0
	}
	f, _ := m.amount.Float64()
	return f
}

func (m Money) safeAmount() *big.Rat {
	if m.amount == nil {
		return new(big.Rat)
	}
	return m.amount
}

// moneyJSON is the JSON representation of Money.
type moneyJSON struct {
	Amount   string   `json:"amount"`
	Currency Currency `json:"currency"`
}

// MarshalJSON implements json.Marshaler.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyJSON{
		Amount:   m.safeAmount().RatString(),
		Currency: m.currency,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *Money) UnmarshalJSON(data []byte) error {
	var v moneyJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	r := new(big.Rat)
	if _, ok := r.SetString(v.Amount); !ok {
		return fmt.Errorf("invalid money amount: %s", v.Amount)
	}
	m.amount = r
	m.currency = v.Currency
	return nil
}
