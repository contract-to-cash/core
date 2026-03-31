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

// ProductID identifies a product.
type ProductID string

// PriceID identifies a price.
type PriceID string

// BalanceEntryID identifies a balance entry (account credit/debit).
type BalanceEntryID string

// CreditNoteID identifies a credit note.
type CreditNoteID string

func (id AccountID) String() string      { return string(id) }
func (id ContractID) String() string     { return string(id) }
func (id InvoiceID) String() string      { return string(id) }
func (id PaymentID) String() string      { return string(id) }
func (id UsageRecordID) String() string  { return string(id) }
func (id ProductID) String() string      { return string(id) }
func (id PriceID) String() string        { return string(id) }
func (id BalanceEntryID) String() string { return string(id) }
func (id CreditNoteID) String() string   { return string(id) }

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

// NewProductID generates a new unique ProductID.
func NewProductID() ProductID { return ProductID(generateULID()) }

// NewPriceID generates a new unique PriceID.
func NewPriceID() PriceID { return PriceID(generateULID()) }

// NewBalanceEntryID generates a new unique BalanceEntryID.
func NewBalanceEntryID() BalanceEntryID { return BalanceEntryID(generateULID()) }

// NewCreditNoteID generates a new unique CreditNoteID.
func NewCreditNoteID() CreditNoteID { return CreditNoteID(generateULID()) }

// GenerateID generates a new unique ID string (for event IDs etc.).
func GenerateID() string {
	return fmt.Sprintf("evt_%s", generateULID())
}
