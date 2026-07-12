---
sidebar_position: 3
---

# プラグインシステム

プラグインシステムにより、明確に定義されたフックを通じて課金ロジックを拡張できます。各フックタイプは特定の目的を持ち、必要なインターフェースだけを実装します。

## Pluginインターフェース

すべてのプラグインは基本インターフェースを実装します：

```go
type Plugin interface {
    Name() string
    Version() string
    Initialize(ctx context.Context, config Config) error
    Shutdown(ctx context.Context) error
    Priority() int  // 小さい数値 = 高い優先度
}
```

### 優先度定数

```go
const (
    PriorityHighest  = 0
    PriorityHigh     = 100
    PriorityNormal   = 500
    PriorityLow      = 900
    PriorityLowest   = 1000
)
```

プラグインは優先度順に実行されます。例えば、優先度0の監査ログは優先度500の割引計算より先に実行されます。

## フックカテゴリ

### 課金計算フック

請求書生成パイプラインに参加するフック：

**DiscountHook** — 割引計算（クーポン、ロイヤリティ、ボリューム）：

```go
type DiscountHook interface {
    Plugin
    CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}
```

**TaxHook** — 割引後金額に対する税計算：

```go
type TaxHook interface {
    Plugin
    CalculateTax(ctx *CalculationContext) (shared.Money, error)
}
```

**InvoiceLifecycleHook** — 請求書計算の前後：

```go
type InvoiceLifecycleHook interface {
    Plugin
    BeforeCalculation(ctx *CalculationContext) error
    AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
```

### 契約ライフサイクルフック

契約の状態変更に反応：

```go
type OnContractCreateHook interface {
    Plugin
    OnContractCreate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractActivateHook interface {
    Plugin
    OnContractActivate(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractSuspendHook, OnContractResumeHook, OnContractCancelHook,
// OnContractCancelScheduledHook, OnContractCancelUnscheduledHook,
// OnContractRenewHook, OnContractTrialEndHook も同様
```

### 決済フック

決済処理フローへのフック：

```go
type BeforeChargeHook interface {
    Plugin
    BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

type AfterChargeHook interface {
    Plugin
    AfterCharge(ctx *PaymentContext) error
}

type OnPaymentFailedHook interface {
    Plugin
    OnPaymentFailed(ctx *PaymentContext, err error) error
}

type OnRefundHook interface {
    Plugin
    OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}
```

### メトリクスフック

KPIやビジネスメトリクスの収集：

```go
type OnContractChangeHook interface {
    Plugin
    OnContractChange(ctx *Context, event ContractChangeEvent) error
}

type OnInvoiceIssuedHook interface {
    Plugin
    OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

type OnPaymentProcessedHook interface {
    Plugin
    OnPaymentProcessed(ctx *PaymentContext) error
}
```

### 請求書生成フック

カスタム請求書レンダリングと配信：

```go
type InvoiceGenerationHook interface {
    Plugin
    BuildDocument(ctx *Context, invoice *invoice.Invoice, doc *InvoiceDocument) error
    AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error
    AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error
}
```

## 計算コンテキスト

課金フックは`CalculationContext`で以下にアクセスできます：

```go
ctx.Contract()              // 契約集約
ctx.Subtotal()              // 現在の小計
ctx.SubtotalAfterDiscount() // 全割引後の小計（税計算用）
ctx.AppliedDiscounts()      // 適用された割引のリスト
ctx.Invoice()               // 生成中の請求書
ctx.ContractID()            // 契約IDへのショートカット
```

## プラグインレジストリ

プラグインの登録、初期化、管理：

```go
registry := plugin.NewRegistry()

// プラグイン登録（インターフェースにより自動分類）
registry.Register(myDiscountPlugin)
registry.Register(myTaxPlugin)
registry.Register(myAuditPlugin)

// 設定で全初期化
configs := map[string]plugin.Config{
    "my-discount": {"percentage": 10},
    "my-tax":      {"priority": plugin.PriorityLow},
}
registry.InitializeAll(ctx, configs)

// 型別フック取得（優先度順）
discountHooks := registry.GetDiscountHooks()
taxHooks := registry.GetTaxHooks()
```

## 公式プラグイン

### 税プラグイン

プラグインの`TaxCalculator`インターフェースで税計算：

```go
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{}) // 10%
```

他の税制度向けにカスタム`TaxCalculator`を実装可能。

### クーポンプラグイン

クーポンベースの割引を管理：
- パーセンテージおよび固定金額割引
- 使用回数制限（グローバルおよびアカウント別）
- 最小購入金額 / 最大割引キャップ
- `applicableTo`によるプラン別制限
- スタッキング制御

```go
couponPlugin := coupon.NewCouponPlugin(couponRepo, clock)
```

### InvoiceCleanupプラグイン

古い請求書データのクリーンアップを管理：

```go
cleanupPlugin := invoicecleanup.NewInvoiceCleanupPlugin(invoiceRepo)
```
