# Contract Billing Core 設計レビュー v2

**レビュー日**: 2026-03-26
**対象ブランチ**: `docs/design-review-improvements`
**前回レビュー**: `2026-03-25-design-review.md`（スコア 4.5/5.0）

---

## 総合所見

前回レビューで指摘された P0・設計課題・改善1〜4は全て適用済み。
本レビューでは、残存する矛盾・考慮漏れ・設計上の気になる点を洗い出した。

---

## 1. 矛盾・不整合（6件）

### 1.1 [P1] `Contract.planID` の型不一致

**場所**: `domain-model.md` L234 / `event-sourcing.md` L459

`domain/shared/identifier.go` で `PlanID string` を定義済みだが、`Contract.planID` や `ContractCreatedEvent.PlanID` は素の `string` のまま。

**修正方針**: `shared.PlanID` に統一する。

---

### 1.2 [P1] `ProrationResult` の `Sub` メソッド呼び出し

**場所**: `domain-model.md` L746

```go
AdjustmentAmount: charge.Sub(credit), // ← Sub は未定義
```

`Money` 型に定義されているのは `Subtract`（L43）。

**修正方針**: `charge.Sub(credit)` → `charge.Subtract(credit)` に修正。戻り値の `(Money, error)` も適切にハンドリング。

---

### 1.3 [P0] `BaseAggregate` の unexported フィールドに別パッケージからアクセス

**場所**: `event-sourcing.md` L165-172, L300

```go
// eventstore パッケージで定義
type BaseAggregate struct {
    id                string          // unexported
    version           int             // unexported
    uncommittedEvents []Event         // unexported
}

// contract パッケージから直接初期化 → コンパイルエラー
BaseAggregate: eventstore.BaseAggregate{id: id}
```

**修正方針**: Go の既存イベントソーシングライブラリ（hallgren/eventsourcing, looplab/eventhorizon）に倣い、コンストラクタ関数 `NewBaseAggregate(id)` を提供する。

---

### 1.4 [P0] `RaiseEvent` 内の `time.Now()` 直接呼び出し

**場所**: `event-sourcing.md` L212

Clock IF を全廃止した方針（改善3）に反して、`BaseAggregate.RaiseEvent` で `time.Now().UTC()` を直接呼んでいる。

**修正方針**: `BaseAggregate` に `clock shared.Clock` フィールドを追加し、`NewBaseAggregate(id, clock)` で受け取る。`OccurredAt` は `clock.Now()` で設定。

---

### 1.5 [P1] `Evolver` インターフェースと `Apply` 実装の乖離

**場所**: `event-sourcing.md` L156-159, L369-405

`Evolver` は「純粋関数」と記述しているが、`ContractAggregate.Apply` は内部で `EventRegistry.Deserialize` を呼んでおり純粋ではない。

**修正方針**: `Apply` は型付き `DomainEvent` を受け取る設計に変更。デシリアライズは呼び出し元（`LoadFromHistory` やリポジトリ層）の責務とする。

```go
// Evolver 修正後
type Evolver interface {
    Apply(event DomainEvent) error  // 型付きイベントを受け取る
}
```

---

### 1.6 [P2] `ContractProjector` が文字列ベースの switch を使用

**場所**: `event-sourcing.md` L797

集約側は型スイッチに移行済みだが、Projector は `event.Type` 文字列で switch している。

**修正方針**: Projector 側も `EventRegistry` 経由でデシリアライズし型スイッチを使用する。ただし Projection 層はインフラ層であり、EventType 定数での switch も許容可能。実装フェーズで統一判断とし、P2（低優先度）とする。

---

## 2. 考慮漏れ（8件）

### 2.1 [P2] クレジット適用フローでの通貨一致チェック明記

**場所**: `domain-model.md` L926-945

`FindAvailable` は通貨パラメータを受け取るが、適用フローの手順に通貨チェックの記述がない。

**修正方針**: フロー説明に「FindAvailable(accountID, invoice.Currency()) で同一通貨のクレジットのみ取得」と明記。

---

### 2.2 [P2] クレジット有効期限切れの自動失効処理

`CreditEntry.IsExpired()` は判定のみ。`GetBalance` が期限切れ分を除外する要件が未明記。

**修正方針**: `GetBalance` / `FindAvailable` のドキュメントに「有効期限内のエントリのみを対象」と明記。加えて、`CreditExpirationProcessor` バッチをバッチ処理リストに追加。

---

### 2.3 [P0] クレジット適用の集約間トランザクション設計

**場所**: `domain-model.md` L937-938

`CreditEntry`（Credit集約）と `Invoice`（Invoice集約）の更新を「単一トランザクション内でアトミックに実行」と記載。しかしイベントソーシングでは1トランザクション = 1集約が原則。

**調査結果**:
- **Saga / Process Manager パターン**: イベント駆動で集約間の整合性を結果整合性で保証する
- **同一集約に含める**: Credit適用をInvoice集約の責務に含める
- **アプリケーションサービスでの調整**: 簡易CQRSなので同一DBトランザクションで複数集約を更新する

**修正方針**: 本プロジェクトは「簡易CQRS（同一DB）」を採用しているため、**アプリケーションサービス層で同一DBトランザクション内に複数集約の更新をまとめる方式**が最も適切。将来フルCQRS移行時はSagaパターンへの移行パスを注記として残す。

---

### 2.4 [P2] `DunningConfig` の保持場所が未定義

**修正方針**: 実装フェーズ（S3）で決定。`Account.BillingInfo` にデフォルト設定を持たせ、`Contract` レベルでのオーバーライドも検討。

---

### 2.5 [P2] `trialing → active` 自動遷移メカニズム未設計

**修正方針**: `TrialExpirationProcessor`（バッチ）の設計は実装フェーズで詳細化。バッチ処理リスト（design-decisions.md）に記載済みなので、ここではフロー概要のみ追記。

---

### 2.6 [P2] Suspension `defer` 時の再開請求フロー未定義

**修正方針**: 実装フェーズで詳細化。設計ドキュメントには「再開時に停止期間分を1つの請求書にまとめる」旨の方針のみ追記。

---

### 2.7 [P1] `quantity` の型が3ドキュメントで不統一

| 場所 | 型 |
|------|-----|
| `UsageRecord.quantity` | `int64` |
| `LineItem.quantity` | `int` |
| `InvoiceLineItem.Quantity` (metrics-invoicegen) | `float64` |

**修正方針**: ドメインモデル側を `int64` に統一。請求書ドキュメント（InvoiceLineItem）の `Quantity` は `float64`（小数量=0.5時間等の表現用）のまま残し、型変換の責務を明記。

---

### 2.8 [P2] `PricingModel.CalculatePrice` の通貨情報

**修正方針**: 各 `PricingModel` 実装が内部の `Money` フィールドから通貨を取得する設計であることをコメントで明記。

---

## 3. 気になる点（6件）

### 3.1 [P2] `InvoiceGenerationHook` が ISP に反している

3メソッド（`BuildDocument`, `AfterRender`, `AfterDelivery`）を1つのIFに含む。

**修正方針**: 他フックと同様に分離を推奨するが、この3つは密結合度が高く分離の実益が薄い場合もある。実装フェーズで判断。

---

### 3.2 [P2] `PaymentGateway` インターフェースの肥大化

11メソッドを含む。全ゲートウェイが全メソッドをサポートするとは限らない。

**修正方針**: `PaymentGateway`（Charge/Refund/GetTransaction）と `AuthorizationGateway`（Authorize/Capture/Void）の分離を推奨。実装フェーズで判断。

---

### 3.3 [P1] `ContractChangeEvent.OldValue/NewValue` が `interface{}`

**修正方針**: ジェネリクスまたは ChangeType ごとの型付きイベント構造体に変更。

---

### 3.4 [P2] `CalculationContext` の `domain/contract` 直接依存

**修正方針**: plugin パッケージがドメイン層を参照するのは避けがたい（計算に契約情報が必要）。現状維持で許容。ただし interface 経由にすることで結合度を下げることは可能。

---

### 3.5 [P1] `LoadFromSnapshot` メソッドが未定義

**場所**: `event-sourcing.md` L623

`ContractAggregate` にも `AggregateRoot` IF にも定義がない。

**修正方針**: `AggregateRoot` に `LoadFromSnapshot(snapshot Snapshot) error` を追加。

---

### 3.6 [P2] `OccurredAt` vs `RecordedAt` の使い分け

`LoadUntil` / `LoadRange` でどちらのタイムスタンプで検索するか未定義。

**修正方針**: 時点再構築は `OccurredAt`（ビジネス時刻）ベースで検索する旨を Event Store IF のドキュメントに明記。

---

## 修正優先度マトリクス

| 優先度 | ID | 内容 | 影響 |
|--------|-----|------|------|
| **P0** | 1.3 | BaseAggregate unexported フィールド | コンパイル不可 |
| **P0** | 1.4 | RaiseEvent の time.Now() | Clock IF 方針違反 |
| **P0** | 2.3 | クレジット集約間トランザクション | データ整合性 |
| **P1** | 1.1 | PlanID 型不一致 | 型安全性 |
| **P1** | 1.2 | Sub → Subtract | コンパイルエラー |
| **P1** | 1.5 | Apply シグネチャ | 設計整合性 |
| **P1** | 2.7 | quantity 型不統一 | 横断整合性 |
| **P1** | 3.3 | ContractChangeEvent interface{} | 型安全性 |
| **P1** | 3.5 | LoadFromSnapshot 未定義 | 機能欠落 |
| **P2** | 残り | 上記以外 | 実装フェーズで対応可 |

---

## 対応状況

| ID | 状態 | 備考 |
|----|------|------|
| 1.3 | ✅ 修正済み | `NewBaseAggregate(id, clock)` コンストラクタ追加 |
| 1.4 | ✅ 修正済み | BaseAggregate に Clock IF 追加、RaiseEvent で clock.Now() 使用 |
| 2.3 | ✅ 設計追記 | アプリケーション層トランザクション方式 + 将来のSaga移行パス |
| 1.1 | ✅ 修正済み | `shared.PlanID` に統一 |
| 1.2 | ✅ 修正済み | `Subtract` + error ハンドリング |
| 1.5 | ✅ 修正済み | `Apply(DomainEvent)` に変更 |
| 1.6 | - | P2: 実装フェーズで判断 |
| 2.1 | ✅ 修正済み | 通貨一致チェック明記 |
| 2.2 | ✅ 修正済み | GetBalance 要件明記 + バッチ追加 |
| 2.7 | ✅ 修正済み | LineItem.quantity を int64 に統一 |
| 3.3 | ✅ 修正済み | 型付き ChangeType + ジェネリクス方針追記 |
| 3.5 | ✅ 修正済み | AggregateRoot IF に追加 |
