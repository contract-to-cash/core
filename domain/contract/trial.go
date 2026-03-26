package contract

import "time"

// TrialConfiguration holds configuration for a contract trial period.
type TrialConfiguration struct {
	TrialEndDate           time.Time `json:"trial_end_date"`
	AutoConvert            bool      `json:"auto_convert"`
	RequirePaymentMethod   bool      `json:"require_payment_method"`
	ConversionReminderDays []int     `json:"conversion_reminder_days"`
}
