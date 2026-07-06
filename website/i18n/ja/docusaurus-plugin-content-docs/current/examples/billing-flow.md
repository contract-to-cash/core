---
sidebar_position: 1
---

# 基本請求フロー

**推奨される決済確認付きプロビジョニング**フローを示す例です。決済確認後にのみサービスが開始されるパターンです:
1. サブスクリプション契約の作成（Draft）
2. 税プラグインの登録
3. ドラフト請求書の生成
4. 契約の有効化と請求書の確定
5. 契約の一時停止（決済待ち）
6. 決済処理と契約の再開
7. イベント履歴の確認

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
    PriceID:      priceEntity.ID(),
    ContractType: contract.ContractTypeSubscription,
    Interval:     pricing.Monthly(),
    Price:        moneyJPY(3000),
    BasePrice:    moneyJPY(3000),
}, metadata)
contractRepo.Save(ctx, agg)
```

### 請求書生成（Draft）

`BillingService`がDraft契約に対して税適用済みのドラフト請求書を生成します:

```
小計:      ¥3,000
消費税(10%): ¥300
合計:      ¥3,300
```

### 有効化と確定

ユーザー確認後、契約を有効化し請求書を確定します:

```go
agg.Activate(metadata)  // Contract: Draft → Active
inv.Finalize()           // Invoice: Draft → Finalized
```

### 一時停止（決済待ち）

決済確認までサービスアクセスを防ぐため、即座に契約を一時停止します:

```go
agg.Suspend(contract.SuspensionConfiguration{
    BillingBehavior: contract.SuspensionBillingSkip,
    Reason:          "awaiting_initial_payment",
}, metadata)  // Contract: Active → Suspended
```

### 決済処理と再開

モックゲートウェイ経由で決済を処理し、確認後に契約を再開してサービスを開始します:

```go
paymentService.ProcessPayment(ctx, inv.ID(), service.ProcessPaymentInput{...})
// Invoice: Finalized → Paid

agg.Resume(metadata)   // Contract: Suspended → Active（サービス開始）
```

### イベント履歴

イベントストアが全操作を記録します:

```
[1] contract.created   (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
[3] contract.suspended (v3) at 2026-04-01
[4] contract.resumed   (v4) at 2026-04-01
```

## 主なポイント

- **決済確認付きプロビジョニング**により、決済確認後にのみサービスが開始される
- `Suspended`ステータスは初回決済待ちと未払い停止の両方に統一的に使用
- 請求パイプラインは登録済みプラグイン（税、割引）を自動的に適用
- 全操作がイミュータブルなイベントとして記録される
- 請求書は小計、割引、税、クレジットの内訳を追跡
- 決済処理はゲートウェイインターフェースにより請求と分離

> **注:** これは推奨フローです。決済ゲーティングが不要な場合は、よりシンプルな `Draft → Activate → Invoice生成 → Pay` フローも利用できます。
