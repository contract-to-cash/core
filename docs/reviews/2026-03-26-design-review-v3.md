# Contract Billing Core 設計レビュー v3

**レビュー日**: 2026-03-26
**対象ブランチ**: `docs/design-review-improvements`
**前回レビュー**: `2026-03-26-design-review-v2.md`

---

## 概要

v2 レビューで P0〜P1 の修正を適用後、全設計ドキュメントを再度クロスチェックした。
v2 修正済みの項目は除外し、**新たに発見した問題のみ**を記録する。

---

## 新規発見事項

### 1. [P0] Money.Add/Subtract の (Money, error) 戻り値が無視されていた

**場所**: `plugin-system.md` 複数箇所

`Money.Add()` / `Money.Subtract()` は `(Money, error)` を返すが、
BillingService.GenerateInvoice 内で `_, _` や 1値代入で error を無視していた。

| 行 | コード（修正前） | 問題 |
|----|-----------------|------|
| L961 | `totalDiscount, _ = totalDiscount.Add(discount)` | error 無視 |
| L971 | `afterDiscount, _ := subtotal.Subtract(totalDiscount)` | error 無視 |
| L981 | `totalTax, _ = totalTax.Add(tax)` | error 無視 |
| L985 | `total, _ := afterDiscount.Add(totalTax)` | error 無視 |
| L998 | `remaining := total.Subtract(appliedCredit)` | 1値代入（コンパイルエラー） |
| L1003 | `appliedCredit = appliedCredit.Add(apply)` | 1値代入（コンパイルエラー） |
| L1008 | `amountDue := total.Subtract(appliedCredit)` | 1値代入（コンパイルエラー） |
| L1125 | `totalCharge, _ = totalCharge.Add(charge)` | error 無視 |
| L1130 | `totalCharge, _ = totalCharge.Add(basePrice)` | error 無視 |

**修正内容**: 全箇所で `(value, err)` を受け取り、`err != nil` をチェック。
`var totalXxx shared.Money` → `shared.Zero(currency)` に変更し nil ポインタも回避。

**状態**: ✅ 修正済み

---

### 2. [P1] `min()` 関数が未定義（Go 1.21 以前でコンパイル不可）

**場所**: `plugin-system.md` L1002

```go
apply := min(entry.RemainingAmount(), remaining)  // min() は Money 型に対応しない
```

**修正内容**:
- `domain-model.md` の Money 型に `Min(other Money) (Money, error)` メソッドを追加
- `min()` → `entry.RemainingAmount().Min(remaining)` に変更

**状態**: ✅ 修正済み

---

### 3. [P1] クーポンプラグイン内の Money.Add error 無視

**場所**: `plugin-system.md` L669

```go
totalDiscount, _ = totalDiscount.Add(discount)
```

**修正内容**: error をチェックし、失敗時はエラーを返す。
`var totalDiscount shared.Money` → `shared.Zero(subtotal.Currency())` に変更。

**状態**: ✅ 修正済み

---

### 4. [P2] architecture.md と plugin-system.md の計算フロー順序が不一致

**場所**: `architecture.md` L193-206

architecture.md には「クレジット充当」ステップが欠落しており、
plugin-system.md のステップ6（クレジット充当）と齟齬があった。

**修正内容**: architecture.md に「7. クレジット台帳からの充当」を追加し、
後続ステップの番号を繰り上げ（8. 請求書生成, 9. AfterCalculation）。

**状態**: ✅ 修正済み

---

### 5. [P2] Repository IF の引数に非 shared 型（ローカル型名）が残存

**場所**: `domain-model.md`

| リポジトリ | メソッド | 修正前 | 修正後 |
|-----------|---------|--------|--------|
| contract.Repository | FindByID | `ContractID` | `shared.ContractID` |
| contract.Repository | FindByIDAsOf | `ContractID` | `shared.ContractID` |
| invoice.Repository | FindByID | `InvoiceID` | `shared.InvoiceID` |
| invoice.Repository | FindByIDAsOf | `InvoiceID` | `shared.InvoiceID` |

**修正内容**: 全て `shared.XxxID` に統一。
usage.Repository, credit.Repository は既に `shared.XxxID` を使用済みで修正不要。

**状態**: ✅ 修正済み

---

## v2 から引き続き P2（実装フェーズ対応）のままの項目

| ID | 内容 | 備考 |
|----|------|------|
| v2-1.6 | ContractProjector の文字列 switch | EventType 定数で switch は許容 |
| v2-3.1 | InvoiceGenerationHook の ISP 分離 | 3メソッドの密結合度が高い |
| v2-3.2 | PaymentGateway IF の肥大化 | 実装時に分離判断 |
| v2-3.4 | CalculationContext の domain/contract 直接依存 | 現状許容 |
| v2-2.4 | DunningConfig の保持場所 | 実装フェーズで決定 |
| v2-2.5 | trialing → active 自動遷移メカニズム | バッチ処理で対応 |
| v2-2.6 | Suspension defer 時の再開請求フロー | 実装フェーズで詳細化 |
| v2-2.8 | PricingModel.CalculatePrice の通貨情報 | コメントで明記済み |
| v2-3.6 | OccurredAt vs RecordedAt の使い分け | Store IF に明記済み |
| P2-7 | InvoiceLineItem.Quantity float64 vs int64 | 型変換責務を注記済み |
| P2-8 | usage-guide.md のコード例不完全 | ガイドの網羅性の問題 |
| P2-9 | Clock IF 一貫性の明記 | ガイダンス改善 |

---

## 修正ファイル一覧

| ファイル | 修正内容 |
|---------|---------|
| `domain-model.md` | Money.Min() メソッド追加、Repository IF の shared.XxxID 統一 |
| `plugin-system.md` | Money.Add/Subtract の error ハンドリング全修正、min() → Min()、Zero() 初期化 |
| `architecture.md` | 計算フローにクレジット充当ステップ追加（ステップ7） |

---

## 総合評価

| 観点 | 前回(v2) | 今回(v3) |
|------|---------|---------|
| P0 残存 | 0件 | 0件 |
| P1 残存 | 0件 | 0件 |
| P2 残存 | 12件 | 12件（全て実装フェーズ対応） |

**設計ドキュメントの品質は十分に高く、実装フェーズに移行可能な状態。**
