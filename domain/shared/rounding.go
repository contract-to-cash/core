package shared

import "math/big"

// RoundingMode defines how a monetary amount is rounded to a fixed number of
// decimal places. It is shared across the domain (e.g. proration configuration)
// and consumed by Money.Round.
type RoundingMode string

const (
	// RoundUp rounds away from zero (e.g. 0.331 -> 0.34, -0.331 -> -0.34).
	RoundUp RoundingMode = "up"
	// RoundDown truncates toward zero (e.g. 0.339 -> 0.33, -0.339 -> -0.33).
	RoundDown RoundingMode = "down"
	// RoundHalfUp rounds to nearest, ties away from zero (e.g. 0.005 -> 0.01).
	RoundHalfUp RoundingMode = "half_up"
)

// Round returns a copy of m rounded to the given number of decimal places using
// the supplied mode. The receiver is not modified and the currency is preserved.
//
// Because Money is backed by big.Rat, business calculations stay exact until a
// caller explicitly reduces an amount to a currency's minor units with Round.
// decimalPlaces is expected to be >= 0 (e.g. 2 for USD/EUR, 0 for JPY); negative
// values are treated as 0. An unrecognised mode falls back to RoundHalfUp.
func (m Money) Round(decimalPlaces int, mode RoundingMode) Money {
	if decimalPlaces < 0 {
		decimalPlaces = 0
	}
	amount := m.safeAmount()

	// scale = 10^decimalPlaces
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalPlaces)), nil)
	scaled := new(big.Rat).Mul(amount, new(big.Rat).SetInt(scale))

	roundedInt := roundRatToInt(scaled, mode)

	result := new(big.Rat).SetFrac(roundedInt, scale)
	return NewMoney(result, m.currency)
}

// roundRatToInt rounds a rational to the nearest integer according to mode.
func roundRatToInt(r *big.Rat, mode RoundingMode) *big.Int {
	q := new(big.Int)
	rem := new(big.Int)
	// QuoRem: q truncated toward zero, rem carries the sign of the numerator.
	q.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Sign() == 0 {
		return q
	}

	one := big.NewInt(1)
	awayFromZero := func() {
		if r.Sign() > 0 {
			q.Add(q, one)
		} else {
			q.Sub(q, one)
		}
	}

	switch mode {
	case RoundDown:
		// truncation toward zero — q already holds it
	case RoundUp:
		awayFromZero()
	case RoundHalfUp:
		// 2*|rem| >= denom  =>  fractional part >= 0.5  =>  round away from zero
		twiceRem := new(big.Int).Abs(rem)
		twiceRem.Mul(twiceRem, big.NewInt(2))
		if twiceRem.Cmp(r.Denom()) >= 0 {
			awayFromZero()
		}
	default:
		// Unknown mode: behave as RoundHalfUp.
		twiceRem := new(big.Int).Abs(rem)
		twiceRem.Mul(twiceRem, big.NewInt(2))
		if twiceRem.Cmp(r.Denom()) >= 0 {
			awayFromZero()
		}
	}
	return q
}
