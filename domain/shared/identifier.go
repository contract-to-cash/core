package shared

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// AccountID identifies an account.
type AccountID string

// ContractID identifies a contract.
type ContractID string

// InvoiceID identifies an invoice.
type InvoiceID string

// PaymentID identifies a payment.
type PaymentID string

// UsageRecordID identifies a usage record.
type UsageRecordID string

// PlanID identifies a pricing plan.
type PlanID string

// CreditEntryID identifies a credit entry.
type CreditEntryID string

func (id AccountID) String() string     { return string(id) }
func (id ContractID) String() string    { return string(id) }
func (id InvoiceID) String() string     { return string(id) }
func (id PaymentID) String() string     { return string(id) }
func (id UsageRecordID) String() string { return string(id) }
func (id PlanID) String() string        { return string(id) }
func (id CreditEntryID) String() string { return string(id) }

func generateULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NewAccountID generates a new unique AccountID.
func NewAccountID() AccountID { return AccountID(generateULID()) }

// NewContractID generates a new unique ContractID.
func NewContractID() ContractID { return ContractID(generateULID()) }

// NewInvoiceID generates a new unique InvoiceID.
func NewInvoiceID() InvoiceID { return InvoiceID(generateULID()) }

// NewPaymentID generates a new unique PaymentID.
func NewPaymentID() PaymentID { return PaymentID(generateULID()) }

// NewUsageRecordID generates a new unique UsageRecordID.
func NewUsageRecordID() UsageRecordID { return UsageRecordID(generateULID()) }

// NewPlanID generates a new unique PlanID.
func NewPlanID() PlanID { return PlanID(generateULID()) }

// NewCreditEntryID generates a new unique CreditEntryID.
func NewCreditEntryID() CreditEntryID { return CreditEntryID(generateULID()) }

// GenerateID generates a new unique ID string (for event IDs etc.).
func GenerateID() string {
	return fmt.Sprintf("evt_%s", generateULID())
}
