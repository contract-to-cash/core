---
sidebar_position: 1
---

# ドメイン型リファレンス

## 共有値オブジェクト

### Money

通貨付き任意精度の金額値。

```go
import "github.com/contract-to-cash/core/domain/shared"

// 生成
price := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
zero := shared.Zero(shared.CurrencyJPY)

// 算術演算（通貨不一致時はエラーを返す）
sum, err := a.Add(b)
diff, err := a.Subtract(b)
product := a.Multiply(new(big.Rat).SetFrac64(10, 100))
negated := a.Negate()

// 比較
a.IsZero()
a.IsNegative()
a.GreaterThan(b)
min, err := a.Min(b)

// アクセス
a.Amount()   // *big.Rat
a.Currency() // Currency
```

**通貨**: `CurrencyJPY`, `CurrencyUSD`, `CurrencyEUR`

### DateRange

半開区間 `[start, end)`。

```go
period, err := shared.NewDateRange(start, end) // start >= end の場合エラー
period.Start()
period.End()
period.Contains(t time.Time) bool
period.Duration() time.Duration
```

### ID型

ULIDベースの識別子：

| 型 | コンストラクタ |
|----|--------------|
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

// 実装
shared.SystemClock{}                                        // 実時間
shared.FixedClock{FixedTime: time.Date(2026, 4, 1, ...)}   // 固定時刻（テスト用）
```

### DomainError

```go
type ErrorCode string

const (
    ErrCodeInvalidStateTransition ErrorCode = "invalid_state_transition"
    ErrCodeBusinessRule           ErrorCode = "business_rule_violation"
    ErrCodeCurrencyMismatch       ErrorCode = "currency_mismatch"
    ErrCodeInvalidDateRange       ErrorCode = "invalid_date_range"
    ErrCodeUnknownEvent           ErrorCode = "unknown_event"
    ErrCodeValidation             ErrorCode = "validation_error"
    ErrCodeNotFound               ErrorCode = "not_found"
    ErrCodeConflict               ErrorCode = "conflict"
    ErrCodeDuplicateRequest       ErrorCode = "duplicate_request"
    ErrCodeVersionConflict        ErrorCode = "version_conflict"
)

shared.NewDomainError(code ErrorCode, message string) error
```

---

## 契約（Contract）

### ContractAggregate

```go
import "github.com/contract-to-cash/core/domain/contract"

agg := contract.NewContractAggregate(contractID, clock)
```

**ステータス定数**: `ContractStatusDraft`, `ContractStatusTrialing`, `ContractStatusActive`, `ContractStatusPastDue`, `ContractStatusSuspended`, `ContractStatusCancelled`, `ContractStatusExpired`

**タイプ定数**: `ContractTypeOneTime`, `ContractTypeSubscription`, `ContractTypeUsageBased`

**課金サイクル定数**: `BillingCycleDaily`, `BillingCycleWeekly`, `BillingCycleMonthly`, `BillingCycleYearly`

#### コマンド

| メソッド | 遷移元 | 遷移先 |
|---------|-------|-------|
| `Create(cmd, metadata)` | (新規) | draft |
| `Activate(metadata)` | draft, trialing | active |
| `StartTrial(config, metadata)` | draft | trialing |
| `EndTrial(converted, metadata)` | trialing | active/cancelled |
| `Suspend(config, metadata)` | active, past_due | suspended |
| `Resume(metadata)` | suspended | active |
| `Cancel(reason, metadata)` | draft, trialing, active, suspended, past_due | cancelled |
| `Renew(metadata)` | active | active（新期間） |
| `ChangePrice(priceID, policy, proration, metadata)` | active | active |
| `UnscheduleChange(reason, metadata)` | active（保留あり） | active |

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
    AutoRenew      bool
    IdempotencyKey string
}
```

#### SuspensionConfiguration

```go
type SuspensionConfiguration struct {
    BillingBehavior SuspensionBillingBehavior // Skip, Defer, Continue
    ResumeDate      *time.Time
    Reason          string
    SuspendedAt     time.Time
    ExtendContract  bool
}
```

#### ChangePolicy

```go
const (
    ChangePolicyImmediate  ChangePolicy = "immediate"    // 即時適用
    ChangePolicyEndOfTerm  ChangePolicy = "end_of_term"  // 期末適用
)
```

#### ゲッター

```go
agg.ContractID() shared.ContractID
agg.AccountID() shared.AccountID
agg.Status() ContractStatus
agg.PriceID() shared.PriceID
agg.PendingPriceID() *shared.PriceID
agg.HasPendingChange() bool
agg.Price() shared.Money
agg.CurrentPeriod() shared.DateRange
agg.GetContractType() ContractType
agg.AutoRenew() bool
```

#### イベントソーシング

```go
agg.LoadFromHistory(events []eventstore.Event) error
agg.LoadFromSnapshot(snapshot eventstore.Snapshot) error
agg.MarshalSnapshot() ([]byte, error)
agg.UncommittedEvents() []eventstore.Event
agg.ClearUncommittedEvents()
agg.Version() int
```

### リポジトリ

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

## 請求書（Invoice）

```go
import "github.com/contract-to-cash/core/domain/invoice"
```

**ステータス定数**: `InvoiceStatusDraft`, `InvoiceStatusFinalized`, `InvoiceStatusIssued`, `InvoiceStatusPaid`, `InvoiceStatusPartialPaid`, `InvoiceStatusOverdue`, `InvoiceStatusVoided`, `InvoiceStatusRefunded`

### 生成

```go
inv := invoice.NewInvoice(id, accountID, contractID, subtotal, discountAmount, taxAmount,
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

### メソッド

```go
inv.Finalize() error           // draft → finalized
inv.ValidatePayment(amount shared.Money) error    // finalized → （決済バリデーション）
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

## 支払い（Payment）

```go
import "github.com/contract-to-cash/core/domain/payment"
```

**ステータス定数**: `PaymentStatusPending`, `PaymentStatusCompleted`, `PaymentStatusFailed`, `PaymentStatusPartiallyRefunded`, `PaymentStatusRefunded`, `PaymentStatusChargedBack`

### メソッド

```go
payment.Complete() error                         // pending → completed
payment.Fail(reason string) error                // pending → failed
payment.MarkRefunded() error                     // completed → refunded
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

**ステータス定数**: `ProductStatusActive`, `ProductStatusArchived`

---

## Price

```go
import "github.com/contract-to-cash/core/domain/pricing"
```

Priceは**作成後は不変**。価格変更には新しいPriceを作成。

**ステータス定数**: `PriceStatusActive`, `PriceStatusArchived`

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

### 価格モデル

```go
type PricingModel interface {
    CalculatePrice(usage int64) shared.Money
}

// 定額（PriceのPricingModelがnil）
// 段階制 / ボリューム制
TieredPrice{Tiers: []pricing.PriceTier{...}, Mode: ...}
```

---

## クレジット

```go
import "github.com/contract-to-cash/core/domain/credit"
```

**理由定数**: `CreditReasonProration`, `CreditReasonCancellation`, `CreditReasonManualAdjustment`, `CreditReasonRefundConversion`, `CreditReasonGoodwill`

```go
entry.IsExpired(now time.Time) bool
entry.IsFullyConsumed() bool
entry.Consume(amount shared.Money) (shared.Money, error) // 消費額を返す（FIFO）
```

---

## 使用量（Usage）

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
