# Contract Billing Core 設計レビュー v4

**レビュー日**: 2026-03-26
**対象ブランチ**: `docs/design-review-improvements`
**前回レビュー**: v3（同日）

---

## 概要

v3 修正後の全設計ドキュメントを再度クロスチェック。v2/v3 で修正済みの項目は除外し、新たに発見した問題のみを記録。

---

## 新規発見・修正した問題

### P1: 型安全性・設計整合性（5件）

| ID | 内容 | 場所 | 修正 |
|----|------|------|------|
| A.1 | ContractAggregate.accountID が `string`、全イベントの ContractID/AccountID も `string` のまま | event-sourcing.md | ✅ `shared.AccountID` / `shared.ContractID` に統一（全9イベント型） |
| D.1 | plugin-system.md セクション5.1の計算フロー順序が architecture.md と不一致（請求書生成ステップ欠落） | plugin-system.md | ✅ ステップ8「請求書生成」追加、9ステップに統一、architecture.md参照リンク追記 |
| H.1 | usage-guide.md の `NewMoney(980, shared.JPY)` — 引数に `int` を渡している（定義は `*big.Rat`） | usage-guide.md | ✅ `big.NewRat` を使うパターンに修正 |
| H.2 | usage-guide.md の `BillingCycle` が構造体として使われているが、domain-model.md では `string` 型 | usage-guide.md | ✅ `BillingCycleMonthly` 定数を使うパターンに修正 |
| B.1 | usage-guide.md の `ContractItem` 型が domain-model.md に未定義 | usage-guide.md | ✅ ファサード関数パターンに書き換え、`NewContract` 引数を最新設計に合わせて修正 |

### P2: 改善推奨（4件）

| ID | 内容 | 場所 | 修正 |
|----|------|------|------|
| E.1 | `LoadFromSnapshot` のバージョン設定がループ（O(n)） | event-sourcing.md | ✅ `SetVersion(v)` メソッド追加、ループを直接代入に最適化 |
| E.2 | EventRegistry と Apply の登録漏れリスク注記なし | event-sourcing.md | ✅ contractEventRegistry に「Register + Apply 両方を更新すること」注記追加 |
| C.1 | Payment `failed → pending` のリトライ上限条件が未明記 | domain-model.md | ✅ 「DunningConfig.MaxRetries に達した場合は failed のまま終端」と注記追加 |
| A.2 | ゼロ金額の生成方法が `NewMoney(0,1)` と `Zero()` で不統一 | - | 方針のみ: `Zero()` を推奨とし、実装フェーズで統一 |

### 未修正（実装フェーズ対応）

| ID | 内容 | 理由 |
|----|------|------|
| F.1 | metrics-invoicegen.md のフック定義が plugin-system.md と二重管理 | ドキュメント構造の問題。実装フェーズでどちらかに統合 |
| G.1 | Priority 定数の説明不足 | plugin-system.md L566-580 に定義はある。ガイダンス強化は実装時 |
| H.3 | Invoice エンティティのゲッターメソッド未列挙 | 全エンティティ共通。実装時に自動生成 |

---

## 修正ファイル一覧

| ファイル | 修正内容 |
|---------|---------|
| `event-sourcing.md` | 全9イベント型の ContractID/AccountID を shared 型に統一、SetVersion 追加、EventRegistry 注記 |
| `plugin-system.md` | 計算フロー順序を9ステップに統一（請求書生成ステップ追加）、architecture.md 参照リンク |
| `usage-guide.md` | NewMoney 引数型修正、BillingCycle 型修正、ContractItem→ファサード関数パターン |
| `domain-model.md` | Payment failed→pending のリトライ上限注記 |

---

## 累計修正サマリ（v2〜v4）

| レビュー | P0 | P1 | P2 | 合計 |
|---------|-----|-----|-----|------|
| v2 | 3 | 6 | 6 | 15 |
| v3 | 1 | 2 | 2 | 5 |
| v4 | 0 | 5 | 4 | 9 |
| **合計修正** | **4** | **13** | **12** | **29** |

## 残存 P2（全て実装フェーズ対応）

- InvoiceGenerationHook の ISP 分離
- PaymentGateway IF の分割
- ContractProjector の型スイッチ化
- DunningConfig の保持場所
- trialing → active 自動遷移メカニズム
- Suspension defer 時の再開請求フロー
- PricingModel.CalculatePrice の通貨情報明記
- metrics-invoicegen.md のフック定義二重管理解消
- Priority 定数のガイダンス強化
- ゼロ金額生成の `Zero()` 統一
- Invoice ゲッターメソッド列挙

---

## 総合評価

**P0/P1 は全てゼロ件。設計ドキュメントは実装フェーズに移行可能な状態。**
