package pricing

import (
	"fmt"
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
//
// The tiers MUST be sorted ascending by UpTo, every tier's UnitPrice and
// FlatFee MUST share a single currency, and UpTo == 0 (unlimited) is only valid
// on the final tier. CalculatePrice relies on these invariants: an unsorted tier
// makes the graduated tierCapacity (UpTo - prevUpTo) negative and produces a
// negative charge, and a mixed-currency tier makes the internal Money.Add fail.
// Construct via NewTieredPrice to validate them up front (issue #156). The
// exported fields remain writable for backward compatibility and persistence
// reconstruction, but all in-repo construction goes through NewTieredPrice; a
// TieredPrice built by bypassing the constructor with mixed-currency tiers makes
// CalculatePrice panic rather than silently bill zero (see mustAddTier).
type TieredPrice struct {
	Tiers []PriceTier
	Mode  TieredPricingMode
}

// NewTieredPrice constructs a TieredPrice, validating the invariants that
// CalculatePrice depends on (issue #156):
//
//   - at least one tier is configured;
//   - the mode is a known TieredPricingMode (graduated or volume);
//   - tiers are sorted strictly ascending by UpTo;
//   - UpTo == 0 (the "unlimited" sentinel) appears only on the final tier, and
//     every non-final tier has UpTo > 0;
//   - every tier's UnitPrice and FlatFee share a single currency.
//
// This surfaces a misconfigured price as an error instead of letting
// CalculatePrice produce a negative charge (unsorted tiers) or silently bill
// zero (mixed-currency Money.Add failure). It mirrors NewUsagePrice's style
// (issue #148).
//
// Persistence note: reconstructing a historically stored price does NOT go
// through this constructor. Price.FromSnapshot (snapshot.go) carries the stored
// PricingModel value through as-is, so already-persisted prices always load
// regardless of these rules — the same replay-safety principle applied across
// the snapshot path.
func NewTieredPrice(tiers []PriceTier, mode TieredPricingMode) (TieredPrice, error) {
	if len(tiers) == 0 {
		return TieredPrice{}, shared.NewDomainError(shared.ErrCodeValidation,
			"tiered price must have at least one tier")
	}
	switch mode {
	case TieredPricingGraduated, TieredPricingVolume:
		// valid
	default:
		return TieredPrice{}, shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("tiered price has unknown mode %q", mode))
	}

	currency := tiers[0].UnitPrice.Currency()
	lastIdx := len(tiers) - 1
	var prevUpTo int64
	for i, tier := range tiers {
		isLast := i == lastIdx

		// UpTo == 0 means unlimited and is only valid on the final tier.
		if tier.UpTo == 0 {
			if !isLast {
				return TieredPrice{}, shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("tiered price: unlimited tier (UpTo=0) is only allowed as the last tier, found at index %d of %d", i, len(tiers)))
			}
		} else {
			if tier.UpTo < 0 {
				return TieredPrice{}, shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("tiered price: tier at index %d has negative UpTo %d", i, tier.UpTo))
			}
			// Strictly ascending among finite tiers. A finite tier after the
			// first must exceed its predecessor's UpTo (a non-final UpTo=0 is
			// already rejected above, so prevUpTo is always a finite bound here).
			if i > 0 && tier.UpTo <= prevUpTo {
				return TieredPrice{}, shared.NewDomainError(shared.ErrCodeValidation,
					fmt.Sprintf("tiered price: tiers must be sorted strictly ascending by UpTo, tier at index %d has UpTo %d <= previous %d", i, tier.UpTo, prevUpTo))
			}
			prevUpTo = tier.UpTo
		}

		// All tier currencies must match each other.
		if tier.UnitPrice.Currency() != currency {
			return TieredPrice{}, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("tiered price: tier at index %d has UnitPrice currency %s, expected %s", i, tier.UnitPrice.Currency(), currency))
		}
		if tier.FlatFee.Currency() != currency {
			return TieredPrice{}, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("tiered price: tier at index %d has FlatFee currency %s, expected %s", i, tier.FlatFee.Currency(), currency))
		}
	}

	return TieredPrice{Tiers: tiers, Mode: mode}, nil
}

// Clone returns a deep copy of the TieredPrice with an independent Tiers
// backing array (issue #196). PriceTier holds only value-type fields (int64 and
// Money, whose internal amount is never mutated in place), so copying the slice
// is sufficient — mutating the returned copy's tiers cannot alter the original's
// CalculatePrice results.
func (p TieredPrice) Clone() TieredPrice {
	tiers := make([]PriceTier, len(p.Tiers))
	copy(tiers, p.Tiers)
	return TieredPrice{Tiers: tiers, Mode: p.Mode}
}

// clonePricingModel returns a defensive copy of a PricingModel so a caller that
// mutates the returned value cannot reach back into a Price's internal model
// (issue #196). Only TieredPrice carries mutable backing state (its exported
// Tiers slice); FlatPrice and UsagePrice are value types whose fields are
// effectively immutable, so they are returned as-is. An unknown/custom model is
// returned unchanged: the library cannot copy a type it does not know, and
// third-party models are expected to be immutable.
func clonePricingModel(m PricingModel) PricingModel {
	switch tp := m.(type) {
	case TieredPrice:
		return tp.Clone()
	case *TieredPrice:
		if tp == nil {
			return m
		}
		c := tp.Clone()
		return &c
	default:
		return m
	}
}

// mustAddTier sums two Money values, panicking on a currency mismatch. By
// construction (NewTieredPrice validates that every tier shares one currency)
// this Add can never fail; a non-nil error therefore signals a TieredPrice built
// by bypassing the constructor with mixed-currency tiers — a caller bug that must
// surface loudly rather than be silently absorbed into a zero charge (which would
// bill the customer zero with no error). This is the same policy as
// assertNonNegativeUsage. See issue #156.
func mustAddTier(a, b shared.Money) shared.Money {
	sum, err := a.Add(b)
	if err != nil {
		panic(fmt.Sprintf("TieredPrice.CalculatePrice: money addition failed (mixed-currency tiers — construct via NewTieredPrice): %v", err))
	}
	return sum
}

// CalculatePrice calculates the price based on the tiered pricing mode.
// Zero usage (or no configured tiers) returns zero money; negative usage
// panics (see the PricingModel contract and assertNonNegativeUsage).
func (p TieredPrice) CalculatePrice(usage int64) shared.Money {
	assertNonNegativeUsage("TieredPrice", usage)
	if len(p.Tiers) == 0 || usage == 0 {
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

		total = mustAddTier(total, tierTotal)

		// Add flat fee if any units fall in this tier
		if unitsInTier > 0 {
			total = mustAddTier(total, tier.FlatFee)
		}

		remaining -= unitsInTier
		prevUpTo = tier.UpTo
	}

	// Overflow guard: if usage exceeds the capacity of all finite tiers (no
	// unlimited final tier with UpTo==0), charge the remaining units at the last
	// tier's rate instead of silently dropping them (review M2). This mirrors
	// calculateVolume's last-tier fallback.
	if remaining > 0 {
		lastTier := p.Tiers[len(p.Tiers)-1]
		factor := new(big.Rat).SetInt64(remaining)
		overflow := lastTier.UnitPrice.Multiply(factor)
		total = mustAddTier(total, overflow)
	}

	return total
}

func (p TieredPrice) calculateVolume(usage int64) shared.Money {
	for _, tier := range p.Tiers {
		if tier.UpTo == 0 || usage <= tier.UpTo {
			factor := new(big.Rat).SetInt64(usage)
			unitTotal := tier.UnitPrice.Multiply(factor)
			return mustAddTier(unitTotal, tier.FlatFee)
		}
	}

	// Usage exceeds all tiers; use the last tier
	lastTier := p.Tiers[len(p.Tiers)-1]
	factor := new(big.Rat).SetInt64(usage)
	unitTotal := lastTier.UnitPrice.Multiply(factor)
	return mustAddTier(unitTotal, lastTier.FlatFee)
}
