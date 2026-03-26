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
	EventTypePlanChanged       eventstore.EventType = "contract.plan_changed"
	EventTypeTrialStarted      eventstore.EventType = "contract.trial_started"
	EventTypeTrialEnded        eventstore.EventType = "contract.trial_ended"
)

// ContractCreatedEvent is raised when a new contract is created.
type ContractCreatedEvent struct {
	ContractID   shared.ContractID `json:"contract_id"`
	AccountID    shared.AccountID  `json:"account_id"`
	PlanID       shared.PlanID     `json:"plan_id"`
	Price        shared.Money      `json:"price"`
	BillingCycle BillingCycle      `json:"billing_cycle"`
	ContractType ContractType      `json:"contract_type"`
	BasePrice    shared.Money      `json:"base_price"`
	CreatedAt    time.Time         `json:"created_at"`
}

func (e *ContractCreatedEvent) EventType() eventstore.EventType { return EventTypeContractCreated }

// ContractActivatedEvent is raised when a contract is activated.
type ContractActivatedEvent struct {
	ContractID  shared.ContractID `json:"contract_id"`
	ActivatedAt time.Time         `json:"activated_at"`
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

// PriceChangedEvent is raised when a contract's price changes.
type PriceChangedEvent struct {
	ContractID  shared.ContractID `json:"contract_id"`
	OldPrice    shared.Money      `json:"old_price"`
	NewPrice    shared.Money      `json:"new_price"`
	ChangedAt   time.Time         `json:"changed_at"`
	EffectiveAt time.Time         `json:"effective_at"`
}

func (e *PriceChangedEvent) EventType() eventstore.EventType { return EventTypePriceChanged }

// PlanChangedEvent is raised when a contract's plan changes.
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
