package payment

import "time"

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
