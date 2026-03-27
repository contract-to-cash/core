---
sidebar_position: 1
---

# 基本課金フロー

この例では、契約作成から決済処理までの完全なフローを実演します：
1. サブスクリプション契約の作成
2. 税プラグインの登録
3. 請求書の生成
4. 決済処理
5. イベント履歴の確認

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

### 契約作成

¥3,000/月のサブスクリプションを作成・有効化：

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
agg.Activate(metadata)
```

### 請求書生成

`BillingService`が税を自動適用して請求書を生成：

```
小計:        ¥3,000
消費税(10%): ¥300
合計:        ¥3,300
```

### 決済処理

モックゲートウェイで決済を処理し、請求書ステータスが`paid`に更新。

### イベント履歴

イベントストアにすべての操作が記録：

```
[1] contract.created (v1) at 2026-04-01
[2] contract.activated (v2) at 2026-04-01
```

## ポイント

- 課金パイプラインが登録済みプラグイン（税、割引）を自動適用
- すべての操作が不変イベントとして記録
- 請求書が小計、割引、税、クレジットの内訳を追跡
- 決済処理がゲートウェイインターフェースにより課金から分離
