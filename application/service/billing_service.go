// Package service provides application-level services that orchestrate
// domain logic, plugin hooks, and external integrations.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/product"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
	"github.com/contract-to-cash/core/plugin"
)

// BillingConfig holds billing service configuration.
// Use NewBillingConfig() for validated construction with defaults.
// Zero-value struct literal (BillingConfig{}) remains valid for backward compatibility;
// zero values are treated as "use defaults" at usage time.
type BillingConfig struct {
	// GracePeriod is integrator-interpreted configuration: core does not act on
	// it and does not enforce it. The integrator's scheduler decides when to call
	// FinalizeInvoice; this value is carried as configuration for that scheduler
	// (e.g. how long to leave a draft open after GenerateInvoice so late usage
	// records can be absorbed and InvoiceLifecycleHooks can adjust it). The core
	// billing pipeline never reads it.
	GracePeriod time.Duration

	// DaysUntilDue is the number of days added to the invoice's issue date
	// (clock.Now() at generation time) to compute its due date. The anchor is the
	// issue date (now), not the billing period end. When left at its zero value,
	// GenerateInvoice falls back to a 30-day due date (see effectiveDaysUntilDue),
	// so BillingConfig{} does not produce an immediately-due invoice.
	DaysUntilDue int

	// CollectionMethod is integrator-interpreted configuration: core does not act
	// on it and never auto-charges. It is carried for the integrator's collection
	// flow (e.g. to decide whether to auto-charge via the payment gateway or send
	// the invoice and wait for payment). The core billing pipeline does not read
	// it to drive any behavior.
	CollectionMethod CollectionMethod

	// AllowPartialPayment, when true, marks generated invoices as accepting
	// partial payments (Invoice.allowPartialPay). Default false: a payment must
	// settle the full amount due in one go (design-decisions 3.1).
	AllowPartialPayment bool

	// TaxRoundingMode is the rounding mode the billing pipeline applies when it
	// quantises amounts to the invoice currency's minor unit (issue #189). The
	// pipeline rounds the subtotal, the total discount, and the total tax to the
	// currency's minor unit (JPY -> integer yen, USD/EUR -> cents) so the
	// persisted subtotal/discount/tax/total/amountDue are all integral in minor
	// units and reconcile exactly against integer-only payment gateways.
	//
	// Default (zero value) is shared.RoundDown: it rounds toward zero, matching
	// Japanese consumption-tax practice of truncating the per-invoice tax
	// (端数切り捨て) and guaranteeing the pipeline never rounds an amount UP past
	// what the exact calculation produced (a rounded discount never exceeds the
	// exact discount, a rounded tax never overcharges). Set shared.RoundHalfUp
	// or shared.RoundUp via WithTaxRoundingMode when a jurisdiction requires it.
	TaxRoundingMode shared.RoundingMode
}

// BillingServiceOption configures optional dependencies of BillingService.
type BillingServiceOption func(*BillingService)

// WithBillingLogger sets a structured logger for the BillingService.
// If not provided, slog.Default() is used.
func WithBillingLogger(l *slog.Logger) BillingServiceOption {
	return func(s *BillingService) {
		s.logger = l
	}
}

// WithBalanceRepo sets the credit repository used for credit application.
func WithBalanceRepo(repo balance.Repository) BillingServiceOption {
	return func(s *BillingService) {
		s.balanceRepo = repo
	}
}

// WithBillingTxManager sets the transaction manager for the BillingService.
// If not provided, a NoopTxManager is used (no transaction wrapping) and a
// Warn-level log is emitted at construction (see WithoutTransactions to opt out).
func WithBillingTxManager(tm tx.TxManager) BillingServiceOption {
	return func(s *BillingService) {
		s.txManager = tm
	}
}

// WithoutTransactions explicitly opts the BillingService into running without a
// transaction manager (the multi-write billing pipeline will NOT be atomic).
// Use it for in-memory demos and tests where that trade-off is intentional; it
// suppresses the non-atomic warning that a silently-defaulted NoopTxManager
// would otherwise emit. Do NOT use it in production with real repositories.
func WithoutTransactions() BillingServiceOption {
	return func(s *BillingService) {
		s.suppressTxWarning = true
	}
}

// BillingService orchestrates the invoice generation flow.
type BillingService struct {
	contractRepo  contract.Repository
	invoiceRepo   invoice.Repository
	usageRepo     usage.Repository
	balanceRepo   balance.Repository
	balanceConfig balance.BalanceConfig
	priceRepo     pricing.PriceRepository
	productRepo   product.Repository
	registry      *plugin.Registry
	config        BillingConfig
	clock         shared.Clock
	logger        *slog.Logger
	txManager     tx.TxManager
	// suppressTxWarning records an explicit WithoutTransactions() opt-in so the
	// default-NoopTxManager warning is not emitted for intentional non-atomic use.
	suppressTxWarning bool
}

// NewBillingService creates a new BillingService.
// Required dependencies are positional arguments; optional dependencies
// (logger, credit repository) are provided via BillingServiceOption.
func NewBillingService(
	contractRepo contract.Repository,
	invoiceRepo invoice.Repository,
	usageRepo usage.Repository,
	balanceConfig balance.BalanceConfig,
	priceRepo pricing.PriceRepository,
	productRepo product.Repository,
	registry *plugin.Registry,
	config BillingConfig,
	clock shared.Clock,
	opts ...BillingServiceOption,
) *BillingService {
	s := &BillingService{
		contractRepo:  contractRepo,
		invoiceRepo:   invoiceRepo,
		usageRepo:     usageRepo,
		balanceConfig: balanceConfig,
		priceRepo:     priceRepo,
		productRepo:   productRepo,
		registry:      registry,
		config:        config,
		clock:         clock,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.txManager == nil {
		repos := tx.Repos{
			Contracts: contractRepo,
			Invoices:  invoiceRepo,
			Balances:  s.balanceRepo,
		}
		if s.suppressTxWarning {
			s.txManager = tx.NewNoopTxManagerExplicit(repos)
		} else {
			s.txManager = tx.NewNoopTxManager(repos)
		}
	}
	tx.WarnIfDefaultNoop(s.logger, s.txManager, "BillingService", "wire WithBillingTxManager(...) (or WithoutTransactions() to acknowledge non-atomic in-memory use)")
	return s
}

// finalizeMaxRetries bounds how many times FinalizeInvoice re-runs its
// load→finalize→save closure on an optimistic-lock conflict. A conflict means
// another caller finalized first; on retry this call re-reads the finalized row
// and is rejected with invalid_state_transition, so a small bound suffices.
const finalizeMaxRetries = 3

// billableStatuses defines which contract statuses allow invoice generation.
var billableStatuses = map[contract.ContractStatus]bool{
	contract.ContractStatusDraft:    true,
	contract.ContractStatusActive:   true,
	contract.ContractStatusTrialing: true,
	contract.ContractStatusPastDue:  true,
	// Suspended is conditionally allowed based on BillingBehavior (checked separately).
	contract.ContractStatusSuspended: true,
}

// pipelineInput holds the pre-calculated values needed by the billing pipeline.
// This allows GenerateInvoice and GenerateProrationInvoice to share the
// discount → tax → credit → lifecycle hook → save pipeline.
type pipelineInput struct {
	agg        *contract.ContractAggregate
	contractID shared.ContractID
	subtotal   shared.Money
	lineItems  []invoice.LineItem
	period     shared.DateRange // billing period for the invoice
	extraOpts  []invoice.InvoiceOption
	// duplicateCheck, when non-nil, re-runs the duplicate-invoice guard INSIDE
	// the tx.Run closure through the transaction-scoped repo (issue #149). The
	// pre-tx check is a cheap fast-fail, but the authoritative check must run in
	// the transaction so a backend that serializes reads (row lock / SERIALIZABLE)
	// closes the concurrent-insert window. Proration invoices pass nil because
	// they intentionally coexist with the period's regular invoice.
	duplicateCheck func(ctx context.Context) error
	// restoreVoidedInvoiceID, when non-empty, names a voided invoice whose consumed
	// credit must be returned to the ledger BEFORE this pipeline applies credit to
	// the new invoice (issue #184). RegenerateInvoice sets it to the voided
	// original so the credit consumed by the voided invoice is not double-charged:
	// it is restored and then re-applied (FIFO) to the regenerated invoice within
	// the same transaction. The restoration is idempotent (see restoreBalances).
	//
	// CONTRACT: the setter must have verified the invoice is actually voided —
	// the pipeline calls restoreBalances directly WITHOUT re-checking status
	// (RegenerateInvoice only reaches here after finding the invoice voided).
	// Restoring a non-voided invoice's applications would fabricate balance;
	// external callers go through RestoreBalancesForVoidedInvoice, which guards.
	restoreVoidedInvoiceID shared.InvoiceID
}

// invoiceRepoFor returns the transaction-scoped invoice repository when ctx
// carries an active transaction, falling back to the field repo otherwise. Reads
// issued during a transaction (e.g. the duplicate-invoice check) must use the
// tx-scoped repo so they observe writes made earlier in the same transaction —
// e.g. the void written by CreditNoteService.ReissueInvoice before it calls
// GenerateInvoice. A field-repo read runs on a separate connection on a real DB
// and would miss the uncommitted void, wrongly raising a conflict.
func (s *BillingService) invoiceRepoFor(ctx context.Context) invoice.Repository {
	if repos, ok := tx.ReposFromContext(ctx); ok && repos.Invoices != nil {
		return repos.Invoices
	}
	return s.invoiceRepo
}

// contractRepoFor returns the transaction-scoped contract repository when ctx
// carries an active transaction, falling back to the field repo otherwise.
func (s *BillingService) contractRepoFor(ctx context.Context) contract.Repository {
	if repos, ok := tx.ReposFromContext(ctx); ok && repos.Contracts != nil {
		return repos.Contracts
	}
	return s.contractRepo
}

// GenerateInvoice generates an invoice for a contract and billing period.
// It follows a 14-step calculation flow with plugin hooks.
func (s *BillingService) GenerateInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) (*invoice.Invoice, error) {
	// Step 1: Load contract aggregate (tx-scoped when inside a transaction)
	agg, err := s.contractRepoFor(ctx).FindByID(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("failed to load contract: %w", err)
	}
	if agg == nil {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found", contractID))
	}

	// Status guard: only billable statuses can generate invoices
	if !billableStatuses[agg.Status()] {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("cannot generate invoice: contract status is %s", agg.Status()))
	}

	// Suspended contracts: respect BillingBehavior
	if agg.Status() == contract.ContractStatusSuspended {
		cfg := agg.SuspensionConfig()
		if cfg == nil || cfg.BillingBehavior == contract.SuspensionBillingSkip {
			return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
				"cannot generate invoice: billing is skipped during suspension")
		}
		if cfg.BillingBehavior == contract.SuspensionBillingDefer {
			return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
				"cannot generate invoice: billing is deferred during suspension")
		}
	}

	// Duplicate invoice prevention
	if err = s.checkDuplicateInvoice(ctx, agg, billingPeriod); err != nil {
		return nil, err
	}

	// Calculate subtotal based on contract type
	subtotal, lineItems, err := s.calculateSubtotal(ctx, agg, billingPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate subtotal: %w", err)
	}

	return s.executeBillingPipeline(ctx, pipelineInput{
		agg:        agg,
		contractID: contractID,
		subtotal:   subtotal,
		lineItems:  lineItems,
		period:     billingPeriod,
		// Re-run the same duplicate guard inside the transaction so a
		// serializing backend closes the concurrent-generate window; the
		// in-memory repo's per-period uniqueness backstops non-serializing
		// backends (issue #149).
		duplicateCheck: func(txCtx context.Context) error {
			return s.checkDuplicateInvoice(txCtx, agg, billingPeriod)
		},
	})
}

// RegenerateInvoice regenerates an invoice for a contract and billing period.
// Unlike GenerateInvoice, it permits invoice generation for contracts suspended
// with SuspensionBillingDefer, provided a voided invoice already exists for the
// same period. This supports the void-and-recreate workflow (e.g., applying a
// coupon during Payment-Gated Provisioning without polluting the event store
// with spurious Resume/Suspend events).
//
// SuspensionBillingSkip still blocks — skip means no billing at all.
// SuspensionBillingContinue is allowed (continue means billing proceeds normally).
// Contracts in non-billable statuses (cancelled, expired) are also blocked.
//
// The regenerated invoice is linked to the voided invoice via RevisionOf/OriginalInvoiceID
// to maintain the audit trail. When a period has been void-and-recreated more than
// once, the revision chain links to the voided invoice with the greatest ID
// (deterministic — see the selection comment below; ULID creation order stands in
// for a void timestamp), so regeneration is reproducible regardless of the order
// FindByContractAndPeriod returns rows in (issue #197).
func (s *BillingService) RegenerateInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) (*invoice.Invoice, error) {
	// Load contract aggregate (tx-scoped when inside a transaction, so reads see
	// writes made earlier in the same transaction — symmetry with GenerateInvoice)
	agg, err := s.contractRepoFor(ctx).FindByID(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("failed to load contract: %w", err)
	}
	if agg == nil {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found", contractID))
	}

	// Status guard: same as GenerateInvoice
	if !billableStatuses[agg.Status()] {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("cannot regenerate invoice: contract status is %s", agg.Status()))
	}

	// Suspended contracts: SuspensionBillingSkip always blocks.
	// SuspensionBillingDefer and SuspensionBillingContinue are permitted
	// (Defer only if a voided invoice exists, checked below; Continue always allowed).
	if agg.Status() == contract.ContractStatusSuspended {
		cfg := agg.SuspensionConfig()
		if cfg == nil || cfg.BillingBehavior == contract.SuspensionBillingSkip {
			return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
				"cannot regenerate invoice: billing is skipped during suspension")
		}
	}

	// Fetch existing invoices for the period once — used for both voided-check
	// and duplicate prevention (avoids a redundant FindByContractAndPeriod call).
	// Tx-scoped when inside a transaction so the void written earlier in the same
	// transaction is visible.
	existing, err := s.invoiceRepoFor(ctx).FindByContractAndPeriod(ctx, contractID, billingPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing invoices: %w", err)
	}

	// Require a voided REGULAR (non-proration) invoice for the same period —
	// this distinguishes regeneration from net-new invoice creation and prevents
	// misuse as a bypass. Voided proration invoices are excluded from both the
	// gate and the selection (issue #232): RegenerateInvoice regenerates the
	// period's REGULAR invoice, so (a) a voided proration must never become the
	// revision root / balance-restoration target (its restoration would return
	// the proration's consumed credit while the voided regular invoice's credit
	// stays lost), and (b) a voided proration ALONE must not authorize minting a
	// net-new full-period invoice for a period that never had a regular one.
	//
	// Deterministic selection (issue #197): FindByContractAndPeriod returns
	// invoices in an unspecified order (the in-memory repo iterates a map), so
	// picking "the last one seen" linked the revision chain to a nondeterministic
	// invoice when a period had been void-and-recreated more than once. We instead
	// link to the voided invoice with the greatest ID. Invoice IDs are ULIDs whose
	// leading bits are a creation timestamp, so lexicographic max == most recently
	// created == most recently voided in a void-and-recreate sequence (each cycle
	// voids the current invoice and creates a newer one). The Invoice entity has no
	// dedicated voidedAt timestamp, so the ULID's creation ordering is the stable
	// stand-in; the choice is documented on RegenerateInvoice.
	var voidedInv *invoice.Invoice
	for _, inv := range existing {
		if inv.Status() != invoice.InvoiceStatusVoided || inv.IsProration() {
			continue
		}
		if voidedInv == nil || inv.ID() > voidedInv.ID() {
			voidedInv = inv
		}
	}
	if voidedInv == nil {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			"cannot regenerate invoice: no voided invoice found for this period")
	}

	// Inline duplicate check: reject if an invoice that participates in the
	// per-period uniqueness constraint already exists for this period (same
	// logic as checkDuplicateInvoice for subscription/usage-based, but using the
	// already-fetched existing slice). Voided and proration invoices are exempt
	// (issue #232) — the predicate is the domain's
	// Invoice.ParticipatesInPeriodUniqueness, the same one the repository
	// constraint enforces, so the layers cannot drift.
	for _, inv := range existing {
		if inv.ParticipatesInPeriodUniqueness() {
			return nil, shared.NewDomainError(shared.ErrCodeConflict,
				"invoice already exists for this billing period")
		}
	}

	// Calculate subtotal based on contract type
	subtotal, lineItems, err := s.calculateSubtotal(ctx, agg, billingPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate subtotal: %w", err)
	}

	// Build revision chain: link the regenerated invoice to the voided original.
	// If the voided invoice itself was a revision, preserve the original chain root.
	var extraOpts []invoice.InvoiceOption
	extraOpts = append(extraOpts, invoice.WithRevisionOf(voidedInv.ID()))
	if orig := voidedInv.OriginalInvoiceID(); orig != nil {
		extraOpts = append(extraOpts, invoice.WithOriginalInvoiceID(*orig))
	} else {
		extraOpts = append(extraOpts, invoice.WithOriginalInvoiceID(voidedInv.ID()))
	}
	extraOpts = append(extraOpts, invoice.WithMetadata(map[string]string{
		invoice.MetadataKeyInvoiceType: invoice.InvoiceTypeRegeneration,
	}))

	return s.executeBillingPipeline(ctx, pipelineInput{
		agg:        agg,
		contractID: contractID,
		subtotal:   subtotal,
		lineItems:  lineItems,
		period:     billingPeriod,
		extraOpts:  extraOpts,
		// Return credit consumed by the voided invoice to the ledger before the
		// pipeline re-applies credit to the regenerated invoice (issue #184).
		restoreVoidedInvoiceID: voidedInv.ID(),
		// In-tx re-check (issue #149): reject if a non-voided invoice appeared
		// for this period between the pre-tx check and the save. The voided
		// original is excluded, so void-and-recreate still succeeds.
		duplicateCheck: func(txCtx context.Context) error {
			return s.rejectIfActivePeriodInvoice(txCtx, contractID, billingPeriod)
		},
	})
}

// GenerateProrationInvoice generates an invoice for a proration adjustment.
// It runs the full billing pipeline (discount hooks, tax hooks, credit application,
// lifecycle hooks) on the proration amount, unlike direct invoice.NewInvoice construction.
//
// The proration parameter comes from a ChangePrice(ChangePolicyImmediate) operation.
// Only the AdjustmentAmount (charge - credit) is billed; CreditAmount and ChargeAmount
// are recorded in line items for traceability.
func (s *BillingService) GenerateProrationInvoice(ctx context.Context, contractID shared.ContractID, proration contract.PlanChangeProration) (*invoice.Invoice, error) {
	// Load contract aggregate (tx-scoped when inside a transaction, symmetric with
	// GenerateInvoice/RegenerateInvoice)
	agg, err := s.contractRepoFor(ctx).FindByID(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("failed to load contract: %w", err)
	}
	if agg == nil {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("contract %s not found", contractID))
	}

	// Proration invoices are only valid for active contracts
	if agg.Status() != contract.ContractStatusActive {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("cannot generate proration invoice: contract status is %s", agg.Status()))
	}

	// Only positive adjustments (upgrades) go through the billing pipeline.
	// Downgrades (negative AdjustmentAmount) should be handled via BalancePolicy
	// (ledger credit, immediate refund, or discard) — see domain-model.md §10.6.
	// Zero adjustments (same-price changes) don't need an invoice.
	if proration.AdjustmentAmount.IsZero() || proration.AdjustmentAmount.IsNegative() {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"proration adjustment must be positive (upgrade); downgrades should use BalancePolicy")
	}

	// Note: checkDuplicateInvoice is intentionally not called here.
	// Proration invoices coexist with period invoices for the same billing period.
	// Callers must ensure they don't invoke this method multiple times for the
	// same price change (the idempotency boundary is the ChangePrice command).

	// Build line items from proration breakdown
	var lineItems []invoice.LineItem

	// Credit line item (negative — what the customer overpaid on the old price)
	if !proration.CreditAmount.IsZero() {
		creditNeg := proration.CreditAmount.Negate()
		li, err := invoice.NewLineItem(
			shared.GenerateID(),
			"Proration credit (unused period on previous price)",
			1, creditNeg, creditNeg, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create proration credit line item: %w", err)
		}
		lineItems = append(lineItems, li)
	}

	// Charge line item (positive — what the customer owes on the new price)
	if !proration.ChargeAmount.IsZero() {
		li, err := invoice.NewLineItem(
			shared.GenerateID(),
			"Proration charge (remaining period on new price)",
			1, proration.ChargeAmount, proration.ChargeAmount, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create proration charge line item: %w", err)
		}
		lineItems = append(lineItems, li)
	}

	// Subtotal is the net adjustment amount
	subtotal := proration.AdjustmentAmount

	// Use the contract's current period as the billing period
	period := agg.CurrentPeriod()

	// Mark the invoice as a proration invoice via metadata
	prorationMeta := map[string]string{
		invoice.MetadataKeyInvoiceType: invoice.InvoiceTypeProration,
		"effective_date":               proration.EffectiveDate.Format(time.RFC3339),
		"credit_amount":                proration.CreditAmount.Amount().RatString(),
		"charge_amount":                proration.ChargeAmount.Amount().RatString(),
	}

	return s.executeBillingPipeline(ctx, pipelineInput{
		agg:        agg,
		contractID: contractID,
		subtotal:   subtotal,
		lineItems:  lineItems,
		period:     period,
		extraOpts:  []invoice.InvoiceOption{invoice.WithMetadata(prorationMeta)},
	})
}

// executeBillingPipeline runs the shared billing pipeline:
// BeforeCalculation → Discount → Tax → Credit application → Invoice creation → AfterCalculation → Save.
func (s *BillingService) executeBillingPipeline(ctx context.Context, input pipelineInput) (*invoice.Invoice, error) {
	agg := input.agg
	// Minor-unit rounding mode for this pipeline (issue #189). Every amount the
	// pipeline persists is quantised to the currency's minor unit with this mode
	// so the invoice reconciles exactly against integer-only payment gateways.
	roundingMode := s.config.effectiveTaxRoundingMode()
	// Quantise the subtotal up front: usage-based subtotals in particular can be
	// exact rationals (e.g. tiered per-unit prices). Rounding here keeps the
	// value the discount hooks see, and every downstream amount, integral in
	// minor units.
	subtotal := input.subtotal.RoundToMinorUnit(roundingMode)
	currency := subtotal.Currency()
	calcCtx := plugin.NewCalculationContext(ctx, agg, shared.Zero(currency))

	// Expose the billing period to calculation hooks so plugins can key
	// period-scoped side effects idempotently (e.g. coupon redemptions keyed by
	// (coupon, contract, period) so a retry / RegenerateInvoice for the same
	// period does not double-consume a use — issue #185).
	calcCtx.SetBillingPeriod(input.period)

	// Resolve ProductID from PriceID for plugin context (e.g. coupon applicability)
	if priceID := agg.PriceID(); priceID != "" {
		priceEntity, priceErr := s.priceRepo.FindByID(ctx, priceID)
		if priceErr != nil {
			return nil, fmt.Errorf("failed to load price for product resolution: %w", priceErr)
		}
		if priceEntity == nil {
			return nil, shared.NewDomainError(shared.ErrCodeNotFound,
				fmt.Sprintf("price %s not found", priceID))
		}
		calcCtx.SetProductID(priceEntity.ProductID())
	}

	// BeforeCalculation (InvoiceLifecycleHooks). Veto-capable: a returned error
	// or a recovered panic aborts invoice generation (see plugin panic policy,
	// docs/internals/plugin-system.md §5.4).
	for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
		if err := plugin.SafeInvoke("InvoiceLifecycleHook.BeforeCalculation", hook.Name(), func() error {
			return hook.BeforeCalculation(calcCtx)
		}); err != nil {
			return nil, fmt.Errorf("BeforeCalculation hook error: %w", err)
		}
	}

	calcCtx.SetSubtotal(subtotal)

	// Execute all DiscountHooks
	totalDiscount := shared.Zero(currency)
	for _, hook := range s.registry.GetDiscountHooks() {
		discount, err := plugin.SafeInvokeMoney("DiscountHook.CalculateDiscount", hook.Name(), func() (shared.Money, error) {
			return hook.CalculateDiscount(calcCtx)
		})
		if err != nil {
			return nil, fmt.Errorf("DiscountHook error: %w", err)
		}
		// Boundary validation (issue #188): a discount hook must return a
		// non-negative amount. A negative discount would inflate the subtotal
		// (afterDiscount = subtotal - discount > subtotal), silently over-billing.
		if discount.IsNegative() {
			return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
				fmt.Sprintf("discount hook %q returned a negative discount: %s",
					hook.Name(), discount.Amount().RatString()))
		}
		totalDiscount, err = totalDiscount.Add(discount)
		if err != nil {
			// A currency mismatch surfaces here; attribute it to the offending
			// plugin so the failure is diagnosable.
			return nil, shared.NewDomainErrorWithCause(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("discount hook %q returned an incompatible currency", hook.Name()), err)
		}
	}

	// Quantise the aggregate discount to the currency's minor unit (issue #189)
	// so subtotal - discount is integral. RoundDown (the default) never rounds
	// the discount up past the exact figure the hooks returned.
	totalDiscount = totalDiscount.RoundToMinorUnit(roundingMode)

	// Cap discount to subtotal (discount must not exceed subtotal)
	if totalDiscount.GreaterThan(subtotal) {
		totalDiscount = subtotal
	}

	// Calculate subtotal after discount
	afterDiscount, err := subtotal.Subtract(totalDiscount)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate after-discount: %w", err)
	}
	calcCtx.SetSubtotalAfterDiscount(afterDiscount)

	// Execute all TaxHooks
	totalTax := shared.Zero(currency)
	for _, hook := range s.registry.GetTaxHooks() {
		tax, err := plugin.SafeInvokeMoney("TaxHook.CalculateTax", hook.Name(), func() (shared.Money, error) {
			return hook.CalculateTax(calcCtx)
		})
		if err != nil {
			return nil, fmt.Errorf("TaxHook error: %w", err)
		}
		// Boundary validation (issue #188): a tax hook must return a non-negative
		// amount. A negative tax exceeding afterDiscount would drive the invoice
		// total below zero, producing an unsettleable invoice.
		if tax.IsNegative() {
			return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
				fmt.Sprintf("tax hook %q returned a negative tax: %s",
					hook.Name(), tax.Amount().RatString()))
		}
		totalTax, err = totalTax.Add(tax)
		if err != nil {
			return nil, shared.NewDomainErrorWithCause(shared.ErrCodeCurrencyMismatch,
				fmt.Sprintf("tax hook %q returned an incompatible currency", hook.Name()), err)
		}
	}

	// Quantise the aggregate tax to the currency's minor unit (issue #189): tax
	// hooks compute rate*afterDiscount exactly (e.g. ¥101 -> ¥101 tax at 10% is
	// ¥10.1), which no integer-only gateway can settle. Rounding here — after
	// summing all tax hooks, once per invoice — makes the tax and therefore the
	// total and amountDue integral in minor units. Since afterDiscount is already
	// integral, total = afterDiscount + roundedTax stays integral.
	totalTax = totalTax.RoundToMinorUnit(roundingMode)

	// total = afterDiscount + totalTax
	total, err := afterDiscount.Add(totalTax)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total: %w", err)
	}

	// Generate invoice ID upfront (needed for CreditApplication records)
	invoiceID := shared.NewInvoiceID()

	// All writes are atomic within a transaction. tx.Run joins an outer
	// transaction if one is already active (e.g. when invoked from
	// CreditNoteService.ReissueInvoice) instead of opening an independent
	// nested one — see review #4.
	var inv *invoice.Invoice
	err = tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		// Re-run the duplicate-invoice guard INSIDE the transaction through the
		// tx-scoped repo before doing any work (issue #149). On a backend that
		// serializes reads (row lock / SERIALIZABLE) this closes the window where
		// two concurrent GenerateInvoice calls both pass the pre-tx check and both
		// insert. The repository's per-period uniqueness (invoice.Repository.Save)
		// is the backstop for non-serializing backends.
		if input.duplicateCheck != nil {
			if dupErr := input.duplicateCheck(txCtx); dupErr != nil {
				return dupErr
			}
		}

		// Configuration guard (issue #241): when the service was constructed with
		// a balance repository (WithBalanceRepo) but the transaction manager's
		// Repos omits Balances, fail loudly instead of silently skipping credit
		// application below — a silent skip over-bills every customer whose
		// ledger credit should have been applied, and silently skips restoring
		// credit consumed by a voided invoice. tx.Run fills missing repos from a
		// reposProvider (e.g. the in-memory NoopTxManager) when joining an outer
		// transaction, and the default NoopTxManager carries the wired balance
		// repo, so demos/tests are unaffected; this fires only for a real
		// TxManager whose Repos wiring forgot Balances. When no balance repo was
		// ever wired, credit application is intentionally disabled and the skip
		// below is the documented behavior.
		if s.balanceRepo != nil && repos.Balances == nil {
			return shared.NewDomainError(shared.ErrCodeValidation,
				"billing misconfiguration: a balance repository is wired (WithBalanceRepo) but the TxManager's Repos does not provide Balances; credit application would be silently skipped — include the balance repository in the transaction-scoped Repos")
		}

		// Restore credit consumed by a voided invoice (issue #184) BEFORE applying
		// credit to the new invoice, so the restored balance is available for
		// FIFO re-application below. This is what makes void-and-recreate
		// (RegenerateInvoice) preserve the customer's credit instead of consuming
		// it against the voided invoice forever. Idempotent — a retry that already
		// restored will find the refund records and skip.
		if input.restoreVoidedInvoiceID != "" && repos.Balances != nil {
			if _, restoreErr := s.restoreBalances(txCtx, repos.Balances, input.restoreVoidedInvoiceID); restoreErr != nil {
				return fmt.Errorf("failed to restore credits from voided invoice: %w", restoreErr)
			}
		}

		// Apply credits (FIFO, skip expired)
		appliedBalance := shared.Zero(currency)
		if repos.Balances != nil {
			var balanceErr error
			appliedBalance, balanceErr = s.applyBalances(txCtx, repos.Balances, agg.AccountID(), invoiceID, total, currency)
			if balanceErr != nil {
				return fmt.Errorf("failed to apply credits: %w", balanceErr)
			}
		}

		// Calculate amount due
		amountDue, amtErr := total.Subtract(appliedBalance)
		if amtErr != nil {
			return fmt.Errorf("failed to calculate amount due: %w", amtErr)
		}

		// Create invoice. Due date is anchored on the issue date (now), not the
		// billing period end; a zero-value DaysUntilDue falls back to 30 days so
		// BillingConfig{} does not yield an immediately-due invoice.
		now := s.clock.Now()
		dueDate := now.AddDate(0, 0, s.config.effectiveDaysUntilDue())

		invOpts := []invoice.InvoiceOption{
			invoice.WithStatus(invoice.InvoiceStatusDraft),
			invoice.WithBillingPeriod(input.period),
			invoice.WithDueDate(dueDate),
			invoice.WithAppliedBalance(appliedBalance),
			invoice.WithAmountDue(amountDue),
			invoice.WithIssueDate(now),
			invoice.WithAllowPartialPayment(s.config.AllowPartialPayment),
		}
		if len(input.lineItems) > 0 {
			invOpts = append(invOpts, invoice.WithLineItems(input.lineItems))
		}
		if agg.PaymentMethodID() != nil {
			invOpts = append(invOpts, invoice.WithPaymentMethodID(agg.PaymentMethodID()))
		}
		invOpts = append(invOpts, input.extraOpts...)

		var invoiceErr error
		inv, invoiceErr = invoice.NewInvoice(
			invoiceID,
			agg.AccountID(),
			input.contractID,
			subtotal,
			totalDiscount,
			totalTax,
			invOpts...,
		)
		if invoiceErr != nil {
			return fmt.Errorf("invoice creation failed: %w", invoiceErr)
		}
		calcCtx.SetInvoice(inv)

		// AfterCalculation (InvoiceLifecycleHooks). Fires INSIDE the transaction,
		// before Save. It aborts the tx on a returned error, so a recovered panic
		// is converted to an error and returned here too: the panic must not
		// unwind through tx.Run (which would leave the backend's rollback
		// behaviour undefined) — instead the tx aborts cleanly and nothing is
		// persisted (plugin panic policy, docs/internals/plugin-system.md §5.4).
		for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
			if hookErr := plugin.SafeInvoke("InvoiceLifecycleHook.AfterCalculation", hook.Name(), func() error {
				return hook.AfterCalculation(calcCtx, inv)
			}); hookErr != nil {
				return fmt.Errorf("AfterCalculation hook error: %w", hookErr)
			}
		}

		// Save invoice
		if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
			return fmt.Errorf("failed to save invoice: %w", saveErr)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return inv, nil
}

// FinalizeInvoice transitions a draft invoice to finalized and fires the
// OnInvoiceIssued metrics hooks. Callers typically invoke this after
// BillingConfig.GracePeriod has elapsed since GenerateInvoice, during which
// late usage records can be absorbed and InvoiceLifecycleHooks can adjust
// the draft. Finalized invoices are immutable.
//
// The save happens BEFORE the hooks fire (same policy as the post-commit
// hooks in batch/contract_renewal.go) so plugins are never notified about
// an invoice that was not persisted. Hook errors are non-fatal: the invoice
// is already finalized and saved, so failures are logged and do not fail
// the finalization.
//
// Concurrency: two concurrent FinalizeInvoice calls are made safe by the
// invoice.Repository concurrency contract. When the repository implements
// optimistic locking, the race loser's Save returns tx.ErrVersionConflict;
// RetryOnConflict re-runs the closure, which re-reads the now-finalized row so
// Finalize rejects it with invalid_state_transition. Either way exactly one
// call finalizes and OnInvoiceIssued fires at most once. A repository that
// serializes reads (row lock / SERIALIZABLE) satisfies the same contract
// without conflicts. Against a last-writer-wins repository this guarantee does
// NOT hold — see Repository.Save.
//
// Nested-transaction caveat: tx.Run joins an outer transaction if the
// caller's ctx already carries one, and returns without committing it. In
// that case the hooks fire before the OUTER commit — if the caller then
// rolls back, plugins were notified about an invoice that was never
// persisted. Call FinalizeInvoice outside your own transactions, or defer
// hook-dependent side effects until the outer commit succeeds.
func (s *BillingService) FinalizeInvoice(ctx context.Context, invoiceID shared.InvoiceID) (*invoice.Invoice, error) {
	// Load, check and transition INSIDE the transaction (same in-tx re-check
	// pattern as PaymentService.ProcessPayment): a check-then-act split across
	// the tx boundary would let two concurrent calls both observe the draft
	// state and double-finalize. RetryOnConflict re-runs the closure when the
	// repository reports an optimistic-lock conflict; on retry the invoice is
	// already finalized, so Finalize rejects it with an
	// invalid_state_transition domain error (not a conflict, so no further
	// retry). tx.Run joins an outer transaction if one is already active.
	var inv *invoice.Invoice
	err := tx.RetryOnConflict(finalizeMaxRetries, func() error {
		var finalized *invoice.Invoice
		runErr := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			loaded, findErr := repos.Invoices.FindByID(txCtx, invoiceID)
			if findErr != nil {
				return fmt.Errorf("failed to load invoice: %w", findErr)
			}
			if loaded == nil {
				return shared.NewDomainError(shared.ErrCodeNotFound,
					fmt.Sprintf("invoice %s not found", invoiceID))
			}

			// Finalize enforces the draft→finalized transition; any other status
			// is rejected with an invalid_state_transition domain error.
			if finalizeErr := loaded.Finalize(); finalizeErr != nil {
				return finalizeErr
			}

			// Save may return tx.ErrVersionConflict under a concurrent
			// finalization; RetryOnConflict handles it.
			if saveErr := repos.Invoices.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			finalized = loaded
			return nil
		})
		if runErr != nil {
			return runErr
		}
		inv = finalized
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Post-commit metrics hooks — non-fatal.
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnInvoiceIssuedHooks() {
		if hookErr := plugin.SafeInvoke("OnInvoiceIssuedHook.OnInvoiceIssued", hook.Name(), func() error {
			return hook.OnInvoiceIssued(pluginCtx, inv)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "OnInvoiceIssued hook failed", hookErr,
				"hook", hook.Name(),
				"invoiceID", invoiceID,
			)
		}
	}

	return inv, nil
}

// calculateSubtotal calculates the subtotal based on the Price entity.
// It validates that the billing period matches the contract's current period,
// loads the Price entity, and delegates to usage charge calculation if needed.
// Returns the subtotal amount and line items with priceID for traceability.
func (s *BillingService) calculateSubtotal(ctx context.Context, agg *contract.ContractAggregate, billingPeriod shared.DateRange) (shared.Money, []invoice.LineItem, error) {
	// 1. Validate billing period matches contract's current period
	//    Skip validation for draft contracts (currentPeriod is zero).
	if !agg.CurrentPeriod().IsZero() && !billingPeriod.Equals(agg.CurrentPeriod()) {
		return shared.Money{}, nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("billing period mismatch: requested %s but contract current period is %s",
				billingPeriod, agg.CurrentPeriod()))
	}

	// 2. Validate PriceID is set
	if agg.PriceID() == "" {
		return shared.Money{}, nil, fmt.Errorf("contract %s has no priceID set", agg.ContractID())
	}

	// 3. Load the Price entity
	price, err := s.priceRepo.FindByID(ctx, agg.PriceID())
	if err != nil {
		return shared.Money{}, nil, fmt.Errorf("failed to load price: %w", err)
	}
	if price == nil {
		return shared.Money{}, nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("price %s not found", agg.PriceID()))
	}

	// 4. Use Price entity amount
	effectiveAmount := price.Amount()

	// 5. Calculate based on pricing model
	if price.PricingModel() == nil {
		// Pure subscription or one-time — return the flat amount with a line item
		description := "Subscription"
		if agg.GetContractType() == contract.ContractTypeOneTime {
			description = "One-time charge"
		}
		li, err := invoice.NewLineItem(
			shared.GenerateID(), description, 1,
			effectiveAmount, effectiveAmount, nil,
			invoice.WithPriceID(price.ID()),
		)
		if err != nil {
			return shared.Money{}, nil, fmt.Errorf("failed to create line item: %w", err)
		}
		return effectiveAmount, []invoice.LineItem{li}, nil
	}

	// 6. Usage-based: load product for usage metrics, calculate usage charges
	return s.calculateUsageCharge(ctx, agg, price, billingPeriod, effectiveAmount)
}

// calculateUsageCharge calculates usage-based charges using the Product entity
// for usage metric definitions and the Price entity's PricingModel for pricing.
// Returns the total charge and line items with priceID for traceability.
func (s *BillingService) calculateUsageCharge(
	ctx context.Context,
	agg *contract.ContractAggregate,
	price *pricing.Price,
	billingPeriod shared.DateRange,
	baseAmount shared.Money,
) (shared.Money, []invoice.LineItem, error) {
	// Load the product to get usage metrics
	prod, err := s.productRepo.FindByID(ctx, price.ProductID())
	if err != nil {
		return shared.Money{}, nil, fmt.Errorf("failed to load product: %w", err)
	}
	if prod == nil {
		return shared.Money{}, nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("product %s not found", price.ProductID()))
	}

	totalCharge := baseAmount // start with base price
	var lineItems []invoice.LineItem

	// Base price line item
	baseLI, err := invoice.NewLineItem(
		shared.GenerateID(), "Base price", 1,
		baseAmount, baseAmount, nil,
		invoice.WithPriceID(price.ID()),
	)
	if err != nil {
		return shared.Money{}, nil, fmt.Errorf("failed to create base price line item: %w", err)
	}
	lineItems = append(lineItems, baseLI)

	for _, metric := range prod.UsageMetrics() {
		summary, err := s.usageRepo.GetSummary(ctx, agg.ContractID(), metric.Name, billingPeriod)
		if err != nil {
			return shared.Money{}, nil, fmt.Errorf("failed to get usage summary for %s: %w", metric.Name, err)
		}

		// Subtracting the included allowance can drive usage negative; clamp to
		// zero here. This clamp is the boundary that upholds the PricingModel
		// contract (pricing.PricingModel): CalculatePrice requires non-negative
		// usage and panics otherwise, so billableUsage must never be passed
		// negative. Zero is a legitimate value (no billable usage after the
		// allowance) and yields a zero charge.
		billableUsage := summary.TotalUsage - metric.IncludedQuantity
		if billableUsage < 0 {
			billableUsage = 0
		}

		// Use the Price's pricing model. safeCalculatePrice recovers a panic
		// from an invalid persisted model and converts it to a per-contract
		// DomainError instead of crashing the caller (see the helper's doc).
		metricPrice, err := safeCalculatePrice(price.PricingModel(), billableUsage, price.ID())
		if err != nil {
			return shared.Money{}, nil, err
		}
		totalCharge, err = totalCharge.Add(metricPrice)
		if err != nil {
			return shared.Money{}, nil, fmt.Errorf("failed to sum usage charge: %w", err)
		}

		if billableUsage > 0 {
			// Line-item consistency (issue #197): the line item's amount is the
			// whole metric charge (metricPrice), which for tiered/volume pricing is
			// NOT quantity × a single per-unit rate. Setting unitPrice = metricPrice
			// while quantity = billableUsage made quantity × unitPrice ≠ amount
			// (it equalled billableUsage × metricPrice). We instead record the
			// EXACT average per-unit price = metricPrice / billableUsage, computed
			// over big.Rat so quantity × unitPrice == amount exactly (no rounding
			// error). This keeps the displayed usage quantity while making the line
			// item internally consistent for tiered/volume/graduated models.
			perUnit := metricPrice.Multiply(new(big.Rat).SetFrac64(1, billableUsage))
			usageLI, liErr := invoice.NewLineItem(
				shared.GenerateID(),
				fmt.Sprintf("Usage: %s", metric.Name),
				billableUsage,
				perUnit, metricPrice, nil,
				invoice.WithPriceID(price.ID()),
			)
			if liErr != nil {
				return shared.Money{}, nil, fmt.Errorf("failed to create usage line item: %w", liErr)
			}
			lineItems = append(lineItems, usageLI)
		}
	}

	return totalCharge, lineItems, nil
}

// safeCalculatePrice invokes model.CalculatePrice(usage) with a recover guard,
// converting a panic into a shared.DomainError that names the price.
//
// Why: pricing model invariants (mixed-currency tiers, unsorted tiers,
// wrong-currency Min/Max clamps) are enforced by the constructors, and
// CalculatePrice PANICS on a violation rather than silently mis-billing (the
// issue #238 direct-misuse policy). But a price persisted BEFORE those
// invariants existed is reconstructed as-is by pricing's FromSnapshot (replay
// safety: stored prices always load), so its first use inside the billing
// pipeline would otherwise crash the integrator's scheduler goroutine — there
// is no recover anywhere between here and GenerateInvoice/RegenerateInvoice.
// This boundary mirrors plugin.SafeInvoke (plugin/safe.go): recover → a
// structured, per-contract error, so one poisoned price fails that contract's
// billing run loudly without taking the process down.
//
// The returned error advises running the model's Validate() method (exported
// on pricing.TieredPrice / pricing.UsagePrice) and re-persisting a corrected
// price. Negative-usage panics cannot normally reach this guard — callers
// clamp billableUsage to zero first — but would be converted the same way.
func safeCalculatePrice(model pricing.PricingModel, usage int64, priceID shared.PriceID) (result shared.Money, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = shared.NewDomainError(shared.ErrCodeBusinessRule,
				fmt.Sprintf("price %s: pricing model rejected calculation: %v "+
					"(the persisted pricing model is invalid; check it with its Validate() method, "+
					"fix the price data, and re-persist)", priceID, r))
		}
	}()
	return model.CalculatePrice(usage), nil
}

// applyBalances applies available credits to the total using FIFO order.
// Expired credits are skipped. For each consumed credit, a CreditApplication
// record is created as an audit trail.
// The balanceRepo parameter is the transaction-scoped repository from RunInTx.
func (s *BillingService) applyBalances(ctx context.Context, balanceRepo balance.Repository, accountID shared.AccountID, invoiceID shared.InvoiceID, total shared.Money, currency shared.Currency) (shared.Money, error) {
	credits, err := balanceRepo.FindAvailable(ctx, accountID, currency)
	if err != nil {
		return shared.Zero(currency), fmt.Errorf("failed to find available balances: %w", err)
	}

	now := s.clock.Now()
	remaining := total
	totalApplied := shared.Zero(currency)

	// Collect all mutations so we can persist them together
	type creditMutation struct {
		entry       *balance.BalanceEntry
		application *balance.BalanceApplication
	}
	var mutations []creditMutation

	for _, entry := range credits {
		if remaining.IsZero() {
			break
		}
		if entry.IsExpired(now) {
			continue
		}
		if entry.IsFullyConsumed() {
			continue
		}

		// ConsumeAt re-checks expiry against the same `now`, holding the
		// "expired credit is unspendable" invariant at the entity even though the
		// FIFO loop already skips expired entries above (issue #196).
		consumed, err := entry.ConsumeAt(remaining, now)
		if err != nil {
			return shared.Zero(currency), err
		}

		if consumed.IsZero() {
			continue
		}

		totalApplied, err = totalApplied.Add(consumed)
		if err != nil {
			return shared.Zero(currency), err
		}

		remaining, err = remaining.Subtract(consumed)
		if err != nil {
			return shared.Zero(currency), err
		}

		mutations = append(mutations, creditMutation{
			entry: entry,
			application: &balance.BalanceApplication{
				ID:             shared.GenerateID(),
				BalanceEntryID: entry.ID(),
				InvoiceID:      invoiceID,
				Amount:         consumed,
				AppliedAt:      now,
			},
		})
	}

	// Persist all mutations: credit entries and application records.
	// All saves participate in the caller's transaction boundary.
	for _, m := range mutations {
		if err := balanceRepo.Save(ctx, m.entry); err != nil {
			return shared.Zero(currency), fmt.Errorf("failed to save balance entry: %w", err)
		}
		if err := balanceRepo.SaveApplication(ctx, m.application); err != nil {
			return shared.Zero(currency), fmt.Errorf("failed to save balance application: %w", err)
		}
	}

	return totalApplied, nil
}

// RestoreBalancesForVoidedInvoice returns the credit a voided invoice consumed
// back to the balance ledger and records BalanceRefund audit rows (issue #184).
//
// The invoice is loaded through the transaction-scoped invoice repository and
// MUST be in voided status: restoring the applications of a live (draft/
// finalized/paid) invoice would fabricate spendable balance — the invoice still
// legitimately holds that credit — so any non-voided status is rejected with a
// business_rule DomainError, and a missing invoice is an error (not a silent
// no-op). The in-tx load means a void written earlier in the same transaction
// (e.g. by CreditNoteService.ReissueInvoice) is visible to the guard.
//
// Call it inside the SAME transaction that voids the invoice so the void and the
// credit restoration commit or roll back together. It joins an active
// transaction stamped on ctx (via tx.Run) and uses the transaction-scoped
// repositories; when no balance repository is wired the restoration is a no-op
// (the voided-status guard still runs). When a balance repository IS wired but
// the TxManager's Repos omits Balances, the call fails with a configuration
// DomainError instead of silently skipping the restoration (issue #241 — same
// misbilling class as silently skipping credit application in the billing
// pipeline). The operation is idempotent — a double
// void / retry restores each application at most once (see restoreBalances).
//
// This is the reversal used by void paths that do NOT go through
// RegenerateInvoice's pipeline — notably CreditNoteService.ReissueInvoice, which
// voids the original and then generates a replacement via GenerateInvoice.
// (RegenerateInvoice's pipeline verifies voided status itself and calls the
// internal restoreBalances directly, skipping this redundant load.)
func (s *BillingService) RestoreBalancesForVoidedInvoice(ctx context.Context, invoiceID shared.InvoiceID) error {
	return tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		invoiceRepo := repos.Invoices
		if invoiceRepo == nil {
			invoiceRepo = s.invoiceRepo
		}
		inv, err := invoiceRepo.FindByID(txCtx, invoiceID)
		if err != nil {
			return fmt.Errorf("failed to load invoice for balance restoration: %w", err)
		}
		if inv == nil {
			return shared.NewDomainError(shared.ErrCodeNotFound,
				fmt.Sprintf("invoice %s not found", invoiceID))
		}
		if inv.Status() != invoice.InvoiceStatusVoided {
			return shared.NewDomainError(shared.ErrCodeBusinessRule,
				fmt.Sprintf("cannot restore balances: invoice %s is %s, not voided", invoiceID, inv.Status()))
		}
		// Configuration guard (issue #241): when a balance repository is wired
		// (WithBalanceRepo) but the TxManager's Repos omits Balances, fail loudly —
		// a reissue that silently skips restoring the voided invoice's consumed
		// credit is the same misbilling class as silently skipping credit
		// application in the billing pipeline. When no balance repo was ever
		// wired, there is no credit ledger to restore and the no-op below is the
		// documented behavior.
		if repos.Balances == nil {
			if s.balanceRepo != nil {
				return shared.NewDomainError(shared.ErrCodeValidation,
					"billing misconfiguration: a balance repository is wired (WithBalanceRepo) but the TxManager's Repos does not provide Balances; balance restoration would be silently skipped — include the balance repository in the transaction-scoped Repos")
			}
			return nil
		}
		_, err = s.restoreBalances(txCtx, repos.Balances, invoiceID)
		return err
	})
}

// restoreBalances returns credit consumed by a (now voided) invoice to the
// balance ledger and records BalanceRefund audit rows. It is the inverse of
// applyBalances: for each BalanceApplication recorded against invoiceID it
// reloads the source entry, calls BalanceEntry.Restore, saves it, and writes a
// BalanceRefund linking the reversal to the application.
//
// Idempotency: applications already reversed by a prior BalanceRefund for the
// invoice (matched by ApplicationID) are skipped, so a double void / transaction
// retry restores each application at most once. Because the whole reversal runs
// inside the caller's transaction, a mid-loop failure rolls the entire reversal
// back and leaves no partial refunds.
//
// Expired entries: a source entry whose expiry has passed is still restored (see
// BalanceEntry.Restore) and left for batch.BalanceExpirationProcessor to forfeit
// through the normal expiration path.
//
// balanceRepo is the transaction-scoped repository from the caller's tx.Run.
// Returns the total amount restored (zero when nothing was applicable).
func (s *BillingService) restoreBalances(ctx context.Context, balanceRepo balance.Repository, invoiceID shared.InvoiceID) (shared.Money, error) {
	apps, err := balanceRepo.FindApplicationsByInvoice(ctx, invoiceID)
	if err != nil {
		return shared.Money{}, fmt.Errorf("failed to load balance applications: %w", err)
	}
	if len(apps) == 0 {
		return shared.Money{}, nil
	}

	// Idempotency guard: skip applications already reversed by an existing refund.
	existingRefunds, err := balanceRepo.FindRefundsByInvoice(ctx, invoiceID)
	if err != nil {
		return shared.Money{}, fmt.Errorf("failed to load balance refunds: %w", err)
	}
	alreadyRefunded := make(map[string]bool, len(existingRefunds))
	for _, ref := range existingRefunds {
		alreadyRefunded[ref.ApplicationID] = true
	}

	now := s.clock.Now()
	totalRestored := shared.Zero(apps[0].Amount.Currency())
	for _, app := range apps {
		if alreadyRefunded[app.ID] {
			continue
		}

		entry, findErr := balanceRepo.FindByID(ctx, app.BalanceEntryID)
		if findErr != nil {
			return shared.Money{}, fmt.Errorf("failed to load balance entry %s: %w", app.BalanceEntryID, findErr)
		}
		if restoreErr := entry.Restore(app.Amount); restoreErr != nil {
			return shared.Money{}, fmt.Errorf("failed to restore balance entry %s: %w", app.BalanceEntryID, restoreErr)
		}
		if saveErr := balanceRepo.Save(ctx, entry); saveErr != nil {
			return shared.Money{}, fmt.Errorf("failed to save restored balance entry: %w", saveErr)
		}

		refund := &balance.BalanceRefund{
			ID:             shared.GenerateID(),
			BalanceEntryID: app.BalanceEntryID,
			AccountID:      entry.AccountID(),
			Amount:         app.Amount,
			RefundedAt:     now,
			InvoiceID:      invoiceID,
			ApplicationID:  app.ID,
		}
		if saveErr := balanceRepo.SaveRefund(ctx, refund); saveErr != nil {
			return shared.Money{}, fmt.Errorf("failed to save balance refund: %w", saveErr)
		}

		totalRestored, err = totalRestored.Add(app.Amount)
		if err != nil {
			return shared.Money{}, fmt.Errorf("failed to sum restored credits: %w", err)
		}
	}

	return totalRestored, nil
}

// rejectIfActivePeriodInvoice returns a conflict DomainError when a non-voided,
// non-proration invoice already exists for the given contract and billing
// period. It reads through the transaction-scoped repo (when active) so it
// observes writes made earlier in the same transaction. RegenerateInvoice uses
// it as its in-tx duplicate re-check (issue #149): the voided original is
// excluded, so void-and-recreate for the same period still succeeds. Proration
// invoices are likewise excluded (issue #232): they intentionally coexist with
// the period's regular invoice. The predicate is the domain's
// Invoice.ParticipatesInPeriodUniqueness — the same one the
// invoice.Repository.Save constraint enforces — so the layers cannot drift.
func (s *BillingService) rejectIfActivePeriodInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) error {
	existing, err := s.invoiceRepoFor(ctx).FindByContractAndPeriod(ctx, contractID, billingPeriod)
	if err != nil {
		return fmt.Errorf("failed to check existing invoices for period: %w", err)
	}
	for _, inv := range existing {
		if inv.ParticipatesInPeriodUniqueness() {
			return shared.NewDomainError(shared.ErrCodeConflict,
				"invoice already exists for this billing period")
		}
	}
	return nil
}

// checkDuplicateInvoice prevents duplicate invoice generation based on contract state.
// Reads go through the transaction-scoped invoice repo (when a transaction is
// active) so a void written earlier in the same transaction is visible — see
// invoiceRepoFor.
//
// Proration invoices are exempt from the duplicate guards (issue #232): they
// intentionally coexist with the period's regular invoice, matching the
// per-period uniqueness contract in invoice.Repository.Save (its partial unique
// index ranges over non-voided, non-proration invoices only). Without the
// exemption, a mid-period upgrade proration would permanently block the
// period's regular invoice. The one-time branch gets the same exemption:
// GenerateProrationInvoice guards contract STATUS (active) but not contract
// TYPE, so a proration adjustment can exist for an active one-time contract,
// and "only one invoice ever" means one regular invoice.
func (s *BillingService) checkDuplicateInvoice(ctx context.Context, agg *contract.ContractAggregate, billingPeriod shared.DateRange) error {
	invoiceRepo := s.invoiceRepoFor(ctx)
	switch {
	case agg.Status() == contract.ContractStatusDraft:
		// Draft contracts: only one draft invoice allowed. No proration
		// exemption needed here: GenerateProrationInvoice requires an ACTIVE
		// contract and there is no active→draft transition, so a draft
		// contract cannot have proration invoices.
		existing, err := invoiceRepo.FindByContractAndStatus(ctx, agg.ContractID(), invoice.InvoiceStatusDraft)
		if err != nil {
			return fmt.Errorf("failed to check existing draft invoices: %w", err)
		}
		if len(existing) > 0 {
			return shared.NewDomainError(shared.ErrCodeConflict,
				"draft invoice already exists for this contract")
		}

	case agg.GetContractType() == contract.ContractTypeOneTime:
		// One-time contracts: only one regular invoice ever (voided and
		// proration invoices are exempt). This branch deliberately does NOT use
		// Invoice.ParticipatesInPeriodUniqueness: the "one invoice ever" rule is
		// per-CONTRACT, not per-period, and FindByContractID can return regular
		// invoices without a billing period — the full predicate's non-zero-period
		// dimension would wrongly exempt those and weaken this guard.
		existing, err := invoiceRepo.FindByContractID(ctx, agg.ContractID())
		if err != nil {
			return fmt.Errorf("failed to check existing invoices: %w", err)
		}
		for _, inv := range existing {
			if inv.Status() == invoice.InvoiceStatusVoided || inv.IsProration() {
				continue
			}
			return shared.NewDomainError(shared.ErrCodeConflict,
				"invoice already exists for one-time contract")
		}

	default:
		// Subscription/usage-based: one invoice per billing period that
		// participates in the per-period uniqueness constraint (voided and
		// proration invoices are exempt). Predicate shared with the repository
		// constraint via Invoice.ParticipatesInPeriodUniqueness.
		existing, err := invoiceRepo.FindByContractAndPeriod(ctx, agg.ContractID(), billingPeriod)
		if err != nil {
			return fmt.Errorf("failed to check existing invoices for period: %w", err)
		}
		for _, inv := range existing {
			if inv.ParticipatesInPeriodUniqueness() {
				return shared.NewDomainError(shared.ErrCodeConflict,
					"invoice already exists for this billing period")
			}
		}
	}

	return nil
}

// MoneyFromInt64 is a helper that creates a Money value from an int64 amount.
func MoneyFromInt64(amount int64, currency shared.Currency) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), currency)
}
