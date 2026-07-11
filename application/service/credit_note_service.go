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
// If not provided, a NoopTxManager is used (no transaction wrapping) and a
// Warn-level log is emitted at construction (see WithoutCreditNoteTransactions
// to opt out).
func WithCreditNoteTxManager(tm tx.TxManager) CreditNoteServiceOption {
	return func(s *CreditNoteService) {
		s.txManager = tm
	}
}

// WithoutCreditNoteTransactions explicitly opts the CreditNoteService into
// running without a transaction manager (credit-note + invoice writes, including
// the void-and-reissue flow, will NOT be atomic). Use it for in-memory demos and
// tests where that trade-off is intentional; it suppresses the non-atomic
// warning that a silently-defaulted NoopTxManager would otherwise emit. Do NOT
// use it in production with real repositories.
func WithoutCreditNoteTransactions() CreditNoteServiceOption {
	return func(s *CreditNoteService) {
		s.suppressTxWarning = true
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
	// suppressTxWarning records an explicit WithoutCreditNoteTransactions() opt-in
	// so the default-NoopTxManager warning is not emitted for intentional non-atomic use.
	suppressTxWarning bool
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
		repos := tx.Repos{
			Invoices:    invoiceRepo,
			CreditNotes: creditNoteRepo,
		}
		if s.suppressTxWarning {
			s.txManager = tx.NewNoopTxManagerExplicit(repos)
		} else {
			s.txManager = tx.NewNoopTxManager(repos)
		}
	}
	tx.WarnIfDefaultNoop(s.logger, s.txManager, "CreditNoteService", "wire WithCreditNoteTxManager(...) (or WithoutCreditNoteTransactions() to acknowledge non-atomic in-memory use)")
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
// must not exceed the invoice total) is evaluated inside a single transaction
// via tx.Run. The invoice is loaded through the transaction-scoped
// repos.Invoices.FindByID, which a real database adapter backs with a row lock
// (SELECT ... FOR UPDATE); concurrent CreateCreditNote calls for the same
// invoice are therefore serialized. The loser blocks until the winner commits,
// then re-reads the now-larger aggregate of existing credit notes and is
// rejected with business_rule_violation, so two callers cannot collectively
// over-credit an invoice (issue #124). The in-memory NoopTxManager runs the
// closure inline without real locking; consumers that need the invariant under
// concurrency must supply a TxManager whose repos.Invoices.FindByID takes the
// row lock (the standard financial-ledger pessimistic-lock pattern).
func (s *CreditNoteService) CreateCreditNote(
	ctx context.Context,
	invoiceID shared.InvoiceID,
	reason invoice.CreditNoteReason,
	items []invoice.CreditNoteItem,
	memo string,
) (*invoice.CreditNote, error) {
	if len(items) == 0 {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"credit note must have at least one item")
	}

	var cn *invoice.CreditNote
	err := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		// Fall back to the field repos when the caller wired an incomplete tx.Repos
		// set (mirrors tx.Run's fill-from-fallback for joined transactions).
		invoiceRepo := repos.Invoices
		if invoiceRepo == nil {
			invoiceRepo = s.invoiceRepo
		}
		creditNoteRepo := repos.CreditNotes
		if creditNoteRepo == nil {
			creditNoteRepo = s.creditNoteRepo
		}

		// Load the invoice inside the transaction. A real adapter backs this with
		// SELECT ... FOR UPDATE, serializing concurrent issuance on this invoice.
		inv, err := invoiceRepo.FindByID(txCtx, invoiceID)
		if err != nil {
			return fmt.Errorf("failed to find invoice: %w", err)
		}

		if !creditNoteEligibleStatuses[inv.Status()] {
			return shared.NewDomainError(shared.ErrCodeBusinessRule,
				fmt.Sprintf("cannot create credit note for invoice in status %s", inv.Status()))
		}

		// Validate that credit note total does not exceed original invoice total.
		// The credit note must be denominated in the invoice's currency; the
		// over-credit comparison below uses Money.GreaterThanStrict so a currency
		// mismatch surfaces as an error rather than an arbitrarily large foreign
		// total slipping through (review #2 / issue #196). Anchor the currency on
		// the invoice, not on items[0].
		currency := inv.Total().Currency()
		itemSubtotal := shared.Zero(currency)
		itemTax := shared.Zero(currency)
		for _, item := range items {
			if item.Amount().Currency() != currency {
				return shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
					fmt.Sprintf("credit note item currency %s does not match invoice currency %s",
						item.Amount().Currency(), currency))
			}
			sub, err := itemSubtotal.Add(item.Amount())
			if err != nil {
				return fmt.Errorf("failed to sum credit note item amounts: %w", err)
			}
			itemSubtotal = sub

			itemTaxAmt := item.TaxAmount()
			if itemTaxAmt.IsZero() {
				itemTaxAmt = shared.Zero(currency)
			}
			if itemTaxAmt.Currency() != currency {
				return shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
					fmt.Sprintf("credit note item tax currency %s does not match invoice currency %s",
						itemTaxAmt.Currency(), currency))
			}
			ta, err := itemTax.Add(itemTaxAmt)
			if err != nil {
				return fmt.Errorf("failed to sum credit note item taxes: %w", err)
			}
			itemTax = ta
		}
		cnTotal, err := itemSubtotal.Add(itemTax)
		if err != nil {
			return fmt.Errorf("failed to compute credit note total: %w", err)
		}

		// Aggregate previously-issued (non-voided) credit notes for this invoice so the
		// cumulative credited amount cannot exceed the invoice total. Reading through the
		// transaction-scoped repo (under the invoice row lock) guarantees this sees every
		// credit note a prior serialized transaction committed for the same invoice.
		existingCNs, err := creditNoteRepo.FindByInvoiceID(txCtx, invoiceID)
		if err != nil {
			return fmt.Errorf("failed to load existing credit notes: %w", err)
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
				return fmt.Errorf("failed to aggregate existing credit notes: %w", err)
			}
		}
		cumulative, err := creditedSoFar.Add(cnTotal)
		if err != nil {
			return fmt.Errorf("failed to compute cumulative credit total: %w", err)
		}
		// GreaterThanStrict surfaces a currency mismatch as an error rather than
		// silently reading as "not greater" (issue #196). currency is anchored on
		// the invoice and every summed amount was validated against it above, so
		// the mismatch branch is defensive.
		exceeds, err := cumulative.GreaterThanStrict(inv.Total())
		if err != nil {
			return shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("cumulative credit note currency %s does not match invoice currency %s",
					cumulative.Currency(), inv.Total().Currency()))
		}
		if exceeds {
			return shared.NewDomainError(shared.ErrCodeBusinessRule,
				fmt.Sprintf("cumulative credit note total %s exceeds invoice total %s",
					cumulative.Amount().RatString(), inv.Total().Amount().RatString()))
		}

		var opts []invoice.CreditNoteOption
		if memo != "" {
			opts = append(opts, invoice.WithCreditNoteMemo(memo))
		}

		created, err := invoice.NewCreditNote(
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
			return err
		}

		if err := creditNoteRepo.Save(txCtx, created); err != nil {
			return fmt.Errorf("failed to save credit note: %w", err)
		}

		cn = created
		return nil
	})
	if err != nil {
		return nil, err
	}

	return cn, nil
}

// creditNoteMaxRetries bounds how many times the credit-note transition methods
// re-run their transaction closure on an optimistic-lock conflict (issue #151),
// mirroring finalizeMaxRetries in BillingService.FinalizeInvoice.
const creditNoteMaxRetries = 3

// txScopedCreditNoteRepo returns the transaction-scoped credit note repository,
// falling back to the field repo when the caller wired an incomplete tx.Repos
// set (mirrors CreateCreditNote / tx.Run's fill-from-fallback for joined
// transactions).
func (s *CreditNoteService) txScopedCreditNoteRepo(repos tx.Repos) invoice.CreditNoteRepository {
	if repos.CreditNotes != nil {
		return repos.CreditNotes
	}
	return s.creditNoteRepo
}

// IssueCreditNote transitions a credit note from draft to issued and fires hooks.
//
// Load → state check → transition → save run inside a single transaction and are
// retried on an optimistic-lock conflict, following the FinalizeInvoice pattern
// (issue #151). The credit note is loaded through the transaction-scoped
// repository so the check-then-act is not split across the tx boundary: a
// concurrent transition on the same note is either serialized (row lock) or its
// Save is rejected with tx.ErrVersionConflict (issue #147 version machinery), in
// which case RetryOnConflict re-reads the now-issued note and Issue rejects the
// retry with invalid_state_transition. This guarantees OnCreditNoteIssued fires
// exactly once per note, so downstream ledger postings are not double-applied.
func (s *CreditNoteService) IssueCreditNote(ctx context.Context, creditNoteID shared.CreditNoteID) (*invoice.CreditNote, error) {
	var cn *invoice.CreditNote
	err := tx.RetryOnConflict(creditNoteMaxRetries, func() error {
		var issued *invoice.CreditNote
		runErr := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			repo := s.txScopedCreditNoteRepo(repos)
			loaded, findErr := repo.FindByID(txCtx, creditNoteID)
			if findErr != nil {
				return fmt.Errorf("failed to find credit note: %w", findErr)
			}
			if issueErr := loaded.Issue(s.clock.Now()); issueErr != nil {
				return issueErr
			}
			if saveErr := repo.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			issued = loaded
			return nil
		})
		if runErr != nil {
			return runErr
		}
		cn = issued
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Post-commit hooks (non-fatal, outside transaction)
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnCreditNoteIssuedHooks() {
		if hookErr := plugin.SafeInvoke("OnCreditNoteIssuedHook.OnCreditNoteIssued", hook.Name(), func() error {
			return hook.OnCreditNoteIssued(pluginCtx, cn)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "OnCreditNoteIssued hook failed", hookErr,
				"hook", hook.Name(),
				"creditNoteID", cn.ID(),
			)
		}
	}

	return cn, nil
}

// ApplyCreditNote transitions a credit note from issued to applied (account credit).
//
// Load → state check → transition → save run inside a single transaction and are
// retried on an optimistic-lock conflict (issue #151), so a concurrent Apply-vs-
// Refund on the same issued note cannot both persist: the loser's Save is
// rejected with tx.ErrVersionConflict, RetryOnConflict re-reads the now-applied
// note, and Apply rejects the retry with invalid_state_transition.
func (s *CreditNoteService) ApplyCreditNote(ctx context.Context, creditNoteID shared.CreditNoteID, creditAmount shared.Money) (*invoice.CreditNote, error) {
	var cn *invoice.CreditNote
	err := tx.RetryOnConflict(creditNoteMaxRetries, func() error {
		var applied *invoice.CreditNote
		runErr := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			repo := s.txScopedCreditNoteRepo(repos)
			loaded, findErr := repo.FindByID(txCtx, creditNoteID)
			if findErr != nil {
				return fmt.Errorf("failed to find credit note: %w", findErr)
			}
			if applyErr := loaded.Apply(creditAmount); applyErr != nil {
				return applyErr
			}
			if saveErr := repo.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			applied = loaded
			return nil
		})
		if runErr != nil {
			return runErr
		}
		cn = applied
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cn, nil
}

// RefundCreditNote transitions a credit note from issued to refunded (payment refund).
//
// Load → state check → transition → save run inside a single transaction and are
// retried on an optimistic-lock conflict (issue #151), mirroring ApplyCreditNote:
// the loser of a concurrent Apply-vs-Refund is rejected with a clean domain error
// rather than double-persisting.
func (s *CreditNoteService) RefundCreditNote(ctx context.Context, creditNoteID shared.CreditNoteID, refundAmount shared.Money) (*invoice.CreditNote, error) {
	var cn *invoice.CreditNote
	err := tx.RetryOnConflict(creditNoteMaxRetries, func() error {
		var refunded *invoice.CreditNote
		runErr := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			repo := s.txScopedCreditNoteRepo(repos)
			loaded, findErr := repo.FindByID(txCtx, creditNoteID)
			if findErr != nil {
				return fmt.Errorf("failed to find credit note: %w", findErr)
			}
			if refundErr := loaded.Refund(refundAmount); refundErr != nil {
				return refundErr
			}
			if saveErr := repo.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			refunded = loaded
			return nil
		})
		if runErr != nil {
			return runErr
		}
		cn = refunded
		return nil
	})
	if err != nil {
		return nil, err
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

	// All writes are atomic within a SINGLE transaction. tx.Run stamps the
	// transaction onto the context, and the inner
	// BillingService.GenerateInvoice (also via tx.Run) detects and JOINS it
	// rather than opening an independent nested transaction — so the void of the
	// original and the creation of the replacement commit or roll back together
	// even on real DB implementations (review #4).
	//
	// The original invoice is loaded INSIDE the transaction through the
	// transaction-scoped repository (issue #151), mirroring CreateCreditNote. On
	// a backend honouring the invoice concurrency contract this serializes with a
	// concurrent payment: if the invoice was paid between an out-of-tx read and
	// the transaction, VoidWithReason acts on the current (paid) state and either
	// rejects the void or the Save version-checks, instead of a stale copy
	// silently overwriting a paid invoice with voided.
	var (
		original    *invoice.Invoice
		replacement *invoice.Invoice
	)
	err := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		invoiceRepo := repos.Invoices
		if invoiceRepo == nil {
			invoiceRepo = s.invoiceRepo
		}

		loaded, findErr := invoiceRepo.FindByID(txCtx, originalInvoiceID)
		if findErr != nil {
			return fmt.Errorf("failed to find invoice: %w", findErr)
		}
		original = loaded

		// Determine the root of the revision chain. If the original already has
		// an originalInvoiceID (it's itself a revision), propagate that root.
		// Otherwise, the original IS the root.
		rootID := originalInvoiceID
		if original.OriginalInvoiceID() != nil {
			rootID = *original.OriginalInvoiceID()
		}

		// Void the original inside the transaction so in-memory state
		// is only mutated when the transaction will persist it.
		if voidErr := original.VoidWithReason(reason); voidErr != nil {
			return fmt.Errorf("failed to void original invoice: %w", voidErr)
		}

		if saveErr := invoiceRepo.Save(txCtx, original); saveErr != nil {
			return fmt.Errorf("failed to save voided invoice: %w", saveErr)
		}

		// Return any credit the original invoice consumed back to the ledger
		// BEFORE generating the replacement (issue #184). Without this the
		// replacement's own credit application would find zero balance and bill
		// full price while the credit stays consumed against the now-voided
		// invoice forever. Runs inside this same transaction (the BillingService
		// joins it), so the void, the credit restoration, and the replacement's
		// re-application commit or roll back together. Idempotent on retry.
		if restoreErr := s.billingSvc.RestoreBalancesForVoidedInvoice(txCtx, originalInvoiceID); restoreErr != nil {
			return fmt.Errorf("failed to restore credits from voided invoice: %w", restoreErr)
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

		if saveErr := invoiceRepo.Save(txCtx, replacement); saveErr != nil {
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
		if hookErr := plugin.SafeInvoke("OnInvoiceRevisedHook.OnInvoiceRevised", hook.Name(), func() error {
			return hook.OnInvoiceRevised(pluginCtx, original, replacement)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "OnInvoiceRevised hook failed", hookErr,
				"hook", hook.Name(),
				"originalInvoiceID", originalInvoiceID,
				"replacementInvoiceID", replacement.ID(),
			)
		}
	}

	return replacement, nil
}
