---
sidebar_position: 3
---

# プラグインパイプラインデモ

この例では、複数プラグインが優先度ベースで課金計算パイプラインにどう合成されるかを示します。

## サンプルの実行

```bash
go run ./examples/plugin-pipeline-demo/
```

## プラグイン構成

4つのプラグインを異なる優先度で登録：

| 優先度 | プラグイン | タイプ | アクション |
|--------|----------|--------|---------|
| 0 | audit-log | InvoiceLifecycleHook | 計算フローのログ |
| 500 | coupon | DiscountHook | 10%クーポン割引 |
| 500 | loyalty-discount | DiscountHook | 5%ロイヤリティ割引 |
| 900 | tax | TaxHook | 消費税10% |

## 計算パイプライン

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

## ポイント

- プラグインは優先度順に実行（小さい数値 = 高い優先度）
- 複数のDiscountHookがスタック — それぞれ元の小計に適用
- 税は割引後金額に対して計算
- InvoiceLifecycleHookでパイプラインの前後を可視化
