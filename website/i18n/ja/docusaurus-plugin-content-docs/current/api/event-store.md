---
sidebar_position: 4
---

# Event Storeリファレンス

## Storeインターフェース

```go
import "github.com/contract-to-cash/core/eventstore"

type Store interface {
    // 楽観的ロック付きイベント追加
    Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error

    // ストリームの全イベントをロード
    Load(ctx context.Context, streamID string) ([]Event, error)

    // 特定バージョンまでのイベントをロード
    LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)

    // 特定時点までのイベントをロード（時間旅行クエリ用）
    LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)

    // 時間範囲内のイベントをロード
    LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)

    // 全イベントを購読（プロジェクション用）
    Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)

    // スナップショット管理
    SaveSnapshot(ctx context.Context, snapshot Snapshot) error
    LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)
    LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
```

## Event

```go
type Event struct {
    ID            string           // ユニークイベントID
    StreamID      string           // 集約ID
    Type          EventType        // イベントタイプ（例: "contract.created"）
    Version       int              // ストリーム内のバージョン
    SchemaVersion int              // アップキャスティング用
    Data          json.RawMessage  // イベントペイロード（JSON）
    Metadata      EventMetadata    // 監査情報
    OccurredAt    time.Time        // ビジネスタイムスタンプ
    RecordedAt    time.Time        // システムタイムスタンプ
}
```

## EventMetadata

```go
type EventMetadata struct {
    UserID    string  // 操作者（必須）
    IPAddress *string // クライアントIP（オプション）
    UserAgent *string // クライアントUA（オプション）
}
// 注: CorrelationID / CausationID は未活用のため Issue #116（Option B）で削除。
// 具体的な利用者が現れた段階で非破壊的に再導入する。
```

## Snapshot

```go
type Snapshot struct {
    StreamID  string          // 集約ID
    Version   int             // スナップショット時点のバージョン
    State     json.RawMessage // シリアライズされた集約状態
    AsOf      time.Time       // スナップショット時点
    CreatedAt time.Time       // スナップショット作成時刻
}
```

## DomainEventインターフェース

イベントはこのインターフェースを実装することでシリアライズ/デシリアライズ可能になります：

```go
type DomainEvent interface {
    EventType() EventType
}
```

## BaseAggregate

イベントソース集約の基本実装：

```go
type BaseAggregate struct { ... }

agg := eventstore.NewBaseAggregate(id, clock)

agg.ID() string
agg.Version() int
agg.SetVersion(v int)
agg.IncrementVersion()
agg.UncommittedEvents() []Event
agg.ClearUncommittedEvents()
agg.RaiseEvent(event DomainEvent, metadata EventMetadata) error
agg.Clock() Clock
```

## EventRegistry

イベントタイプとGo構造体のマッピング：

```go
registry := eventstore.NewEventRegistry()

registry.Register(event DomainEvent)
registry.Deserialize(eventType EventType, data json.RawMessage) (DomainEvent, error)
```

## Upcaster

イベントスキーマの進化に対応：

```go
type Upcaster interface {
    CanUpcast(eventType EventType, fromVersion int) bool
    Upcast(event Event) (Event, error)
}
```

## 契約イベントタイプ

| EventType | 定数 |
|-----------|------|
| `contract.created` | `EventTypeContractCreated` |
| `contract.activated` | `EventTypeContractActivated` |
| `contract.suspended` | `EventTypeContractSuspended` |
| `contract.resumed` | `EventTypeContractResumed` |
| `contract.cancelled` | `EventTypeContractCancelled` |
| `contract.renewed` | `EventTypeContractRenewed` |
| `contract.expired` | `EventTypeContractExpired` |
| `contract.price_changed` | `EventTypePriceChanged` |
| `contract.price_change_scheduled` | `EventTypePriceChangeScheduled` |
| `contract.price_change_unscheduled` | `EventTypePriceChangeUnscheduled` |
| `contract.trial_started` | `EventTypeTrialStarted` |
| `contract.trial_ended` | `EventTypeTrialEnded` |
| `contract.plan_changed` | `EventTypePlanChanged` |
| `contract.payment_method_changed` | `EventTypePaymentMethodChanged` |
| `contract.cancellation_scheduled` | `EventTypeCancellationScheduled` |
| `contract.cancellation_unscheduled` | `EventTypeCancellationUnscheduled` |

## インメモリ実装

テスト・デモ用：

```go
import "github.com/contract-to-cash/core/infrastructure/inmemory"

store := inmemory.NewInMemoryEventStore(clock)
```

スレッドセーフ。購読とスナップショットを含むStoreインターフェースの全メソッドをサポート。
