package service

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
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

func newPaidInvoice(accountID shared.AccountID, contractID shared.ContractID) *invoice.Invoice {
	inv := invoice.NewInvoice(
		shared.NewInvoiceID(),
		accountID,
		contractID,
		jpy(10000),
		jpy(0),
		jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithLineItems([]invoice.LineItem{
			invoice.NewLineItem("li-1", "Subscription", 1, jpy(10000), jpy(10000), big.NewRat(10, 100)),
		}),
	)
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
	draftInv := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(10000), jpy(0), jpy(0),
	)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{draftInv.ID(): draftInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(1000), big.NewRat(10, 100), jpy(100)),
	}

	_, err := svc.CreateCreditNote(context.Background(), draftInv.ID(), invoice.CreditNoteReasonOther, items, "")
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

func TestCreateCreditNote_FinalizedInvoice_Rejected(t *testing.T) {
	accountID := shared.NewAccountID()
	contractID := shared.NewContractID()
	finalizedInv := invoice.NewInvoice(
		shared.NewInvoiceID(), accountID, contractID,
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
	)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{finalizedInv.ID(): finalizedInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, nil)

	items := []invoice.CreditNoteItem{
		invoice.NewCreditNoteItem("li-1", "Refund", jpy(1000), big.NewRat(10, 100), jpy(100)),
	}

	_, err := svc.CreateCreditNote(context.Background(), finalizedInv.ID(), invoice.CreditNoteReasonOther, items, "")
	if err == nil {
		t.Fatal("expected error for finalized invoice (should use Void instead)")
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
	originalInv := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(1000),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithInvoiceNumber("INV-001"),
		invoice.WithBillingPeriod(period),
	)

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

	originalInv := invoice.NewInvoice(
		shared.NewInvoiceID(), agg.AccountID(), agg.ContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusFinalized),
		invoice.WithBillingPeriod(period),
	)

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

	_, err := svc.ReissueInvoice(context.Background(), originalInv.ID(), "correction")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hookPlugin.called {
		t.Error("expected OnInvoiceRevised hook to be called")
	}
}

func TestReissueInvoice_VoidedInvoice_Rejected(t *testing.T) {
	clock := newTestClock()
	voidedInv := invoice.NewInvoice(
		shared.NewInvoiceID(), shared.NewAccountID(), shared.NewContractID(),
		jpy(10000), jpy(0), jpy(0),
		invoice.WithStatus(invoice.InvoiceStatusVoided),
	)

	invRepo := &mockInvoiceRepoWithFind{invoices: map[shared.InvoiceID]*invoice.Invoice{voidedInv.ID(): voidedInv}}
	cnRepo := &mockCreditNoteRepo{}
	svc := newCreditNoteService(invRepo, cnRepo, nil, clock)

	_, err := svc.ReissueInvoice(context.Background(), voidedInv.ID(), "test")
	if err == nil {
		t.Fatal("expected error reissuing voided invoice")
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
