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

func (p *failingOnPaymentFailedPlugin) Name() string { return "failing-on-payment-failed" }
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
