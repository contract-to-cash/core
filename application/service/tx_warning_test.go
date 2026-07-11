package service

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/plugin"
)

// warnLogger returns a logger writing WARN+ records into buf.
func warnLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// A write-side service constructed WITHOUT a transaction manager must emit a
// single Warn-level log at construction so a consumer who forgot to wire one
// gets a loud runtime signal instead of silent non-atomic corruption (#187).
func TestBillingService_WarnsWhenNoTxManager(t *testing.T) {
	var buf bytes.Buffer
	NewBillingService(
		&mockContractRepo{}, &mockInvoiceRepo{}, &mockUsageRepo{},
		balance.BalanceConfig{}, &mockPriceRepo{}, &mockProductRepo{},
		plugin.NewRegistry(), BillingConfig{}, newTestClock(),
		WithBillingLogger(warnLogger(&buf)),
	)
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "NOT atomic") {
		t.Fatalf("expected a non-atomic WARN log, got: %q", out)
	}
	if !strings.Contains(out, "BillingService") {
		t.Fatalf("expected BillingService in the log, got: %q", out)
	}
	if got := strings.Count(out, "NOT atomic"); got != 1 {
		t.Fatalf("expected the warning to be emitted exactly once, got %d", got)
	}
}

// WithoutTransactions() is the explicit opt-in for intentional in-memory use and
// must suppress the warning.
func TestBillingService_NoWarnWithExplicitOptIn(t *testing.T) {
	var buf bytes.Buffer
	NewBillingService(
		&mockContractRepo{}, &mockInvoiceRepo{}, &mockUsageRepo{},
		balance.BalanceConfig{}, &mockPriceRepo{}, &mockProductRepo{},
		plugin.NewRegistry(), BillingConfig{}, newTestClock(),
		WithBillingLogger(warnLogger(&buf)),
		WithoutTransactions(),
	)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning with WithoutTransactions(), got: %q", buf.String())
	}
}

// Wiring a real (non-noop) TxManager must also suppress the warning.
func TestBillingService_NoWarnWithRealTxManager(t *testing.T) {
	var buf bytes.Buffer
	NewBillingService(
		&mockContractRepo{}, &mockInvoiceRepo{}, &mockUsageRepo{},
		balance.BalanceConfig{}, &mockPriceRepo{}, &mockProductRepo{},
		plugin.NewRegistry(), BillingConfig{}, newTestClock(),
		WithBillingLogger(warnLogger(&buf)),
		WithBillingTxManager(fakeRealTxManager{}),
	)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning with a real TxManager, got: %q", buf.String())
	}
}

func TestCreditNoteService_WarnsWhenNoTxManager(t *testing.T) {
	var buf bytes.Buffer
	NewCreditNoteService(
		&mockInvoiceRepo{}, &mockCreditNoteRepo{}, plugin.NewRegistry(), newTestClock(),
		WithCreditNoteLogger(warnLogger(&buf)),
	)
	if out := buf.String(); !strings.Contains(out, "NOT atomic") || !strings.Contains(out, "CreditNoteService") {
		t.Fatalf("expected a CreditNoteService non-atomic WARN log, got: %q", out)
	}
}

func TestCreditNoteService_NoWarnWithExplicitOptIn(t *testing.T) {
	var buf bytes.Buffer
	NewCreditNoteService(
		&mockInvoiceRepo{}, &mockCreditNoteRepo{}, plugin.NewRegistry(), newTestClock(),
		WithCreditNoteLogger(warnLogger(&buf)),
		WithoutCreditNoteTransactions(),
	)
	if buf.Len() != 0 {
		t.Fatalf("expected no warning with WithoutCreditNoteTransactions(), got: %q", buf.String())
	}
}

// fakeRealTxManager is a non-noop TxManager for the suppression test.
type fakeRealTxManager struct{}

func (fakeRealTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	return fn(ctx, tx.Repos{})
}
