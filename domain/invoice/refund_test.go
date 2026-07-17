package invoice

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// newInvoiceWithStatus builds an invoice in the given status for
// state-transition tests. WithStatus accepts only Draft (issue #238), so the
// fixture drives the invoice to its target status through the real
// transitions (see advanceInvoiceTo in status_fixture_test.go).
func newInvoiceWithStatus(t *testing.T, status InvoiceStatus) *Invoice {
	t.Helper()
	opts := fixtureOptionsFor(status, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	inv := mustNewInvoice(t,
		shared.NewInvoiceID(),
		shared.NewAccountID(),
		shared.NewContractID(),
		shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		shared.Zero(shared.CurrencyJPY),
		opts...,
	)
	advanceInvoiceTo(t, inv, status)
	return inv
}

func TestMarkRefunded_FromPaid(t *testing.T) {
	inv := newInvoiceWithStatus(t, InvoiceStatusPaid)
	before := inv.Version()

	if err := inv.MarkRefunded("customer requested full refund"); err != nil {
		t.Fatalf("unexpected error refunding paid invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusRefunded {
		t.Errorf("expected refunded, got %s", inv.Status())
	}
	if inv.RefundReason() != "customer requested full refund" {
		t.Errorf("expected refund reason set, got %q", inv.RefundReason())
	}
	// Like Finalize, MarkRefunded bumps the optimistic-locking version (#130).
	if inv.Version() != before+1 {
		t.Errorf("expected version %d, got %d", before+1, inv.Version())
	}
}

func TestMarkRefunded_FromPartialPaid(t *testing.T) {
	inv := newInvoiceWithStatus(t, InvoiceStatusPartialPaid)

	if err := inv.MarkRefunded("partial payment refunded"); err != nil {
		t.Fatalf("unexpected error refunding partial_paid invoice: %v", err)
	}
	if inv.Status() != InvoiceStatusRefunded {
		t.Errorf("expected refunded, got %s", inv.Status())
	}
}

func TestMarkRefunded_EmptyReason_Rejected(t *testing.T) {
	inv := newInvoiceWithStatus(t, InvoiceStatusPaid)

	err := inv.MarkRefunded("")
	if err == nil {
		t.Fatal("expected error for empty refund reason, got nil")
	}
	assertDomainCode(t, err, shared.ErrCodeValidation)
	if inv.Status() != InvoiceStatusPaid {
		t.Errorf("status must not change on rejected refund, got %s", inv.Status())
	}
}

func TestMarkRefunded_InvalidSourceStates_Rejected(t *testing.T) {
	// Every state other than Paid / PartialPaid must be rejected, including
	// Voided (a distinct terminal state that never collected funds) and
	// Refunded (already terminal — guards against double refund).
	invalid := []InvoiceStatus{
		InvoiceStatusDraft,
		InvoiceStatusFinalized,
		InvoiceStatusIssued,
		InvoiceStatusOverdue,
		InvoiceStatusVoided,
		InvoiceStatusRefunded,
	}
	for _, status := range invalid {
		t.Run(string(status), func(t *testing.T) {
			inv := newInvoiceWithStatus(t, status)
			before := inv.Version()

			err := inv.MarkRefunded("attempted refund")
			if err == nil {
				t.Fatalf("expected error refunding invoice in status %s, got nil", status)
			}
			assertDomainCode(t, err, shared.ErrCodeInvalidStateTransition)
			if inv.Status() != status {
				t.Errorf("status must not change on rejected refund: expected %s, got %s", status, inv.Status())
			}
			if inv.Version() != before {
				t.Errorf("version must not change on rejected refund: expected %d, got %d", before, inv.Version())
			}
		})
	}
}

// assertDomainCode asserts that err is a *shared.DomainError with the given code.
func assertDomainCode(t *testing.T, err error, want shared.ErrorCode) {
	t.Helper()
	var de *shared.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *shared.DomainError, got %T (%v)", err, err)
	}
	if de.Code != want {
		t.Errorf("expected error code %s, got %s", want, de.Code)
	}
}
