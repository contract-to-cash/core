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
    subtotal              shared.Money           // 基本料金
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
func (c *CalculationContext) Subtotal() shared.Money                     { return c.subtotal }
func (c *CalculationContext) SubtotalAfterDiscount() shared.Money        { return c.subtotalAfterDiscount }
func (c *CalculationContext) AppliedDiscounts() []AppliedDiscount        { /* コピーを返す */ }

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
2. 料金計算（コア、契約タイプに応じて分岐）
3. DiscountHook.CalculateDiscount()          ← 割引計算（全DiscountHook）
   → 割引上限ガード（割引合計 > subtotalの場合にcap）
4. 小計算出（コア: subtotal - totalDiscount）
5. TaxHook.CalculateTax()                    ← 税計算（割引後に対して）
6. 合計算出（コア: afterDiscount + totalTax）
7. クレジット台帳からの充当（コア）          ← 残高があれば税込合計から差引
8. 請求書をdraft状態で生成 → GracePeriod後にfinalize
9. InvoiceLifecycleHook.AfterCalculation()   ← 計算後処理
```

> **注**: このフロー順序は `architecture.md` セクション5.2 と同一。

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

        discount := coupon.CalculateDiscount(subtotal)
        var err error
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
    "time"
    
    "github.com/contract-to-cash/core/domain/shared"
)

type CouponID string

type CouponType string

const (
    CouponTypePercentage CouponType = "percentage" // 割合割引
    CouponTypeFixed      CouponType = "fixed"      // 固定額割引
)

type Coupon struct {
    id            CouponID
    code          string
    couponType    CouponType
    value         *big.Rat         // 割合 or 固定額
    currency      shared.Currency  // 固定額の場合の通貨
    minAmount     *shared.Money    // 最低購入額
    maxDiscount   *shared.Money    // 最大割引額
    validFrom     time.Time
    validUntil    time.Time
    usageLimit    *int             // 使用回数制限
    usedCount     int
    applicableTo  []string         // 適用可能なプランID（空なら全て）
}

func (c *Coupon) Code() string {
    return c.code
}

func (c *Coupon) IsValid(at time.Time) bool {
    if at.Before(c.validFrom) || at.After(c.validUntil) {
        return false
    }
    if c.usageLimit != nil && c.usedCount >= *c.usageLimit {
        return false
    }
    return true
}

func (c *Coupon) CalculateDiscount(subtotal shared.Money) shared.Money {
    var discount shared.Money
    
    switch c.couponType {
    case CouponTypePercentage:
        discount = subtotal.Multiply(c.value)
    case CouponTypeFixed:
        discount = shared.NewMoney(c.value, c.currency)
    }
    
    // 最大割引額の制限
    if c.maxDiscount != nil {
        maxAmt := c.maxDiscount.Amount()
        if discount.Amount().Cmp(maxAmt) > 0 {
            discount = *c.maxDiscount
        }
    }
    
    // 小計を超えない
    if discount.Amount().Cmp(subtotal.Amount()) > 0 {
        discount = subtotal
    }
    
    return discount
}

// CouponRepository クーポンリポジトリ
type CouponRepository interface {
    FindByCode(ctx context.Context, code string) (*Coupon, error)
    FindApplicable(ctx context.Context, contractID string, at time.Time) ([]*Coupon, error)
    Save(ctx context.Context, coupon *Coupon) error
    RecordUsage(ctx context.Context, couponID CouponID, contractID string) error
}
```

## 7. 税計算プラグイン実装例

```go
// plugins/tax/plugin.go
package tax

import (
    "context"
    
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

func (p *TaxPlugin) Initialize(ctx context.Context, config plugin.Config) error {
    return nil
}

func (p *TaxPlugin) Shutdown(ctx context.Context) error {
    return nil
}

// CalculateTax TaxHookの実装
// DiscountHookやInvoiceLifecycleHookの空実装は不要
func (p *TaxPlugin) CalculateTax(ctx *plugin.CalculationContext) (shared.Money, error) {
    contract := ctx.Contract()
    afterDiscount := ctx.SubtotalAfterDiscount()

    // 請求先情報から税率を決定
    taxRate := p.calculator.GetTaxRate(ctx.Context(), contract.BillingAddress())

    tax := afterDiscount.Multiply(taxRate)

    return tax, nil
}

// TaxCalculator 税率計算インターフェース
type TaxCalculator interface {
    GetTaxRate(ctx context.Context, address shared.Address) *big.Rat
}

// JapaneseTaxCalculator 日本の消費税計算
type JapaneseTaxCalculator struct{}

func (c *JapaneseTaxCalculator) GetTaxRate(ctx context.Context, address shared.Address) *big.Rat {
    // 日本国内は10%
    if address.Country == "JP" {
        return big.NewRat(10, 100)
    }
    // 国外は0%
    return big.NewRat(0, 1)
}
```

## 8. アプリケーション層での統合

### 8.1 請求サービスでのプラグイン利用

```go
// application/service/billing_service.go
package service

import (
    "context"

    "github.com/contract-to-cash/core/domain/balance"
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/invoice"
    "github.com/contract-to-cash/core/domain/pricing"
    "github.com/contract-to-cash/core/domain/product"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/plugin"
)

// BillingConfig 請求処理設定
type BillingConfig struct {
    // GracePeriod 請求書確定（finalize）までの猶予期間
    // この間に遅延UsageRecordの吸収やInvoiceLifecycleHookでの調整が可能
    // デフォルト: 1時間
    GracePeriod time.Duration

    // DaysUntilDue 請求書送付から支払い期限までの日数
    // CollectionMethod が "send_invoice" の場合に使用
    // デフォルト: 30日
    DaysUntilDue int

    // CollectionMethod 回収方法
    //   "charge_automatically": 確定後に自動課金
    //   "send_invoice": 請求書を送付し支払いを待つ
    // デフォルト: "charge_automatically"
    CollectionMethod string
}

type BillingService struct {
    contractRepo contract.Repository
    invoiceRepo  invoice.Repository
    usageRepo    usage.Repository
    balanceRepo   balance.Repository   // nil許容: クレジット機能未使用の場合（WithBalanceRepoオプションで設定）
    balanceConfig balance.BalanceConfig
    priceRepo    pricing.PriceRepository
    productRepo  product.Repository
    registry     *plugin.Registry
    config       BillingConfig
    clock        shared.Clock
}

// BillingServiceOption NewBillingServiceのオプション引数
type BillingServiceOption func(*BillingService)

// WithBalanceRepo balanceRepoを設定するオプション
func WithBalanceRepo(repo balance.Repository) BillingServiceOption {
    return func(s *BillingService) { s.balanceRepo = repo }
}

func NewBillingService(
    contractRepo contract.Repository,
    invoiceRepo invoice.Repository,
    usageRepo usage.Repository,
    balanceConfig balance.BalanceConfig,
    priceRepo pricing.PriceRepository,
    productRepo product.Repository,
    registry *plugin.Registry,
    config BillingConfig,
    clock shared.Clock,
    opts ...BillingServiceOption,
) *BillingService {
    s := &BillingService{
        contractRepo:  contractRepo,
        invoiceRepo:   invoiceRepo,
        usageRepo:     usageRepo,
        balanceConfig: balanceConfig,
        priceRepo:     priceRepo,
        productRepo:   productRepo,
        registry:      registry,
        config:        config,
        clock:         clock,
    }
    for _, opt := range opts {
        opt(s)
    }
    return s
}

// GenerateInvoice 請求書を生成する（draft状態）
//
// 契約タイプに応じた計算フロー:
//   subscription  → 固定料金のみ
//   usage_based   → 従量料金のみ（UsageRecord集計 + PricingModel適用）
//   hybrid        → 固定料金 + 従量料金
//
// 生成された請求書はdraft状態。GracePeriod経過後に FinalizeInvoice() で確定する。
func (s *BillingService) GenerateInvoice(
    ctx context.Context,
    contractID string,
    billingPeriod shared.DateRange,
) (*invoice.Invoice, error) {
    // 契約取得
    c, err := s.contractRepo.FindByID(ctx, shared.ContractID(contractID))
    if err != nil {
        return nil, err
    }

    // 2. 料金計算（契約タイプに応じて分岐）
    subtotal, err := s.calculateSubtotal(ctx, c, billingPeriod)
    if err != nil {
        return nil, fmt.Errorf("subtotal calculation failed: %w", err)
    }

    calcCtx := plugin.NewCalculationContext(ctx, c, subtotal)

    // 1. BeforeCalculation（InvoiceLifecycleHook）
    for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
        if err := hook.BeforeCalculation(calcCtx); err != nil {
            return nil, fmt.Errorf("before calculation hook failed: %w", err)
        }
    }

    // 3. 割引計算（DiscountHook のみ）
    totalDiscount := shared.Zero(subtotal.Currency())
    for _, hook := range s.registry.GetDiscountHooks() {
        discount, err := hook.CalculateDiscount(calcCtx)
        if err != nil {
            return nil, fmt.Errorf("discount calculation failed: %w", err)
        }
        totalDiscount, err = totalDiscount.Add(discount)
        if err != nil {
            return nil, fmt.Errorf("discount accumulation failed: %w", err)
        }
    }

    // 割引上限ガード: 割引合計がsubtotalを超えないようにする
    // ゼロ金額請求書は正常ケース（全額割引等）として扱う
    if totalDiscount.GreaterThan(subtotal) {
        totalDiscount = subtotal
    }

    // 4. 小計算出（割引後）
    afterDiscount, err := subtotal.Subtract(totalDiscount)
    if err != nil {
        return nil, fmt.Errorf("discount subtraction failed: %w", err)
    }
    calcCtx.SetSubtotalAfterDiscount(afterDiscount)

    // 5. 税計算（TaxHook のみ、割引後の金額に対して）
    totalTax := shared.Zero(subtotal.Currency())
    for _, hook := range s.registry.GetTaxHooks() {
        tax, err := hook.CalculateTax(calcCtx)
        if err != nil {
            return nil, fmt.Errorf("tax calculation failed: %w", err)
        }
        totalTax, err = totalTax.Add(tax)
        if err != nil {
            return nil, fmt.Errorf("tax accumulation failed: %w", err)
        }
    }

    // 合計算出（税込）
    total, err := afterDiscount.Add(totalTax)
    if err != nil {
        return nil, fmt.Errorf("total calculation failed: %w", err)
    }

    // 6. クレジット台帳からの充当（domain-model.md セクション9.5参照）
    appliedBalance := shared.Zero(total.Currency())
    if s.balanceRepo != nil {
        credits, err := s.balanceRepo.FindAvailable(ctx, c.AccountID(), total.Currency())
        if err != nil {
            return nil, fmt.Errorf("credit lookup failed: %w", err)
        }
        for _, entry := range credits {
            if entry.IsExpired(s.clock.Now()) {
                continue
            }
            remaining, err := total.Subtract(appliedBalance) // まだ充当が必要な額
            if err != nil {
                return nil, fmt.Errorf("credit remaining calculation failed: %w", err)
            }
            if remaining.IsZero() {
                break
            }
            apply, err := entry.RemainingAmount().Min(remaining)
            if err != nil {
                return nil, fmt.Errorf("credit min calculation failed: %w", err)
            }
            appliedBalance, err = appliedBalance.Add(apply)
            if err != nil {
                return nil, fmt.Errorf("credit accumulation failed: %w", err)
            }
            // BalanceApplication 作成 + BalanceEntry.remainingAmount 減算
            // （同一DBトランザクション内でアトミックに実行 — domain-model.md 9.5参照）
        }
    }
    amountDue, err := total.Subtract(appliedBalance)
    if err != nil {
        return nil, fmt.Errorf("amount due calculation failed: %w", err)
    }

    // 7. 請求書作成（draft状態）
    inv := invoice.NewInvoice(
        invoice.NewInvoiceID(),
        c.AccountID(),
        c.ID(),
        subtotal,
        totalDiscount,
        totalTax,
        invoice.WithStatus(invoice.InvoiceStatusDraft),
        invoice.WithBillingPeriod(billingPeriod),
        invoice.WithDueDate(s.calculateDueDate(billingPeriod)),
        invoice.WithAppliedBalance(appliedBalance),
        invoice.WithAmountDue(amountDue),
    )
    calcCtx.SetInvoice(inv)

    // 8. AfterCalculation（InvoiceLifecycleHook）
    for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
        if err := hook.AfterCalculation(calcCtx, inv); err != nil {
            return nil, fmt.Errorf("after calculation hook failed: %w", err)
        }
    }

    // 保存（draft状態で保存。GracePeriod後にFinalizeInvoiceで確定）
    if err := s.invoiceRepo.Save(ctx, inv); err != nil {
        return nil, err
    }

    return inv, nil
}

// calculateSubtotal 契約タイプに応じた基本料金を算出する
func (s *BillingService) calculateSubtotal(
    ctx context.Context,
    c *contract.Contract,
    period shared.DateRange,
) (shared.Money, error) {
    switch c.ContractType() {

    case contract.ContractTypeSubscription:
        // サブスクリプション: 固定料金
        return c.Price(), nil

    case contract.ContractTypeUsageBased:
        // 従量課金: UsageRecord集計 → PricingModel適用
        return s.calculateUsageCharge(ctx, c, period)

    case contract.ContractTypeOneTime:
        // 買い切り: 固定料金（1回のみ）
        return c.Price(), nil

    default:
        return shared.Money{}, fmt.Errorf("unknown contract type: %s", c.ContractType())
    }
}

// calculateUsageCharge 従量料金を算出する
//
// フロー:
//   1. 請求期間のUsageRecordを集計（UsageSummary取得）
//   2. 含有枠（Included Allowance）があれば差し引き
//   3. PricingModel（Graduated/Volume）で料金算出
//
// メータリング（生イベントの収集・集計）はOSSスコープ外。
// 利用者がアプリケーション側で集計し、UsageRecordとして記録する。
func (s *BillingService) calculateUsageCharge(
    ctx context.Context,
    c *contract.Contract,
    period shared.DateRange,
) (shared.Money, error) {
    plan := c.Plan()
    totalCharge := shared.Zero(c.Price().Currency())

    for _, metric := range plan.UsageMetrics() {
        // 1. 請求期間の使用量を集計
        summary, err := s.usageRepo.GetSummary(ctx, c.ID(), metric.Name, period)
        if err != nil {
            return shared.Money{}, fmt.Errorf("usage summary failed for %s: %w", metric.Name, err)
        }

        // 2. 含有枠（Included Allowance）の差し引き
        billableUsage := summary.TotalUsage
        if metric.IncludedQuantity > 0 {
            billableUsage -= metric.IncludedQuantity
            if billableUsage < 0 {
                billableUsage = 0
            }
        }

        // 3. PricingModelで料金算出
        charge := metric.PricingModel.CalculatePrice(billableUsage)
        totalCharge, err = totalCharge.Add(charge)
        if err != nil {
            return shared.Money{}, fmt.Errorf("charge accumulation failed for %s: %w", metric.Name, err)
        }
    }

    // 基本料金（ハイブリッド課金の固定部分）がある場合は加算
    if basePrice := c.BasePrice(); !basePrice.IsZero() {
        var err error
        totalCharge, err = totalCharge.Add(basePrice)
        if err != nil {
            return shared.Money{}, fmt.Errorf("base price addition failed: %w", err)
        }
    }

    return totalCharge, nil
}

// FinalizeInvoice 請求書を確定する
// GracePeriod経過後に呼び出す。確定後は変更不可。
func (s *BillingService) FinalizeInvoice(ctx context.Context, invoiceID string) error {
    inv, err := s.invoiceRepo.FindByID(ctx, shared.InvoiceID(invoiceID))
    if err != nil {
        return err
    }
    if inv.Status() != invoice.InvoiceStatusDraft {
        return fmt.Errorf("invoice %s is not in draft status", invoiceID)
    }

    inv.Finalize()
    return s.invoiceRepo.Save(ctx, inv)
}

// ProcessPriceChange 価格変更時のクレジット処理
// PlanChangeProration.AdjustmentAmount < 0 の場合、BalancePolicy に従い分岐
func (s *BillingService) ProcessPriceChange(ctx context.Context, contractID shared.ContractID, newPriceID shared.PriceID) error {
    // 1. 日割り計算
    proration, err := s.calculator.CalculateProration(ctx, contractID, newPriceID)
    if err != nil {
        return fmt.Errorf("proration calculation failed: %w", err)
    }

    c, err := s.contractRepo.FindByID(ctx, contractID)
    if err != nil {
        return err
    }
    accountID := c.AccountID()

    // 2. AdjustmentAmount の符号で分岐
    if proration.AdjustmentAmount.IsNegative() {
        // ダウングレード: BalancePolicy に従う
        switch s.balanceConfig.DowngradePolicy {
        case balance.BalancePolicyLedger:
            // クレジット台帳に積む
            entry := balance.NewBalanceEntry(accountID, proration.AdjustmentAmount.Negate(), balance.BalanceReasonProration)
            if err := s.balanceRepo.Save(ctx, entry); err != nil {
                return fmt.Errorf("credit entry save failed: %w", err)
            }
        case balance.BalancePolicyRefund:
            // 即時返金
            if err := s.gateway.Refund(ctx, &port.RefundRequest{Amount: proration.AdjustmentAmount.Negate()}); err != nil {
                return fmt.Errorf("refund failed: %w", err)
            }
        case balance.BalancePolicyNone:
            // 何もしない
        }
    } else if !proration.AdjustmentAmount.IsZero() {
        // アップグレード: 差額のみ請求
        // 請求書を生成して決済
    }

    return nil
}

func (s *BillingService) calculateDueDate(period shared.DateRange) time.Time {
    daysUntilDue := s.config.DaysUntilDue
    if daysUntilDue == 0 {
        daysUntilDue = 30
    }
    return period.End().AddDate(0, 0, daysUntilDue)
}
```

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
    c := &contract.Contract{/* ... */}
    ctx := plugin.NewCalculationContext(context.Background(), c)
    ctx.SetSubtotal(shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY))

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

本ライブラリは初版（v1.0.0）から分割フック設計を採用している。

```
DiscountHook          — 割引計算のみ
TaxHook               — 税計算のみ
InvoiceLifecycleHook  — 計算前後処理
ContractLifecycleHook — 契約ライフサイクル
PaymentHook           — 支払い処理
MetricsHook           — メトリクス収集
InvoiceGenerationHook — 請求書生成
```

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
