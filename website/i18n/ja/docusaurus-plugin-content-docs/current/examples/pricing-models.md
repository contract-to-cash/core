---
sidebar_position: 5
---

# 価格モデルデモ

この例では、ライブラリがサポートする各種価格モデルを実演します。

## サンプルの実行

```bash
go run ./examples/pricing-models-demo/
```

## 対応モデル

### 定額制

課金期間ごとのシンプルな固定金額：

```go
price := pricing.NewPrice(productID, moneyJPY(3000), shared.CurrencyJPY,
    pricing.BillingCycleMonthly, nil, createdAt)
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
    pricing.BillingCycleMonthly, pricingModel, createdAt)
// 基本料金¥5,000 + 1,000回を超えるAPIコールの従量課金
```

## ポイント

- 価格モデルは契約ではなくPriceエンティティで定義
- 同一プロダクトに複数の価格を設定可能（月額/年額、異なるティア）
- 契約別の価格オーバーライドでカスタム交渉価格に対応
- 使用量メトリクスはProductで定義、価格ルールはPriceで定義
