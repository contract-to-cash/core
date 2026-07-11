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

// MetricName identifies a usage metric (e.g. "api_calls", "storage_gb").
// Using a typed string prevents accidental mixing with other string fields.
type MetricName string

func (m MetricName) String() string { return string(m) }

// ulidEntropy is the process-wide entropy source for generateULID.
//
// It is a concurrency-safe (mutex-locked) MONOTONIC reader over crypto/rand:
// within the same millisecond, each successive ULID's entropy component is a
// strictly-increasing increment over the previous one, so creation order ==
// lexicographic order even for IDs minted in the same millisecond (issue #197
// follow-up). A plain crypto/rand reader — the previous implementation — gave
// two same-millisecond ULIDs a RANDOM relative order, which broke the
// documented "IDs are sortable by creation order" guarantee exactly when it
// matters (tight loops, batch generation, fast CI machines).
//
// crypto/rand is retained as the base entropy (rather than switching to
// ulid.Make()/DefaultEntropy, whose entropy is a time-seeded math/rand PRNG) so
// IDs stay unpredictable; Monotonic only constrains ordering within a
// millisecond, adding a bounded random increment per ID. The inc=0 argument
// selects the library default (random increments up to math.MaxUint32), whose
// 80-bit entropy space makes same-millisecond overflow practically impossible.
var ulidEntropy = &ulid.LockedMonotonicReader{
	MonotonicReader: ulid.Monotonic(rand.Reader, 0),
}

// generateULID builds a new ULID string.
//
// Ordering guarantee: ULIDs from this function are strictly increasing in
// creation order, INCLUDING within the same millisecond — the timestamp
// component orders across milliseconds and the monotonic entropy source
// (ulidEntropy) orders within one. Code may therefore rely on "greater ID =>
// created later" for IDs minted by this process (e.g. the latest-voided-invoice
// selection in BillingService.RegenerateInvoice).
//
// This is the single deliberate exception to the project-wide "no time.Now();
// always go through shared.Clock" rule. The time value here is only the ULID
// timestamp component, which exists to make IDs lexicographically sortable by
// creation order; it is never used for domain time logic, comparisons, or
// business decisions, so it does not need to be injectable/mockable. Keeping it
// as a direct time.Now() call avoids threading a Clock through every
// NewXxxID() constructor and every call site that generates an ID.
func generateULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulidEntropy).String()
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
