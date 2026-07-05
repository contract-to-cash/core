---
sidebar_position: 2
---

# イベントソーシング

Contract Billing Coreは、Contract集約にイベントソーシングを使用し、完全な監査証跡と時間旅行機能を提供します。

## 仕組み

現在の状態のみを保存する代わりに、すべての状態変更が不変のイベントとして記録されます：

```
ContractCreatedEvent     → status: draft, price: ¥3,000
ContractActivatedEvent   → status: active
PriceChangedEvent        → price: ¥5,000（即時適用）
ContractSuspendedEvent   → status: suspended
ContractResumedEvent     → status: active
ContractRenewedEvent     → 新しい期間、保留価格がプロモート
```

現在の状態は、すべてのイベントを最初からリプレイして再構築します：

```go
agg := contract.NewContractAggregate(contractID, clock)
events, _ := eventStore.Load(ctx, string(contractID))
agg.LoadFromHistory(events)
// aggは現在の状態を反映
```

## イベント構造

すべてのイベントには監査とトレーサビリティのためのメタデータが含まれます：

```go
type Event struct {
    ID            string           // ユニークイベントID
    StreamID      string           // 集約ID（契約ID）
    Type          EventType        // 例: "contract.created"
    Version       int              // ストリーム内のシーケンシャルバージョン
    SchemaVersion int              // 将来のアップキャスティング用
    Data          json.RawMessage  // イベント固有のペイロード
    Metadata      EventMetadata    // 監査情報
    OccurredAt    time.Time        // ビジネス時刻
    RecordedAt    time.Time        // システム時刻
}

type EventMetadata struct {
    UserID    string   // 操作を実行した人（必須）
    IPAddress *string  // オプション
    UserAgent *string  // オプション
}
// 注: CorrelationID / CausationID は未活用のため Issue #116（Option B）で削除。
// 具体的な利用者が現れた段階で非破壊的に再導入する。
```

## 時間旅行クエリ

過去の任意の時点での契約状態をクエリ：

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// 5月15日時点の契約状態は？
may15 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
historical, _ := queryService.GetContractAsOf(ctx, contractID, may15)
historical.Status() // "active"
historical.Price()  // ¥3,000（価格変更前）

// 完全な変更履歴
history, _ := queryService.GetContractHistory(ctx, contractID)
for _, entry := range history {
    fmt.Printf("%s: %s (by %s)\n", entry.OccurredAt, entry.EventType, entry.UserID)
}
```

## スナップショット

多数のイベントを持つ集約では、スナップショットでロードを高速化：

```go
snapshotService := service.NewSnapshotService(eventStore, clock, snapshotInterval)

// 現在の状態のスナップショットを保存
snapshotService.CreateSnapshot(ctx, agg)

// ロード時はスナップショット + 最近のイベントを使用（全イベントの代わりに）
snap, _ := eventStore.LoadSnapshot(ctx, string(contractID))
agg.LoadFromSnapshot(*snap)
// スナップショットバージョン以降のイベントのみリプレイ
```

## Event Storeインターフェース

このインターフェースをデータベース向けに実装します：

```go
type Store interface {
    Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error
    Load(ctx context.Context, streamID string) ([]Event, error)
    LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)
    LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)
    LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)
    Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)
    SaveSnapshot(ctx context.Context, snapshot Snapshot) error
    LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)
    LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
```

`expectedVersion`による楽観的ロックで並行書き込みの競合を防止します。

## 契約イベント一覧

| イベント | トリガー |
|---------|---------|
| `ContractCreatedEvent` | `agg.Create()` |
| `ContractActivatedEvent` | `agg.Activate()` |
| `ContractSuspendedEvent` | `agg.Suspend()` |
| `ContractResumedEvent` | `agg.Resume()` |
| `ContractCancelledEvent` | `agg.Cancel()` |
| `ContractRenewedEvent` | `agg.Renew()` |
| `ContractExpiredEvent` | `agg.Renew()` autoRenew=false時 |
| `PriceChangedEvent` | `agg.ChangePrice()` IMMEDIATEポリシー |
| `PriceChangeScheduledEvent` | `agg.ChangePrice()` END_OF_TERMポリシー |
| `PriceChangeUnscheduledEvent` | `agg.UnscheduleChange()` |
| `TrialStartedEvent` | `agg.StartTrial()` |
| `TrialEndedEvent` | `agg.EndTrial()` |

## プロジェクション

プロジェクションサービスでイベントから読み取り最適化ビューを構築：

```go
type Projector interface {
    Project(ctx context.Context, event eventstore.Event) error
    Rebuild(ctx context.Context, until time.Time) error
}

projectionService := projection.NewProjectionService(eventStore, projection.ProjectionOptions{
    SyncMode:   true,
    BatchSize:  100,
    MaxRetries: 3,
})
projectionService.RegisterProjector(myProjector)
projectionService.Start(ctx)
```
