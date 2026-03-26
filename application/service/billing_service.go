// Package service provides application-level services that orchestrate
// domain logic, plugin hooks, and external integrations.
package service

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/domain/usage"
	"github.com/contract-to-cash/core/plugin"
)

// PlanRepository provides access to pricing plans.
type PlanRepository interface {
	FindByID(ctx context.Context, id shared.PlanID) (*pricing.Plan, error)
}

// BillingConfig holds billing service configuration.
type BillingConfig struct {
	GracePeriod      time.Duration
	DaysUntilDue     int
	CollectionMethod string
}

// BillingService orchestrates the invoice generation flow.
type BillingService struct {
	contractRepo contract.Repository
	invoiceRepo  invoice.Repository
	usageRepo    usage.Repository
	creditRepo   credit.Repository
	creditConfig credit.CreditConfig
	planRepo     PlanRepository
	registry     *plugin.Registry
	config       BillingConfig
	clock        shared.Clock
}

// NewBillingService creates a new BillingService.
func NewBillingService(
	contractRepo contract.Repository,
	invoiceRepo invoice.Repository,
	usageRepo usage.Repository,
	creditRepo credit.Repository,
	creditConfig credit.CreditConfig,
	planRepo PlanRepository,
	registry *plugin.Registry,
	config BillingConfig,
	clock shared.Clock,
) *BillingService {
	return &BillingService{
		contractRepo: contractRepo,
		invoiceRepo:  invoiceRepo,
		usageRepo:    usageRepo,
		creditRepo:   creditRepo,
		creditConfig: creditConfig,
		planRepo:     planRepo,
		registry:     registry,
		config:       config,
		clock:        clock,
	}
}

// GenerateInvoice generates an invoice for a contract and billing period.
// It follows a 14-step calculation flow with plugin hooks.
func (s *BillingService) GenerateInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) (*invoice.Invoice, error) {
	// Step 1: Load contract aggregate
	agg, err := s.contractRepo.FindByID(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("failed to load contract: %w", err)
	}

	// Create CalculationContext with zero subtotal initially
	currency := agg.Price().Currency()
	calcCtx := plugin.NewCalculationContext(ctx, agg, shared.Zero(currency))

	// Step 2: BeforeCalculation (InvoiceLifecycleHooks)
	for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
		if err := hook.BeforeCalculation(calcCtx); err != nil {
			return nil, fmt.Errorf("BeforeCalculation hook error: %w", err)
		}
	}

	// Step 3: Calculate subtotal based on contract type
	subtotal, err := s.calculateSubtotal(ctx, agg, billingPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate subtotal: %w", err)
	}
	calcCtx.SetSubtotal(subtotal)

	// Step 4: Execute all DiscountHooks
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
		tax, err := hook.CalculateTax(calcCtx)
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

	inv := invoice.NewInvoice(
		invoiceID,
		agg.AccountID(),
		contractID,
		subtotal,
		totalDiscount,
		totalTax,
		invoice.WithStatus(invoice.InvoiceStatusDraft),
		invoice.WithBillingPeriod(billingPeriod),
		invoice.WithDueDate(dueDate),
		invoice.WithAppliedCredit(appliedCredit),
		invoice.WithAmountDue(amountDue),
		invoice.WithIssueDate(now),
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

// calculateSubtotal calculates the subtotal based on contract type.
func (s *BillingService) calculateSubtotal(ctx context.Context, agg *contract.ContractAggregate, billingPeriod shared.DateRange) (shared.Money, error) {
	switch agg.GetContractType() {
	case contract.ContractTypeSubscription:
		return agg.Price(), nil

	case contract.ContractTypeOneTime:
		return agg.Price(), nil

	case contract.ContractTypeUsageBased:
		return s.calculateUsageCharge(ctx, agg, billingPeriod)

	default:
		return shared.Money{}, fmt.Errorf("unsupported contract type: %s", agg.GetContractType())
	}
}

// calculateUsageCharge calculates usage-based charges by iterating over
// plan usage metrics, querying usage summaries, applying included quantities,
// and computing prices via the pricing model.
func (s *BillingService) calculateUsageCharge(ctx context.Context, agg *contract.ContractAggregate, billingPeriod shared.DateRange) (shared.Money, error) {
	currency := agg.Price().Currency()

	// Load the plan to get usage metrics
	plan, err := s.planRepo.FindByID(ctx, agg.PlanID())
	if err != nil {
		return shared.Money{}, fmt.Errorf("failed to load plan: %w", err)
	}

	totalCharge := shared.Zero(currency)

	for _, metric := range plan.UsageMetrics() {
		summary, err := s.usageRepo.GetSummary(ctx, agg.ContractID(), metric.Name, billingPeriod)
		if err != nil {
			return shared.Money{}, fmt.Errorf("failed to get usage summary for metric %s: %w", metric.Name, err)
		}

		// Subtract included quantity
		billableUsage := summary.TotalUsage - metric.IncludedQuantity
		if billableUsage < 0 {
			billableUsage = 0
		}

		// Calculate price via the metric's pricing model
		metricPrice := metric.PricingModel.CalculatePrice(billableUsage)

		totalCharge, err = totalCharge.Add(metricPrice)
		if err != nil {
			return shared.Money{}, fmt.Errorf("failed to sum usage charge: %w", err)
		}
	}

	// Add the base price
	basePrice := agg.Price()
	totalCharge, err = totalCharge.Add(basePrice)
	if err != nil {
		return shared.Money{}, fmt.Errorf("failed to add base price: %w", err)
	}

	return totalCharge, nil
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

// MoneyFromInt64 is a helper that creates a Money value from an int64 amount.
func MoneyFromInt64(amount int64, currency shared.Currency) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), currency)
}
