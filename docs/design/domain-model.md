# ドメインモデル設計

## 1. 共通値オブジェクト

### 1.1 Money（金額）

```go
// domain/shared/money.go
package shared

import (
    "errors"
    "math/big"
)

// Money は通貨と金額を表現する値オブジェクト
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

func NewMoney(amount *big.Rat, currency Currency) Money {
    return Money{amount: amount, currency: currency}
}

func (m Money) Add(other Money) (Money, error) {
    if m.currency != other.currency {
        return Money{}, errors.New("currency mismatch")
    }
    result := new(big.Rat).Add(m.amount, other.amount)
    return NewMoney(result, m.currency), nil
}

func (m Money) Subtract(other Money) (Money, error) {
    if m.currency != other.currency {
        return Money{}, errors.New("currency mismatch")
    }
    result := new(big.Rat).Sub(m.amount, other.amount)
    return NewMoney(result, m.currency), nil
}

func (m Money) Multiply(factor *big.Rat) Money {
    result := new(big.Rat).Mul(m.amount, factor)
    return NewMoney(result, m.currency)
}

func (m Money) IsNegative() bool {
    return m.amount != nil && m.amount.Sign() < 0
}

func (m Money) IsZero() bool {
    return m.amount == nil || m.amount.Sign() == 0
}

func (m Money) GreaterThan(other Money) bool {
    if m.amount == nil || other.amount == nil {
        return false
    }
    return m.amount.Cmp(other.amount) > 0
}

func (m Money) Amount() *big.Rat {
    if m.amount == nil {
        return new(big.Rat)
    }
    return m.amount
}

func (m Money) Currency() Currency {
    return m.currency
}

// Zero 指定通貨のゼロ金額を生成する
// var totalDiscount shared.Money の代わりに使用し、nilポインタを回避する
func Zero(currency Currency) Money {
    return NewMoney(new(big.Rat), currency)
}
```

### 1.2 DateRange（期間）

```go
// domain/shared/datetime.go
package shared

import "time"

// DateRange は期間を表す値オブジェクト
type DateRange struct {
    start time.Time
    end   time.Time
}

func NewDateRange(start, end time.Time) (DateRange, error) {
    if end.Before(start) {
        return DateRange{}, errors.New("end must be after start")
    }
    return DateRange{start: start.UTC(), end: end.UTC()}, nil
}

func (r DateRange) Contains(t time.Time) bool {
    return !t.Before(r.start) && !t.After(r.end)
}

func (r DateRange) Duration() time.Duration {
    return r.end.Sub(r.start)
}
```

## 2. Account（アカウント）

```go
// domain/account/entity.go
package account

import (
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/shared"
)

type AccountID string

type Account struct {
    id          AccountID
    name        string
    email       string
    billingInfo BillingInfo
    createdAt   time.Time
    updatedAt   time.Time
}

type BillingInfo struct {
    Address      Address
    TaxID        string
    PaymentTerms int // 支払い期限（日数）
}

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
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/account"
    "github.com/yourorg/contract-billing-core/domain/shared"
)

type ContractID string

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

type Contract struct {
    id              ContractID
    accountID       account.AccountID
    planID          string
    status          ContractStatus
    contractType    ContractType
    billingCycle    BillingCycle
    currentPeriod   shared.DateRange
    trialConfig     *TrialConfiguration  // トライアル設定（オプショナル）
    suspensionConfig *SuspensionConfiguration
    price           shared.Money         // サブスクリプション/買い切りの固定料金
    basePrice       shared.Money         // ハイブリッド課金の固定部分（従量課金のみの場合はゼロ）
    plan            *pricing.Plan        // 料金プラン（UsageMetric含む）
    metadata        map[string]string
    createdAt       time.Time
    updatedAt       time.Time
    version         int
}

// BillingCycle 請求サイクル
type BillingCycle string

const (
    BillingCycleDaily   BillingCycle = "daily"
    BillingCycleWeekly  BillingCycle = "weekly"
    BillingCycleMonthly BillingCycle = "monthly"
    BillingCycleYearly  BillingCycle = "yearly"
)
```

### 3.2 トライアル設定

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

### 3.3 一時停止設定

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

### 3.4 日割り計算設定

```go
// domain/contract/proration.go
package contract

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

// ProrationConfig 日割り計算設定
type ProrationConfig struct {
    Behavior       ProrationBehavior
    RoundingMode   RoundingMode
}

type RoundingMode string

const (
    RoundingModeUp   RoundingMode = "up"
    RoundingModeDown RoundingMode = "down"
    RoundingModeHalfUp RoundingMode = "half_up"
)
```

### 3.5 リポジトリインターフェース

```go
// domain/contract/repository.go
package contract

import (
    "context"
    "time"
)

// Repository 契約リポジトリインターフェース
type Repository interface {
    // 基本CRUD
    Save(ctx context.Context, contract *Contract) error
    FindByID(ctx context.Context, id ContractID) (*Contract, error)
    FindByAccountID(ctx context.Context, accountID account.AccountID) ([]*Contract, error)
    
    // クエリ
    FindActiveByPlanID(ctx context.Context, planID string) ([]*Contract, error)
    FindExpiring(ctx context.Context, before time.Time) ([]*Contract, error)
    FindTrialsEndingSoon(ctx context.Context, within time.Duration) ([]*Contract, error)
    
    // 時点指定（イベントソーシング）
    FindByIDAsOf(ctx context.Context, id ContractID, asOf time.Time) (*Contract, error)
}
```

## 4. Invoice（請求書）

### 4.1 エンティティ定義

```go
// domain/invoice/entity.go
package invoice

import (
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/account"
    "github.com/yourorg/contract-billing-core/domain/contract"
    "github.com/yourorg/contract-billing-core/domain/shared"
)

type InvoiceID string

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

type Invoice struct {
    id                InvoiceID
    invoiceNumber     string                    // 請求書番号
    accountID         account.AccountID
    contractID        contract.ContractID
    lineItems         []LineItem
    subtotal          shared.Money
    taxAmount         shared.Money
    discountAmount    shared.Money
    total             shared.Money
    paidAmount        shared.Money              // 入金済み金額
    balance           shared.Money              // 残高
    status            InvoiceStatus
    billingPeriod     shared.DateRange
    issueDate         time.Time
    dueDate           time.Time
    paidAt            *time.Time
    metadata          map[string]string
    allowPartialPay   bool                      // 部分入金許可
}

type LineItem struct {
    id          string
    description string
    quantity    int
    unitPrice   shared.Money
    amount      shared.Money
    taxRate     *big.Rat
    metadata    map[string]string
}
```

### 4.2 リポジトリインターフェース

```go
// domain/invoice/repository.go
package invoice

import (
    "context"
    "time"
)

type Repository interface {
    Save(ctx context.Context, invoice *Invoice) error
    FindByID(ctx context.Context, id InvoiceID) (*Invoice, error)
    FindByContractID(ctx context.Context, contractID contract.ContractID) ([]*Invoice, error)
    FindByAccountID(ctx context.Context, accountID account.AccountID) ([]*Invoice, error)
    FindOverdue(ctx context.Context) ([]*Invoice, error)
    FindByStatus(ctx context.Context, status InvoiceStatus) ([]*Invoice, error)
    
    // 時点指定
    FindByIDAsOf(ctx context.Context, id InvoiceID, asOf time.Time) (*Invoice, error)
}
```

## 5. Payment（支払い）

### 5.1 エンティティ定義

```go
// domain/payment/entity.go
package payment

import (
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/invoice"
    "github.com/yourorg/contract-billing-core/domain/shared"
)

type PaymentID string

type PaymentStatus string

const (
    PaymentStatusPending          PaymentStatus = "pending"           // 処理中
    PaymentStatusCompleted        PaymentStatus = "completed"         // 支払い完了
    PaymentStatusFailed           PaymentStatus = "failed"            // 支払い失敗
    PaymentStatusPartiallyRefunded PaymentStatus = "partially_refunded" // 一部返金済み
    PaymentStatusRefunded         PaymentStatus = "refunded"          // 全額返金済み
    PaymentStatusChargedBack      PaymentStatus = "charged_back"      // チャージバック
)

// PaymentStatus 状態遷移ルール:
//   pending    → completed | failed
//   completed  → partially_refunded | refunded | charged_back
//   partially_refunded → refunded（残額返金時）
//   failed     → pending（リトライ時）
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
    id                PaymentID
    invoiceID         invoice.InvoiceID
    amount            shared.Money
    method            PaymentMethod
    status            PaymentStatus
    gatewayTransactionID string  // 外部決済サービスのトランザクションID
    failureReason     *string
    processedAt       time.Time
    metadata          map[string]string
}
```

### 5.2 支払い失敗時のリトライ（Dunning）

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

## 6. Usage（従量課金）

### 6.1 エンティティ定義

```go
// domain/usage/entity.go
package usage

import (
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/contract"
)

type UsageRecordID string

type UsageRecord struct {
    id          UsageRecordID
    contractID  contract.ContractID
    metricName  string       // 例: "api_calls", "storage_gb", "active_users"
    quantity    int64
    timestamp   time.Time
    metadata    map[string]string
    idempotencyKey string   // 重複登録防止
}

// UsageSummary 集計結果
type UsageSummary struct {
    ContractID  contract.ContractID
    MetricName  string
    Period      shared.DateRange
    TotalUsage  int64
}
```

### 6.2 リポジトリインターフェース

```go
// domain/usage/repository.go
package usage

import (
    "context"
    "time"
)

type Repository interface {
    Record(ctx context.Context, record *UsageRecord) error
    GetSummary(ctx context.Context, contractID contract.ContractID, metric string, period shared.DateRange) (*UsageSummary, error)
    GetRecords(ctx context.Context, contractID contract.ContractID, metric string, from, to time.Time) ([]*UsageRecord, error)
}
```

## 7. PricingModel（料金モデル）

```go
// domain/pricing/model.go
package pricing

import (
    "github.com/yourorg/contract-billing-core/domain/shared"
)

type PlanID string

type Plan struct {
    id           PlanID
    name         string
    description  string
    pricingModel PricingModel     // 固定料金部分（FlatPrice等）
    usageMetrics []UsageMetric    // 従量課金メトリクス（ハイブリッド対応）
    features     []Feature
    metadata     map[string]string
}

// UsageMetric 従量課金メトリクス定義
type UsageMetric struct {
    Name             string       // メトリクス名（例: "api_calls", "storage_gb"）
    PricingModel     PricingModel // 料金モデル（TieredPrice, UsagePrice等）
    IncludedQuantity int64        // 含有枠（基本料金に含まれる無料枠。0 = 枠なし）
}

type PricingModel interface {
    CalculatePrice(usage int64) shared.Money
}

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

func (p TieredPrice) CalculatePrice(usage int64) shared.Money {
    if p.Mode == TieredPricingVolume {
        return p.calculateVolume(usage)
    }
    return p.calculateGraduated(usage)
}

func (p TieredPrice) calculateGraduated(usage int64) shared.Money {
    // 段階ごとに該当する使用量 × 単価を合算
    // ...
}

func (p TieredPrice) calculateVolume(usage int64) shared.Money {
    // 到達段階の単価 × 全使用量
    // ...
}

// UsagePrice 従量料金
type UsagePrice struct {
    UnitPrice shared.Money
    Minimum   *shared.Money // 最低料金
    Maximum   *shared.Money // 最大料金（上限）
}

func (p UsagePrice) CalculatePrice(usage int64) shared.Money {
    // 従量料金計算ロジック
    // ...
}

type Feature struct {
    Name     string
    Included bool
    Limit    *int64 // nil = 無制限
}
```

## 8. ドメインサービス

### 8.1 請求計算サービス

```go
// domain/billing/service.go
package billing

import (
    "context"
    
    "github.com/yourorg/contract-billing-core/domain/contract"
    "github.com/yourorg/contract-billing-core/domain/invoice"
    "github.com/yourorg/contract-billing-core/domain/usage"
)

// Calculator 請求計算サービス
type Calculator interface {
    CalculateInvoice(ctx context.Context, contract *contract.Contract) (*invoice.Invoice, error)
    CalculateProration(ctx context.Context, contract *contract.Contract, newPrice shared.Money) (*ProrationResult, error)
}

type ProrationResult struct {
    CreditAmount shared.Money  // 返金（クレジット）額
    ChargeAmount shared.Money  // 追加請求額
    EffectiveDate time.Time
}
```

### 8.2 契約エンジン

```go
// domain/contract/engine.go
package contract

import "context"

// Engine 契約タイプごとの処理エンジン
type Engine interface {
    // 請求書生成
    GenerateInvoice(ctx context.Context, contract *Contract) (*invoice.Invoice, error)
    
    // 更新処理
    Renew(ctx context.Context, contract *Contract) error
    
    // キャンセル処理
    Cancel(ctx context.Context, contract *Contract) error
}

// SubscriptionEngine サブスクリプション用エンジン
type SubscriptionEngine struct {
    // ...
}

// UsageBasedEngine 従量課金用エンジン
type UsageBasedEngine struct {
    usageRepo usage.Repository
    // ...
}
```
