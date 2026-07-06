package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
)

// --- Mock CreditNote Repository ---

type mockCreditNoteRepo struct {
	saved *invoice.CreditNote
}

func (m *mockCreditNoteRepo) Save(_ context.Context, cn *invoice.CreditNote) error {
	m.saved = cn
	return nil
}
func (m *mockCreditNoteRepo) FindByID(_ context.Context, _ shared.CreditNoteID) (*invoice.CreditNote, error) {
	return m.saved, nil
}
func (m *mockCreditNoteRepo) FindByInvoiceID(_ context.Context, _ shared.InvoiceID) ([]*invoice.CreditNote, error) {
	return nil, nil
}
func (m *mockCreditNoteRepo) FindByAccountID(_ context.Context, _ shared.AccountID) ([]*invoice.CreditNote, error) {
	return nil, nil
}
func (m *mockCreditNoteRepo) FindByContractID(_ context.Context, _ shared.ContractID) ([]*invoice.CreditNote, error) {
	return nil, nil
}
func (m *mockCreditNoteRepo) FindByStatus(_ context.Context, _ invoice.CreditNoteStatus) ([]*invoice.CreditNote, error) {
	return nil, nil
}

// --- Mock Invoice Repo with FindByID support ---

type mockInvoiceRepoWithFind struct {
	mockInvoiceRepo
	invoices map[shared.InvoiceID]*invoice.Invoice
}

func (m *mockInvoiceRepoWithFind) FindByID(_ context.Context, id shared.InvoiceID) (*invoice.Invoice, error) {
	if inv, ok := m.invoices[id]; ok {
		return inv, nil
	}
	return nil, shared.NewDomainError(shared.ErrCodeNotFound, "invoice not found")
}

func (m *mockInvoiceRepoWithFind) Save(_ context.Context, inv *invoice.Invoice) error {
	m.saved = inv
	if m.invoices == nil {
		m.invoices = make(map[shared.InvoiceID]*invoice.Invoice)
	}
	m.invoices[inv.ID()] = inv
	return nil
}

// --- Helpers ---

func mustLineItems() []invoice.LineItem {
	li, err := invoice.NewLineItem("li-1", "Subscription", 1, jpy(10000), jpy(10000), big.NewRat(10, 100))
	if err != nil {
		panic(fmt.Sprintf("mustLineItems: %v", err))
	}
	return []invoice.LineItem{li}
}

func newPaidInvoice(accountID shared.AccountID, contractID shared.ContractID) *invoice.Invoice {
	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(),
		accountID,
		contractID,
		jpy(10000),
		jpy(0),
		jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithLineItems(mustLineItems()),
	)
	if err != nil {
		panic("newPaidInvoice: " + err.Error())
	}
	_ = inv.RecordPayment(jpy(11000), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	return inv
}

func newCreditNoteService(invRepo invoice.Repository, cnRepo invoice.CreditNoteRepository, registry *plugin.Registry, clock shared.Clock) *CreditNoteService {
	if registry == nil {
		registry = plugin.NewRegistry()
	}
	if clock == nil {
		clock = newTestClock()
	}
	return NewCreditNoteService(invRepo, cnRepo, registry, clock)
}

// --- CreateCreditNote tests ---

func TestCreateCreditNote_Success(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}

	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Partial refund", jpy(5000), big.NewRat(10, 100), jpy(500)),
	}

	cn, err := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOrderChange, items, "Plan downgrade")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cn.Status() != invoice.CreditNoteStatusDraft {
		t.Errorf("expected draft, got %s", cn.Status())
	}
	if cn.InvoiceID() != paidInv.ID() {
		t.Errorf("expected invoiceID %s, got %s", paidInv.ID(), cn.InvoiceID())
	}
	if cn.AccountID() != accountID {
		t.Errorf("expected accountID %s, got %s", accountID, cn.AccountID())
	}
	if cn.ContractID() != contractID {
		t.Errorf("expected contractID %s, got %s", contractID, cn.ContractID())
	}
	if cn.Memo() != "Plan downgrade" {
		t.Errorf("expected memo 'Plan downgrade', got %q", cn.Memo())
	}
	if cnRepo.saved == nil {
		t.Error("expected credit note to be saved")
	}
}

func TestCreateCreditNote_InvoiceNotFound(t *testing.T) {
	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(1000), big.NewRat(10, 100), jpy(100)),
	}

	_, err := svc.CreateCreditNote(context.Background(), shared.NewInvoiceID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error for non-existent invoice")
	}
}

func TestCreateCreditNote_DraftInvoice_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	draftInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(10000), jpy(0), jpy(0),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{draftInv.ID(): draftInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(1000), big.NewRat(10, 100), jpy(100)),
	}

	_, err = svc.CreateCreditNote(context.Background(), draftInv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error for draft invoice (should use Void instead)")
	}
}

func TestCreateCreditNote_ExceedsInvoiceTotal_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID) // total = 11000 (10000 + 1000 tax)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Over refund", jpy(50000), big.NewRat(10, 100), jpy(5000)),
	}

	_, err := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error when credit note total exceeds invoice total")
	}
}

// cnListRepo is a credit-note repo whose FindByInvoiceID returns a preset list,
// used to exercise the cumulative over-credit guard across multiple credit notes.
type cnListRepo struct {
	mockCreditNoteRepo
	existing []*invoice.CreditNote
}

func (m *cnListRepo) FindByInvoiceID(_ context.Context, _ shared.InvoiceID) ([]*invoice.CreditNote, error) {
	return m.existing, nil
}

// TestCreateCreditNote_CumulativeExceedsInvoiceTotal_Rejected guards against
// over-crediting via multiple credit notes that are each within the invoice
// total but collectively exceed it. An existing 8000 credit note plus a new
// 5000 one would total 13000 against an 11000 invoice and must be rejected.
func TestCreateCreditNote_CumulativeExceedsInvoiceTotal_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID) // total = 11000

	existingCN, err := invoice.NewCreditNote(
		shared.NewCreditNoteID(),
		paidInv.ID(),
		accountID,
		contractID,
		invoice.CreditNoteReasonOther,
		[]invoice.CreditNoteItem{
			invoice.NewCreditNoteItem("li-1", "First credit", jpy(8000), big.NewRat(0, 1), jpy(0)),
		},
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("failed to build existing credit note: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &cnListRepo{existing: []*invoice.CreditNote{existingCN}}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-2", "Second credit", jpy(5000), big.NewRat(0, 1), jpy(0)),
	}

	_, err = svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error when cumulative credit notes exceed invoice total")
	}
}

// TestCreateCreditNote_CumulativeWithinTotal_Allowed confirms the cumulative
// guard does not reject a second credit note that stays within the invoice total
// (8000 existing + 2000 new = 10000 <= 11000).
func TestCreateCreditNote_CumulativeWithinTotal_Allowed(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID) // total = 11000

	existingCN, err := invoice.NewCreditNote(
		shared.NewCreditNoteID(),
		paidInv.ID(),
		accountID,
		contractID,
		invoice.CreditNoteReasonOther,
		[]invoice.CreditNoteItem{
			invoice.NewCreditNoteItem("li-1", "First credit", jpy(8000), big.NewRat(0, 1), jpy(0)),
		},
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("failed to build existing credit note: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &cnListRepo{existing: []*invoice.CreditNote{existingCN}}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-2", "Second credit", jpy(2000), big.NewRat(0, 1), jpy(0)),
	}

	if _, err := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOther, items, ""); err != nil {
		t.Fatalf("did not expect error for cumulative credit within invoice total: %v", err)
	}
}

// TestCreateCreditNote_ForeignCurrencyItem_Rejected guards against the review #2
// bypass: a credit note whose items are in a different currency from the invoice
// must be rejected. Previously the over-credit guard used Money.GreaterThan,
// which returns false on a currency mismatch, so an arbitrarily large foreign
// total slipped through and was accepted.
func TestCreateCreditNote_ForeignCurrencyItem_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID) // JPY invoice, total = 11000

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	// USD item with a huge amount that would exceed the JPY invoice total if the
	// currencies were comparable.
	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "USD over-credit",
			shared.NewMoney(big.NewRat(1000000, 1), shared.CurrencyUSD),
			big.NewRat(10, 100),
			shared.NewMoney(big.NewRat(100000, 1), shared.CurrencyUSD)),
	}

	_, err := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error for credit note in a different currency from the invoice")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if domErr.Code != shared.ErrCodeCurrencyMismatch {
		t.Errorf("expected error code %s, got %s", shared.ErrCodeCurrencyMismatch, domErr.Code)
	}
	if cnRepo.saved != nil {
		t.Error("expected no credit note to be saved on currency mismatch")
	}
}

func TestCreateCreditNote_FinalizedInvoice_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	finalizedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{finalizedInv.ID(): finalizedInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(1000), big.NewRat(10, 100), jpy(100)),
	}

	_, err = svc.CreateCreditNote(context.Background(), finalizedInv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error for finalized invoice (should use Void instead)")
	}
}

func TestCreateCreditNote_EmptyItems_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	_, err := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOther, []invoice.CreditNoteItem{}, "")
	if err == nil {
		t.Fatal("expected error for empty items")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected validation error code, got %s", domErr.Code)
	}
}

func TestCreateCreditNote_NilItems_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	_, err := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOther, nil, "")
	if err == nil {
		t.Fatal("expected error for nil items")
	}
	var domErr *shared.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if domErr.Code != shared.ErrCodeValidation {
		t.Errorf("expected validation error code, got %s", domErr.Code)
	}
}

// --- IssueCreditNote tests ---

func TestIssueCreditNote_Success(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Full refund", jpy(10000), big.NewRat(10, 100), jpy(1000)),
	}
	cn, _ := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonDuplicate, items, "")

	issued, err := svc.IssueCreditNote(context.Background(), cn.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issued.Status() != invoice.CreditNoteStatusIssued {
		t.Errorf("expected issued, got %s", issued.Status())
	}
	if issued.IssuedAt() == nil {
		t.Error("expected issuedAt to be set")
	}
}

func TestIssueCreditNote_Hook_Called(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}

	registry := plugin.NewRegistry()
	hookPlugin := &testCreditNoteHook{}
	_ = registry.Register(hookPlugin)

	svc := newCreditNoteService(invRepo, cnRepo, registry, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(5000), big.NewRat(10, 100), jpy(500)),
	}
	cn, _ := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOrderChange, items, "")

	_, err := svc.IssueCreditNote(context.Background(), cn.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hookPlugin.called {
		t.Error("expected OnCreditNoteIssued hook to be called")
	}
}

// --- ApplyCreditNote tests ---

func TestApplyCreditNote_Success(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Credit", jpy(3000), big.NewRat(10, 100), jpy(300)),
	}
	cn, _ := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOrderChange, items, "")
	_, _ = svc.IssueCreditNote(context.Background(), cn.ID())

	applied, err := svc.ApplyCreditNote(context.Background(), cn.ID(), jpy(3300))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied.Status() != invoice.CreditNoteStatusApplied {
		t.Errorf("expected applied, got %s", applied.Status())
	}
	if applied.CreditAmount().Amount().Cmp(big.NewRat(3300, 1)) != 0 {
		t.Errorf("expected creditAmount 3300, got %s", applied.CreditAmount().Amount().RatString())
	}
}

// --- RefundCreditNote tests ---

func TestRefundCreditNote_Success(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(10000), big.NewRat(10, 100), jpy(1000)),
	}
	cn, _ := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonCancellation, items, "")
	_, _ = svc.IssueCreditNote(context.Background(), cn.ID())

	refunded, err := svc.RefundCreditNote(context.Background(), cn.ID(), jpy(11000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refunded.Status() != invoice.CreditNoteStatusRefunded {
		t.Errorf("expected refunded, got %s", refunded.Status())
	}
}

// --- ReissueInvoice tests ---

func TestReissueInvoice_Success(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	// Create a finalized invoice with matching billing period
	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithInvoiceNumber("INV-001"),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock, WithBillingService(billingSvc))

	replacement, err := svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original should be voided
	original, _ := invRepo.FindByID(context.Background(), originalInv.ID())
	if original.Status() != invoice.InvoiceStatusVoided {
		t.Errorf("expected original to be voided, got %s", original.Status())
	}
	if original.VoidReason() != "billing error" {
		t.Errorf("expected voidReason 'billing error', got %q", original.VoidReason())
	}

	// Replacement should link to original
	if replacement.RevisionOf() == nil {
		t.Fatal("expected replacement to have revisionOf set")
	}
	if *replacement.RevisionOf() != originalInv.ID() {
		t.Errorf("expected revisionOf %s, got %s", originalInv.ID(), *replacement.RevisionOf())
	}
}

func TestReissueInvoice_Hook_Called(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	registry := plugin.NewRegistry()
	hookPlugin := &testInvoiceRevisedHook{}
	_ = registry.Register(hookPlugin)

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, registry, clock, WithBillingService(billingSvc))

	_, err = svc.ReissueInvoice(context.Background(), originalInv.ID(), "correction")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hookPlugin.called {
		t.Error("expected OnInvoiceRevised hook to be called")
	}
}

func TestReissueInvoice_VoidedInvoice_Rejected(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))

	voidedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
		invoice.WithBillingPeriod(currentPeriodOf(agg)),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{voidedInv.ID(): voidedInv}}
	cnRepo := &mockCreditNoteRepo{}

	// BillingService must be configured so we actually reach VoidWithReason
	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock, WithBillingService(billingSvc))

	_, err = svc.ReissueInvoice(context.Background(), voidedInv.ID(), "test")
	if err == nil {
		t.Fatal("expected error reissuing voided invoice")
	}
}

func TestReissueInvoice_EmptyReason_Rejected(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	inv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{inv.ID(): inv}}
	cnRepo := &mockCreditNoteRepo{}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock, WithBillingService(billingSvc))

	_, err = svc.ReissueInvoice(context.Background(), inv.ID(), "")
	if err == nil {
		t.Fatal("expected error for empty void reason")
	}
}

// --- Task 1: ReissueInvoice uses TxManager ---

func TestReissueInvoice_UsesTransaction(t *testing.T) {
	// Verify that ReissueInvoice runs all writes within a transaction
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	// Use a tracking TxManager to verify RunInTx is called
	trackingTx := &trackingTxManager{
		inner: tx.NewNoopTxManager(tx.Repos{Invoices: invRepo}),
	}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock,
		WithBillingService(billingSvc),
		WithCreditNoteTxManager(trackingTx),
	)

	_, err = svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !trackingTx.called {
		t.Error("expected ReissueInvoice to use RunInTx")
	}
}

// TestReissueInvoice_UsesSingleTransaction guards review #4: the inner
// BillingService.GenerateInvoice must join the outer CreditNoteService
// transaction rather than opening an independent nested one. The inner billing
// tx manager must therefore never be invoked.
func TestReissueInvoice_UsesSingleTransaction(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	outerTx := &trackingTxManager{inner: tx.NewNoopTxManager(tx.Repos{Invoices: invRepo})}
	innerTx := &trackingTxManager{inner: tx.NewNoopTxManager(tx.Repos{Contracts: &mockContractRepo{agg: agg}, Invoices: invRepo})}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBillingTxManager(innerTx),
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock,
		WithBillingService(billingSvc),
		WithCreditNoteTxManager(outerTx),
	)

	if _, err = svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if outerTx.count != 1 {
		t.Errorf("expected exactly 1 outer transaction, got %d", outerTx.count)
	}
	if innerTx.count != 0 {
		t.Errorf("expected inner billing to join the outer transaction (0 inner tx), got %d", innerTx.count)
	}
}

// TestReissueInvoice_AppliesCreditsViaJoinedTransaction guards review M1/W1: when
// the inner BillingService joins the outer CreditNoteService transaction, it must
// still apply credits even though the outer manager only wired the Invoices repo.
// tx.Run fills the missing Balances repo from the billing manager on join.
func TestReissueInvoice_AppliesCreditsViaJoinedTransaction(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	// Seed a 3000 JPY credit for the account.
	balRepo := inmemory.NewInMemoryBalanceRepository(clock)
	entry, _ := balance.NewBalanceEntry(agg.AccountID(), jpy(3000), balance.BalanceReasonGoodwill, clock.Now())
	if err := balRepo.Save(context.Background(), entry); err != nil {
		t.Fatalf("failed to seed balance: %v", err)
	}

	// BillingService HAS the balance repo (its default tx manager carries Balances).
	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
		WithBalanceRepo(balRepo),
	)

	// CreditNoteService's tx manager LACKS Balances (only Invoices).
	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock,
		WithBillingService(billingSvc),
		WithCreditNoteTxManager(tx.NewNoopTxManager(tx.Repos{Invoices: invRepo})),
	)

	replacement, err := svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The 3000 credit must have been applied on the replacement, proving the
	// joined transaction received the Balances repo via fallback.
	if replacement.AppliedBalance().Amount().Cmp(big.NewRat(3000, 1)) != 0 {
		t.Errorf("expected 3000 credit applied on reissued invoice, got %s",
			replacement.AppliedBalance().Amount().RatString())
	}
}

func TestReissueInvoice_TransactionFailure_ReturnsError(t *testing.T) {
	// If the transaction fails, the error should propagate and no replacement is returned.
	// In a real DB implementation, the void Save would be rolled back.
	// With failingTxManager, the closure is never executed so no writes occur.
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	// failingTxManager never executes the closure — simulates transaction open failure
	failingTx := &failingTxManager{}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock,
		WithBillingService(billingSvc),
		WithCreditNoteTxManager(failingTx),
	)

	replacement, err := svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error")
	if err == nil {
		t.Fatal("expected error from failing TxManager")
	}
	if replacement != nil {
		t.Error("expected nil replacement when transaction fails")
	}

	// Verify the repo was NOT written to (closure never ran)
	if invRepo.saved != nil {
		t.Error("expected no writes when transaction fails")
	}
}

// txAwareInvoiceRepo records whether FindByID for a target invoice ID was
// invoked with a context carrying an in-progress transaction. Used to prove the
// issue #151 (M3) fix: ReissueInvoice loads the original invoice INSIDE the
// transaction (through the tx-scoped repo), not before it.
type txAwareInvoiceRepo struct {
	*mockInvoiceRepoWithFind
	targetID         shared.InvoiceID
	loadedTargetInTx bool
	loadedTarget     bool
}

func (r *txAwareInvoiceRepo) FindByID(ctx context.Context, id shared.InvoiceID) (*invoice.Invoice, error) {
	if id == r.targetID {
		r.loadedTarget = true
		if _, ok := tx.ReposFromContext(ctx); ok {
			r.loadedTargetInTx = true
		}
	}
	return r.mockInvoiceRepoWithFind.FindByID(ctx, id)
}

// TestReissueInvoice_LoadsOriginalInsideTransaction guards issue #151 (M3): the
// original invoice must be read through the transaction-scoped repository inside
// the tx boundary, so a concurrent payment committed before the tx is observed by
// VoidWithReason instead of a stale pre-tx snapshot silently overwriting it.
func TestReissueInvoice_LoadsOriginalInsideTransaction(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	inner := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	invRepo := &txAwareInvoiceRepo{mockInvoiceRepoWithFind: inner, targetID: originalInv.ID()}
	cnRepo := &mockCreditNoteRepo{}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock, WithBillingService(billingSvc))

	if _, err = svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !invRepo.loadedTarget {
		t.Fatal("expected ReissueInvoice to load the original invoice")
	}
	if !invRepo.loadedTargetInTx {
		t.Error("expected the original invoice to be loaded INSIDE the transaction (issue #151)")
	}
}

// --- Task 2: Post-save hooks should be non-fatal ---

func TestIssueCreditNote_HookFailure_NonFatal(t *testing.T) {
	// When OnCreditNoteIssued hook fails, the operation should still succeed
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	paidInv := newPaidInvoice(accountID, contractID)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{paidInv.ID(): paidInv}}
	cnRepo := &mockCreditNoteRepo{}

	registry := plugin.NewRegistry()
	failingHook := &failingCreditNoteHook{}
	_ = registry.Register(failingHook)

	svc := newCreditNoteService(invRepo, cnRepo, registry, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(5000), big.NewRat(10, 100), jpy(500)),
	}
	cn, _ := svc.CreateCreditNote(context.Background(), paidInv.ID(), invoice.CreditNoteReasonOrderChange, items, "")

	issued, err := svc.IssueCreditNote(context.Background(), cn.ID())
	if err != nil {
		t.Fatalf("expected hook failure to be non-fatal, got error: %v", err)
	}
	if issued.Status() != invoice.CreditNoteStatusIssued {
		t.Errorf("expected issued, got %s", issued.Status())
	}
}

func TestReissueInvoice_HookFailure_NonFatal(t *testing.T) {
	// When OnInvoiceRevised hook fails, the operation should still succeed
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	registry := plugin.NewRegistry()
	failingHook := &failingInvoiceRevisedHook{}
	_ = registry.Register(failingHook)

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, registry, clock,
		WithBillingService(billingSvc),
		WithCreditNoteLogger(slog.Default()),
	)

	replacement, err := svc.ReissueInvoice(context.Background(), originalInv.ID(), "correction")
	if err != nil {
		t.Fatalf("expected hook failure to be non-fatal, got error: %v", err)
	}
	if replacement == nil {
		t.Fatal("expected replacement invoice")
	}
}

// --- Task 3: ReissueInvoice sets originalInvoiceID ---

func TestReissueInvoice_SetsOriginalInvoiceID(t *testing.T) {
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	originalInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{originalInv.ID(): originalInv}}
	cnRepo := &mockCreditNoteRepo{}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock, WithBillingService(billingSvc))

	replacement, err := svc.ReissueInvoice(context.Background(), originalInv.ID(), "billing error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// originalInvoiceID should point to the root invoice
	if replacement.OriginalInvoiceID() == nil {
		t.Fatal("expected originalInvoiceID to be set on replacement")
	}
	if *replacement.OriginalInvoiceID() != originalInv.ID() {
		t.Errorf("expected originalInvoiceID %s, got %s", originalInv.ID(), *replacement.OriginalInvoiceID())
	}
}

func TestReissueInvoice_ChainedRevision_PreservesOriginalInvoiceID(t *testing.T) {
	// When reissuing an already-revised invoice, originalInvoiceID should point to the root
	clock := newTestClock()
	agg, priceEntity := newActiveAggWithPrice(clock, contract.ContractTypeSubscription, jpy(10000))
	period := currentPeriodOf(agg)

	rootID := shared.NewInvoiceID()
	// This invoice was itself a revision of rootID
	revisedInv, err := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
		invoice.WithOriginalInvoiceID(rootID),
		invoice.WithRevisionOf(rootID),
	)
	if err != nil {
		t.Fatalf("unexpected error creating invoice: %v", err)
	}

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{revisedInv.ID(): revisedInv}}
	cnRepo := &mockCreditNoteRepo{}

	billingSvc := NewBillingService(
		&mockContractRepo{agg: agg},
		invRepo,
		&mockUsageRepo{},
		balance.BalanceConfig{},
		priceRepoFor(priceEntity),
		&mockProductRepo{},
		plugin.NewRegistry(),
		BillingConfig{DaysUntilDue: 30},
		clock,
	)

	svc := NewCreditNoteService(invRepo, cnRepo, plugin.NewRegistry(), clock, WithBillingService(billingSvc))

	replacement, err := svc.ReissueInvoice(context.Background(), revisedInv.ID(), "second correction")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// originalInvoiceID should point to the ROOT, not the intermediate revision
	if replacement.OriginalInvoiceID() == nil {
		t.Fatal("expected originalInvoiceID to be set")
	}
	if *replacement.OriginalInvoiceID() != rootID {
		t.Errorf("expected originalInvoiceID to be root %s, got %s", rootID, *replacement.OriginalInvoiceID())
	}

	// revisionOf should point to the DIRECT parent
	if replacement.RevisionOf() == nil {
		t.Fatal("expected revisionOf to be set")
	}
	if *replacement.RevisionOf() != revisedInv.ID() {
		t.Errorf("expected revisionOf %s, got %s", revisedInv.ID(), *replacement.RevisionOf())
	}
}

// --- Test hook plugins ---

type testCreditNoteHook struct {
	called bool
}

func (p *testCreditNoteHook) Name() string                                        { return "test-cn-hook" }
func (p *testCreditNoteHook) Version() string                                     { return "1.0.0" }
func (p *testCreditNoteHook) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *testCreditNoteHook) Shutdown(_ context.Context) error                    { return nil }
func (p *testCreditNoteHook) Priority() int                                       { return 500 }
func (p *testCreditNoteHook) OnCreditNoteIssued(_ *plugin.Context, _ *invoice.CreditNote) error {
	p.called = true
	return nil
}

type testInvoiceRevisedHook struct {
	called bool
}

func (p *testInvoiceRevisedHook) Name() string                                        { return "test-rev-hook" }
func (p *testInvoiceRevisedHook) Version() string                                     { return "1.0.0" }
func (p *testInvoiceRevisedHook) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *testInvoiceRevisedHook) Shutdown(_ context.Context) error                    { return nil }
func (p *testInvoiceRevisedHook) Priority() int                                       { return 500 }
func (p *testInvoiceRevisedHook) OnInvoiceRevised(_ *plugin.Context, _ *invoice.Invoice, _ *invoice.Invoice) error {
	p.called = true
	return nil
}

// --- Failing hook plugins (for non-fatal tests) ---

type failingCreditNoteHook struct{}

func (p *failingCreditNoteHook) Name() string                                        { return "failing-cn-hook" }
func (p *failingCreditNoteHook) Version() string                                     { return "1.0.0" }
func (p *failingCreditNoteHook) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *failingCreditNoteHook) Shutdown(_ context.Context) error                    { return nil }
func (p *failingCreditNoteHook) Priority() int                                       { return 500 }
func (p *failingCreditNoteHook) OnCreditNoteIssued(_ *plugin.Context, _ *invoice.CreditNote) error {
	return fmt.Errorf("hook intentionally failed")
}

type failingInvoiceRevisedHook struct{}

func (p *failingInvoiceRevisedHook) Name() string                                        { return "failing-rev-hook" }
func (p *failingInvoiceRevisedHook) Version() string                                     { return "1.0.0" }
func (p *failingInvoiceRevisedHook) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *failingInvoiceRevisedHook) Shutdown(_ context.Context) error                    { return nil }
func (p *failingInvoiceRevisedHook) Priority() int                                       { return 500 }
func (p *failingInvoiceRevisedHook) OnInvoiceRevised(_ *plugin.Context, _ *invoice.Invoice, _ *invoice.Invoice) error {
	return fmt.Errorf("hook intentionally failed")
}

// --- TxManager test doubles ---

type trackingTxManager struct {
	inner  tx.TxManager
	called bool
	count  int
}

func (m *trackingTxManager) RunInTx(ctx context.Context, fn func(context.Context, tx.Repos) error) error {
	m.called = true
	m.count++
	return m.inner.RunInTx(ctx, fn)
}

type failingTxManager struct{}

func (m *failingTxManager) RunInTx(_ context.Context, _ func(context.Context, tx.Repos) error) error {
	return fmt.Errorf("transaction failed")
}
