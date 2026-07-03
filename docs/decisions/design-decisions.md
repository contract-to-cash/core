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

**主要なバッチ処理：**
| 処理 | 説明 |
|------|------|
| `InvoiceGenerator` | 定期請求書生成 |
| `ContractRenewal` | 契約自動更新 |
| `PaymentRetry` | 失敗決済リトライ |
| `TrialExpiration` | トライアル終了処理 |
| `UsageAggregator` | 従量課金集計 |
| `BalanceExpiration` | 有効期限切れ残高（クレジット）の失効処理 |

**理由：**
- スケジューラは環境依存（cron, Kubernetes CronJob, Cloud Scheduler等）
- OSSは処理ロジックのみに集中

## 4. 冪等性

### 4.1 冪等性キー

| 決定 | **契約作成で必須、TTLは利用者設定可能（デフォルト24h）** |
|------|---------------------------------------------------|

```go
type CreateContractCommand struct {
    IdempotencyKey string // 必須
    // ...
}

type IdempotencyConfig struct {
    TTL time.Duration // デフォルト: 24 * time.Hour
}
```

**理由：**
- ネットワーク障害等でのリトライ時に重複作成を防止
- TTLを設けることでストレージ圧迫を防ぐ

## 5. 監査ログ

### 5.1 メタデータ要件

| 決定 | **UserIDは必須、IP/UserAgentはオプション** |
|------|----------------------------------------|

```go
type EventMetadata struct {
    UserID      string  // 必須
    IPAddress   *string // オプション
    UserAgent   *string // オプション
    CorrelationID string
    CausationID   string
}
```

**理由：**
- 誰が操作したかは必須情報
- IP/UserAgentはプライバシー配慮でオプション
- 相関ID/因果IDはトレーシングに有用

## 6. プラグインシステム

### 6.1 実行順序

| 決定 | **会計基準に則った順序で実行** |
|------|------------------------------|

```
1. 計算前処理（InvoiceLifecycleHook.BeforeCalculation）
2. 料金計算（契約タイプに応じて分岐）
3. 割引適用（DiscountHook、割引上限ガード付き）
4. 小計算出（subtotal - totalDiscount）
5. 税計算（TaxHook、割引後に対して）
6. 合計算出（afterDiscount + totalTax）
7. クレジット台帳からの充当（残高があれば差引）
8. 請求書をdraft状態で生成
9. 計算後処理（InvoiceLifecycleHook.AfterCalculation）
```

> このフロー順序は `architecture.md` セクション6.3 および `plugin-system.md` セクション5.1 と同一。

**理由：**
- 会計上正しい計算順序を保証
- 税は割引後の金額に対して計算する必要がある

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
| 冪等性 | TTL | 設定可能 | 固定 |
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
