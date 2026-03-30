---
sidebar_position: 1
---

# 基本課金フロー

この例では、**Payment-Gated Provisioning**フロー（決済確認後にサービスを開始する推奨パターン）を実演します：
1. サブスクリプション契約の作成（Draft）
2. 税プラグインの登録
3. ドラフト請求書の生成
4. 契約と請求書の確定（Activate / Finalize）
5. 契約のSuspend（決済待ち）
6. 決済処理とResume（サービス開始）
7. イベント履歴の確認

## サンプルの実行

```bash
go run ./examples/billing-demo/
```

## 処理内容

### セットアップ

インメモリインフラを作成し、消費税10%プラグインを登録：

```go
registry := plugin.NewRegistry()
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
registry.Register(taxPlugin)
```

### 契約作成（Draft）

¥3,000/月のサブスクリプションをDraft状態で作成：

```go
agg.Create(contract.CreateContractCommand{
    AccountID:    shared.AccountID("acct-demo-001"),
    PlanID:       shared.PlanID("plan-standard"),
    PriceID:      priceEntity.ID(),
    ContractType: contract.ContractTypeSubscription,
    BillingCycle: contract.BillingCycleMonthly,
    Price:        moneyJPY(3000),
    BasePrice:    moneyJPY(3000),
}, metadata)
```

### 請求書生成（Draft）

契約がDraft状態のまま、`BillingService`が税を自動適用してドラフト請求書を生成：

```
小計:        ¥3,000
消費税(10%): ¥300
合計:        ¥3,300
```

### 確定（Activate / Finalize）

ユーザー確認後、契約をActivateし請求書をFinalize：

```go
agg.Activate(metadata)  // Contract: Draft → Active
// Invoice: Draft → Finalized
```

### Suspend（決済待ち）

決済完了までサービスアクセスを防ぐため、契約を即座にSuspend：

```go
agg.Suspend(metadata)  // Contract: Active → Suspended
```

### 決済処理とResume

モックゲートウェイで決済を処理。確認後、契約をResumeしてサービス開始：

```go
// Invoice: Finalized → Paid
agg.Resume(metadata)   // Contract: Suspended → Active（サービス開始）
```

### イベント履歴

イベントストアにすべての操作が記録：

```
[1] contract.created (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
[3] contract.suspended (v3) at 2026-04-01
[4] contract.resumed (v4) at 2026-04-01
```

## ポイント

- **Payment-Gated Provisioning**により、決済確認後にのみサービスが開始される
- `Suspended`ステータスが「初回決済待ち」と「未払い停止」の統一的な「サービス非稼働」状態として機能
- 課金パイプラインが登録済みプラグイン（税、割引）を自動適用
- すべての操作が不変イベントとして記録
- 請求書が小計、割引、税、クレジットの内訳を追跡
- 決済処理がゲートウェイインターフェースにより課金から分離

> **注意:** これは推奨フローです。決済ゲーティングが不要な場合は、`Draft → Activate → 請求書生成 → 決済処理`のシンプルなフローも利用可能です。
