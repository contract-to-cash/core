package contract

// ChangePolicy defines when a price/plan change takes effect.
type ChangePolicy string

const (
	// ChangePolicyImmediate applies the change immediately.
	// Proration may be calculated for the current period.
	ChangePolicyImmediate ChangePolicy = "immediate"

	// ChangePolicyEndOfTerm defers the change to the next renewal.
	// The current period continues at the current price.
	ChangePolicyEndOfTerm ChangePolicy = "end_of_term"
)
