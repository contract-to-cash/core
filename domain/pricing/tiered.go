package pricing

import (
	"math/big"

	"github.com/contract-to-cash/core/domain/shared"
)

// TieredPricingMode determines how tiered pricing is calculated.
type TieredPricingMode string

const (
	// TieredPricingGraduated calculates each tier's usage at that tier's rate.
	TieredPricingGraduated TieredPricingMode = "graduated"
	// TieredPricingVolume applies the single tier rate that covers total usage to all units.
	TieredPricingVolume TieredPricingMode = "volume"
)

// PriceTier defines a pricing tier boundary and its rates.
type PriceTier struct {
	UpTo      int64
	UnitPrice shared.Money
	FlatFee   shared.Money
}

// TieredPrice is a pricing model that charges based on usage tiers.
type TieredPrice struct {
	Tiers []PriceTier
	Mode  TieredPricingMode
}

// CalculatePrice calculates the price based on the tiered pricing mode.
func (p TieredPrice) CalculatePrice(usage int64) shared.Money {
	if len(p.Tiers) == 0 || usage <= 0 {
		// Use currency from first tier if available, otherwise fallback
		if len(p.Tiers) > 0 {
			return shared.Zero(p.Tiers[0].UnitPrice.Currency())
		}
		return shared.Money{}
	}

	switch p.Mode {
	case TieredPricingGraduated:
		return p.calculateGraduated(usage)
	case TieredPricingVolume:
		return p.calculateVolume(usage)
	default:
		return shared.Zero(p.Tiers[0].UnitPrice.Currency())
	}
}

func (p TieredPrice) calculateGraduated(usage int64) shared.Money {
	currency := p.Tiers[0].UnitPrice.Currency()
	total := shared.Zero(currency)
	remaining := usage
	var prevUpTo int64

	for _, tier := range p.Tiers {
		if remaining <= 0 {
			break
		}
		// UpTo=0 means unlimited — consume all remaining units
		var unitsInTier int64
		if tier.UpTo == 0 {
			unitsInTier = remaining
		} else {
			tierCapacity := tier.UpTo - prevUpTo
			unitsInTier = remaining
			if unitsInTier > tierCapacity {
				unitsInTier = tierCapacity
			}
		}

		factor := new(big.Rat).SetInt64(unitsInTier)
		tierTotal := tier.UnitPrice.Multiply(factor)

		var err error
		total, err = total.Add(tierTotal)
		if err != nil {
			return shared.Zero(currency)
		}

		// Add flat fee if any units fall in this tier
		if unitsInTier > 0 {
			total, err = total.Add(tier.FlatFee)
			if err != nil {
				return shared.Zero(currency)
			}
		}

		remaining -= unitsInTier
		prevUpTo = tier.UpTo
	}

	return total
}

func (p TieredPrice) calculateVolume(usage int64) shared.Money {
	currency := p.Tiers[0].UnitPrice.Currency()

	for _, tier := range p.Tiers {
		if tier.UpTo == 0 || usage <= tier.UpTo {
			factor := new(big.Rat).SetInt64(usage)
			unitTotal := tier.UnitPrice.Multiply(factor)
			result, err := unitTotal.Add(tier.FlatFee)
			if err != nil {
				return shared.Zero(currency)
			}
			return result
		}
	}

	// Usage exceeds all tiers; use the last tier
	lastTier := p.Tiers[len(p.Tiers)-1]
	factor := new(big.Rat).SetInt64(usage)
	unitTotal := lastTier.UnitPrice.Multiply(factor)
	result, err := unitTotal.Add(lastTier.FlatFee)
	if err != nil {
		return shared.Zero(currency)
	}
	return result
}
