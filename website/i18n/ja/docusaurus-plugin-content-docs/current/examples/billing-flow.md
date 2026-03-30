---
sidebar_position: 1
---

# 基本請求フロー

最もシンプルな契約から決済までのパターンを示す例です:
1. サブスクリプション契約の作成（Draft）
2. 税プラグインの登録
3. 契約の有効化
4. 請求書の生成と確定
5. 決済処理
6. イベント履歴の確認

## 実行

```bash
go run ./examples/billing-demo/
```

## 処理の内容

### セットアップ

インメモリインフラを作成し、10%消費税プラグインを登録します:

```go
registry := plugin.NewRegistry()
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
registry.Register(taxPlugin)
registry.InitializeAll(ctx, map[string]plugin.Config{
    "tax": {"priority": plugin.PriorityLow},
})
```

### 契約作成（Draft）

¥3,000/月のサブスクリプションをDraftステータスで作成します:

```go
agg := contract.NewContractAggregate(contractID, clock)
agg.Create(contract.CreateContractCommand{
    AccountID:    shared.AccountID("acct-demo-001"),
    PlanID:       shared.PlanID("plan-standard"),
    PriceID:      priceEntity.ID(),
    ContractType: contract.ContractTypeSubscription,
    BillingCycle: contract.BillingCycleMonthly,
    Price:        moneyJPY(3000),
    BasePrice:    moneyJPY(3000),
}, metadata)
contractRepo.Save(ctx, agg)
```

### 有効化

契約をDraftからActiveに遷移させます:

```go
agg.Activate(metadata)  // Contract: Draft → Active
contractRepo.Save(ctx, agg)
```

### 請求書生成

`BillingService`が登録済みプラグインにより自動的に税を適用した請求書を生成します:

```
小計:      ¥3,000
消費税(10%): ¥300
合計:      ¥3,300
```

### 決済処理

モックゲートウェイ経由で決済が処理され、請求書がPaidに遷移します:

```go
paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{
    PaymentMethodID: "pm-visa-1234",
    Amount:          inv.AmountDue(),
    Currency:        shared.CurrencyJPY,
    IdempotencyKey:  "demo-pay-001",
})
// Invoice: Finalized → Paid
```

### イベント履歴

イベントストアが全ての契約操作を記録します:

```
[1] contract.created   (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
```

## 主なポイント

- 請求パイプラインは登録済みプラグイン（税、割引）を自動的に適用
- 全操作がイミュータブルなイベントとして記録される
- 請求書は小計、割引、税、クレジットの内訳を追跡
- 決済処理はゲートウェイインターフェースにより請求と分離

## 決済確認付きプロビジョニング

決済完了後にのみサービスを開始するユースケース（ホスティング等）では、Suspend/Resumeステップを追加します:

```
Draft → Invoice(draft) → Activate + Finalize → Suspend(決済待ち) → Pay → Resume → Active
```

`Suspended`状態は「初回決済待ち」と「未払い停止」の統一的な「サービス非稼働」状態として機能します。詳細は[アーキテクチャ: 決済確認付きプロビジョニング](../architecture.md)と[Issue #5](https://github.com/contract-to-cash/core/issues/5)を参照してください。
