package pricing

import (
	"fmt"
	"math/big"

	"github.com/contract-to-cash/core/domain/shared"
)

// UsagePrice is a pricing model that charges per unit of usage with optional min/max clamps.
//
// Minimum and Maximum, when set, MUST be denominated in the same currency as
// UnitPrice. Construct via NewUsagePrice to validate this invariant up front
// (issue #148). The exported fields remain writable for backward compatibility
// and persistence reconstruction, but a UsagePrice built by bypassing the
// constructor with a wrong-currency clamp makes CalculatePrice PANIC rather
// than silently skip the clamp (issue #238): the legacy Money.GreaterThan
// comparator returns false on a currency mismatch, so before that guard a
// misconfigured Maximum silently failed to cap the charge (over-billing) and a
// misconfigured Minimum silently failed to floor it (under-billing).
type UsagePrice struct {
	UnitPrice shared.Money
	Minimum   *shared.Money
	Maximum   *shared.Money
}

// NewUsagePrice constructs a UsagePrice, validating that any Minimum/Maximum
// clamp shares UnitPrice's currency. This surfaces a misconfigured clamp as an
// error instead of letting CalculatePrice silently drop it (issue #148).
func NewUsagePrice(unitPrice shared.Money, minimum, maximum *shared.Money) (UsagePrice, error) {
	if minimum != nil && minimum.Currency() != unitPrice.Currency() {
		return UsagePrice{}, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
			fmt.Sprintf("usage price minimum currency %s does not match unit price currency %s",
				minimum.Currency(), unitPrice.Currency()))
	}
	if maximum != nil && maximum.Currency() != unitPrice.Currency() {
		return UsagePrice{}, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
			fmt.Sprintf("usage price maximum currency %s does not match unit price currency %s",
				maximum.Currency(), unitPrice.Currency()))
	}
	return UsagePrice{UnitPrice: unitPrice, Minimum: minimum, Maximum: maximum}, nil
}

// CalculatePrice calculates usage * UnitPrice, clamped by Minimum and Maximum.
//
// Zero usage returns zero money — the Minimum clamp is DELIBERATELY NOT applied
// when usage is zero (issue #162 L-2). Minimum is a floor on the charge for a
// period in which the metered resource was actually used; a period with no usage
// at all bills nothing. A consumer that wants an unconditional periodic minimum
// (a floor that applies even at zero usage) should model it as a separate flat
// base charge (e.g. a hybrid contract) rather than expecting UsagePrice.Minimum
// to cover it. Negative usage panics (see the PricingModel contract and
// assertNonNegativeUsage).
//
// Before applying the clamps it validates that Minimum/Maximum share
// UnitPrice's currency and PANICS on a mismatch (issue #238). A UsagePrice
// built by bypassing NewUsagePrice with a wrong-currency clamp would otherwise
// have the clamp silently ignored (Money.GreaterThan no-ops on a currency
// mismatch), producing a silently wrong amount — a caller bug that must
// surface loudly. This is the same policy as mustAddTier and
// assertNonNegativeUsage; the signature returns no error, so a panic is the
// only loud channel.
func (p UsagePrice) CalculatePrice(usage int64) shared.Money {
	assertNonNegativeUsage("UsagePrice", usage)
	if usage == 0 {
		return shared.Zero(p.UnitPrice.Currency())
	}

	// Invariant re-check for constructor bypass (issue #238): a wrong-currency
	// clamp must not be silently dropped by the GreaterThan comparisons below.
	if p.Minimum != nil && p.Minimum.Currency() != p.UnitPrice.Currency() {
		panic(fmt.Sprintf("UsagePrice.CalculatePrice: minimum currency %s does not match unit price currency %s (construct via NewUsagePrice)",
			p.Minimum.Currency(), p.UnitPrice.Currency()))
	}
	if p.Maximum != nil && p.Maximum.Currency() != p.UnitPrice.Currency() {
		panic(fmt.Sprintf("UsagePrice.CalculatePrice: maximum currency %s does not match unit price currency %s (construct via NewUsagePrice)",
			p.Maximum.Currency(), p.UnitPrice.Currency()))
	}

	factor := new(big.Rat).SetInt64(usage)
	result := p.UnitPrice.Multiply(factor)

	if p.Minimum != nil && p.Minimum.GreaterThan(result) {
		result = *p.Minimum
	}
	if p.Maximum != nil && result.GreaterThan(*p.Maximum) {
		result = *p.Maximum
	}

	return result
}
