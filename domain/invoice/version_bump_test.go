package invoice

import (
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// TestInvoice_MutatingMethods_BumpVersion asserts that EVERY state-mutating
// method on Invoice increments the optimistic-locking version (issue #147).
// Without this, a compliant optimistic-locking repository cannot detect lost
// updates on those paths.
//
// Helpers newDraftInvoice() and jpy() are shared with void_test.go /
// line_item_test.go in this package.
func TestInvoice_MutatingMethods_BumpVersion(t *testing.T) {
	t.Parallel()

	// Each case builds an invoice in a state where the method under test is
	// legal, records the version, invokes the mutator, and asserts the version
	// advanced by exactly one.
	tests := []struct {
		name   string
		build  func(t *testing.T) *Invoice
		mutate func(t *testing.T, inv *Invoice)
	}{
		{
			name:  "Finalize",
			build: func(t *testing.T) *Invoice { return newDraftInvoice() },
			mutate: func(t *testing.T, inv *Invoice) {
				if err := inv.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
			},
		},
		{
			name: "RecordPayment",
			build: func(t *testing.T) *Invoice {
				inv := newDraftInvoice()
				if err := inv.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				return inv
			},
			mutate: func(t *testing.T, inv *Invoice) {
				if err := inv.RecordPayment(jpy(10000), time.Now()); err != nil {
					t.Fatalf("RecordPayment: %v", err)
				}
			},
		},
		{
			name:  "Void",
			build: func(t *testing.T) *Invoice { return newDraftInvoice() },
			mutate: func(t *testing.T, inv *Invoice) {
				if err := inv.Void(); err != nil {
					t.Fatalf("Void: %v", err)
				}
			},
		},
		{
			name: "VoidWithReason",
			build: func(t *testing.T) *Invoice {
				inv := newDraftInvoice()
				if err := inv.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				return inv
			},
			mutate: func(t *testing.T, inv *Invoice) {
				if err := inv.VoidWithReason("credit note issued"); err != nil {
					t.Fatalf("VoidWithReason: %v", err)
				}
			},
		},
		{
			name: "MarkRefunded",
			build: func(t *testing.T) *Invoice {
				inv := newDraftInvoice()
				if err := inv.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				if err := inv.RecordPayment(jpy(10000), time.Now()); err != nil {
					t.Fatalf("RecordPayment: %v", err)
				}
				return inv
			},
			mutate: func(t *testing.T, inv *Invoice) {
				if err := inv.MarkRefunded("customer request"); err != nil {
					t.Fatalf("MarkRefunded: %v", err)
				}
			},
		},
		{
			name:  "SetRevisionOf",
			build: func(t *testing.T) *Invoice { return newDraftInvoice() },
			mutate: func(t *testing.T, inv *Invoice) {
				inv.SetRevisionOf(shared.NewInvoiceID())
			},
		},
		{
			name:  "SetOriginalInvoiceID",
			build: func(t *testing.T) *Invoice { return newDraftInvoice() },
			mutate: func(t *testing.T, inv *Invoice) {
				inv.SetOriginalInvoiceID(shared.NewInvoiceID())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inv := tc.build(t)
			before := inv.Version()
			tc.mutate(t, inv)
			if got := inv.Version(); got != before+1 {
				t.Errorf("%s: version = %d, want %d (before %d)", tc.name, got, before+1, before)
			}
		})
	}
}

// TestCreditNote_MutatingMethods_BumpVersion asserts that EVERY state-mutating
// method on CreditNote increments the optimistic-locking version (issue #147).
//
// newTestCreditNote() (draft, total 5500 JPY) is shared with credit_note_test.go.
func TestCreditNote_MutatingMethods_BumpVersion(t *testing.T) {
	t.Parallel()

	issued := func(t *testing.T) *CreditNote {
		cn := newTestCreditNote()
		if err := cn.Issue(time.Now()); err != nil {
			t.Fatalf("Issue: %v", err)
		}
		return cn
	}

	tests := []struct {
		name   string
		build  func(t *testing.T) *CreditNote
		mutate func(t *testing.T, cn *CreditNote)
	}{
		{
			name:  "Issue",
			build: func(t *testing.T) *CreditNote { return newTestCreditNote() },
			mutate: func(t *testing.T, cn *CreditNote) {
				if err := cn.Issue(time.Now()); err != nil {
					t.Fatalf("Issue: %v", err)
				}
			},
		},
		{
			name:  "Apply",
			build: issued,
			mutate: func(t *testing.T, cn *CreditNote) {
				if err := cn.Apply(jpy(5000)); err != nil {
					t.Fatalf("Apply: %v", err)
				}
			},
		},
		{
			name:  "Refund",
			build: issued,
			mutate: func(t *testing.T, cn *CreditNote) {
				if err := cn.Refund(jpy(5000)); err != nil {
					t.Fatalf("Refund: %v", err)
				}
			},
		},
		{
			name:  "Void",
			build: issued,
			mutate: func(t *testing.T, cn *CreditNote) {
				if err := cn.Void(); err != nil {
					t.Fatalf("Void: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cn := tc.build(t)
			before := cn.Version()
			tc.mutate(t, cn)
			if got := cn.Version(); got != before+1 {
				t.Errorf("%s: version = %d, want %d (before %d)", tc.name, got, before+1, before)
			}
		})
	}
}

// The snapshot round-trip version-preservation checks live in the
// forbidigo-allowlisted snapshot test files (snapshot_test.go /
// credit_note_snapshot_test.go), which are permitted to call the Snapshot APIs.
