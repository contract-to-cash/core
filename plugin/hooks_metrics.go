package plugin

import (
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// ContractChangeType describes the kind of change that occurred on a contract.
type ContractChangeType string

const (
	ContractChangeCreated   ContractChangeType = "created"
	ContractChangeActivated ContractChangeType = "activated"
	ContractChangeSuspended ContractChangeType = "suspended"
	ContractChangeResumed   ContractChangeType = "resumed"
	ContractChangeCancelled ContractChangeType = "cancelled"
	ContractChangeRenewed   ContractChangeType = "renewed"
	ContractChangeTrialEnd  ContractChangeType = "trial_end"
	// ContractChangeExpired marks a contract that reached its natural term end
	// with autoRenew=false and transitioned to Expired. It is distinct from
	// ContractChangeCancelled so churn metrics do not conflate voluntary
	// cancellation with natural expiry (issue #162 B3). Note that a scheduled
	// cancellation (cancelAtPeriodEnd) transitions the contract to Cancelled at
	// renewal time and is therefore reported as ContractChangeCancelled, not
	// Expired — it is user-initiated churn, even though it takes effect at the
	// period boundary.
	ContractChangeExpired ContractChangeType = "expired"
)

// ContractChangeEvent carries information about a contract change for metrics hooks.
type ContractChangeEvent struct {
	ContractID shared.ContractID
	ChangeType ContractChangeType
	OldStatus  *contract.ContractStatus
	NewStatus  *contract.ContractStatus
	OldPriceID *shared.PriceID
	NewPriceID *shared.PriceID
	MRRChange  *shared.Money
	Timestamp  time.Time
}

// OnContractChangeHook is called when a contract changes for metrics/analytics purposes.
type OnContractChangeHook interface {
	Plugin
	OnContractChange(ctx *Context, event ContractChangeEvent) error
}

// OnInvoiceIssuedHook is called when an invoice is issued, for metrics/analytics.
// Implementations must be idempotent (deduplicate by invoice ID): retries and
// idempotent-replay convergence can deliver the same invoice more than once.
type OnInvoiceIssuedHook interface {
	Plugin
	OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

// OnPaymentProcessedHook is called when a payment is processed, for metrics/analytics.
// It receives the same *PaymentContext as the other payment hooks (issue #223):
// ctx.Payment() is the processed payment, and ctx.Invoice() / ctx.ContractID() /
// ctx.AccountID() let implementations attribute the payment to a contract and
// account without extra repository lookups.
// Implementations must be idempotent (deduplicate by payment ID): concurrent
// requests converging on the same idempotency key can deliver the same
// payment more than once.
type OnPaymentProcessedHook interface {
	Plugin
	OnPaymentProcessed(ctx *PaymentContext) error
}
