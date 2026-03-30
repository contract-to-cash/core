---
sidebar_position: 3
---

# 課金と価格設定

このガイドでは、課金モデル、請求書生成、計算パイプラインについて説明します。

## 価格モデル

価格モデルは契約ではなくPriceエンティティで定義されます。同一プロダクトに複数の価格を設定可能（月額/年額、異なるティア）。

### 定額制

課金期間ごとのシンプルな固定金額：

```go
price := pricing.NewPrice(productID, moneyJPY(3000), shared.CurrencyJPY,
    pricing.BillingCycleMonthly, nil)
// 使用量に関係なく月額¥3,000
```

### 段階制（Tiered）

使用量の各段階に異なる料金（段階ごとに個別計算）：

```go
model := pricing.NewTieredPrice([]pricing.Tier{
    {UpTo: 100, UnitPrice: moneyJPY(10)},    // 最初の100: ¥10/個
    {UpTo: 500, UnitPrice: moneyJPY(8)},     // 101-500: ¥8/個
    {UpTo: 0, UnitPrice: moneyJPY(5)},       // 501+: ¥5/個（無制限）
})
// 250個 = (100 × ¥10) + (150 × ¥8) = ¥2,200
```

### ボリューム制

合計数量に基づく単一料金（全数量が該当するティアの価格）：

```go
model := pricing.NewVolumePrice([]pricing.Tier{
    {UpTo: 100, UnitPrice: moneyJPY(10)},    // 1-100個: ¥10/個
    {UpTo: 500, UnitPrice: moneyJPY(8)},     // 101-500個: ¥8/個
    {UpTo: 0, UnitPrice: moneyJPY(5)},       // 501+個: ¥5/個
})
// 250個 = 250 × ¥8 = ¥2,000（全て¥8ティア）
```

### 従量課金

メータリングされた使用量に基づく課金（無料枠付き）：

```go
product := product.NewProduct("APIサービス",
    product.WithUsageMetrics([]product.UsageMetric{
        {Name: "api_calls", IncludedQuantity: 1000},
    }),
)
price := pricing.NewPrice(product.ID(), moneyJPY(5000), shared.CurrencyJPY,
    pricing.BillingCycleMonthly, pricingModel)
// 基本料金¥5,000 + 1,000回を超えるAPIコールの従量課金
```

使用量メトリクスはProductで定義、価格ルールはPriceで定義。契約別の価格オーバーライドでカスタム交渉価格に対応。

## 請求書生成

`BillingService.GenerateInvoice()`は14ステップのパイプラインを実行します。完全なステップの内訳は[アーキテクチャ](./architecture#請求書生成パイプライン)を参照してください。

### 例: 税付き請求フロー

```go
// 税プラグイン（消費税10%）を登録
registry := plugin.NewRegistry()
taxPlugin := tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{})
registry.Register(taxPlugin)
registry.InitializeAll(ctx, map[string]plugin.Config{
    "tax": {"priority": plugin.PriorityLow},
})

// ¥3,000/月の契約に対して請求書を生成
inv, _ := billingService.GenerateInvoice(ctx, contractID, agg.CurrentPeriod())
// 小計:      ¥3,000
// 消費税(10%): ¥300
// 合計:      ¥3,300
```

### プラグインパイプラインの例

複数プラグインが登録されている場合、優先度ベースで課金計算パイプラインに合成されます：

| 優先度 | プラグイン | タイプ | アクション |
|--------|----------|--------|---------|
| 0 | audit-log | InvoiceLifecycleHook | 計算フローのログ |
| 500 | coupon | DiscountHook | 10%クーポン割引 |
| 500 | loyalty-discount | DiscountHook | 5%ロイヤリティ割引 |
| 900 | tax | TaxHook | 消費税10% |

¥10,000/月の契約に対して：

```
1. [AuditLog] BeforeCalculation（開始ログ）
2. 小計:              ¥10,000
3. [Coupon] -10%:     -¥1,000
4. [Loyalty] -5%:     -¥500
5. 割引後:            ¥8,500
6. [Tax] +10%:        +¥850
7. 合計:              ¥9,350
8. [AuditLog] AfterCalculation（結果ログ）
```

主なポイント：
- プラグインは優先度順に実行（小さい数値 = 高い優先度）
- 複数のDiscountHookがスタック — それぞれ元の小計に適用
- 税は割引後金額に対して計算
- InvoiceLifecycleHookでパイプラインの前後を可視化

## サンプルの実行

```bash
# 基本的な課金フロー
go run ./examples/billing-demo/

# 価格モデル（定額、段階、ボリューム、従量）
go run ./examples/pricing-models-demo/

# 複数プラグインのパイプライン
go run ./examples/plugin-pipeline-demo/
```
