---
sidebar_label: Plugin System
---

# プラグインシステム設計

## 1. 概要

### 1.1 プラグインシステムの目的

契約決済システムでは、以下のような機能をプラガブルに拡張できる必要がある：

- **クーポン・割引** - 様々な割引ロジック
- **税計算** - 国・地域ごとの税計算
- **通知** - メール、Slack、SMS等
- **メトリクス収集** - 各種KPI計算
- **請求書生成** - PDF生成、電子帳簿保存法対応等

### 1.2 設計原則

1. **疎結合** - コアロジックとプラグインは明確に分離
2. **型安全** - インターフェースによる契約
3. **テスト容易性** - プラグインは個別にテスト可能
4. **実行順序制御** - プラグインの実行順序を制御可能

## 2. プラグインインターフェース

### 2.1 基本インターフェース

```go
// plugin/plugin.go
package plugin

import "context"

// Plugin プラグインの基本インターフェース
type Plugin interface {
    // Name プラグイン名を返す
    Name() string
    
    // Version プラグインバージョンを返す
    Version() string
    
    // Initialize プラグインを初期化する
    Initialize(ctx context.Context, config Config) error
    
    // Shutdown プラグインをシャットダウンする
    Shutdown(ctx context.Context) error
    
    // Priority 実行優先度を返す（小さいほど先に実行）
    Priority() int
}

// Config プラグイン設定
type Config map[string]interface{}

// 設定値の読み出しヘルパー（issue #239）。Initialize では生の型アサーション
// （config["key"].(int) 等）ではなくこれらを使う: JSON からロードした設定は
// encoding/json が数値を float64 でデコードするため素の .(int) にマッチせず、
// また present-but-mistyped な値は「黙って既定値のまま走る」のではなく
// エラーとして Initialize から返すべきであるため。
//   - 欠落キー: (zero, false, nil) — 欠落はエラーではない
//   - Int は int と「整数値の float64」を受理し、それ以外の型・非整数はエラー
func (c Config) Int(key string) (value int, present bool, err error)
func (c Config) Bool(key string) (value bool, present bool, err error)
```

### 2.2 計算コンテキスト（型安全）

```go
// plugin/context.go
package plugin

import (
    "context"

    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/invoice"
    "github.com/contract-to-cash/core/domain/shared"
)

// CalculationContext 請求計算コンテキスト
// コアが各計算ステップで値を設定し、プラグインが参照する
// metadata は使用せず、型付きフィールドでデータを共有する
type CalculationContext struct {
    ctx                   context.Context
    contract              *contract.ContractAggregate
    invoice               *invoice.Invoice
    productID             shared.ProductID       // 課金対象の Product ID（クーポン適用判定等に使用）
    billingPeriod         shared.DateRange       // 請求対象期間（冪等なクーポン引換のキーに使用、#185）
    subtotal              shared.Money           // 基本料金（BeforeCalculation中はゼロ、後述）
    subtotalAfterDiscount shared.Money           // 割引後小計（TaxHookが参照）
    appliedDiscounts      []AppliedDiscount      // 適用された割引の記録
}

// AppliedDiscount 適用された割引の記録
type AppliedDiscount struct {
    PluginName string       // 割引を適用したプラグイン名
    Code       string       // クーポンコード等
    Amount     shared.Money // 割引額
}

func NewCalculationContext(ctx context.Context, c *contract.ContractAggregate, subtotal shared.Money) *CalculationContext {
    return &CalculationContext{
        ctx:                   ctx,
        contract:              c,
        subtotal:              subtotal,
        subtotalAfterDiscount: subtotal,
    }
}

func (c *CalculationContext) Context() context.Context                    { return c.ctx }
func (c *CalculationContext) Contract() *contract.ContractAggregate       { return c.contract }
func (c *CalculationContext) ContractID() shared.ContractID              {
    if c.contract == nil { return "" }
    return c.contract.ContractID()
}
func (c *CalculationContext) ProductID() shared.ProductID                { return c.productID }
func (c *CalculationContext) Subtotal() shared.Money                     { return c.subtotal }
func (c *CalculationContext) SubtotalAfterDiscount() shared.Money        { return c.subtotalAfterDiscount }
func (c *CalculationContext) AppliedDiscounts() []AppliedDiscount        { /* コピーを返す */ }

// SetProductID コアが基本料金算出前に Price から解決して設定する
func (c *CalculationContext) SetProductID(id shared.ProductID) { c.productID = id }

// BillingPeriod / SetBillingPeriod 請求対象期間（生成される請求書の BillingPeriod と同一）。
// コアが全計算フックの実行前に設定するため、DiscountHook から参照できる。
// クーポンプラグインは (couponID, contractID, billingPeriod) を冪等な引換キーに使う（#185, §6.3）。
func (c *CalculationContext) BillingPeriod() shared.DateRange     { return c.billingPeriod }
func (c *CalculationContext) SetBillingPeriod(p shared.DateRange) { c.billingPeriod = p }

// SetSubtotal コアが基本料金算出後に設定する
func (c *CalculationContext) SetSubtotal(s shared.Money) { c.subtotal = s }

// SetSubtotalAfterDiscount コアが割引適用後に設定する
func (c *CalculationContext) SetSubtotalAfterDiscount(s shared.Money) { c.subtotalAfterDiscount = s }

// RecordDiscount プラグインが割引を記録する
func (c *CalculationContext) RecordDiscount(d AppliedDiscount) {
    c.appliedDiscounts = append(c.appliedDiscounts, d)
}

// SetInvoice コアが請求書作成後に設定する（AfterCalculation用）
func (c *CalculationContext) SetInvoice(inv *invoice.Invoice) { c.invoice = inv }
func (c *CalculationContext) Invoice() *invoice.Invoice       { return c.invoice }
```

> **⚠️ `BeforeCalculation` 中の `Subtotal()` はゼロ**: コアの請求パイプライン
> (`BillingService.executeBillingPipeline`) は `CalculationContext` を
> **subtotal=Zero** で生成し、`InvoiceLifecycleHook.BeforeCalculation` を発火した**後**に
> `SetSubtotal(基本料金)` を呼ぶ。したがって:
> - `BeforeCalculation` の中で `ctx.Subtotal()` を読むと **ゼロ**が返る（基本料金はまだ未設定）。
> - `DiscountHook.CalculateDiscount` の時点では `ctx.Subtotal()` は基本料金を返す。
> - `TaxHook.CalculateTax` の時点では `ctx.SubtotalAfterDiscount()` が割引後小計を返す。
> - `ProductID()` は基本料金算出前（BeforeCalculation より前）に Price から解決されるため、
>   `BeforeCalculation` でも参照できる。
>
> 基本料金に依存する前処理は `BeforeCalculation` ではなく `DiscountHook` 以降で行うこと。

### 2.3 汎用コンテキスト（非計算フック用）

```go
// Context 契約ライフサイクル、支払い、メトリクス等の汎用コンテキスト
type Context struct {
    ctx      context.Context
    metadata map[string]interface{}
}

func NewContext(ctx context.Context) *Context {
    return &Context{ctx: ctx, metadata: make(map[string]interface{})}
}

func (c *Context) Context() context.Context                    { return c.ctx }
func (c *Context) SetMetadata(key string, value interface{})   { c.metadata[key] = value }
func (c *Context) GetMetadata(key string) (interface{}, bool)  { v, ok := c.metadata[key]; return v, ok }
```

## 3. フック定義

### 3.1 割引フック

```go
// plugin/hooks.go
package plugin

import (
    "github.com/contract-to-cash/core/domain/shared"
)

// DiscountHook 割引計算フック
// クーポン、ボリューム割引、キャンペーン割引等に使用
type DiscountHook interface {
    Plugin

    // CalculateDiscount 割引額を計算して返す
    // ctx.Subtotal() で基本料金を参照可能
    //
    // 戻り値の契約（issue #188）: 返す割引額は **非負** かつ ctx.Subtotal() と
    // **同一通貨** でなければならない。負の割引を返すとコアの請求パイプラインは
    // ErrCodeBusinessRule（プラグイン名と金額を含む）で請求を中断する。
    // 通貨不一致は ErrCodeCurrencyMismatch（同じくプラグイン名を含む）。
    // 「割引合計 > subtotal」の clamp はコアが行う（§5.1 手順3）。
    CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}
```

### 3.2 税計算フック

```go
// TaxHook 税計算フック
// 国・地域ごとの税計算に使用
type TaxHook interface {
    Plugin

    // CalculateTax 税額を計算して返す
    // ctx.SubtotalAfterDiscount() で割引後の小計を参照可能
    // コアが割引後の金額でこのフックを呼ぶため、
    // 会計基準の順序（割引→税）が構造的に保証される
    //
    // 戻り値の契約（issue #188）: 返す税額は **非負** かつ
    // ctx.SubtotalAfterDiscount() と **同一通貨** でなければならない。
    // 負の税を返すとコアは ErrCodeBusinessRule（プラグイン名と金額を含む）で
    // 請求を中断する（負の税で total が負値になり決済不能な請求書が生成されるのを防ぐ）。
    // 通貨不一致は ErrCodeCurrencyMismatch（プラグイン名を含む）。
    CalculateTax(ctx *CalculationContext) (shared.Money, error)
}
```

### 3.3 請求書ライフサイクルフック

```go
// InvoiceLifecycleHook 請求書計算の前後処理フック
// 計算前のバリデーションや計算後の通知等に使用
type InvoiceLifecycleHook interface {
    Plugin

    // BeforeCalculation 計算前処理
    BeforeCalculation(ctx *CalculationContext) error

    // AfterCalculation 計算後処理
    AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
```

### 3.4 フック分離の設計根拠

`InvoiceCalculationHook` を3つのフックに分離した理由:

1. **ISP（インターフェース分離の原則）** — 割引のみのプラグインに空のCalculateTax実装を強制しない
2. **計算順序の構造的保証** — コアが「DiscountHook → 小計算出 → TaxHook」の順で呼び出すため、Priority値による順序制御ミスが発生しない
3. **型安全なコンテキスト** — `CalculationContext` でプラグイン間のデータ受け渡しを型安全に行う

### 3.5 契約ライフサイクルフック（ISP準拠・個別分離）

DiscountHook/TaxHookと同じ設計方針で、契約ライフサイクルの各イベントを
個別のフックIFとして定義する。必要なイベントだけ実装すればよい。

```go
// plugin/hooks_contract.go

// OnContractCreateHook 契約作成時
type OnContractCreateHook interface {
    Plugin
    OnContractCreate(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractActivateHook 契約有効化時
type OnContractActivateHook interface {
    Plugin
    OnContractActivate(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractSuspendHook 契約一時停止時
type OnContractSuspendHook interface {
    Plugin
    OnContractSuspend(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractResumeHook 契約再開時
type OnContractResumeHook interface {
    Plugin
    OnContractResume(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractCancelHook 契約解約時
type OnContractCancelHook interface {
    Plugin
    OnContractCancel(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractCancelScheduledHook 期末解約の予約時（ScheduleCancellation）
type OnContractCancelScheduledHook interface {
    Plugin
    OnContractCancelScheduled(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractCancelUnscheduledHook 予約済み解約の取消時（UnscheduleCancellation）
type OnContractCancelUnscheduledHook interface {
    Plugin
    OnContractCancelUnscheduled(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractRenewHook 契約更新時
type OnContractRenewHook interface {
    Plugin
    OnContractRenew(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractTrialEndHook トライアル終了時
type OnContractTrialEndHook interface {
    Plugin
    OnContractTrialEnd(ctx *Context, contract *contract.ContractAggregate, converted bool) error
}

// 複数イベントを監視したいプラグインは複数IFを実装する:
//   var _ plugin.OnContractCreateHook = (*NotifyPlugin)(nil)
//   var _ plugin.OnContractCancelHook = (*NotifyPlugin)(nil)
```

### 3.6 支払いコンテキスト

支払いフック専用の型安全コンテキスト。`CalculationContext` と同じパターンで、
PaymentService が既にロード済みの payment, invoice, contract を保持する。

```go
// plugin/context_payment.go

// PaymentContext 支払いフック用の型安全コンテキスト
type PaymentContext struct {
    ctx      context.Context
    payment  *payment.Payment
    invoice  *invoice.Invoice
    contract *contract.ContractAggregate
}

func NewPaymentContext(ctx context.Context, p *payment.Payment, inv *invoice.Invoice) *PaymentContext {
    return &PaymentContext{ctx: ctx, payment: p, invoice: inv}
}

func (pc *PaymentContext) Context() context.Context                    { return pc.ctx }
func (pc *PaymentContext) Payment() *payment.Payment                   { return pc.payment }
func (pc *PaymentContext) Invoice() *invoice.Invoice                   { return pc.invoice }
func (pc *PaymentContext) Contract() *contract.ContractAggregate       { return pc.contract }
func (pc *PaymentContext) SetContract(c *contract.ContractAggregate)   { pc.contract = c }
func (pc *PaymentContext) ContractID() shared.ContractID              {
    if pc.invoice == nil { return "" }
    return pc.invoice.ContractID()
}
func (pc *PaymentContext) AccountID() shared.AccountID                {
    if pc.invoice == nil { return "" }
    return pc.invoice.AccountID()
}
```

### 3.7 支払いフック（ISP準拠・個別分離）

```go
// plugin/hooks_payment.go

// BeforeChargeHook 課金前処理
type BeforeChargeHook interface {
    Plugin
    BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

// AfterChargeHook 課金後処理
type AfterChargeHook interface {
    Plugin
    AfterCharge(ctx *PaymentContext) error
}

// OnPaymentFailedHook 支払い失敗時
type OnPaymentFailedHook interface {
    Plugin
    OnPaymentFailed(ctx *PaymentContext, err error) error
}

// OnRefundHook 返金時
type OnRefundHook interface {
    Plugin
    OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}

// CompensationMethod 補償に用いた手段（issue #257）
type CompensationMethod string

const (
    CompensationMethodVoid   CompensationMethod = "void"   // Settlement 前の Void で取消
    CompensationMethodRefund CompensationMethod = "refund" // Void 失敗後の Refund フォールバック
    CompensationMethodNone   CompensationMethod = "none"   // Void も Refund も失敗（MANUAL RECONCILIATION）
)

// CompensationReason 補償が走った理由（issue #257）
type CompensationReason string

const (
    CompensationReasonLocalSaveFailed CompensationReason = "local_save_failed" // ローカル tx（payment/invoice Save）失敗
    CompensationReasonOutboxVeto      CompensationReason = "outbox_veto"       // PaymentOutboxWriter の veto（#248）
    // tx 内冪等性チェックが effective key と Failed 終端状態の既存 payment との
    // 衝突を検出した（pre-charge ルックアップと並行ライターの race）。直前の課金は
    // 実在し、ローカル記録の裏付けが無いため補償で巻き戻すが、Save は一度も
    // 試行されていない — local_save_failed とは区別される（#234 レビュー）
    CompensationReasonIdempotencyConflict CompensationReason = "idempotency_conflict"
)

// CompensationResult サガ補償の実行結果（issue #257）
type CompensationResult struct {
    TransactionID      string             // 元課金の gateway transaction ID
    Amount             shared.Money       // 元課金額
    Method             CompensationMethod // 実際に成功した手段（失敗時は none。部分成功はあり得ない）
    Reason             CompensationReason // 補償理由
    CompensationErr    error              // nil = 補償成功。非 nil = Void も Refund も失敗 = MANUAL RECONCILIATION 状態
    MarkCompensatedErr error              // 冪等キーの MarkCompensated 失敗（補償成功時のみ試行される、#87）
}

// OnCompensationExecutedHook サガ補償（charge reversal）実行後に発火する非致命フック。
// ProcessPayment の「ゲートウェイ課金成功 → ローカル tx 失敗 → Void / fallback Refund」
// 経路の可観測性の盲点を塞ぐ（issue #257）。補償の成功・失敗**両方**で発火する
// （失敗 = Method none + CompensationErr 非 nil = 人手リコンサイルが必要な状態で、
// 統合者が最もアラートしたいケース）。非致命: フックの error / panic はログされ、
// ProcessPayment の戻り値を変えない。
type OnCompensationExecutedHook interface {
    Plugin
    OnCompensationExecuted(ctx *PaymentContext, result CompensationResult) error
}
```

### 3.8 メトリクスフック（ISP準拠・個別分離）

```go
// plugin/hooks_metrics.go

// OnContractChangeHook 契約変更メトリクス
type OnContractChangeHook interface {
    Plugin
    OnContractChange(ctx *Context, event ContractChangeEvent) error
}

// OnInvoiceIssuedHook 請求書発行メトリクス
type OnInvoiceIssuedHook interface {
    Plugin
    OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

// OnPaymentProcessedHook 支払い処理メトリクス。
// 他の支払いフックと同じ *PaymentContext を受け取る（issue #223）:
// ctx.Payment() が処理済みの支払い、ctx.Invoice() / ctx.ContractID() /
// ctx.AccountID() で追加のリポジトリ参照なしに契約・アカウントへ帰属できる。
type OnPaymentProcessedHook interface {
    Plugin
    OnPaymentProcessed(ctx *PaymentContext) error
}

// ContractChangeType 契約変更種別（型安全）
type ContractChangeType string

const (
    ContractChangeCreated    ContractChangeType = "created"
    ContractChangeActivated  ContractChangeType = "activated"
    ContractChangeSuspended  ContractChangeType = "suspended"
    ContractChangeResumed    ContractChangeType = "resumed"
    ContractChangeCancelled  ContractChangeType = "cancelled"
    ContractChangeRenewed   ContractChangeType = "renewed"
    ContractChangeTrialEnd  ContractChangeType = "trial_end"
    // ContractChangeExpired は契約が autoRenew=false の自然満了で Expired へ
    // 遷移したことを表す。ContractChangeCancelled とは区別され、解約チャーンと
    // 自然満了をメトリクスで混同しないためにある（issue #162 B3）。
    // 注意: スケジュール解約（cancelAtPeriodEnd）は更新時点で Cancelled へ遷移
    // するため ContractChangeCancelled として報告される（期間境界で効力が生じても
    // ユーザー起因のチャーンであり、Expired ではない）。
    ContractChangeExpired    ContractChangeType = "expired"
)

type ContractChangeEvent struct {
    ContractID  shared.ContractID
    ChangeType  ContractChangeType    // 型安全な変更種別
    OldStatus   *contract.ContractStatus // ステータス変更の場合の旧値（nilは該当なし）
    NewStatus   *contract.ContractStatus // ステータス変更の場合の新値
    OldPriceID  *shared.PriceID        // 価格変更の場合の旧価格ID
    NewPriceID  *shared.PriceID        // 価格変更の場合の新価格ID
    MRRChange   *shared.Money         // MRR変動額（メトリクス用）
    Timestamp   time.Time
}
```

### 3.9 請求書生成フック

```go
// plugin/hooks_invoicegen.go

// InvoiceGenerationHook 請求書生成フック
type InvoiceGenerationHook interface {
    Plugin

    // BuildDocument 請求書ドキュメント構築時
    BuildDocument(ctx *Context, invoice *invoice.Invoice, doc *InvoiceDocument) error

    // AfterRender レンダリング後
    AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error

    // AfterDelivery 送付後
    AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error
}

// InvoiceDocument レンダリング対象の請求書ドキュメント
// Phase 4で詳細フィールド（IssuerInfo, LineItems, TaxAmount等）を拡張予定
type InvoiceDocument struct {
    InvoiceID     string
    InvoiceNumber string
}

// DeliveryResult 請求書送付結果
type DeliveryResult struct {
    DeliveryID string
    Status     string
    SentAt     *time.Time
    Error      *string
}
```

### 3.10 クレジットノートフック

```go
// plugin/hooks_creditnote.go

// OnCreditNoteIssuedHook クレジットノート発行時
type OnCreditNoteIssuedHook interface {
    Plugin
    OnCreditNoteIssued(ctx *Context, creditNote *invoice.CreditNote) error
}

// OnInvoiceRevisedHook 請求書の無効化・差替時
type OnInvoiceRevisedHook interface {
    Plugin
    OnInvoiceRevised(ctx *Context, original *invoice.Invoice, replacement *invoice.Invoice) error
}
```

## 4. プラグインレジストリ

### 4.1 レジストリ実装

```go
// plugin/registry.go
package plugin

import (
    "context"
    "sort"
    "sync"
)

// Registry プラグインレジストリ
type Registry struct {
    mu      sync.RWMutex
    plugins map[string]Plugin

    // 請求計算フック
    discountHooks          []DiscountHook
    taxHooks               []TaxHook
    invoiceLifecycleHooks  []InvoiceLifecycleHook

    // 契約ライフサイクルフック（ISP分離）
    onContractCreateHooks            []OnContractCreateHook
    onContractActivateHooks          []OnContractActivateHook
    onContractSuspendHooks           []OnContractSuspendHook
    onContractResumeHooks            []OnContractResumeHook
    onContractCancelHooks            []OnContractCancelHook
    onContractCancelScheduledHooks   []OnContractCancelScheduledHook
    onContractCancelUnscheduledHooks []OnContractCancelUnscheduledHook
    onContractRenewHooks             []OnContractRenewHook
    onContractTrialEndHooks          []OnContractTrialEndHook

    // 支払いフック（ISP分離）
    beforeChargeHooks           []BeforeChargeHook
    afterChargeHooks            []AfterChargeHook
    onPaymentFailedHooks        []OnPaymentFailedHook
    onRefundHooks               []OnRefundHook
    onCompensationExecutedHooks []OnCompensationExecutedHook

    // メトリクスフック（ISP分離）
    onContractChangeHooks   []OnContractChangeHook
    onInvoiceIssuedHooks    []OnInvoiceIssuedHook
    onPaymentProcessedHooks []OnPaymentProcessedHook

    // 請求書生成フック
    invoiceGenerationHooks []InvoiceGenerationHook

    // クレジットノートフック
    onCreditNoteIssuedHooks []OnCreditNoteIssuedHook
    onInvoiceRevisedHooks   []OnInvoiceRevisedHook
}

func NewRegistry() *Registry {
    return &Registry{
        plugins: make(map[string]Plugin),
    }
}

// Register プラグインを登録
func (r *Registry) Register(plugin Plugin) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    if _, exists := r.plugins[plugin.Name()]; exists {
        return fmt.Errorf("plugin %s already registered", plugin.Name())
    }
    
    r.plugins[plugin.Name()] = plugin
    
    // フック別に分類（型アサーションで自動判定）
    // 1つのプラグインが複数のフックIFを実装可能

    // 請求計算フック
    if h, ok := plugin.(DiscountHook); ok { r.discountHooks = append(r.discountHooks, h) }
    if h, ok := plugin.(TaxHook); ok { r.taxHooks = append(r.taxHooks, h) }
    if h, ok := plugin.(InvoiceLifecycleHook); ok { r.invoiceLifecycleHooks = append(r.invoiceLifecycleHooks, h) }

    // 契約ライフサイクルフック（ISP分離）
    if h, ok := plugin.(OnContractCreateHook); ok { r.onContractCreateHooks = append(r.onContractCreateHooks, h) }
    if h, ok := plugin.(OnContractActivateHook); ok { r.onContractActivateHooks = append(r.onContractActivateHooks, h) }
    if h, ok := plugin.(OnContractSuspendHook); ok { r.onContractSuspendHooks = append(r.onContractSuspendHooks, h) }
    if h, ok := plugin.(OnContractResumeHook); ok { r.onContractResumeHooks = append(r.onContractResumeHooks, h) }
    if h, ok := plugin.(OnContractCancelHook); ok { r.onContractCancelHooks = append(r.onContractCancelHooks, h) }
    if h, ok := plugin.(OnContractCancelScheduledHook); ok { r.onContractCancelScheduledHooks = append(r.onContractCancelScheduledHooks, h) }
    if h, ok := plugin.(OnContractCancelUnscheduledHook); ok { r.onContractCancelUnscheduledHooks = append(r.onContractCancelUnscheduledHooks, h) }
    if h, ok := plugin.(OnContractRenewHook); ok { r.onContractRenewHooks = append(r.onContractRenewHooks, h) }
    if h, ok := plugin.(OnContractTrialEndHook); ok { r.onContractTrialEndHooks = append(r.onContractTrialEndHooks, h) }

    // 支払いフック（ISP分離）
    if h, ok := plugin.(BeforeChargeHook); ok { r.beforeChargeHooks = append(r.beforeChargeHooks, h) }
    if h, ok := plugin.(AfterChargeHook); ok { r.afterChargeHooks = append(r.afterChargeHooks, h) }
    if h, ok := plugin.(OnPaymentFailedHook); ok { r.onPaymentFailedHooks = append(r.onPaymentFailedHooks, h) }
    if h, ok := plugin.(OnRefundHook); ok { r.onRefundHooks = append(r.onRefundHooks, h) }
    if h, ok := plugin.(OnCompensationExecutedHook); ok { r.onCompensationExecutedHooks = append(r.onCompensationExecutedHooks, h) }

    // メトリクスフック（ISP分離）
    if h, ok := plugin.(OnContractChangeHook); ok { r.onContractChangeHooks = append(r.onContractChangeHooks, h) }
    if h, ok := plugin.(OnInvoiceIssuedHook); ok { r.onInvoiceIssuedHooks = append(r.onInvoiceIssuedHooks, h) }
    if h, ok := plugin.(OnPaymentProcessedHook); ok { r.onPaymentProcessedHooks = append(r.onPaymentProcessedHooks, h) }

    // 請求書生成フック
    if h, ok := plugin.(InvoiceGenerationHook); ok { r.invoiceGenerationHooks = append(r.invoiceGenerationHooks, h) }

    // クレジットノートフック
    if h, ok := plugin.(OnCreditNoteIssuedHook); ok { r.onCreditNoteIssuedHooks = append(r.onCreditNoteIssuedHooks, h) }
    if h, ok := plugin.(OnInvoiceRevisedHook); ok { r.onInvoiceRevisedHooks = append(r.onInvoiceRevisedHooks, h) }

    return nil
}

// sortByPriority は []Plugin を Priority 昇順に**その場で**安定ソートする
// （InitializeAll / ShutdownAll 用）。sortedCopy はフックスライスをコピーしてから
// 安定ソートして返す（ゲッター用、内部スライスは変更しない）。どちらも
// sort.SliceStable を使い、同一 Priority のフックは入力順を保つ。
func sortByPriority(plugins []Plugin) {
    sort.SliceStable(plugins, func(i, j int) bool {
        return plugins[i].Priority() < plugins[j].Priority()
    })
}

func sortedCopy[T Plugin](hooks []T) []T {
    if len(hooks) == 0 {
        return nil
    }
    cp := make([]T, len(hooks))
    copy(cp, hooks)
    sort.SliceStable(cp, func(i, j int) bool {
        return cp[i].Priority() < cp[j].Priority()
    })
    return cp
}

// InitializeAll 全プラグインを Priority 昇順で初期化する。
//
// 初期化は all-or-nothing（#197）: いずれかの Initialize が失敗したら、その呼び出しで
// 既に初期化済みのプラグインを **逆順に Shutdown してロールバック** してからエラーを返す。
// これをしないと、失敗した InitializeAll が「半初期化された」プラグイン（接続・goroutine・
// ファイルハンドル等を掴んだまま）をリークさせる — 呼び出し側はエラーしか受け取らず、
// どのプラグインが先に成功したかを知る手段がないため解放できない。ロールバック中の
// Shutdown エラーは errors.Join で元の初期化エラーに連結され、可観測だが元原因を隠さない。
func (r *Registry) InitializeAll(ctx context.Context, configs map[string]Config) error {
    // ... Priority 昇順にソート ...
    var initialized []Plugin
    for _, p := range plugins {
        if err := SafeInvoke("Plugin.Initialize", p.Name(), func() error {
            return p.Initialize(ctx, configs[p.Name()])
        }); err != nil {
            initErr := fmt.Errorf("failed to initialize plugin %q: %w", p.Name(), err)
            // 既に初期化したプレフィックスを逆順に Shutdown（#197）
            if rbErr := shutdownInReverse(ctx, initialized); rbErr != nil {
                return errors.Join(initErr, rbErr)
            }
            return initErr
        }
        initialized = append(initialized, p)
    }
    return nil
}

// ShutdownAll 全プラグインを逆 Priority 順にシャットダウンする。
//
// InitializeAll と異なり、**最初のエラーで中断しない**（#197）: 1 つの失敗が後続の
// プラグインを未 Shutdown のまま（リソースをリーク）残さないよう、全プラグインの Shutdown を
// 試み、エラーを収集して errors.Join で連結して返す。
func (r *Registry) ShutdownAll(ctx context.Context) error {
    // ... Priority 昇順にソートし、shutdownInReverse で逆順に全件試行 ...
    return shutdownInReverse(ctx, plugins)
}

// shutdownInReverse は与えられたスライスを逆順に、全件 Shutdown を試みてエラーを
// errors.Join で連結して返す（1 件の失敗で中断しない）。
func shutdownInReverse(ctx context.Context, plugins []Plugin) error {
    var errs []error
    for i := len(plugins) - 1; i >= 0; i-- {
        p := plugins[i]
        if err := SafeInvoke("Plugin.Shutdown", p.Name(), func() error {
            return p.Shutdown(ctx)
        }); err != nil {
            errs = append(errs, fmt.Errorf("failed to shutdown plugin %q: %w", p.Name(), err))
        }
    }
    return errors.Join(errs...)
}

// --- フックゲッター（Priority ソート済みの「コピー」を返す） ---
//
// ⚠️ ゲッターは内部スライスをそのまま返すのではなく、必ず sortedCopy で
// **Priority 昇順にソートしたコピー**を返す。登録順の内部スライスを生で返す実装は
// (1) 呼び出し側が Priority 順を期待できず、(2) 返したスライスを呼び出し側が変更すると
// レジストリ内部状態を破壊するため誤り。sortedCopy は STABLE ソートで、内部スライスは
// 登録順を保つため、同一 Priority のフックは登録順で実行される（issue #162 P1）。
func (r *Registry) GetDiscountHooks() []DiscountHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return sortedCopy(r.discountHooks)
}

func (r *Registry) GetTaxHooks() []TaxHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return sortedCopy(r.taxHooks)
}

func (r *Registry) GetInvoiceLifecycleHooks() []InvoiceLifecycleHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return sortedCopy(r.invoiceLifecycleHooks)
}

// 契約ライフサイクル（各イベント個別）
func (r *Registry) GetOnContractCreateHooks() []OnContractCreateHook { ... }
func (r *Registry) GetOnContractActivateHooks() []OnContractActivateHook { ... }
func (r *Registry) GetOnContractCancelHooks() []OnContractCancelHook { ... }
func (r *Registry) GetOnContractCancelScheduledHooks() []OnContractCancelScheduledHook { ... }
func (r *Registry) GetOnContractCancelUnscheduledHooks() []OnContractCancelUnscheduledHook { ... }
// ... 他の契約ライフサイクルフックも同様

// 支払い（各イベント個別）
func (r *Registry) GetBeforeChargeHooks() []BeforeChargeHook { ... }
func (r *Registry) GetAfterChargeHooks() []AfterChargeHook { ... }
func (r *Registry) GetOnPaymentFailedHooks() []OnPaymentFailedHook { ... }
func (r *Registry) GetOnRefundHooks() []OnRefundHook { ... }
func (r *Registry) GetOnCompensationExecutedHooks() []OnCompensationExecutedHook { ... }

// メトリクス（各イベント個別）
func (r *Registry) GetOnContractChangeHooks() []OnContractChangeHook { ... }
func (r *Registry) GetOnInvoiceIssuedHooks() []OnInvoiceIssuedHook { ... }
func (r *Registry) GetOnPaymentProcessedHooks() []OnPaymentProcessedHook { ... }

// 請求書生成
func (r *Registry) GetInvoiceGenerationHooks() []InvoiceGenerationHook { ... }

// クレジットノート
func (r *Registry) GetOnCreditNoteIssuedHooks() []OnCreditNoteIssuedHook { ... }
func (r *Registry) GetOnInvoiceRevisedHooks() []OnInvoiceRevisedHook { ... }
```

## 5. プラグイン実行順序

### 5.1 コアが保証する計算順序

請求書計算の順序はコアが構造的に保証する。
Priority値に依存しないため、プラグイン登録順のミスで会計基準違反が発生しない。

```
0. 基本料金を最小単位に丸める（コア: subtotal = RoundToMinorUnit(mode), issue #189）
   → 従量課金の厳密有理数（段階単価等）を通貨の最小単位へ量子化。丸めモードは
     BillingConfig.TaxRoundingMode（既定は RoundDown = ゼロ方向）。以降パイプラインが
     永続化する全金額がこのモードで整数化され、整数専用ゲートウェイと厳密に照合できる
   → ctx.SetBillingPeriod(period)（全計算フックの前に設定。DiscountHook が請求期間を
     参照でき、クーポンが (coupon, contract, period) で冪等に引換できる。issue #185）
   → ctx.SetProductID(...)（Price から解決）
1. InvoiceLifecycleHook.BeforeCalculation()  ← 計算前処理
                                                ⚠️ この時点で ctx.Subtotal() は ZERO
                                                   （ctx.ProductID() / ctx.BillingPeriod() は参照可能）
2. 基本料金（丸め済み）をコンテキストへ設定（コア、契約タイプに応じて分岐）
   → 以降 ctx.Subtotal() は基本料金を返す
3. DiscountHook.CalculateDiscount()          ← 割引計算（全DiscountHook、ctx.Subtotal()=基本料金）
   → 各フックの戻り値を境界検証（負値は ErrCodeBusinessRule で中断、通貨不一致は
     ErrCodeCurrencyMismatch。いずれもプラグイン名を含む。issue #188）
   → 割引合計を最小単位に丸める（コア: totalDiscount = RoundToMinorUnit(mode), issue #189）
   → 割引上限ガード（割引合計 > subtotalの場合にcap）
4. 小計算出（コア: subtotal - totalDiscount）→ ctx.SetSubtotalAfterDiscount()
5. TaxHook.CalculateTax()                    ← 税計算（ctx.SubtotalAfterDiscount()に対して）
   → 各フックの戻り値を境界検証（負値は ErrCodeBusinessRule で中断。issue #188）
   → 税合計を最小単位に丸める（コア: totalTax = RoundToMinorUnit(mode), 全 TaxHook 合算後に
     1 請求書あたり 1 回。例: ¥101 × 10% = ¥10.1 → 整数化。issue #189）
6. 合計算出（コア: afterDiscount + totalTax。両者とも整数化済みなので total も整数）
7. クレジット台帳からの充当（コア、FIFO）    ← 残高があれば税込合計から差引（tx内）
8. 請求書をdraft状態で生成（コア、tx内）→ GracePeriod後に FinalizeInvoice で確定
9. InvoiceLifecycleHook.AfterCalculation()   ← 計算後処理（**保存(Save)より前**に発火、tx内）
10. 保存（コア、tx内）
```

> **全フックは `plugin.SafeInvoke` / `SafeInvokeMoney` 経由で発火**され、パニックは
> `*PluginPanicError` へ変換される（フェイタリティ・ポリシーは §5.4、issue #193）。

> **注**: このフロー順序は `architecture.md` セクション6.3 と同一。実コードは
> `application/service/billing_service.go` の
> `executeBillingPipeline`（正準はソース）。
>
> **プラグイン可観測性の要点**:
> - `BeforeCalculation` 中の `ctx.Subtotal()` は **ゼロ**（基本料金は手順2でコンテキストへ設定される）。
> - `AfterCalculation` は請求書生成の**後・保存の前**に発火する（プラグインが受け取る請求書は
>   まだ永続化されていない）。永続化済みを前提とする処理は `OnInvoiceIssuedHook`
>   （`FinalizeInvoice` の保存後に発火）で行うこと。

### 5.2 Priority の役割（同一フック内の順序制御）

Priorityは**同一フック種別内**での実行順序のみに影響する。
例: 複数のDiscountHookがある場合、Priority順に実行される。

```go
const (
    PriorityHighest = 0
    PriorityHigh    = 100
    PriorityNormal  = 500
    PriorityLow     = 900
    PriorityLowest  = 1000
)

// 例: ボリューム割引（先に適用）→ クーポン割引（後に適用）
type VolumeDiscountPlugin struct{}
func (p *VolumeDiscountPlugin) Priority() int { return PriorityHigh }

type CouponPlugin struct{}
func (p *CouponPlugin) Priority() int { return PriorityNormal }
```

フック種別間の順序（DiscountHook → TaxHook）はコアが制御するため、
TaxPluginのPriorityをどう設定してもDiscountHookより先に実行されることはない。

### 5.3 Hook発火責任（誰がフックを呼ぶか）

全23種のフックのうち、コアが自動発火するのは15種。残りは統合者（サービス開発者）
またはアダプタが発火する。プラグインを書く前に、実装するフックが「誰に呼ばれるか」を
この表で確認すること。

**コアが自動発火するフック（15種）**

| Hook | 発火箇所 |
|------|---------|
| `DiscountHook` | `BillingService` 請求パイプライン（GenerateInvoice / RegenerateInvoice / GenerateProrationInvoice） |
| `TaxHook` | 同上 |
| `InvoiceLifecycleHook` | 同上（BeforeCalculation / AfterCalculation） |
| `OnInvoiceIssuedHook` | `BillingService.FinalizeInvoice`（確定保存後、非致命） |
| `BeforeChargeHook` | `PaymentService.ProcessPayment`（ゲートウェイ課金前） |
| `AfterChargeHook` | `PaymentService.ProcessPayment`（成功パス、非致命）、`PaymentService.SettlePayment`（Pending→Completed の実遷移時のみ・コミット後、非致命。冪等 no-op リプレイでは発火しない） |
| `OnPaymentProcessedHook` | `PaymentService.ProcessPayment`（成功パス、非致命）、`PaymentService.SettlePayment`（同上） |
| `OnPaymentFailedHook` | `PaymentService.ProcessPayment`（ゲートウェイ失敗時、非致命）、`PaymentService.MarkPaymentFailed`（Pending→Failed の実遷移時のみ・コミット後、非致命。冪等 no-op リプレイでは発火しない） |
| `OnRefundHook` | `PaymentService.Refund`（非致命） |
| `OnCompensationExecutedHook` | `PaymentService.ProcessPayment`（サガ補償の実行後、非致命。補償成功・失敗の**両方**で発火する — 失敗 = MANUAL RECONCILIATION 状態。issue #257） |
| `OnCreditNoteIssuedHook` | `CreditNoteService`（発行後、非致命） |
| `OnInvoiceRevisedHook` | `CreditNoteService.ReissueInvoice`（非致命） |
| `OnContractRenewHook` | `batch.ContractRenewalProcessor`（保存後、非致命） |
| `OnContractTrialEndHook` | `batch.TrialExpirationProcessor`（保存後、非致命） |
| `OnContractChangeHook` | `batch.ContractRenewalProcessor`（renewed / autoRenew=false の自然満了は expired、cancelAtPeriodEnd のスケジュール解約は cancelled）、`batch.TrialExpirationProcessor`（trial_end） |

> **発火タイミングの注意**: コアが「保存後」に発火するフック（`OnInvoiceIssuedHook` /
> `AfterChargeHook` / `OnPaymentProcessedHook` 等）は、呼び出し側が自前のトランザクション内から
> サービスメソッドを呼んだ場合、`tx.Run` が外側トランザクションにジョインするため
> **外側コミットの前**に発火する。外側をロールバックすると「保存されていないのに通知済み」に
> なるため、外側をロールバックし得る場合はサービス呼び出しをトランザクション外で行うこと。
> また、冪等リプレイの収束（同一冪等キーへの並行リクエスト等）により同一エンティティに対して
> 複数回発火し得るため、メトリクス系フックは対象 ID でのデデュープを前提に実装する。

**統合者が発火するフック（7種）**

契約の Create / Activate / Suspend / Resume / Cancel、および期末解約の予約・取消
（`ScheduleCancellation` / `UnscheduleCancellation`）はコアにアプリケーション
サービスが存在しない（集約メソッドを統合者コードが直接呼ぶ）ため、対応するフックも
統合者が発火する:

- `OnContractCreateHook` / `OnContractActivateHook` / `OnContractSuspendHook` /
  `OnContractResumeHook` / `OnContractCancelHook`
- `OnContractCancelScheduledHook` / `OnContractCancelUnscheduledHook`
  （統合者が集約の `ScheduleCancellation` / `UnscheduleCancellation` を呼んだ後に発火する）

実装リファレンス: `examples/hosting-integration-demo/main.go`（集約の状態遷移を
実行 → 保存 → `registry.GetOnContract*Hooks()` をループし、各フックを
`plugin.FireNonFatal` 経由で発火するパターン。パニック隔離と「ログして続行」の
非致命ポリシーがコア発火フックと揃う — §5.4）。

**アダプタが発火するフック（1種）**

- `InvoiceGenerationHook`（BuildDocument / AfterRender / AfterDelivery）—
  請求書のレンダリング・送付パイプラインはコアのスコープ外。
  利用者が実装する請求書生成アダプタが各フェーズで発火する。

> **注（#248）**: 上表とは別に、コアは tx 内 Save 直後・コミット前で**統合者ポート
> `PaymentOutboxWriter` / `InvoiceOutboxWriter`**（フックではない）を呼ぶ。これらは
> トランザクショナル・アウトボックス用で、**#248 自体はフック数を増やしていない**
> （当時 22。現在の総数は #257 の `OnCompensationExecutedHook` を加えた 23、§10.3）。詳細は §11。

### 5.4 パニック隔離とフェイタリティ・ポリシー（issue #193）

プラグインは第三者コードであり、`panic` する可能性がある。コアが発火する全フックは
**`plugin.SafeInvoke`（および `(shared.Money, error)` を返す `SafeInvokeMoney`）** 経由で
呼び出され、`recover()` でパニックを捕捉し、スタックトレース（`runtime/debug.Stack`）を
添えた構造化エラー **`*plugin.PluginPanicError`**（`PluginName` / `HookType` / `Value` /
`Stack` を保持）へ変換する。これにより、暴走したプラグイン 1 つが進行中の請求・支払い
トランザクションを破壊すること（例: 課金成功後に `AfterCharge` がパニックし、
`ProcessPayment` のローカル永続化を突き抜けて「課金済みだが未記録」状態を生む）を防ぐ。
なお `SafeInvoke` が隔離するのはフック本体の呼び出しのみである: `Name()` / `Priority()` は
`SafeInvoke` の**外**で呼ばれる（`SafeInvoke` の引数として、および Priority ソート中）ため、
これらのゲッターがパニックすると隔離されない — プラグイン作者は `Name()` / `Priority()` を
自明な実装（フィールド/定数を返すだけ）に保つこと。

**捕捉したパニックは、フックがエラーを返したのと同じフェイタリティ・ポリシーで扱う。**
フックが「拒否権を持つ（veto-capable）」か「非致命（non-fatal）」かは §5.3 の発火箇所と
一致する:

| 分類 | 対象フック | パニック時の挙動 |
|------|-----------|-----------------|
| **拒否権あり（veto）** | `InvoiceLifecycleHook.BeforeCalculation` / `DiscountHook` / `TaxHook` / `BeforeChargeHook` | パニック → パイプラインエラーとして**伝播**し、操作をクリーンに中断する（フックがエラーを返した場合と同一）。ゲートウェイ課金前・保存前なので副作用は残らない |
| **tx 内・保存前** | `InvoiceLifecycleHook.AfterCalculation` | トランザクション内（Save より前）で発火。パニック → エラーへ変換して `tx.Run` のクロージャから返す。**パニックが `tx.Run` を突き抜けない**ため（バックエンド依存のロールバック挙動を避ける）、tx はクリーンに中断し何も永続化されない |
| **非致命（non-fatal）** | `AfterChargeHook` / `OnPaymentProcessedHook` / `OnPaymentFailedHook` / `OnRefundHook` / `OnCompensationExecutedHook` / `OnInvoiceIssuedHook` / `OnCreditNoteIssuedHook` / `OnInvoiceRevisedHook` / `OnContractRenewHook` / `OnContractTrialEndHook` / `OnContractChangeHook`（バッチ含む） | パニック → `plugin.LogNonFatalHookError` が**プラグイン名・フック種別・スタックを Error レベルでログ**し、処理を継続する。同種の後続フックも通常どおり実行される（1 つのパニックが他フックを止めない） |
| **ライフサイクル** | `InitializeAll` / `ShutdownAll` の `Plugin.Initialize` / `Plugin.Shutdown` | パニック → エラーへ変換して返す。パニックする `Initialize` は起動を**回復不能にクラッシュさせず**、`*PluginPanicError` を含むエラーとして扱う |

> **注**: 統合者が発火するフック（契約 Create/Activate/Suspend/Resume/Cancel/
> CancelScheduled/CancelUnscheduled の 7 種）と
> アダプタが発火する `InvoiceGenerationHook` はコアの発火経路外のため、コアの
> `SafeInvoke` ラップは適用されない。統合者・アダプタは自コードで同様のパニック隔離を
> 行うこと。このパターン（SafeInvoke + LogNonFatalHookError の「ログして続行」）を
> 1 呼び出しに束ねた出荷済みヘルパーが **`plugin.FireNonFatal`** で、非致命な統合者発火
> フックはこれをそのまま使えばよい（veto 意味論が必要なフックは `SafeInvoke` を直接使い、
> 返ったエラーを自分で処理する）。`examples/hosting-integration-demo/main.go` の発火ループが
> `FireNonFatal` 利用のリファレンス。

**API**:

```go
// plugin/safe.go
func SafeInvoke(hookType, pluginName string, fn func() error) error
func SafeInvokeMoney(hookType, pluginName string, fn func() (shared.Money, error)) (shared.Money, error)
func AsPanic(err error) (*PluginPanicError, bool)   // err が *PluginPanicError を包むか判定
func LogNonFatalHookError(logger *slog.Logger, msg string, err error, attrs ...any)
// FireNonFatal は 1 フックを SafeInvoke で実行し、返却/回復されたエラーを
// LogNonFatalHookError でログして**伝播させない**（非致命ポリシーの統合者向けヘルパー。
// パニックは Error レベル + スタック、通常エラーは Warn。nil logger は slog.Default()）。
func FireNonFatal(logger *slog.Logger, hookType, pluginName string, fn func() error)

type PluginPanicError struct {
    PluginName string
    HookType   string
    Value      any    // recover() が返したパニック値
    Stack      []byte // debug.Stack()
}
```

## 6. クーポンプラグイン実装例

### 6.1 プラグイン実装

> **⚠️ この例は要点の抜粋**: 完全な実装（フィルタリング・使用上限のアドバイザリ判定・
> 引換確定）は `plugins/coupon/plugin.go` を参照。クーポンは「計算（`CalculateDiscount`）」と
> 「引換確定（`AfterCalculation`）」を分離しており、その設計根拠とトランザクション整合性は
> §6.3 にある。リポジトリ側（原子的 `SaveRedemption` 契約）のリファレンス実装は
> `infrastructure/inmemory` の `CouponRepository`（issue #240）。

```go
// plugins/coupon/plugin.go
package coupon

import (
    "context"
    "errors"
    "fmt"

    "github.com/contract-to-cash/core/domain/invoice"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/plugin"
)

// CouponPlugin クーポンプラグイン。
// DiscountHook（割引を計算）AND InvoiceLifecycleHook（AfterCalculation で引換を確定）を
// 実装する。「計算」と「確定」の分離が issue #185 を解決する（§6.3）。
type CouponPlugin struct {
    repo     CouponRepository
    config   CouponConfig
    priority int
    clock    shared.Clock
}

// インターフェース準拠の確認（コンパイル時チェック）。両方のフックを実装する。
var (
    _ plugin.DiscountHook         = (*CouponPlugin)(nil)
    _ plugin.InvoiceLifecycleHook = (*CouponPlugin)(nil)
)

type CouponConfig struct {
    MaxCouponsPerInvoice int
    AllowStacking        bool // 複数クーポン併用可否
}

// NewCouponPlugin は既定値 MaxCouponsPerInvoice=1 / AllowStacking=false で構築する
// （スタッキング無効時は「最初に検証を通った1枚」だけを適用する）。
func NewCouponPlugin(repo CouponRepository, clock shared.Clock) *CouponPlugin {
    return &CouponPlugin{
        repo:     repo,
        priority: plugin.PriorityNormal,
        clock:    clock,
        config: CouponConfig{
            MaxCouponsPerInvoice: 1,
            AllowStacking:        false,
        },
    }
}

func (p *CouponPlugin) Name() string    { return "coupon" }
func (p *CouponPlugin) Version() string { return "1.2.0" }
func (p *CouponPlugin) Priority() int   { return p.priority }

// Initialize は maxCouponsPerInvoice / allowStacking / priority を読む。
// 生の型アサーションではなく Config.Int / Config.Bool を使う（issue #239, §2.1）—
// JSON 由来の数値（float64）を受理し、型不一致は黙殺せずエラーとして返す。
func (p *CouponPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if n, ok, err := config.Int("maxCouponsPerInvoice"); err != nil {
        return fmt.Errorf("coupon: %w", err)
    } else if ok {
        p.config.MaxCouponsPerInvoice = n
    }
    if b, ok, err := config.Bool("allowStacking"); err != nil {
        return fmt.Errorf("coupon: %w", err)
    } else if ok {
        p.config.AllowStacking = b
    }
    if n, ok, err := config.Int("priority"); err != nil {
        return fmt.Errorf("coupon: %w", err)
    } else if ok {
        p.priority = n
    }
    return nil
}

func (p *CouponPlugin) Shutdown(_ context.Context) error { return nil }

// CalculateDiscount DiscountHook の実装。
// **永続化の副作用を持たない**（issue #185）: 適用可能クーポンと既存引換を「読む」だけで、
// 割引を計算して ctx.RecordDiscount(...) するのみ。引換は AfterCalculation で確定するため、
// このフックの後にパイプラインがロールバックしてもクーポンは消費されない。
//
// クーポン選択は「first valid wins」: スライスを事前に truncate せず、すべての検証
// （有効期間・minAmount・通貨・使用上限）を通ったクーポンだけを applied として数える。
// 先頭に無効クーポン（期限切れ等）があっても後続の有効クーポンを潰さない（issue #158）。
func (p *CouponPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    currency := ctx.Subtotal().Currency()
    zero := shared.Zero(currency)

    // フルクエリで適用可能クーポンを取得（ContractID / AccountID / ProductID / At）。
    coupons, err := p.repo.FindApplicable(ctx.Context(), CouponQuery{
        ContractID: ctx.ContractID(),
        AccountID:  /* contract.AccountID() */ "",
        ProductID:  ctx.ProductID(),
        At:         p.clock.Now(),
    })
    if err != nil {
        return zero, fmt.Errorf("coupon: find applicable: %w", err)
    }

    // 適用上限（0=無制限）。AllowStacking=false は MaxCouponsPerInvoice より優先し 1 枚。
    effectiveLimit := 0
    if !p.config.AllowStacking {
        effectiveLimit = 1
    } else if p.config.MaxCouponsPerInvoice > 0 {
        effectiveLimit = p.config.MaxCouponsPerInvoice
    }

    subtotal := ctx.Subtotal()
    total := zero
    applied := 0
    for _, c := range coupons {
        if effectiveLimit > 0 && applied >= effectiveLimit {
            break
        }
        // 有効期間・minAmount(通貨一致)・使用上限（引換行から再構成、進行中の
        // (contract, period) を除外）をアドバイザリに再チェック。権威ある上限強制は
        // AfterCalculation → SaveRedemption が原子的に行う（§6.3）。
        if !c.IsValid(p.clock.Now()) {
            continue
        }
        // … minAmount / usageLimit / perAccountUsageLimit チェック（省略、§6.3）…

        discount, err := c.CalculateDiscount(subtotal)
        if err != nil {
            return zero, fmt.Errorf("coupon: calculate discount: %w", err)
        }
        if discount.Currency() != currency {
            continue // 別通貨の固定額クーポンはスキップ（issue #148）
        }
        applied++

        // 型安全な割引記録（引換はここでは書かない）。
        ctx.RecordDiscount(plugin.AppliedDiscount{
            PluginName: p.Name(),
            Code:       c.Code(),
            Amount:     discount,
        })
        if total, err = total.Add(discount); err != nil {
            return zero, fmt.Errorf("coupon: sum discounts: %w", err)
        }
    }
    return total, nil
}

// BeforeCalculation は no-op（InvoiceLifecycleHook を満たすため）。
func (p *CouponPlugin) BeforeCalculation(_ *plugin.CalculationContext) error { return nil }

// AfterCalculation は適用した各クーポンの引換を冪等に確定する（tx 内・請求書生成後・
// 保存前）。SaveRedemption に RedemptionLimits を渡し、使用上限を原子的に強制する。
// ErrUsageLimitReached はラップして返し tx をロールバックさせる（§6.3、issue #185/#195）。
func (p *CouponPlugin) AfterCalculation(ctx *plugin.CalculationContext, inv *invoice.Invoice) error {
    if inv == nil {
        return nil
    }
    for _, d := range ctx.AppliedDiscounts() {
        if d.PluginName != p.Name() {
            continue
        }
        // … FindByCode → NewRedemption → SaveRedemption(redemption, limits) …
        _ = errors.Is // ErrUsageLimitReached の判定に使用（§6.3）
    }
    return nil
}
```

### 6.2 クーポンドメイン

```go
// plugins/coupon/domain.go
package coupon

import (
    "math/big"
    "time"

    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/shared"
)

type CouponID string

type CouponType string

const (
    CouponTypePercentage CouponType = "percentage" // 割合割引
    CouponTypeFixed      CouponType = "fixed"      // 固定額割引
)

// CodeType 共有プロモコードと1回限りのユニークコードを区別する
type CodeType string

const (
    CodeTypeShared CodeType = "shared" // 複数アカウントが利用可能なプロモコード
    CodeTypeUnique CodeType = "unique" // 特定の引換に割り当てられた1回限りコード
)

type Coupon struct {
    id                      CouponID
    code                    string
    codeType                CodeType
    couponType              CouponType
    value                   *big.Rat                // 割合(例 10/100) or 固定額
    currency                shared.Currency         // 固定額の場合の通貨
    minAmount               *shared.Money           // 最低購入額（nil=なし）
    maxDiscount             *shared.Money           // 最大割引額（nil=上限なし）
    validFrom               time.Time
    validUntil              time.Time
    usageLimit              *int                    // グローバル使用回数制限
    usedCount               int                     // マイグレーションベースライン専用（§6.3、プラグインは加算しない）
    perAccountUsageLimit    *int                    // アカウントごとの使用回数上限（nil=無制限）
    applicableTo            []shared.ProductID      // 適用可能な Product ID（空なら全 Product）
    applicableContractTypes []contract.ContractType // 適用可能な契約タイプ（空なら全タイプ）
    allowedAccountIDs       []shared.AccountID      // 許可リスト（空なら制限なし）
    blockedAccountIDs       []shared.AccountID      // 拒否リスト
}

// 主要メソッド（抜粋。フィールドは非公開、getter/判定メソッド経由でアクセスする）:
//   Code() / CodeType() / CouponType() / Value() / ValidFrom() / ValidUntil()
//   MinAmount() / MaxDiscount() / UsageLimit() / UsedCount() / PerAccountUsageLimit()
//   ApplicableTo() []shared.ProductID
//   IsValid(at time.Time) bool
//   IsApplicableToProduct(productID shared.ProductID) bool
//   IsApplicableToContractType(ct contract.ContractType) bool
//   IsAccountAllowed(accountID shared.AccountID) bool
//   WithCodeType / WithPerAccountUsageLimit / WithAllowedAccountIDs /
//   WithBlockedAccountIDs / WithApplicableContractTypes（ビルダー）

// CalculateDiscount は (shared.Money, error) を返す。
// maxDiscount の通貨が割引通貨と一致しない場合、上限を黙って捨てず error を返す（issue #148）。
func (c *Coupon) CalculateDiscount(subtotal shared.Money) (shared.Money, error) {
    var discount shared.Money

    switch c.couponType {
    case CouponTypePercentage:
        discount = subtotal.Multiply(c.value)
    case CouponTypeFixed:
        discount = shared.NewMoney(c.value, c.currency)
    default:
        return shared.Zero(subtotal.Currency()), nil
    }

    // 最大割引額の制限（通貨不一致は error）
    if c.maxDiscount != nil {
        capped, err := discount.Min(*c.maxDiscount)
        if err != nil {
            return shared.Money{}, shared.NewDomainError(shared.ErrCodeCurrencyMismatch,
                "coupon maxDiscount currency does not match discount currency")
        }
        discount = capped
    }

    return discount, nil
}

// ErrUsageLimitReached は SaveRedemption が使用上限超過で引換を拒否したときに返す
// センチネルエラー（#195）。プラグインの AfterCalculation はこれをラップした説明的
// エラーを返してパイプラインをロールバックさせる（リトライは枯渇クーポンを外して再計算）。
// 実装は errors.Is(err, ErrUsageLimitReached) が成立する形で返すこと。
var ErrUsageLimitReached = errors.New("coupon: usage limit reached")

// RedemptionLimits は SaveRedemption が挿入と同一原子操作で強制すべき使用上限。
// プラグインが確定対象クーポンから設定する（nil = その次元は無制限）。
type RedemptionLimits struct {
    GlobalLimit     *int // Coupon.UsageLimit()。nil=無制限
    GlobalBaseline  int  // Coupon.UsedCount()（引換台帳以前の移行ベースライン）
    PerAccountLimit *int // Coupon.PerAccountUsageLimit()。nil=無制限
}

// CouponQuery は FindApplicable の検索パラメータ（プラグインが CalculationContext から詰める）。
type CouponQuery struct {
    ContractID shared.ContractID
    AccountID  shared.AccountID
    ProductID  shared.ProductID
    At         time.Time
}

// CouponRepository クーポンリポジトリ（実際のシグネチャは型付き ID を使う）
type CouponRepository interface {
    FindByCode(ctx context.Context, code string) (*Coupon, error)
    FindApplicable(ctx context.Context, query CouponQuery) ([]*Coupon, error)
    Save(ctx context.Context, coupon *Coupon) error
    // SaveRedemption は「冪等 + 使用上限の原子的強制」を1操作で行う（#185, #195, §6.3）。
    // 実装は同一クーポンへの並行 SaveRedemption に対して以下を1つの直列化された原子
    // ステップとして行うこと:
    //   (a) 冪等: 同一 IdempotencyKey (couponID, contractID, billingPeriod) の行が
    //       既にあれば、挿入せず・新規使用として数えず nil を返す。
    //   (b) 上限判定: それ以外で挿入が limits を超過するなら ErrUsageLimitReached を返し
    //       挿入しない（GlobalBaseline + 既存行数 >= GlobalLimit、または
    //       アカウント別既存行数 >= PerAccountLimit）。
    //   (c) 挿入: それ以外は永続化する。
    // (b) をここで原子的に行うことが #195（DISTINCT キー同士の TOCTOU）を塞ぐ。
    SaveRedemption(ctx context.Context, redemption *Redemption, limits RedemptionLimits) error
    // FindRedemptions は使用回数の唯一の情報源。プラグインが redemption 行を数えて
    // グローバル/アカウント別の使用上限を（進行中の (contract, period) を除外して）
    // CalculateDiscount 内で**アドバイザリに**判定する（権威ある判定は SaveRedemption(b)）。
    FindRedemptions(ctx context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error)
}
```

> **注**: `CalculateDiscount` は「小計を超えない」clamp を行わない — 割引合計が subtotal を
> 超えないガードは **コアの請求パイプライン**が担う（§5.1 手順3の割引上限ガード）。

### 6.3 クーポン引換のトランザクション整合性（#185 / #195）

**問題**: 引換（`Redemption`）の永続化と使用回数の加算を `CalculateDiscount`（＝**計算フック**、
`tx.Run` の**前**に発火）の副作用として行うと、以下が壊れる。

- **Burn（消し込み過ぎ）**: 割引フェーズで引換保存に成功 → tx 内の重複再チェックや
  `Invoices.Save` が失敗してロールバック → 請求書も割引も無いのに単回クーポンが恒久的に消費される。
- **二重引換**: `GenerateInvoice` のリトライ、または同一期間の `RegenerateInvoice` が
  パイプラインを再実行 → 1 請求期間に対して引換行と使用回数が二重に加算される。
  `usageLimit` 付きプロモが黙って枯渇し、`perAccountUsageLimit=1` は
  **実際に成功するリトライで割引を拒否**してしまう。

**設計**: 「計算」と「引換確定」を分離する。

1. `CalculateDiscount` は**永続化の副作用を持たない**（計算して `ctx.RecordDiscount(...)` するだけ）。
2. `CouponPlugin` は `InvoiceLifecycleHook` も実装し、`AfterCalculation`（**tx 内・請求書生成後・
   保存前**に発火、§5.1 手順9）で引換を確定する。ここでは `invoice` から請求期間と請求書 ID を取得できる。
3. 引換は **冪等**。キーは `(couponID, contractID, billingPeriod)`（`Redemption.IdempotencyKey()`）。
   `SaveRedemption` は同一キーの2回目以降を no-op にする。
4. 使用回数は redemption 行から**再構成**する（別カウンタを持たない）。`CalculateDiscount` 内の
   グローバル/アカウント別の上限判定は、**進行中の (contract, period)** を除外して redemption を
   数える（リトライ安全）が、これは**アドバイザリ（best-effort な読み取り）**である。
5. `Coupon.usedCount` は**マイグレーションベースライン専用**。redemption 行が存在する以前の
   歴史的使用分（カウンタしか持たない旧システムからの移行等）だけを表し、
   **プラグインは決してインクリメントしない**。グローバル上限判定は
   `usedCount + count(redemption 行) >= usageLimit`。したがって1回の使用は
   「usedCount に反映済み」**または**「redemption 行がある」の**どちらか一方**で
   なければならない（両方だと二重カウント）。`RedemptionLimits.GlobalBaseline` に渡すのも
   この `usedCount` である。
6. **使用上限の権威ある強制は `SaveRedemption` が原子的に行う（#195）**。プラグインは確定対象
   クーポンの上限を `RedemptionLimits`（`GlobalLimit` / `GlobalBaseline` / `PerAccountLimit`）に
   詰めて `SaveRedemption` へ渡し、リポジトリが「冪等チェック → 上限カウント → 挿入」を1つの
   直列化された原子操作として実行する。超過時は `ErrUsageLimitReached` を返す。

**#195 が塞ぐ欠陥（#185 後の残存 TOCTOU）**: `CalculateDiscount` の上限判定は check-then-act の
「読み取り」に過ぎない。異なる契約に対する2つの並行 `GenerateInvoice` が両方とも
「99 < 100」を読んで両方 discount を適用し、両方の `AfterCalculation` が異なるキーを挿入すると、
100 回上限のプロモが 101 回引換されうる（`perAccountUsageLimit` も、同一アカウントの別
契約/期間で同型）。原子的な `SaveRedemption`(手順6) が敗者の確定を `ErrUsageLimitReached` で
拒否することでこれを塞ぐ。

**保証される不変条件**:

- **(i)** パイプライン失敗は使用回数を恒久消費しない（ロールバック後の引換はリトライが同一キーで再利用）。
- **(ii)** 同一期間のリトライ / `RegenerateInvoice` はちょうど1回だけ消費する。
- **(iii)** 同一キーの並行確定は1件の引換に収束する（リポジトリの冪等契約）。
- **(iv)** DISTINCT キー同士でも、グローバル/アカウント別の使用上限を**厳密に**超えない（#195）。
  上限 L に対する N 並行確定はちょうど L 件だけ成功し、残りは `ErrUsageLimitReached` で拒否される。
  敗者の `AfterCalculation` は説明的エラー（センチネルをラップ）を返し tx をロールバックさせる。
  リトライ時の `CalculateDiscount` は勝者の確定済み行を数えるため枯渇クーポンをスキップし、
  割引無しの請求書へ収束する（プラグインのリポジトリは請求 tx の一部ではないので勝者の行は残る）。

> **リポジトリ実装の指針**: `SaveRedemption` は
> `(coupon_id, contract_id, period_start, period_end)` の UNIQUE 制約で冪等性を、
> **クーポン ID をキーにした直列化（advisory lock / SERIALIZABLE tx /
> `INSERT ... ON CONFLICT DO NOTHING` + 同一ロック下でのカウント再チェック）** で上限強制の
> 原子性を担保する。インメモリ実装は (a)〜(c) 全体を1つの mutex で囲む —
> 出荷済みのリファレンス実装は `infrastructure/inmemory` の `CouponRepository`（issue #240）。
> `AfterCalculation` からの
> 引換確定エラーは致命（tx をロールバック）— 「請求書は保存されたのにクーポン使用が記録されない」
> 状態、および上限超過での過剰引換を防ぐ。抽象的な発火順序・可観測性は §5.1 を参照。
> 実装リファレンスは `plugins/coupon/plugin.go`（`AfterCalculation` が `RedemptionLimits` を
> 渡し、`ErrUsageLimitReached` をラップして返す）。

> **⚠️ アップグレード注意（#185 以前のデータ）**: 旧実装は1回の使用につき redemption 行の保存
> （`SaveRedemption`）**と** `usedCount` の加算（`RecordUsage`）の**両方**を行っていた。
> 既存データを持つデプロイメントがそのままアップグレードすると、歴史的使用が
> ベースラインと行の両方で**二重カウント**され、グローバル上限に早く到達する
> （例: 上限100・歴史的使用40のプロモは、新規20回で 40+40+20=100 に達してブロックされる）。
> デプロイ前に一度だけ、**(a)** redemption 行が既にある使用分を `usedCount` から差し引く
> （全使用が行を持つ場合は 0 にリセット）、**または (b)** `usedCount` に反映済みの歴史的
> redemption 行を削除する（もしくは `FindRedemptions` の結果から除外する）こと。
> 新規デプロイメント（既存 redemption データなし）は対応不要。

## 7. 税計算プラグイン実装例

> **📌 現行実装は最小構成（無条件10%）**: 公式 `plugins/tax` の `TaxCalculator` は
> **請求先住所を受け取らず**、`JapaneseTaxCalculator` は管轄に関わらず一律10%を返す。
> これは意図的な最小実装であり、**管轄別（JP/国外、軽減税率等）の税率判定は利用者が
> `TaxCalculator` を差し替えて実装する拡張ポイント**である（住所別税率をコアに入れるのは
> 機能追加要望であり、ドキュメント修正の対象ではない）。以下は現行コードに一致する。

```go
// plugins/tax/plugin.go & calculator.go
package tax

import (
    "context"
    "math/big"

    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/plugin"
)

// TaxPlugin 税計算プラグイン
type TaxPlugin struct {
    calculator TaxCalculator
    priority   int
}

func NewTaxPlugin(calculator TaxCalculator) *TaxPlugin {
    return &TaxPlugin{
        calculator: calculator,
        // Priority は **同一 TaxHook 種別内**の順序にしか影響しない（§5.2）。
        // 「割引→税」というフック種別間の順序はコアが構造的に保証するため、
        // この値をどう設定しても DiscountHook より先に実行されることはない。
        // PriorityLow は複数 TaxHook を登録したときの相対順序の既定値にすぎない。
        priority: plugin.PriorityLow,
    }
}

// インターフェース準拠の確認（TaxHookのみ実装）
var _ plugin.TaxHook = (*TaxPlugin)(nil)

func (p *TaxPlugin) Name() string    { return "tax" }
func (p *TaxPlugin) Version() string { return "1.0.0" }
func (p *TaxPlugin) Priority() int   { return p.priority }

// Initialize は "priority" があれば priority を上書きする（Config.Int 経由、issue #239）
func (p *TaxPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if n, ok, err := config.Int("priority"); err != nil {
        return fmt.Errorf("tax: %w", err)
    } else if ok {
        p.priority = n
    }
    return nil
}

func (p *TaxPlugin) Shutdown(_ context.Context) error { return nil }

// CalculateTax TaxHookの実装
// DiscountHookやInvoiceLifecycleHookの空実装は不要
func (p *TaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
    afterDiscount := ctx.SubtotalAfterDiscount()
    // 現行実装は住所を参照しない（管轄別税率は利用者が TaxCalculator を差し替えて実装）
    taxRate := p.calculator.GetTaxRate(ctx.Context())
    // nil レートは契約違反として ErrCodeBusinessRule で拒否する（Money.Multiply は
    // nil 係数で panic するため、*PluginPanicError ではなく行動可能なエラーに変える）
    if taxRate == nil {
        return shared.Zero(afterDiscount.Currency()), shared.NewDomainError(
            shared.ErrCodeBusinessRule, "tax: TaxCalculator.GetTaxRate returned a nil rate ...")
    }
    tax := afterDiscount.Multiply(taxRate)
    return tax, nil
}

// TaxCalculator 税率計算インターフェース（住所引数なしの最小 IF）
type TaxCalculator interface {
    // GetTaxRate は適用税率を返す。
    // 契約: 戻り値は **非 nil**。「税なし」は nil ではなく明示的なゼロ率
    // big.NewRat(0, 1) を返すこと（nil は CalculateTax が ErrCodeBusinessRule で
    // 拒否し、その請求書の計算を veto する）。
    GetTaxRate(ctx context.Context) *big.Rat
}

// JapaneseTaxCalculator 日本の消費税計算（管轄判定なし・一律10%）
type JapaneseTaxCalculator struct{}

func (c *JapaneseTaxCalculator) GetTaxRate(_ context.Context) *big.Rat {
    return big.NewRat(10, 100)
}
```

> **管轄対応の拡張例**: 越境請求で住所別の税率を適用したい場合は、`TaxCalculator` の
> 実装を差し替える（例: `GetTaxRate` 内で `ctx` から請求先国を解決して分岐、または
> `contract.BillingAddress()` を参照する独自 IF を定義する）。コアの `TaxHook` 契約は
> `CalculateTax(ctx *CalculationContext)` のままなので、プラグイン側の差し替えで完結する。

## 8. アプリケーション層での統合

### 8.1 請求サービスでのプラグイン利用

> **正準はソース**: 完全な実装は `application/service/billing_service.go` を参照。
> 以下は請求パイプライン（`GenerateInvoice` → `executeBillingPipeline`）の**要点の抜粋**である。
> フルコピーは陳腐化しやすいため掲載しない。設定・構築系は `docs/api/services.md` も参照。

**設定・構築（要点）**

- `BillingConfig`（`billing_config.go`）: `GracePeriod` / `DaysUntilDue` /
  `CollectionMethod CollectionMethod`（`CollectionAutoCharge = "charge_automatically"`
  または `CollectionSendInvoice = "send_invoice"`）/ `AllowPartialPayment bool`。
  検証付き構築は `NewBillingConfig(opts...)`（デフォルト: GracePeriod 1h / DaysUntilDue 30 /
  CollectionMethod CollectionAutoCharge）。
  - **`GracePeriod` と `CollectionMethod` は統合者が解釈する情報フィールド**であり、
    **コアの請求パイプラインはこれらを読まない**。`NewBillingConfig` が値を検証するだけで、
    コアは `GracePeriod` に基づいて自動 finalize せず、`CollectionMethod` に基づいて自動課金しない。
    finalize のタイミングは統合者のスケジューラが `FinalizeInvoice` を呼ぶことで決まり、
    回収方法（自動課金 / 請求書送付）も統合者のフローが解釈する（issue #154）。
  - **`DaysUntilDue` の起点は請求書の発行日（生成時の `clock.Now()`）**であり、請求期間末
    （`period.End()`）ではない。ゼロ値は利用時に 30 日へフォールバックするため、
    `BillingConfig{}`（ゼロ値）でも即日期限にはならない（issue #154）。
- `NewBillingService(contractRepo, invoiceRepo, usageRepo, balanceConfig, priceRepo,
  productRepo, registry, config, clock, opts...)`。オプション: `WithBalanceRepo`（クレジット台帳、
  nil 許容）/ `WithBillingTxManager`（既定は `NoopTxManager`）/ `WithBillingLogger`（既定は
  `slog.Default()`）。`balanceRepo` / `logger` / `txManager` はフィールドとして保持される。
  - **`TxManager` は本番では必須**（issue #187）。`WithBillingTxManager` を省くと既定の
    `tx.NewNoopTxManager` にフォールバックし、請求パイプラインの複数書き込み
    （クレジット台帳 FIFO 充当 → Invoice 保存）が**非アトミック**になる。Invoice 保存が失敗すると
    クレジットだけ消費され請求書が存在しない破損状態が残る。`PaymentService` /
    `CreditNoteService`、各バッチプロセッサも同様に本番では実 `TxManager` を配線する。
  - この見落としは静かなので、**既定 Noop へフォールバックした場合は構築時に `Warn` ログを 1 回出す**
    （`tx.WarnIfDefaultNoop`）。インメモリ/デモ/テストで意図的にトランザクション無しにする場合は
    `WithoutTransactions()`（`PaymentService` は `WithoutPaymentTransactions()`、
    `CreditNoteService` は `WithoutCreditNoteTransactions()`）または
    `tx.NewNoopTxManagerExplicit(...)` で明示的にオプトインし、警告を抑止する。
    詳細と破損パターンの列挙は `docs/guides/integration.md` の
    「Transaction Manager (REQUIRED for production)」節を参照。

**`GenerateInvoice(ctx, contractID shared.ContractID, billingPeriod shared.DateRange)` の流れ**

```go
// application/service/billing_service.go（抜粋・要点）
func (s *BillingService) GenerateInvoice(ctx context.Context, contractID shared.ContractID, billingPeriod shared.DateRange) (*invoice.Invoice, error) {
    // 1. 契約集約をロード（tx 中は tx スコープの repo 経由）
    agg, err := s.contractRepoFor(ctx).FindByID(ctx, contractID)
    // ...

    // 2. ステータスガード: billable なステータスのみ請求可（draft/active/trialing/past_due/
    //    suspended）。suspended は SuspensionBillingBehavior により skip/defer を拒否する
    if !billableStatuses[agg.Status()] { /* business_rule DomainError */ }

    // 3. 重複請求ガード（契約タイプ・ステータスに応じた既存請求書チェック）
    if err = s.checkDuplicateInvoice(ctx, agg, billingPeriod); err != nil { return nil, err }

    // 4. 基本料金の算出（Price エンティティ + PricingModel。従量は UsageSummary 集計）
    subtotal, lineItems, err := s.calculateSubtotal(ctx, agg, billingPeriod)

    // 5. 共有パイプラインへ（RegenerateInvoice / GenerateProrationInvoice も同じ）
    return s.executeBillingPipeline(ctx, pipelineInput{ /* agg, subtotal, lineItems, period, duplicateCheck */ })
}
```

`executeBillingPipeline` がプラグインフックを発火する中核であり、順序は §5.1 と一致する:

```go
func (s *BillingService) executeBillingPipeline(ctx context.Context, input pipelineInput) (*invoice.Invoice, error) {
    // 基本料金を最小単位へ丸める（issue #189）。丸めモードは config.TaxRoundingMode（既定 RoundDown）。
    roundingMode := s.config.effectiveTaxRoundingMode()
    subtotal := input.subtotal.RoundToMinorUnit(roundingMode)
    currency := subtotal.Currency()

    // コンテキストは subtotal=Zero で生成される（BeforeCalculation では Subtotal()=0）
    calcCtx := plugin.NewCalculationContext(ctx, input.agg, shared.Zero(currency))
    calcCtx.SetBillingPeriod(input.period) // 全計算フックの前に請求期間を公開（issue #185）

    // ProductID を Price から解決してコンテキストへ（BeforeCalculation でも参照可能）
    if priceID := input.agg.PriceID(); priceID != "" {
        price, _ := s.priceRepo.FindByID(ctx, priceID)
        calcCtx.SetProductID(price.ProductID())
    }

    // BeforeCalculation（この時点で Subtotal() はゼロ）。全フックは SafeInvoke でラップ（§5.4）。
    for _, h := range s.registry.GetInvoiceLifecycleHooks() {
        plugin.SafeInvoke("InvoiceLifecycleHook.BeforeCalculation", h.Name(), func() error { return h.BeforeCalculation(calcCtx) })
    }

    calcCtx.SetSubtotal(subtotal) // ← ここで初めて基本料金がコンテキストへ

    // DiscountHook（SafeInvokeMoney）→ 境界検証（負値/通貨、#188）→ totalDiscount を丸め（#189）
    //   → 割引上限ガード → SetSubtotalAfterDiscount
    // TaxHook（SubtotalAfterDiscount に対して）→ 境界検証 → totalTax を丸め（#189）
    //   → total = afterDiscount + tax

    invoiceID := shared.NewInvoiceID()
    err := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
        // tx 内で重複チェック再実行（issue #149）
        if input.duplicateCheck != nil { /* ... */ }

        // クレジット台帳から FIFO 充当（entry.Consume + BalanceApplication を作成、tx スコープ repo で保存）
        appliedBalance, _ := s.applyBalances(txCtx, repos.Balances, agg.AccountID(), invoiceID, total, currency)
        amountDue, _ := total.Subtract(appliedBalance)

        // draft 請求書を生成（NewInvoice は error も返す。WithAllowPartialPayment 等を伝播）
        inv, _ = invoice.NewInvoice(invoiceID, agg.AccountID(), input.contractID, subtotal, totalDiscount, totalTax, invOpts...)
        calcCtx.SetInvoice(inv)

        // AfterCalculation は **保存(Save)より前**に発火（SafeInvoke でラップ、パニックは tx を突き抜けない）
        for _, h := range s.registry.GetInvoiceLifecycleHooks() {
            plugin.SafeInvoke("InvoiceLifecycleHook.AfterCalculation", h.Name(), func() error { return h.AfterCalculation(calcCtx, inv) })
        }

        return repos.Invoices.Save(txCtx, inv) // 保存
    })
    return inv, err
}
```

**その他のメソッド（実在するもの）**

- `RegenerateInvoice` — void 済み請求書がある期間の再生成（revisionOf/originalInvoiceID をリンク）。
- `GenerateProrationInvoice` — アップグレード差額（正の AdjustmentAmount のみ）を同パイプラインで請求。
- `FinalizeInvoice` — draft→finalized を tx + `RetryOnConflict` で確定し、保存後に
  `OnInvoiceIssuedHook`（非致命）を発火。

> **注**: 旧版の本節に載っていた `ProcessPriceChange` / `calculateDueDate` は実コードに存在しない
> （due date は `executeBillingPipeline` 内で `now.AddDate(0, 0, config.effectiveDaysUntilDue())`
> として算出される。`now` は発行日 = `clock.Now()`。ゼロ値の `DaysUntilDue` は 30 日へフォールバック）。
> ダウングレード時の BalancePolicy 分岐は `domain-model.md` を参照。従量課金の基本料金算出
> （`calculateSubtotal` / `calculateUsageCharge`）は Price/Product エンティティ経由であり、
> 廃止済みの `c.Plan()` は使用しない。

## 9. カスタムプラグイン作成ガイド

### 9.1 実装手順

1. **インターフェース選択**: 必要なフックインターフェースを実装
2. **優先度設定**: 他のプラグインとの実行順序を考慮
3. **設定読み込み**: `Initialize`で設定を読み込む
4. **リソース解放**: `Shutdown`でリソースを解放

### 9.2 テスト

```go
// plugins/coupon/plugin_test.go
package coupon

import (
    "context"
    "math/big"
    "testing"

    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/plugin"
)

func TestCouponPlugin_CalculateDiscount(t *testing.T) {
    fixedNow := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
    clock := shared.FixedClock{FixedTime: fixedNow}

    mockRepo := &MockCouponRepository{
        coupons: []*Coupon{
            {
                id:         "coupon-1",
                code:       "SAVE10",
                couponType: CouponTypePercentage,
                value:      big.NewRat(10, 100), // 10%
                validFrom:  fixedNow.Add(-24 * time.Hour),
                validUntil: fixedNow.Add(24 * time.Hour),
            },
        },
    }

    p := NewCouponPlugin(mockRepo, clock)
    p.Initialize(context.Background(), plugin.Config{})

    // CalculationContext を使用（型安全）
    // NewCalculationContext は (ctx, *contract.ContractAggregate, subtotal) の3引数
    var c *contract.ContractAggregate // テスト用の集約を構築する
    ctx := plugin.NewCalculationContext(context.Background(), c,
        shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY))

    discount, err := p.CalculateDiscount(ctx)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    expected := big.NewRat(1000, 1) // 10000 * 10% = 1000
    if discount.Amount().Cmp(expected) != 0 {
        t.Errorf("expected %v, got %v", expected, discount.Amount())
    }

    // 型安全な割引記録の検証
    discounts := ctx.AppliedDiscounts()
    if len(discounts) != 1 {
        t.Fatalf("expected 1 applied discount, got %d", len(discounts))
    }
    if discounts[0].Code != "SAVE10" {
        t.Errorf("expected coupon code SAVE10, got %s", discounts[0].Code)
    }
}
```

## 10. API互換性とバージョニング戦略

### 10.1 Semantic Versioning

> **📌 pre-1.0 注記**: 本ライブラリは現在 **v0.x** である。v1.0.0 までは
> **MINOR バージョン（v0.x.0）に破壊的変更が含まれ得る**。破壊的変更は
> CHANGELOG の該当エントリに **BREAKING** と明記される（pre-v1.0 の運用規約。
> 例: v0.3.0 の `OnPaymentProcessedHook` シグネチャ変更）。
> 以下の表は v1.0.0 以降のバージョニング整理である。

本ライブラリはSemantic Versioning 2.0.0に従う。

| バージョン変更 | 条件 | 例 |
|---------------|------|-----|
| **Major (v2.0.0)** | プラグインインターフェースの破壊的変更 | CalculationContextのフィールド型変更 |
| **Minor (v1.x.0)** | 新規フックの追加、既存フックへのメソッド追加（デフォルト実装あり） | MetricsHookに新メソッド追加 |
| **Patch (v1.x.y)** | バグ修正、ドキュメント修正 | Registry のスレッドセーフ修正 |

### 10.2 破壊的変更の定義

以下をプラグインAPIの破壊的変更と定義する：

1. **インターフェースのメソッド追加**（デフォルト実装なし）
2. **インターフェースのメソッドシグネチャ変更**（引数型・戻り値型の変更）
3. **インターフェースの削除・統合・分離**
4. **CalculationContext/Context型のフィールド削除・型変更**
5. **Registry APIの変更**（Register/Get系メソッド）

### 10.3 初版のフック設計について

本ライブラリは初版から**イベント単位の細粒度フック設計**を採用している。
契約ライフサイクル・支払い・メトリクスは、粗粒度の統合 IF ではなく、各イベントごとの
個別 IF に分かれている（`ContractLifecycleHook` / `PaymentHook` / `MetricsHook` という
粗粒度インターフェースは**存在したことがない**）。

```
請求計算:        DiscountHook / TaxHook / InvoiceLifecycleHook
契約ライフサイクル: OnContractCreate/Activate/Suspend/Resume/Cancel/CancelScheduled/CancelUnscheduled/Renew/TrialEndHook（9種）
支払い:          BeforeChargeHook / AfterChargeHook / OnPaymentFailedHook / OnRefundHook / OnCompensationExecutedHook（5種）
メトリクス:       OnContractChangeHook / OnInvoiceIssuedHook / OnPaymentProcessedHook（3種）
クレジットノート:   OnCreditNoteIssuedHook / OnInvoiceRevisedHook（2種）
請求書生成:       InvoiceGenerationHook（1種）
```

（完全な定義は §3、カテゴリ別一覧は §5.3 を参照。合計23種。
`OnCompensationExecutedHook` は #257 で追加された（SemVer minor、新規フックの追加）。）

`InvoiceCalculationHook`（割引・税・ライフサイクルの統合インターフェース）は
ISP違反と計算順序の脆さの懸念から、設計段階で分割を決定した。
そのため、v1.x→v2.0.0の移行ガイドや非推奨プロセスは不要。

### 10.4 カスタムプラグイン開発者向けガイドライン

```go
// 割引プラグインを作る場合:
// DiscountHook のみ実装すればよい（TaxHookの空実装は不要）
var _ plugin.DiscountHook = (*MyDiscountPlugin)(nil)

type MyDiscountPlugin struct{}
func (p *MyDiscountPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    // 割引ロジック
}

// 税計算プラグインを作る場合:
// TaxHook のみ実装すればよい
var _ plugin.TaxHook = (*MyTaxPlugin)(nil)

type MyTaxPlugin struct{}
func (p *MyTaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
    // ctx.SubtotalAfterDiscount() で割引後の金額を参照可能
}

// 割引と税の両方を1プラグインで実装する場合:
// DiscountHook と TaxHook の両方を実装
var _ plugin.DiscountHook = (*MyBillingPlugin)(nil)
var _ plugin.TaxHook = (*MyBillingPlugin)(nil)

type MyBillingPlugin struct{}
func (p *MyBillingPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) { ... }
func (p *MyBillingPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) { ... }
// Registry が型アサーションで両方のフックに自動登録する
```

## 11. トランザクショナル・アウトボックス（#248）

### 11.1 解決する問題（通知消失）

コアが自動発火する支払い/請求フック（`AfterChargeHook` / `OnPaymentProcessedHook` /
`OnInvoiceIssuedHook`）は、いずれも**書き込みトランザクションがコミットした後**（post-commit）に
走る（§5.3）。統合者がこれらのフックから耐久通知（webhook イベント等）の enqueue を行うと、
その enqueue を「支払い/請求レコードを書いた同じ tx」に join できない。コミットとフック実行の
あいだにプロセスがクラッシュすると、`payment.charged` / `contract.first_payment` のような
イベントが**恒久的に消失**する（#248 / platform#45）。

トランザクショナル・アウトボックスは、通知行を**業務レコードと同一の tx で INSERT** し、
実配信は別プロセス（relay / poller）がアウトボックス表を読んで非同期に行うことで、
「レコードは書けたが通知は消えた」を構造的に無くす。コアはこの「同一 tx 内 INSERT」の
発火点を一級の手段として提供する。

### 11.2 採用設計（Design W）: Writer ポート（フックではない）

新しいプラグインフックは**追加しない**（#248 はフック総数を変えない。当時 22、現在は
#257 を含め §10.3 のとおり **23**）。代わりに、
統合者が実装する **OutboxWriter ポート**をコアが tx 内 Save 直後・コミット前に呼ぶ。行の
組み立て（自分の webhook スキーマ・イベント語彙・初回判定 I/O）は統合者側が自由に行う。

**なぜプラグインフックにしないか**:

1. **動機イベントが I/O を要する** — `contract.first_payment`（初回支払いか否か）の判定には
   過去の支払い有無を調べる I/O が要る。計算パイプラインの純粋 build フック（I/O 禁止）では
   この初回判定を実装できない。
2. **precedent との整合** — `credit_note_service.go` は「狭いイベントのために新フックを足さない」
   方針を採っており、それに倣う。
3. **依存を増やさない** — フック化すると `plugin → application/port` の新依存が要る。Writer
   ポートなら `application/port` に閉じ、`plugin` パッケージ・registry は無変更で済む。

### 11.3 ポート定義

```go
// application/port/outbox.go
type PaymentOutboxWriter interface {
    // ProcessPayment の記録 tx 内（両 Save 成功直後・コミット前）で呼ばれる。
    OnPaymentRecorded(ctx context.Context, p *payment.Payment, inv *invoice.Invoice) error
}

type InvoiceOutboxWriter interface {
    // FinalizeInvoice の Save 成功直後・コミット前で呼ばれる。
    OnInvoiceFinalized(ctx context.Context, inv *invoice.Invoice) error
}
```

配線は `service.WithPaymentOutboxWriter(w)` / `service.WithInvoiceOutboxWriter(w)`。
**未配線（nil）ならアウトボックス段は完全スキップ**され、既存挙動は不変（回帰なし）。

**実装契約（必読）**:

- **ctx は tx スコープ**。統合者はこの ctx から現在の tx を取り出し（例:
  `QuerierFromContext(ctx)`）、自分の outbox 表へ**相乗り INSERT** する。別コネクション/別 tx を
  使うと原子性が黙って壊れる（回帰が静か）。
- **軽量な INSERT のみ**。外部通信・重処理・実 webhook 配信は禁止（tx を長時間握ると
  ロック/コネクション枯渇）。実配信は別 relay/poller が outbox を読んで行う。
- **at-least-once / dedup**: リトライ・冪等リプレイ収束で同一 (payment/invoice) に対して
  複数回呼ばれ得る。行は冪等キー（payment ID 等）で dedup 前提にする。
- **NoopTxManager 使用時は原子性ゼロ**（§11.6）。本番は実 TxManager 必須。

### 11.4 発火パス（発火 / スキップ）

コアは「**新しい payment/invoice 状態を実際に永続化するパス**」でだけ Writer を呼ぶ。
`return nil` すべてではなく、Save 直後の該当パスに限定する（下表）。

**`PaymentService.ProcessPayment`（ゲートウェイ経路）**

| パス | Writer |
|------|--------|
| Pending→Completed 昇格（両 Save 成功直後） | **発火** `OnPaymentRecorded(existing, inv)` |
| 通常成功（両 Save 成功直後） | **発火** `OnPaymentRecorded(p, inv)` |
| in-tx Completed 冪等リプレイ（新規 Save なし） | スキップ |
| terminal state → error / `errDuplicateKeyRaceSignal` | スキップ |
| pre-charge Completed short-circuit（tx に入る前に return） | スキップ |
| post-RunInTx の raced-loser 収束（tx 外・勝者が既に書いた） | スキップ |

**`PaymentService.settleZeroAmountPayment`（zero-amount 経路）**

| パス | Writer |
|------|--------|
| 昇格（両 Save 成功直後） | **発火** `OnPaymentRecorded(existing, inv)` |
| 通常成功（両 Save 成功直後） | **発火** `OnPaymentRecorded(p, inv)` |
| Completed 冪等リプレイ | スキップ |
| post-tx の raced-loser 収束（Save が `errDuplicateKeyRaceSignal` でクロージャを中断 → tx 終了後に `convergeOnDuplicateKeyWinner` が勝者へ収束。新規 Save なし、#241b） | スキップ |

**`PaymentService.SettlePayment`（非同期決済のセトルメント経路、payment-gateway.md §6.5）**

| パス | Writer |
|------|--------|
| Pending→Completed セトルメント（両 Save 成功直後・コミット前） | **発火** `OnPaymentRecorded(loaded, inv)` |
| Completed 冪等リプレイ（新規 Save なし） | スキップ |
| terminal state 拒否（invalid_state_transition でエラー） | スキップ |

> **veto のリスク級**: `SettlePayment` での Writer veto は**ゲートウェイの金銭移動を伴わない**
> （入金は既に顧客の払込で完了しており、コアが取り消すべき課金が存在しない）。ロールバックは
> webhook の再配送（at-least-once）による再試行で無害に収束する — リスク級は
> `OnInvoiceFinalized` と同じであり、ProcessPayment の「課金取消」級ではない（§11.5）。

**`BillingService.FinalizeInvoice`**

| パス | Writer |
|------|--------|
| `Invoices.Save(loaded)` 成功直後・コミット前 | **発火** `OnInvoiceFinalized(loaded)` |
| load 失敗 / not-found / `Finalize()` 拒否（invalid_state_transition） | スキップ |

正準はソース（`application/service/payment_service.go` の `firePaymentOutbox`、
`payment_settlement.go` の `SettlePayment`、`billing_service.go` の `fireInvoiceOutbox`）。

### 11.5 B1: veto と課金取消のトレードオフ（2 経路のリスク非対称）

Writer が返す **error（および recover したパニック）は tx をロールバックさせる**（veto）。
2 経路でリスクの重みが異なる:

- **`OnPaymentRecorded` の veto は「成功済みゲートウェイ課金の取消」を伴う**。tx が
  ロールバックすると saga 補償が発火し、課金が **Void（不成立なら Refund）で巻き戻る**。
  これは「原子性の代償」であり、既存の「payment 保存失敗 → 課金取消」と**同じ経路**。
  一過性エラーで実返金が走るため、`OnPaymentRecorded` は**冪等で堅牢な軽量 INSERT** に限る。
  リカバリ可能な理由で失敗させてはならない。
- **`OnInvoiceFinalized` の veto は金銭移動を伴わない**。`FinalizeInvoice` は
  `RetryOnConflict` 内で走るが、Writer の error は version-conflict ではないので再試行されず
  そのまま伝播する。ロールバックは無害な「次回の再 finalize」で済む（ProcessPayment と非対称）。

### 11.6 パニック隔離・Noop 警告・OnInvoiceIssued との住み分け

- **パニック隔離**: Writer 呼び出しは `plugin.SafeInvoke("PaymentOutboxWriter.OnPaymentRecorded",
  "outbox", ...)`（invoice 側も同様）でラップされ、パニックは `*plugin.PluginPanicError` に
  変換されて tx.Run クロージャから **error として返る**（tx を突き抜けない）。§5.4 の
  「tx 内・保存前」カテゴリと同じ扱い。
- **NoopTxManager 警告**: Writer が配線されているのに txManager が既定 Noop（暗黙フォールバック）
  の場合、構築時に専用の `Warn` を 1 回出す（payment 保存と outbox INSERT が非原子である旨）。
  既存の `tx.WarnIfDefaultNoop`（multi-write 非原子）に加えた追加警告。明示的 Noop
  （`WithoutPaymentTransactions` / `WithoutTransactions`）は抑止される。
- **`OnInvoiceIssuedHook` との住み分け**: outbox writer（in-tx・確実配信）と post-commit の
  `OnInvoiceIssuedHook`（非致命・メトリクス等）は**併用可能**で、両方配線した統合者は
  同一 finalize で 2 系統が発火し得る。**確実配信は outbox、best-effort な集計は
  OnInvoiceIssued** と役割を分ける。`AfterChargeHook` / `OnPaymentProcessedHook` も同様に残る。

### 11.7 スコープ外（follow-up）

`Refund`（`OnRefund`）と支払い失敗（`OnPaymentFailed`）の in-tx outbox は今回対象外。
#248 が名指しするのは **ProcessPayment 成功 + FinalizeInvoice の 2 経路**であり、これらは
follow-up issue とする。

### 11.8 SemVer

新ポート（`PaymentOutboxWriter` / `InvoiceOutboxWriter`）と新オプション
（`WithPaymentOutboxWriter` / `WithInvoiceOutboxWriter`）の**追加のみ**。既存挙動は writer
未配線で不変。#248 はフック総数を変えない（現在の総数は §10.3 の 23）。よって **Minor**。
