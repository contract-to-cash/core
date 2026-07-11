package shared

import "sync"

// DefaultMinorUnitExponent is the minor-unit exponent assumed for any Currency
// that has not been explicitly registered. Most ISO 4217 currencies use two
// minor-unit digits (cents), so 2 is the safe default for the open Currency
// string type. Register a currency with RegisterCurrencyMinorUnit to override.
const DefaultMinorUnitExponent = 2

// minorUnitExponents maps a currency to the number of decimal places in its
// smallest transactable unit (its "minor unit"): JPY has none (¥1 is the
// smallest unit, exponent 0), USD/EUR have two (cents, exponent 2). It is an
// open, extensible registry because Currency is an open string type — consumers
// can register additional currencies (e.g. BHD=3, KWD=3) or override a default.
var (
	minorUnitMu        sync.RWMutex
	minorUnitExponents = map[Currency]int{
		CurrencyJPY: 0,
		CurrencyUSD: 2,
		CurrencyEUR: 2,
	}
)

// RegisterCurrencyMinorUnit registers (or overrides) the minor-unit exponent for
// a currency. The exponent is the number of decimal places in the currency's
// smallest transactable unit (e.g. 0 for JPY, 2 for USD, 3 for KWD). A negative
// exponent is clamped to 0. This is safe for concurrent use; call it during
// application start-up before billing runs.
func RegisterCurrencyMinorUnit(currency Currency, exponent int) {
	if exponent < 0 {
		exponent = 0
	}
	minorUnitMu.Lock()
	defer minorUnitMu.Unlock()
	minorUnitExponents[currency] = exponent
}

// MinorUnitExponent returns the number of decimal places in the currency's
// minor unit. Unregistered currencies fall back to DefaultMinorUnitExponent (2),
// which matches the majority of ISO 4217 currencies. Use
// RegisterCurrencyMinorUnit to register currencies with a different precision.
func (c Currency) MinorUnitExponent() int {
	minorUnitMu.RLock()
	defer minorUnitMu.RUnlock()
	if exp, ok := minorUnitExponents[c]; ok {
		return exp
	}
	return DefaultMinorUnitExponent
}
