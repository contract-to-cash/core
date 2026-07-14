package payment

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestNewPayment(t *testing.T) {
	id := shared.NewPaymentID()
	invoiceID := shared.NewInvoiceID()
	amount := shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY)
	method := PaymentMethodCreditCard
	gatewayTxID := "gw_tx_12345"
	processedAt := time.Now().UTC()

	p, _ := NewPayment(id, invoiceID, amount, method, gatewayTxID, processedAt)

	if p.ID() != id {
		t.Errorf("expected id %s, got %s", id, p.ID())
	}
	if p.InvoiceID() != invoiceID {
		t.Errorf("expected invoiceID %s, got %s", invoiceID, p.InvoiceID())
	}
	if p.Amount().Amount().Cmp(big.NewRat(5000, 1)) != 0 {
		t.Errorf("expected amount 5000, got %s", p.Amount().Amount().RatString())
	}
	if p.Method() != PaymentMethodCreditCard {
		t.Errorf("expected method credit_card, got %s", p.Method())
	}
	if p.Status() != PaymentStatusPending {
		t.Errorf("expected status pending, got %s", p.Status())
	}
	if p.GatewayTransactionID() != gatewayTxID {
		t.Errorf("expected gateway tx id %s, got %s", gatewayTxID, p.GatewayTransactionID())
	}
	if p.FailureReason() != nil {
		t.Error("expected failure reason to be nil")
	}
	if p.ProcessedAt() != processedAt {
		t.Errorf("expected processedAt %v, got %v", processedAt, p.ProcessedAt())
	}
	if p.Metadata() == nil {
		t.Error("expected metadata to be initialized")
	}
}

func TestPaymentStatus_Constants(t *testing.T) {
	statuses := []PaymentStatus{
		PaymentStatusPending,
		PaymentStatusCompleted,
		PaymentStatusFailed,
		PaymentStatusPartiallyRefunded,
		PaymentStatusRefunded,
		PaymentStatusChargedBack,
	}

	expected := []string{
		"pending", "completed", "failed",
		"partially_refunded", "refunded", "charged_back",
	}

	for i, s := range statuses {
		if string(s) != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], s)
		}
	}
}

func newTestPayment() *Payment {
	p, err := NewPayment(
		shared.NewPaymentID(),
		shared.NewInvoiceID(),
		shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		PaymentMethodCreditCard,
		"gw_tx_test",
		time.Now().UTC(),
	)
	if err != nil {
		panic("newTestPayment: " + err.Error())
	}
	return p
}

func completePayment(t *testing.T, p *Payment) {
	t.Helper()
	if err := p.Complete(); err != nil {
		t.Fatalf("setup: failed to complete payment: %v", err)
	}
}

func TestPayment_Complete(t *testing.T) {
	p := newTestPayment()

	if err := p.Complete(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusCompleted {
		t.Errorf("expected completed, got %s", p.Status())
	}
}

func TestPayment_Complete_InvalidState(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.Complete(); err == nil {
		t.Error("expected error completing already completed payment")
	}
}

func TestPayment_Fail(t *testing.T) {
	p := newTestPayment()
	reason := "insufficient_funds"

	if err := p.Fail(reason); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusFailed {
		t.Errorf("expected failed, got %s", p.Status())
	}
	if p.FailureReason() == nil || *p.FailureReason() != reason {
		t.Errorf("expected failure reason %q", reason)
	}
}

func TestPayment_Fail_InvalidState(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.Fail("test"); err == nil {
		t.Error("expected error failing completed payment")
	}
}

func TestPayment_MarkRefunded_FromCompleted(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.MarkRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
}

func TestPayment_MarkRefunded_FromPartiallyRefunded(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)
	if err := p.MarkPartiallyRefunded(); err != nil {
		t.Fatalf("setup: failed to partially refund: %v", err)
	}

	if err := p.MarkRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
}

func TestPayment_MarkRefunded_InvalidState(t *testing.T) {
	p := newTestPayment()

	if err := p.MarkRefunded(); err == nil {
		t.Error("expected error refunding pending payment")
	}
}

func TestPayment_MarkPartiallyRefunded(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.MarkPartiallyRefunded(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded, got %s", p.Status())
	}
}

func TestPayment_MarkPartiallyRefunded_InvalidState(t *testing.T) {
	p := newTestPayment()

	if err := p.MarkPartiallyRefunded(); err == nil {
		t.Error("expected error partially refunding pending payment")
	}
}

func TestPayment_MarkChargedBack(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	if err := p.MarkChargedBack(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusChargedBack {
		t.Errorf("expected charged_back, got %s", p.Status())
	}
}

func TestPayment_MarkChargedBack_InvalidState(t *testing.T) {
	p := newTestPayment()

	if err := p.MarkChargedBack(); err == nil {
		t.Error("expected error charging back pending payment")
	}
}

func TestPayment_Metadata_ReturnsCopy(t *testing.T) {
	p := newTestPayment()
	meta := p.Metadata()
	meta["key"] = "value"

	if len(p.Metadata()) != 0 {
		t.Error("Metadata() should return a copy, not a reference")
	}
}

func TestPayment_SetMetadata(t *testing.T) {
	p := newTestPayment()
	before := p.Version()

	p.SetMetadata(MetadataKeyInstructionsURL, "https://gw.example.com/voucher/1")
	p.SetMetadata("custom", "value")

	meta := p.Metadata()
	if meta[MetadataKeyInstructionsURL] != "https://gw.example.com/voucher/1" {
		t.Errorf("expected instructions URL in metadata, got %q", meta[MetadataKeyInstructionsURL])
	}
	if meta["custom"] != "value" {
		t.Errorf("expected custom metadata, got %q", meta["custom"])
	}
	// Initialization-time setter: no optimistic-locking version bump
	// (mirrors SetIdempotencyKey).
	if p.Version() != before {
		t.Errorf("SetMetadata must not bump the version: %d -> %d", before, p.Version())
	}
}

// --- RecordRefund tests ---

func TestPayment_RecordRefund_FullRefund(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	err := p.RecordRefund(p.Amount())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded, got %s", p.Status())
	}
	if p.RefundedAmount().Amount().Cmp(p.Amount().Amount()) != 0 {
		t.Errorf("expected refundedAmount == amount")
	}
}

// TestPayment_RecordRefund_NegativeAmount_Rejected guards a financial invariant:
// a negative refund would otherwise DECREASE the cumulative refunded total and
// could flip status refunded -> partially_refunded. See review M3.
func TestPayment_RecordRefund_NegativeAmount_Rejected(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	neg := shared.NewMoney(big.NewRat(-1000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(neg)
	if err == nil {
		t.Fatal("expected error for negative refund amount, got nil")
	}
	if p.RefundedAmount().Amount().Sign() != 0 {
		t.Errorf("refunded amount changed after rejected refund: %s", p.RefundedAmount().Amount().RatString())
	}
	if p.Status() != PaymentStatusCompleted {
		t.Errorf("status changed after rejected refund: %s", p.Status())
	}
}

func TestPayment_RecordRefund_PartialRefund(t *testing.T) {
	p := newTestPayment()
	completePayment(t, p)

	partial := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(partial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded, got %s", p.Status())
	}
	if p.RefundedAmount().Amount().Cmp(big.NewRat(2000, 1)) != 0 {
		t.Errorf("expected refundedAmount 2000, got %s", p.RefundedAmount().Amount().RatString())
	}
}

func TestPayment_RecordRefund_CumulativePartialRefunds(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	partial := shared.NewMoney(big.NewRat(2000, 1), shared.CurrencyJPY)

	// First partial refund
	if err := p.RecordRefund(partial); err != nil {
		t.Fatalf("first refund failed: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded after first refund, got %s", p.Status())
	}

	// Second partial refund from partially_refunded state
	if err := p.RecordRefund(partial); err != nil {
		t.Fatalf("second refund failed: %v", err)
	}
	if p.Status() != PaymentStatusPartiallyRefunded {
		t.Errorf("expected partially_refunded after second refund, got %s", p.Status())
	}
	if p.RefundedAmount().Amount().Cmp(big.NewRat(4000, 1)) != 0 {
		t.Errorf("expected cumulative refundedAmount 4000, got %s", p.RefundedAmount().Amount().RatString())
	}

	// Final refund to reach full amount
	remaining := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	if err := p.RecordRefund(remaining); err != nil {
		t.Fatalf("final refund failed: %v", err)
	}
	if p.Status() != PaymentStatusRefunded {
		t.Errorf("expected refunded after final refund, got %s", p.Status())
	}
}

func TestPayment_RecordRefund_ExceedsAmount(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	excess := shared.NewMoney(big.NewRat(6000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(excess)
	if err == nil {
		t.Fatal("expected error when refund exceeds payment amount")
	}
}

func TestPayment_RecordRefund_CumulativeExceedsAmount(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	partial := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	if err := p.RecordRefund(partial); err != nil {
		t.Fatalf("first refund failed: %v", err)
	}

	// Second refund would exceed total
	excess := shared.NewMoney(big.NewRat(3000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(excess)
	if err == nil {
		t.Fatal("expected error when cumulative refund exceeds payment amount")
	}
}

func TestPayment_RecordRefund_InvalidState(t *testing.T) {
	// Pending payment cannot be refunded
	p := newTestPayment()
	refundAmt := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	err := p.RecordRefund(refundAmt)
	if err == nil {
		t.Fatal("expected error refunding pending payment")
	}

	// Failed payment cannot be refunded
	p2 := newTestPayment()
	_ = p2.Fail("test")
	err = p2.RecordRefund(refundAmt)
	if err == nil {
		t.Fatal("expected error refunding failed payment")
	}
}

// --- IdempotencyKey tests ---

func TestPayment_IdempotencyKey(t *testing.T) {
	p := newTestPayment()
	if p.IdempotencyKey() != "" {
		t.Error("expected empty idempotency key by default")
	}

	p.SetIdempotencyKey("idem-123")
	if p.IdempotencyKey() != "idem-123" {
		t.Errorf("expected idem-123, got %s", p.IdempotencyKey())
	}
}

func TestPaymentMethod_Constants(t *testing.T) {
	methods := []PaymentMethod{
		PaymentMethodCreditCard,
		PaymentMethodBankTransfer,
		PaymentMethodDirectDebit,
		PaymentMethodConvenience,
		PaymentMethodCarrier,
	}

	expected := []string{
		"credit_card", "bank_transfer", "direct_debit",
		"convenience_store", "carrier",
	}

	for i, m := range methods {
		if string(m) != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], m)
		}
	}
}

// --- Exhaustive invalid state transition tests ---

// paymentInState returns a Payment set to the given status via valid transitions.
func paymentInState(t *testing.T, status PaymentStatus) *Payment {
	t.Helper()
	p := newTestPayment()
	switch status {
	case PaymentStatusPending:
		// already pending
	case PaymentStatusCompleted:
		if err := p.Complete(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	case PaymentStatusFailed:
		if err := p.Fail("test"); err != nil {
			t.Fatalf("setup: %v", err)
		}
	case PaymentStatusPartiallyRefunded:
		if err := p.Complete(); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := p.MarkPartiallyRefunded(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	case PaymentStatusRefunded:
		if err := p.Complete(); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := p.MarkRefunded(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	case PaymentStatusChargedBack:
		if err := p.Complete(); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := p.MarkChargedBack(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	default:
		t.Fatalf("unknown status: %s", status)
	}
	return p
}

func TestPayment_Complete_AllInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status PaymentStatus
	}{
		{"from completed", PaymentStatusCompleted},
		{"from failed", PaymentStatusFailed},
		{"from partially_refunded", PaymentStatusPartiallyRefunded},
		{"from refunded", PaymentStatusRefunded},
		{"from charged_back", PaymentStatusChargedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paymentInState(t, tt.status)
			if err := p.Complete(); err == nil {
				t.Errorf("expected error calling Complete() from %s", tt.status)
			}
		})
	}
}

func TestPayment_Fail_AllInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status PaymentStatus
	}{
		{"from completed", PaymentStatusCompleted},
		{"from failed", PaymentStatusFailed},
		{"from partially_refunded", PaymentStatusPartiallyRefunded},
		{"from refunded", PaymentStatusRefunded},
		{"from charged_back", PaymentStatusChargedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paymentInState(t, tt.status)
			if err := p.Fail("test"); err == nil {
				t.Errorf("expected error calling Fail() from %s", tt.status)
			}
		})
	}
}

func TestPayment_MarkRefunded_AllInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status PaymentStatus
	}{
		{"from pending", PaymentStatusPending},
		{"from failed", PaymentStatusFailed},
		{"from refunded", PaymentStatusRefunded},
		{"from charged_back", PaymentStatusChargedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paymentInState(t, tt.status)
			if err := p.MarkRefunded(); err == nil {
				t.Errorf("expected error calling MarkRefunded() from %s", tt.status)
			}
		})
	}
}

func TestPayment_MarkPartiallyRefunded_AllInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status PaymentStatus
	}{
		{"from pending", PaymentStatusPending},
		{"from failed", PaymentStatusFailed},
		{"from partially_refunded", PaymentStatusPartiallyRefunded},
		{"from refunded", PaymentStatusRefunded},
		{"from charged_back", PaymentStatusChargedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paymentInState(t, tt.status)
			if err := p.MarkPartiallyRefunded(); err == nil {
				t.Errorf("expected error calling MarkPartiallyRefunded() from %s", tt.status)
			}
		})
	}
}

func TestPayment_MarkChargedBack_AllInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status PaymentStatus
	}{
		{"from pending", PaymentStatusPending},
		{"from failed", PaymentStatusFailed},
		{"from refunded", PaymentStatusRefunded},
		{"from charged_back", PaymentStatusChargedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paymentInState(t, tt.status)
			if err := p.MarkChargedBack(); err == nil {
				t.Errorf("expected error calling MarkChargedBack() from %s", tt.status)
			}
		})
	}
}

// TestPayment_MarkChargedBack_FromPartiallyRefunded verifies a chargeback is
// permitted after a partial refund: real-world chargebacks routinely follow a
// partial refund when the customer disputes the remaining charge (issue #162 L-9).
func TestPayment_MarkChargedBack_FromPartiallyRefunded(t *testing.T) {
	p := paymentInState(t, PaymentStatusPartiallyRefunded)
	if err := p.MarkChargedBack(); err != nil {
		t.Fatalf("unexpected error charging back a partially_refunded payment: %v", err)
	}
	if p.Status() != PaymentStatusChargedBack {
		t.Errorf("expected charged_back, got %s", p.Status())
	}
}

func TestPayment_RecordRefund_AllInvalidStates(t *testing.T) {
	tests := []struct {
		name   string
		status PaymentStatus
	}{
		{"from pending", PaymentStatusPending},
		{"from failed", PaymentStatusFailed},
		{"from refunded", PaymentStatusRefunded},
		{"from charged_back", PaymentStatusChargedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paymentInState(t, tt.status)
			refundAmt := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
			if err := p.RecordRefund(refundAmt); err == nil {
				t.Errorf("expected error calling RecordRefund() from %s", tt.status)
			}
		})
	}
}

func TestPayment_RecordRefund_ZeroAmount(t *testing.T) {
	p := newTestPayment() // amount = 5000
	completePayment(t, p)

	zero := shared.NewMoney(big.NewRat(0, 1), shared.CurrencyJPY)
	// A zero refund is meaningless and previously flipped status from completed
	// to partially_refunded (a spurious state change). RecordRefund now rejects
	// non-positive amounts, so the payment must be left untouched (review M3).
	err := p.RecordRefund(zero)
	if err == nil {
		t.Fatal("expected error for zero refund amount, got nil")
	}
	if p.Status() != PaymentStatusCompleted {
		t.Errorf("expected status unchanged (completed) after rejected zero refund, got %s", p.Status())
	}
	if !p.RefundedAmount().IsZero() {
		t.Errorf("expected refundedAmount to be zero, got %s", p.RefundedAmount().Amount().RatString())
	}
}
