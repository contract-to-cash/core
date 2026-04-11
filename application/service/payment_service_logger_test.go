package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// --- Failing hook plugins for logger tests ---

type failingAfterChargePlugin struct {
	err error
}

func (p *failingAfterChargePlugin) Name() string                                        { return "failing-after-charge" }
func (p *failingAfterChargePlugin) Version() string                                     { return "1.0.0" }
func (p *failingAfterChargePlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *failingAfterChargePlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *failingAfterChargePlugin) Priority() int                                       { return 500 }
func (p *failingAfterChargePlugin) AfterCharge(_ *plugin.PaymentContext) error {
	return p.err
}

type failingOnRefundPlugin struct {
	err error
}

func (p *failingOnRefundPlugin) Name() string                                        { return "failing-on-refund" }
func (p *failingOnRefundPlugin) Version() string                                     { return "1.0.0" }
func (p *failingOnRefundPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *failingOnRefundPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *failingOnRefundPlugin) Priority() int                                       { return 500 }
func (p *failingOnRefundPlugin) OnRefund(_ *plugin.PaymentContext, _ shared.Money) error {
	return p.err
}

type failingOnPaymentFailedPlugin struct {
	err error
}

func (p *failingOnPaymentFailedPlugin) Name() string    { return "failing-on-payment-failed" }
func (p *failingOnPaymentFailedPlugin) Version() string { return "1.0.0" }
func (p *failingOnPaymentFailedPlugin) Initialize(_ context.Context, _ plugin.Config) error {
	return nil
}
func (p *failingOnPaymentFailedPlugin) Shutdown(_ context.Context) error { return nil }
func (p *failingOnPaymentFailedPlugin) Priority() int                    { return 500 }
func (p *failingOnPaymentFailedPlugin) OnPaymentFailed(_ *plugin.PaymentContext, _ error) error {
	return p.err
}

func TestWithPaymentLogger_AfterChargeHookError_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	hookErr := fmt.Errorf("webhook delivery failed")
	failPlugin := &failingAfterChargePlugin{err: hookErr}
	registry := plugin.NewRegistry()
	_ = registry.Register(failPlugin)

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		invRepo,
		nil,
		&mockEventStore{},
		registry,
		clock,
		WithPaymentLogger(logger),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-log-001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output for AfterCharge hook error, got nothing")
	}
	if !containsAll(logOutput, "AfterCharge hook failed", "webhook delivery failed") {
		t.Errorf("log output missing expected content, got: %s", logOutput)
	}
}

func TestWithPaymentLogger_InvoiceLookupErrorOnRefund_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	registry := plugin.NewRegistry()

	svc := NewPaymentService(
		&mockGateway{},
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		registry,
		clock,
		WithPaymentLogger(logger),
	)

	// First, create a payment
	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-refund-001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Make invoice repo fail for the refund lookup
	invRepo.inv = nil
	buf.Reset()

	err = svc.Refund(context.Background(), pmt.ID(), RefundInput{
		Reason: port.RefundReasonRequestedByCustomer,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output for invoice lookup failure, got nothing")
	}
	if !containsAll(logOutput, "invoice lookup failed on refund") {
		t.Errorf("log output missing expected content, got: %s", logOutput)
	}
}

func TestWithPaymentLogger_OnRefundHookError_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := &mockPaymentRepo{}
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	hookErr := fmt.Errorf("refund notification failed")
	failPlugin := &failingOnRefundPlugin{err: hookErr}
	registry := plugin.NewRegistry()
	_ = registry.Register(failPlugin)

	svc := NewPaymentService(
		&mockGateway{},
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		registry,
		clock,
		WithPaymentLogger(logger),
	)

	// Create a payment first
	pmt, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-refund-002",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf.Reset()

	err = svc.Refund(context.Background(), pmt.ID(), RefundInput{
		Reason: port.RefundReasonRequestedByCustomer,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output for OnRefund hook error, got nothing")
	}
	if !containsAll(logOutput, "OnRefund hook failed", "refund notification failed") {
		t.Errorf("log output missing expected content, got: %s", logOutput)
	}
}

func TestWithPaymentLogger_OnPaymentFailedHookError_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	hookErr := fmt.Errorf("payment failure notification failed")
	failPlugin := &failingOnPaymentFailedPlugin{err: hookErr}
	registry := plugin.NewRegistry()
	_ = registry.Register(failPlugin)

	svc := NewPaymentService(
		&mockGateway{failCharge: true},
		&mockPaymentRepo{},
		invRepo,
		nil,
		&mockEventStore{},
		registry,
		clock,
		WithPaymentLogger(logger),
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-fail-001",
	})
	if err == nil {
		t.Fatal("expected error for failed charge")
	}

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output for OnPaymentFailed hook error, got nothing")
	}
	if !containsAll(logOutput, "OnPaymentFailed hook failed", "payment failure notification failed") {
		t.Errorf("log output missing expected content, got: %s", logOutput)
	}
}

func TestPaymentServiceDefaultLogger_NoLogger_UsesSlogDefault(t *testing.T) {
	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}

	// No WithPaymentLogger option — should not panic
	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
	)

	_, err := svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "key-default-001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithCustomerGateway_ResolvesFallback(t *testing.T) {
	clock := newPaymentTestClock()
	agg := newTestContractAggregate(clock, contract.ContractTypeSubscription, jpy(1000))
	inv := newFinalizedInvoice(agg.AccountID(), agg.ContractID(), jpy(1000), nil)

	customerPM := "pm-customer-via-option"
	custGateway := &mockCustomerGateway{
		customer: &port.Customer{
			ID:                     string(agg.AccountID()),
			DefaultPaymentMethodID: &customerPM,
		},
	}

	svc := NewPaymentService(
		&mockGateway{},
		&mockPaymentRepo{},
		&mockInvoiceRepoForPayment{inv: inv},
		&mockContractRepo{agg: agg},
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithCustomerGateway(custGateway),
	)

	resolved, err := svc.ResolvePaymentMethod(context.Background(), inv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "pm-customer-via-option" {
		t.Errorf("expected pm-customer-via-option, got %s", resolved)
	}
}

// containsAll checks that s contains all substrings.
func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// --- M1: effective key length warning ---

func TestWithPaymentLogger_LongOriginalKey_WarnsOnEffectiveKeyOverflow(t *testing.T) {
	// When the derived effective key (original + "-" + 26-char ULID) exceeds
	// the strictest gateway limit (Adyen = 64 bytes), the service must emit
	// a warning log so operators can shorten the caller-supplied key before
	// Adyen rejects the retry with an opaque validation error.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	// 40-byte original key → 40 + 27 = 67 bytes derived key → over Adyen's 64.
	longKey := strings.Repeat("a", 40)

	txm := &switchableTxManager{
		failUntil:   1,
		paymentRepo: paymentRepo,
		invoiceRepo: invRepo,
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
		WithPaymentTxManager(txm),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  longKey,
	})

	logs := buf.String()
	if !containsAll(logs, "derived effective key exceeds Adyen", "maxAllowed=64") {
		t.Errorf("expected Adyen key-length warning in logs, got:\n%s", logs)
	}
}

func TestWithPaymentLogger_ShortOriginalKey_NoOverflowWarning(t *testing.T) {
	// A 10-byte original key yields 10 + 27 = 37 bytes — well under 64.
	// The warning must NOT fire.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	invRepo := &mockInvoiceRepoForPayment{inv: inv}
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	txm := &switchableTxManager{
		failUntil:   1,
		paymentRepo: paymentRepo,
		invoiceRepo: invRepo,
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		invRepo,
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
		WithPaymentTxManager(txm),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "short-key",
	})

	if strings.Contains(buf.String(), "derived effective key exceeds") {
		t.Errorf("unexpected length warning for a short key:\n%s", buf.String())
	}
}

// --- HIGH-v4-D: boundary tests around adyenMaxIdempotencyKeyLen (64) ---

// --- M-LW1: Resolve-path length warning must fire for pre-populated long keys ---

func TestWithPaymentLogger_ResolvePath_LongStoredKey_WarnsOnLoad(t *testing.T) {
	// A previously-populated IdempotencyStore may hand back an effective
	// key that exceeds Adyen's 64-byte limit (e.g. from a deployment with
	// an older longer-key policy, or a different PaymentService instance
	// with different constraints). The resolve path must warn so operators
	// can shorten the stored key before gateway Charge fails with an
	// opaque validation error.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	// Pre-populate the store with a 70-byte effective key.
	longEffectiveKey := strings.Repeat("e", 70)
	if err := store.MarkCompensated(context.Background(), "orig", longEffectiveKey); err != nil {
		t.Fatalf("setup: MarkCompensated failed: %v", err)
	}

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "orig",
	})

	logs := buf.String()
	if !strings.Contains(logs, "stored effective key exceeds Adyen") {
		t.Errorf("expected resolve-path length warning, got logs:\n%s", logs)
	}
	if !strings.Contains(logs, "effectiveKeyLen=70") {
		t.Errorf("expected effectiveKeyLen=70 in logs, got:\n%s", logs)
	}
	if !strings.Contains(logs, "maxAllowed=64") {
		t.Errorf("expected maxAllowed=64 in logs, got:\n%s", logs)
	}
}

func TestWithPaymentLogger_ResolvePath_BoundaryAt64_NoWarn(t *testing.T) {
	// Exact-boundary test for the resolve-path length warning:
	// len == 64 is AT the Adyen limit, not over → must NOT warn.
	// A regression from `>` to `>=` would spuriously warn here.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	_ = store.MarkCompensated(context.Background(), "b64", strings.Repeat("e", 64))

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "b64",
	})

	if strings.Contains(buf.String(), "stored effective key exceeds") {
		t.Errorf("64-byte stored key must not trigger the resolve-path warning; got:\n%s", buf.String())
	}
}

func TestWithPaymentLogger_ResolvePath_BoundaryAt65_Warns(t *testing.T) {
	// Exact-boundary test: len == 65 is OVER the Adyen limit → MUST warn.
	// A regression that changed the threshold (e.g. `> 65`) would silently
	// suppress this warning.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	_ = store.MarkCompensated(context.Background(), "b65", strings.Repeat("e", 65))

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "b65",
	})

	logs := buf.String()
	if !strings.Contains(logs, "stored effective key exceeds Adyen") {
		t.Errorf("65-byte stored key must trigger the resolve-path warning; got:\n%s", logs)
	}
	if !strings.Contains(logs, "effectiveKeyLen=65") {
		t.Errorf("expected effectiveKeyLen=65 in logs, got:\n%s", logs)
	}
	if !strings.Contains(logs, "maxAllowed=64") {
		t.Errorf("expected maxAllowed=64 in logs, got:\n%s", logs)
	}
}

func TestWithPaymentLogger_ResolvePath_ShortStoredKey_NoWarn(t *testing.T) {
	// Symmetric negative: a 60-byte stored key must NOT trigger the
	// resolve-path warning.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	clock := newPaymentTestClock()
	inv := newSimpleFinalizedInvoice()
	paymentRepo := newFakePaymentRepo()
	store := newFakeIdempotencyStore()
	gw := &trackingGateway{}

	_ = store.MarkCompensated(context.Background(), "orig2", strings.Repeat("e", 60))

	svc := NewPaymentService(
		gw,
		paymentRepo,
		&mockInvoiceRepoForPayment{inv: inv},
		nil,
		&mockEventStore{},
		plugin.NewRegistry(),
		clock,
		WithPaymentLogger(logger),
		WithIdempotencyStore(store),
	)

	_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
		PaymentMethodID: "pm-001",
		Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
		Currency:        shared.CurrencyJPY,
		IdempotencyKey:  "orig2",
	})

	if strings.Contains(buf.String(), "stored effective key exceeds") {
		t.Errorf("unexpected warning for 60-byte stored key:\n%s", buf.String())
	}
}

func TestWithPaymentLogger_EffectiveKeyLengthBoundary(t *testing.T) {
	// Effective key = originalKey (N bytes) + "-" + 26-char ULID = N + 27.
	// Adyen limit is 64 bytes. The boundary cases are:
	//   N = 37 → 64 bytes → NOT over limit → NO warn
	//   N = 38 → 65 bytes → OVER limit → WARN
	// If the check ever regresses from `>` to `>=`, N=37 would spuriously
	// warn; if it regresses from `>` to `>=65`, N=38 would silently pass.
	tests := []struct {
		name      string
		origLen   int
		wantWarn  bool
		wantLenIn string // substring of expected effectiveKeyLen= field
	}{
		{"37-bytes→64 at limit, no warn", 37, false, ""},
		{"38-bytes→65 over limit, warn", 38, true, "effectiveKeyLen=65"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			clock := newPaymentTestClock()
			inv := newSimpleFinalizedInvoice()
			invRepo := &mockInvoiceRepoForPayment{inv: inv}
			paymentRepo := newFakePaymentRepo()
			store := newFakeIdempotencyStore()
			gw := &trackingGateway{}
			txm := &switchableTxManager{
				failUntil:   1,
				paymentRepo: paymentRepo,
				invoiceRepo: invRepo,
			}

			svc := NewPaymentService(
				gw,
				paymentRepo,
				invRepo,
				nil,
				&mockEventStore{},
				plugin.NewRegistry(),
				clock,
				WithPaymentLogger(logger),
				WithPaymentTxManager(txm),
				WithIdempotencyStore(store),
			)

			origKey := strings.Repeat("a", tc.origLen)
			_, _ = svc.ProcessPayment(context.Background(), inv.ID(), ProcessPaymentInput{
				PaymentMethodID: "pm-001",
				Amount:          shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
				Currency:        shared.CurrencyJPY,
				IdempotencyKey:  origKey,
			})

			logs := buf.String()
			hasWarn := strings.Contains(logs, "derived effective key exceeds Adyen")
			if hasWarn != tc.wantWarn {
				t.Errorf("origLen=%d: wantWarn=%v, got=%v\nlogs:\n%s", tc.origLen, tc.wantWarn, hasWarn, logs)
			}
			if tc.wantWarn && !strings.Contains(logs, tc.wantLenIn) {
				t.Errorf("expected logs to contain %q, got:\n%s", tc.wantLenIn, logs)
			}
			if tc.wantWarn && !strings.Contains(logs, "maxAllowed=64") {
				t.Errorf("expected maxAllowed=64 in logs, got:\n%s", logs)
			}
		})
	}
}
