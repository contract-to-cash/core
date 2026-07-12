package contract

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
)

// Compile-time assertions that the schema-versioned events self-declare their
// current schema version (so RaiseEvent stamps them with it, not v1). These are
// the events with a corresponding Upcaster in upcaster.go. ContractCreatedEvent
// is at v4 (metadata, issue #219); the others are at v2.
var (
	_ eventstore.SchemaVersioned = (*ContractCreatedEvent)(nil)
	_ eventstore.SchemaVersioned = (*PriceChangedEvent)(nil)
	_ eventstore.SchemaVersioned = (*TrialEndedEvent)(nil)
	_ eventstore.SchemaVersioned = (*ContractRenewedEvent)(nil)
	_ eventstore.SchemaVersioned = (*ContractSuspendedEvent)(nil)
)

// Event type constants.
const (
	EventTypeContractCreated         eventstore.EventType = "contract.created"
	EventTypeContractActivated       eventstore.EventType = "contract.activated"
	EventTypeContractSuspended       eventstore.EventType = "contract.suspended"
	EventTypeContractResumed         eventstore.EventType = "contract.resumed"
	EventTypeContractCancelled       eventstore.EventType = "contract.cancelled"
	EventTypePriceChanged            eventstore.EventType = "contract.price_changed"
	EventTypeTrialStarted            eventstore.EventType = "contract.trial_started"
	EventTypeTrialEnded              eventstore.EventType = "contract.trial_ended"
	EventTypePaymentMethodChanged    eventstore.EventType = "contract.payment_method_changed"
	EventTypeContractRenewed         eventstore.EventType = "contract.renewed"
	EventTypeContractExpired         eventstore.EventType = "contract.expired"
	EventTypeCancellationScheduled   eventstore.EventType = "contract.cancellation_scheduled"
	EventTypeCancellationUnscheduled eventstore.EventType = "contract.cancellation_unscheduled"
	EventTypePriceChangeScheduled    eventstore.EventType = "contract.price_change_scheduled"
	EventTypePriceChangeUnscheduled  eventstore.EventType = "contract.price_change_unscheduled"
	EventTypeContractPastDue         eventstore.EventType = "contract.past_due"
	EventTypeContractRecovered       eventstore.EventType = "contract.recovered"
)

// ContractCreatedEvent is raised when a new contract is created.
//
// IdempotencyKey carries the caller-supplied creation idempotency key
// (required by ContractAggregate.Create since issue #159) so that
// persistence adapters can enforce at-most-once contract creation with a
// unique index — see the uniqueness note on Repository.Save. It was added in
// SchemaVersion 3; historical v1/v2 payloads have no idempotency_key and
// deserialize with an empty string, which Apply tolerates (replay of
// pre-#159 history must never fail).
//
// Metadata carries optional integrator-defined key-value pairs (e.g.
// "creator_id", issue #219). It was added in SchemaVersion 4; historical
// v1/v2/v3 payloads have no metadata and deserialize with a nil map, which
// Apply tolerates (replay of pre-#219 history must never fail).
type ContractCreatedEvent struct {
	ContractID     shared.ContractID `json:"contract_id"`
	AccountID      shared.AccountID  `json:"account_id"`
	PriceID        shared.PriceID    `json:"price_id"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"` // added in schema v3 (issue #159)
	Price          shared.Money      `json:"price"`
	Interval       BillingInterval   `json:"interval,omitempty"` // Flexible billing interval
	ContractType   ContractType      `json:"contract_type"`
	BasePrice      shared.Money      `json:"base_price"`
	AutoRenew      bool              `json:"auto_renew"`
	Metadata       map[string]string `json:"metadata,omitempty"` // added in schema v4 (issue #219)
	CreatedAt      time.Time         `json:"created_at"`
}

func (e *ContractCreatedEvent) EventType() eventstore.EventType { return EventTypeContractCreated }

// CurrentSchemaVersion reports that the current ContractCreatedEvent payload is
// schema version 4 (carries metadata, issue #219). Version 3 added
// idempotency_key (issue #159); version 2 was the interval-based shape
// (post-#111). ContractCreatedEventUpcaster migrates legacy v1 payloads
// (billing_cycle-only) to the v2 shape, ContractCreatedIdempotencyKeyUpcaster
// marks v2 payloads v3 (a missing idempotency_key deserializes to "", which
// Apply tolerates), and ContractCreatedMetadataUpcaster marks v3 payloads v4
// (a missing metadata deserializes to nil, which Apply tolerates).
func (e *ContractCreatedEvent) CurrentSchemaVersion() int { return 4 }

// ContractActivatedEvent is raised when a contract is activated.
type ContractActivatedEvent struct {
	ContractID    shared.ContractID `json:"contract_id"`
	ActivatedAt   time.Time         `json:"activated_at"`
	CurrentPeriod shared.DateRange  `json:"current_period"`
}

func (e *ContractActivatedEvent) EventType() eventstore.EventType { return EventTypeContractActivated }

// ContractSuspendedEvent is raised when a contract is suspended.
//
// ExtendContract and SuspendedAt were promoted onto the event in SchemaVersion 2
// (issue #194). Previously ExtendContract lived only on the caller's
// SuspensionConfiguration and was silently dropped through the event round-trip
// (Apply reconstructed the config from BillingBehavior/ResumeDate/Reason only),
// and SuspendedAt, though present on the event, was never copied back into the
// replayed SuspensionConfiguration. Both are now carried on the event and fully
// reconstructed by Apply so a suspension's ExtendContract flag and suspend
// timestamp survive replay and snapshotting.
//
// SuspendedAt additionally serves as a reconstruction input: Resume extends the
// current billing period by (resume time − SuspendedAt) when ExtendContract is
// true, and that computation must be deterministic on replay.
//
// Schema: ContractSuspendedEventUpcaster marks legacy v1 payloads v2. Legacy
// defaults are extend_contract=false (pre-#194 suspensions never extended the
// period) and, defensively, suspended_at falls back to the event's OccurredAt
// when absent or zero (real v1 payloads always carried suspended_at, but a zero
// anchor would corrupt the resume-extension math).
type ContractSuspendedEvent struct {
	ContractID      shared.ContractID         `json:"contract_id"`
	SuspendedAt     time.Time                 `json:"suspended_at"`
	BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
	ResumeDate      *time.Time                `json:"resume_date,omitempty"`
	ExtendContract  bool                      `json:"extend_contract"`
	Reason          string                    `json:"reason"`
}

func (e *ContractSuspendedEvent) EventType() eventstore.EventType { return EventTypeContractSuspended }

// CurrentSchemaVersion reports that the current ContractSuspendedEvent payload is
// schema version 2 (carries extend_contract and a fully-reconstructed
// suspended_at, issue #194). ContractSuspendedEventUpcaster marks legacy v1
// payloads v2.
func (e *ContractSuspendedEvent) CurrentSchemaVersion() int { return 2 }

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

// CurrentSchemaVersion reports that the current PriceChangedEvent payload is
// schema version 2 (PriceID-based with a Policy field). PriceChangedEventUpcaster
// migrates legacy v1 payloads (Money-based, no PriceID/Policy) up to this shape.
func (e *PriceChangedEvent) CurrentSchemaVersion() int { return 2 }

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

// TrialStartedEvent is raised when a trial period starts.
type TrialStartedEvent struct {
	ContractID  shared.ContractID  `json:"contract_id"`
	TrialConfig TrialConfiguration `json:"trial_config"`
	StartedAt   time.Time          `json:"started_at"`
}

func (e *TrialStartedEvent) EventType() eventstore.EventType { return EventTypeTrialStarted }

// TrialEndedEvent is raised when a trial period ends.
//
// CurrentPeriod carries the initial billing period established when a trial
// converts (Converted=true), mirroring ContractActivatedEvent.CurrentPeriod.
// Without it a converted contract would be Active with a zero-value period and
// would silently fall out of the renewal/billing cycle (issue #146). It is the
// zero value when Converted=false (the contract is cancelled, no billing).
//
// Schema: added in SchemaVersion 2. Historical v1 payloads have no
// current_period; TrialEndedEventUpcaster marks them v2 and the aggregate's
// Apply derives the period from the billing interval anchored at EndedAt (the
// interval is not carried on this event, only on the earlier
// ContractCreatedEvent), so replay is deterministic.
type TrialEndedEvent struct {
	ContractID    shared.ContractID `json:"contract_id"`
	EndedAt       time.Time         `json:"ended_at"`
	Converted     bool              `json:"converted"`
	CurrentPeriod shared.DateRange  `json:"current_period"`
}

func (e *TrialEndedEvent) EventType() eventstore.EventType { return EventTypeTrialEnded }

// CurrentSchemaVersion reports that the current TrialEndedEvent payload is schema
// version 2 (carries current_period, added in issue #146). TrialEndedEventUpcaster
// marks legacy v1 payloads v2 so the aggregate's Apply derives the missing period.
func (e *TrialEndedEvent) CurrentSchemaVersion() int { return 2 }

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
	ContractID   shared.ContractID `json:"contract_id"`
	OldPeriod    shared.DateRange  `json:"old_period"`
	NewPeriod    shared.DateRange  `json:"new_period"`
	OldPriceID   shared.PriceID    `json:"old_price_id"`
	NewPriceID   shared.PriceID    `json:"new_price_id"`
	PriceChanged bool              `json:"price_changed"`
	OldInterval  BillingInterval   `json:"old_interval,omitempty"` // Previous billing interval
	NewInterval  BillingInterval   `json:"new_interval,omitempty"` // New billing interval
	RenewedAt    time.Time         `json:"renewed_at"`
}

func (e *ContractRenewedEvent) EventType() eventstore.EventType { return EventTypeContractRenewed }

// CurrentSchemaVersion reports that the current ContractRenewedEvent payload is
// schema version 2 (interval-based old_interval/new_interval, post-#111).
// ContractRenewedEventUpcaster migrates legacy v1 payloads (billing_cycle-only)
// up to this shape.
func (e *ContractRenewedEvent) CurrentSchemaVersion() int { return 2 }

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

// ContractPastDueEvent is raised when an active contract enters the past_due
// state, typically after a payment failure drives dunning.
type ContractPastDueEvent struct {
	ContractID shared.ContractID `json:"contract_id"`
	Reason     string            `json:"reason"`
	MarkedAt   time.Time         `json:"marked_at"`
}

func (e *ContractPastDueEvent) EventType() eventstore.EventType { return EventTypeContractPastDue }

// ContractRecoveredEvent is raised when a past_due contract returns to active,
// typically after a successful payment.
type ContractRecoveredEvent struct {
	ContractID  shared.ContractID `json:"contract_id"`
	RecoveredAt time.Time         `json:"recovered_at"`
}

func (e *ContractRecoveredEvent) EventType() eventstore.EventType { return EventTypeContractRecovered }
