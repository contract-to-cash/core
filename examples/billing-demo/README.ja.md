# 基本請求デモ

最もシンプルなエンドツーエンドの例です。サブスクリプション契約を作成し、税込み請求書を生成し、決済を処理します。

## 実行結果

```
1. Contract created   -> Draft (¥3,000/month)
2. Contract activated  -> Active
3. Invoice generated   -> ¥3,000 + Tax 10% = ¥3,300
4. Invoice finalized   -> Ready for payment
5. Payment processed   -> ¥3,300 completed
6. Invoice updated     -> Paid, Balance ¥0
```

## 主要コンセプト

| コンセプト | 説明 |
|-----------|------|
| **BillingService** | 14ステップの請求書生成フローを統括 |
| **TaxPlugin** | 割引後小計に消費税10%を計算 |
| **PaymentService** | ゲートウェイ経由で決済し、請求書ステータスを更新 |
| **EventStore** | 全契約変更がイベントとして永続化（監査証跡） |

## 実行

```bash
go run ./examples/billing-demo/
```
