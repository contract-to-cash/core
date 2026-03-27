package contract

import (
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
)

// ContractStatus represents the lifecycle status of a contract.
type ContractStatus string

const (
	ContractStatusDraft     ContractStatus = "draft"
	ContractStatusTrialing  ContractStatus = "trialing"
	ContractStatusActive    ContractStatus = "active"
	ContractStatusPastDue   ContractStatus = "past_due"
	ContractStatusSuspended ContractStatus = "suspended"
	ContractStatusCancelled ContractStatus = "cancelled"
	ContractStatusExpired   ContractStatus = "expired"
)

// ContractType represents the type of a contract.
type ContractType string

const (
	ContractTypeOneTime      ContractType = "one_time"
	ContractTypeSubscription ContractType = "subscription"
	ContractTypeUsageBased   ContractType = "usage_based"
)

// BillingCycle is an alias for pricing.BillingCycle.
// Deprecated: Use pricing.BillingCycle directly in new code.
type BillingCycle = pricing.BillingCycle

// Deprecated: Use pricing.BillingCycleDaily etc. directly in new code.
const (
	BillingCycleDaily   = pricing.BillingCycleDaily
	BillingCycleWeekly  = pricing.BillingCycleWeekly
	BillingCycleMonthly = pricing.BillingCycleMonthly
	BillingCycleYearly  = pricing.BillingCycleYearly
)

// Contract represents a contract entity.
type Contract struct {
	id               shared.ContractID
	accountID        shared.AccountID
	planID           shared.PlanID
	status           ContractStatus
	contractType     ContractType
	billingCycle     BillingCycle
	currentPeriod    shared.DateRange
	trialConfig      *TrialConfiguration
	suspensionConfig *SuspensionConfiguration
	paymentMethodID  *string
	price            shared.Money
	basePrice        shared.Money
	metadata         map[string]string
	createdAt        time.Time
	updatedAt        time.Time
	version          int
}

// ID returns the contract ID.
func (c *Contract) ID() shared.ContractID { return c.id }

// AccountID returns the account ID.
func (c *Contract) AccountID() shared.AccountID { return c.accountID }

// PlanID returns the plan ID.
func (c *Contract) PlanID() shared.PlanID { return c.planID }

// Status returns the contract status.
func (c *Contract) Status() ContractStatus { return c.status }

// ContractType returns the contract type.
func (c *Contract) ContractType() ContractType { return c.contractType }

// BillingCycle returns the billing cycle.
func (c *Contract) BillingCycle() BillingCycle { return c.billingCycle }

// CurrentPeriod returns the current billing period.
func (c *Contract) CurrentPeriod() shared.DateRange { return c.currentPeriod }

// TrialConfig returns the trial configuration, if any.
func (c *Contract) TrialConfig() *TrialConfiguration { return c.trialConfig }

// SuspensionConfig returns the suspension configuration, if any.
func (c *Contract) SuspensionConfig() *SuspensionConfiguration { return c.suspensionConfig }

// Price returns the current price.
func (c *Contract) Price() shared.Money { return c.price }

// BasePrice returns the base price.
func (c *Contract) BasePrice() shared.Money { return c.basePrice }

// PaymentMethodID returns the contract-level payment method ID.
func (c *Contract) PaymentMethodID() *string { return c.paymentMethodID }

// Metadata returns the contract metadata.
func (c *Contract) Metadata() map[string]string {
	if c.metadata == nil {
		return nil
	}
	cp := make(map[string]string, len(c.metadata))
	for k, v := range c.metadata {
		cp[k] = v
	}
	return cp
}

// CreatedAt returns the creation timestamp.
func (c *Contract) CreatedAt() time.Time { return c.createdAt }

// UpdatedAt returns the last update timestamp.
func (c *Contract) UpdatedAt() time.Time { return c.updatedAt }

// Version returns the entity version.
func (c *Contract) Version() int { return c.version }
