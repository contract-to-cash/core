---
sidebar_label: Design Decisions
---

# 設計決定事項

本ドキュメントでは、Contract Billing Coreの設計上の重要な決定事項とその理由を記録する。

## 1. アーキテクチャ全般

### 1.1 CQRS（コマンド・クエリ分離）

| 決定 | **簡易CQRS**を採用 |
|------|-------------------|
| 内容 | 同一DBでProjectionテーブルを使用 |
| 理由 | 実装の複雑さとメリットのバランス |

**選択肢：**
- フルCQRS（別DB） → 複雑性が高い
- **簡易CQRS** → 同一DBで十分なパフォーマンス ✓
- CQRSなし → イベントソーシングの利点が活かせない

### 1.2 Projection更新タイミング

| 決定 | **利用者が選択可能** |
|------|---------------------|
| 内容 | 同期/非同期をオプションで指定可能 |

```go
type ProjectionOptions struct {
    SyncMode bool // true: 同期更新, false: 非同期更新
}
```

**理由：**
- 要件によって最適な選択が異なる
- 即座の一貫性が必要 → 同期
- スループット重視 → 非同期

### 1.3 マルチテナント

| 決定 | **サービス側に委ねる** |
|------|----------------------|
| 内容 | OSS側ではテナント分離を行わない |

**理由：**
- テナント分離の方式は利用者により様々
- OSSの複雑性を抑える
- 必要な場合はAccountIDでのフィルタリングで対応可能

### 1.4 タイムゾーン

| 決定 | **UTC固定** |
|------|------------|
| 内容 | 全イベント時刻はUTCで記録 |

**理由：**
- 時刻計算の一貫性
- タイムゾーン関連バグの防止
- 表示時に変換すれば十分

```go
// ❌ 禁止: time.Now() の直接呼び出し（テスト不可）
// event.OccurredAt = time.Now().UTC()

// ✅ 推奨: Clock IF 経由でUTC時刻を取得（テスト容易）
event.OccurredAt = clock.Now() // shared.Clock は常にUTCを返す
```

## 2. 契約ドメイン

### 2.1 トライアル管理

| 決定 | **ContractStatusに`trialing`を追加** |
|------|--------------------------------------|

```go
const (
    ContractStatusDraft     ContractStatus = "draft"
    ContractStatusTrialing  ContractStatus = "trialing"  // 追加
    ContractStatusActive    ContractStatus = "active"
    // ...
)
```

**トライアル設定：**

```go
type TrialConfiguration struct {
    TrialEndDate          time.Time // トライアル終了日
    AutoConvert           bool      // 自動本契約移行
    RequirePaymentMethod  bool      // 支払い方法事前登録必須
    ConversionReminderDays []int    // 移行リマインダー
}
```

**理由：**
- トライアルは独立した契約状態として管理が適切
- 期間管理、自動移行、通知などの機能を統一的に扱える

### 2.2 日割り計算（Proration）

| 決定 | **利用者が動作を選択可能** |
|------|--------------------------|

```go
type ProrationBehavior string

const (
    // 変更時に即座に日割り調整
    ProrationImmediate ProrationBehavior = "immediate"
    // 次サイクルから新価格適用（日割りなし）
    ProrationNextCycle ProrationBehavior = "next_cycle"
    // 変更時に即座に新価格で全額請求
    ProrationImmediateFull ProrationBehavior = "immediate_full"
)
```

**理由：**
- ビジネスモデルによって適切な方式が異なる
- デフォルトは`immediate`（最も一般的）

### 2.3 一時停止（Suspension）

| 決定 | **請求動作・期間延長・再開日を利用者が選択可能** |
|------|-------------------------------------------|

```go
type SuspensionConfiguration struct {
    SuspendedAt        time.Time
    ResumeDate         *time.Time          // nil = 手動再開
    BillingBehavior    SuspensionBillingBehavior
    ExtendContract     bool                // 契約期間延長
    Reason             string
}

type SuspensionBillingBehavior string

const (
    SuspensionBillingSkip     SuspensionBillingBehavior = "skip"     // 請求しない
    SuspensionBillingDefer    SuspensionBillingBehavior = "defer"    // 再開時にまとめて請求
    SuspensionBillingContinue SuspensionBillingBehavior = "continue" // 継続請求
)
```

**理由：**
- 一時停止の理由（ユーザー都合、サービス都合）により適切な動作が異なる
- 柔軟性を持たせることで様々なユースケースに対応

## 3. 請求・支払いドメイン

### 3.1 部分入金

| 決定 | **利用者が許可を選択、残高は同一請求書で管理** |
|------|-------------------------------------------|

```go
type Invoice struct {
    // ...
    AllowPartialPayment bool         // 部分入金許可
    PaidAmount          shared.Money // 入金済み金額
    Balance             shared.Money // 残高
}
```

**ステータス：**
- `partial_paid` ステータスを追加

**強制（オプトイン）：**
- 部分入金は **明示的なオプトイン制**。`Invoice.allowPartialPay` が false（既定）の場合、
  `Invoice.ValidatePayment` / `RecordPayment` は残高を残す入金（`amountDue` 未満）を
  `business_rule_violation` で拒否する。全額一括入金のみ許可。
- 生成フローでのオプトインは `BillingConfig.AllowPartialPayment`（または
  `service.WithAllowPartialPayment(true)`）で設定し、`BillingService.GenerateInvoice` が
  生成請求書の `allowPartialPay` に伝播する。低レベルでは `invoice.WithAllowPartialPayment(true)`。

**理由：**
- B2B取引では部分入金が発生しうる
- 残高を同一請求書で管理することで追跡が容易
- 既定で全額入金を要求し、部分入金は利用者が意図的に許可した請求書のみに限定する

### 3.2 バッチ処理

| 決定 | **OSSでインターフェースを定義、スケジューラはサービス側** |
|------|---------------------------------------------------|

```go
// OSSで定義
type BatchProcessor interface {
    Process(ctx context.Context, opts BatchOptions) (*BatchResult, error)
}

type BatchOptions struct {
    DryRun         bool
    ContinueOnError bool
    Concurrency    int
    // ...
}
```

**主要なバッチ処理（実装状況は issue #159 の棚卸しで確定）：**
| 処理 | 実装 | 説明 |
|------|------|------|
| `ContractRenewal` | ✅ `batch.ContractRenewalProcessor` | 契約自動更新 |
| `TrialExpiration` | ✅ `batch.TrialExpirationProcessor` | トライアル終了処理 |
| `BalanceExpiration` | ✅ `batch.BalanceExpirationProcessor`（#159 で追加） | 有効期限切れ残高（クレジット）の失効処理。`balance.Repository.FindExpired` でスキャンし、`BalanceEntry.MarkExpired` で残高を没収する |
| `InvoiceGenerator` | ❌ 未実装（利用者スケジューラ側の実装例として定義のみ） | 定期請求書生成 — `BillingService.GenerateInvoice` を利用者のスケジューラから呼ぶ |
| `PaymentRetry` | ❌ 未実装（利用者スケジューラ側の実装例として定義のみ） | 失敗決済リトライ — `PaymentService.ProcessPayment` + Dunning 状態を利用者側で組み合わせる |
| `UsageAggregator` | ❌ 未実装（利用者スケジューラ側の実装例として定義のみ) | 従量課金集計 — 集計は `GenerateInvoice` の従量パスが内部で行う |

未実装の 3 種は「コアがプロセッサを提供する」と読める宣言だったが実体がなかったため、
#159 の方針（#116 の delete-unused と同じ枠組み）に基づき「未実装・利用者責務」と明記する。
具体的な利用者要望が出た時点で `BatchProcessor` 実装として追加する。

**理由：**
- スケジューラは環境依存（cron, Kubernetes CronJob, Cloud Scheduler等）
- OSSは処理ロジックのみに集中

## 4. 冪等性

### 4.1 冪等性キー（issue #159 で強制境界を確定）

| 決定 | **コアは「存在」を強制し、「一意性」はリポジトリ/アダプタの契約とする** |
|------|---------------------------------------------------|

```go
type CreateContractCommand struct {
    IdempotencyKey string // 必須。Create が空を validation エラーで拒否
    // ...
}
```

**強制の分担（issue #159）：**

- **コア（強制済み）**: `ContractAggregate.Create` は空の `IdempotencyKey` を
  `validation` の DomainError で拒否し、キーを `ContractCreatedEvent`
  （SchemaVersion 3 で `idempotency_key` を追加）に載せる。集約は
  `IdempotencyKey()` getter を持ち、スナップショットにも保存される。
- **リポジトリ/アダプタ（利用者責務）**: キーの**一意性**強制はコアには不可能
  （単一集約の境界を越えるため）。永続層がユニークインデックス等で
  at-most-once 作成を保証する — 推奨 DDL と衝突時の
  `shared.ErrCodeConflict` 返却の契約は `contract.Repository.Save` の godoc に
  記載（#149 の請求書期間一意性パターンのミラー）。歴史的イベント（キーが空）は
  制約の対象外とする。
- **TTL**: 旧記載の `IdempotencyConfig{TTL}` 型はコードに存在したことがなく、
  削除した（#116 の「未使用宣言は削除」方針）。キーの保持期間・失効は
  アダプタの関心事であり、必要なら利用者がインデックス側で実装する。

**後方互換：**
- 歴史的な `ContractCreatedEvent`（v1/v2、キー未記録）は
  `ContractCreatedIdempotencyKeyUpcaster` が SchemaVersion を 3 に上げるのみで、
  キーは空のままリプレイされる（`Apply` が空を許容）。リプレイは壊れない。

**理由：**
- ネットワーク障害等でのリトライ時に重複作成を防止
- 宣言だけで強制されない「死にフィールド」状態（#159 指摘）を解消し、
  強制境界（コア=存在 / アダプタ=一意性）を明文化する

## 5. 監査ログ

### 5.1 メタデータ要件

| 決定 | **UserIDは必須、IP/UserAgentはオプション** |
|------|----------------------------------------|

```go
type EventMetadata struct {
    UserID      string  // 必須
    IPAddress   *string // オプション
    UserAgent   *string // オプション
}
```

**理由：**
- 誰が操作したかは必須情報
- IP/UserAgentはプライバシー配慮でオプション

### 5.2 CorrelationID / CausationID の扱い（Issue #116）

| 決定 | **Option B: 具体的な利用者が現れるまで削除する** |
|------|--------------------------------------------------|

`EventMetadata` には当初、将来のトレーシング統合に備えて `CorrelationID` と
`CausationID` の 2 フィールドが定義されていた。しかし約 1 年経過してもコアの
どのフロー（`BillingService` / `PaymentService` / `CreditNoteService` / `batch/` /
Saga）もこれらを設定せず、参照するプラグインフックもクエリ／プロジェクションも
存在しなかった（`docs/internals/codebase-review-20260327.md` で「未活用」と指摘）。

**選択肢：**
- Option A（採用・文書化）: セマンティクスを定義し、全サービスで伝播を実装、
  `docs/guides/tracing.md` を追加、伝播を検証する統合テストを追加する。
  → 直近の四半期にトレーシングをロードマップに載せる場合のみ妥当。
- **Option B（削除）** → 具体的な利用者が現れるまで削除する ✓

**理由：**
- **非対称なリスク**: 削除は追記専用ストレージ上のイベントスキーマ変更（本来は
  Upcaster 対応）だが、**追加は純粋に非破壊（additive）** なので後からいつでも
  再導入できる。コミットしないなら削除する方が安全。
- 中途半端に「定義済みだが未使用」の状態は最悪。採用者がスキーマ上のフィールドを
  見て、我々が定義していないセマンティクスを推測してしまう。
- 追記専用ストレージでは全書き込みに（空値でも）永続的に含まれ、ペイロードを
  わずかに肥大させる。

**後方互換（読み取り安全性）：**
- `EventMetadata` は schema-versioned な `Event.Data` ペイロードとは独立に JSON
  デシリアライズされるため、**Upcaster は不要**。
- 既存の保存済みイベントの metadata JSON に `correlation_id` / `causation_id`
  キーが残っていても、`json.Unmarshal` は未知キーを無視するため読み取りは壊れない。
- したがって snapshot / schema version のバンプも不要（metadata はイベント本体の
  スキーマバージョン管理下にない）。

**再導入する場合：**
- トレーシングを実装する段階で、単一フィールドの追加として `EventMetadata` に
  戻せばよい（非破壊変更）。その際は Option A のとおりセマンティクス定義・伝播実装・
  ガイド・テストをセットで行う。

## 6. プラグインシステム

### 6.1 実行順序

| 決定 | **会計基準に則った順序で実行** |
|------|------------------------------|

```
1. 計算前処理（InvoiceLifecycleHook.BeforeCalculation）
   ※ この時点で ctx.Subtotal() は ZERO（基本料金は手順2でコンテキストへ設定）
2. 基本料金の算出とコンテキストへの設定（契約タイプに応じて分岐）
   → 以降 ctx.Subtotal() は基本料金を返す
3. 割引適用（DiscountHook、割引上限ガード付き。ctx.Subtotal()=基本料金）
4. 小計算出（subtotal - totalDiscount）→ ctx.SetSubtotalAfterDiscount()
5. 税計算（TaxHook、ctx.SubtotalAfterDiscount()に対して）
6. 合計算出（afterDiscount + totalTax）
7. クレジット台帳からの充当（残高があれば差引、FIFO・tx内）
8. 請求書をdraft状態で生成（tx内）
9. 計算後処理（InvoiceLifecycleHook.AfterCalculation）← 保存(Save)より前・tx内
10. 保存（tx内）
```

> このフロー順序は `architecture.md` セクション6.3 および `plugin-system.md` セクション5.1 と同一。
> 実コードは `application/service/billing_service.go` の `executeBillingPipeline`（正準はソース）。

**理由：**
- 会計上正しい計算順序を保証
- 税は割引後の金額に対して計算する必要がある

**プラグイン可観測性の要点：**
- `BeforeCalculation` 中の `ctx.Subtotal()` は **ゼロ**（基本料金はフックの後にコンテキストへ設定される）
- `AfterCalculation` は請求書生成の**後・保存の前**に発火する（受け取る請求書は未永続化）。
  永続化済みを前提とする処理は `OnInvoiceIssuedHook`（`FinalizeInvoice` の保存後に発火）で行う

### 6.2 トランザクション境界

| 決定 | **プラグインはコアと同一トランザクション** |
|------|----------------------------------------|

**理由：**
- データ整合性の保証
- ロールバック時にプラグインの変更も戻る

## 7. イベントソーシング

### 7.1 イベントバージョニング

| 決定 | **SchemaVersionフィールドを追加、将来のupcaster用** |
|------|------------------------------------------------|

```go
type Event struct {
    // ...
    SchemaVersion int // イベントスキーマバージョン
    // ...
}
```

**理由：**
- イベントスキーマの変更に対応
- 古いイベントを新しいスキーマに変換（upcaster）可能

### 7.2 スナップショット戦略

| 決定 | **N件ごとのスナップショット（デフォルト100件）** |
|------|-------------------------------------------|

**理由：**
- イベント再生の高速化
- 頻度は利用者が調整可能

## 8. 決定マトリクス

| カテゴリ | 項目 | 決定 | 代替案 |
|---------|------|------|--------|
| アーキテクチャ | CQRS | 簡易CQRS | フルCQRS, なし |
| アーキテクチャ | Projection更新 | 選択可能 | 同期のみ, 非同期のみ |
| アーキテクチャ | マルチテナント | サービス側 | OSS側で対応 |
| アーキテクチャ | タイムゾーン | UTC固定 | 設定可能 |
| 契約 | トライアル | ステータスで管理 | 別エンティティ |
| 契約 | 日割り | 選択可能 | 固定方式 |
| 契約 | 一時停止 | 選択可能 | 固定方式 |
| 請求 | 部分入金 | 選択可能 | 不許可 |
| 請求 | バッチ | インターフェースのみ | フル実装 |
| 冪等性 | スコープ | 契約作成 | 全操作 |
| 冪等性 | 強制境界 | コア=存在 / アダプタ=一意性 | コアで一意性強制 |
| 冪等性 | TTL | アダプタ責務 | コアで固定 |
| 監査 | UserID | 必須 | オプション |
| 監査 | IP/UA | オプション | 必須 |
| プラグイン | 実行順序 | 会計基準 | 任意 |
| プラグイン | トランザクション | 同一 | 分離 |
| イベント | バージョニング | SchemaVersion | なし |
| イベント | スナップショット | N件ごと | 時間ベース |

## 9. 将来の検討事項

以下は現時点では実装しないが、将来検討が必要な項目：

1. **分散トランザクション** - 複数サービス間の整合性
2. **イベントアーカイブ** - 古いイベントの別ストレージ移動
3. **リアルタイム通知** - WebSocket/SSEによるイベント配信
4. **A/Bテスト機能** - 料金プランのA/Bテスト
5. **多通貨対応の拡張** - 為替レート管理、通貨換算
