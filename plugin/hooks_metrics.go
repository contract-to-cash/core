package plugin

import (
	"time"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
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
type OnInvoiceIssuedHook interface {
	Plugin
	OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

// OnPaymentProcessedHook is called when a payment is processed, for metrics/analytics.
type OnPaymentProcessedHook interface {
	Plugin
	OnPaymentProcessed(ctx *Context, payment *payment.Payment) error
}
