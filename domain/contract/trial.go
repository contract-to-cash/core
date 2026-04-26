package contract

import "time"

// TrialConfiguration holds configuration for a contract trial period.
type TrialConfiguration struct {
	TrialEndDate           time.Time `json:"trial_end_date"`
	AutoConvert            bool      `json:"auto_convert"`
	RequirePaymentMethod   bool      `json:"require_payment_method"`
	ConversionReminderDays []int     `json:"conversion_reminder_days"`
}

// clone returns a deep copy of the configuration, including the
// ConversionReminderDays slice. Returns nil if the receiver is nil.
func (c *TrialConfiguration) clone() *TrialConfiguration {
	if c == nil {
		return nil
	}
	cp := *c
	if c.ConversionReminderDays != nil {
		cp.ConversionReminderDays = append([]int(nil), c.ConversionReminderDays...)
	}
	return &cp
}

// cloneValue returns a deep copy of the configuration by value.
// Use when the caller holds a value (not a pointer) and needs intake defense.
func (c TrialConfiguration) cloneValue() TrialConfiguration {
	cp := c
	if c.ConversionReminderDays != nil {
		cp.ConversionReminderDays = append([]int(nil), c.ConversionReminderDays...)
	}
	return cp
}
