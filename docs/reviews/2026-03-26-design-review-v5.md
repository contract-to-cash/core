# Contract Billing Core 設計レビュー v5

**レビュー日**: 2026-03-26
**対象ブランチ**: `docs/design-review-improvements`
**前回レビュー**: v4（同日）

---

## 概要

v4 修正後の全設計ドキュメントをクロスチェック。エージェント検出 + 手動検証で精査。
v2〜v4 で修正済みの項目は除外し、新たに発見した問題のみを記録。

---

## 新規発見・修正した問題

### P1: Clock IF 方針違反（1件）

| ID | 内容 | 場所 | 修正 |
|----|------|------|------|
| 5.1 | `InvoiceGenerationService` の `time.Now()` 直接呼び出し | metrics-invoicegen.md L1112 | ✅ `s.clock.Now()` に修正、Service 構造体に `clock shared.Clock` 追加、コンストラクタに clock 引数追加 |

### P1: 計算フロー不整合（1件）

| ID | 内容 | 場所 | 修正 |
|----|------|------|------|
| 5.2 | design-decisions.md の計算フローにクレジット充当・請求書生成・フック前後処理が欠落（6ステップのまま） | design-decisions.md L249-257 | ✅ architecture.md / plugin-system.md と同じ9ステップに統一、相互参照リンク追加 |

### P2: 注記改善（1件）

| ID | 内容 | 場所 | 修正 |
|----|------|------|------|
| 5.3 | Apply の exhaustive lint と default clause の共存が曖昧 | event-sourcing.md L420-422 | ✅ 「default clause は未知イベント型の防御として保持」と明記、lint ツール名を追記 |

---

## エージェント誤報告の検証結果

クロスチェックエージェントが P0 として報告した「domain-model.md にセクション9（Credit）が欠落」は**誤報告**。
domain-model.md L777 に「9. クレジット台帳（Credit Ledger）」が存在することを確認済み。

---

## 修正ファイル一覧

| ファイル | 修正内容 |
|---------|---------|
| `metrics-invoicegen.md` | Service 構造体に clock フィールド追加、NewService に clock 引数追加、time.Now() → s.clock.Now() |
| `design-decisions.md` | 計算フローを9ステップに統一、相互参照リンク追加 |
| `event-sourcing.md` | Apply の exhaustive lint + default clause 注記改善 |

---

## 全レビュー累計修正サマリ（v2〜v5）

| レビュー | P0 | P1 | P2 | 合計 |
|---------|-----|-----|-----|------|
| v2 | 3 | 6 | 6 | 15 |
| v3 | 1 | 2 | 2 | 5 |
| v4 | 0 | 5 | 4 | 9 |
| v5 | 0 | 2 | 1 | 3 |
| **合計** | **4** | **15** | **13** | **32** |

---

## 残存 P2（全て実装フェーズ対応）

| # | 内容 |
|---|------|
| 1 | InvoiceGenerationHook の ISP 分離 |
| 2 | PaymentGateway IF の分割 |
| 3 | ContractProjector の型スイッチ化 |
| 4 | DunningConfig の保持場所 |
| 5 | trialing → active 自動遷移メカニズム |
| 6 | Suspension defer 時の再開請求フロー |
| 7 | metrics-invoicegen.md のフック定義二重管理解消 |
| 8 | Invoice → InvoiceDocument 変換フローの明示 |
| 9 | usage-guide.md のバッチ処理実装例拡充 |
| 10 | WebhookProcessor handler 関数の error 型ガイドライン |
| 11 | LineItem.Quantity int64→float64 変換関数の定義場所 |
| 12 | ゼロ金額生成の `Zero()` 統一 |

---

## v6 追加チェック（同日追記）

v5 修正後に全ドキュメントを再クロスチェックした結果、軽微な問題を **2件** 発見・修正。

| ID | 内容 | 場所 | 修正 |
|----|------|------|------|
| 6.1 | design-decisions.md L60 で `time.Now().UTC()` がアンチパターンか仕様か曖昧 | design-decisions.md | ✅ `❌ 禁止` / `✅ 推奨` の対比パターンに書き換え |
| 6.2 | metrics-invoicegen.md L1524 の DI 設定例で clock 引数が漏れ | metrics-invoicegen.md | ✅ `clock` 引数を追加 |

**v6 チェックの全観点結果:**
- A. 型の整合性: 問題なし
- B. メソッド呼び出しと定義: 問題なし
- C. 状態遷移ルール: 問題なし
- D. 計算フロー（3箇所）: 完全一致
- E. Clock IF 徹底: ✅ 全解消（v6.1 修正で最後の1件を解消）
- F. import パス: 問題なし
- G. プラグインフック: 問題なし
- H. イベント定義: 問題なし
- I. v5 修正箇所の検証: ✅ 正常（v6.2 の DI 例漏れを補完）

---

## 最終累計（v2〜v6）

| レビュー | P0 | P1 | P2 | 合計 |
|---------|-----|-----|-----|------|
| v2 | 3 | 6 | 6 | 15 |
| v3 | 1 | 2 | 2 | 5 |
| v4 | 0 | 5 | 4 | 9 |
| v5 | 0 | 2 | 1 | 3 |
| v6 | 0 | 0 | 2 | 2 |
| **合計** | **4** | **15** | **15** | **34** |

---

## 総合評価

**P0: 0件、P1: 0件。6回のレビューを経て全設計ドキュメントの P0/P1 はゼロ件。**

- 計算フローが architecture.md / plugin-system.md / design-decisions.md の3箇所で完全同期
- Clock IF の適用漏れが全て解消（design-decisions.md の説明例含む）
- 全イベント型の shared ID 統一完了
- Money 型の error ハンドリング統一完了

**設計ドキュメントは実装フェーズに移行可能な状態。**
