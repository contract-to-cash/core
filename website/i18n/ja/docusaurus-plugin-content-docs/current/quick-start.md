---
sidebar_position: 2
---

# クイックスタート

Contract Billing Coreを数分でセットアップしましょう。

## インストール

```bash
go get github.com/contract-to-cash/core
```

Go 1.25以上が必要です。

## 基本的な課金フロー

この例では、契約作成から決済処理までの完全なフローを実演します。

### 1. インフラストラクチャのセットアップ

```go
package main

import (
    "context"
    "math/big"
    "time"

    "github.com/contract-to-cash/core/application/service"
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/balance"
    "github.com/contract-to-cash/core/domain/pricing"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
    "github.com/contract-to-cash/core/infrastructure/inmemory"
    "github.com/contract-to-cash/core/plugin"
    "github.com/contract-to-cash/core/plugins/tax"
)

func main() {
    ctx := context.Background()
    clock := shared.FixedClock{FixedTime: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}

    // インフラ（本番環境では独自の実装に置き換え）
    eventStore := inmemory.NewInMemoryEventStore(clock)
    contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)
    invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
    balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
    usageRepo := inmemory.NewInMemoryUsageRepository()
    priceRepo := inmemory.NewInMemoryPriceRepository()
    productRepo := inmemory.NewInMemoryProductRepository()
```

### 2. プラグインの登録

```go
    // 税プラグイン（消費税10%）を登録
    registry := plugin.NewRegistry()
    taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
    registry.Register(taxPlugin)
    registry.InitializeAll(ctx, map[string]plugin.Config{
        "tax": {"priority": plugin.PriorityLow},
    })
    defer registry.ShutdownAll(ctx)
```

### 3. 契約の作成

```go
    // Priceエンティティを作成
    price := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
    priceEntity := pricing.NewPrice(
        shared.NewProductID(), price, shared.CurrencyJPY,
        pricing.BillingCycleMonthly, nil,
    )
    priceRepo.Save(ctx, priceEntity)

    // サブスクリプション契約を作成・有効化
    contractID := shared.NewContractID()
    agg := contract.NewContractAggregate(contractID, clock)
    agg.Create(contract.CreateContractCommand{
        AccountID:    shared.AccountID("acct-001"),
        PlanID:       shared.PlanID("plan-standard"),
        PriceID:      priceEntity.ID(),
        ContractType: contract.ContractTypeSubscription,
        BillingCycle: contract.BillingCycleMonthly,
        Price:        price,
        BasePrice:    price,
    }, eventstore.EventMetadata{UserID: "system"})
    agg.Activate(eventstore.EventMetadata{UserID: "system"})
    contractRepo.Save(ctx, agg)
```

### 4. 請求書の生成

```go
    billingService := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo,
        balance.BalanceConfig{},
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30},
        clock,
        service.WithBalanceRepo(balanceRepo),
    )

    inv, _ := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
    // inv.Subtotal()       => ¥3,000
    // inv.TaxAmount()      => ¥300 (消費税10%)
    // inv.Total()          => ¥3,300
```

### 5. 決済処理

```go
    paymentService := service.NewPaymentService(
        myGateway, paymentRepo, invoiceRepo, contractRepo,
        eventStore, registry, clock,
    )

    inv.Finalize()
    invoiceRepo.Save(ctx, inv)

    payment, _ := paymentService.ProcessPayment(ctx, inv.ID(),
        service.ProcessPaymentInput{
            PaymentMethodID: "pm-visa-1234",
            Amount:          inv.AmountDue(),
            Currency:        shared.CurrencyJPY,
            IdempotencyKey:  "pay-001",
        },
    )
    // payment.Status() => "completed"
}
```

## サンプルの実行

リポジトリには7つの実行可能なサンプルが含まれています：

```bash
# 基本的な課金フロー
go run ./examples/billing-demo/

# イベントソーシング時間旅行
go run ./examples/event-sourcing-demo/

# 複数プラグインのパイプライン
go run ./examples/plugin-pipeline-demo/

# 契約ライフサイクル（トライアル、停止、解約）
go run ./examples/lifecycle-demo/

# フック経由の外部サービスプロビジョニング
go run ./examples/hosting-integration-demo/

# マルチサービスルーティング
go run ./examples/multi-service-demo/

# 価格モデル（定額、段階、ボリューム、従量）
go run ./examples/pricing-models-demo/
```

すべてのサンプルはインメモリ実装を使用し、外部依存はありません。

## 次のステップ

- [アーキテクチャ](./guides/architecture) — システム設計を理解する
- [システム統合](./guides/system-integration) — あなたのサービスへの組み込み方法
- [プラグインシステム](./guides/plugin-system) — 独自プラグインの構築
