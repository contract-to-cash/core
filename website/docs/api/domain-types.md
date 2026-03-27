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
| `PlanID` | `NewPlanID()` |
| `UsageRecordID` | `NewUsageRecordID()` |
| `CreditEntryID` | `NewCreditEntryID()` |

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
    ErrCodeBusinessRule           ErrorCode = "business_rule"
    ErrCodeCurrencyMismatch       ErrorCode = "currency_mismatch"
    ErrCodeInvalidDateRange       ErrorCode = "invalid_date_range"
    ErrCodeUnknownEvent           ErrorCode = "unknown_event"
)

shared.NewDomainError(code ErrorCode, message string) error
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

**Billing cycle constants**: `BillingCycleDaily`, `BillingCycleWeekly`, `BillingCycleMonthly`, `BillingCycleYearly`

#### Commands

| Method | From Status | To Status |
|--------|------------|-----------|
| `Create(cmd, metadata)` | (new) | draft |
| `Activate(metadata)` | draft | active |
| `StartTrial(config, metadata)` | draft | trialing |
| `EndTrial(converted, metadata)` | trialing | active/cancelled |
| `Suspend(config, metadata)` | active | suspended |
| `Resume(metadata)` | suspended | active |
| `Cancel(reason, metadata)` | active/suspended | cancelled |
| `Renew(metadata)` | active | active (new period) |
| `ChangePrice(priceID, policy, proration, metadata)` | active | active |
| `UnscheduleChange(reason, metadata)` | active (has pending) | active |
| `SetPriceOverride(override, metadata)` | active | active |
| `ClearPriceOverride(metadata)` | active | active |

#### CreateContractCommand

```go
type CreateContractCommand struct {
    AccountID    shared.AccountID
    PlanID       shared.PlanID
    PriceID      shared.PriceID
    ContractType ContractType
    BillingCycle BillingCycle
    Price        shared.Money
    BasePrice    shared.Money
    AutoRenew    bool
}
```

#### SuspensionConfiguration

```go
type SuspensionConfiguration struct {
    BillingBehavior SuspensionBillingBehavior // Skip, Defer, Continue
    ResumeDate      *time.Time
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
agg.PriceID() shared.PriceID
agg.PendingPriceID() *shared.PriceID
agg.HasPendingChange() bool
agg.PriceOverride() *shared.Money
agg.Price() shared.Money
agg.CurrentPeriod() shared.DateRange
agg.GetContractType() ContractType
agg.AutoRenew() bool
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
    FindActiveByPlanID(ctx context.Context, planID shared.PlanID) ([]*ContractAggregate, error)
    FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindTrialsEndingSoon(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
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
inv := invoice.NewInvoice(id, accountID, contractID,
    invoice.WithLineItems(items),
    invoice.WithBillingPeriod(period),
    invoice.WithDueDate(dueDate),
    invoice.WithStatus(invoice.InvoiceStatusDraft),
    invoice.WithAppliedCredit(creditAmount),
    invoice.WithAmountDue(amountDue),
    invoice.WithAllowPartialPayment(false),
    invoice.WithInvoiceNumber("INV-2026-001"),
)
```

### Methods

```go
inv.Finalize() error           // draft → finalized
inv.ValidatePayment() error    // finalized → (validates for payment)
inv.ID() shared.InvoiceID
inv.Subtotal() shared.Money
inv.DiscountAmount() shared.Money
inv.TaxAmount() shared.Money
inv.Total() shared.Money
inv.AppliedCredit() shared.Money
inv.AmountDue() shared.Money
inv.PaidAmount() shared.Money
inv.Balance() shared.Money
inv.Status() InvoiceStatus
inv.BillingPeriod() shared.DateRange
inv.DueDate() time.Time
```

---

## Payment

```go
import "github.com/contract-to-cash/core/domain/payment"
```

**Status constants**: `PaymentStatusPending`, `PaymentStatusCompleted`, `PaymentStatusFailed`, `PaymentStatusPartiallyRefunded`, `PaymentStatusRefunded`, `PaymentStatusChargedBack`

**Method constants**: `PaymentMethodCreditCard`, `PaymentMethodBankTransfer`, `PaymentMethodDirectDebit`, `PaymentMethodConvenienceStore`, `PaymentMethodCarrier`

### Methods

```go
payment.Complete() error                         // pending → completed
payment.Fail(reason string) error                // pending → failed
payment.MarkRefunded() error                     // completed → refunded
payment.MarkPartiallyRefunded(amount Money) error

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
price := pricing.NewPrice(productID, amount, currency, billingCycle, pricingModel)

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
// Tiered pricing
pricing.NewTieredPrice(tiers []Tier)
// Volume pricing
pricing.NewVolumePrice(tiers []Tier)
```

---

## Credit

```go
import "github.com/contract-to-cash/core/domain/credit"
```

**Reason constants**: `CreditReasonProration`, `CreditReasonCancellation`, `CreditReasonManualAdjustment`, `CreditReasonRefundConversion`, `CreditReasonGoodwill`

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
record := usage.NewUsageRecord(contractID, metricName, quantity, timestamp, idempotencyKey)

type UsageSummary struct {
    ContractID shared.ContractID
    MetricName string
    Period     shared.DateRange
    TotalUsage int64
}
```
