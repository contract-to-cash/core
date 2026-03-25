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

### 2.2 フック用コンテキスト

```go
// plugin/context.go
package plugin

import (
    "context"
    
    "github.com/yourorg/contract-billing-core/domain/contract"
    "github.com/yourorg/contract-billing-core/domain/invoice"
)

// Context プラグイン実行コンテキスト
type Context struct {
    ctx       context.Context
    contract  *contract.Contract
    invoice   *invoice.Invoice
    metadata  map[string]interface{}
}

func NewContext(ctx context.Context) *Context {
    return &Context{
        ctx:      ctx,
        metadata: make(map[string]interface{}),
    }
}

func (c *Context) Context() context.Context {
    return c.ctx
}

func (c *Context) Contract() *contract.Contract {
    return c.contract
}

func (c *Context) SetContract(contract *contract.Contract) {
    c.contract = contract
}

func (c *Context) Invoice() *invoice.Invoice {
    return c.invoice
}

func (c *Context) SetInvoice(invoice *invoice.Invoice) {
    c.invoice = invoice
}

func (c *Context) SetMetadata(key string, value interface{}) {
    c.metadata[key] = value
}

func (c *Context) GetMetadata(key string) (interface{}, bool) {
    v, ok := c.metadata[key]
    return v, ok
}
```

## 3. フック定義

### 3.1 請求書計算フック

```go
// plugin/hooks.go
package plugin

import (
    "github.com/yourorg/contract-billing-core/domain/invoice"
    "github.com/yourorg/contract-billing-core/domain/shared"
)

// InvoiceCalculationHook 請求書計算フック
type InvoiceCalculationHook interface {
    Plugin
    
    // BeforeCalculation 計算前処理
    BeforeCalculation(ctx *Context) error
    
    // CalculateDiscount 割引計算（クーポン等）
    CalculateDiscount(ctx *Context, subtotal shared.Money) (shared.Money, error)
    
    // CalculateTax 税計算
    CalculateTax(ctx *Context, subtotal shared.Money) (shared.Money, error)
    
    // AfterCalculation 計算後処理
    AfterCalculation(ctx *Context, invoice *invoice.Invoice) error
}
```

### 3.2 契約ライフサイクルフック

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

### 3.3 支払いフック

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

### 3.4 メトリクスフック

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

### 3.5 請求書生成フック

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
    
    // フック別のプラグインリスト
    invoiceCalculationHooks  []InvoiceCalculationHook
    contractLifecycleHooks   []ContractLifecycleHook
    paymentHooks             []PaymentHook
    metricsHooks             []MetricsHook
    invoiceGenerationHooks   []InvoiceGenerationHook
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
    
    // フック別に分類
    if h, ok := plugin.(InvoiceCalculationHook); ok {
        r.invoiceCalculationHooks = append(r.invoiceCalculationHooks, h)
        r.sortByPriority(r.invoiceCalculationHooks)
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

// GetInvoiceCalculationHooks 請求書計算フックを取得
func (r *Registry) GetInvoiceCalculationHooks() []InvoiceCalculationHook {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.invoiceCalculationHooks
}

// 他のフックも同様にゲッター提供
```

## 5. プラグイン実行順序

### 5.1 会計基準に則った順序

請求書計算において、プラグインは以下の順序で実行される：

```
1. 基本料金計算
2. 数量調整（従量課金）
3. 割引適用（クーポン等）  ← DiscountPluginが実行
4. 小計算出
5. 税計算（割引後に対して）  ← TaxPluginが実行
6. 合計算出
```

### 5.2 優先度による制御

```go
// 優先度定数
const (
    PriorityHighest = 0
    PriorityHigh    = 100
    PriorityNormal  = 500
    PriorityLow     = 900
    PriorityLowest  = 1000
)

// 割引プラグインは税計算より先に実行
type CouponPlugin struct{}
func (p *CouponPlugin) Priority() int { return PriorityNormal }

type TaxPlugin struct{}
func (p *TaxPlugin) Priority() int { return PriorityLow }
```

## 6. クーポンプラグイン実装例

### 6.1 プラグイン実装

```go
// plugins/coupon/plugin.go
package coupon

import (
    "context"
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/shared"
    "github.com/yourorg/contract-billing-core/plugin"
)

// CouponPlugin クーポンプラグイン
type CouponPlugin struct {
    repo     CouponRepository
    config   CouponConfig
    priority int
}

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

func (p *CouponPlugin) BeforeCalculation(ctx *plugin.Context) error {
    return nil
}

func (p *CouponPlugin) CalculateDiscount(ctx *plugin.Context, subtotal shared.Money) (shared.Money, error) {
    contract := ctx.Contract()
    if contract == nil {
        return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
    }
    
    // 契約に適用されているクーポンを取得
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
        
        // 使用記録
        ctx.SetMetadata(fmt.Sprintf("applied_coupon_%d", i), coupon.Code())
    }
    
    return totalDiscount, nil
}

func (p *CouponPlugin) CalculateTax(ctx *plugin.Context, subtotal shared.Money) (shared.Money, error) {
    // クーポンプラグインは税計算を行わない
    return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
}

func (p *CouponPlugin) AfterCalculation(ctx *plugin.Context, invoice *invoice.Invoice) error {
    return nil
}
```

### 6.2 クーポンドメイン

```go
// plugins/coupon/domain.go
package coupon

import (
    "time"
    
    "github.com/yourorg/contract-billing-core/domain/shared"
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
    
    "github.com/yourorg/contract-billing-core/domain/shared"
    "github.com/yourorg/contract-billing-core/plugin"
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

func (p *TaxPlugin) Name() string    { return "tax" }
func (p *TaxPlugin) Version() string { return "1.0.0" }
func (p *TaxPlugin) Priority() int   { return p.priority }

func (p *TaxPlugin) Initialize(ctx context.Context, config plugin.Config) error {
    return nil
}

func (p *TaxPlugin) Shutdown(ctx context.Context) error {
    return nil
}

func (p *TaxPlugin) BeforeCalculation(ctx *plugin.Context) error {
    return nil
}

func (p *TaxPlugin) CalculateDiscount(ctx *plugin.Context, subtotal shared.Money) (shared.Money, error) {
    // 税プラグインは割引を行わない
    return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
}

func (p *TaxPlugin) CalculateTax(ctx *plugin.Context, subtotal shared.Money) (shared.Money, error) {
    contract := ctx.Contract()
    if contract == nil {
        return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
    }
    
    // 請求先情報から税率を決定
    taxRate := p.calculator.GetTaxRate(ctx.Context(), contract.BillingAddress())
    
    tax := subtotal.Multiply(taxRate)
    
    ctx.SetMetadata("tax_rate", taxRate.FloatString(4))
    
    return tax, nil
}

func (p *TaxPlugin) AfterCalculation(ctx *plugin.Context, invoice *invoice.Invoice) error {
    return nil
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
    
    "github.com/yourorg/contract-billing-core/domain/contract"
    "github.com/yourorg/contract-billing-core/domain/invoice"
    "github.com/yourorg/contract-billing-core/domain/shared"
    "github.com/yourorg/contract-billing-core/plugin"
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
    contract, err := s.contractRepo.FindByID(ctx, contract.ContractID(contractID))
    if err != nil {
        return nil, err
    }
    
    // プラグインコンテキスト作成
    pluginCtx := plugin.NewContext(ctx)
    pluginCtx.SetContract(contract)
    
    // BeforeCalculation フック実行
    for _, hook := range s.registry.GetInvoiceCalculationHooks() {
        if err := hook.BeforeCalculation(pluginCtx); err != nil {
            return nil, fmt.Errorf("before calculation hook failed: %w", err)
        }
    }
    
    // 基本料金計算
    subtotal := s.calculateBasePrice(contract)
    
    // 割引計算（プラグイン順）
    var totalDiscount shared.Money
    for _, hook := range s.registry.GetInvoiceCalculationHooks() {
        discount, err := hook.CalculateDiscount(pluginCtx, subtotal)
        if err != nil {
            return nil, fmt.Errorf("discount calculation failed: %w", err)
        }
        totalDiscount, _ = totalDiscount.Add(discount)
    }
    
    // 小計（割引後）
    afterDiscount, _ := subtotal.Subtract(totalDiscount)
    
    // 税計算（割引後に対して）
    var totalTax shared.Money
    for _, hook := range s.registry.GetInvoiceCalculationHooks() {
        tax, err := hook.CalculateTax(pluginCtx, afterDiscount)
        if err != nil {
            return nil, fmt.Errorf("tax calculation failed: %w", err)
        }
        totalTax, _ = totalTax.Add(tax)
    }
    
    // 請求書作成
    inv := invoice.NewInvoice(
        invoice.NewInvoiceID(),
        contract.AccountID(),
        contract.ID(),
        subtotal,
        totalDiscount,
        totalTax,
    )
    
    pluginCtx.SetInvoice(inv)
    
    // AfterCalculation フック実行
    for _, hook := range s.registry.GetInvoiceCalculationHooks() {
        if err := hook.AfterCalculation(pluginCtx, inv); err != nil {
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
    
    "github.com/yourorg/contract-billing-core/domain/shared"
    "github.com/yourorg/contract-billing-core/plugin"
)

func TestCouponPlugin_CalculateDiscount(t *testing.T) {
    // モックリポジトリ
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
    
    ctx := plugin.NewContext(context.Background())
    ctx.SetContract(&contract.Contract{/* ... */})
    
    subtotal := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)
    
    discount, err := p.CalculateDiscount(ctx, subtotal)
    
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    expected := big.NewRat(1000, 1) // 10000 * 10% = 1000
    if discount.Amount().Cmp(expected) != 0 {
        t.Errorf("expected %v, got %v", expected, discount.Amount())
    }
}
```

## 8. API互換性とバージョニング戦略

### 8.1 Semantic Versioning

本ライブラリはSemantic Versioning 2.0.0に従う。

| バージョン変更 | 条件 | 例 |
|---------------|------|-----|
| **Major (v2.0.0)** | プラグインインターフェースの破壊的変更 | フック分離（InvoiceCalculationHook → DiscountHook + TaxHook） |
| **Minor (v1.x.0)** | 新規フックの追加、既存フックへのメソッド追加（デフォルト実装あり） | MetricsHookに新メソッド追加 |
| **Patch (v1.x.y)** | バグ修正、ドキュメント修正 | Registry のスレッドセーフ修正 |

### 8.2 破壊的変更の定義

以下をプラグインAPIの破壊的変更と定義する：

1. **インターフェースのメソッド追加**（デフォルト実装なし）
2. **インターフェースのメソッドシグネチャ変更**（引数型・戻り値型の変更）
3. **インターフェースの削除・統合・分離**
4. **Context型のフィールド削除・型変更**
5. **Registry APIの変更**（Enable/Disable/Get系メソッド）

### 8.3 計画されている破壊的変更（v2.0.0）

改善計画（改善2: プラグインフック分離）により、以下の破壊的変更を v2.0.0 で実施する：

```
v1.x（現行）                        v2.0.0（改善後）
─────────────────────────           ─────────────────────────
InvoiceCalculationHook              DiscountHook
  ├─ BeforeCalculation()            TaxHook
  ├─ CalculateDiscount()            InvoiceLifecycleHook
  ├─ CalculateTax()                   ├─ BeforeCalculation()
  └─ AfterCalculation()               └─ AfterCalculation()
```

### 8.4 v1.x LTS方針

| 項目 | 方針 |
|------|------|
| サポート期間 | v2.0.0 リリース後 12ヶ月間 |
| セキュリティ修正 | サポート期間中は適用 |
| バグ修正 | Criticalのみ適用 |
| 新機能 | 追加しない |

### 8.5 移行ガイド

v1.x から v2.0.0 への移行パターン：

```go
// ============================================================
// v1.x: InvoiceCalculationHook を実装するプラグイン
// ============================================================

type MyPlugin struct{}

func (p *MyPlugin) CalculateDiscount(ctx *Context, subtotal Money) (Money, error) {
    // 割引ロジック
}

func (p *MyPlugin) CalculateTax(ctx *Context, subtotal Money) (Money, error) {
    // このプラグインは税計算に関心がないが、実装が必要だった
    return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
}

// ============================================================
// v2.0.0: DiscountHook のみ実装すればよい
// ============================================================

type MyPlugin struct{}

func (p *MyPlugin) CalculateDiscount(ctx *CalculationContext) (Money, error) {
    // 割引ロジック（CalculateTax の空実装は不要）
}
```

**移行手順:**

1. `go get github.com/contract-to-cash/core/v2` でv2モジュールをインポート
2. プラグインが実装しているフックを特定（割引のみ？税のみ？両方？）
3. 各フックに対応する新インターフェースを実装
4. `Registry.Enable()` の呼び出しはそのまま使える（Registry が自動的にフック種別を判定）
5. `CalculationContext`（旧 `Context`）の型安全なAPIに移行

### 8.6 非推奨通知のプロセス

```
v1.x.0  InvoiceCalculationHook に @deprecated 注釈を追加
        コンパイル時に deprecation warning を出力
        ↓
v1.x+1  移行ガイドへのリンクをGoDocに記載
        ↓
v2.0.0  InvoiceCalculationHook を削除
        DiscountHook / TaxHook / InvoiceLifecycleHook に分離
```
