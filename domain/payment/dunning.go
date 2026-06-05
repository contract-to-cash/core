package payment

import "time"

// Dunning configuration is a CONSUMER-FACING SCHEMA, not an engine.
//
// The library deliberately does not run an automatic dunning loop: scheduling
// (cron, Cloud Scheduler, k8s CronJob, ...) is the consumer's responsibility
// (see design-decisions.md §3.2 — "OSS defines the interface, the scheduler
// lives on the service side"). These types let a consumer declare their retry
// cadence and per-attempt actions and drive them against the contract state
// machine: on payment failure call ContractAggregate.MarkPastDue, on recovery
// call RecoverFromPastDue, and on exhaustion Suspend or Cancel — mapping to the
// DunningActionType values below. Nothing in domain/ or application/ consumes
// these structs today; they are a stable schema for that consumer-side loop.

// DunningActionType represents the type of action to take during dunning.
type DunningActionType string

const (
	DunningActionRetry           DunningActionType = "retry"
	DunningActionNotify          DunningActionType = "notify"
	DunningActionSuspendContract DunningActionType = "suspend_contract"
	DunningActionCancelContract  DunningActionType = "cancel_contract"
)

// DunningAction represents a single action in a dunning sequence.
type DunningAction struct {
	AttemptNumber int
	Action        DunningActionType
	Template      string
}

// DunningConfig holds the configuration for dunning behavior.
type DunningConfig struct {
	MaxRetries     int
	RetryIntervals []time.Duration
	Actions        []DunningAction
}
