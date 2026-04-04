package contract

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// Event type constants.
const (
	EventTypeContractCreated   eventstore.EventType = "contract.created"
	EventTypeContractActivated eventstore.EventType = "contract.activated"
	EventTypeContractSuspended eventstore.EventType = "contract.suspended"
	EventTypeContractResumed   eventstore.EventType = "contract.resumed"
	EventTypeContractCancelled eventstore.EventType = "contract.cancelled"
	EventTypePriceChanged      eventstore.EventType = "contract.price_changed"
	// Deprecated: PlanChangedEvent is no longer raised. Kept for backward compatibility
	// with existing event stores that may contain historical plan_changed events.
	EventTypePlanChanged             eventstore.EventType = "contract.plan_changed"
	EventTypeTrialStarted            eventstore.EventType = "contract.trial_started"
	EventTypeTrialEnded              eventstore.EventType = "contract.trial_ended"
	EventTypePaymentMethodChanged    eventstore.EventType = "contract.payment_method_changed"
	EventTypeContractRenewed         eventstore.EventType = "contract.renewed"
	EventTypeContractExpired         eventstore.EventType = "contract.expired"
	EventTypeCancellationScheduled   eventstore.EventType = "contract.cancellation_scheduled"
	EventTypeCancellationUnscheduled eventstore.EventType = "contract.cancellation_unscheduled"
	EventTypePriceChangeScheduled    eventstore.EventType = "contract.price_change_scheduled"
	EventTypePriceChangeUnscheduled  eventstore.EventType = "contract.price_change_unscheduled"
)

// ContractCreatedEvent is raised when a new contract is created.
type ContractCreatedEvent struct {
	ContractID shared.ContractID `json:"contract_id"`
	AccountID  shared.AccountID  `json:"account_id"`
	// Deprecated: PlanID is kept for backward compatibility with historical events.
	// New events use PriceID instead.
	PlanID       shared.PlanID  `json:"plan_id,omitempty"`
	PriceID      shared.PriceID `json:"price_id,omitempty"`
	Price        shared.Money   `json:"price"`
	BillingCycle BillingCycle   `json:"billing_cycle"`
	ContractType ContractType   `json:"contract_type"`
	BasePrice    shared.Money   `json:"base_price"`
	AutoRenew    bool           `json:"auto_renew"`
	CreatedAt    time.Time      `json:"created_at"`
}

func (e *ContractCreatedEvent) EventType() eventstore.EventType { return EventTypeContractCreated }

// ContractActivatedEvent is raised when a contract is activated.
type ContractActivatedEvent struct {
	ContractID    shared.ContractID `json:"contract_id"`
	ActivatedAt   time.Time         `json:"activated_at"`
	CurrentPeriod shared.DateRange  `json:"current_period"`
}

func (e *ContractActivatedEvent) EventType() eventstore.EventType { return EventTypeContractActivated }

// ContractSuspendedEvent is raised when a contract is suspended.
type ContractSuspendedEvent struct {
	ContractID      shared.ContractID         `json:"contract_id"`
	SuspendedAt     time.Time                 `json:"suspended_at"`
	BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
	ResumeDate      *time.Time                `json:"resume_date,omitempty"`
	Reason          string                    `json:"reason"`
}

func (e *ContractSuspendedEvent) EventType() eventstore.EventType { return EventTypeContractSuspended }

// ContractResumedEvent is raised when a suspended contract is resumed.
type ContractResumedEvent struct {
	ContractID shared.ContractID `json:"contract_id"`
	ResumedAt  time.Time         `json:"resumed_at"`
}

func (e *ContractResumedEvent) EventType() eventstore.EventType { return EventTypeContractResumed }

// ContractCancelledEvent is raised when a contract is cancelled.
type ContractCancelledEvent struct {
	ContractID  shared.ContractID `json:"contract_id"`
	CancelledAt time.Time         `json:"cancelled_at"`
	Reason      string            `json:"reason"`
}

func (e *ContractCancelledEvent) EventType() eventstore.EventType { return EventTypeContractCancelled }

// PriceChangedEvent is raised when a contract's price changes immediately.
type PriceChangedEvent struct {
	ContractID shared.ContractID    `json:"contract_id"`
	OldPriceID shared.PriceID       `json:"old_price_id"`
	NewPriceID shared.PriceID       `json:"new_price_id"`
	Policy     ChangePolicy         `json:"policy"`
	Proration  *PlanChangeProration `json:"proration,omitempty"`
	ChangedAt  time.Time            `json:"changed_at"`
	// Legacy fields kept for backward compatibility with historical events.
	OldPrice    shared.Money `json:"old_price"`
	NewPrice    shared.Money `json:"new_price"`
	EffectiveAt time.Time    `json:"effective_at"`
}

func (e *PriceChangedEvent) EventType() eventstore.EventType { return EventTypePriceChanged }

// PriceChangeScheduledEvent is raised when a price change is deferred to next renewal.
type PriceChangeScheduledEvent struct {
	ContractID     shared.ContractID `json:"contract_id"`
	CurrentPriceID shared.PriceID    `json:"current_price_id"`
	NewPriceID     shared.PriceID    `json:"new_price_id"`
	Policy         ChangePolicy      `json:"policy"`
	ScheduledAt    time.Time         `json:"scheduled_at"`
}

func (e *PriceChangeScheduledEvent) EventType() eventstore.EventType {
	return EventTypePriceChangeScheduled
}

// PriceChangeUnscheduledEvent is raised when a pending price change is cancelled.
type PriceChangeUnscheduledEvent struct {
	ContractID       shared.ContractID `json:"contract_id"`
	CancelledPriceID shared.PriceID    `json:"cancelled_price_id"`
	Reason           string            `json:"reason"`
	UnscheduledAt    time.Time         `json:"unscheduled_at"`
}

func (e *PriceChangeUnscheduledEvent) EventType() eventstore.EventType {
	return EventTypePriceChangeUnscheduled
}

// PlanChangedEvent is raised when a contract's plan changes.
// Deprecated: No longer raised by new code. Kept for backward compatibility
// with existing event stores that may contain historical plan_changed events.
type PlanChangedEvent struct {
	ContractID shared.ContractID    `json:"contract_id"`
	OldPlanID  shared.PlanID        `json:"old_plan_id"`
	NewPlanID  shared.PlanID        `json:"new_plan_id"`
	Proration  *PlanChangeProration `json:"proration,omitempty"`
	ChangedAt  time.Time            `json:"changed_at"`
}

func (e *PlanChangedEvent) EventType() eventstore.EventType { return EventTypePlanChanged }

// TrialStartedEvent is raised when a trial period starts.
type TrialStartedEvent struct {
	ContractID  shared.ContractID  `json:"contract_id"`
	TrialConfig TrialConfiguration `json:"trial_config"`
	StartedAt   time.Time          `json:"started_at"`
}

func (e *TrialStartedEvent) EventType() eventstore.EventType { return EventTypeTrialStarted }

// TrialEndedEvent is raised when a trial period ends.
type TrialEndedEvent struct {
	ContractID shared.ContractID `json:"contract_id"`
	EndedAt    time.Time         `json:"ended_at"`
	Converted  bool              `json:"converted"`
}

func (e *TrialEndedEvent) EventType() eventstore.EventType { return EventTypeTrialEnded }

// PaymentMethodChangedEvent is raised when a contract's payment method changes.
type PaymentMethodChangedEvent struct {
	ContractID         shared.ContractID `json:"contract_id"`
	OldPaymentMethodID *string           `json:"old_payment_method_id,omitempty"`
	NewPaymentMethodID *string           `json:"new_payment_method_id,omitempty"`
	ChangedAt          time.Time         `json:"changed_at"`
}

func (e *PaymentMethodChangedEvent) EventType() eventstore.EventType {
	return EventTypePaymentMethodChanged
}

// ContractRenewedEvent is raised when a contract is renewed for a new billing period.
type ContractRenewedEvent struct {
	ContractID      shared.ContractID `json:"contract_id"`
	OldPeriod       shared.DateRange  `json:"old_period"`
	NewPeriod       shared.DateRange  `json:"new_period"`
	OldPriceID      shared.PriceID    `json:"old_price_id"`
	NewPriceID      shared.PriceID    `json:"new_price_id"`
	PriceChanged    bool              `json:"price_changed"`
	OldBillingCycle BillingCycle      `json:"old_billing_cycle,omitempty"`
	NewBillingCycle BillingCycle      `json:"new_billing_cycle,omitempty"`
	RenewedAt       time.Time         `json:"renewed_at"`
}

func (e *ContractRenewedEvent) EventType() eventstore.EventType { return EventTypeContractRenewed }

// ContractExpiredEvent is raised when a contract expires at the end of its period.
type ContractExpiredEvent struct {
	ContractID  shared.ContractID `json:"contract_id"`
	ExpiredAt   time.Time         `json:"expired_at"`
	FinalPeriod shared.DateRange  `json:"final_period"`
}

func (e *ContractExpiredEvent) EventType() eventstore.EventType { return EventTypeContractExpired }

// CancellationScheduledEvent is raised when a contract is scheduled for cancellation at period end.
type CancellationScheduledEvent struct {
	ContractID  shared.ContractID `json:"contract_id"`
	Reason      string            `json:"reason"`
	ScheduledAt time.Time         `json:"scheduled_at"`
}

func (e *CancellationScheduledEvent) EventType() eventstore.EventType {
	return EventTypeCancellationScheduled
}

// CancellationUnscheduledEvent is raised when a scheduled cancellation is revoked.
type CancellationUnscheduledEvent struct {
	ContractID    shared.ContractID `json:"contract_id"`
	UnscheduledAt time.Time         `json:"unscheduled_at"`
}

func (e *CancellationUnscheduledEvent) EventType() eventstore.EventType {
	return EventTypeCancellationUnscheduled
}
