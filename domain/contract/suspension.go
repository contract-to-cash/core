package contract

import "time"

// SuspensionBillingBehavior defines how billing is handled during suspension.
type SuspensionBillingBehavior string

const (
	SuspensionBillingSkip     SuspensionBillingBehavior = "skip"
	SuspensionBillingDefer    SuspensionBillingBehavior = "defer"
	SuspensionBillingContinue SuspensionBillingBehavior = "continue"
)

// SuspensionConfiguration holds configuration for a contract suspension.
type SuspensionConfiguration struct {
	SuspendedAt     time.Time                `json:"suspended_at"`
	ResumeDate      *time.Time               `json:"resume_date,omitempty"`
	BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
	ExtendContract  bool                     `json:"extend_contract"`
	Reason          string                   `json:"reason"`
}
