package service

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

// --- Payment instructions propagation (port.ChargeResponse.Instructions →
// pending Payment.Metadata under the reserved payment.MetadataKeyInstructions*
// keys). These pin the boundary fix for async/push payment methods: the
// customer-facing instruction (konbini voucher URL, virtual-account summary,
// payment deadline) must survive persistUnsettledCharge instead of being lost
// at the service boundary. ---

func fullTestInstructions() *port.PaymentInstructions {
	// Non-UTC zone on purpose: persistence must normalize to UTC RFC3339.
	jst := time.FixedZone("JST", 9*60*60)
	expires := time.Date(2026, 1, 22, 9, 0, 0, 0, jst)
	return &port.PaymentInstructions{
		Kind:      "konbini_voucher",
		URL:       "https://gw.example.com/voucher/abc123",
		Reference: "PAY-CODE-778899",
		ExpiresAt: &expires,
	}
}

func assertInstructionsMetadata(t *testing.T, p *payment.Payment) {
	t.Helper()
	meta := p.Metadata()
	if got := meta[payment.MetadataKeyInstructionsKind]; got != "konbini_voucher" {
		t.Errorf("expected instructions_kind %q, got %q", "konbini_voucher", got)
	}
	if got := meta[payment.MetadataKeyInstructionsURL]; got != "https://gw.example.com/voucher/abc123" {
		t.Errorf("expected instructions_url persisted, got %q", got)
	}
	if got := meta[payment.MetadataKeyInstructionsReference]; got != "PAY-CODE-778899" {
		t.Errorf("expected instructions_reference persisted, got %q", got)
	}
	// 2026-01-22 09:00 JST == 2026-01-22 00:00 UTC — pins UTC normalization.
	if got := meta[payment.MetadataKeyInstructionsExpiresAt]; got != "2026-01-22T00:00:00Z" {
		t.Errorf("expected instructions_expires_at as UTC RFC3339, got %q", got)
	}
}

func TestProcessPayment_PendingInstructions_PersistedOnMetadata(t *testing.T) {
	gw := &mockGateway{
		chargeStatus:            port.TransactionStatusPending,
		chargePaymentMethodType: port.PaymentMethodTypeConvenienceStore,
		chargeInstructions:      fullTestInstructions(),
	}
	f := newSettlementFixture(t, gw)

	pmt := f.processPending(t, "key-instr-pending")

	// The payment returned alongside ErrPaymentPending carries the instructions.
	assertInstructionsMetadata(t, pmt)

	// And they round-trip through the repository (the later settlement /
	// integrator notification loads the record, not the in-flight pointer).
	stored, err := f.payRepo.FindByIdempotencyKey(context.Background(), "key-instr-pending")
	if err != nil || stored == nil {
		t.Fatalf("expected persisted pending payment, got (%v, %v)", stored, err)
	}
	assertInstructionsMetadata(t, stored)
}

func TestProcessPayment_RequiresActionInstructions_PersistedOnMetadata(t *testing.T) {
	gw := &mockGateway{
		requiresAction:     true,
		threeDSRedirect:    "https://gw.example.com/3ds/redirect",
		chargeInstructions: fullTestInstructions(),
	}
	f := newSettlementFixture(t, gw)

	pmt, err := f.svc.ProcessPayment(context.Background(), f.inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-instr-3ds",
	})
	if !errors.Is(err, ErrRequiresAction) {
		t.Fatalf("expected ErrRequiresAction, got: %v", err)
	}
	if pmt == nil {
		t.Fatal("expected pending payment, got nil")
	}
	assertInstructionsMetadata(t, pmt)

	stored, findErr := f.payRepo.FindByIdempotencyKey(context.Background(), "key-instr-3ds")
	if findErr != nil || stored == nil {
		t.Fatalf("expected persisted pending payment, got (%v, %v)", stored, findErr)
	}
	assertInstructionsMetadata(t, stored)
}

func TestProcessPayment_NilInstructions_NoReservedMetadataKeys(t *testing.T) {
	gw := &mockGateway{chargeStatus: port.TransactionStatusPending} // Instructions == nil
	f := newSettlementFixture(t, gw)

	pmt := f.processPending(t, "key-instr-nil")

	meta := pmt.Metadata()
	for _, key := range []string{
		payment.MetadataKeyInstructionsKind,
		payment.MetadataKeyInstructionsURL,
		payment.MetadataKeyInstructionsReference,
		payment.MetadataKeyInstructionsExpiresAt,
	} {
		if v, ok := meta[key]; ok {
			t.Errorf("nil instructions must not set reserved key %q (got %q)", key, v)
		}
	}
}

func TestProcessPayment_PartialInstructions_OnlyNonEmptyKeysSet(t *testing.T) {
	gw := &mockGateway{
		chargeStatus: port.TransactionStatusPending,
		chargeInstructions: &port.PaymentInstructions{
			Kind: "hosted_page",
			URL:  "https://gw.example.com/pay/xyz",
			// Reference and ExpiresAt intentionally unset.
		},
	}
	f := newSettlementFixture(t, gw)

	pmt := f.processPending(t, "key-instr-partial")

	meta := pmt.Metadata()
	if meta[payment.MetadataKeyInstructionsKind] != "hosted_page" {
		t.Errorf("expected instructions_kind, got %q", meta[payment.MetadataKeyInstructionsKind])
	}
	if meta[payment.MetadataKeyInstructionsURL] != "https://gw.example.com/pay/xyz" {
		t.Errorf("expected instructions_url, got %q", meta[payment.MetadataKeyInstructionsURL])
	}
	if v, ok := meta[payment.MetadataKeyInstructionsReference]; ok {
		t.Errorf("empty Reference must not set a reserved key (got %q)", v)
	}
	if v, ok := meta[payment.MetadataKeyInstructionsExpiresAt]; ok {
		t.Errorf("nil ExpiresAt must not set a reserved key (got %q)", v)
	}
}

func TestProcessPayment_PendingInstructions_IdempotentReplay_PreservesMetadata(t *testing.T) {
	gw := &mockGateway{
		chargeStatus:       port.TransactionStatusPending,
		chargeInstructions: fullTestInstructions(),
	}
	f := newSettlementFixture(t, gw)

	first := f.processPending(t, "key-instr-replay")
	second := f.processPending(t, "key-instr-replay")

	if first.ID() != second.ID() {
		t.Fatalf("replay must return the existing pending payment: %s vs %s", first.ID(), second.ID())
	}
	// The replayed payment still carries the originally persisted instructions.
	assertInstructionsMetadata(t, second)

	stored, err := f.payRepo.FindByIdempotencyKey(context.Background(), "key-instr-replay")
	if err != nil || stored == nil {
		t.Fatalf("expected persisted pending payment, got (%v, %v)", stored, err)
	}
	assertInstructionsMetadata(t, stored)
}
