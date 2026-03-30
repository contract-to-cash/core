# 基本請求デモ

**推奨される決済確認付きプロビジョニングフロー**を示すエンドツーエンドの例です。契約を作成し、ドラフト請求書を生成し、有効化、決済待ちで一時停止、決済処理、再開の一連のフローを実行します。

## 実行結果

```
1. Contract created (Draft)     -> ¥3,000/月 サブスクリプション
2. Draft invoice generated      -> ¥3,000 + 消費税10% = ¥3,300
3. User confirmed               -> 契約: Active, 請求書: Finalized
4. Contract suspended            -> 初回決済待ち
5. Payment processed             -> ¥3,300 決済完了
6. Contract resumed              -> サービス開始！
7. Final state                   -> 請求書: 支払済, 残高: ¥0
```

## 決済確認付きプロビジョニングフロー

このデモは、決済完了後にのみサービスが開始される推奨フローに従います:

```
Draft ──→ Invoice(draft) ──→ Activate + Finalize ──→ Suspend(決済待ち)
                                                          │
                                                      決済成功
                                                          │
                                                    Resume ──→ Active（サービス開始）
```

`Suspended`状態は、以下の両方を統一的に「サービス非稼働」として扱います:
- **初回有効化**: 新規契約が初回決済を待っている状態
- **未払い停止**: 既存契約が延滞している状態（Dunning）

どちらも同じ方法で解決します: 決済完了 → Resume → サービス（再）開始。

> **注**: これは推奨フローですが、唯一の方法ではありません。決済確認前のサービス開始が許容される場合は、よりシンプルな `Draft -> Activate -> Invoice -> Pay` フローも利用できます。設計議論の詳細は [Issue #5](https://github.com/contract-to-cash/core/issues/5) を参照してください。

## 主要コンセプト

| コンセプト | 説明 |
|-----------|------|
| **BillingService** | 請求書生成フローを統括（ステータスガードでDraft契約を許可） |
| **TaxPlugin** | 割引後小計に消費税10%を計算 |
| **PaymentService** | ゲートウェイ経由で決済し、請求書ステータスを更新 |
| **Suspend/Resume** | 決済確認によるサービス開始のゲーティング |
| **EventStore** | 全契約変更がイベントとして永続化（監査証跡） |

## 実行

```bash
go run ./examples/billing-demo/
```
