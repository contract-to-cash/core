package balance

// BalancePolicy defines how credits are handled for downgrades and cancellations.
type BalancePolicy string

const (
	BalancePolicyLedger BalancePolicy = "ledger"
	BalancePolicyRefund BalancePolicy = "refund"
	BalancePolicyNone   BalancePolicy = "none"
)

// BalanceConfig holds the configuration for credit handling.
type BalanceConfig struct {
	DowngradePolicy    BalancePolicy
	CancellationPolicy BalancePolicy
	AllowManualRefund  bool
	ExpirationDays     int
}
