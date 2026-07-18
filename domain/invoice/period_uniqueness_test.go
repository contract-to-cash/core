package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// TestParticipatesInPeriodUniqueness pins the exemption predicate shared by the
// Repository.Save per-period uniqueness constraint and the BillingService
// duplicate-invoice guards (issue #232): an invoice participates only when it
// is not voided, not a proration adjustment, and has a non-zero billing period.
func TestParticipatesInPeriodUniqueness(t *testing.T) {
	period, err := shared.NewDateRange(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("failed to create period: %v", err)
	}
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	zero := shared.Zero(shared.CurrencyJPY)

	newInv := func(t *testing.T, opts ...InvoiceOption) *Invoice {
		t.Helper()
		inv, err := NewInvoice(shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
			amount, zero, zero, opts...)
		if err != nil {
			t.Fatalf("failed to create invoice: %v", err)
		}
		return inv
	}

	t.Run("regular invoice with period participates", func(t *testing.T) {
		inv := newInv(t, WithBillingPeriod(period))
		if !inv.ParticipatesInPeriodUniqueness() {
			t.Error("regular non-voided invoice with a billing period must participate")
		}
	})

	t.Run("voided invoice is exempt", func(t *testing.T) {
		inv := newInv(t, WithBillingPeriod(period))
		if err := inv.Void(); err != nil {
			t.Fatalf("void failed: %v", err)
		}
		if inv.ParticipatesInPeriodUniqueness() {
			t.Error("voided invoice must be exempt (void-and-recreate leaves it alongside its replacement)")
		}
	})

	t.Run("proration invoice is exempt", func(t *testing.T) {
		inv := newInv(t, WithBillingPeriod(period), WithMetadata(map[string]string{
			MetadataKeyInvoiceType: InvoiceTypeProration,
		}))
		if inv.ParticipatesInPeriodUniqueness() {
			t.Error("proration invoice must be exempt (coexists with the period's regular invoice)")
		}
	})

	t.Run("regeneration replacement participates", func(t *testing.T) {
		inv := newInv(t, WithBillingPeriod(period), WithMetadata(map[string]string{
			MetadataKeyInvoiceType: InvoiceTypeRegeneration,
		}))
		if !inv.ParticipatesInPeriodUniqueness() {
			t.Error("a regeneration replacement IS a regular period invoice and must participate")
		}
	})

	t.Run("zero billing period is exempt", func(t *testing.T) {
		inv := newInv(t)
		if inv.ParticipatesInPeriodUniqueness() {
			t.Error("invoice without a billing period has no period slot to occupy and must be exempt")
		}
	})
}
