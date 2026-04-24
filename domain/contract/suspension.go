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
	SuspendedAt     time.Time                 `json:"suspended_at"`
	ResumeDate      *time.Time                `json:"resume_date,omitempty"`
	BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
	ExtendContract  bool                      `json:"extend_contract"`
	Reason          string                    `json:"reason"`
}

// clone returns a deep copy of the configuration, including the ResumeDate
// pointer. Returns nil if the receiver is nil.
func (c *SuspensionConfiguration) clone() *SuspensionConfiguration {
	if c == nil {
		return nil
	}
	cp := *c
	if c.ResumeDate != nil {
		t := *c.ResumeDate
		cp.ResumeDate = &t
	}
	return &cp
}

// cloneValue returns a deep copy of the configuration by value.
// Use when the caller holds a value (not a pointer) and needs intake defense.
func (c SuspensionConfiguration) cloneValue() SuspensionConfiguration {
	cp := c
	if c.ResumeDate != nil {
		t := *c.ResumeDate
		cp.ResumeDate = &t
	}
	return cp
}
