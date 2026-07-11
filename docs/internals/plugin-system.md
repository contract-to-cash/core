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

// OnPaymentProcessedHook 支払い処理メトリクス
type OnPaymentProcessedHook interface {
    Plugin
    OnPaymentProcessed(ctx *Context, payment *payment.Payment) error
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
    onContractCreateHooks   []OnContractCreateHook
    onContractActivateHooks []OnContractActivateHook
    onContractSuspendHooks  []OnContractSuspendHook
    onContractResumeHooks   []OnContractResumeHook
    onContractCancelHooks   []OnContractCancelHook
    onContractRenewHooks    []OnContractRenewHook
    onContractTrialEndHooks []OnContractTrialEndHook

    // 支払いフック（ISP分離）
    beforeChargeHooks    []BeforeChargeHook
    afterChargeHooks     []AfterChargeHook
    onPaymentFailedHooks []OnPaymentFailedHook
    onRefundHooks        []OnRefundHook

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
    if h, ok := plugin.(OnContractRenewHook); ok { r.onContractRenewHooks = append(r.onContractRenewHooks, h) }
    if h, ok := plugin.(OnContractTrialEndHook); ok { r.onContractTrialEndHooks = append(r.onContractTrialEndHooks, h) }

    // 支払いフック（ISP分離）
    if h, ok := plugin.(BeforeChargeHook); ok { r.beforeChargeHooks = append(r.beforeChargeHooks, h) }
    if h, ok := plugin.(AfterChargeHook); ok { r.afterChargeHooks = append(r.afterChargeHooks, h) }
    if h, ok := plugin.(OnPaymentFailedHook); ok { r.onPaymentFailedHooks = append(r.onPaymentFailedHooks, h) }
    if h, ok := plugin.(OnRefundHook); ok { r.onRefundHooks = append(r.onRefundHooks, h) }

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

func (r *Registry) sortByPriority(hooks interface{}) {
    // Priority順にソート（小さいほど先）
    // ...
}

// InitializeAll 全プラグインを初期化
func (r *Registry) InitializeAll(ctx context.Context, configs map[string]Config) error {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    for name, plugin := range r.plugins {
        config := configs[name]
        if err := plugin.Initialize(ctx, config); err != nil {
            return fmt.Errorf("failed to initialize plugin %s: %w", name, err)
        }
    }
    
    return nil
}

// ShutdownAll 全プラグインをシャットダウン
func (r *Registry) ShutdownAll(ctx context.Context) error {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    var errs []error
    for _, plugin := range r.plugins {
        if err := plugin.Shutdown(ctx); err != nil {
            errs = append(errs, err)
        }
    }
    
    if len(errs) > 0 {
        return fmt.Errorf("shutdown errors: %v", errs)
    }
    return nil
}

func (r *Registry) GetDiscountHooks() []DiscountHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.discountHooks
}

func (r *Registry) GetTaxHooks() []TaxHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.taxHooks
}

func (r *Registry) GetInvoiceLifecycleHooks() []InvoiceLifecycleHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.invoiceLifecycleHooks
}

// 契約ライフサイクル（各イベント個別）
func (r *Registry) GetOnContractCreateHooks() []OnContractCreateHook { ... }
func (r *Registry) GetOnContractActivateHooks() []OnContractActivateHook { ... }
func (r *Registry) GetOnContractCancelHooks() []OnContractCancelHook { ... }
// ... 他の契約ライフサイクルフックも同様

// 支払い（各イベント個別）
func (r *Registry) GetBeforeChargeHooks() []BeforeChargeHook { ... }
func (r *Registry) GetAfterChargeHooks() []AfterChargeHook { ... }
func (r *Registry) GetOnPaymentFailedHooks() []OnPaymentFailedHook { ... }
func (r *Registry) GetOnRefundHooks() []OnRefundHook { ... }

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
1. InvoiceLifecycleHook.BeforeCalculation()  ← 計算前処理
                                                ⚠️ この時点で ctx.Subtotal() は ZERO
                                                   （ctx.ProductID() は参照可能）
2. 基本料金をコンテキストへ設定（コア、契約タイプに応じて分岐）
   → 以降 ctx.Subtotal() は基本料金を返す
3. DiscountHook.CalculateDiscount()          ← 割引計算（全DiscountHook、ctx.Subtotal()=基本料金）
   → 各フックの戻り値を境界検証（負値は ErrCodeBusinessRule で中断、通貨不一致は
     ErrCodeCurrencyMismatch。いずれもプラグイン名を含む。issue #188）
   → 割引上限ガード（割引合計 > subtotalの場合にcap）
4. 小計算出（コア: subtotal - totalDiscount）→ ctx.SetSubtotalAfterDiscount()
5. TaxHook.CalculateTax()                    ← 税計算（ctx.SubtotalAfterDiscount()に対して）
   → 各フックの戻り値を境界検証（負値は ErrCodeBusinessRule で中断。issue #188）
6. 合計算出（コア: afterDiscount + totalTax）
7. クレジット台帳からの充当（コア、FIFO）    ← 残高があれば税込合計から差引（tx内）
8. 請求書をdraft状態で生成（コア、tx内）→ GracePeriod後に FinalizeInvoice で確定
9. InvoiceLifecycleHook.AfterCalculation()   ← 計算後処理（**保存(Save)より前**に発火、tx内）
10. 保存（コア、tx内）
```

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

全20種のフックのうち、コアが自動発火するのは14種。残りは統合者（サービス開発者）
またはアダプタが発火する。プラグインを書く前に、実装するフックが「誰に呼ばれるか」を
この表で確認すること。

**コアが自動発火するフック（14種）**

| Hook | 発火箇所 |
|------|---------|
| `DiscountHook` | `BillingService` 請求パイプライン（GenerateInvoice / RegenerateInvoice / GenerateProrationInvoice） |
| `TaxHook` | 同上 |
| `InvoiceLifecycleHook` | 同上（BeforeCalculation / AfterCalculation） |
| `OnInvoiceIssuedHook` | `BillingService.FinalizeInvoice`（確定保存後、非致命） |
| `BeforeChargeHook` | `PaymentService.ProcessPayment`（ゲートウェイ課金前） |
| `AfterChargeHook` | `PaymentService.ProcessPayment`（成功パス、非致命） |
| `OnPaymentProcessedHook` | `PaymentService.ProcessPayment`（成功パス、非致命） |
| `OnPaymentFailedHook` | `PaymentService.ProcessPayment`（ゲートウェイ失敗時、非致命） |
| `OnRefundHook` | `PaymentService.Refund`（非致命） |
| `OnCreditNoteIssuedHook` | `CreditNoteService`（発行後、非致命） |
| `OnInvoiceRevisedHook` | `CreditNoteService.ReissueInvoice`（非致命） |
| `OnContractRenewHook` | `batch.ContractRenewalProcessor`（保存後、非致命） |
| `OnContractTrialEndHook` | `batch.TrialExpirationProcessor`（保存後、非致命） |
| `OnContractChangeHook` | `batch.ContractRenewalProcessor`（renewed/cancelled）、`batch.TrialExpirationProcessor`（trial_end） |

> **発火タイミングの注意**: コアが「保存後」に発火するフック（`OnInvoiceIssuedHook` /
> `AfterChargeHook` / `OnPaymentProcessedHook` 等）は、呼び出し側が自前のトランザクション内から
> サービスメソッドを呼んだ場合、`tx.Run` が外側トランザクションにジョインするため
> **外側コミットの前**に発火する。外側をロールバックすると「保存されていないのに通知済み」に
> なるため、外側をロールバックし得る場合はサービス呼び出しをトランザクション外で行うこと。
> また、冪等リプレイの収束（同一冪等キーへの並行リクエスト等）により同一エンティティに対して
> 複数回発火し得るため、メトリクス系フックは対象 ID でのデデュープを前提に実装する。

**統合者が発火するフック（5種）**

契約の Create / Activate / Suspend / Resume / Cancel はコアにアプリケーション
サービスが存在しない（集約メソッドを統合者コードが直接呼ぶ）ため、対応するフックも
統合者が発火する:

- `OnContractCreateHook` / `OnContractActivateHook` / `OnContractSuspendHook` /
  `OnContractResumeHook` / `OnContractCancelHook`

実装リファレンス: `examples/hosting-integration-demo/main.go`（集約の状態遷移を
実行 → 保存 → `registry.GetOnContract*Hooks()` をループして発火するパターン）。

**アダプタが発火するフック（1種）**

- `InvoiceGenerationHook`（BuildDocument / AfterRender / AfterDelivery）—
  請求書のレンダリング・送付パイプラインはコアのスコープ外。
  利用者が実装する請求書生成アダプタが各フェーズで発火する。

### 5.4 パニック隔離とフェイタリティ・ポリシー（issue #193）

プラグインは第三者コードであり、`panic` する可能性がある。コアが発火する全フックは
**`plugin.SafeInvoke`（および `(shared.Money, error)` を返す `SafeInvokeMoney`）** 経由で
呼び出され、`recover()` でパニックを捕捉し、スタックトレース（`runtime/debug.Stack`）を
添えた構造化エラー **`*plugin.PluginPanicError`**（`PluginName` / `HookType` / `Value` /
`Stack` を保持）へ変換する。これにより、暴走したプラグイン 1 つが進行中の請求・支払い
トランザクションを破壊すること（例: 課金成功後に `AfterCharge` がパニックし、
`ProcessPayment` のローカル永続化を突き抜けて「課金済みだが未記録」状態を生む）を防ぐ。

**捕捉したパニックは、フックがエラーを返したのと同じフェイタリティ・ポリシーで扱う。**
フックが「拒否権を持つ（veto-capable）」か「非致命（non-fatal）」かは §5.3 の発火箇所と
一致する:

| 分類 | 対象フック | パニック時の挙動 |
|------|-----------|-----------------|
| **拒否権あり（veto）** | `InvoiceLifecycleHook.BeforeCalculation` / `DiscountHook` / `TaxHook` / `BeforeChargeHook` | パニック → パイプラインエラーとして**伝播**し、操作をクリーンに中断する（フックがエラーを返した場合と同一）。ゲートウェイ課金前・保存前なので副作用は残らない |
| **tx 内・保存前** | `InvoiceLifecycleHook.AfterCalculation` | トランザクション内（Save より前）で発火。パニック → エラーへ変換して `tx.Run` のクロージャから返す。**パニックが `tx.Run` を突き抜けない**ため（バックエンド依存のロールバック挙動を避ける）、tx はクリーンに中断し何も永続化されない |
| **非致命（non-fatal）** | `AfterChargeHook` / `OnPaymentProcessedHook` / `OnPaymentFailedHook` / `OnRefundHook` / `OnInvoiceIssuedHook` / `OnCreditNoteIssuedHook` / `OnInvoiceRevisedHook` / `OnContractRenewHook` / `OnContractTrialEndHook` / `OnContractChangeHook`（バッチ含む） | パニック → `plugin.LogNonFatalHookError` が**プラグイン名・フック種別・スタックを Error レベルでログ**し、処理を継続する。同種の後続フックも通常どおり実行される（1 つのパニックが他フックを止めない） |
| **ライフサイクル** | `InitializeAll` / `ShutdownAll` の `Plugin.Initialize` / `Plugin.Shutdown` | パニック → エラーへ変換して返す。パニックする `Initialize` は起動を**回復不能にクラッシュさせず**、`*PluginPanicError` を含むエラーとして扱う |

> **注**: 統合者が発火するフック（契約 Create/Activate/Suspend/Resume/Cancel の 5 種）と
> アダプタが発火する `InvoiceGenerationHook` はコアの発火経路外のため、コアの
> `SafeInvoke` ラップは適用されない。統合者・アダプタは自コードで同様のパニック隔離を
> 行うことが推奨される（`plugin.SafeInvoke` / `plugin.LogNonFatalHookError` は公開 API なので
> そのまま利用できる）。`examples/hosting-integration-demo/main.go` の発火ループはリファレンス。

**API**:

```go
// plugin/safe.go
func SafeInvoke(hookType, pluginName string, fn func() error) error
func SafeInvokeMoney(hookType, pluginName string, fn func() (shared.Money, error)) (shared.Money, error)
func AsPanic(err error) (*PluginPanicError, bool)   // err が *PluginPanicError を包むか判定
func LogNonFatalHookError(logger *slog.Logger, msg string, err error, attrs ...any)

type PluginPanicError struct {
    PluginName string
    HookType   string
    Value      any    // recover() が返したパニック値
    Stack      []byte // debug.Stack()
}
```

## 6. クーポンプラグイン実装例

### 6.1 プラグイン実装

```go
// plugins/coupon/plugin.go
package coupon

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/plugin"
)

// CouponPlugin クーポンプラグイン
// DiscountHook のみを実装する（TaxHookやInvoiceLifecycleHookの空実装は不要）
type CouponPlugin struct {
    repo     CouponRepository
    config   CouponConfig
    priority int
    clock    shared.Clock
}

// インターフェース準拠の確認（コンパイル時チェック）
var _ plugin.DiscountHook = (*CouponPlugin)(nil)

type CouponConfig struct {
    MaxCouponsPerInvoice int
    AllowStacking        bool // 複数クーポン併用可否
}

func NewCouponPlugin(repo CouponRepository, clock shared.Clock) *CouponPlugin {
    return &CouponPlugin{
        repo:     repo,
        priority: plugin.PriorityNormal,
        clock:    clock,
    }
}

func (p *CouponPlugin) Name() string    { return "coupon" }
func (p *CouponPlugin) Version() string { return "1.0.0" }
func (p *CouponPlugin) Priority() int   { return p.priority }

func (p *CouponPlugin) Initialize(ctx context.Context, config plugin.Config) error {
    if v, ok := config["maxCouponsPerInvoice"].(int); ok {
        p.config.MaxCouponsPerInvoice = v
    }
    if v, ok := config["allowStacking"].(bool); ok {
        p.config.AllowStacking = v
    }
    return nil
}

func (p *CouponPlugin) Shutdown(ctx context.Context) error {
    return nil
}

// CalculateDiscount DiscountHookの実装
// CalculateTax, BeforeCalculation, AfterCalculation の空実装は不要
func (p *CouponPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    contract := ctx.Contract()
    subtotal := ctx.Subtotal()

    coupons, err := p.repo.FindApplicable(ctx.Context(), contract.ContractID(), p.clock.Now())
    if err != nil {
        return shared.Money{}, err
    }

    if len(coupons) == 0 {
        return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
    }

    totalDiscount := shared.Zero(subtotal.Currency())
    for i, coupon := range coupons {
        if !p.config.AllowStacking && i > 0 {
            break
        }
        if p.config.MaxCouponsPerInvoice > 0 && i >= p.config.MaxCouponsPerInvoice {
            break
        }

        discount, err := coupon.CalculateDiscount(subtotal)
        if err != nil {
            return shared.Money{}, fmt.Errorf("coupon discount calculation failed: %w", err)
        }
        totalDiscount, err = totalDiscount.Add(discount)
        if err != nil {
            return shared.Money{}, fmt.Errorf("coupon discount accumulation failed: %w", err)
        }

        // 型安全な割引記録（metadata[string]interface{} ではない）
        ctx.RecordDiscount(plugin.AppliedDiscount{
            PluginName: p.Name(),
            Code:       coupon.Code(),
            Amount:     discount,
        })
    }

    return totalDiscount, nil
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

// CouponRepository クーポンリポジトリ（実際のシグネチャは型付き ID を使う）
type CouponRepository interface {
    FindByCode(ctx context.Context, code string) (*Coupon, error)
    FindApplicable(ctx context.Context, query CouponQuery) ([]*Coupon, error)
    Save(ctx context.Context, coupon *Coupon) error
    // SaveRedemption は (couponID, contractID, billingPeriod) で冪等（#185, §6.3）。
    // 同一キーの2回目以降は no-op（重複行を作らず、使用回数も増やさない）。
    SaveRedemption(ctx context.Context, redemption *Redemption) error
    // FindRedemptions は使用回数の唯一の情報源。プラグインが redemption 行を数えて
    // グローバル/アカウント別の使用上限を（進行中の (contract, period) を除外して）判定する。
    FindRedemptions(ctx context.Context, couponID CouponID, accountID *shared.AccountID) ([]*Redemption, error)
}
```

> **注**: `CalculateDiscount` は「小計を超えない」clamp を行わない — 割引合計が subtotal を
> 超えないガードは **コアの請求パイプライン**が担う（§5.1 手順3の割引上限ガード）。

### 6.3 クーポン引換のトランザクション整合性（#185）

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
4. 使用回数は redemption 行から**再構成**する（別カウンタを持たない）。グローバル/アカウント別の
   上限判定は、**進行中の (contract, period)** を除外して redemption を数える（リトライ安全）。
5. `Coupon.usedCount` は**マイグレーションベースライン専用**。redemption 行が存在する以前の
   歴史的使用分（カウンタしか持たない旧システムからの移行等）だけを表し、
   **プラグインは決してインクリメントしない**。グローバル上限判定は
   `usedCount + count(redemption 行) >= usageLimit`。したがって1回の使用は
   「usedCount に反映済み」**または**「redemption 行がある」の**どちらか一方**で
   なければならない（両方だと二重カウント）。

**保証される不変条件**:

- **(i)** パイプライン失敗は使用回数を恒久消費しない（ロールバック後の引換はリトライが同一キーで再利用）。
- **(ii)** 同一期間のリトライ / `RegenerateInvoice` はちょうど1回だけ消費する。
- **(iii)** 同一キーの並行確定は1件の引換に収束する（リポジトリの冪等契約。DISTINCT キー同士の
  グローバル上限 TOCTOU の完全なハードニングは #195 で別途対応）。

> **リポジトリ実装の指針**: `SaveRedemption` は
> `(coupon_id, contract_id, period_start, period_end)` の UNIQUE 制約 + upsert /
> insert-or-ignore で冪等性と並行安全性を担保する。`AfterCalculation` からの引換確定エラーは
> 致命（tx をロールバック）— 「請求書は保存されたのにクーポン使用が記録されない」状態を防ぐ。
> 抽象的な発火順序・可観測性は §5.1 を参照。実装リファレンスは `plugins/coupon/plugin.go`。

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
        priority:   plugin.PriorityLow, // 割引より後に実行
    }
}

// インターフェース準拠の確認（TaxHookのみ実装）
var _ plugin.TaxHook = (*TaxPlugin)(nil)

func (p *TaxPlugin) Name() string    { return "tax" }
func (p *TaxPlugin) Version() string { return "1.0.0" }
func (p *TaxPlugin) Priority() int   { return p.priority }

// Initialize は config["priority"] があれば priority を上書きする
func (p *TaxPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if v, ok := config["priority"]; ok {
        if n, ok := v.(int); ok {
            p.priority = n
        }
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
    tax := afterDiscount.Multiply(taxRate)
    return tax, nil
}

// TaxCalculator 税率計算インターフェース（住所引数なしの最小 IF）
type TaxCalculator interface {
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
    // コンテキストは subtotal=Zero で生成される（BeforeCalculation では Subtotal()=0）
    calcCtx := plugin.NewCalculationContext(ctx, input.agg, shared.Zero(currency))

    // ProductID を Price から解決してコンテキストへ（BeforeCalculation でも参照可能）
    if priceID := input.agg.PriceID(); priceID != "" {
        price, _ := s.priceRepo.FindByID(ctx, priceID)
        calcCtx.SetProductID(price.ProductID())
    }

    // BeforeCalculation（この時点で Subtotal() はゼロ）
    for _, h := range s.registry.GetInvoiceLifecycleHooks() { h.BeforeCalculation(calcCtx) }

    calcCtx.SetSubtotal(subtotal) // ← ここで初めて基本料金がコンテキストへ

    // DiscountHook → 割引上限ガード → SetSubtotalAfterDiscount
    // TaxHook（SubtotalAfterDiscount に対して）→ total = afterDiscount + tax

    invoiceID := shared.NewInvoiceID()
    err := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
        // tx 内で重複チェック再実行（issue #149）
        if input.duplicateCheck != nil { /* ... */ }

        // クレジット台帳から FIFO 充当（entry.Consume + BalanceApplication を作成、tx スコープ repo で保存）
        appliedBalance := s.applyBalances(txCtx, repos.Balances, agg.AccountID(), invoiceID, total, currency)
        amountDue := total.Subtract(appliedBalance)

        // draft 請求書を生成（WithAllowPartialPayment(s.config.AllowPartialPayment) 等を伝播）
        inv = invoice.NewInvoice(invoiceID, agg.AccountID(), input.contractID, subtotal, totalDiscount, totalTax, invOpts...)
        calcCtx.SetInvoice(inv)

        // AfterCalculation は **保存(Save)より前**に発火
        for _, h := range s.registry.GetInvoiceLifecycleHooks() { h.AfterCalculation(calcCtx, inv) }

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

本ライブラリは初版（v1.0.0）から**イベント単位の細粒度フック設計**を採用している。
契約ライフサイクル・支払い・メトリクスは、粗粒度の統合 IF ではなく、各イベントごとの
個別 IF に分かれている（`ContractLifecycleHook` / `PaymentHook` / `MetricsHook` という
粗粒度インターフェースは**存在したことがない**）。

```
請求計算:        DiscountHook / TaxHook / InvoiceLifecycleHook
契約ライフサイクル: OnContractCreate/Activate/Suspend/Resume/Cancel/Renew/TrialEndHook（7種）
支払い:          BeforeChargeHook / AfterChargeHook / OnPaymentFailedHook / OnRefundHook（4種）
メトリクス:       OnContractChangeHook / OnInvoiceIssuedHook / OnPaymentProcessedHook（3種）
クレジットノート:   OnCreditNoteIssuedHook / OnInvoiceRevisedHook（2種）
請求書生成:       InvoiceGenerationHook（1種）
```

（完全な定義は §3、カテゴリ別一覧は §5.3 を参照。合計20種。）

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
