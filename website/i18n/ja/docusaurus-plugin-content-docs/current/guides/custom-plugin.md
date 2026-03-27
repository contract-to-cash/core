---
sidebar_position: 2
---

# カスタムプラグインガイド

課金動作を拡張するカスタムプラグインの構築方法を学びます。

## 基本構造

すべてのプラグインは`Plugin`インターフェースに加え、1つ以上のフックインターフェースを実装します：

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

## 例: ロイヤリティ割引プラグイン

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

## 例: 監査ログプラグイン

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

## 例: ホスティングプロビジョニングプラグイン

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

## カスタムプラグインの登録

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

## マルチフックプラグイン

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

## プラグイン実行順序

請求書生成時のプラグイン実行順序：

1. **InvoiceLifecycleHook.BeforeCalculation**（優先度順）
2. 小計計算
3. **DiscountHook.CalculateDiscount**（優先度順、複数）
4. 割引キャップ
5. **TaxHook.CalculateTax**（優先度順）
6. 請求書作成
7. **InvoiceLifecycleHook.AfterCalculation**（優先度順）
