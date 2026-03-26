package contract

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// contractEventRegistry is the package-level event registry for contract events.
var contractEventRegistry = func() *eventstore.EventRegistry {
	r := eventstore.NewEventRegistry()
	r.Register(&ContractCreatedEvent{})
	r.Register(&ContractActivatedEvent{})
	r.Register(&ContractSuspendedEvent{})
	r.Register(&ContractResumedEvent{})
	r.Register(&ContractCancelledEvent{})
	r.Register(&PriceChangedEvent{})
	r.Register(&PlanChangedEvent{})
	r.Register(&TrialStartedEvent{})
	r.Register(&TrialEndedEvent{})
	return r
}()

// CreateContractCommand holds the parameters for creating a new contract.
type CreateContractCommand struct {
	IdempotencyKey string
	AccountID      shared.AccountID
	PlanID         shared.PlanID
	ContractType   ContractType
	BillingCycle   BillingCycle
	Price          shared.Money
	BasePrice      shared.Money
}

// ContractAggregate is the event-sourced aggregate for contracts.
type ContractAggregate struct {
	eventstore.BaseAggregate

	contractID       shared.ContractID
	accountID        shared.AccountID
	planID           shared.PlanID
	status           ContractStatus
	contractType     ContractType
	billingCycle     BillingCycle
	currentPeriod    shared.DateRange
	trialConfig      *TrialConfiguration
	suspensionConfig *SuspensionConfiguration
	price            shared.Money
	basePrice        shared.Money
	metadata         map[string]string
	createdAt        time.Time
	updatedAt        time.Time
}

// NewContractAggregate creates a new ContractAggregate.
func NewContractAggregate(id shared.ContractID, clock shared.Clock) *ContractAggregate {
	return &ContractAggregate{
		BaseAggregate: eventstore.NewBaseAggregate(string(id), clock),
		contractID:    id,
	}
}

// ContractID returns the contract ID.
func (a *ContractAggregate) ContractID() shared.ContractID { return a.contractID }

// AccountID returns the account ID.
func (a *ContractAggregate) AccountID() shared.AccountID { return a.accountID }

// PlanID returns the plan ID.
func (a *ContractAggregate) PlanID() shared.PlanID { return a.planID }

// Status returns the current status.
func (a *ContractAggregate) Status() ContractStatus { return a.status }

// GetContractType returns the contract type.
func (a *ContractAggregate) GetContractType() ContractType { return a.contractType }

// GetBillingCycle returns the billing cycle.
func (a *ContractAggregate) GetBillingCycle() BillingCycle { return a.billingCycle }

// CurrentPeriod returns the current billing period.
func (a *ContractAggregate) CurrentPeriod() shared.DateRange { return a.currentPeriod }

// TrialConfig returns the trial configuration.
func (a *ContractAggregate) TrialConfig() *TrialConfiguration { return a.trialConfig }

// SuspensionConfig returns the suspension configuration.
func (a *ContractAggregate) SuspensionConfig() *SuspensionConfiguration { return a.suspensionConfig }

// Price returns the current price.
func (a *ContractAggregate) Price() shared.Money { return a.price }

// BasePrice returns the base price.
func (a *ContractAggregate) BasePrice() shared.Money { return a.basePrice }

// GetMetadata returns a copy of the contract metadata.
func (a *ContractAggregate) GetMetadata() map[string]string {
	if a.metadata == nil {
		return nil
	}
	cp := make(map[string]string, len(a.metadata))
	for k, v := range a.metadata {
		cp[k] = v
	}
	return cp
}

// CreatedAt returns the creation timestamp.
func (a *ContractAggregate) CreatedAt() time.Time { return a.createdAt }

// UpdatedAt returns the last update timestamp.
func (a *ContractAggregate) UpdatedAt() time.Time { return a.updatedAt }

// Create creates a new contract from a command.
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error {
	if a.status != "" {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot create contract: already in status %s", a.status))
	}

	now := a.Clock().Now()
	event := &ContractCreatedEvent{
		ContractID:   a.contractID,
		AccountID:    cmd.AccountID,
		PlanID:       cmd.PlanID,
		Price:        cmd.Price,
		BasePrice:    cmd.BasePrice,
		BillingCycle: cmd.BillingCycle,
		ContractType: cmd.ContractType,
		CreatedAt:    now,
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// Activate activates a contract.
func (a *ContractAggregate) Activate(metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusDraft && a.status != ContractStatusTrialing {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot activate contract: current status is %s", a.status))
	}

	event := &ContractActivatedEvent{
		ContractID:  a.contractID,
		ActivatedAt: a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// Suspend suspends a contract.
func (a *ContractAggregate) Suspend(config SuspensionConfiguration, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive && a.status != ContractStatusPastDue {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot suspend contract: current status is %s", a.status))
	}

	event := &ContractSuspendedEvent{
		ContractID:      a.contractID,
		SuspendedAt:     a.Clock().Now(),
		BillingBehavior: config.BillingBehavior,
		ResumeDate:      config.ResumeDate,
		Reason:          config.Reason,
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// Resume resumes a suspended contract.
func (a *ContractAggregate) Resume(metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusSuspended {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot resume contract: current status is %s", a.status))
	}

	event := &ContractResumedEvent{
		ContractID: a.contractID,
		ResumedAt:  a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// Cancel cancels a contract.
func (a *ContractAggregate) Cancel(reason string, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusDraft && a.status != ContractStatusTrialing && a.status != ContractStatusActive && a.status != ContractStatusSuspended && a.status != ContractStatusPastDue {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot cancel contract: current status is %s", a.status))
	}

	event := &ContractCancelledEvent{
		ContractID:  a.contractID,
		CancelledAt: a.Clock().Now(),
		Reason:      reason,
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// ChangePrice changes the contract price.
func (a *ContractAggregate) ChangePrice(newPrice shared.Money, effectiveAt time.Time, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot change price: current status is %s", a.status))
	}

	event := &PriceChangedEvent{
		ContractID:  a.contractID,
		OldPrice:    a.price,
		NewPrice:    newPrice,
		ChangedAt:   a.Clock().Now(),
		EffectiveAt: effectiveAt,
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// ChangePlan changes the contract plan.
func (a *ContractAggregate) ChangePlan(newPlanID shared.PlanID, proration *PlanChangeProration, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot change plan: current status is %s", a.status))
	}

	event := &PlanChangedEvent{
		ContractID: a.contractID,
		OldPlanID:  a.planID,
		NewPlanID:  newPlanID,
		Proration:  proration,
		ChangedAt:  a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// StartTrial starts a trial period.
func (a *ContractAggregate) StartTrial(config TrialConfiguration, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusDraft {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot start trial: current status is %s", a.status))
	}

	event := &TrialStartedEvent{
		ContractID:  a.contractID,
		TrialConfig: config,
		StartedAt:   a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// EndTrial ends a trial period.
func (a *ContractAggregate) EndTrial(converted bool, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusTrialing {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot end trial: current status is %s", a.status))
	}

	event := &TrialEndedEvent{
		ContractID: a.contractID,
		EndedAt:    a.Clock().Now(),
		Converted:  converted,
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// Apply applies a domain event to update aggregate state.
func (a *ContractAggregate) Apply(event eventstore.DomainEvent) error {
	switch e := event.(type) {
	case *ContractCreatedEvent:
		a.contractID = e.ContractID
		a.accountID = e.AccountID
		a.planID = e.PlanID
		a.price = e.Price
		a.basePrice = e.BasePrice
		a.billingCycle = e.BillingCycle
		a.contractType = e.ContractType
		a.status = ContractStatusDraft
		a.createdAt = e.CreatedAt
		a.updatedAt = e.CreatedAt

	case *ContractActivatedEvent:
		a.status = ContractStatusActive
		a.updatedAt = e.ActivatedAt

	case *ContractSuspendedEvent:
		a.status = ContractStatusSuspended
		a.suspensionConfig = &SuspensionConfiguration{
			BillingBehavior: e.BillingBehavior,
			ResumeDate:      e.ResumeDate,
			Reason:          e.Reason,
		}
		a.updatedAt = e.SuspendedAt

	case *ContractResumedEvent:
		a.status = ContractStatusActive
		a.suspensionConfig = nil
		a.updatedAt = e.ResumedAt

	case *ContractCancelledEvent:
		a.status = ContractStatusCancelled
		a.updatedAt = e.CancelledAt

	case *PriceChangedEvent:
		a.price = e.NewPrice
		a.updatedAt = e.ChangedAt

	case *PlanChangedEvent:
		a.planID = e.NewPlanID
		a.updatedAt = e.ChangedAt

	case *TrialStartedEvent:
		a.status = ContractStatusTrialing
		a.trialConfig = &e.TrialConfig
		a.updatedAt = e.StartedAt

	case *TrialEndedEvent:
		a.trialConfig = nil
		if e.Converted {
			a.status = ContractStatusActive
		} else {
			a.status = ContractStatusCancelled
		}
		a.updatedAt = e.EndedAt

	default:
		return shared.NewDomainError(shared.ErrCodeUnknownEvent,
			fmt.Sprintf("unknown event type: %T", event))
	}

	return nil
}

// MarshalSnapshot serializes the aggregate state for snapshot storage.
func (a *ContractAggregate) MarshalSnapshot() ([]byte, error) {
	state := contractSnapshotState{
		ContractID:       a.contractID,
		AccountID:        a.accountID,
		PlanID:           a.planID,
		Status:           a.status,
		ContractType:     a.contractType,
		BillingCycle:     a.billingCycle,
		CurrentPeriod:    a.currentPeriod,
		TrialConfig:      a.trialConfig,
		SuspensionConfig: a.suspensionConfig,
		Price:            a.price,
		BasePrice:        a.basePrice,
		Metadata:         a.metadata,
		CreatedAt:        a.createdAt,
		UpdatedAt:        a.updatedAt,
	}
	return json.Marshal(state)
}

// LoadFromHistory restores aggregate state by replaying persisted events.
func (a *ContractAggregate) LoadFromHistory(events []eventstore.Event) error {
	for _, e := range events {
		domainEvent, err := contractEventRegistry.Deserialize(e.Type, e.Data)
		if err != nil {
			return fmt.Errorf("failed to deserialize event: %w", err)
		}
		if err := a.Apply(domainEvent); err != nil {
			return err
		}
		a.IncrementVersion()
	}
	return nil
}

// contractSnapshotState is the JSON representation of aggregate state for snapshots.
type contractSnapshotState struct {
	ContractID       shared.ContractID        `json:"contract_id"`
	AccountID        shared.AccountID         `json:"account_id"`
	PlanID           shared.PlanID            `json:"plan_id"`
	Status           ContractStatus           `json:"status"`
	ContractType     ContractType             `json:"contract_type"`
	BillingCycle     BillingCycle             `json:"billing_cycle"`
	CurrentPeriod    shared.DateRange         `json:"current_period"`
	TrialConfig      *TrialConfiguration      `json:"trial_config,omitempty"`
	SuspensionConfig *SuspensionConfiguration `json:"suspension_config,omitempty"`
	Price            shared.Money             `json:"price"`
	BasePrice        shared.Money             `json:"base_price"`
	Metadata         map[string]string        `json:"metadata,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}

// LoadFromSnapshot restores aggregate state from a snapshot.
func (a *ContractAggregate) LoadFromSnapshot(snapshot eventstore.Snapshot) error {
	var state contractSnapshotState
	if err := json.Unmarshal(snapshot.State, &state); err != nil {
		return fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	a.contractID = state.ContractID
	a.accountID = state.AccountID
	a.planID = state.PlanID
	a.status = state.Status
	a.contractType = state.ContractType
	a.billingCycle = state.BillingCycle
	a.currentPeriod = state.CurrentPeriod
	a.trialConfig = state.TrialConfig
	a.suspensionConfig = state.SuspensionConfig
	a.price = state.Price
	a.basePrice = state.BasePrice
	a.metadata = state.Metadata
	a.createdAt = state.CreatedAt
	a.updatedAt = state.UpdatedAt
	a.SetVersion(snapshot.Version)

	return nil
}
