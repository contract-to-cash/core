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
	// A duplicate registration is a programmer error surfaced at package init
	// (two events claiming the same EventType, or a registration wired twice),
	// so fail fast with a panic rather than propagate an error through this var
	// initializer.
	mustRegister := func(event eventstore.DomainEvent) {
		if err := r.Register(event); err != nil {
			panic(fmt.Sprintf("contract event registry: %v", err))
		}
	}
	mustRegister(&ContractCreatedEvent{})
	mustRegister(&ContractActivatedEvent{})
	mustRegister(&ContractSuspendedEvent{})
	mustRegister(&ContractResumedEvent{})
	mustRegister(&ContractCancelledEvent{})
	mustRegister(&PriceChangedEvent{})
	mustRegister(&TrialStartedEvent{})
	mustRegister(&TrialEndedEvent{})
	mustRegister(&PaymentMethodChangedEvent{})
	mustRegister(&ContractRenewedEvent{})
	mustRegister(&ContractExpiredEvent{})
	mustRegister(&CancellationScheduledEvent{})
	mustRegister(&CancellationUnscheduledEvent{})
	mustRegister(&PriceChangeScheduledEvent{})
	mustRegister(&PriceChangeUnscheduledEvent{})
	mustRegister(&ContractPastDueEvent{})
	mustRegister(&ContractRecoveredEvent{})
	return r
}()

// CreateContractCommand holds the parameters for creating a new contract.
//
// IdempotencyKey is REQUIRED (design-decisions.md section 4.1): Create rejects
// an empty key with a validation DomainError. The key is carried on
// ContractCreatedEvent so persistence adapters can enforce at-most-once
// creation with a unique index — see the uniqueness note on Repository.Save.
// The core validates presence only; uniqueness enforcement is the
// repository/adapter's contract.
type CreateContractCommand struct {
	IdempotencyKey string
	AccountID      shared.AccountID
	PriceID        shared.PriceID
	ContractType   ContractType
	Interval       BillingInterval
	Price          shared.Money
	BasePrice      shared.Money
	AutoRenew      bool
}

// ContractAggregate is the event-sourced aggregate for contracts.
type ContractAggregate struct {
	eventstore.BaseAggregate

	contractID        shared.ContractID
	accountID         shared.AccountID
	idempotencyKey    string
	status            ContractStatus
	contractType      ContractType
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
	// billingAnchorDay is the day-of-month (1..31) of the original activation,
	// used to prevent month-end billing-anchor drift across successive renewals
	// (issue #186). It is 0 until the initial billing period is established
	// (Activate / trial conversion) and is derived on replay from the initial
	// period's start day — it is never mutated by a renewal, so the anchor
	// survives clamping (Jan 31 -> Feb 28 -> Mar 31, not -> Mar 28). See
	// RenewWithInterval and BillingInterval.AddToWithAnchorDay.
	billingAnchorDay int
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

// IdempotencyKey returns the creation idempotency key (issue #159).
//
// It is empty for aggregates replayed from history recorded before the key
// was carried on ContractCreatedEvent (schema version < 3) and for legacy
// snapshots; it is always non-empty for contracts created since, because
// Create validates it.
func (a *ContractAggregate) IdempotencyKey() string { return a.idempotencyKey }

// Status returns the current status.
func (a *ContractAggregate) Status() ContractStatus { return a.status }

// GetContractType returns the contract type.
func (a *ContractAggregate) GetContractType() ContractType { return a.contractType }

// GetInterval returns the billing interval.
func (a *ContractAggregate) GetInterval() BillingInterval { return a.interval }

// CurrentPeriod returns the current billing period.
func (a *ContractAggregate) CurrentPeriod() shared.DateRange { return a.currentPeriod }

// TrialConfig returns the trial configuration.
// The returned value is a deep copy — mutating it does not affect the aggregate.
func (a *ContractAggregate) TrialConfig() *TrialConfiguration { return a.trialConfig.clone() }

// SuspensionConfig returns the suspension configuration.
// The returned value is a deep copy — mutating it does not affect the aggregate.
func (a *ContractAggregate) SuspensionConfig() *SuspensionConfiguration {
	return a.suspensionConfig.clone()
}

// PaymentMethodID returns the contract-level payment method ID.
// The returned pointer is a defensive copy — mutating the pointee does not
// affect the aggregate.
func (a *ContractAggregate) PaymentMethodID() *string { return shared.PtrCopy(a.paymentMethodID) }

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
// The returned pointer is a defensive copy — mutating the pointee does not
// affect the aggregate.
func (a *ContractAggregate) PendingPriceID() *shared.PriceID {
	return shared.PtrCopy(a.pendingPriceID)
}

// HasPendingChange returns whether there is a pending price change.
func (a *ContractAggregate) HasPendingChange() bool { return a.pendingPriceID != nil }

// CreatedAt returns the creation timestamp.
func (a *ContractAggregate) CreatedAt() time.Time { return a.createdAt }

// UpdatedAt returns the last update timestamp.
func (a *ContractAggregate) UpdatedAt() time.Time { return a.updatedAt }

// BillingAnchorDay returns the day-of-month (1..31) that billing periods are
// anchored to, or 0 if no billing period has been established yet. It is set
// when the contract activates (or a trial converts) and is preserved across
// renewals so a month-end anchor does not drift (issue #186).
func (a *ContractAggregate) BillingAnchorDay() int { return a.billingAnchorDay }

// Create creates a new contract from a command.
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error {
	if a.status != "" {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot create contract: already in status %s", a.status))
	}

	// A nil clock would panic on Clock().Now() below. NewContractAggregate takes
	// the clock as a plain argument (no error return), so guard it here at the
	// first command instead — the aggregate is unusable without a clock, and this
	// turns a nil-pointer panic into a domain error (issue #196).
	if a.Clock() == nil {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"clock must be set (NewContractAggregate was given a nil Clock)")
	}
	now := a.Clock().Now()
	if cmd.IdempotencyKey == "" {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"IdempotencyKey must be set")
	}
	// Create writes an immutable ContractCreatedEvent, so a nonsensical command
	// persisted here is uncorrectable. Reject the invariant violations up front
	// (issue #196).
	if cmd.AccountID == "" {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"AccountID must be set")
	}
	if cmd.Interval.IsZero() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"Interval must be set")
	}
	// A contract must reference a price somehow: either a PriceID (the Price
	// entity path) or a non-zero legacy Price amount. A contract with neither has
	// no basis for billing.
	if cmd.PriceID == "" && cmd.Price.IsZero() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"either PriceID or a non-zero Price must be set")
	}
	// Price and BasePrice must agree on currency so the event stream never carries
	// a self-contradictory monetary pair. A zero amount carries no currency
	// signal, so only cross-check when both are non-zero.
	if !cmd.Price.IsZero() && !cmd.BasePrice.IsZero() && cmd.Price.Currency() != cmd.BasePrice.Currency() {
		return shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
			fmt.Sprintf("Price currency %s does not match BasePrice currency %s",
				cmd.Price.Currency(), cmd.BasePrice.Currency()))
	}
	event := &ContractCreatedEvent{
		ContractID:     a.contractID,
		AccountID:      cmd.AccountID,
		PriceID:        cmd.PriceID,
		IdempotencyKey: cmd.IdempotencyKey,
		Price:          cmd.Price,
		BasePrice:      cmd.BasePrice,
		Interval:       cmd.Interval,
		ContractType:   cmd.ContractType,
		AutoRenew:      cmd.AutoRenew,
		CreatedAt:      now,
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// Activate activates a contract.
//
// From Trialing, activation is a trial conversion: it is routed through
// EndTrial(converted=true) so that both conversion paths — the batch
// TrialExpirationProcessor and an integrator calling Activate directly — produce
// identical state (status Active, initial billing period established,
// trialConfig cleared) and record the same TrialEndedEvent (issue #146).
func (a *ContractAggregate) Activate(metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusDraft && a.status != ContractStatusTrialing {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot activate contract: current status is %s", a.status))
	}

	if a.status == ContractStatusTrialing {
		return a.EndTrial(true, metadata)
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

	// Intake defense: deep-copy the caller-owned ResumeDate pointer so that a
	// post-call mutation by the caller cannot rewrite the event payload.
	config = config.cloneValue()

	event := &ContractSuspendedEvent{
		ContractID:      a.contractID,
		SuspendedAt:     a.Clock().Now(),
		BillingBehavior: config.BillingBehavior,
		ResumeDate:      config.ResumeDate,
		ExtendContract:  config.ExtendContract,
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

// MarkPastDue transitions an active contract to past_due, e.g. when a payment
// failure starts the dunning process. From past_due the contract can recover
// (RecoverFromPastDue), be suspended (max retries reached), or be cancelled.
func (a *ContractAggregate) MarkPastDue(reason string, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusActive {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot mark past due: current status is %s", a.status))
	}

	event := &ContractPastDueEvent{
		ContractID: a.contractID,
		Reason:     reason,
		MarkedAt:   a.Clock().Now(),
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
}

// RecoverFromPastDue transitions a past_due contract back to active, e.g. after
// a successful payment clears the outstanding balance.
func (a *ContractAggregate) RecoverFromPastDue(metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusPastDue {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot recover from past due: current status is %s", a.status))
	}

	event := &ContractRecoveredEvent{
		ContractID:  a.contractID,
		RecoveredAt: a.Clock().Now(),
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
		// Intake defense: deep-copy the caller-owned PlanChangeProration so a
		// post-call mutation by the caller cannot rewrite the event payload
		// after RaiseEvent. Money fields inside PlanChangeProration are
		// effectively immutable from outside the shared package, so a shallow
		// struct copy via shared.PtrCopy is sufficient.
		Proration: shared.PtrCopy(proration),
		ChangedAt: a.Clock().Now(),
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
	// Terminal contracts must not raise further events. Without this guard a
	// cancelled/expired contract that still carried a pending price change (see
	// the Apply fix in issue #196 that now clears it) could record a
	// PriceChangeUnscheduledEvent after reaching a terminal state.
	if a.status == ContractStatusCancelled || a.status == ContractStatusExpired {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot unschedule change: contract is %s", a.status))
	}
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

	// Intake defense: deep-copy the caller-owned pointer so that a post-call
	// mutation by the caller cannot rewrite the event payload.
	event := &PaymentMethodChangedEvent{
		ContractID:         a.contractID,
		OldPaymentMethodID: a.paymentMethodID,
		NewPaymentMethodID: shared.PtrCopy(paymentMethodID),
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

	// Validate the trial configuration against the aggregate's clock before it is
	// written to the immutable event stream (issue #196). A zero or already-past
	// TrialEndDate would put the contract straight into an expired trial, and a
	// negative reminder-day offset is meaningless.
	now := a.Clock().Now()
	if config.TrialEndDate.IsZero() {
		return shared.NewDomainError(shared.ErrCodeValidation,
			"TrialEndDate must be set")
	}
	if !config.TrialEndDate.After(now) {
		return shared.NewDomainError(shared.ErrCodeValidation,
			fmt.Sprintf("TrialEndDate %s must be in the future (now %s)",
				config.TrialEndDate.Format(time.RFC3339), now.Format(time.RFC3339)))
	}
	for _, d := range config.ConversionReminderDays {
		if d < 0 {
			return shared.NewDomainError(shared.ErrCodeValidation,
				fmt.Sprintf("ConversionReminderDays must not be negative: got %d", d))
		}
	}

	// Intake defense: deep-copy the caller-owned ConversionReminderDays slice
	// so that a post-call mutation by the caller cannot rewrite the event
	// payload.
	config = config.cloneValue()

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
//
// When converted=true the contract becomes Active and an initial billing period
// is established from the billing interval anchored at the end time — the same
// way Activate establishes it — so the converted contract participates in the
// renewal/billing cycle (issue #146). When converted=false the contract is
// cancelled and no period is set.
func (a *ContractAggregate) EndTrial(converted bool, metadata eventstore.EventMetadata) error {
	if a.status != ContractStatusTrialing {
		return shared.NewDomainError(shared.ErrCodeInvalidStateTransition,
			fmt.Sprintf("cannot end trial: current status is %s", a.status))
	}

	now := a.Clock().Now()
	event := &TrialEndedEvent{
		ContractID: a.contractID,
		EndedAt:    now,
		Converted:  converted,
	}
	if converted {
		// Establish the initial billing period, mirroring Activate.
		period, err := shared.NewDateRange(now, a.interval.AddTo(now))
		if err != nil {
			return err
		}
		event.CurrentPeriod = period
	}

	if err := a.Apply(event); err != nil {
		return err
	}
	return a.RaiseEvent(event, metadata)
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

	// Calculate the next period, anchoring the end on the original billing day
	// so a month-end anchor does not drift across renewals (issue #186). The
	// start chains from the previous period end (a possibly-clamped date such
	// as Feb 28), but AddToWithAnchorDay restores the anchor day (e.g. 31)
	// wherever the target month allows: Feb 28 -> Mar 31, Mar 31 -> Apr 30, ...
	newPeriodStart := a.currentPeriod.End()
	newPeriodEnd := newInterval.AddToWithAnchorDay(newPeriodStart, a.billingAnchorDay)
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

	event := &ContractRenewedEvent{
		ContractID:   a.contractID,
		OldPeriod:    a.currentPeriod,
		NewPeriod:    newPeriod,
		OldPriceID:   oldPriceID,
		NewPriceID:   newPriceID,
		PriceChanged: priceChanged,
		OldInterval:  a.interval,
		NewInterval:  newInterval,
		RenewedAt:    a.Clock().Now(),
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
// Apply mutates the aggregate's in-memory state for a single event. It is used
// both by command methods (which call Apply then RaiseEvent) and by
// LoadFromHistory during replay.
//
// Ordering note (issue #162 L-8): command methods intentionally call Apply
// BEFORE RaiseEvent so a rejected transition (Apply returning an error) never
// records an event. The residual fragility is the reverse edge — if RaiseEvent
// failed AFTER a successful Apply, the aggregate would carry a mutation with no
// corresponding uncommitted event. In practice RaiseEvent only fails on a
// json.Marshal error, which cannot occur for these plain-struct events, and any
// error from a command method causes the caller to discard the aggregate
// without saving. This is documented rather than restructured: reordering to
// RaiseEvent-then-Apply would trade this theoretical edge for a worse one (an
// event recorded for a mutation that then fails to apply).
func (a *ContractAggregate) Apply(event eventstore.DomainEvent) error {
	switch e := event.(type) {
	case *ContractCreatedEvent:
		a.contractID = e.ContractID
		a.accountID = e.AccountID
		// Empty for historical events recorded before schema version 3 (the
		// key was never persisted); replay must tolerate that (issue #159).
		a.idempotencyKey = e.IdempotencyKey
		a.priceID = e.PriceID
		a.price = e.Price
		a.basePrice = e.BasePrice
		a.interval = e.Interval
		a.contractType = e.ContractType
		a.autoRenew = e.AutoRenew
		a.status = ContractStatusDraft
		a.createdAt = e.CreatedAt
		a.updatedAt = e.CreatedAt

	case *ContractActivatedEvent:
		a.status = ContractStatusActive
		a.currentPeriod = e.CurrentPeriod
		// Capture the billing anchor from the initial period's start day. This
		// is the original activation day-of-month and is never changed by later
		// renewals, so it survives month-end clamping (issue #186). Derived here
		// rather than stored on the event: the initial period already carries
		// the start, so historical streams reconstruct the anchor identically.
		a.billingAnchorDay = anchorDayFrom(e.CurrentPeriod)
		a.updatedAt = e.ActivatedAt

	case *ContractSuspendedEvent:
		a.status = ContractStatusSuspended
		// Reconstruct the FULL SuspensionConfiguration from the event. Earlier
		// this dropped SuspendedAt and ExtendContract (issue #194), so a
		// suspension configured with ExtendContract=true replayed as false and
		// SuspendedAt replayed as the zero time. Both are now carried on the
		// event and restored here, so live mutation and replay agree.
		//
		// Build the value form first (so cloneValue's deep-copy of ResumeDate
		// runs once, no extra construct-then-clone indirection) and take the
		// address of the resulting independent copy.
		cfg := SuspensionConfiguration{
			SuspendedAt:     e.SuspendedAt,
			BillingBehavior: e.BillingBehavior,
			ResumeDate:      e.ResumeDate,
			ExtendContract:  e.ExtendContract,
			Reason:          e.Reason,
		}.cloneValue()
		a.suspensionConfig = &cfg
		a.updatedAt = e.SuspendedAt

	case *ContractResumedEvent:
		// Honor ExtendContract (issue #194): when the suspension was configured
		// to extend the contract, push the current billing period's end out by
		// the suspension duration (resume time − suspended time). The suspension
		// config is still present at this point — during replay it was set by
		// the immediately-preceding ContractSuspendedEvent, and after a snapshot
		// taken while suspended it is restored by LoadFromSnapshot — so both
		// SuspendedAt and ExtendContract are available. The extension is thus
		// reconstructed deterministically from event data (ResumedAt) plus
		// replayed state, and ContractResumedEvent needs no extra field.
		if sc := a.suspensionConfig; sc != nil && sc.ExtendContract && !a.currentPeriod.IsZero() {
			if d := e.ResumedAt.Sub(sc.SuspendedAt); d > 0 {
				extended, err := shared.NewDateRange(a.currentPeriod.Start(), a.currentPeriod.End().Add(d))
				if err != nil {
					return err
				}
				a.currentPeriod = extended
			}
		}
		a.status = ContractStatusActive
		a.suspensionConfig = nil
		a.updatedAt = e.ResumedAt

	case *ContractCancelledEvent:
		a.status = ContractStatusCancelled
		a.cancelAtPeriodEnd = false
		// Clear pending/trial state on cancellation so a terminal contract does
		// not report HasPendingChange()==true or retain a live trial config
		// (issue #196). Cancelling directly from Trialing previously left
		// trialConfig set (only EndTrial cleared it), and a scheduled price change
		// left pendingPriceID set. Clearing here is replay-safe: it is
		// deterministic from the event and idempotent across re-applies.
		a.pendingPriceID = nil
		a.trialConfig = nil
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
		// Copy the value so the aggregate's pendingPriceID does not alias the
		// event field.
		pending := e.NewPriceID
		a.pendingPriceID = &pending // priceID unchanged
		a.updatedAt = e.ScheduledAt

	case *PriceChangeUnscheduledEvent:
		a.pendingPriceID = nil
		a.updatedAt = e.UnscheduledAt

	case *TrialStartedEvent:
		a.status = ContractStatusTrialing
		// Deep-copy the slice inside TrialConfiguration so the aggregate owns
		// its ConversionReminderDays independently of the event payload.
		cfg := e.TrialConfig.cloneValue()
		a.trialConfig = &cfg
		a.updatedAt = e.StartedAt

	case *TrialEndedEvent:
		a.trialConfig = nil
		if e.Converted {
			a.status = ContractStatusActive
			period := e.CurrentPeriod
			if period.IsZero() {
				// Legacy TrialEndedEvent (schema v1) carried no current_period.
				// Derive it deterministically from the billing interval —
				// recovered from the earlier ContractCreatedEvent in this same
				// replay — anchored at EndedAt, mirroring how Activate
				// establishes the initial period. This keeps replay
				// deterministic and prevents converted trials from silently
				// falling out of the renewal/billing cycle (issue #146).
				derived, err := shared.NewDateRange(e.EndedAt, a.interval.AddTo(e.EndedAt))
				if err != nil {
					return err
				}
				period = derived
			}
			a.currentPeriod = period
			// Anchor the billing day on the converted period's start, mirroring
			// ContractActivatedEvent (issue #186).
			a.billingAnchorDay = anchorDayFrom(period)
		} else {
			a.status = ContractStatusCancelled
		}
		a.updatedAt = e.EndedAt

	case *PaymentMethodChangedEvent:
		// Deep-copy the pointer so the aggregate owns its paymentMethodID
		// independently of the event payload.
		a.paymentMethodID = shared.PtrCopy(e.NewPaymentMethodID)
		a.updatedAt = e.ChangedAt

	case *ContractRenewedEvent:
		a.currentPeriod = e.NewPeriod
		a.priceID = e.NewPriceID
		a.pendingPriceID = nil
		if !e.NewInterval.IsZero() {
			a.interval = e.NewInterval
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

	case *ContractPastDueEvent:
		a.status = ContractStatusPastDue
		a.updatedAt = e.MarkedAt

	case *ContractRecoveredEvent:
		a.status = ContractStatusActive
		a.updatedAt = e.RecoveredAt

	default:
		return shared.NewDomainError(shared.ErrCodeUnknownEvent,
			fmt.Sprintf("unknown event type: %T", event))
	}

	return nil
}

// anchorDayFrom returns the billing anchor day-of-month derived from a period's
// start. It returns 0 for a zero-value period (no billing established), which
// AddToWithAnchorDay treats as "no anchor" and falls back to plain clamping.
func anchorDayFrom(period shared.DateRange) int {
	if period.IsZero() {
		return 0
	}
	return period.Start().Day()
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
		SchemaVersion:     contractSnapshotSchemaVersion,
		ContractID:        a.contractID,
		AccountID:         a.accountID,
		IdempotencyKey:    a.idempotencyKey,
		Status:            a.status,
		ContractType:      a.contractType,
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
		BillingAnchorDay:  a.billingAnchorDay,
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

// contractSnapshotSchemaVersion is the current schema version of
// contractSnapshotState. Version 2 dropped the deprecated billing_cycle field;
// the billing interval is now carried solely by the interval field. Snapshots
// written before that version (schema_version 0/1) stored only billing_cycle,
// and LoadFromSnapshot converts it to an interval on read. Version 3 added
// billing_anchor_day (issue #186); legacy snapshots (schema_version <= 2) omit
// it and LoadFromSnapshot derives it from the current period's start day.
const contractSnapshotSchemaVersion = 3

// contractSnapshotState is the JSON representation of aggregate state for snapshots.
type contractSnapshotState struct {
	SchemaVersion     int                      `json:"schema_version,omitempty"`
	ContractID        shared.ContractID        `json:"contract_id"`
	AccountID         shared.AccountID         `json:"account_id"`
	IdempotencyKey    string                   `json:"idempotency_key,omitempty"` // added with issue #159; empty in legacy snapshots
	Status            ContractStatus           `json:"status"`
	ContractType      ContractType             `json:"contract_type"`
	Interval          BillingInterval          `json:"interval,omitempty"` // Flexible billing interval
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
	BillingAnchorDay  int                      `json:"billing_anchor_day,omitempty"` // added in schema v3 (issue #186); 0 in legacy snapshots
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
	// Empty in snapshots written before issue #159 — tolerated, mirroring
	// replay of historical ContractCreatedEvent payloads without the key.
	a.idempotencyKey = state.IdempotencyKey
	a.status = state.Status
	a.contractType = state.ContractType
	if !state.Interval.IsZero() {
		a.interval = state.Interval
	} else {
		// Legacy snapshot (schema_version 0/1): the interval was stored only in
		// the now-removed billing_cycle field. Recover it from the raw payload
		// using the Strict converter so an unknown or absent cycle fails loudly
		// rather than silently coercing to Monthly — mirroring the event upcaster
		// policy (issue #162 L-4 / #196). A genuinely-valid legacy snapshot (one
		// of daily/weekly/monthly/yearly) still loads unchanged.
		var legacy struct {
			BillingCycle pricing.BillingCycle `json:"billing_cycle"`
		}
		if err := json.Unmarshal(snapshot.State, &legacy); err != nil {
			return fmt.Errorf("failed to unmarshal legacy snapshot billing_cycle: %w", err)
		}
		interval, ok := pricing.BillingCycleToIntervalStrict(legacy.BillingCycle)
		if !ok {
			return shared.NewDomainError(shared.ErrCodeValidation,
				fmt.Sprintf("legacy snapshot has unknown or absent billing_cycle %q (expected daily, weekly, monthly, or yearly)", legacy.BillingCycle))
		}
		a.interval = interval
	}
	a.currentPeriod = state.CurrentPeriod
	// Deep-copy pointer fields so the aggregate does not alias the local
	// snapshot state struct (defense against future refactors that may retain
	// or reuse the state value).
	a.trialConfig = state.TrialConfig.clone()
	a.suspensionConfig = state.SuspensionConfig.clone()
	a.paymentMethodID = shared.PtrCopy(state.PaymentMethodID)
	a.priceID = state.PriceID
	a.price = state.Price
	a.basePrice = state.BasePrice
	a.autoRenew = state.AutoRenew
	a.cancelAtPeriodEnd = state.CancelAtPeriodEnd
	a.pendingPriceID = shared.PtrCopy(state.PendingPriceID)
	// Legacy snapshots (schema_version <= 2, issue #186) have no
	// billing_anchor_day. Fall back to the current period's start day so the
	// anchor is still honored on the next renewal — a best-effort reconstruction
	// that degrades to clamping-only for an already-drifted period. New
	// snapshots carry the true anchor.
	a.billingAnchorDay = state.BillingAnchorDay
	if a.billingAnchorDay == 0 {
		a.billingAnchorDay = anchorDayFrom(state.CurrentPeriod)
	}
	a.createdAt = state.CreatedAt
	a.updatedAt = state.UpdatedAt
	a.SetVersion(snapshot.Version)

	return nil
}
