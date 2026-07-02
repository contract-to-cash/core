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
	GracePeriod      time.Duration
	DaysUntilDue     int
	CollectionMethod CollectionMethod
	// AllowPartialPayment, when true, marks generated invoices as accepting
	// partial payments (Invoice.allowPartialPay). Default false: a payment must
	// settle the full amount due in one go (design-decisions 3.1).
	AllowPartialPayment bool
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
// If not provided, a NoopTxManager is used (no transaction wrapping).
func WithBillingTxManager(tm tx.TxManager) BillingServiceOption {
	return func(s *BillingService) {
		s.txManager = tm
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
		s.txManager = tx.NewNoopTxManager(tx.Repos{
			Contracts: contractRepo,
			Invoices:  invoiceRepo,
			Balances:  s.balanceRepo,
		})
	}
	return s
}

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
// to maintain the audit trail.
func (s *BillingService) RegenerateInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) (*invoice.Invoice, error) {
	// Load contract aggregate (tx-scoped when inside a transaction, so reads see
	// writes made earlier in the same transaction — symmetry with GenerateInvoice)
	agg, err := s.contractRepoFor(ctx).FindByID(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("failed to load contract: %w", err)
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

	// Require a voided invoice for the same period — this distinguishes regeneration
	// from net-new invoice creation and prevents misuse as a bypass.
	// Use the last voided invoice found so that the revision chain links to the
	// most recently voided entry (important when void-and-recreate runs more than once).
	var voidedInv *invoice.Invoice
	for _, inv := range existing {
		if inv.Status() == invoice.InvoiceStatusVoided {
			voidedInv = inv
		}
	}
	if voidedInv == nil {
		return nil, shared.NewDomainError(shared.ErrCodeBusinessRule,
			"cannot regenerate invoice: no voided invoice found for this period")
	}

	// Inline duplicate check: reject if a non-voided invoice already exists for
	// this period (same logic as checkDuplicateInvoice for subscription/usage-based,
	// but using the already-fetched existing slice).
	for _, inv := range existing {
		if inv.Status() != invoice.InvoiceStatusVoided {
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
		"invoice_type": "regeneration",
	}))

	return s.executeBillingPipeline(ctx, pipelineInput{
		agg:        agg,
		contractID: contractID,
		subtotal:   subtotal,
		lineItems:  lineItems,
		period:     billingPeriod,
		extraOpts:  extraOpts,
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
		"invoice_type":   "proration",
		"effective_date": proration.EffectiveDate.Format(time.RFC3339),
		"credit_amount":  proration.CreditAmount.Amount().RatString(),
		"charge_amount":  proration.ChargeAmount.Amount().RatString(),
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
	subtotal := input.subtotal
	currency := subtotal.Currency()
	calcCtx := plugin.NewCalculationContext(ctx, agg, shared.Zero(currency))

	// Resolve ProductID from PriceID for plugin context (e.g. coupon applicability)
	if priceID := agg.PriceID(); priceID != "" {
		priceEntity, priceErr := s.priceRepo.FindByID(ctx, priceID)
		if priceErr != nil {
			return nil, fmt.Errorf("failed to load price for product resolution: %w", priceErr)
		}
		calcCtx.SetProductID(priceEntity.ProductID())
	}

	// BeforeCalculation (InvoiceLifecycleHooks)
	for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
		if err := hook.BeforeCalculation(calcCtx); err != nil {
			return nil, fmt.Errorf("BeforeCalculation hook error: %w", err)
		}
	}

	calcCtx.SetSubtotal(subtotal)

	// Execute all DiscountHooks
	totalDiscount := shared.Zero(currency)
	for _, hook := range s.registry.GetDiscountHooks() {
		discount, err := hook.CalculateDiscount(calcCtx)
		if err != nil {
			return nil, fmt.Errorf("DiscountHook error: %w", err)
		}
		totalDiscount, err = totalDiscount.Add(discount)
		if err != nil {
			return nil, fmt.Errorf("failed to sum discounts: %w", err)
		}
	}

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
		tax, err := hook.CalculateTax(calcCtx)
		if err != nil {
			return nil, fmt.Errorf("TaxHook error: %w", err)
		}
		totalTax, err = totalTax.Add(tax)
		if err != nil {
			return nil, fmt.Errorf("failed to sum taxes: %w", err)
		}
	}

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

		// Create invoice
		now := s.clock.Now()
		dueDate := now.AddDate(0, 0, s.config.DaysUntilDue)

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

		// AfterCalculation (InvoiceLifecycleHooks)
		for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
			if hookErr := hook.AfterCalculation(calcCtx, inv); hookErr != nil {
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
func (s *BillingService) FinalizeInvoice(ctx context.Context, invoiceID shared.InvoiceID) (*invoice.Invoice, error) {
	inv, err := s.invoiceRepoFor(ctx).FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load invoice: %w", err)
	}
	if inv == nil {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("invoice %s not found", invoiceID))
	}

	// Finalize enforces the draft→finalized transition; any other status
	// is rejected with an invalid_state_transition domain error.
	if err := inv.Finalize(); err != nil {
		return nil, err
	}

	// Persist within a transaction. tx.Run joins an outer transaction if one
	// is already active.
	err = tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		return repos.Invoices.Save(txCtx, inv)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to save finalized invoice: %w", err)
	}

	// Post-commit metrics hooks — non-fatal.
	pluginCtx := plugin.NewContext(ctx)
	for _, hook := range s.registry.GetOnInvoiceIssuedHooks() {
		if hookErr := hook.OnInvoiceIssued(pluginCtx, inv); hookErr != nil {
			s.logger.Warn("OnInvoiceIssued hook failed",
				"hook", hook.Name(),
				"invoiceID", invoiceID,
				"error", hookErr,
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
		return shared.Money{}, nil, fmt.Errorf(
			"billing period mismatch: requested %s but contract current period is %s",
			billingPeriod, agg.CurrentPeriod(),
		)
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

		billableUsage := summary.TotalUsage - metric.IncludedQuantity
		if billableUsage < 0 {
			billableUsage = 0
		}

		// Use the Price's pricing model
		metricPrice := price.PricingModel().CalculatePrice(billableUsage)
		totalCharge, err = totalCharge.Add(metricPrice)
		if err != nil {
			return shared.Money{}, nil, fmt.Errorf("failed to sum usage charge: %w", err)
		}

		if billableUsage > 0 {
			usageLI, liErr := invoice.NewLineItem(
				shared.GenerateID(),
				fmt.Sprintf("Usage: %s", metric.Name),
				billableUsage,
				metricPrice, metricPrice, nil,
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

		consumed, err := entry.Consume(remaining)
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

// checkDuplicateInvoice prevents duplicate invoice generation based on contract state.
// Reads go through the transaction-scoped invoice repo (when a transaction is
// active) so a void written earlier in the same transaction is visible — see
// invoiceRepoFor.
func (s *BillingService) checkDuplicateInvoice(ctx context.Context, agg *contract.ContractAggregate, billingPeriod shared.DateRange) error {
	invoiceRepo := s.invoiceRepoFor(ctx)
	switch {
	case agg.Status() == contract.ContractStatusDraft:
		// Draft contracts: only one draft invoice allowed
		existing, err := invoiceRepo.FindByContractAndStatus(ctx, agg.ContractID(), invoice.InvoiceStatusDraft)
		if err != nil {
			return fmt.Errorf("failed to check existing draft invoices: %w", err)
		}
		if len(existing) > 0 {
			return shared.NewDomainError(shared.ErrCodeConflict,
				"draft invoice already exists for this contract")
		}

	case agg.GetContractType() == contract.ContractTypeOneTime:
		// One-time contracts: only one invoice ever
		existing, err := invoiceRepo.FindByContractID(ctx, agg.ContractID())
		if err != nil {
			return fmt.Errorf("failed to check existing invoices: %w", err)
		}
		for _, inv := range existing {
			if inv.Status() != invoice.InvoiceStatusVoided {
				return shared.NewDomainError(shared.ErrCodeConflict,
					"invoice already exists for one-time contract")
			}
		}

	default:
		// Subscription/usage-based: one invoice per billing period
		existing, err := invoiceRepo.FindByContractAndPeriod(ctx, agg.ContractID(), billingPeriod)
		if err != nil {
			return fmt.Errorf("failed to check existing invoices for period: %w", err)
		}
		for _, inv := range existing {
			if inv.Status() != invoice.InvoiceStatusVoided {
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
