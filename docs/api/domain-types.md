---
sidebar_position: 1
---

# Domain Types Reference

## Shared Value Objects

### Money

Arbitrary-precision monetary value with currency.

```go
import "github.com/contract-to-cash/core/domain/shared"

// Construction
price := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
zero := shared.Zero(shared.CurrencyJPY)

// Arithmetic (returns error on currency mismatch)
sum, err := a.Add(b)
diff, err := a.Subtract(b)
product := a.Multiply(new(big.Rat).SetFrac64(10, 100))
negated := a.Negate()

// Comparison
a.IsZero()
a.IsNegative()
a.GreaterThan(b)
min, err := a.Min(b)

// Access
a.Amount()   // *big.Rat
a.Currency() // Currency
```

**Currencies**: `CurrencyJPY`, `CurrencyUSD`, `CurrencyEUR`

### DateRange

Half-open interval `[start, end)`.

```go
period, err := shared.NewDateRange(start, end) // error if start >= end
period.Start()
period.End()
period.Contains(t time.Time) bool
period.Duration() time.Duration
```

### ID Types

ULID-based identifiers:

| Type | Constructor |
|------|-------------|
| `ContractID` | `NewContractID()` |
| `AccountID` | `NewAccountID()` |
| `InvoiceID` | `NewInvoiceID()` |
| `PaymentID` | `NewPaymentID()` |
| `ProductID` | `NewProductID()` |
| `PriceID` | `NewPriceID()` |
| `UsageRecordID` | `NewUsageRecordID()` |
| `BalanceEntryID` | `NewBalanceEntryID()` |
| `CreditNoteID` | `NewCreditNoteID()` |

### Clock

```go
type Clock interface {
    Now() time.Time
}

// Implementations
shared.SystemClock{}                                    // Real time
shared.FixedClock{FixedTime: time.Date(2026, 4, 1, ...)} // Fixed time (testing)
```

### DomainError

```go
type ErrorCode string

const (
    ErrCodeInvalidStateTransition ErrorCode = "invalid_state_transition"
    ErrCodeBusinessRule           ErrorCode = "business_rule_violation"
    ErrCodeValidation             ErrorCode = "validation_error"
    ErrCodeNotFound               ErrorCode = "not_found"
    ErrCodeConflict               ErrorCode = "conflict"
    ErrCodeDuplicateRequest       ErrorCode = "duplicate_request"
    ErrCodeVersionConflict        ErrorCode = "version_conflict"
    ErrCodeCurrencyMismatch       ErrorCode = "currency_mismatch"
    ErrCodeInvalidDateRange       ErrorCode = "invalid_date_range"
    ErrCodeUnknownEvent           ErrorCode = "unknown_event"
)

shared.NewDomainError(code ErrorCode, message string) *DomainError
```

---

## Contract

### ContractAggregate

```go
import "github.com/contract-to-cash/core/domain/contract"

agg := contract.NewContractAggregate(contractID, clock)
```

**Status constants**: `ContractStatusDraft`, `ContractStatusTrialing`, `ContractStatusActive`, `ContractStatusPastDue`, `ContractStatusSuspended`, `ContractStatusCancelled`, `ContractStatusExpired`

**Type constants**: `ContractTypeOneTime`, `ContractTypeSubscription`, `ContractTypeUsageBased`

**Billing interval**: use `pricing.BillingInterval` (`{unit, count}`). Convenience constructors: `pricing.Daily()`, `pricing.Weekly()`, `pricing.Monthly()`, `pricing.Yearly()`, `pricing.Quarterly()`, `pricing.SemiAnnual()`. The `pricing.BillingCycle` string constants (`BillingCycleDaily/Weekly/Monthly/Yearly`) remain in the `pricing` package for `Price` construction, display, and adapter code, but the contract domain no longer surfaces them (removed in #111).

#### Commands

| Method | From Status | To Status |
|--------|------------|-----------|
| `Create(cmd, metadata)` | (new) | draft |
| `Activate(metadata)` | draft, trialing | active |
| `StartTrial(config, metadata)` | draft | trialing |
| `EndTrial(converted, metadata)` | trialing | active/cancelled |
| `Suspend(config, metadata)` | active, past_due | suspended |
| `Resume(metadata)` | suspended | active |
| `Cancel(reason, metadata)` | draft, trialing, active, suspended, past_due | cancelled |
| `RenewWithInterval(newInterval BillingInterval, metadata)` | active | active (new period) |
| `ChangePrice(priceID, policy, proration, metadata)` | active | active |
| `UnscheduleChange(reason, metadata)` | active (has pending) | active |

#### CreateContractCommand

```go
type CreateContractCommand struct {
    AccountID      shared.AccountID
    PriceID        shared.PriceID
    ContractType   ContractType
    Interval       BillingInterval
    Price          shared.Money
    BasePrice      shared.Money
    AutoRenew      bool
    IdempotencyKey string
}
```

#### SuspensionConfiguration

```go
type SuspensionConfiguration struct {
    SuspendedAt     time.Time
    ResumeDate      *time.Time
    BillingBehavior SuspensionBillingBehavior // Skip, Defer, Continue
    ExtendContract  bool
    Reason          string
}
```

#### ChangePolicy

```go
const (
    ChangePolicyImmediate  ChangePolicy = "immediate"
    ChangePolicyEndOfTerm  ChangePolicy = "end_of_term"
)
```

#### Getters

```go
agg.ContractID() shared.ContractID
agg.AccountID() shared.AccountID
agg.Status() ContractStatus
agg.GetContractType() ContractType
agg.GetInterval() BillingInterval
agg.CurrentPeriod() shared.DateRange
agg.TrialConfig() *TrialConfiguration
agg.SuspensionConfig() *SuspensionConfiguration
agg.PaymentMethodID() *string
agg.PriceID() shared.PriceID
agg.Price() shared.Money
agg.BasePrice() shared.Money
agg.AutoRenew() bool
agg.CancelAtPeriodEnd() bool
agg.PendingPriceID() *shared.PriceID
agg.HasPendingChange() bool
agg.CreatedAt() time.Time
agg.UpdatedAt() time.Time
```

#### Event Sourcing

```go
agg.LoadFromHistory(events []eventstore.Event) error
agg.LoadFromSnapshot(snapshot eventstore.Snapshot) error
agg.MarshalSnapshot() ([]byte, error)
agg.UncommittedEvents() []eventstore.Event
agg.ClearUncommittedEvents()
agg.Version() int
```

### Repository

```go
type Repository interface {
    Save(ctx context.Context, aggregate *ContractAggregate) error
    FindByID(ctx context.Context, id shared.ContractID) (*ContractAggregate, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*ContractAggregate, error)
    FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindTrialsEndingBefore(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindByIDAsOf(ctx context.Context, id shared.ContractID, asOf time.Time) (*ContractAggregate, error)
    FindDueForRenewal(ctx context.Context, asOf time.Time) ([]*ContractAggregate, error)
}
```

---

## Invoice

```go
import "github.com/contract-to-cash/core/domain/invoice"
```

**Status constants**: `InvoiceStatusDraft`, `InvoiceStatusFinalized`, `InvoiceStatusIssued`, `InvoiceStatusPaid`, `InvoiceStatusPartialPaid`, `InvoiceStatusOverdue`, `InvoiceStatusVoided`, `InvoiceStatusRefunded`

### Construction

```go
inv := invoice.NewInvoice(id, accountID, contractID, subtotal, discountAmount, taxAmount,
    invoice.WithLineItems(items),
    invoice.WithBillingPeriod(period),
    invoice.WithDueDate(dueDate),
    invoice.WithStatus(invoice.InvoiceStatusDraft),
    invoice.WithAppliedBalance(creditAmount),
    invoice.WithAmountDue(amountDue),
    invoice.WithAllowPartialPayment(false),
    invoice.WithInvoiceNumber("INV-2026-001"),
    invoice.WithOriginalInvoiceID(originalID),   // for reissued invoices
    invoice.WithRevisionOf(parentID),             // for revision chain linking
    invoice.WithPaymentMethodID(&pmID),           // invoice-level payment method override
)
```

### Methods

```go
inv.Finalize() error           // draft → finalized
inv.MarkIssued() error         // finalized → issued (delivery flows; integrator fires)
inv.MarkOverdue(now time.Time) error // finalized|issued → overdue (now strictly after due date)
inv.Void() error               // draft|finalized → voided
inv.VoidWithReason(reason string) error  // any except voided|refunded → voided (with reason)
inv.ValidatePayment(amount shared.Money) error    // finalized|issued|partial_paid|overdue → (validates payment amount)
inv.RecordPayment(amount shared.Money, paidAt time.Time) error // records payment, updates balance and status
inv.SetRevisionOf(id shared.InvoiceID)            // sets direct parent in revision chain
inv.SetOriginalInvoiceID(id shared.InvoiceID)     // sets root of revision chain

inv.ID() shared.InvoiceID
inv.InvoiceNumber() string
inv.AccountID() shared.AccountID
inv.ContractID() shared.ContractID
inv.LineItems() []LineItem
inv.Subtotal() shared.Money
inv.DiscountAmount() shared.Money
inv.TaxAmount() shared.Money
inv.Total() shared.Money
inv.AppliedBalance() shared.Money
inv.AmountDue() shared.Money
inv.PaidAmount() shared.Money
inv.Balance() shared.Money
inv.Status() InvoiceStatus
inv.BillingPeriod() shared.DateRange
inv.IssueDate() time.Time
inv.DueDate() time.Time
inv.PaidAt() *time.Time
inv.AllowPartialPay() bool
inv.PaymentMethodID() *string
inv.OriginalInvoiceID() *shared.InvoiceID  // root of the revision chain
inv.RevisionOf() *shared.InvoiceID         // direct parent in the revision chain
inv.VoidReason() string
inv.Metadata() map[string]string
```

### Revision Support

Invoices support a two-level linking model for void-and-recreate workflows:

- **`originalInvoiceID`** — always points to the first invoice in the revision chain (the root), regardless of how many revisions have occurred.
- **`revisionOf`** — points to the direct parent (the immediate predecessor in the chain).

Example: `Inv-1 -> Inv-2 -> Inv-3`
- `Inv-2`: `originalInvoiceID=Inv-1`, `revisionOf=Inv-1`
- `Inv-3`: `originalInvoiceID=Inv-1`, `revisionOf=Inv-2`

---

## CreditNote

```go
import "github.com/contract-to-cash/core/domain/invoice"
```

A credit note adjusts a previously issued invoice. It follows a lifecycle from draft through issuance to resolution (applied, refunded, or voided).

**Status constants**: `CreditNoteStatusDraft`, `CreditNoteStatusIssued`, `CreditNoteStatusApplied`, `CreditNoteStatusRefunded`, `CreditNoteStatusVoided`

**Reason constants**: `CreditNoteReasonDuplicate`, `CreditNoteReasonOrderChange`, `CreditNoteReasonCancellation`, `CreditNoteReasonProductUnsatisfactory`, `CreditNoteReasonOther`

### CreditNoteItem

```go
item := invoice.NewCreditNoteItem(invoiceLineItemID, description, amount, taxRate, taxAmount)

item.InvoiceLineItemID() string
item.Description() string
item.Amount() shared.Money
item.TaxRate() *big.Rat
item.TaxAmount() shared.Money
```

### Construction

```go
cn, err := invoice.NewCreditNote(id, invoiceID, accountID, contractID, reason, items,
    invoice.WithCreditNoteMemo("Adjustment for duplicate charge"),
    invoice.WithCreditNoteNumber("CN-2026-001"),
)
```

Returns an error if `items` is empty.

### Methods

```go
cn.Issue(issuedAt time.Time) error              // draft → issued
cn.Apply(creditAmount shared.Money) error       // issued → applied
cn.Refund(refundAmount shared.Money) error      // issued → refunded
cn.Void() error                                 // draft|issued → voided

cn.ID() shared.CreditNoteID
cn.Number() string
cn.InvoiceID() shared.InvoiceID
cn.AccountID() shared.AccountID
cn.ContractID() shared.ContractID
cn.Status() CreditNoteStatus
cn.Reason() CreditNoteReason
cn.Memo() string
cn.Items() []CreditNoteItem
cn.Subtotal() shared.Money
cn.TaxAmount() shared.Money
cn.Total() shared.Money        // subtotal + taxAmount
cn.CreditAmount() shared.Money // amount applied as account credit
cn.RefundAmount() shared.Money // amount refunded via payment gateway
cn.IssuedAt() *time.Time
cn.CreatedAt() time.Time
```

### CreditNoteRepository

```go
type CreditNoteRepository interface {
    Save(ctx context.Context, cn *CreditNote) error
    FindByID(ctx context.Context, id shared.CreditNoteID) (*CreditNote, error)
    FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*CreditNote, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*CreditNote, error)
    FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*CreditNote, error)
    FindByStatus(ctx context.Context, status CreditNoteStatus) ([]*CreditNote, error)
}
```

---

## Payment

```go
import "github.com/contract-to-cash/core/domain/payment"
```

**Status constants**: `PaymentStatusPending`, `PaymentStatusCompleted`, `PaymentStatusFailed`, `PaymentStatusPartiallyRefunded`, `PaymentStatusRefunded`, `PaymentStatusChargedBack`

**Method constants**: `PaymentMethodCreditCard`, `PaymentMethodBankTransfer`, `PaymentMethodDirectDebit`, `PaymentMethodConvenience`, `PaymentMethodCarrier`

### Methods

```go
payment.Complete() error                         // pending → completed
payment.Fail(reason string) error                // pending → failed
payment.MarkRefunded() error                     // completed|partially_refunded → refunded
payment.MarkPartiallyRefunded() error

payment.ID() shared.PaymentID
payment.InvoiceID() shared.InvoiceID
payment.Amount() shared.Money
payment.Status() PaymentStatus
payment.GatewayTransactionID() string
```

---

## Product

```go
import "github.com/contract-to-cash/core/domain/product"
```

**Status constants**: `ProductStatusActive`, `ProductStatusArchived`

---

## Price

```go
import "github.com/contract-to-cash/core/domain/pricing"
```

Prices are **immutable after creation**. To change pricing, create a new Price.

**Status constants**: `PriceStatusActive`, `PriceStatusArchived`

```go
price := pricing.NewPrice(productID, amount, currency, billingCycle, pricingModel, createdAt)

price.ID() shared.PriceID
price.ProductID() shared.ProductID
price.Amount() shared.Money
price.Currency() shared.Currency
price.BillingCycle() BillingCycle
price.PricingModel() PricingModel
price.Status() PriceStatus
```

### Pricing Models

```go
type PricingModel interface {
    CalculatePrice(usage int64) shared.Money
}

// Flat pricing (nil PricingModel on Price)
// Tiered pricing — construct via NewTieredPrice, which validates the tiers
// (sorted ascending by UpTo, UpTo=0 only on the last tier, single currency) and
// returns (TieredPrice, error). Graduated: each tier priced independently.
pricing.NewTieredPrice([]pricing.PriceTier{...}, pricing.TieredPricingGraduated)
// Volume pricing (all units at the tier they fall into)
pricing.NewTieredPrice([]pricing.PriceTier{...}, pricing.TieredPricingVolume)
```

---

## Credit

```go
import "github.com/contract-to-cash/core/domain/balance"
```

**Reason constants**: `BalanceReasonProration`, `BalanceReasonCancellation`, `BalanceReasonManualAdjustment`, `BalanceReasonRefundConversion`, `BalanceReasonGoodwill`

```go
entry.IsExpired(now time.Time) bool
entry.IsFullyConsumed() bool
entry.Consume(amount shared.Money) (shared.Money, error) // Returns consumed amount (FIFO)
```

---

## Usage

```go
import "github.com/contract-to-cash/core/domain/usage"
```

```go
record, err := usage.NewUsageRecord(id, contractID, metricName, quantity, timestamp, idempotencyKey)

type UsageSummary struct {
    ContractID shared.ContractID
    MetricName string
    Period     shared.DateRange
    TotalUsage int64
}
```
