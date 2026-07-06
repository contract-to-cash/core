package contract

import (
	"github.com/contract-to-cash/core/domain/pricing"
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

// BillingInterval is an alias for pricing.BillingInterval.
type BillingInterval = pricing.BillingInterval

// NOTE (issue #159): this package previously declared a read-only `Contract`
// entity here — a state-stored mirror of ContractAggregate with no
// constructor, no package-external references, and 0% coverage. It was dead
// code and has been removed. The event-sourced ContractAggregate
// (aggregate.go) is the single contract model in this package; read models
// and projections are the consumer's concern.
