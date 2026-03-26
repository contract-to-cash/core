package credit

// CreditPolicy defines how credits are handled for downgrades and cancellations.
type CreditPolicy string

const (
	CreditPolicyLedger CreditPolicy = "ledger"
	CreditPolicyRefund CreditPolicy = "refund"
	CreditPolicyNone   CreditPolicy = "none"
)

// CreditConfig holds the configuration for credit handling.
type CreditConfig struct {
	DowngradePolicy    CreditPolicy
	CancellationPolicy CreditPolicy
	AllowManualRefund  bool
	ExpirationDays     int
}
