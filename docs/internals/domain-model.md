---
sidebar_label: Domain Model
---

# ドメインモデル設計

## 論理データモデル図

```mermaid
erDiagram
    Account ||--o{ Contract : "holds"
    Account ||--o{ BalanceEntry : "owns"
    Product ||--o{ Price : "priced by"
    Product ||--o{ Feature : "includes"
    Product ||--o{ UsageMetric : "tracks"
    Contract ||--o{ Invoice : "generates"
    Contract ||--o{ UsageRecord : "records"
    Contract ||--o| TrialConfiguration : "trial config"
    Contract ||--o| SuspensionConfiguration : "suspension config"
    Invoice ||--o{ LineItem : "contains"
    Invoice ||--o{ CreditNote : "adjusted by"
    Invoice ||--o{ Payment : "paid by"
    CreditNote ||--o{ CreditNoteItem : "contains"
    Invoice ||--o| Invoice : "revision of"

    Account {
        AccountID id PK
    }

    Product {
        ProductID id PK
        string name
        string description
        ProductStatus status "active | archived"
        map metadata
        timestamp createdAt
    }

    Feature {
        string name
        bool included
        int64 limit "optional"
    }

    UsageMetric {
        string name
        int64 includedQuantity
    }

    Price {
        PriceID id PK
        ProductID productID FK
        Money amount
        Currency currency
        BillingInterval interval "{unit, count} 例: {month, 3}"
        PricingModel pricingModel "flat | tiered | usage"
        PriceStatus status "active | archived"
        timestamp createdAt
    }

    Contract {
        ContractID id PK
        AccountID accountID FK
        PriceID priceID FK
        ContractStatus status "draft | trialing | active | past_due | suspended | cancelled | expired"
        ContractType contractType "one_time | subscription | usage_based"
        BillingInterval interval
        DateRange currentPeriod
        Money price
        Money basePrice
        bool autoRenew
        bool cancelAtPeriodEnd
        PriceID pendingPriceID "optional"
        string paymentMethodID "optional"
        map metadata
        int version
        timestamp createdAt
        timestamp updatedAt
    }

    TrialConfiguration {
        timestamp trialEndDate
        bool autoConvert
        bool requirePaymentMethod
        int_array conversionReminderDays
    }

    SuspensionConfiguration {
        timestamp suspendedAt
        timestamp resumeDate "optional"
        SuspensionBillingBehavior billingBehavior "skip | defer | continue"
        bool extendContract
        string reason
    }

    Invoice {
        InvoiceID id PK
        string invoiceNumber
        AccountID accountID FK
        ContractID contractID FK
        InvoiceStatus status "draft | finalized | issued | paid | partial_paid | overdue | voided | refunded"
        Money subtotal
        Money taxAmount
        Money discountAmount
        Money total
        Money appliedBalance
        Money amountDue
        Money paidAmount
        Money balance
        DateRange billingPeriod
        bool allowPartialPay
        string paymentMethodID "optional"
        InvoiceID originalInvoiceID "optional: revision chain root"
        InvoiceID revisionOf "optional: direct parent"
        string voidReason "optional"
        map metadata
        timestamp issueDate "optional"
        timestamp dueDate
        timestamp paidAt "optional"
    }

    LineItem {
        string id PK
        string description
        int64 quantity
        Money unitPrice
        Money amount
        Decimal taxRate "*big.Rat"
        PriceID priceID FK "optional"
        map metadata
    }

    Payment {
        PaymentID id PK
        InvoiceID invoiceID FK
        Money amount
        Money refundedAmount
        PaymentMethod method "credit_card | bank_transfer | direct_debit | convenience_store | carrier"
        PaymentStatus status "pending | completed | failed | partially_refunded | refunded | charged_back"
        string gatewayTransactionID
        string idempotencyKey
        string failureReason "optional"
        map metadata
        timestamp processedAt
    }

    BalanceEntry {
        BalanceEntryID id PK
        AccountID accountID FK
        Money originalAmount
        Money remainingAmount
        BalanceReason reason "proration | cancellation | manual_adjustment | refund_conversion | goodwill"
        string sourceType "optional"
        string sourceID "optional"
        string description
        int version "optimistic lock"
        timestamp expiresAt "optional"
        timestamp createdAt
    }

    UsageRecord {
        UsageRecordID id PK
        ContractID contractID FK
        string metricName
        int64 quantity
        string idempotencyKey
        timestamp timestamp
        map metadata
    }

    UsageSummary {
        ContractID contractID FK
        string metricName
        DateRange period
        int64 totalUsage
    }

    CreditNote {
        CreditNoteID id PK
        string number
        InvoiceID invoiceID FK
        AccountID accountID FK
        ContractID contractID FK
        CreditNoteStatus status "draft | issued | applied | refunded | voided"
        CreditNoteReason reason "duplicate | order_change | cancellation | product_unsatisfactory | other"
        string memo
        Money subtotal
        Money taxAmount
        Money total
        Money creditAmount
        Money refundAmount
        timestamp issuedAt "optional"
        timestamp createdAt
    }

    CreditNoteItem {
        string invoiceLineItemID FK
        string description
        Money amount
        Decimal taxRate "*big.Rat"
        Money taxAmount
    }
```

> **凡例:** Account は外部境界（このドメイン外で管理）。Contract はイベントソーシング集約根（`ContractAggregate`）。
> Money は `big.Rat` ベースの値オブジェクト、Decimal は `*big.Rat`、ID は ULID で生成。
> TrialConfiguration / SuspensionConfiguration / UsageSummary は値オブジェクト（独立した永続化IDを持たない）。

## 1. 共通値オブジェクト

### 1.1 Money（金額）

```go
// domain/shared/money.go
package shared

import (
    "encoding/json"
    "fmt"
    "math/big"
)

// Money は通貨と金額を表現する値オブジェクト
// big.Rat を使用し、浮動小数点誤差を回避する
type Money struct {
    amount   *big.Rat
    currency Currency
}

type Currency string

const (
    CurrencyJPY Currency = "JPY"
    CurrencyUSD Currency = "USD"
    CurrencyEUR Currency = "EUR"
)

func NewMoney(amount *big.Rat, currency Currency) Money
func Zero(currency Currency) Money

func (m Money) Amount() *big.Rat
func (m Money) Currency() Currency
func (m Money) Add(other Money) (Money, error)
func (m Money) Subtract(other Money) (Money, error)
func (m Money) Multiply(factor *big.Rat) Money
func (m Money) Negate() Money
func (m Money) IsNegative() bool
func (m Money) IsZero() bool
func (m Money) GreaterThan(other Money) bool
func (m Money) Min(other Money) (Money, error)
func (m Money) MarshalJSON() ([]byte, error)
func (m *Money) UnmarshalJSON(data []byte) error
```

### 1.2 DateRange（期間）

```go
// domain/shared/datetime.go
package shared

import (
    "fmt"
    "time"
)

// DateRange は期間を表す値オブジェクト
// 半開区間 [start, end) を採用。endは含まない。連続する期間の隣接判定に適している。
type DateRange struct {
    start time.Time
    end   time.Time
}

func NewDateRange(start, end time.Time) (DateRange, error)

func (r DateRange) Start() time.Time
func (r DateRange) End() time.Time
func (r DateRange) Contains(t time.Time) bool
func (r DateRange) Duration() time.Duration
func (r DateRange) Equals(other DateRange) bool
func (r DateRange) IsZero() bool
func (r DateRange) String() string
func (r DateRange) Next(cycle string) DateRange
func (r DateRange) MarshalJSON() ([]byte, error)
func (r *DateRange) UnmarshalJSON(data []byte) error

// AddBillingCycleDuration は請求サイクル1周期分を加算するユーティリティ関数。
// DateRange.Next() とContractAggregate両方から参照される唯一の変換ロジック。
// cycle: "monthly" | "yearly" | "weekly" | "daily"
func AddBillingCycleDuration(t time.Time, cycle string) time.Time
```

### 1.3 共有ID型

全ドメインで使用するID型を `domain/shared/identifier.go` に集約する。
これにより各ドメインパッケージは `shared` のみに依存し、互いを参照しない（循環依存の解消）。

```go
// domain/shared/identifier.go
package shared

// 全ドメインで使用するID型
type AccountID string
type ContractID string
type InvoiceID string
type PaymentID string
type UsageRecordID string
type ProductID string
type PriceID string
type BalanceEntryID string
type CreditNoteID string

// ID生成ヘルパー
func NewAccountID() AccountID           { return AccountID(generateULID()) }
func NewContractID() ContractID         { return ContractID(generateULID()) }
func NewInvoiceID() InvoiceID           { return InvoiceID(generateULID()) }
func NewPaymentID() PaymentID           { return PaymentID(generateULID()) }
func NewUsageRecordID() UsageRecordID   { return UsageRecordID(generateULID()) }
func NewProductID() ProductID           { return ProductID(generateULID()) }
func NewPriceID() PriceID               { return PriceID(generateULID()) }
func NewBalanceEntryID() BalanceEntryID { return BalanceEntryID(generateULID()) }
func NewCreditNoteID() CreditNoteID     { return CreditNoteID(generateULID()) }

// GenerateID はイベントID等の汎用ID文字列を生成する
func GenerateID() string { return fmt.Sprintf("evt_%s", generateULID()) }
```

### 1.4 DomainError（ドメインエラー）

```go
// domain/shared/errors.go
package shared

import "fmt"

type ErrorCode string

const (
    ErrCodeInvalidStateTransition ErrorCode = "invalid_state_transition"
    ErrCodeValidation             ErrorCode = "validation_error"
    ErrCodeCurrencyMismatch       ErrorCode = "currency_mismatch"
    ErrCodeInvalidDateRange       ErrorCode = "invalid_date_range"
    ErrCodeNotFound               ErrorCode = "not_found"
    ErrCodeConflict               ErrorCode = "conflict"
    ErrCodeDuplicateRequest       ErrorCode = "duplicate_request"
    ErrCodeUnknownEvent           ErrorCode = "unknown_event"
    ErrCodeVersionConflict        ErrorCode = "version_conflict"
    ErrCodeBusinessRule           ErrorCode = "business_rule_violation"
)

// DomainError 構造化ドメインエラー
type DomainError struct {
    Code    ErrorCode
    Message string
    Cause   error
}

func NewDomainError(code ErrorCode, message string) *DomainError
func NewDomainErrorWithCause(code ErrorCode, message string, cause error) *DomainError
func (e *DomainError) Error() string   // "[code] message" 形式
func (e *DomainError) Unwrap() error
```

### 1.5 Clock（時刻抽象）

```go
// domain/shared/clock.go
package shared

import "time"

// Clock は時刻取得を抽象化するインターフェース。
// 全てのドメイン・アプリケーションコードは time.Now() ではなく Clock を使用すること。
type Clock interface {
    Now() time.Time
}

// SystemClock は本番用の Clock 実装。UTC で現在時刻を返す。
type SystemClock struct{}
func (c SystemClock) Now() time.Time { return time.Now().UTC() }

// FixedClock はテスト用の Clock 実装。固定時刻を返す。
type FixedClock struct {
    FixedTime time.Time
}
func (c FixedClock) Now() time.Time { return c.FixedTime }
```

## 2. Account（アカウント）

```go
// domain/account/entity.go
package account

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// AccountID は shared/identifier.go で定義

type Account struct {
    id          shared.AccountID
    name        string
    email       string
    billingInfo BillingInfo
    createdAt   time.Time
    updatedAt   time.Time
}

type BillingInfo struct {
    Address      Address
    TaxID        string
    PaymentTerms int              // 支払い期限（日数）
    BalanceConfig *balance.BalanceConfig // クレジット設定（nil = グローバルデフォルトを使用）
}
// NOTE: balance パッケージ（セクション9）の import が必要
// import "domain/balance"

type Address struct {
    Line1      string
    Line2      string
    City       string
    State      string
    PostalCode string
    Country    string
}
```

## 3. Contract（契約）

### 3.1 エンティティ定義

```go
// domain/contract/entity.go
package contract

import (
    "github.com/contract-to-cash/core/domain/pricing"
)

// ContractID は shared/identifier.go で定義

type ContractStatus string

const (
    ContractStatusDraft     ContractStatus = "draft"
    ContractStatusTrialing  ContractStatus = "trialing"
    ContractStatusActive    ContractStatus = "active"
    ContractStatusPastDue   ContractStatus = "past_due"    // 支払い遅延（Dunning中）
    ContractStatusSuspended ContractStatus = "suspended"
    ContractStatusCancelled ContractStatus = "cancelled"
    ContractStatusExpired   ContractStatus = "expired"
)

// ContractStatus 状態遷移ルール:
//   draft     → active | trialing | cancelled（作成直後のキャンセル）
//   trialing  → active（トライアル終了・自動移行）| cancelled（トライアル中の解約）
//   active    → past_due | suspended | cancelled | expired
//   past_due  → active（支払い成功）| suspended（リトライ上限到達）| cancelled
//   suspended → active（再開）| cancelled（一時停止中の解約）
//   cancelled → 終端状態（遷移なし）
//   expired   → 終端状態（遷移なし）

type ContractType string

const (
    ContractTypeOneTime    ContractType = "one_time"
    ContractTypeSubscription ContractType = "subscription"
    ContractTypeUsageBased ContractType = "usage_based"
)

// BillingInterval は pricing.BillingInterval のエイリアス。
// 課金サイクルは常に BillingInterval（{unit, count}）で表現する。
// 旧 BillingCycle 文字列スキャフォールディングは #111 で撤去済み。
type BillingInterval = pricing.BillingInterval

```

> **注（issue #159）**: 以前ここに定義されていた read-only な `Contract` エンティティ
> （ContractAggregate の状態保持ミラー）は、コンストラクタ・パッケージ外参照が一切なく
> 死にコードだったため削除済み。契約モデルは event-sourced な `ContractAggregate`
> （§3.2）に一本化されている。読み取りモデル / Projection は利用者側の責務。

### 3.2 ContractAggregate（イベントソーシング対応）

```go
// domain/contract/aggregate.go
package contract

import (
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
)

// CreateContractCommand はContract作成時のパラメータを保持する。
// IdempotencyKey は必須（design-decisions §4.1、issue #159）: Create が空を
// validation エラーで拒否し、キーは ContractCreatedEvent（schema v3）に載る。
// コアが強制するのは「存在」まで — キーの一意性はリポジトリ/アダプタが
// ユニークインデックス等で強制する（contract.Repository.Save の godoc 参照）。
type CreateContractCommand struct {
    IdempotencyKey string // 必須。空は validation エラー
    AccountID      shared.AccountID
    PriceID        shared.PriceID
    ContractType   ContractType
    Interval       BillingInterval
    Price          shared.Money
    BasePrice      shared.Money
    AutoRenew      bool
}

// ContractAggregate はイベントソーシングに対応した契約集約
type ContractAggregate struct {
    eventstore.BaseAggregate

    contractID        shared.ContractID
    accountID         shared.AccountID
    status            ContractStatus
    contractType      ContractType
    interval          BillingInterval
    currentPeriod     shared.DateRange
    trialConfig       *TrialConfiguration
    suspensionConfig  *SuspensionConfiguration
    paymentMethodID   *string
    priceID           shared.PriceID
    price             shared.Money
    basePrice         shared.Money
    autoRenew         bool
    cancelAtPeriodEnd bool
    pendingPriceID    *shared.PriceID
    createdAt         time.Time
    updatedAt         time.Time
}

func NewContractAggregate(id shared.ContractID, clock shared.Clock) *ContractAggregate

// コマンドメソッド（各メソッドはイベントを生成し Apply で状態を更新する）
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) Activate(metadata eventstore.EventMetadata) error
func (a *ContractAggregate) Suspend(config SuspensionConfiguration, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) Resume(metadata eventstore.EventMetadata) error
func (a *ContractAggregate) Cancel(reason string, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) ChangePrice(newPriceID shared.PriceID, policy ChangePolicy, proration *PlanChangeProration, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) UnscheduleChange(reason string, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) ChangePaymentMethod(paymentMethodID *string, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) StartTrial(config TrialConfiguration, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) EndTrial(converted bool, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) RenewWithInterval(newInterval BillingInterval, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) ScheduleCancellation(reason string, metadata eventstore.EventMetadata) error
func (a *ContractAggregate) UnscheduleCancellation(metadata eventstore.EventMetadata) error

// イベント適用・リプレイ
func (a *ContractAggregate) Apply(event eventstore.DomainEvent) error
func (a *ContractAggregate) LoadFromHistory(events []eventstore.Event) error
func (a *ContractAggregate) MarshalSnapshot() ([]byte, error)
func (a *ContractAggregate) LoadFromSnapshot(snapshot eventstore.Snapshot) error

// Getters
func (a *ContractAggregate) ContractID() shared.ContractID
func (a *ContractAggregate) AccountID() shared.AccountID
func (a *ContractAggregate) Status() ContractStatus
func (a *ContractAggregate) GetContractType() ContractType
func (a *ContractAggregate) GetInterval() BillingInterval
func (a *ContractAggregate) CurrentPeriod() shared.DateRange
func (a *ContractAggregate) TrialConfig() *TrialConfiguration
func (a *ContractAggregate) SuspensionConfig() *SuspensionConfiguration
func (a *ContractAggregate) PaymentMethodID() *string
func (a *ContractAggregate) Price() shared.Money
func (a *ContractAggregate) BasePrice() shared.Money
func (a *ContractAggregate) PriceID() shared.PriceID
func (a *ContractAggregate) AutoRenew() bool
func (a *ContractAggregate) CancelAtPeriodEnd() bool
func (a *ContractAggregate) PendingPriceID() *shared.PriceID
func (a *ContractAggregate) HasPendingChange() bool
func (a *ContractAggregate) CreatedAt() time.Time
func (a *ContractAggregate) UpdatedAt() time.Time
```

### 3.3 イベント型一覧

```go
// domain/contract/events.go
package contract

// 全15種のドメインイベント
const (
    EventTypeContractCreated         eventstore.EventType = "contract.created"
    EventTypeContractActivated       eventstore.EventType = "contract.activated"
    EventTypeContractSuspended       eventstore.EventType = "contract.suspended"
    EventTypeContractResumed         eventstore.EventType = "contract.resumed"
    EventTypeContractCancelled       eventstore.EventType = "contract.cancelled"
    EventTypePriceChanged            eventstore.EventType = "contract.price_changed"
    EventTypeTrialStarted            eventstore.EventType = "contract.trial_started"
    EventTypeTrialEnded              eventstore.EventType = "contract.trial_ended"
    EventTypePaymentMethodChanged    eventstore.EventType = "contract.payment_method_changed"
    EventTypeContractRenewed         eventstore.EventType = "contract.renewed"
    EventTypeContractExpired         eventstore.EventType = "contract.expired"
    EventTypeCancellationScheduled   eventstore.EventType = "contract.cancellation_scheduled"
    EventTypeCancellationUnscheduled eventstore.EventType = "contract.cancellation_unscheduled"
    EventTypePriceChangeScheduled    eventstore.EventType = "contract.price_change_scheduled"
    EventTypePriceChangeUnscheduled  eventstore.EventType = "contract.price_change_unscheduled"
)

type ContractCreatedEvent struct {
    ContractID     shared.ContractID
    AccountID      shared.AccountID
    PriceID        shared.PriceID
    IdempotencyKey string          // SchemaVersion 3 で追加（issue #159）。歴史的イベントでは空
    Price          shared.Money
    BasePrice      shared.Money
    Interval       BillingInterval
    ContractType   ContractType
    AutoRenew      bool
    CreatedAt      time.Time
}

type ContractActivatedEvent struct {
    ContractID    shared.ContractID
    ActivatedAt   time.Time
    CurrentPeriod shared.DateRange
}

type ContractSuspendedEvent struct {
    ContractID      shared.ContractID
    SuspendedAt     time.Time
    BillingBehavior SuspensionBillingBehavior
    ResumeDate      *time.Time
    Reason          string
}

type ContractResumedEvent struct {
    ContractID shared.ContractID
    ResumedAt  time.Time
}

type ContractCancelledEvent struct {
    ContractID  shared.ContractID
    CancelledAt time.Time
    Reason      string
}

type PriceChangedEvent struct {
    ContractID shared.ContractID
    OldPriceID shared.PriceID
    NewPriceID shared.PriceID
    Policy     ChangePolicy
    Proration  *PlanChangeProration
    ChangedAt  time.Time
    // レガシーフィールド（後方互換）
    OldPrice    shared.Money
    NewPrice    shared.Money
    EffectiveAt time.Time
}

type PriceChangeScheduledEvent struct {
    ContractID     shared.ContractID
    CurrentPriceID shared.PriceID
    NewPriceID     shared.PriceID
    Policy         ChangePolicy
    ScheduledAt    time.Time
}

type PriceChangeUnscheduledEvent struct {
    ContractID       shared.ContractID
    CancelledPriceID shared.PriceID
    Reason           string
    UnscheduledAt    time.Time
}

type TrialStartedEvent struct {
    ContractID  shared.ContractID
    TrialConfig TrialConfiguration
    StartedAt   time.Time
}

type TrialEndedEvent struct {
    ContractID    shared.ContractID
    EndedAt       time.Time
    Converted     bool
    CurrentPeriod shared.DateRange // 転換時（Converted=true）の初期課金期間。SchemaVersion 2 で追加
}

type PaymentMethodChangedEvent struct {
    ContractID         shared.ContractID
    OldPaymentMethodID *string
    NewPaymentMethodID *string
    ChangedAt          time.Time
}

type ContractRenewedEvent struct {
    ContractID      shared.ContractID
    OldPeriod       shared.DateRange
    NewPeriod       shared.DateRange
    OldPriceID      shared.PriceID
    NewPriceID      shared.PriceID
    PriceChanged    bool
    OldInterval     BillingInterval
    NewInterval     BillingInterval
    RenewedAt       time.Time
}

type ContractExpiredEvent struct {
    ContractID  shared.ContractID
    ExpiredAt   time.Time
    FinalPeriod shared.DateRange
}

type CancellationScheduledEvent struct {
    ContractID  shared.ContractID
    Reason      string
    ScheduledAt time.Time
}

type CancellationUnscheduledEvent struct {
    ContractID    shared.ContractID
    UnscheduledAt time.Time
}
```

### 3.4 変更ポリシー

```go
// domain/contract/policy.go
package contract

// ChangePolicy は価格変更の適用タイミングを定義する
type ChangePolicy string

const (
    // ChangePolicyImmediate は即座に変更を適用する。日割り計算が発生しうる。
    ChangePolicyImmediate ChangePolicy = "immediate"

    // ChangePolicyEndOfTerm は次回更新時に変更を適用する。
    // 現在の期間は現行価格のまま継続する。
    ChangePolicyEndOfTerm ChangePolicy = "end_of_term"
)
```

### 3.5 トライアル設定

```go
// domain/contract/trial.go
package contract

import "time"

// TrialConfiguration トライアル設定
type TrialConfiguration struct {
    TrialEndDate          time.Time     // トライアル終了日
    AutoConvert           bool          // 自動本契約移行
    RequirePaymentMethod  bool          // 支払い方法事前登録必須
    ConversionReminderDays []int        // 移行リマインダー（何日前に通知）
}
```

`RequirePaymentMethod=true` の場合、支払い方法が未登録の契約は
`batch.TrialExpirationProcessor` が自動変換をブロックし、失敗として記録する
（契約は Trialing のまま。支払い方法を登録するか `RequirePaymentMethod` を外すまで変換されない）。

**トライアル転換時の課金期間（issue #146）**: トライアルが転換する（`EndTrial(converted=true)`）と、
契約は Active になると同時に `Activate` と同じ方法で初期課金期間（`currentPeriod`）が確立される
（`interval` を `EndedAt` に加算した `[EndedAt, interval.AddTo(EndedAt))`）。これがないと転換済み契約は
ゼロ値の期間を持ち、更新・請求サイクルから静かに脱落する。転換パスは 2 つあるが結果は一致する:

- `batch.TrialExpirationProcessor` が呼ぶ `EndTrial(converted=true)`
- 統合者が Trialing 契約に対して呼ぶ `Activate`（内部で `EndTrial(converted=true)` に委譲）

どちらも「Active + 課金期間確立 + `trialConfig` クリア + `TrialEndedEvent`（converted=true）の記録」という
同一の状態・イベントを生成する。`TrialEndedEvent.CurrentPeriod` は SchemaVersion 2 で追加された。
歴史的な v1 ペイロード（`current_period` なし）は `TrialEndedEventUpcaster` が v2 にマークし、
Apply が `interval`（先行する `ContractCreatedEvent` から復元される）を `EndedAt` に加算して期間を
決定的に導出する（`interval` はこのイベントに載っていないため Upcaster では埋められない）。
非転換（cancelled）の場合は期間を設定しない。

### 3.6 一時停止設定

```go
// domain/contract/suspension.go
package contract

import "time"

// SuspensionConfiguration 一時停止設定
type SuspensionConfiguration struct {
    SuspendedAt        time.Time
    ResumeDate         *time.Time          // nil = 手動再開
    BillingBehavior    SuspensionBillingBehavior
    ExtendContract     bool                // 契約期間延長
    Reason             string
}

// SuspensionBillingBehavior 一時停止中の請求動作
type SuspensionBillingBehavior string

const (
    // 一時停止中は請求しない
    SuspensionBillingSkip SuspensionBillingBehavior = "skip"
    // 再開時にまとめて請求
    SuspensionBillingDefer SuspensionBillingBehavior = "defer"
    // 一時停止中も請求継続
    SuspensionBillingContinue SuspensionBillingBehavior = "continue"
)
```

### 3.7 日割り計算設定

```go
// domain/contract/proration.go
package contract

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// ProrationBehavior 日割り計算動作
type ProrationBehavior string

const (
    // 変更時に即座に日割り調整
    ProrationImmediate ProrationBehavior = "immediate"
    // 次サイクルから新価格適用（日割りなし）
    ProrationNextCycle ProrationBehavior = "next_cycle"
    // 変更時に即座に新価格で全額請求
    ProrationImmediateFull ProrationBehavior = "immediate_full"
)

// RoundingMode 端数処理モード
type RoundingMode string

const (
    RoundingUp     RoundingMode = "up"
    RoundingDown   RoundingMode = "down"
    RoundingHalfUp RoundingMode = "half_up"
)

// ProrationConfig 日割り計算設定
type ProrationConfig struct {
    Behavior     ProrationBehavior
    RoundingMode RoundingMode
}

// PlanChangeProration は価格変更時の日割り計算結果を保持する。
// billing.ProrationResult と同等だが、循環依存回避のため contract ドメイン内で定義。
type PlanChangeProration struct {
    CreditAmount     shared.Money
    ChargeAmount     shared.Money
    AdjustmentAmount shared.Money
    EffectiveDate    time.Time
}
```

### 3.8 リポジトリインターフェース

```go
// domain/contract/repository.go
package contract

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// Repository 契約リポジトリインターフェース
type Repository interface {
    // 基本CRUD
    Save(ctx context.Context, aggregate *ContractAggregate) error
    FindByID(ctx context.Context, id shared.ContractID) (*ContractAggregate, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*ContractAggregate, error)

    // クエリ
    FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindTrialsEndingBefore(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindDueForRenewal(ctx context.Context, asOf time.Time) ([]*ContractAggregate, error)

    // 時点指定（イベントソーシング）
    FindByIDAsOf(ctx context.Context, id shared.ContractID, asOf time.Time) (*ContractAggregate, error)
}
```

## 4. Invoice（請求書）

### 4.1 エンティティ定義

```go
// domain/invoice/entity.go
package invoice

import (
    "math/big"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// InvoiceID は shared/identifier.go で定義
// account, contract パッケージへの直接依存なし（shared.AccountID, shared.ContractID を使用）

type InvoiceStatus string

const (
    InvoiceStatusDraft      InvoiceStatus = "draft"      // 生成直後（GracePeriod中）
    InvoiceStatusFinalized  InvoiceStatus = "finalized"  // 確定済み（変更不可）
    InvoiceStatusIssued     InvoiceStatus = "issued"      // 送付済み
    InvoiceStatusPaid       InvoiceStatus = "paid"
    InvoiceStatusPartialPaid InvoiceStatus = "partial_paid"  // 一部入金
    InvoiceStatusOverdue    InvoiceStatus = "overdue"
    InvoiceStatusVoided     InvoiceStatus = "voided"
    InvoiceStatusRefunded   InvoiceStatus = "refunded"
)

// InvoiceStatus 状態遷移ルール:
//   draft        → finalized（Finalize）| voided（Void）
//   finalized    → issued（MarkIssued）| overdue（MarkOverdue）| paid | partial_paid（RecordPayment）| voided（VoidWithReason）
//   issued       → paid | partial_paid（RecordPayment）| overdue（MarkOverdue）| voided（VoidWithReason）
//   partial_paid → paid（残額入金）| refunded（MarkRefunded）| voided（VoidWithReason）
//   overdue      → paid | partial_paid（RecordPayment）| voided（VoidWithReason）
//   paid         → refunded（MarkRefunded）| voided（VoidWithReason）
//   voided       → 終端状態（遷移なし。refunded へは遷移不可）
//   refunded     → 終端状態（遷移なし）
//
// issued / overdue への遷移は #159 で追加された明示メソッドで行う（従来は snapshot 経由のみ）:
//   - MarkIssued(): finalized → issued。送付（レンダリング/送信）はコアのスコープ外のため、
//     発火は統合者の送付フロー。
//   - MarkOverdue(now): finalized|issued → overdue。now が dueDate を厳密に過ぎている場合のみ。
//     dueDate 未設定の請求書は overdue にできない。検出（FindOverdue 等）と発火は統合者の
//     スケジューラ責務。partial_paid は入金済み情報を失わないため overdue へ遷移させない。
//   どちらも楽観ロックの version を bump する（#147 のルール: 永続状態を変える全メソッドが bump）。
//
// MarkRefunded の遷移元は paid / partial_paid のみ（実際に入金があった請求書だけ返金しうる）。
// voided は「入金前にキャンセルされた」別の終端状態であり、返金対象の入金が存在しないため
// voided → refunded は許可しない（VoidWithReason も voided/refunded を別終端として扱う）。
// 上位トリガー（CreditNote 全額適用 / Payment 全額返金 / 手動）の決定は呼び出し側に委ねる（issue #99）。

type LineItem struct {
    id          string
    description string
    quantity    int64            // UsageRecord.quantity と型統一
    unitPrice   shared.Money
    amount      shared.Money
    taxRate     *big.Rat
    priceID     shared.PriceID   // 料金体系トレーサビリティ用
    metadata    map[string]string
}

// NewLineItem creates a new LineItem.
// Returns an error if quantity is negative.
func NewLineItem(id, description string, quantity int64, unitPrice, amount shared.Money, taxRate *big.Rat, opts ...LineItemOption) (LineItem, error)

type LineItemOption func(*LineItem)
func WithPriceID(id shared.PriceID) LineItemOption

type Invoice struct {
    id                shared.InvoiceID
    invoiceNumber     string
    accountID         shared.AccountID
    contractID        shared.ContractID
    lineItems         []LineItem
    subtotal          shared.Money
    taxAmount         shared.Money
    discountAmount    shared.Money
    total             shared.Money              // 割引・税込み合計
    appliedBalance    shared.Money              // クレジット台帳から充当された金額
    amountDue         shared.Money              // 実請求額（total - appliedBalance）
    paidAmount        shared.Money              // 入金済み金額
    balance           shared.Money              // 未払い残高（amountDue - paidAmount）
    status            InvoiceStatus
    billingPeriod     shared.DateRange
    issueDate         time.Time
    dueDate           time.Time
    paidAt            *time.Time
    paymentMethodID   *string                   // 請求書レベルの決済手段ID上書き
    metadata          map[string]string
    allowPartialPay   bool                      // 部分入金許可

    // リビジョンサポート: void-and-recreate ワークフロー用の2レベルリンク
    // originalInvoiceID はリビジョンチェーンのルート（最初の請求書）を指す
    // revisionOf は直接の親（チェーン内の前バージョン）を指す
    originalInvoiceID *shared.InvoiceID
    revisionOf        *shared.InvoiceID
    voidReason        string
    refundReason      string
}

// コンストラクタ
func NewInvoice(
    id shared.InvoiceID,
    accountID shared.AccountID,
    contractID shared.ContractID,
    subtotal shared.Money,
    discountAmount shared.Money,
    taxAmount shared.Money,
    opts ...InvoiceOption,
) *Invoice

// Functional Options
type InvoiceOption func(*Invoice)
func WithStatus(s InvoiceStatus) InvoiceOption
func WithBillingPeriod(p shared.DateRange) InvoiceOption
func WithDueDate(t time.Time) InvoiceOption
func WithIssueDate(t time.Time) InvoiceOption
func WithAppliedBalance(c shared.Money) InvoiceOption
func WithAmountDue(a shared.Money) InvoiceOption
func WithAllowPartialPayment(allow bool) InvoiceOption
func WithLineItems(items []LineItem) InvoiceOption
func WithInvoiceNumber(n string) InvoiceOption
func WithPaymentMethodID(id *string) InvoiceOption
func WithOriginalInvoiceID(id shared.InvoiceID) InvoiceOption
func WithRevisionOf(id shared.InvoiceID) InvoiceOption

// 状態遷移メソッド
func (inv *Invoice) Finalize() error
func (inv *Invoice) MarkIssued() error
func (inv *Invoice) MarkOverdue(now time.Time) error
func (inv *Invoice) RecordPayment(amount shared.Money, paidAt time.Time) error
func (inv *Invoice) ValidatePayment(amount shared.Money) error
func (inv *Invoice) Void() error
func (inv *Invoice) VoidWithReason(reason string) error
func (inv *Invoice) MarkRefunded(reason string) error

// リビジョンリンク設定
func (inv *Invoice) SetRevisionOf(id shared.InvoiceID)
func (inv *Invoice) SetOriginalInvoiceID(id shared.InvoiceID)

// Getters
func (inv *Invoice) OriginalInvoiceID() *shared.InvoiceID
func (inv *Invoice) RevisionOf() *shared.InvoiceID
func (inv *Invoice) VoidReason() string
func (inv *Invoice) RefundReason() string
func (inv *Invoice) PaymentMethodID() *string
```
// NOTE: metrics-invoicegen の InvoiceLineItem.Quantity は float64（小数量=0.5時間等の表現用）。
// ドメインモデル → 請求書ドキュメントの変換時に int64→float64 キャストを行う。

### 4.2 CreditNote（クレジットノート）

```go
// domain/invoice/credit_note.go
package invoice

import (
    "math/big"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// CreditNoteStatus クレジットノートのライフサイクルステータス
type CreditNoteStatus string

const (
    CreditNoteStatusDraft    CreditNoteStatus = "draft"
    CreditNoteStatusIssued   CreditNoteStatus = "issued"
    CreditNoteStatusApplied  CreditNoteStatus = "applied"
    CreditNoteStatusRefunded CreditNoteStatus = "refunded"
    CreditNoteStatusVoided   CreditNoteStatus = "voided"
)

// CreditNoteReason クレジットノートの発行理由
type CreditNoteReason string

const (
    CreditNoteReasonDuplicate             CreditNoteReason = "duplicate"
    CreditNoteReasonOrderChange           CreditNoteReason = "order_change"
    CreditNoteReasonCancellation          CreditNoteReason = "cancellation"
    CreditNoteReasonProductUnsatisfactory CreditNoteReason = "product_unsatisfactory"
    CreditNoteReasonOther                 CreditNoteReason = "other"
)

// CreditNoteItem は請求書明細単位の調整を表す
type CreditNoteItem struct {
    invoiceLineItemID string
    description       string
    amount            shared.Money
    taxRate           *big.Rat
    taxAmount         shared.Money
}

func NewCreditNoteItem(invoiceLineItemID, description string, amount shared.Money, taxRate *big.Rat, taxAmount shared.Money) CreditNoteItem

// CreditNote は発行済み請求書に対する調整を表すエンティティ
type CreditNote struct {
    id           shared.CreditNoteID
    number       string
    invoiceID    shared.InvoiceID
    accountID    shared.AccountID
    contractID   shared.ContractID
    status       CreditNoteStatus
    reason       CreditNoteReason
    memo         string
    items        []CreditNoteItem
    subtotal     shared.Money
    taxAmount    shared.Money
    total        shared.Money
    creditAmount shared.Money
    refundAmount shared.Money
    issuedAt     *time.Time
    createdAt    time.Time
}

// コンストラクタ
// Returns an error if items is empty.
func NewCreditNote(
    id shared.CreditNoteID,
    invoiceID shared.InvoiceID,
    accountID shared.AccountID,
    contractID shared.ContractID,
    reason CreditNoteReason,
    items []CreditNoteItem,
    opts ...CreditNoteOption,
) (*CreditNote, error)

type CreditNoteOption func(*CreditNote)
func WithCreditNoteMemo(memo string) CreditNoteOption
func WithCreditNoteNumber(number string) CreditNoteOption

// 状態遷移メソッド
func (cn *CreditNote) Issue(issuedAt time.Time) error
func (cn *CreditNote) Apply(creditAmount shared.Money) error
func (cn *CreditNote) Refund(refundAmount shared.Money) error
func (cn *CreditNote) Void() error
```

### 4.3 Invoice リポジトリインターフェース

```go
// domain/invoice/repository.go
package invoice

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

type Repository interface {
    Save(ctx context.Context, invoice *Invoice) error
    FindByID(ctx context.Context, id shared.InvoiceID) (*Invoice, error)
    FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*Invoice, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*Invoice, error)
    FindOverdue(ctx context.Context) ([]*Invoice, error)
    FindByStatus(ctx context.Context, status InvoiceStatus) ([]*Invoice, error)
    FindByContractAndStatus(ctx context.Context, contractID shared.ContractID, status InvoiceStatus) ([]*Invoice, error)
    FindByContractAndPeriod(ctx context.Context, contractID shared.ContractID, period shared.DateRange) ([]*Invoice, error)
    FindUnpaidByContract(ctx context.Context, contractID shared.ContractID) ([]*Invoice, error)

    // 時点指定
    FindByIDAsOf(ctx context.Context, id shared.InvoiceID, asOf time.Time) (*Invoice, error)
}
```

### 4.4 CreditNote リポジトリインターフェース

```go
// domain/invoice/credit_note_repository.go
package invoice

import (
    "context"

    "github.com/contract-to-cash/core/domain/shared"
)

type CreditNoteRepository interface {
    Save(ctx context.Context, cn *CreditNote) error
    FindByID(ctx context.Context, id shared.CreditNoteID) (*CreditNote, error)
    FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*CreditNote, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*CreditNote, error)
    FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*CreditNote, error)
    FindByStatus(ctx context.Context, status CreditNoteStatus) ([]*CreditNote, error)
}
```

## 5. Payment（支払い）

### 5.1 エンティティ定義

```go
// domain/payment/entity.go
package payment

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// PaymentID は shared/identifier.go で定義

type PaymentStatus string

const (
    PaymentStatusPending           PaymentStatus = "pending"            // 処理中
    PaymentStatusCompleted         PaymentStatus = "completed"          // 支払い完了
    PaymentStatusFailed            PaymentStatus = "failed"             // 支払い失敗
    PaymentStatusPartiallyRefunded PaymentStatus = "partially_refunded" // 一部返金済み
    PaymentStatusRefunded          PaymentStatus = "refunded"           // 全額返金済み
    PaymentStatusChargedBack       PaymentStatus = "charged_back"       // チャージバック
)

// PaymentStatus 状態遷移ルール:
//   pending    → completed | failed
//   completed  → partially_refunded | refunded | charged_back
//   partially_refunded → refunded（残額返金時）
//   failed     → pending（リトライ時。DunningConfig.MaxRetries に達した場合は failed のまま終端）
//   charged_back, refunded → 終端状態（遷移なし）

type PaymentMethod string

const (
    PaymentMethodCreditCard  PaymentMethod = "credit_card"
    PaymentMethodBankTransfer PaymentMethod = "bank_transfer"
    PaymentMethodDirectDebit PaymentMethod = "direct_debit"
    PaymentMethodConvenience PaymentMethod = "convenience_store"  // コンビニ払い
    PaymentMethodCarrier     PaymentMethod = "carrier"            // キャリア決済
)

type Payment struct {
    id                   shared.PaymentID
    invoiceID            shared.InvoiceID
    amount               shared.Money
    refundedAmount       shared.Money      // 返金累計額
    method               PaymentMethod
    status               PaymentStatus
    gatewayTransactionID string            // 外部決済サービスのトランザクションID
    idempotencyKey       string            // リトライ時の重複防止キー
    failureReason        *string
    processedAt          time.Time
    metadata             map[string]string
}

// コンストラクタ
func NewPayment(
    id shared.PaymentID,
    invoiceID shared.InvoiceID,
    amount shared.Money,
    method PaymentMethod,
    gatewayTransactionID string,
    processedAt time.Time,
) *Payment

// 状態遷移メソッド
func (p *Payment) Complete() error
func (p *Payment) Fail(reason string) error
func (p *Payment) MarkRefunded() error
func (p *Payment) MarkPartiallyRefunded() error
func (p *Payment) MarkChargedBack() error

// RecordRefund は返金額を記録し、累計に基づいてステータスを自動更新する。
// MarkRefunded/MarkPartiallyRefunded の代替として推奨。
func (p *Payment) RecordRefund(amount shared.Money) error

func (p *Payment) SetIdempotencyKey(key string)
```

### 5.2 Payment リポジトリインターフェース

```go
// domain/payment/repository.go
package payment

import (
    "context"

    "github.com/contract-to-cash/core/domain/shared"
)

type Repository interface {
    Save(ctx context.Context, payment *Payment) error
    FindByID(ctx context.Context, id shared.PaymentID) (*Payment, error)
    FindByInvoiceID(ctx context.Context, invoiceID shared.InvoiceID) ([]*Payment, error)
    // FindByIdempotencyKey はリトライ時の重複防止に使用。見つからない場合は nil を返す。
    FindByIdempotencyKey(ctx context.Context, key string) (*Payment, error)
}
```

### 5.3 支払い失敗時のリトライ（Dunning）

```go
// domain/payment/dunning.go
package payment

import "time"

// DunningConfig 支払い失敗時の回収設定
type DunningConfig struct {
    MaxRetries     int           // 最大リトライ回数
    RetryIntervals []time.Duration // リトライ間隔
    Actions        []DunningAction // 各ステップでのアクション
}

type DunningAction struct {
    AttemptNumber int
    Action        DunningActionType
    Template      string  // 通知テンプレートID
}

type DunningActionType string

const (
    DunningActionRetry           DunningActionType = "retry"
    DunningActionNotify          DunningActionType = "notify"
    DunningActionSuspendContract DunningActionType = "suspend_contract"
    DunningActionCancelContract  DunningActionType = "cancel_contract"
)
```

> **注**: Payment Gateway インターフェース（PaymentGateway, CustomerGateway, WebhookHandler）は
> `application/port/` パッケージに配置。`domain/payment/` にはエンティティ・イベント・
> リポジトリIFのみ残す。詳細は `payment-gateway.md` を参照。

## 6. Usage（従量課金）

### 6.1 エンティティ定義

```go
// domain/usage/entity.go
package usage

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// UsageRecordID は shared/identifier.go で定義

type UsageRecord struct {
    id             shared.UsageRecordID
    contractID     shared.ContractID
    metricName     string       // 例: "api_calls", "storage_gb", "active_users"
    quantity       int64
    timestamp      time.Time
    metadata       map[string]string
    idempotencyKey string       // 重複登録防止
}

// NewUsageRecord creates a new UsageRecord. Returns an error if quantity is negative.
func NewUsageRecord(
    id shared.UsageRecordID,
    contractID shared.ContractID,
    metricName string,
    quantity int64,
    timestamp time.Time,
    idempotencyKey string,
) (*UsageRecord, error)

// Getters
func (r *UsageRecord) ID() shared.UsageRecordID
func (r *UsageRecord) ContractID() shared.ContractID
func (r *UsageRecord) MetricName() string
func (r *UsageRecord) Quantity() int64
func (r *UsageRecord) Timestamp() time.Time
func (r *UsageRecord) Metadata() map[string]string
func (r *UsageRecord) IdempotencyKey() string

// UsageSummary 集計結果
type UsageSummary struct {
    ContractID shared.ContractID
    MetricName string
    Period     shared.DateRange
    TotalUsage int64
}
```

### 6.2 リポジトリインターフェース

```go
// domain/usage/repository.go
package usage

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

type Repository interface {
    Record(ctx context.Context, record *UsageRecord) error
    GetSummary(ctx context.Context, contractID shared.ContractID, metric string, period shared.DateRange) (*UsageSummary, error)
    GetRecords(ctx context.Context, contractID shared.ContractID, metric string, from, to time.Time) ([]*UsageRecord, error)
}
```

## 7. Pricing（料金モデル）

### 7.1 BillingInterval / BillingCycle

課金サイクルの新 API は `BillingInterval`（`{unit, count}` の値オブジェクト）。
`contract` パッケージはこれをエイリアスとして参照する（セクション6.1参照）。
`BillingCycle`（文字列）は `pricing` パッケージに残っており、`Price` 構築・表示・
アダプタコードで使うが、内部では `BillingCycleToInterval` で `BillingInterval` に変換される。
新規コードでは `BillingInterval` を使う（#111）。

```go
// domain/pricing/price.go
package pricing

// BillingInterval は課金サイクルを {unit, count} で表す値オブジェクト（新 API）。
type BillingInterval struct { /* unit, count */ }

// BillingCycle は請求サイクルを表す文字列型。pricing パッケージに残存し、
// Price 構築・表示・アダプタ向けに使う。BillingCycleToInterval で BillingInterval に変換される。
type BillingCycle string

const (
    BillingCycleDaily   BillingCycle = "daily"
    BillingCycleWeekly  BillingCycle = "weekly"
    BillingCycleMonthly BillingCycle = "monthly"
    BillingCycleYearly  BillingCycle = "yearly"
)

// BillingCycleToInterval は文字列サイクルを BillingInterval に変換する。
func BillingCycleToInterval(c BillingCycle) BillingInterval
```

### 7.2 Price（価格）

```go
// domain/pricing/price.go
package pricing

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

type PriceStatus string

const (
    PriceStatusActive   PriceStatus = "active"
    PriceStatusArchived PriceStatus = "archived"
)

// Price は「どう課金するか」を表すイミュータブルなエンティティ。
// 作成後は amount, currency, interval, pricingModel を変更できない。
// 価格改定時は新しい Price を作成する。
type Price struct {
    id           shared.PriceID
    productID    shared.ProductID
    amount       shared.Money
    currency     shared.Currency
    interval     BillingInterval  // 課金サイクル（新 API）。旧 billingCycle は #111 で撤去
    pricingModel PricingModel
    status       PriceStatus
    createdAt    time.Time
}

// NewPrice は後方互換の BillingCycle 文字列を受け取り、内部で BillingInterval に変換する。
func NewPrice(
    productID shared.ProductID,
    amount shared.Money,
    currency shared.Currency,
    billingCycle BillingCycle,
    pricingModel PricingModel,
    createdAt time.Time,
) *Price

// NewPriceWithInterval は BillingInterval を直接受け取る（新規コード推奨）。
func NewPriceWithInterval(
    productID shared.ProductID,
    amount shared.Money,
    currency shared.Currency,
    interval BillingInterval,
    pricingModel PricingModel,
    createdAt time.Time,
) *Price

func (p *Price) ID() shared.PriceID
func (p *Price) ProductID() shared.ProductID
func (p *Price) Amount() shared.Money
func (p *Price) Currency() shared.Currency
func (p *Price) Interval() BillingInterval // 課金サイクル（新 API）
// BillingCycle は interval から導出した文字列を返す。
// 完全一致する BillingCycle がない interval（例: quarterly）では "" を返す。
func (p *Price) BillingCycle() BillingCycle
func (p *Price) PricingModel() PricingModel
func (p *Price) Status() PriceStatus
func (p *Price) CreatedAt() time.Time
func (p *Price) Archive() error
```

### 7.3 PriceRepository

```go
// domain/pricing/price.go
package pricing

type PriceRepository interface {
    FindByID(ctx context.Context, id shared.PriceID) (*Price, error)
    FindByProductID(ctx context.Context, productID shared.ProductID) ([]*Price, error)
    FindActiveByProductID(ctx context.Context, productID shared.ProductID) ([]*Price, error)
    Save(ctx context.Context, price *Price) error
}
```

### 7.4 PricingModel

```go
// domain/pricing/model.go
package pricing

import (
    "github.com/contract-to-cash/core/domain/shared"
)

type PricingModel interface {
    CalculatePrice(usage int64) shared.Money
}

// UsageMetric 従量課金メトリクス定義
// Deprecated: プロダクト定義には product.UsageMetric を使用すること。
// 従量料金計算で PricingModel を保持するためこの型は残存する。
type UsageMetric struct {
    Name             string
    PricingModel     PricingModel
    IncludedQuantity int64
}

// Feature 機能定義
// Deprecated: product.Feature を使用すること。
type Feature struct {
    Name     string
    Included bool
    Limit    *int64
}
```

```go
// FlatPrice 固定料金
type FlatPrice struct {
    Price shared.Money
}

func (p FlatPrice) CalculatePrice(usage int64) shared.Money {
    return p.Price
}

// TieredPrice 段階料金
type TieredPrice struct {
    Tiers []PriceTier
    Mode  TieredPricingMode
}

type TieredPricingMode string

const (
    // TieredPricingGraduated 段階別課金（デフォルト・推奨）
    // 各段階に該当する使用量にその段階の単価を適用する
    // 例: 0-100回@¥10 + 101-500回@¥8 → 250回 = (100×¥10)+(150×¥8) = ¥2,200
    TieredPricingGraduated TieredPricingMode = "graduated"

    // TieredPricingVolume 全量課金
    // 到達した段階の単価を全使用量に適用する
    // 例: 0-100回@¥10, 101-500回@¥8 → 250回 = 250×¥8 = ¥2,000
    // 注意: クリフエッジ問題（100回=¥1,000 > 101回=¥808）が発生しうる
    TieredPricingVolume TieredPricingMode = "volume"
)

type PriceTier struct {
    UpTo      int64        // この数量まで（0 = 無制限）
    UnitPrice shared.Money
    FlatFee   shared.Money // この段階の固定料金
}

// NewTieredPrice は検証付きコンストラクタ（issue #156）。
// 以下の不変条件を検証し、違反時は shared.DomainError を返す:
//   - tier が1つ以上ある（validation_error）
//   - mode が graduated / volume のいずれか（validation_error）
//   - tier が UpTo 昇順に厳密ソート済み（validation_error）
//   - UpTo==0（無制限）は最終 tier のみ、非最終 tier は UpTo>0（validation_error）
//   - 全 tier の UnitPrice / FlatFee が単一通貨（currency_mismatch）
// 未ソート tier による負の課金や、通貨混在による ¥0 黙殺を構築時に排除する。
// 公開フィールドは後方互換のため書き込み可能だが、リポジトリ内の構築は
// すべて本コンストラクタを経由する。
func NewTieredPrice(tiers []PriceTier, mode TieredPricingMode) (TieredPrice, error)

// CalculatePrice は上記不変条件を前提とする。コンストラクタを迂回した
// 通貨混在 tier では、¥0 を黙って返さず panic する（assertNonNegativeUsage と同方針）。
// 履歴価格の復元（Price.FromSnapshot）は本コンストラクタを通らず、保存済み
// PricingModel をそのまま透過するため常にロード可能（リプレイ安全性）。
func (p TieredPrice) CalculatePrice(usage int64) shared.Money

// UsagePrice 従量料金
type UsagePrice struct {
    UnitPrice shared.Money
    Minimum   *shared.Money // 最低料金
    Maximum   *shared.Money // 最大料金（上限）
}

func (p UsagePrice) CalculatePrice(usage int64) shared.Money
```

## 8. Product（プロダクト）

```go
// domain/product/entity.go
package product

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// ProductStatus プロダクトのライフサイクルステータス
type ProductStatus string

const (
    ProductStatusActive   ProductStatus = "active"
    ProductStatusArchived ProductStatus = "archived"
)

// Feature プロダクト機能定義
type Feature struct {
    Name     string
    Included bool
    Limit    *int64
}

// UsageMetric プロダクト従量課金メトリクス定義
type UsageMetric struct {
    Name             string
    IncludedQuantity int64
}

// Product は「何を売るか」を表すエンティティ。
// 「どう課金するか」は pricing.Price で定義する。
type Product struct {
    id           shared.ProductID
    name         string
    description  string
    features     []Feature
    usageMetrics []UsageMetric
    status       ProductStatus
    metadata     map[string]string
    createdAt    time.Time
}

func NewProduct(name string, description string) *Product

func (p *Product) ID() shared.ProductID
func (p *Product) Name() string
func (p *Product) Description() string
func (p *Product) Status() ProductStatus
func (p *Product) CreatedAt() time.Time
func (p *Product) Features() []Feature
func (p *Product) UsageMetrics() []UsageMetric
func (p *Product) Metadata() map[string]string

func (p *Product) AddFeature(f Feature)
func (p *Product) AddUsageMetric(m UsageMetric)
func (p *Product) SetMetadata(key, value string)
func (p *Product) Archive() error
```

### 8.1 Product リポジトリインターフェース

```go
// domain/product/repository.go
package product

import (
    "context"

    "github.com/contract-to-cash/core/domain/shared"
)

type Repository interface {
    FindByID(ctx context.Context, id shared.ProductID) (*Product, error)
    Save(ctx context.Context, product *Product) error
}
```

## 9. ドメインサービス

### 9.1 請求計算サービス

```go
// domain/billing/service.go
package billing

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// Calculator 請求計算ドメインサービス
// contract と invoice を橋渡しする（両方のドメインを参照してよい唯一のドメインサービス）
// Clock IF を DI で受け取り、日割り計算等の時刻判定に使用する
type Calculator interface {
    // GenerateInvoice 契約から請求書を生成
    GenerateInvoice(ctx context.Context, contractID shared.ContractID) error

    // CalculateProration 日割り計算
    CalculateProration(ctx context.Context, contractID shared.ContractID, newPrice shared.Money) (*ProrationResult, error)
}

type ProrationResult struct {
    CreditAmount     shared.Money // 旧価格残日数分（内訳記録用）
    ChargeAmount     shared.Money // 新価格残日数分（内訳記録用）
    AdjustmentAmount shared.Money // 日割り調整額（ChargeAmount - CreditAmount）
    // AdjustmentAmount > 0: 追加請求（アップグレード）
    // AdjustmentAmount < 0: 次回請求からクレジット差引（ダウングレード）
    // AdjustmentAmount = 0: 精算不要
    EffectiveDate time.Time
}

// NewProrationResult CreditとChargeからAdjustmentAmountを自動算出
func NewProrationResult(credit, charge shared.Money, effectiveDate time.Time) (*ProrationResult, error)
// 決済時の動作:
//   アップグレード（Adjustment > 0）→ AdjustmentAmount のみ1回請求
//   ダウングレード（Adjustment < 0）→ BalancePolicy に従って処理（後述）
//   同額価格変更（Adjustment = 0）→ 決済なし
```

> **注**: 旧 `domain/contract/engine.go`（`Engine`, `SubscriptionEngine`, `UsageBasedEngine`）は
> 削除済み。契約タイプ別の処理ロジックは `application/service/billing_service.go` の
> `calculateSubtotal()` に移動している（`plugin-system.md` セクション8参照）。

## 10. クレジット台帳（Credit Ledger）

価格変更（ダウングレード）、手動調整、返金のクレジット変換等で発生する
預かり金（クレジット残高）を管理する。

### 10.1 クレジットポリシー

利用者がクレジットの扱いを設定できる。

```go
// domain/balance/policy.go
package balance

// BalancePolicy クレジット発生時のポリシー
type BalancePolicy string

const (
    // BalancePolicyLedger クレジット台帳に積み、次回以降の請求書で自動差引
    BalancePolicyLedger BalancePolicy = "ledger"

    // BalancePolicyRefund 即座に元の決済手段に返金
    BalancePolicyRefund BalancePolicy = "refund"

    // BalancePolicyNone クレジットを発生させない（差額は切り捨て）
    BalancePolicyNone BalancePolicy = "none"
)

// BalanceConfig クレジット設定
// Account.BillingInfo に含める。未設定の場合はグローバルデフォルトを使用。
type BalanceConfig struct {
    DowngradePolicy    BalancePolicy
    CancellationPolicy BalancePolicy
    AllowManualRefund  bool
    ExpirationDays     int
}
```

### 10.2 クレジットエントリ

```go
// domain/balance/entity.go
package balance

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// BalanceEntryID は shared/identifier.go で定義

// BalanceEntry クレジット台帳の1エントリ
type BalanceEntry struct {
    id              shared.BalanceEntryID
    accountID       shared.AccountID
    originalAmount  shared.Money      // 発生時の金額
    remainingAmount shared.Money      // 未使用残高
    reason          BalanceReason      // 発生理由
    sourceType      string            // 発生元の種類（"proration", "manual", "refund_conversion"）
    sourceID        string            // 発生元ID（ProrationResult ID, 管理者操作ID等）
    description     string            // 説明（「Pro価格→Basic価格への日割り調整」等）
    expiresAt       *time.Time        // 有効期限（nil = 無期限）
    createdAt       time.Time
    version         int               // Consume() のたびにインクリメント。楽観的ロック用。
    loadedVersion   int               // ロード時のバージョン。Save時に比較してコンフリクト検出。
}

type BalanceReason string

const (
    BalanceReasonProration        BalanceReason = "proration"          // 価格変更の日割り差額
    BalanceReasonCancellation     BalanceReason = "cancellation"       // 解約時の未使用期間
    BalanceReasonManualAdjustment BalanceReason = "manual_adjustment"  // 手動調整（CS対応等）
    BalanceReasonRefundConversion BalanceReason = "refund_conversion"  // 返金→クレジット変換
    BalanceReasonGoodwill         BalanceReason = "goodwill"           // お詫び・補填
)

// NewBalanceEntry creates a new BalanceEntry.
// createdAt は Clock.Now() 経由で呼び出し元が提供すること。
func NewBalanceEntry(accountID shared.AccountID, amount shared.Money, reason BalanceReason, createdAt time.Time) *BalanceEntry

// Consume は残高を消費し、実際に消費された金額を返す。
// 残高不足の場合は残高分のみ消費する（min(remainingAmount, amount)）。
// 消費が発生した場合は version をインクリメントする。
func (e *BalanceEntry) Consume(amount shared.Money) (shared.Money, error)

// MarkExpired は期限切れエントリの残高を没収し、没収額を返す（issue #159）。
// 「失効」= remainingAmount のゼロ化（status フィールドは持たない。ゼロ残高は
// IsFullyConsumed / FindAvailable / GetBalance すべてで不活性になる）。
// originalAmount と expiresAt は監査用に保持される。
// ガード: expiresAt 未設定・未失効は business_rule エラー。全消費済みは
// ゼロ没収・version 非バンプの冪等 no-op（バッチ再実行安全）。
// 没収が発生した場合は Consume 同様 version をインクリメントする（楽観ロック）。
// 発火は batch.BalanceExpirationProcessor（スケジューラは利用者側）。
func (e *BalanceEntry) MarkExpired(now time.Time) (shared.Money, error)

func (e *BalanceEntry) IsExpired(now time.Time) bool
func (e *BalanceEntry) IsFullyConsumed() bool
func (e *BalanceEntry) Version() int
func (e *BalanceEntry) SetVersion(v int)        // リポジトリ実装がロード後に呼び出す
func (e *BalanceEntry) LoadedVersion() int
```

### 10.3 クレジット適用記録

```go
// domain/balance/application.go
package balance

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// BalanceApplication クレジットの消費記録
// どのクレジットが、どの請求書で、いくら使われたかを追跡
type BalanceApplication struct {
    ID             string
    BalanceEntryID shared.BalanceEntryID     // 消費元のクレジット
    InvoiceID      shared.InvoiceID          // 適用先の請求書
    Amount         shared.Money              // 適用額
    AppliedAt      time.Time
}

// BalanceRefund クレジット残高からの返金記録
// BalanceConfig.AllowManualRefund = true の場合のみ作成可能
type BalanceRefund struct {
    ID             string
    BalanceEntryID shared.BalanceEntryID
    AccountID      shared.AccountID
    Amount         shared.Money
    RefundedAt     time.Time
}
```

### 10.4 リポジトリインターフェース

```go
// domain/balance/repository.go
package balance

import (
    "context"

    "github.com/contract-to-cash/core/domain/shared"
)

type Repository interface {
    Save(ctx context.Context, entry *BalanceEntry) error
    FindByID(ctx context.Context, id shared.BalanceEntryID) (*BalanceEntry, error)

    // FindAvailable 有効なクレジット残高を持つエントリを取得
    // 指定通貨のみ、有効期限内、古い順（FIFO消費）
    FindAvailable(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)

    // GetBalance アカウントのクレジット残高合計
    // 有効期限内（expiresAt が nil または now より後）かつ
    // remainingAmount > 0 のエントリのみを集計する。
    GetBalance(ctx context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error)

    // 適用記録
    SaveApplication(ctx context.Context, app *BalanceApplication) error
    FindApplicationsByInvoice(ctx context.Context, invoiceID shared.InvoiceID) ([]*BalanceApplication, error)

    // 返金記録
    SaveRefund(ctx context.Context, refund *BalanceRefund) error

    // FindByAccountID 全エントリ取得（全消費・期限切れ含む、作成時刻昇順）
    FindByAccountID(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*BalanceEntry, error)

    // FindExpired 期限切れかつ残高が未没収（remainingAmount > 0）のエントリを
    // 作成時刻昇順で返す（issue #159）。batch.BalanceExpirationProcessor の
    // スキャンに使用。契約側の FindDueForRenewal に相当。
    FindExpired(ctx context.Context, asOf time.Time) ([]*BalanceEntry, error)
}
```

### 10.5 請求書生成時のクレジット適用フロー

```
請求計算（BillingService.GenerateInvoice）:
  1. 基本料金計算
  2. DiscountHook（クーポン等）
  3. TaxHook（税計算）
  4. 合計算出
  5. クレジット適用
  |   - FindAvailable(accountID, invoice.Currency()) で
  |     同一通貨かつ有効期限内のクレジットをFIFO取得
  |   - 古いクレジットから順に消費（有効期限切れはスキップ）
  |   - Invoice.appliedBalance に適用額を記録
  |   - BalanceApplication レコード作成
  |   - 適用額は min(entry.remainingAmount, 残り充当必要額) で算出
  |   - BalanceEntry.remainingAmount を減算
  6. 実請求額 = 合計 - クレジット適用額
  |   - 実請求額 > 0: 決済実行
  |   - 実請求額 = 0: 決済不要（全額クレジットで充当）
```

#### トランザクション戦略

クレジット適用は **BalanceEntry（Credit集約）** と **Invoice（Invoice集約）** を
跨ぐ操作であり、イベントソーシングの「1トランザクション = 1集約」原則と緊張関係にある。

本プロジェクトでは **簡易CQRS（同一DB）** を採用しているため、以下の方式を適用する:

**方式: アプリケーションサービス層での同一DBトランザクション**

```go
// application/service/billing_service.go
func (s *BillingService) applyCredits(ctx context.Context, tx *sql.Tx, invoice *invoice.Invoice) error {
    // 同一DBトランザクション内で以下を実行:
    // 1. BalanceEntry.remainingAmount を減算（SELECT FOR UPDATE でロック）
    // 2. BalanceApplication レコードを作成
    // 3. Invoice.appliedBalance / amountDue を更新
    //
    // 同一DBを使用するため、通常のDBトランザクション(BEGIN/COMMIT)で
    // アトミック性を保証できる。イベントソーシングのイベント追加も
    // 同一トランザクション内で行われる。
    ...
}
```

**将来のフルCQRS移行時の移行パス:**

フルCQRS（別DB）に移行する場合は、以下の Saga / Process Manager パターンに置き換える:
1. `InvoiceFinalizedEvent` を発行
2. `BalanceApplicationSaga` がイベントを受信し、クレジット適用コマンドを発行
3. 成功時: `CreditAppliedEvent` → Invoice の amountDue を更新
4. 失敗時: 補償トランザクション（クレジット適用取消）を実行

現時点では簡易CQRS前提のため、DBトランザクション方式で十分である。

### 10.6 ダウングレード時のフロー（BalancePolicy別）

```
ProrationResult.AdjustmentAmount < 0（ダウングレード）
  |
  +-- BalancePolicyLedger（デフォルト）
  |   -> BalanceEntry 作成（reason: proration）
  |   -> 次回以降の請求書で自動差引
  |
  +-- BalancePolicyRefund
  |   -> PaymentGateway.Refund() で即時返金
  |   -> 元の決済手段に返金
  |
  +-- BalancePolicyNone
      -> 何もしない（差額は切り捨て）
```
