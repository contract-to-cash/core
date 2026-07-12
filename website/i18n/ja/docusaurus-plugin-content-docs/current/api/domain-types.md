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
| `UsageRecordID` | `NewUsageRecordID()` |
| `BalanceEntryID` | `NewBalanceEntryID()` |
| `CreditNoteID` | `NewCreditNoteID()` |

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

## 契約（Contract）

### ContractAggregate

```go
import "github.com/contract-to-cash/core/domain/contract"

agg := contract.NewContractAggregate(contractID, clock)
```

**ステータス定数**: `ContractStatusDraft`, `ContractStatusTrialing`, `ContractStatusActive`, `ContractStatusPastDue`, `ContractStatusSuspended`, `ContractStatusCancelled`, `ContractStatusExpired`

**タイプ定数**: `ContractTypeOneTime`, `ContractTypeSubscription`, `ContractTypeUsageBased`

**課金間隔（Billing interval）**: `pricing.BillingInterval`（`{unit, count}`）を使用する。便利なコンストラクタ: `pricing.Daily()`, `pricing.Weekly()`, `pricing.Monthly()`, `pricing.Yearly()`, `pricing.Quarterly()`, `pricing.SemiAnnual()`。`pricing.BillingCycle` 文字列定数（`BillingCycleDaily/Weekly/Monthly/Yearly`）は `Price` 構築・表示・アダプタ用に `pricing` パッケージ内へ残存するが、契約ドメインでは公開しない（#111 で撤去）。

**one_time 契約は interval を省略可（#218）**: `ContractType == ContractTypeOneTime` のときのみ `CreateContractCommand.Interval` を zero のまま省略できる。その場合、契約は `CurrentPeriod()` 未設定（zero 値）のまま有効化され、更新/満了クエリと更新バッチの対象外になり、`RenewWithInterval` は business-rule エラーを返す。他の契約タイプでは従来どおり非 zero の interval が必須。Price 側は `pricing.NewOneTimePrice` を使う。interval 付きで作成済みの既存 one_time 契約は完全に有効なまま。

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
| `RenewWithInterval(newInterval BillingInterval, metadata)` | active | active（新期間） |
| `ChangePrice(priceID, policy, proration, metadata)` | active | active |
| `UnscheduleChange(reason, metadata)` | active（保留あり） | active |

#### CreateContractCommand

```go
type CreateContractCommand struct {
    AccountID      shared.AccountID
    PriceID        shared.PriceID
    ContractType   ContractType
    Interval       BillingInterval // one_time のみ省略可（zero、#218）。他タイプは必須
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
    ChangePolicyImmediate  ChangePolicy = "immediate"    // 即時適用
    ChangePolicyEndOfTerm  ChangePolicy = "end_of_term"  // 期末適用
)
```

#### ゲッター

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
agg.GetMetadata() map[string]string
agg.CreatedAt() time.Time
agg.UpdatedAt() time.Time
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
    FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindTrialsEndingBefore(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
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
    invoice.WithAppliedBalance(creditAmount),
    invoice.WithAmountDue(amountDue),
    invoice.WithAllowPartialPayment(false),
    invoice.WithInvoiceNumber("INV-2026-001"),
    invoice.WithOriginalInvoiceID(originalID),   // 再発行請求書用
    invoice.WithRevisionOf(parentID),             // リビジョンチェーンリンク用
    invoice.WithPaymentMethodID(&pmID),           // 請求書レベルの支払い方法オーバーライド
)
```

### メソッド

```go
inv.Finalize() error           // draft → finalized
inv.Void() error               // draft/finalized → voided
inv.VoidWithReason(reason string) error  // any except voided|refunded → voided（理由付き）
inv.ValidatePayment(amount shared.Money) error    // finalized|issued|partial_paid|overdue → （決済バリデーション）
inv.RecordPayment(amount shared.Money, paidAt time.Time) error  // 決済を記録

// リビジョンサポート（void-and-recreate ワークフロー）
inv.SetRevisionOf(id shared.InvoiceID)         // 直接の親請求書を設定
inv.SetOriginalInvoiceID(id shared.InvoiceID)  // リビジョンチェーンのルートを設定
inv.OriginalInvoiceID() *shared.InvoiceID      // チェーンのルート請求書ID
inv.RevisionOf() *shared.InvoiceID             // 直接の親請求書ID
inv.VoidReason() string                        // 無効化理由

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
inv.PaymentMethodID() *string
inv.AllowPartialPay() bool
inv.Metadata() map[string]string
```

### リビジョンサポート

請求書は void-and-recreate ワークフローのための2レベルリンクモデルをサポートします：

- **`originalInvoiceID`** — リビジョン回数に関わらず、常にリビジョンチェーンの最初の請求書（ルート）を指します。
- **`revisionOf`** — 直接の親（チェーン内の直前の請求書）を指します。

例: `Inv-1 -> Inv-2 -> Inv-3`
- `Inv-2`: `originalInvoiceID=Inv-1`, `revisionOf=Inv-1`
- `Inv-3`: `originalInvoiceID=Inv-1`, `revisionOf=Inv-2`

---

## クレジットノート（CreditNote）

```go
import "github.com/contract-to-cash/core/domain/invoice"
```

クレジットノートは、発行済み請求書に対する調整（返金やクレジット適用）を表すエンティティです。

**ステータス定数**: `CreditNoteStatusDraft`, `CreditNoteStatusIssued`, `CreditNoteStatusApplied`, `CreditNoteStatusRefunded`, `CreditNoteStatusVoided`

**理由定数**: `CreditNoteReasonDuplicate`, `CreditNoteReasonOrderChange`, `CreditNoteReasonCancellation`, `CreditNoteReasonProductUnsatisfactory`, `CreditNoteReasonOther`

### 生成

```go
cn, err := invoice.NewCreditNote(
    id,          // shared.CreditNoteID
    invoiceID,   // shared.InvoiceID
    accountID,   // shared.AccountID
    contractID,  // shared.ContractID
    reason,      // CreditNoteReason
    items,       // []CreditNoteItem（1件以上必須）
    invoice.WithCreditNoteMemo("メモ"),
    invoice.WithCreditNoteNumber("CN-2026-001"),
)
```

### CreditNoteItem

```go
item := invoice.NewCreditNoteItem(
    invoiceLineItemID, // 対象の請求書明細ID
    description,       // 説明
    amount,            // shared.Money（調整額）
    taxRate,           // *big.Rat（税率）
    taxAmount,         // shared.Money（税額）
)

item.InvoiceLineItemID() string
item.Description() string
item.Amount() shared.Money
item.TaxRate() *big.Rat
item.TaxAmount() shared.Money
```

### メソッド

```go
cn.Issue(issuedAt time.Time) error              // draft → issued
cn.Apply(creditAmount shared.Money) error        // issued → applied（クレジット適用）
cn.Refund(refundAmount shared.Money) error       // issued → refunded（返金処理）
cn.Void() error                                  // draft/issued → voided

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
cn.Total() shared.Money
cn.CreditAmount() shared.Money
cn.RefundAmount() shared.Money
cn.IssuedAt() *time.Time
cn.CreatedAt() time.Time
```

### リポジトリ

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

## 支払い（Payment）

```go
import "github.com/contract-to-cash/core/domain/payment"
```

**ステータス定数**: `PaymentStatusPending`, `PaymentStatusCompleted`, `PaymentStatusFailed`, `PaymentStatusPartiallyRefunded`, `PaymentStatusRefunded`, `PaymentStatusChargedBack`

**メソッド定数**: `PaymentMethodCreditCard`, `PaymentMethodBankTransfer`, `PaymentMethodDirectDebit`, `PaymentMethodConvenience`, `PaymentMethodCarrier`

### メソッド

```go
payment.Complete() error                         // pending → completed
payment.Fail(reason string) error                // pending → failed
payment.MarkRefunded() error                     // completed|partially_refunded → refunded
payment.MarkPartiallyRefunded() error            // completed → partially_refunded

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
price := pricing.NewPrice(productID, amount, currency, billingCycle, pricingModel, createdAt)

// 柔軟な interval（quarterly, semi-annual, ...）。zero interval は拒否する。
price, err := pricing.NewPriceWithInterval(productID, amount, currency, interval, pricingModel, createdAt)

// one_time 用 Price（#218）: Interval() は zero、BillingCycle() は空文字列、
// PricingModel() は nil（フラットな金額を 1 回だけ課金）。one_time 契約向け。
price, err := pricing.NewOneTimePrice(productID, amount, currency, createdAt)

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
import "github.com/contract-to-cash/core/domain/balance"
```

**理由定数**: `BalanceReasonProration`, `BalanceReasonCancellation`, `BalanceReasonManualAdjustment`, `BalanceReasonRefundConversion`, `BalanceReasonGoodwill`

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
