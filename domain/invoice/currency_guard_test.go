package invoice

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// These tests cover the currency / sign guards added for issue #148.

func issuedTestCreditNote(t *testing.T) *CreditNote {
	t.Helper()
	cn := newTestCreditNote() // total = jpy(5000) + jpy(500) tax = jpy(5500)
	if err := cn.Issue(time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("issue: %v", err)
	}
	return cn
}

func TestCreditNote_Apply_CurrencyAndSignGuards(t *testing.T) {
	tests := []struct {
		name    string
		amount  shared.Money
		wantErr shared.ErrorCode // "" means success
	}{
		{"negative", jpy(-100), shared.ErrCodeValidation},
		{"zero", jpy(0), shared.ErrCodeValidation},
		{"wrong currency", usd(1000000), shared.ErrCodeCurrencyMismatch},
		{"exceeds total", jpy(999999), shared.ErrCodeBusinessRule},
		{"happy partial", jpy(1000), ""},
		{"happy full", jpy(5500), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cn := issuedTestCreditNote(t)
			err := cn.Apply(tc.amount)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cn.Status() != CreditNoteStatusApplied {
					t.Errorf("expected status applied, got %s", cn.Status())
				}
				if cn.CreditAmount().Amount().Cmp(tc.amount.Amount()) != 0 {
					t.Errorf("expected creditAmount %s, got %s",
						tc.amount.Amount().RatString(), cn.CreditAmount().Amount().RatString())
				}
				return
			}
			assertDomainCode(t, err, tc.wantErr)
			// Rejected transitions must not mutate state.
			if cn.Status() != CreditNoteStatusIssued {
				t.Errorf("expected status unchanged (issued), got %s", cn.Status())
			}
			if !cn.CreditAmount().IsZero() {
				t.Errorf("expected creditAmount unchanged (zero), got %s", cn.CreditAmount().Amount().RatString())
			}
		})
	}
}

func TestCreditNote_Refund_CurrencyAndSignGuards(t *testing.T) {
	tests := []struct {
		name    string
		amount  shared.Money
		wantErr shared.ErrorCode
	}{
		{"negative", jpy(-100), shared.ErrCodeValidation},
		{"zero", jpy(0), shared.ErrCodeValidation},
		{"wrong currency", usd(1000000), shared.ErrCodeCurrencyMismatch},
		{"exceeds total", jpy(999999), shared.ErrCodeBusinessRule},
		{"happy partial", jpy(1000), ""},
		{"happy full", jpy(5500), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cn := issuedTestCreditNote(t)
			err := cn.Refund(tc.amount)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cn.Status() != CreditNoteStatusRefunded {
					t.Errorf("expected status refunded, got %s", cn.Status())
				}
				if cn.RefundAmount().Amount().Cmp(tc.amount.Amount()) != 0 {
					t.Errorf("expected refundAmount %s, got %s",
						tc.amount.Amount().RatString(), cn.RefundAmount().Amount().RatString())
				}
				return
			}
			assertDomainCode(t, err, tc.wantErr)
			if cn.Status() != CreditNoteStatusIssued {
				t.Errorf("expected status unchanged (issued), got %s", cn.Status())
			}
			if !cn.RefundAmount().IsZero() {
				t.Errorf("expected refundAmount unchanged (zero), got %s", cn.RefundAmount().Amount().RatString())
			}
		})
	}
}

// A successful Apply must still bump the optimistic-locking version (issue #147),
// which the currency/sign guard added in #148 must not regress.
func TestCreditNote_Apply_BumpsVersionOnSuccess(t *testing.T) {
	cn := issuedTestCreditNote(t)
	before := cn.Version()
	if err := cn.Apply(jpy(1000)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn.Version() != before+1 {
		t.Errorf("expected version %d, got %d", before+1, cn.Version())
	}
}

func TestNewCreditNote_RejectsNonPositiveItemAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  shared.Money
		wantErr shared.ErrorCode
	}{
		{"negative item", jpy(-500), shared.ErrCodeValidation},
		{"zero item", jpy(0), shared.ErrCodeValidation},
		{"positive item", jpy(500), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cn, err := NewCreditNote(
				shared.NewCreditNoteID(),
				shared.NewInvoiceID(),
				shared.NewAccountID(),
				shared.NewContractID(),
				CreditNoteReasonOrderChange,
				[]CreditNoteItem{
					NewCreditNoteItem("li-1", "Adjustment", tc.amount, big.NewRat(10, 100), jpy(0)),
				},
				time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC),
			)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cn == nil {
					t.Fatal("expected non-nil credit note")
				}
				return
			}
			if cn != nil {
				t.Error("expected nil credit note on rejection")
			}
			assertDomainCode(t, err, tc.wantErr)
		})
	}
}

func TestInvoice_ValidatePayment_SignAndCurrencyGuards(t *testing.T) {
	newFinalized := func(t *testing.T, subtotal shared.Money) *Invoice {
		t.Helper()
		inv, err := NewInvoice(
			shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
			subtotal, shared.Zero(subtotal.Currency()), shared.Zero(subtotal.Currency()),
			WithStatus(InvoiceStatusFinalized),
		)
		if err != nil {
			t.Fatalf("new invoice: %v", err)
		}
		return inv
	}

	t.Run("negative rejected", func(t *testing.T) {
		inv := newFinalized(t, jpy(1000))
		assertDomainCode(t, inv.ValidatePayment(jpy(-1)), shared.ErrCodeValidation)
	})

	t.Run("wrong currency rejected", func(t *testing.T) {
		inv := newFinalized(t, jpy(1000))
		assertDomainCode(t, inv.ValidatePayment(usd(1000)), shared.ErrCodeCurrencyMismatch)
	})

	t.Run("full payment accepted", func(t *testing.T) {
		inv := newFinalized(t, jpy(1000))
		if err := inv.ValidatePayment(jpy(1000)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// A zero-amount invoice (e.g. fully discounted) must accept a zero payment,
	// which is exactly what PaymentService.ProcessPayment submits (issue #148).
	t.Run("zero payment on zero-amount invoice accepted", func(t *testing.T) {
		inv := newFinalized(t, jpy(0))
		if err := inv.ValidatePayment(jpy(0)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// Guard the specific regression from the issue: with partial payment allowed,
	// a negative payment previously slipped through and decreased paidAmount.
	t.Run("negative rejected even when partial payment allowed", func(t *testing.T) {
		inv, err := NewInvoice(
			shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
			jpy(1000), jpy(0), jpy(0),
			WithStatus(InvoiceStatusFinalized), WithAllowPartialPayment(true),
		)
		if err != nil {
			t.Fatalf("new invoice: %v", err)
		}
		assertDomainCode(t, inv.ValidatePayment(jpy(-500)), shared.ErrCodeValidation)
	})
}

func TestNewInvoice_WithAppliedBalance_SurfacesCurrencyMismatch(t *testing.T) {
	t.Run("mismatch surfaced", func(t *testing.T) {
		inv, err := NewInvoice(
			shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
			jpy(1000), jpy(0), jpy(0),
			WithAppliedBalance(usd(100)),
		)
		if inv != nil {
			t.Error("expected nil invoice on currency mismatch")
		}
		assertDomainCode(t, err, shared.ErrCodeCurrencyMismatch)
	})

	t.Run("matching currency recalculates amountDue", func(t *testing.T) {
		inv, err := NewInvoice(
			shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
			jpy(1000), jpy(0), jpy(0),
			WithAppliedBalance(jpy(300)),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inv.AmountDue().Amount().Cmp(big.NewRat(700, 1)) != 0 {
			t.Errorf("expected amountDue 700, got %s", inv.AmountDue().Amount().RatString())
		}
		if inv.Balance().Amount().Cmp(big.NewRat(700, 1)) != 0 {
			t.Errorf("expected balance 700, got %s", inv.Balance().Amount().RatString())
		}
	})
}
