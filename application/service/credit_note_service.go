package service

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/plugin"
)

// CreditNoteServiceOption configures optional dependencies of CreditNoteService.
type CreditNoteServiceOption func(*CreditNoteService)

// WithBillingService sets the billing service for invoice reissue workflows.
func WithBillingService(bs *BillingService) CreditNoteServiceOption {
	return func(s *CreditNoteService) {
		s.billingSvc = bs
	}
}

// CreditNoteService orchestrates credit note creation, issuance, and invoice revision.
type CreditNoteService struct {
	invoiceRepo    invoice.Repository
	creditNoteRepo invoice.CreditNoteRepository
	registry       *plugin.Registry
	clock          shared.Clock
	billingSvc     *BillingService
}

// NewCreditNoteService creates a new CreditNoteService.
func NewCreditNoteService(
	invoiceRepo invoice.Repository,
	creditNoteRepo invoice.CreditNoteRepository,
	registry *plugin.Registry,
	clock shared.Clock,
	opts ...CreditNoteServiceOption,
) *CreditNoteService {
	s := &CreditNoteService{
		invoiceRepo:    invoiceRepo,
		creditNoteRepo: creditNoteRepo,
		registry:       registry,
		clock:          clock,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// creditNoteEligibleStatuses defines which invoice statuses allow credit note creation.
// Draft and finalized invoices should use Void() directly instead.
var creditNoteEligibleStatuses = map[invoice.InvoiceStatus]bool{
	invoice.InvoiceStatusIssued:      true,
	invoice.InvoiceStatusPaid:        true,
	invoice.InvoiceStatusPartialPaid: true,
	invoice.InvoiceStatusOverdue:     true,
}

// CreateCreditNote creates a new credit note in draft status for an existing invoice.
func (s *CreditNoteService) CreateCreditNote(
	ctx context.Context,
	invoiceID shared.InvoiceID,
	reason invoice.CreditNoteReason,
	items []invoice.CreditNoteItem,
	memo string,
) (*invoice.CreditNote, error) {
	inv, err := s.invoiceRepo.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to find invoice: %w", err)
	}

	if !creditNoteEligibleStatuses[inv.Status()] {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("cannot create credit note for invoice in status %s", inv.Status()))
	}

	// Validate that credit note total does not exceed original invoice total
	var itemSubtotal, itemTax shared.Money
	currency := items[0].Amount().Currency()
	itemSubtotal = shared.Zero(currency)
	itemTax = shared.Zero(currency)
	for _, item := range items {
		s, _ := itemSubtotal.Add(item.Amount())
		itemSubtotal = s
		ta, _ := itemTax.Add(item.TaxAmount())
		itemTax = ta
	}
	cnTotal, _ := itemSubtotal.Add(itemTax)
	if cnTotal.GreaterThan(inv.Total()) {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("credit note total %s exceeds invoice total %s",
				cnTotal.Amount().RatString(), inv.Total().Amount().RatString()))
	}

	var opts []invoice.CreditNoteOption
	if memo != "" {
		opts = append(opts, invoice.WithCreditNoteMemo(memo))
	}

	cn := invoice.NewCreditNote(
		shared.NewCreditNoteID(),
		invoiceID,
		inv.AccountID(),
		inv.ContractID(),
		reason,
		items,
		opts...,
	)

	if err := s.creditNoteRepo.Save(ctx, cn); err != nil {
		return nil, fmt.Errorf("failed to save credit note: %w", err)
	}

	return cn, nil
}

// IssueCreditNote transitions a credit note from draft to issued and fires hooks.
func (s *CreditNoteService) IssueCreditNote(ctx context.Context, creditNoteID shared.CreditNoteID) (*invoice.CreditNote, error) {
	cn, err := s.creditNoteRepo.FindByID(ctx, creditNoteID)
	if err != nil {
		return nil, fmt.Errorf("failed to find credit note: %w", err)
	}

	if err := cn.Issue(s.clock.Now()); err != nil {
		return nil, err
	}

	if err := s.creditNoteRepo.Save(ctx, cn); err != nil {
		return nil, fmt.Errorf("failed to save credit note: %w", err)
	}

	// Fire OnCreditNoteIssued hooks
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnCreditNoteIssuedHooks() {
		if err := hook.OnCreditNoteIssued(pluginCtx, cn); err != nil {
			return nil, fmt.Errorf("OnCreditNoteIssued hook error: %w", err)
		}
	}

	return cn, nil
}

// ApplyCreditNote transitions a credit note from issued to applied (account credit).
func (s *CreditNoteService) ApplyCreditNote(ctx context.Context, creditNoteID shared.CreditNoteID, creditAmount shared.Money) (*invoice.CreditNote, error) {
	cn, err := s.creditNoteRepo.FindByID(ctx, creditNoteID)
	if err != nil {
		return nil, fmt.Errorf("failed to find credit note: %w", err)
	}

	if err := cn.Apply(creditAmount); err != nil {
		return nil, err
	}

	if err := s.creditNoteRepo.Save(ctx, cn); err != nil {
		return nil, fmt.Errorf("failed to save credit note: %w", err)
	}

	return cn, nil
}

// RefundCreditNote transitions a credit note from issued to refunded (payment refund).
func (s *CreditNoteService) RefundCreditNote(ctx context.Context, creditNoteID shared.CreditNoteID, refundAmount shared.Money) (*invoice.CreditNote, error) {
	cn, err := s.creditNoteRepo.FindByID(ctx, creditNoteID)
	if err != nil {
		return nil, fmt.Errorf("failed to find credit note: %w", err)
	}

	if err := cn.Refund(refundAmount); err != nil {
		return nil, err
	}

	if err := s.creditNoteRepo.Save(ctx, cn); err != nil {
		return nil, fmt.Errorf("failed to save credit note: %w", err)
	}

	return cn, nil
}

// ReissueInvoice voids the original invoice and generates a replacement linked to it.
// The replacement invoice has revisionOf set to the original's ID.
// Requires a BillingService to be configured via WithBillingService.
func (s *CreditNoteService) ReissueInvoice(ctx context.Context, originalInvoiceID shared.InvoiceID, reason string) (*invoice.Invoice, error) {
	// Pre-flight: ensure BillingService is available before mutating state
	if s.billingSvc == nil {
		return nil, fmt.Errorf("billing service not configured: cannot reissue invoice")
	}

	original, err := s.invoiceRepo.FindByID(ctx, originalInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to find invoice: %w", err)
	}

	// Void the original
	if err := original.VoidWithReason(reason); err != nil {
		return nil, fmt.Errorf("failed to void original invoice: %w", err)
	}
	if err := s.invoiceRepo.Save(ctx, original); err != nil {
		return nil, fmt.Errorf("failed to save voided invoice: %w", err)
	}

	// Generate replacement via BillingService
	replacement, err := s.billingSvc.GenerateInvoice(ctx, original.ContractID(), original.BillingPeriod())
	if err != nil {
		return nil, fmt.Errorf("failed to generate replacement invoice: %w", err)
	}

	// Set revisionOf link on the replacement
	replacement.SetRevisionOf(originalInvoiceID)

	if err := s.invoiceRepo.Save(ctx, replacement); err != nil {
		return nil, fmt.Errorf("failed to save linked replacement invoice: %w", err)
	}

	// Fire OnInvoiceRevised hooks
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnInvoiceRevisedHooks() {
		if err := hook.OnInvoiceRevised(pluginCtx, original, replacement); err != nil {
			return nil, fmt.Errorf("OnInvoiceRevised hook error: %w", err)
		}
	}

	return replacement, nil
}
