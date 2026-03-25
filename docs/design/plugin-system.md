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
    contract              *contract.Contract
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

func NewCalculationContext(ctx context.Context, c *contract.Contract) *CalculationContext {
    return &CalculationContext{
        ctx:      ctx,
        contract: c,
    }
}

func (c *CalculationContext) Context() context.Context        { return c.ctx }
func (c *CalculationContext) Contract() *contract.Contract    { return c.contract }
func (c *CalculationContext) Subtotal() shared.Money          { return c.subtotal }
func (c *CalculationContext) SubtotalAfterDiscount() shared.Money { return c.subtotalAfterDiscount }
func (c *CalculationContext) AppliedDiscounts() []AppliedDiscount { return c.appliedDiscounts }

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

### 3.5 契約ライフサイクルフック

```go
// plugin/hooks.go (続き)

// ContractLifecycleHook 契約ライフサイクルフック
type ContractLifecycleHook interface {
    Plugin
    
    // OnCreate 契約作成時
    OnCreate(ctx *Context, contract *contract.Contract) error
    
    // OnActivate 契約有効化時
    OnActivate(ctx *Context, contract *contract.Contract) error
    
    // OnSuspend 契約一時停止時
    OnSuspend(ctx *Context, contract *contract.Contract) error
    
    // OnResume 契約再開時
    OnResume(ctx *Context, contract *contract.Contract) error
    
    // OnCancel 契約解約時
    OnCancel(ctx *Context, contract *contract.Contract) error
    
    // OnRenew 契約更新時
    OnRenew(ctx *Context, contract *contract.Contract) error
    
    // OnTrialEnd トライアル終了時
    OnTrialEnd(ctx *Context, contract *contract.Contract, converted bool) error
}
```

### 3.6 支払いフック

```go
// plugin/hooks.go (続き)

// PaymentHook 支払いフック
type PaymentHook interface {
    Plugin
    
    // BeforeCharge 課金前処理
    BeforeCharge(ctx *Context, amount shared.Money) error
    
    // AfterCharge 課金後処理
    AfterCharge(ctx *Context, payment *payment.Payment) error
    
    // OnPaymentFailed 支払い失敗時
    OnPaymentFailed(ctx *Context, payment *payment.Payment, err error) error
    
    // OnRefund 返金時
    OnRefund(ctx *Context, payment *payment.Payment, amount shared.Money) error
}
```

### 3.7 メトリクスフック

```go
// plugin/hooks.go (続き)

// MetricsHook メトリクス収集フック
type MetricsHook interface {
    Plugin
    
    // OnContractChange 契約変更時
    OnContractChange(ctx *Context, event ContractChangeEvent) error
    
    // OnInvoiceIssued 請求書発行時
    OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
    
    // OnPaymentProcessed 支払い処理時
    OnPaymentProcessed(ctx *Context, payment *payment.Payment) error
}

type ContractChangeEvent struct {
    ContractID  string
    ChangeType  string // "created", "activated", "cancelled", etc.
    OldValue    interface{}
    NewValue    interface{}
    Timestamp   time.Time
}
```

### 3.8 請求書生成フック

```go
// plugin/hooks.go (続き)

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

type InvoiceDocument struct {
    InvoiceID     string
    InvoiceNumber string
    IssuerInfo    CompanyInfo
    CustomerInfo  CustomerInfo
    LineItems     []DocumentLineItem
    Subtotal      shared.Money
    TaxAmount     shared.Money
    Total         shared.Money
    Notes         string
    
    // 電子帳簿保存法対応フィールド
    QualifiedInvoiceNumber string // 適格請求書番号
    TaxRegistrationNumber  string // 登録番号
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

    // フック別のプラグインリスト（分離後）
    discountHooks          []DiscountHook
    taxHooks               []TaxHook
    invoiceLifecycleHooks  []InvoiceLifecycleHook
    contractLifecycleHooks []ContractLifecycleHook
    paymentHooks           []PaymentHook
    metricsHooks           []MetricsHook
    invoiceGenerationHooks []InvoiceGenerationHook
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
    if h, ok := plugin.(DiscountHook); ok {
        r.discountHooks = append(r.discountHooks, h)
        r.sortByPriority(r.discountHooks)
    }
    if h, ok := plugin.(TaxHook); ok {
        r.taxHooks = append(r.taxHooks, h)
        r.sortByPriority(r.taxHooks)
    }
    if h, ok := plugin.(InvoiceLifecycleHook); ok {
        r.invoiceLifecycleHooks = append(r.invoiceLifecycleHooks, h)
        r.sortByPriority(r.invoiceLifecycleHooks)
    }
    if h, ok := plugin.(ContractLifecycleHook); ok {
        r.contractLifecycleHooks = append(r.contractLifecycleHooks, h)
        r.sortByPriority(r.contractLifecycleHooks)
    }
    if h, ok := plugin.(PaymentHook); ok {
        r.paymentHooks = append(r.paymentHooks, h)
        r.sortByPriority(r.paymentHooks)
    }
    if h, ok := plugin.(MetricsHook); ok {
        r.metricsHooks = append(r.metricsHooks, h)
        r.sortByPriority(r.metricsHooks)
    }
    if h, ok := plugin.(InvoiceGenerationHook); ok {
        r.invoiceGenerationHooks = append(r.invoiceGenerationHooks, h)
        r.sortByPriority(r.invoiceGenerationHooks)
    }
    
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

// ContractLifecycleHook, PaymentHook, MetricsHook, InvoiceGenerationHook も同様
```

## 5. プラグイン実行順序

### 5.1 コアが保証する計算順序

請求書計算の順序はコアが構造的に保証する。
Priority値に依存しないため、プラグイン登録順のミスで会計基準違反が発生しない。

```
1. InvoiceLifecycleHook.BeforeCalculation()  ← 計算前処理
2. 基本料金計算（コア）
3. DiscountHook.CalculateDiscount()          ← 割引計算（全DiscountHook）
4. 小計算出（コア: subtotal - totalDiscount）
5. TaxHook.CalculateTax()                    ← 税計算（割引後に対して）
6. 合計算出（コア: afterDiscount + totalTax）
7. InvoiceLifecycleHook.AfterCalculation()   ← 計算後処理
```

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
}

// インターフェース準拠の確認（コンパイル時チェック）
var _ plugin.DiscountHook = (*CouponPlugin)(nil)

type CouponConfig struct {
    MaxCouponsPerInvoice int
    AllowStacking        bool // 複数クーポン併用可否
}

func NewCouponPlugin(repo CouponRepository) *CouponPlugin {
    return &CouponPlugin{
        repo:     repo,
        priority: plugin.PriorityNormal,
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

    coupons, err := p.repo.FindApplicable(ctx.Context(), contract.ID(), time.Now())
    if err != nil {
        return shared.Money{}, err
    }

    if len(coupons) == 0 {
        return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
    }

    var totalDiscount shared.Money
    for i, coupon := range coupons {
        if !p.config.AllowStacking && i > 0 {
            break
        }
        if p.config.MaxCouponsPerInvoice > 0 && i >= p.config.MaxCouponsPerInvoice {
            break
        }

        discount := coupon.CalculateDiscount(subtotal)
        totalDiscount, _ = totalDiscount.Add(discount)

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
    
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/invoice"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/plugin"
)

type BillingService struct {
    contractRepo contract.Repository
    invoiceRepo  invoice.Repository
    registry     *plugin.Registry
}

func NewBillingService(
    contractRepo contract.Repository,
    invoiceRepo invoice.Repository,
    registry *plugin.Registry,
) *BillingService {
    return &BillingService{
        contractRepo: contractRepo,
        invoiceRepo:  invoiceRepo,
        registry:     registry,
    }
}

func (s *BillingService) GenerateInvoice(ctx context.Context, contractID string) (*invoice.Invoice, error) {
    // 契約取得
    c, err := s.contractRepo.FindByID(ctx, shared.ContractID(contractID))
    if err != nil {
        return nil, err
    }

    calcCtx := plugin.NewCalculationContext(ctx, c)

    // 1. BeforeCalculation（InvoiceLifecycleHook）
    for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
        if err := hook.BeforeCalculation(calcCtx); err != nil {
            return nil, fmt.Errorf("before calculation hook failed: %w", err)
        }
    }

    // 2. 基本料金計算
    subtotal := s.calculateBasePrice(c)
    calcCtx.SetSubtotal(subtotal)

    // 3. 割引計算（DiscountHook のみ）
    //    コアがこのステップで DiscountHook だけを呼ぶため、
    //    TaxHook が割引より先に実行されることは構造的にありえない
    var totalDiscount shared.Money
    for _, hook := range s.registry.GetDiscountHooks() {
        discount, err := hook.CalculateDiscount(calcCtx)
        if err != nil {
            return nil, fmt.Errorf("discount calculation failed: %w", err)
        }
        totalDiscount, _ = totalDiscount.Add(discount)
    }

    // 4. 小計算出（割引後）
    afterDiscount, _ := subtotal.Subtract(totalDiscount)
    calcCtx.SetSubtotalAfterDiscount(afterDiscount)

    // 5. 税計算（TaxHook のみ、割引後の金額に対して）
    //    コアが割引後の金額で TaxHook を呼ぶため、
    //    会計基準の順序が構造的に保証される
    var totalTax shared.Money
    for _, hook := range s.registry.GetTaxHooks() {
        tax, err := hook.CalculateTax(calcCtx)
        if err != nil {
            return nil, fmt.Errorf("tax calculation failed: %w", err)
        }
        totalTax, _ = totalTax.Add(tax)
    }

    // 6. 請求書作成
    inv := invoice.NewInvoice(
        invoice.NewInvoiceID(),
        c.AccountID(),
        c.ID(),
        subtotal,
        totalDiscount,
        totalTax,
    )
    calcCtx.SetInvoice(inv)

    // 7. AfterCalculation（InvoiceLifecycleHook）
    for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
        if err := hook.AfterCalculation(calcCtx, inv); err != nil {
            return nil, fmt.Errorf("after calculation hook failed: %w", err)
        }
    }

    // 保存
    if err := s.invoiceRepo.Save(ctx, inv); err != nil {
        return nil, err
    }

    return inv, nil
}

func (s *BillingService) calculateBasePrice(contract *contract.Contract) shared.Money {
    return contract.Price()
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
    mockRepo := &MockCouponRepository{
        coupons: []*Coupon{
            {
                id:         "coupon-1",
                code:       "SAVE10",
                couponType: CouponTypePercentage,
                value:      big.NewRat(10, 100), // 10%
                validFrom:  time.Now().Add(-24 * time.Hour),
                validUntil: time.Now().Add(24 * time.Hour),
            },
        },
    }

    p := NewCouponPlugin(mockRepo)
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
