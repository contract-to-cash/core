package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/contract-to-cash/core/application/tx"
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

// WithCreditNoteTxManager sets the transaction manager for the CreditNoteService.
// If not provided, a NoopTxManager is used (no transaction wrapping).
func WithCreditNoteTxManager(tm tx.TxManager) CreditNoteServiceOption {
	return func(s *CreditNoteService) {
		s.txManager = tm
	}
}

// WithCreditNoteLogger sets a structured logger for the CreditNoteService.
// If not provided, slog.Default() is used.
func WithCreditNoteLogger(l *slog.Logger) CreditNoteServiceOption {
	return func(s *CreditNoteService) {
		s.logger = l
	}
}

// CreditNoteService orchestrates credit note creation, issuance, and invoice revision.
type CreditNoteService struct {
	invoiceRepo    invoice.Repository
	creditNoteRepo invoice.CreditNoteRepository
	registry       *plugin.Registry
	clock          shared.Clock
	billingSvc     *BillingService
	txManager      tx.TxManager
	logger         *slog.Logger
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
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.txManager == nil {
		s.txManager = tx.NewNoopTxManager(tx.Repos{
			Invoices: invoiceRepo,
		})
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
//
// The cumulative over-credit guard (existing non-voided credit notes + this one
// must not exceed the invoice total) is evaluated read-then-write without a
// surrounding transaction, because CreditNoteRepository is not part of tx.Repos.
// Under concurrency this is therefore a check-then-act: two simultaneous calls
// for the same invoice can both observe the pre-existing total and both pass the
// cap. Consumers that must guarantee the invariant under concurrent issuance are
// responsible for serializing CreateCreditNote per invoice (e.g. an advisory/row
// lock on the invoice, or a DB constraint). A transactional cumulative cap is
// tracked separately (see issue for CreditNote transactional integrity).
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

	if len(items) == 0 {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"credit note must have at least one item")
	}

	// Validate that credit note total does not exceed original invoice total.
	// The credit note must be denominated in the invoice's currency: otherwise
	// the over-credit comparison below (Money.GreaterThan) silently returns false
	// on a currency mismatch and an arbitrarily large foreign total slips through
	// (review #2). Anchor the currency on the invoice, not on items[0].
	currency := inv.Total().Currency()
	itemSubtotal := shared.Zero(currency)
	itemTax := shared.Zero(currency)
	for _, item := range items {
		if item.Amount().Currency() != currency {
			return nil, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("credit note item currency %s does not match invoice currency %s",
					item.Amount().Currency(), currency))
		}
		s, err := itemSubtotal.Add(item.Amount())
		if err != nil {
			return nil, fmt.Errorf("failed to sum credit note item amounts: %w", err)
		}
		itemSubtotal = s

		itemTaxAmt := item.TaxAmount()
		if itemTaxAmt.IsZero() {
			itemTaxAmt = shared.Zero(currency)
		}
		if itemTaxAmt.Currency() != currency {
			return nil, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("credit note item tax currency %s does not match invoice currency %s",
					itemTaxAmt.Currency(), currency))
		}
		ta, err := itemTax.Add(itemTaxAmt)
		if err != nil {
			return nil, fmt.Errorf("failed to sum credit note item taxes: %w", err)
		}
		itemTax = ta
	}
	cnTotal, err := itemSubtotal.Add(itemTax)
	if err != nil {
		return nil, fmt.Errorf("failed to compute credit note total: %w", err)
	}
	// Aggregate previously-issued (non-voided) credit notes for this invoice so the
	// cumulative credited amount cannot exceed the invoice total. Checking only this
	// credit note against the invoice total would let multiple individually-valid
	// credit notes collectively over-credit the customer.
	existingCNs, err := s.creditNoteRepo.FindByInvoiceID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing credit notes: %w", err)
	}
	creditedSoFar := shared.Zero(currency)
	for _, existing := range existingCNs {
		if existing.Status() == invoice.CreditNoteStatusVoided {
			continue
		}
		// Defensive: a credit note in a different currency cannot be summed; the
		// per-note currency guard above prevents creating such notes, so skip.
		if existing.Total().Currency() != currency {
			continue
		}
		creditedSoFar, err = creditedSoFar.Add(existing.Total())
		if err != nil {
			return nil, fmt.Errorf("failed to aggregate existing credit notes: %w", err)
		}
	}
	cumulative, err := creditedSoFar.Add(cnTotal)
	if err != nil {
		return nil, fmt.Errorf("failed to compute cumulative credit total: %w", err)
	}
	if cumulative.GreaterThan(inv.Total()) {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("cumulative credit note total %s exceeds invoice total %s",
				cumulative.Amount().RatString(), inv.Total().Amount().RatString()))
	}

	var opts []invoice.CreditNoteOption
	if memo != "" {
		opts = append(opts, invoice.WithCreditNoteMemo(memo))
	}

	cn, err := invoice.NewCreditNote(
		shared.NewCreditNoteID(),
		invoiceID,
		inv.AccountID(),
		inv.ContractID(),
		reason,
		items,
		s.clock.Now(),
		opts...,
	)
	if err != nil {
		return nil, err
	}

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

	// Post-save hooks (non-fatal, outside transaction)
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnCreditNoteIssuedHooks() {
		if hookErr := hook.OnCreditNoteIssued(pluginCtx, cn); hookErr != nil {
			s.logger.Warn("OnCreditNoteIssued hook failed",
				"creditNoteID", cn.ID(),
				"error", hookErr,
			)
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
// The replacement invoice has revisionOf set to the original's ID, and originalInvoiceID
// set to the root of the revision chain.
// Requires a BillingService to be configured via WithBillingService.
// All writes run within a transaction via TxManager.
func (s *CreditNoteService) ReissueInvoice(ctx context.Context, originalInvoiceID shared.InvoiceID, reason string) (*invoice.Invoice, error) {
	// Pre-flight: ensure BillingService is available before mutating state
	if s.billingSvc == nil {
		return nil, fmt.Errorf("billing service not configured: cannot reissue invoice")
	}

	original, err := s.invoiceRepo.FindByID(ctx, originalInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to find invoice: %w", err)
	}

	// Determine the root of the revision chain before entering transaction.
	// If the original already has an originalInvoiceID (it's itself a revision),
	// propagate that root. Otherwise, the original IS the root.
	rootID := originalInvoiceID
	if original.OriginalInvoiceID() != nil {
		rootID = *original.OriginalInvoiceID()
	}

	// All writes are atomic within a SINGLE transaction. tx.Run stamps the
	// transaction onto the context, and the inner
	// BillingService.GenerateInvoice (also via tx.Run) detects and JOINS it
	// rather than opening an independent nested transaction — so the void of the
	// original and the creation of the replacement commit or roll back together
	// even on real DB implementations (review #4).
	var replacement *invoice.Invoice
	err = tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		// Void the original inside the transaction so in-memory state
		// is only mutated when the transaction will persist it.
		if voidErr := original.VoidWithReason(reason); voidErr != nil {
			return fmt.Errorf("failed to void original invoice: %w", voidErr)
		}

		if saveErr := repos.Invoices.Save(txCtx, original); saveErr != nil {
			return fmt.Errorf("failed to save voided invoice: %w", saveErr)
		}

		// Generate replacement via BillingService
		var genErr error
		replacement, genErr = s.billingSvc.GenerateInvoice(txCtx, original.ContractID(), original.BillingPeriod())
		if genErr != nil {
			return fmt.Errorf("failed to generate replacement invoice: %w", genErr)
		}

		// Set revision links on the replacement
		replacement.SetRevisionOf(originalInvoiceID)
		replacement.SetOriginalInvoiceID(rootID)

		if saveErr := repos.Invoices.Save(txCtx, replacement); saveErr != nil {
			return fmt.Errorf("failed to save linked replacement invoice: %w", saveErr)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Post-commit hooks (non-fatal, outside transaction)
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnInvoiceRevisedHooks() {
		if hookErr := hook.OnInvoiceRevised(pluginCtx, original, replacement); hookErr != nil {
			s.logger.Warn("OnInvoiceRevised hook failed",
				"originalInvoiceID", originalInvoiceID,
				"replacementInvoiceID", replacement.ID(),
				"error", hookErr,
			)
		}
	}

	return replacement, nil
}
