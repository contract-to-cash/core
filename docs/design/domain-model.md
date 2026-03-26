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
type PlanID string
type CreditEntryID string

// ID生成ヘルパー
func NewAccountID() AccountID     { return AccountID(generateULID()) }
func NewContractID() ContractID   { return ContractID(generateULID()) }
func NewInvoiceID() InvoiceID     { return InvoiceID(generateULID()) }
func NewPaymentID() PaymentID     { return PaymentID(generateULID()) }
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
    CreditConfig *credit.CreditConfig // クレジット設定（nil = グローバルデフォルトを使用）
}
// NOTE: credit パッケージ（セクション9）の import が必要
// import "domain/credit"

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

    "github.com/contract-to-cash/core/domain/shared"
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

type Contract struct {
    id              shared.ContractID
    accountID       shared.AccountID
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
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*Contract, error)
    
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

type Invoice struct {
    id                InvoiceID
    invoiceNumber     string                    // 請求書番号
    accountID         shared.AccountID
    contractID        shared.ContractID
    lineItems         []LineItem
    subtotal          shared.Money
    taxAmount         shared.Money
    discountAmount    shared.Money
    total             shared.Money              // 割引・税込み合計
    appliedCredit     shared.Money              // クレジット台帳から充当された金額
    amountDue         shared.Money              // 実請求額（total - appliedCredit）
    paidAmount        shared.Money              // 入金済み金額
    balance           shared.Money              // 未払い残高（amountDue - paidAmount）
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
    FindByContractID(ctx context.Context, contractID shared.ContractID) ([]*Invoice, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*Invoice, error)
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
    
    "github.com/contract-to-cash/core/domain/shared"
)

// PaymentID は shared/identifier.go で定義

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
    invoiceID         shared.InvoiceID
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
    id          shared.UsageRecordID
    contractID  shared.ContractID
    metricName  string       // 例: "api_calls", "storage_gb", "active_users"
    quantity    int64
    timestamp   time.Time
    metadata    map[string]string
    idempotencyKey string   // 重複登録防止
}

// UsageSummary 集計結果
type UsageSummary struct {
    ContractID  shared.ContractID
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
    GetSummary(ctx context.Context, contractID shared.ContractID, metric string, period shared.DateRange) (*UsageSummary, error)
    GetRecords(ctx context.Context, contractID shared.ContractID, metric string, from, to time.Time) ([]*UsageRecord, error)
}
```

## 7. PricingModel（料金モデル）

```go
// domain/pricing/model.go
package pricing

import (
    "github.com/contract-to-cash/core/domain/shared"
)

// PlanID は shared/identifier.go で定義

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
    CreditAmount  shared.Money // 旧プラン残日数分（内訳記録用）
    ChargeAmount  shared.Money // 新プラン残日数分（内訳記録用）
    AdjustmentAmount shared.Money // 日割り調整額（ChargeAmount - CreditAmount）
    // AdjustmentAmount > 0: 追加請求（アップグレード）
    // AdjustmentAmount < 0: 次回請求からクレジット差引（ダウングレード）
    // AdjustmentAmount = 0: 精算不要
    EffectiveDate time.Time
}

// NewProrationResult CreditとChargeからAdjustmentAmountを自動算出
func NewProrationResult(credit, charge shared.Money, effectiveDate time.Time) *ProrationResult {
    return &ProrationResult{
        CreditAmount:  credit,
        ChargeAmount:  charge,
        AdjustmentAmount: charge.Sub(credit), // 差額のみ精算
        EffectiveDate: effectiveDate,
    }
}
// 決済時の動作:
//   アップグレード（Adjustment > 0）→ AdjustmentAmount のみ1回請求
//   ダウングレード（Adjustment < 0）→ CreditPolicy に従って処理（後述）
//   同額プラン変更（Adjustment = 0）→ 決済なし
```

> **注**: 旧 `domain/contract/engine.go`（`Engine`, `SubscriptionEngine`, `UsageBasedEngine`）は
> 削除済み。契約タイプ別の処理ロジックは `application/service/billing_service.go` の
> `calculateSubtotal()` に移動している（`plugin-system.md` セクション8参照）。

## 9. クレジット台帳（Credit Ledger）

プラン変更（ダウングレード）、手動調整、返金のクレジット変換等で発生する
預かり金（クレジット残高）を管理する。

### 9.1 クレジットポリシー

利用者がクレジットの扱いを設定できる。

```go
// domain/credit/policy.go
package credit

// CreditPolicy クレジット発生時のポリシー
type CreditPolicy string

const (
    // CreditPolicyLedger クレジット台帳に積み、次回以降の請求書で自動差引
    CreditPolicyLedger CreditPolicy = "ledger"

    // CreditPolicyRefund 即座に元の決済手段に返金
    CreditPolicyRefund CreditPolicy = "refund"

    // CreditPolicyNone クレジットを発生させない（差額は切り捨て）
    CreditPolicyNone CreditPolicy = "none"
)

// CreditConfig クレジット設定
// Account.BillingInfo に含める。未設定の場合はグローバルデフォルトを使用。
type CreditConfig struct {
    // DowngradePolicy ダウングレード時のクレジットポリシー
    // デフォルト: CreditPolicyLedger
    DowngradePolicy CreditPolicy

    // CancellationPolicy 解約時の未使用期間分のクレジットポリシー
    // デフォルト: CreditPolicyNone
    CancellationPolicy CreditPolicy

    // AllowManualRefund クレジット残高からの手動返金を許可するか
    // true: オペレーターがクレジット残高を返金に変換できる
    // false: クレジットは請求書差引のみ
    AllowManualRefund bool

    // ExpirationDays クレジットの有効期限（日数）
    // 0 = 無期限
    ExpirationDays int
}
```

### 9.2 クレジットエントリ

```go
// domain/credit/entity.go
package credit

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// CreditEntryID は shared/identifier.go で定義

// CreditEntry クレジット台帳の1エントリ
type CreditEntry struct {
    id              shared.CreditEntryID
    accountID       shared.AccountID
    originalAmount  shared.Money      // 発生時の金額
    remainingAmount shared.Money      // 未使用残高
    reason          CreditReason      // 発生理由
    sourceType      string            // 発生元の種類（"proration", "manual", "refund_conversion"）
    sourceID        string            // 発生元ID（ProrationResult ID, 管理者操作ID等）
    description     string            // 説明（「Proプラン→Basicプランへの日割り調整」等）
    expiresAt       *time.Time        // 有効期限（nil = 無期限）
    createdAt       time.Time
}

type CreditReason string

const (
    CreditReasonProration        CreditReason = "proration"          // プラン変更の日割り差額
    CreditReasonCancellation     CreditReason = "cancellation"       // 解約時の未使用期間
    CreditReasonManualAdjustment CreditReason = "manual_adjustment"  // 手動調整（CS対応等）
    CreditReasonRefundConversion CreditReason = "refund_conversion"  // 返金→クレジット変換
    CreditReasonGoodwill         CreditReason = "goodwill"           // お詫び・補填
)

// IsExpired 有効期限切れか判定
func (e *CreditEntry) IsExpired(now time.Time) bool {
    return e.expiresAt != nil && now.After(*e.expiresAt)
}

// IsFullyConsumed 全額消費済みか判定
func (e *CreditEntry) IsFullyConsumed() bool {
    return e.remainingAmount.IsZero()
}
```

### 9.3 クレジット適用記録

```go
// domain/credit/application.go
package credit

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// CreditApplication クレジットの消費記録
// どのクレジットが、どの請求書で、いくら使われたかを追跡
type CreditApplication struct {
    id            string
    creditEntryID shared.CreditEntryID     // 消費元のクレジット
    invoiceID     shared.InvoiceID  // 適用先の請求書
    amount        shared.Money      // 適用額
    appliedAt     time.Time
}

// CreditRefund クレジット残高からの返金記録
// CreditConfig.AllowManualRefund = true の場合のみ作成可能
// AllowManualRefund = false の場合、返金は CreditEntry の作成元（決済トランザクション）経由で行う
type CreditRefund struct {
    id            string
    creditEntryID shared.CreditEntryID
    accountID     shared.AccountID
    amount        shared.Money
    refundedAt    time.Time
}
```

### 9.4 リポジトリインターフェース

```go
// domain/credit/repository.go
package credit

import (
    "context"

    "github.com/contract-to-cash/core/domain/shared"
)

type Repository interface {
    Save(ctx context.Context, entry *CreditEntry) error
    FindByID(ctx context.Context, id shared.CreditEntryID) (*CreditEntry, error)

    // FindAvailable 有効なクレジット残高を持つエントリを取得
    // 指定通貨のみ、有効期限内、古い順（FIFO消費）
    FindAvailable(ctx context.Context, accountID shared.AccountID, currency shared.Currency) ([]*CreditEntry, error)

    // アカウントのクレジット残高合計
    GetBalance(ctx context.Context, accountID shared.AccountID, currency shared.Currency) (shared.Money, error)

    // 適用記録
    SaveApplication(ctx context.Context, app *CreditApplication) error
    FindApplicationsByInvoice(ctx context.Context, invoiceID shared.InvoiceID) ([]*CreditApplication, error)

    // 返金記録
    SaveRefund(ctx context.Context, refund *CreditRefund) error
}
```

### 9.5 請求書生成時のクレジット適用フロー

```
請求計算（BillingService.GenerateInvoice）:
  ① 基本料金計算
  ② DiscountHook（クーポン等）
  ③ TaxHook（税計算）
  ④ 合計算出
  ⑤ ★ クレジット適用 ← 新規ステップ
  │   - FindAvailable(accountID) で有効クレジットをFIFO取得
  │   - 古いクレジットから順に消費（有効期限切れはスキップ）
  │   - Invoice.appliedCredit に適用額を記録
  │   - CreditApplication レコード作成
  │   - CreditEntry.remainingAmount 減算と CreditApplication 作成は
  │     単一トランザクション内でアトミックに実行する
  │   - 適用額は min(entry.remainingAmount, 残り充当必要額) で算出
  │     （remainingAmount がマイナスになることを防止）
  │   - CreditEntry.remainingAmount を減算
  ⑥ 実請求額 = 合計 - クレジット適用額
  │   - 実請求額 > 0: 決済実行
  │   - 実請求額 = 0: 決済不要（全額クレジットで充当）
```

### 9.6 ダウングレード時のフロー（CreditPolicy別）

```
ProrationResult.AdjustmentAmount < 0（ダウングレード）
  │
  ├─ CreditPolicyLedger（デフォルト）
  │   → CreditEntry 作成（reason: proration）
  │   → 次回以降の請求書で自動差引
  │
  ├─ CreditPolicyRefund
  │   → PaymentGateway.Refund() で即時返金
  │   → 元の決済手段に返金
  │
  └─ CreditPolicyNone
      → 何もしない（差額は切り捨て）
```
