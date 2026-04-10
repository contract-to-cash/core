package contract

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/contract-to-cash/core/domain/pricing"
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
	r.Register(&TrialStartedEvent{})
	r.Register(&TrialEndedEvent{})
	r.Register(&PaymentMethodChangedEvent{})
	r.Register(&ContractRenewedEvent{})
	r.Register(&ContractExpiredEvent{})
	r.Register(&CancellationScheduledEvent{})
	r.Register(&CancellationUnscheduledEvent{})
	r.Register(&PriceChangeScheduledEvent{})
	r.Register(&PriceChangeUnscheduledEvent{})
	return r
}()

// CreateContractCommand holds the parameters for creating a new contract.
type CreateContractCommand struct {
	IdempotencyKey string
	AccountID      shared.AccountID
	PriceID        shared.PriceID
	ContractType   ContractType
	BillingCycle   BillingCycle // Deprecated: Use Interval instead. Kept for backward compatibility.
	Interval       BillingInterval
	Price          shared.Money
	BasePrice      shared.Money
	AutoRenew      bool
}

// resolvedInterval returns the BillingInterval to use. If Interval is set, it is returned.
// If BillingCycle is set, it is converted to a BillingInterval for backward compatibility.
// Returns zero-value BillingInterval if neither is set.
func (cmd *CreateContractCommand) resolvedInterval() BillingInterval {
	if !cmd.Interval.IsZero() {
		return cmd.Interval
	}
	if cmd.BillingCycle != "" {
		return pricing.BillingCycleToInterval(cmd.BillingCycle)
	}
	return BillingInterval{}
}

// ContractAggregate is the event-sourced aggregate for contracts.
type ContractAggregate struct {
	eventstore.BaseAggregate

	contractID        shared.ContractID
	accountID         shared.AccountID
	status            ContractStatus
	contractType      ContractType
	billingCycle      BillingCycle
	interval          BillingInterval
	currentPeriod     shared.DateRange
	trialConfig       *TrialConfiguration
	suspensionConfig  *SuspensionConfiguration
	paymentMethodID   *string
	priceID           shared.PriceID
	price             shared.Money
	basePrice         shared.Money
	autoRenew         bool
	cancelAtPeriodEnd bool
	pendingPriceID    *shared.PriceID
	metadata          map[string]string
	createdAt         time.Time
	updatedAt         time.Time
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

// Status returns the current status.
func (a *ContractAggregate) Status() ContractStatus { return a.status }

// GetContractType returns the contract type.
func (a *ContractAggregate) GetContractType() ContractType { return a.contractType }

// GetBillingCycle returns the billing cycle.
// Deprecated: Use GetInterval() for new code.
func (a *ContractAggregate) GetBillingCycle() BillingCycle { return a.billingCycle }

// GetInterval returns the billing interval.
func (a *ContractAggregate) GetInterval() BillingInterval { return a.interval }

// CurrentPeriod returns the current billing period.
func (a *ContractAggregate) CurrentPeriod() shared.DateRange { return a.currentPeriod }

// TrialConfig returns the trial configuration.
func (a *ContractAggregate) TrialConfig() *TrialConfiguration { return a.trialConfig }

// SuspensionConfig returns the suspension configuration.
func (a *ContractAggregate) SuspensionConfig() *SuspensionConfiguration { return a.suspensionConfig }

// PaymentMethodID returns the contract-level payment method ID.
func (a *ContractAggregate) PaymentMethodID() *string { return a.paymentMethodID }

// Price returns the current price.
func (a *ContractAggregate) Price() shared.Money { return a.price }

// BasePrice returns the base price.
func (a *ContractAggregate) BasePrice() shared.Money { return a.basePrice }

// PriceID returns the current price ID.
func (a *ContractAggregate) PriceID() shared.PriceID { return a.priceID }

// AutoRenew returns whether the contract auto-renews.
func (a *ContractAggregate) AutoRenew() bool { return a.autoRenew }

// CancelAtPeriodEnd returns whether the contract will cancel at the end of the current period.
func (a *ContractAggregate) CancelAtPeriodEnd() bool { return a.cancelAtPeriodEnd }

// PendingPriceID returns the pending price ID to apply at next renewal.
func (a *ContractAggregate) PendingPriceID() *shared.PriceID { return a.pendingPriceID }

// HasPendingChange returns whether there is a pending price change.
func (a *ContractAggregate) HasPendingChange() bool { return a.pendingPriceID != nil }

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
	interval := cmd.resolvedInterval()
	if interval.IsZero() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"either BillingCycle or Interval must be set")
	}
	event := &ContractCreatedEvent{
		ContractID:   a.contractID,
		AccountID:    cmd.AccountID,
		PriceID:      cmd.PriceID,
		Price:        cmd.Price,
		BasePrice:    cmd.BasePrice,
		BillingCycle: cmd.BillingCycle,
		Interval:     interval,
		ContractType: cmd.ContractType,
		AutoRenew:    cmd.AutoRenew,
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

	now := a.Clock().Now()
	// Calculate the initial billing period based on interval
	periodEnd := a.interval.AddTo(now)
	initialPeriod, err := shared.NewDateRange(now, periodEnd)
	if err != nil {
		return err
	}

	event := &ContractActivatedEvent{
		ContractID:    a.contractID,
		ActivatedAt:   now,
		CurrentPeriod: initialPeriod,
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

// ChangePrice changes the contract price using the specified policy.
// IMMEDIATE changes take effect now; END_OF_TERM defers to next renewal.
func (a *ContractAggregate) ChangePrice(newPriceID shared.PriceID, policy ChangePolicy, proration *PlanChangeProration, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot change price: current status is %s", a.status))
	}

	switch policy {
	case ChangePolicyImmediate:
		return a.changePriceImmediate(newPriceID, proration, metadata)
	case ChangePolicyEndOfTerm:
		return a.changePriceEndOfTerm(newPriceID, metadata)
	default:
		return shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("unknown change policy: %s", policy))
	}
}

func (a *ContractAggregate) changePriceImmediate(newPriceID shared.PriceID, proration *PlanChangeProration, metadata eventstore.EventMetadata) error {
	event := &PriceChangedEvent{
		ContractID: a.contractID,
		OldPriceID: a.priceID,
		NewPriceID: newPriceID,
		Policy:     ChangePolicyImmediate,
		Proration:  proration,
		ChangedAt:  a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

func (a *ContractAggregate) changePriceEndOfTerm(newPriceID shared.PriceID, metadata eventstore.EventMetadata) error {
	event := &PriceChangeScheduledEvent{
		ContractID:     a.contractID,
		CurrentPriceID: a.priceID,
		NewPriceID:     newPriceID,
		Policy:         ChangePolicyEndOfTerm,
		ScheduledAt:    a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// UnscheduleChange cancels a pending price change.
func (a *ContractAggregate) UnscheduleChange(reason string, metadata eventstore.EventMetadata) error {
	if a.pendingPriceID == nil {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"no pending change to cancel")
	}

	event := &PriceChangeUnscheduledEvent{
		ContractID:       a.contractID,
		CancelledPriceID: *a.pendingPriceID,
		Reason:           reason,
		UnscheduledAt:    a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// ChangePaymentMethod changes the contract-level payment method.
func (a *ContractAggregate) ChangePaymentMethod(paymentMethodID *string, metadata eventstore.EventMetadata) error {
	if a.status == "" || a.status == ContractStatusCancelled || a.status == ContractStatusExpired {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot change payment method: current status is %s", a.status))
	}

	event := &PaymentMethodChangedEvent{
		ContractID:         a.contractID,
		OldPaymentMethodID: a.paymentMethodID,
		NewPaymentMethodID: paymentMethodID,
		ChangedAt:          a.Clock().Now(),
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

// Renew renews the contract for a new billing period.
// The newBillingCycle parameter specifies the billing cycle for the next period,
// typically resolved from the Price entity by the caller (e.g., batch processor).
// Deprecated: Use RenewWithInterval for new code.
func (a *ContractAggregate) Renew(newBillingCycle BillingCycle, metadata eventstore.EventMetadata) error {
	return a.RenewWithInterval(pricing.BillingCycleToInterval(newBillingCycle), metadata)
}

// RenewWithInterval renews the contract for a new billing period using a BillingInterval.
func (a *ContractAggregate) RenewWithInterval(newInterval BillingInterval, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot renew: status is %s", a.status))
	}

	if a.cancelAtPeriodEnd {
		return a.Cancel("scheduled cancellation at period end", metadata)
	}

	if !a.autoRenew {
		return a.expire(metadata)
	}

	// Calculate the next period using the new interval's AddTo
	newPeriodStart := a.currentPeriod.End()
	newPeriodEnd := newInterval.AddTo(newPeriodStart)
	newPeriod, err := shared.NewDateRange(newPeriodStart, newPeriodEnd)
	if err != nil {
		return err
	}

	oldPriceID := a.priceID
	newPriceID := a.priceID
	priceChanged := false
	if a.pendingPriceID != nil {
		newPriceID = *a.pendingPriceID
		priceChanged = true
	}

	newBillingCycle := newInterval.ToBillingCycle()

	event := &ContractRenewedEvent{
		ContractID:      a.contractID,
		OldPeriod:       a.currentPeriod,
		NewPeriod:       newPeriod,
		OldPriceID:      oldPriceID,
		NewPriceID:      newPriceID,
		PriceChanged:    priceChanged,
		OldBillingCycle: a.billingCycle,
		NewBillingCycle: newBillingCycle,
		OldInterval:     a.interval,
		NewInterval:     newInterval,
		RenewedAt:       a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// ScheduleCancellation schedules the contract for cancellation at the end of the current period.
func (a *ContractAggregate) ScheduleCancellation(reason string, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot schedule cancellation: current status is %s", a.status))
	}
	if a.cancelAtPeriodEnd {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"cancellation is already scheduled")
	}

	event := &CancellationScheduledEvent{
		ContractID:  a.contractID,
		Reason:      reason,
		ScheduledAt: a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// UnscheduleCancellation revokes a previously scheduled cancellation.
func (a *ContractAggregate) UnscheduleCancellation(metadata eventstore.EventMetadata) error {
	if !a.cancelAtPeriodEnd {
		return shared.NewDomainError(shared.ErrCodeBusinessRule,
			"no cancellation is scheduled")
	}

	event := &CancellationUnscheduledEvent{
		ContractID:    a.contractID,
		UnscheduledAt: a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// expire transitions the contract to expired status.
func (a *ContractAggregate) expire(metadata eventstore.EventMetadata) error {
	event := &ContractExpiredEvent{
		ContractID:  a.contractID,
		ExpiredAt:   a.Clock().Now(),
		FinalPeriod: a.currentPeriod,
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
		a.priceID = e.PriceID
		a.price = e.Price
		a.basePrice = e.BasePrice
		a.billingCycle = e.BillingCycle
		// Resolve interval: prefer explicit Interval, fall back to BillingCycle conversion
		if !e.Interval.IsZero() {
			a.interval = e.Interval
		} else {
			a.interval = pricing.BillingCycleToInterval(e.BillingCycle)
		}
		a.contractType = e.ContractType
		a.autoRenew = e.AutoRenew
		a.status = ContractStatusDraft
		a.createdAt = e.CreatedAt
		a.updatedAt = e.CreatedAt

	case *ContractActivatedEvent:
		a.status = ContractStatusActive
		a.currentPeriod = e.CurrentPeriod
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
		a.cancelAtPeriodEnd = false
		a.updatedAt = e.CancelledAt

	case *PriceChangedEvent:
		a.priceID = e.NewPriceID
		a.pendingPriceID = nil // IMMEDIATE clears pending
		// Note: a.price (Money) is NOT updated here. In the new PriceID-based
		// architecture, the authoritative price amount lives in the Price
		// aggregate. The legacy a.price field is only set at creation time
		// for backward compatibility.
		a.updatedAt = e.ChangedAt

	case *PriceChangeScheduledEvent:
		a.pendingPriceID = &e.NewPriceID // priceID unchanged
		a.updatedAt = e.ScheduledAt

	case *PriceChangeUnscheduledEvent:
		a.pendingPriceID = nil
		a.updatedAt = e.UnscheduledAt

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

	case *PaymentMethodChangedEvent:
		a.paymentMethodID = e.NewPaymentMethodID
		a.updatedAt = e.ChangedAt

	case *ContractRenewedEvent:
		a.currentPeriod = e.NewPeriod
		a.priceID = e.NewPriceID
		a.pendingPriceID = nil
		if !e.NewInterval.IsZero() {
			a.interval = e.NewInterval
			a.billingCycle = e.NewInterval.ToBillingCycle()
		} else if e.NewBillingCycle != "" {
			a.billingCycle = e.NewBillingCycle
			a.interval = pricing.BillingCycleToInterval(e.NewBillingCycle)
		}
		a.updatedAt = e.RenewedAt

	case *ContractExpiredEvent:
		a.status = ContractStatusExpired
		a.updatedAt = e.ExpiredAt

	case *CancellationScheduledEvent:
		a.cancelAtPeriodEnd = true
		a.updatedAt = e.ScheduledAt

	case *CancellationUnscheduledEvent:
		a.cancelAtPeriodEnd = false
		a.updatedAt = e.UnscheduledAt

	default:
		return shared.NewDomainError(shared.ErrCodeUnknownEvent,
			fmt.Sprintf("unknown event type: %T", event))
	}

	return nil
}

// MarshalSnapshot serializes the aggregate state for snapshot storage.
//
// This is the event-sourced snapshot pattern: it returns []byte for storage
// inside eventstore.Snapshot.State, used alongside event replay to restore
// aggregate state. It is distinct from the ToSnapshot/FromSnapshot pattern
// used by state-stored entities (Invoice, Payment, BalanceEntry, etc. — see
// each domain package's snapshot.go). Do not add ToSnapshot/FromSnapshot to
// ContractAggregate, and do not add MarshalSnapshot to state-stored
// entities — the two patterns serve different persistence models.
func (a *ContractAggregate) MarshalSnapshot() ([]byte, error) {
	state := contractSnapshotState{
		ContractID:        a.contractID,
		AccountID:         a.accountID,
		Status:            a.status,
		ContractType:      a.contractType,
		BillingCycle:      a.billingCycle,
		Interval:          a.interval,
		CurrentPeriod:     a.currentPeriod,
		TrialConfig:       a.trialConfig,
		SuspensionConfig:  a.suspensionConfig,
		PaymentMethodID:   a.paymentMethodID,
		PriceID:           a.priceID,
		Price:             a.price,
		BasePrice:         a.basePrice,
		AutoRenew:         a.autoRenew,
		CancelAtPeriodEnd: a.cancelAtPeriodEnd,
		PendingPriceID:    a.pendingPriceID,
		Metadata:          a.metadata,
		CreatedAt:         a.createdAt,
		UpdatedAt:         a.updatedAt,
	}
	return json.Marshal(state)
}

// contractUpcasterChain is the package-level upcaster chain for contract events.
var contractUpcasterChain = NewContractUpcasterChain()

// LoadFromHistory restores aggregate state by replaying persisted events.
func (a *ContractAggregate) LoadFromHistory(events []eventstore.Event) error {
	for _, e := range events {
		// Upcast legacy event schemas before deserialization.
		upcasted, err := contractUpcasterChain.Upcast(e)
		if err != nil {
			return fmt.Errorf("failed to upcast event: %w", err)
		}
		domainEvent, err := contractEventRegistry.Deserialize(upcasted.Type, upcasted.Data)
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
	ContractID        shared.ContractID        `json:"contract_id"`
	AccountID         shared.AccountID         `json:"account_id"`
	Status            ContractStatus           `json:"status"`
	ContractType      ContractType             `json:"contract_type"`
	BillingCycle      BillingCycle             `json:"billing_cycle"`      // Deprecated: kept for backward compat
	Interval          BillingInterval          `json:"interval,omitempty"` // New: flexible billing interval
	CurrentPeriod     shared.DateRange         `json:"current_period"`
	TrialConfig       *TrialConfiguration      `json:"trial_config,omitempty"`
	SuspensionConfig  *SuspensionConfiguration `json:"suspension_config,omitempty"`
	PaymentMethodID   *string                  `json:"payment_method_id,omitempty"`
	PriceID           shared.PriceID           `json:"price_id,omitempty"`
	Price             shared.Money             `json:"price"`
	BasePrice         shared.Money             `json:"base_price"`
	AutoRenew         bool                     `json:"auto_renew"`
	CancelAtPeriodEnd bool                     `json:"cancel_at_period_end"`
	PendingPriceID    *shared.PriceID          `json:"pending_price_id,omitempty"`
	Metadata          map[string]string        `json:"metadata,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// LoadFromSnapshot restores aggregate state from a snapshot.
func (a *ContractAggregate) LoadFromSnapshot(snapshot eventstore.Snapshot) error {
	var state contractSnapshotState
	if err := json.Unmarshal(snapshot.State, &state); err != nil {
		return fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	a.contractID = state.ContractID
	a.accountID = state.AccountID
	a.status = state.Status
	a.contractType = state.ContractType
	a.billingCycle = state.BillingCycle
	// Resolve interval: prefer explicit Interval, fall back to BillingCycle conversion
	if !state.Interval.IsZero() {
		a.interval = state.Interval
	} else {
		a.interval = pricing.BillingCycleToInterval(state.BillingCycle)
	}
	a.currentPeriod = state.CurrentPeriod
	a.trialConfig = state.TrialConfig
	a.suspensionConfig = state.SuspensionConfig
	a.paymentMethodID = state.PaymentMethodID
	a.priceID = state.PriceID
	a.price = state.Price
	a.basePrice = state.BasePrice
	a.autoRenew = state.AutoRenew
	a.cancelAtPeriodEnd = state.CancelAtPeriodEnd
	a.pendingPriceID = state.PendingPriceID
	a.metadata = state.Metadata
	a.createdAt = state.CreatedAt
	a.updatedAt = state.UpdatedAt
	a.SetVersion(snapshot.Version)

	return nil
}
