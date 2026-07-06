package pricing

import (
	"fmt"
	"math/big"

	"github.com/contract-to-cash/core/domain/shared"
)

// UsagePrice is a pricing model that charges per unit of usage with optional min/max clamps.
//
// Minimum and Maximum, when set, MUST be denominated in the same currency as
// UnitPrice. CalculatePrice compares them against the computed charge with
// Money.GreaterThan, which silently returns false on a currency mismatch — so a
// wrong-currency clamp would be silently ignored rather than applied. Construct
// via NewUsagePrice to validate this invariant up front (issue #148). The
// broader move to make CalculatePrice itself currency-safe is tracked separately
// (issue #156).
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
// Zero usage returns zero money; negative usage panics (see the PricingModel
// contract and assertNonNegativeUsage).
func (p UsagePrice) CalculatePrice(usage int64) shared.Money {
	assertNonNegativeUsage("UsagePrice", usage)
	if usage == 0 {
		return shared.Zero(p.UnitPrice.Currency())
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
