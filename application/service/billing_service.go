// Package service provides application-level services that orchestrate
// domain logic, plugin hooks, and external integrations.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/product"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
	"github.com/contract-to-cash/core/plugin"
)

// BillingConfig holds billing service configuration.
type BillingConfig struct {
	GracePeriod      time.Duration
	DaysUntilDue     int
	CollectionMethod string
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

// WithCreditRepo sets the credit repository used for credit application.
func WithCreditRepo(repo credit.Repository) BillingServiceOption {
	return func(s *BillingService) {
		s.creditRepo = repo
	}
}

// BillingService orchestrates the invoice generation flow.
type BillingService struct {
	contractRepo contract.Repository
	invoiceRepo  invoice.Repository
	usageRepo    usage.Repository
	creditRepo   credit.Repository
	creditConfig credit.CreditConfig
	priceRepo    pricing.PriceRepository
	productRepo  product.Repository
	registry     *plugin.Registry
	config       BillingConfig
	clock        shared.Clock
	logger       *slog.Logger
}

// NewBillingService creates a new BillingService.
// Required dependencies are positional arguments; optional dependencies
// (logger, credit repository) are provided via BillingServiceOption.
func NewBillingService(
	contractRepo contract.Repository,
	invoiceRepo invoice.Repository,
	usageRepo usage.Repository,
	creditConfig credit.CreditConfig,
	priceRepo pricing.PriceRepository,
	productRepo product.Repository,
	registry *plugin.Registry,
	config BillingConfig,
	clock shared.Clock,
	opts ...BillingServiceOption,
) *BillingService {
	s := &BillingService{
		contractRepo: contractRepo,
		invoiceRepo:  invoiceRepo,
		usageRepo:    usageRepo,
		creditConfig: creditConfig,
		priceRepo:    priceRepo,
		productRepo:  productRepo,
		registry:     registry,
		config:       config,
		clock:        clock,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
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

// GenerateInvoice generates an invoice for a contract and billing period.
// It follows a 14-step calculation flow with plugin hooks.
func (s *BillingService) GenerateInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) (*invoice.Invoice, error) {
	// Step 1: Load contract aggregate
	agg, err := s.contractRepo.FindByID(ctx, contractID)
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

	// Create CalculationContext with zero subtotal initially
	currency := agg.Price().Currency()
	calcCtx := plugin.NewCalculationContext(ctx, agg, shared.Zero(currency))

	// Step 2: BeforeCalculation (InvoiceLifecycleHooks)
	for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
		if err = hook.BeforeCalculation(calcCtx); err != nil {
			return nil, fmt.Errorf("BeforeCalculation hook error: %w", err)
		}
	}

	// Step 3: Calculate subtotal based on contract type
	subtotal, lineItems, err := s.calculateSubtotal(ctx, agg, billingPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate subtotal: %w", err)
	}
	calcCtx.SetSubtotal(subtotal)

	// Step 4: Execute all DiscountHooks
	totalDiscount := shared.Zero(currency)
	for _, hook := range s.registry.GetDiscountHooks() {
		var discount shared.Money
		discount, err = hook.CalculateDiscount(calcCtx)
		if err != nil {
			return nil, fmt.Errorf("DiscountHook error: %w", err)
		}
		totalDiscount, err = totalDiscount.Add(discount)
		if err != nil {
			return nil, fmt.Errorf("failed to sum discounts: %w", err)
		}
	}

	// Step 5: Cap discount to subtotal (discount must not exceed subtotal)
	if totalDiscount.GreaterThan(subtotal) {
		totalDiscount = subtotal
	}

	// Step 6: Calculate subtotal after discount
	afterDiscount, err := subtotal.Subtract(totalDiscount)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate after-discount: %w", err)
	}
	calcCtx.SetSubtotalAfterDiscount(afterDiscount)

	// Step 7: Execute all TaxHooks
	totalTax := shared.Zero(currency)
	for _, hook := range s.registry.GetTaxHooks() {
		var tax shared.Money
		tax, err = hook.CalculateTax(calcCtx)
		if err != nil {
			return nil, fmt.Errorf("TaxHook error: %w", err)
		}
		totalTax, err = totalTax.Add(tax)
		if err != nil {
			return nil, fmt.Errorf("failed to sum taxes: %w", err)
		}
	}

	// Step 8: total = afterDiscount + totalTax
	total, err := afterDiscount.Add(totalTax)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate total: %w", err)
	}

	// Step 9: Generate invoice ID upfront (needed for CreditApplication records)
	invoiceID := shared.NewInvoiceID()

	// Step 10: Apply credits (FIFO, skip expired) - only if creditRepo is not nil
	appliedCredit := shared.Zero(currency)
	if s.creditRepo != nil {
		appliedCredit, err = s.applyCredits(ctx, agg.AccountID(), invoiceID, total, currency)
		if err != nil {
			return nil, fmt.Errorf("failed to apply credits: %w", err)
		}
	}

	// Step 11: Calculate amount due
	amountDue, err := total.Subtract(appliedCredit)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate amount due: %w", err)
	}

	// Step 12: Create invoice
	now := s.clock.Now()
	dueDate := now.AddDate(0, 0, s.config.DaysUntilDue)

	opts := []invoice.InvoiceOption{
		invoice.WithStatus(invoice.InvoiceStatusDraft),
		invoice.WithBillingPeriod(billingPeriod),
		invoice.WithDueDate(dueDate),
		invoice.WithAppliedCredit(appliedCredit),
		invoice.WithAmountDue(amountDue),
		invoice.WithIssueDate(now),
	}
	if len(lineItems) > 0 {
		opts = append(opts, invoice.WithLineItems(lineItems))
	}
	if agg.PaymentMethodID() != nil {
		opts = append(opts, invoice.WithPaymentMethodID(agg.PaymentMethodID()))
	}

	inv := invoice.NewInvoice(
		invoiceID,
		agg.AccountID(),
		contractID,
		subtotal,
		totalDiscount,
		totalTax,
		opts...,
	)
	calcCtx.SetInvoice(inv)

	// Step 13: AfterCalculation (InvoiceLifecycleHooks)
	for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
		if err := hook.AfterCalculation(calcCtx, inv); err != nil {
			return nil, fmt.Errorf("AfterCalculation hook error: %w", err)
		}
	}

	// Step 14: Save invoice
	if err := s.invoiceRepo.Save(ctx, inv); err != nil {
		return nil, fmt.Errorf("failed to save invoice: %w", err)
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
		li := invoice.NewLineItem(
			shared.GenerateID(), description, 1,
			effectiveAmount, effectiveAmount, nil,
			invoice.WithPriceID(price.ID()),
		)
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
	lineItems = append(lineItems, invoice.NewLineItem(
		shared.GenerateID(), "Base price", 1,
		baseAmount, baseAmount, nil,
		invoice.WithPriceID(price.ID()),
	))

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
			lineItems = append(lineItems, invoice.NewLineItem(
				shared.GenerateID(),
				fmt.Sprintf("Usage: %s", metric.Name),
				billableUsage,
				metricPrice, metricPrice, nil,
				invoice.WithPriceID(price.ID()),
			))
		}
	}

	return totalCharge, lineItems, nil
}

// applyCredits applies available credits to the total using FIFO order.
// Expired credits are skipped. For each consumed credit, a CreditApplication
// record is created as an audit trail.
//
// NOTE: The caller is responsible for providing transactional guarantees.
// If a persistence error occurs mid-loop, some credits may be saved while
// others are not. In production, wrap this in a database transaction.
func (s *BillingService) applyCredits(ctx context.Context, accountID shared.AccountID, invoiceID shared.InvoiceID, total shared.Money, currency shared.Currency) (shared.Money, error) {
	credits, err := s.creditRepo.FindAvailable(ctx, accountID, currency)
	if err != nil {
		return shared.Zero(currency), fmt.Errorf("failed to find available credits: %w", err)
	}

	now := s.clock.Now()
	remaining := total
	totalApplied := shared.Zero(currency)

	// Collect all mutations so we can persist them together
	type creditMutation struct {
		entry       *credit.CreditEntry
		application *credit.CreditApplication
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
			application: &credit.CreditApplication{
				ID:            shared.GenerateID(),
				CreditEntryID: entry.ID(),
				InvoiceID:     invoiceID,
				Amount:        consumed,
				AppliedAt:     now,
			},
		})
	}

	// Persist all mutations: credit entries and application records
	for _, m := range mutations {
		if err := s.creditRepo.Save(ctx, m.entry); err != nil {
			return shared.Zero(currency), fmt.Errorf("failed to save credit entry: %w", err)
		}
		if err := s.creditRepo.SaveApplication(ctx, m.application); err != nil {
			return shared.Zero(currency), fmt.Errorf("failed to save credit application: %w", err)
		}
	}

	return totalApplied, nil
}

// checkDuplicateInvoice prevents duplicate invoice generation based on contract state.
func (s *BillingService) checkDuplicateInvoice(ctx context.Context, agg *contract.ContractAggregate, billingPeriod shared.DateRange) error {
	switch {
	case agg.Status() == contract.ContractStatusDraft:
		// Draft contracts: only one draft invoice allowed
		existing, err := s.invoiceRepo.FindByContractAndStatus(ctx, agg.ContractID(), invoice.InvoiceStatusDraft)
		if err != nil {
			return fmt.Errorf("failed to check existing draft invoices: %w", err)
		}
		if len(existing) > 0 {
			return shared.NewDomainError(shared.ErrCodeConflict,
				"draft invoice already exists for this contract")
		}

	case agg.GetContractType() == contract.ContractTypeOneTime:
		// One-time contracts: only one invoice ever
		existing, err := s.invoiceRepo.FindByContractID(ctx, agg.ContractID())
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
		existing, err := s.invoiceRepo.FindByContractAndPeriod(ctx, agg.ContractID(), billingPeriod)
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
