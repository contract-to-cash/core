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
//
// ⚠️ This registry is PROCESS-GLOBAL MUTABLE STATE that the billing pipeline
// reads on every rounding step (Money.RoundToMinorUnit → MinorUnitExponent).
// All RegisterCurrencyMinorUnit calls MUST happen at application start-up,
// before any billing runs. The mutex only makes individual reads/writes
// data-race-free; it does NOT make a billing run atomic with respect to a
// registration. Re-registering a currency while invoices are being generated
// changes the rounding quantum mid-flight, so amounts computed before and
// after the change within the same pipeline can disagree (e.g. a subtotal
// rounded at exponent 2 combined with a tax rounded at exponent 0) — producing
// invoices that do not reconcile against the gateway.
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
// exponent is clamped to 0.
//
// ⚠️ Call this ONLY during application start-up, before any billing runs. The
// call itself is data-race-free (mutex-guarded), but the registry is global:
// registering or overriding a currency while billing pipelines are in flight
// changes the rounding of those in-flight calculations mid-stream, so a single
// invoice can mix amounts quantized at different exponents and fail to
// reconcile against the payment gateway (see the note on minorUnitExponents).
// There is no supported way to change a currency's exponent at runtime.
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
