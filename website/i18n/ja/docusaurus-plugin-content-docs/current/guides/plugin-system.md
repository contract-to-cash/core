---
sidebar_position: 5
---

# プラグインシステム

プラグインシステムにより、明確に定義されたフックを通じて課金ロジックを拡張できます。各フックタイプは特定の目的を持ち、必要なインターフェースだけを実装します。完全なフックインターフェースの定義については[プラグインフックAPIリファレンス](../api/plugin-hooks)を参照してください。

## 概要

すべてのプラグインは基本`Plugin`インターフェース（Name、Version、Initialize、Shutdown、Priority）に加え、1つ以上のフックインターフェースを実装します。プラグインは優先度順に実行されます（小さい数値 = 高い優先度）。

| 優先度定数 | 値 | ユースケース |
|----------|------|----------|
| `PriorityHighest` | 0 | 監査ログ、バリデーション |
| `PriorityHigh` | 100 | 前処理、コアビジネスロジック |
| `PriorityNormal` | 500 | デフォルトプラグイン |
| `PriorityLow` | 900 | 後処理（税） |
| `PriorityLowest` | 1000 | 最終手段、クリーンアップ |

## フックカテゴリ

- **課金計算** — `DiscountHook`、`TaxHook`、`InvoiceLifecycleHook` — 請求書生成パイプラインに参加
- **契約ライフサイクル** — `OnContractCreateHook`、`OnContractActivateHook`、`OnContractSuspendHook`、`OnContractResumeHook`、`OnContractCancelHook`、`OnContractRenewHook`、`OnContractTrialEndHook` — 契約の状態変更に反応
- **決済** — `BeforeChargeHook`、`AfterChargeHook`、`OnPaymentFailedHook`、`OnRefundHook` — 決済処理フローへのフック
- **メトリクス** — `OnContractChangeHook`、`OnInvoiceIssuedHook`、`OnPaymentProcessedHook` — KPIやビジネスメトリクスの収集
- **請求書生成** — `InvoiceGenerationHook` — カスタム請求書レンダリングと配信
- **クレジットノート** — `OnCreditNoteIssuedHook`、`OnInvoiceRevisedHook` — クレジットノートと請求書リビジョンイベントに反応

## カスタムプラグインの構築

### 基本構造

```go
package myplugin

import (
    "context"
    "github.com/contract-to-cash/core/plugin"
)

type MyPlugin struct {
    priority int
}

func (p *MyPlugin) Name() string    { return "my-plugin" }
func (p *MyPlugin) Version() string { return "1.0.0" }
func (p *MyPlugin) Priority() int   { return p.priority }

func (p *MyPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if v, ok := config["priority"]; ok {
        if n, ok := v.(int); ok {
            p.priority = n
        }
    }
    return nil
}

func (p *MyPlugin) Shutdown(_ context.Context) error { return nil }
```

### 例: ロイヤリティ割引プラグイン

全請求書に5%割引を適用するプラグイン：

```go
type LoyaltyDiscountPlugin struct {
    priority int
    rate     *big.Rat
}

func NewLoyaltyDiscountPlugin(discountPercent int) *LoyaltyDiscountPlugin {
    return &LoyaltyDiscountPlugin{
        rate: new(big.Rat).SetFrac64(int64(discountPercent), 100),
    }
}

func (p *LoyaltyDiscountPlugin) Name() string    { return "loyalty-discount" }
func (p *LoyaltyDiscountPlugin) Version() string { return "1.0.0" }
func (p *LoyaltyDiscountPlugin) Priority() int   { return p.priority }

func (p *LoyaltyDiscountPlugin) Initialize(_ context.Context, config plugin.Config) error {
    if v, ok := config["priority"]; ok {
        if n, ok := v.(int); ok {
            p.priority = n
        }
    }
    return nil
}

func (p *LoyaltyDiscountPlugin) Shutdown(_ context.Context) error { return nil }

// DiscountHookを実装
func (p *LoyaltyDiscountPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    discount := ctx.Subtotal().Multiply(p.rate)
    ctx.RecordDiscount(plugin.AppliedDiscount{
        PluginName: "loyalty-discount",
        Code:       "LOYALTY",
        Amount:     discount,
    })
    return discount, nil
}
```

### 例: 監査ログプラグイン

請求書計算ライフサイクルをログするプラグイン：

```go
type AuditLogPlugin struct {
    priority int
    logger   *slog.Logger
}

// InvoiceLifecycleHookを実装
func (p *AuditLogPlugin) BeforeCalculation(ctx *plugin.CalculationContext) error {
    p.logger.Info("請求書計算開始",
        "contract_id", ctx.ContractID(),
        "subtotal", ctx.Subtotal().Amount().RatString(),
    )
    return nil
}

func (p *AuditLogPlugin) AfterCalculation(ctx *plugin.CalculationContext, inv *invoice.Invoice) error {
    p.logger.Info("請求書計算完了",
        "contract_id", ctx.ContractID(),
        "total", inv.Total().Amount().RatString(),
        "discounts_applied", len(ctx.AppliedDiscounts()),
    )
    return nil
}
```

### 例: ホスティングプロビジョニングプラグイン

契約ライフサイクルイベントでホスティングリソースをプロビジョニング/デプロビジョニング：

```go
type HostingPlugin struct {
    priority           int
    provisioningClient ProvisioningClient
}

// OnContractActivateHookを実装
func (p *HostingPlugin) OnContractActivate(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.CreateServer(ctx.Context(), c.ContractID(), c.PlanID())
}

// OnContractSuspendHookを実装
func (p *HostingPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.StopServer(ctx.Context(), c.ContractID())
}

// OnContractResumeHookを実装
func (p *HostingPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.StartServer(ctx.Context(), c.ContractID())
}

// OnContractCancelHookを実装
func (p *HostingPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningClient.DeleteServer(ctx.Context(), c.ContractID())
}
```

:::caution
`OnContractResume`フックは初回プロビジョニング（初回決済）と再有効化（停止後の決済）を区別できません。プロビジョニング状態を外部で追跡し、`OnContractResume`内で確認することを検討してください:

```go
func (p *HostingPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    if p.provisioningClient.Exists(ctx.Context(), c.ContractID()) {
        return p.provisioningClient.StartServer(ctx.Context(), c.ContractID())
    }
    return p.provisioningClient.CreateServer(ctx.Context(), c.ContractID(), c.PlanID())
}
```

詳細は[Issue #5](https://github.com/contract-to-cash/core/issues/5)を参照。
:::

### マルチフックプラグイン

1つのプラグインで複数のフックインターフェースを実装できます：

```go
type MetricsPlugin struct {
    priority int
    metrics  MetricsCollector
}

// OnContractChangeHook + OnInvoiceIssuedHook + OnPaymentProcessedHookを実装
func (p *MetricsPlugin) OnContractChange(ctx *plugin.Context, event plugin.ContractChangeEvent) error {
    p.metrics.RecordContractChange(event.ChangeType, event.MRRChange)
    return nil
}

func (p *MetricsPlugin) OnInvoiceIssued(ctx *plugin.Context, inv *invoice.Invoice) error {
    p.metrics.RecordRevenue(inv.Total())
    return nil
}

func (p *MetricsPlugin) OnPaymentProcessed(ctx *plugin.Context, pay *payment.Payment) error {
    p.metrics.RecordPayment(pay.Status(), pay.Amount())
    return nil
}
```

## プラグインの登録

```go
registry := plugin.NewRegistry()

// レジストリはインターフェースによりプラグインを自動分類
registry.Register(&LoyaltyDiscountPlugin{})   // → DiscountHook
registry.Register(&AuditLogPlugin{})          // → InvoiceLifecycleHook
registry.Register(&HostingPlugin{})           // → 複数の契約フック

configs := map[string]plugin.Config{
    "loyalty-discount": {"priority": plugin.PriorityNormal},
    "audit-log":        {"priority": plugin.PriorityHighest},
    "hosting":          {"priority": plugin.PriorityHigh},
}
registry.InitializeAll(ctx, configs)
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

:::note
`applicableTo`によるプラン別制限は現在`PlanID`ベースで照合しています。Product/Price分離の一環として、将来のリリースで`ProductID`ベースの照合に移行予定です。
:::

```go
couponPlugin := coupon.NewCouponPlugin(couponRepo, clock)
```

### InvoiceCleanupプラグイン

古い請求書データのクリーンアップを管理：

```go
cleanupPlugin := invoicecleanup.NewInvoiceCleanupPlugin(invoiceRepo, clock)
```

## プラグイン実行順序

請求書生成時のプラグイン実行順序：

1. **InvoiceLifecycleHook.BeforeCalculation**（優先度順）
2. 小計計算
3. **DiscountHook.CalculateDiscount**（優先度順、複数）
4. 割引キャップ
5. **TaxHook.CalculateTax**（優先度順）
6. 請求書作成
7. **InvoiceLifecycleHook.AfterCalculation**（優先度順）

```bash
go run ./examples/plugin-pipeline-demo/
```
