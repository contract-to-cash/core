# イベントソーシング設計

## 1. 概要

### 1.1 イベントソーシングとは

イベントソーシングは、アプリケーションの状態変更をイベントのシーケンスとして記録するアーキテクチャパターンです。

**従来のCRUD方式：**
```
現在の状態のみを保存
Contract: { status: "active", price: 10000 }
```

**イベントソーシング方式：**
```
すべての変更をイベントとして記録
1. ContractCreated { price: 5000 }
2. ContractActivated { activatedAt: "2024-01-01" }
3. PriceChanged { oldPrice: 5000, newPrice: 10000 }
→ 現在の状態はイベントを再生して導出
```

### 1.2 イベントソーシングの利点

| 利点 | 説明 |
|------|------|
| **完全な監査証跡** | いつ、何が起こったかを完全に記録 |
| **時点再構築** | 任意の過去時点の状態を再現可能 |
| **デバッグ容易性** | 問題発生時にイベントを追跡して原因特定 |
| **イベント駆動統合** | 他システムとの疎結合な連携が可能 |
| **Projection再構築** | 読み取りモデルを何度でも再構築可能 |

## 2. Event Store インターフェース

### 2.1 基本インターフェース

```go
// eventstore/store.go
package eventstore

import (
    "context"
    "time"
)

// Store イベントストアインターフェース
type Store interface {
    // イベントの永続化（楽観的ロック付き）
    Append(ctx context.Context, streamID string, events []Event, expectedVersion int) error
    
    // ストリームの全イベント取得
    Load(ctx context.Context, streamID string) ([]Event, error)
    
    // 特定バージョンまでのイベント取得
    LoadUntilVersion(ctx context.Context, streamID string, version int) ([]Event, error)
    
    // 特定時点までのイベント取得
    LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)
    
    // 期間指定でのイベント取得
    LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)
    
    // 全ストリームのイベント購読（Projection用）
    Subscribe(ctx context.Context, fromPosition int64) (<-chan Event, error)
    
    // スナップショット
    SaveSnapshot(ctx context.Context, snapshot Snapshot) error
    LoadSnapshot(ctx context.Context, streamID string) (*Snapshot, error)
    LoadSnapshotBefore(ctx context.Context, streamID string, before time.Time) (*Snapshot, error)
}
```

### 2.2 イベント構造

```go
// eventstore/event.go
package eventstore

import (
    "encoding/json"
    "time"
)

// EventType イベントタイプ定数
type EventType string

// DomainEvent 型付きドメインイベントインターフェース
// 各集約のイベント型がこのインターフェースを実装する
type DomainEvent interface {
    EventType() EventType
}

// Event 永続化されたイベント
type Event struct {
    ID            string          // イベント一意ID
    StreamID      string          // 集約ID
    Type          EventType       // イベントタイプ（型付き）
    Version       int             // ストリーム内のバージョン
    SchemaVersion int             // イベントスキーマバージョン（将来のupcaster用）
    Data          json.RawMessage // イベントデータ
    Metadata      EventMetadata   // メタデータ
    OccurredAt    time.Time       // イベント発生時刻（ビジネス時刻）
    RecordedAt    time.Time       // イベント記録時刻（システム時刻）
}

// EventMetadata イベントメタデータ
type EventMetadata struct {
    UserID      string  // 操作ユーザーID（必須）
    IPAddress   *string // IPアドレス（オプション）
    UserAgent   *string // User-Agent（オプション）
    CorrelationID string // 相関ID（トレーシング用）
    CausationID   string // 因果ID（イベントチェーン追跡用）
}
```

### 2.3 スナップショット構造

```go
// eventstore/snapshot.go
package eventstore

import (
    "encoding/json"
    "time"
)

// Snapshot 集約のスナップショット
type Snapshot struct {
    StreamID  string          // 集約ID
    Version   int             // スナップショット時点のバージョン
    State     json.RawMessage // 集約の状態
    AsOf      time.Time       // スナップショット時点
    CreatedAt time.Time       // スナップショット作成時刻
}
```

## 3. 集約ルート

### 3.1 基本構造

```go
// eventstore/aggregate.go
package eventstore

// AggregateRoot 集約ルートの基本インターフェース
type AggregateRoot interface {
    ID() string
    Version() int
    UncommittedEvents() []Event
    ClearUncommittedEvents()
    LoadFromHistory(events []Event) error
}

// Evolver 状態進化インターフェース（純粋関数）
type Evolver interface {
    // Apply はイベントを適用して新しい状態を返す（純粋関数）
    Apply(event Event) error
}
```

### 3.2 基本実装

```go
// eventstore/aggregate_base.go
package eventstore

// BaseAggregate 集約ルートの基本実装
type BaseAggregate struct {
    id                string
    version           int
    uncommittedEvents []Event
}

func (a *BaseAggregate) ID() string {
    return a.id
}

func (a *BaseAggregate) Version() int {
    return a.version
}

func (a *BaseAggregate) UncommittedEvents() []Event {
    return a.uncommittedEvents
}

func (a *BaseAggregate) ClearUncommittedEvents() {
    a.uncommittedEvents = nil
}

// RaiseEvent 型付きドメインイベントを発行
// 旧シグネチャ: RaiseEvent(eventType string, data interface{}, metadata EventMetadata)
// 新シグネチャ: DomainEvent から EventType() を取得するため、文字列指定が不要
func (a *BaseAggregate) RaiseEvent(domainEvent DomainEvent, metadata EventMetadata) error {
    jsonData, err := json.Marshal(domainEvent)
    if err != nil {
        return err
    }

    event := Event{
        ID:            GenerateID(),
        StreamID:      a.id,
        Type:          domainEvent.EventType(),
        Version:       a.version + len(a.uncommittedEvents) + 1,
        SchemaVersion: 1,
        Data:          jsonData,
        Metadata:      metadata,
        // OccurredAt はイベントの記録時刻（システム時刻）。
        // ビジネス上の時刻（CreatedAt, ActivatedAt 等）は集約が Clock IF 経由で設定し、
        // Data フィールド（DomainEvent）内に含める。
        // RecordedAt は Event Store 側で設定されるため、ここでは OccurredAt のみ設定。
        OccurredAt:    time.Now().UTC(),
    }

    a.uncommittedEvents = append(a.uncommittedEvents, event)
    return nil
}

func (a *BaseAggregate) IncrementVersion() {
    a.version++
}
```

### 3.3 イベントレジストリ

```go
// eventstore/event_registry.go
package eventstore

import (
    "encoding/json"
    "fmt"
    "reflect"
)

// EventRegistry イベントの型情報を管理
// イベントストアからの復元時に EventType → Go型 のマッピングを提供する
type EventRegistry struct {
    types map[EventType]reflect.Type
}

func NewEventRegistry() *EventRegistry {
    return &EventRegistry{types: make(map[EventType]reflect.Type)}
}

// Register イベント型を登録
func (r *EventRegistry) Register(event DomainEvent) {
    r.types[event.EventType()] = reflect.TypeOf(event)
}

// Deserialize イベントデータから型付きイベントを復元
func (r *EventRegistry) Deserialize(eventType EventType, data json.RawMessage) (DomainEvent, error) {
    t, ok := r.types[eventType]
    if !ok {
        return nil, fmt.Errorf("unknown event type: %s", eventType)
    }
    event := reflect.New(t).Interface().(DomainEvent)
    if err := json.Unmarshal(data, event); err != nil {
        return nil, err
    }
    return event, nil
}
```

## 4. 契約集約の実装例

### 4.1 契約集約

```go
// domain/contract/aggregate.go
package contract

import (
    "fmt"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
)

// ContractAggregate 契約集約
type ContractAggregate struct {
    eventstore.BaseAggregate
    clock shared.Clock // 時刻生成（テスト時に差し替え可能）

    // 状態
    accountID     string
    status        ContractStatus
    planID        string
    price         Money
    billingCycle  BillingCycle
    currentPeriod DateRange
    createdAt     time.Time
    updatedAt     time.Time
}

// NewContractAggregate 新しい契約集約を作成
func NewContractAggregate(id string, clock shared.Clock) *ContractAggregate {
    return &ContractAggregate{
        BaseAggregate: eventstore.BaseAggregate{id: id},
        clock:         clock,
    }
}

// Create 契約を作成
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error {
    if a.status != "" {
        return shared.NewDomainError(shared.ErrCodeConflict, "contract already exists")
    }

    now := a.clock.Now()
    event := &ContractCreatedEvent{
        ContractID:   a.ID(),
        AccountID:    cmd.AccountID,
        PlanID:       cmd.PlanID,
        Price:        cmd.Price,
        BillingCycle: cmd.BillingCycle,
        CreatedAt:    now,
    }

    return a.RaiseEvent(event, metadata)
}

// Activate 契約を有効化
func (a *ContractAggregate) Activate(metadata eventstore.EventMetadata) error {
    if a.status != ContractStatusDraft && a.status != ContractStatusTrialing {
        return shared.NewDomainError(shared.ErrCodeInvalidStateTransition, "contract cannot be activated")
    }

    event := &ContractActivatedEvent{
        ContractID:  a.ID(),
        ActivatedAt: a.clock.Now(),
    }

    return a.RaiseEvent(event, metadata)
}

// Suspend 契約を一時停止
func (a *ContractAggregate) Suspend(config SuspensionConfiguration, metadata eventstore.EventMetadata) error {
    if a.status != ContractStatusActive {
        return shared.NewDomainError(shared.ErrCodeInvalidStateTransition, "only active contracts can be suspended")
    }

    event := &ContractSuspendedEvent{
        ContractID:      a.ID(),
        SuspendedAt:     a.clock.Now(),
        BillingBehavior: config.BillingBehavior,
        ResumeDate:      config.ResumeDate,
        Reason:          config.Reason,
    }

    return a.RaiseEvent(event, metadata)
}

// LoadFromHistory イベント履歴から状態を復元
// EventRegistry を使って型付きイベントにデシリアライズしてから Apply に渡す
func (a *ContractAggregate) LoadFromHistory(events []eventstore.Event) error {
    for _, event := range events {
        if err := a.Apply(event); err != nil {
            return err
        }
        a.IncrementVersion()
    }
    return nil
}

// Apply 型付きイベントを適用して状態を更新
// 文字列switchではなく型スイッチを使用し、コンパイル時の安全性を確保する
func (a *ContractAggregate) Apply(event eventstore.Event) error {
    // EventRegistry 経由でデシリアライズ済みの DomainEvent を受け取る想定
    // ここでは Event.Data からの復元も併せて示す
    domainEvent, err := contractEventRegistry.Deserialize(event.Type, event.Data)
    if err != nil {
        return err
    }

    switch e := domainEvent.(type) {
    case *ContractCreatedEvent:
        a.accountID = e.AccountID
        a.planID = e.PlanID
        a.price = e.Price
        a.billingCycle = e.BillingCycle
        a.status = ContractStatusDraft
        a.createdAt = e.CreatedAt
        a.updatedAt = e.CreatedAt

    case *ContractActivatedEvent:
        a.status = ContractStatusActive
        a.updatedAt = e.ActivatedAt

    case *ContractSuspendedEvent:
        a.status = ContractStatusSuspended
        a.updatedAt = e.SuspendedAt

    // 型スイッチにより、新しいイベント型の追加忘れは
    // exhaustive lint ツールで検出可能
    default:
        return shared.NewDomainError(
            shared.ErrCodeUnknownEvent,
            fmt.Sprintf("unknown event type: %T", domainEvent),
        )
    }

    return nil
}

// contractEventRegistry 契約集約のイベントレジストリ
var contractEventRegistry = func() *eventstore.EventRegistry {
    r := eventstore.NewEventRegistry()
    r.Register(ContractCreatedEvent{})
    r.Register(ContractActivatedEvent{})
    r.Register(ContractSuspendedEvent{})
    r.Register(ContractResumedEvent{})
    r.Register(ContractCancelledEvent{})
    r.Register(PriceChangedEvent{})
    r.Register(PlanChangedEvent{})
    r.Register(TrialStartedEvent{})
    r.Register(TrialEndedEvent{})
    return r
}()
```

### 4.2 ドメインイベント定義

```go
// domain/contract/events.go
package contract

import (
    "time"

    "github.com/contract-to-cash/core/eventstore"
)

// ============================================================
// EventType 定数
// ============================================================

const (
    EventTypeContractCreated   eventstore.EventType = "contract.created"
    EventTypeContractActivated eventstore.EventType = "contract.activated"
    EventTypeContractSuspended eventstore.EventType = "contract.suspended"
    EventTypeContractResumed   eventstore.EventType = "contract.resumed"
    EventTypeContractCancelled eventstore.EventType = "contract.cancelled"
    EventTypePriceChanged      eventstore.EventType = "contract.price_changed"
    EventTypePlanChanged       eventstore.EventType = "contract.plan_changed"
    EventTypeTrialStarted      eventstore.EventType = "contract.trial_started"
    EventTypeTrialEnded        eventstore.EventType = "contract.trial_ended"
)

// ============================================================
// イベント構造体（全て DomainEvent IF を実装）
// ============================================================

// ContractCreatedEvent 契約作成イベント
type ContractCreatedEvent struct {
    ContractID   string       `json:"contract_id"`
    AccountID    string       `json:"account_id"`
    PlanID       string       `json:"plan_id"`
    Price        Money        `json:"price"`
    BillingCycle BillingCycle `json:"billing_cycle"`
    CreatedAt    time.Time    `json:"created_at"`
}

func (e ContractCreatedEvent) EventType() eventstore.EventType { return EventTypeContractCreated }

// ContractActivatedEvent 契約有効化イベント
type ContractActivatedEvent struct {
    ContractID  string    `json:"contract_id"`
    ActivatedAt time.Time `json:"activated_at"`
}

func (e ContractActivatedEvent) EventType() eventstore.EventType { return EventTypeContractActivated }

// ContractSuspendedEvent 契約一時停止イベント
type ContractSuspendedEvent struct {
    ContractID      string                    `json:"contract_id"`
    SuspendedAt     time.Time                 `json:"suspended_at"`
    BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
    ResumeDate      *time.Time                `json:"resume_date,omitempty"`
    Reason          string                    `json:"reason"`
}

func (e ContractSuspendedEvent) EventType() eventstore.EventType { return EventTypeContractSuspended }

// ContractResumedEvent 契約再開イベント
type ContractResumedEvent struct {
    ContractID string    `json:"contract_id"`
    ResumedAt  time.Time `json:"resumed_at"`
}

func (e ContractResumedEvent) EventType() eventstore.EventType { return EventTypeContractResumed }

// ContractCancelledEvent 契約解約イベント
type ContractCancelledEvent struct {
    ContractID  string    `json:"contract_id"`
    CancelledAt time.Time `json:"cancelled_at"`
    Reason      string    `json:"reason"`
}

func (e ContractCancelledEvent) EventType() eventstore.EventType { return EventTypeContractCancelled }

// PriceChangedEvent 価格変更イベント
type PriceChangedEvent struct {
    ContractID  string    `json:"contract_id"`
    OldPrice    Money     `json:"old_price"`
    NewPrice    Money     `json:"new_price"`
    ChangedAt   time.Time `json:"changed_at"`
    EffectiveAt time.Time `json:"effective_at"`
}

func (e PriceChangedEvent) EventType() eventstore.EventType { return EventTypePriceChanged }

// PlanChangedEvent プラン変更イベント
type PlanChangedEvent struct {
    ContractID   string            `json:"contract_id"`
    OldPlanID    string            `json:"old_plan_id"`
    NewPlanID    string            `json:"new_plan_id"`
    Proration    *ProrationResult  `json:"proration,omitempty"`
    ChangedAt    time.Time         `json:"changed_at"`
}

func (e PlanChangedEvent) EventType() eventstore.EventType { return EventTypePlanChanged }

// TrialStartedEvent トライアル開始イベント
type TrialStartedEvent struct {
    ContractID   string             `json:"contract_id"`
    TrialConfig  TrialConfiguration `json:"trial_config"`
    StartedAt    time.Time          `json:"started_at"`
}

func (e TrialStartedEvent) EventType() eventstore.EventType { return EventTypeTrialStarted }

// TrialEndedEvent トライアル終了イベント
type TrialEndedEvent struct {
    ContractID string    `json:"contract_id"`
    EndedAt    time.Time `json:"ended_at"`
    Converted  bool      `json:"converted"` // 本契約に移行したか
}

func (e TrialEndedEvent) EventType() eventstore.EventType { return EventTypeTrialEnded }
```

## 5. Clock インターフェース

テスト容易性のため、全ての時刻生成を `Clock` インターフェース経由で行う。
`time.Now()` の直接呼び出しはドメイン層・アプリケーション層では禁止する。

```go
// domain/shared/clock.go
package shared

import "time"

// Clock 時刻生成インターフェース
type Clock interface {
    Now() time.Time
}

// SystemClock 本番用（実時刻）
type SystemClock struct{}

func (c SystemClock) Now() time.Time {
    return time.Now().UTC()
}

// FixedClock テスト用（固定時刻）
type FixedClock struct {
    FixedTime time.Time
}

func (c FixedClock) Now() time.Time {
    return c.FixedTime
}
```

**使用箇所**: 集約（ContractAggregate等）、アプリケーションサービス（SnapshotService等）、
Webhook処理（タイムスタンプ検証）で DI により注入する。

---

## 6. 時点再構築（Temporal Query）

### 5.1 時点指定クエリサービス

```go
// application/query/temporal_query_service.go
package query

import (
    "context"
    "time"
    
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
)

// TemporalQueryService 時点指定クエリサービス
type TemporalQueryService struct {
    eventStore eventstore.Store
    clock      shared.Clock
}

func NewTemporalQueryService(store eventstore.Store, clock shared.Clock) *TemporalQueryService {
    return &TemporalQueryService{eventStore: store, clock: clock}
}

// GetContractAsOf 指定時点の契約状態を取得
func (s *TemporalQueryService) GetContractAsOf(
    ctx context.Context,
    contractID string,
    asOf time.Time,
) (*contract.ContractAggregate, error) {
    // スナップショットの読み込み（指定時点より前の最新）
    snapshot, err := s.eventStore.LoadSnapshotBefore(ctx, contractID, asOf)
    
    agg := contract.NewContractAggregate(contractID, s.clock)
    
    var events []eventstore.Event
    if snapshot != nil {
        // スナップショットから状態を復元
        if err := agg.LoadFromSnapshot(snapshot); err != nil {
            return nil, err
        }
        // スナップショット以降のイベントを取得
        events, err = s.eventStore.LoadRange(ctx, contractID, snapshot.AsOf, asOf)
    } else {
        // 全イベントを取得
        events, err = s.eventStore.LoadUntil(ctx, contractID, asOf)
    }
    
    if err != nil {
        return nil, err
    }
    
    // イベントを適用
    if err := agg.LoadFromHistory(events); err != nil {
        return nil, err
    }
    
    return agg, nil
}

// GetContractHistory 契約の変更履歴を取得
func (s *TemporalQueryService) GetContractHistory(
    ctx context.Context,
    contractID string,
) ([]ContractHistoryEntry, error) {
    events, err := s.eventStore.Load(ctx, contractID)
    if err != nil {
        return nil, err
    }
    
    var history []ContractHistoryEntry
    for _, event := range events {
        history = append(history, ContractHistoryEntry{
            EventType:  event.Type,
            OccurredAt: event.OccurredAt,
            UserID:     event.Metadata.UserID,
            Data:       event.Data,
        })
    }
    
    return history, nil
}

// CompareStates 2つの時点の状態を比較
func (s *TemporalQueryService) CompareStates(
    ctx context.Context,
    contractID string,
    time1, time2 time.Time,
) (*StateDiff, error) {
    state1, err := s.GetContractAsOf(ctx, contractID, time1)
    if err != nil {
        return nil, err
    }
    
    state2, err := s.GetContractAsOf(ctx, contractID, time2)
    if err != nil {
        return nil, err
    }
    
    return ComputeDiff(state1, state2), nil
}

type ContractHistoryEntry struct {
    EventType  eventstore.EventType
    OccurredAt time.Time
    UserID     string
    Data       json.RawMessage
}

type StateDiff struct {
    Changes []FieldChange
}

type FieldChange struct {
    Field    string
    OldValue interface{}
    NewValue interface{}
}
```

## 7. Projection（読み取りモデル）

### 7.1 Projection更新サービス

```go
// application/projection/service.go
package projection

import (
    "context"
    "time"
    
    "github.com/contract-to-cash/core/eventstore"
)

// ProjectionService Projection更新サービス
type ProjectionService struct {
    eventStore eventstore.Store
    projectors []Projector
    options    ProjectionOptions
}

type ProjectionOptions struct {
    SyncMode    bool          // true: 同期更新, false: 非同期更新
    BatchSize   int           // 非同期時のバッチサイズ
    MaxRetries  int           // リトライ回数
    RetryDelay  time.Duration // リトライ間隔
}

// Projector Projection更新インターフェース
type Projector interface {
    // Project イベントをProjectionに反映
    Project(ctx context.Context, event eventstore.Event) error
    
    // Rebuild 指定時点までのProjectionを再構築
    Rebuild(ctx context.Context, until time.Time) error
}

// Start Projection更新を開始（非同期モード）
func (s *ProjectionService) Start(ctx context.Context) error {
    if s.options.SyncMode {
        return nil // 同期モードでは不要
    }
    
    events, err := s.eventStore.Subscribe(ctx, 0)
    if err != nil {
        return err
    }
    
    go func() {
        for event := range events {
            for _, projector := range s.projectors {
                if err := projector.Project(ctx, event); err != nil {
                    // エラーハンドリング（リトライ等）
                }
            }
        }
    }()
    
    return nil
}

// RebuildAll 全Projectionを再構築
func (s *ProjectionService) RebuildAll(ctx context.Context, until time.Time) error {
    for _, projector := range s.projectors {
        if err := projector.Rebuild(ctx, until); err != nil {
            return err
        }
    }
    return nil
}
```

### 7.2 契約Projectionの実装例

```go
// infrastructure/projection/contract_projector.go
package projection

import (
    "context"
    "encoding/json"
    
    "github.com/contract-to-cash/core/eventstore"
)

// ContractProjector 契約のProjection更新
type ContractProjector struct {
    db *sql.DB
}

func (p *ContractProjector) Project(ctx context.Context, event eventstore.Event) error {
    switch event.Type {
    case contract.EventTypeContractCreated:
        return p.handleContractCreated(ctx, event)
    case contract.EventTypeContractActivated:
        return p.handleContractActivated(ctx, event)
    case contract.EventTypeContractSuspended:
        return p.handleContractSuspended(ctx, event)
    // 他のイベントタイプも EventType 定数で参照
    }
    return nil
}

func (p *ContractProjector) handleContractCreated(ctx context.Context, event eventstore.Event) error {
    var e contract.ContractCreatedEvent
    if err := json.Unmarshal(event.Data, &e); err != nil {
        return err
    }
    
    _, err := p.db.ExecContext(ctx, `
        INSERT INTO contracts_projection 
        (id, account_id, plan_id, status, price_amount, price_currency, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
    `, e.ContractID, e.AccountID, e.PlanID, "draft", 
       e.Price.Amount, e.Price.Currency, e.CreatedAt)
    
    return err
}

func (p *ContractProjector) Rebuild(ctx context.Context, until time.Time) error {
    // 既存のProjectionをクリア
    _, err := p.db.ExecContext(ctx, "TRUNCATE contracts_projection")
    if err != nil {
        return err
    }
    
    // イベントを順番に再生
    // ...
    return nil
}
```

## 8. データベーススキーマ

### 8.1 Event Store テーブル

```sql
-- イベントテーブル
CREATE TABLE events (
    id UUID PRIMARY KEY,
    stream_id VARCHAR(255) NOT NULL,
    type VARCHAR(255) NOT NULL,
    version INT NOT NULL,
    schema_version INT NOT NULL DEFAULT 1,
    data JSONB NOT NULL,
    metadata JSONB NOT NULL,
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
    recorded_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- ストリーム内でのバージョン一意性を保証（楽観的ロック）
    UNIQUE (stream_id, version)
);

-- インデックス
CREATE INDEX idx_events_stream_id ON events(stream_id);
CREATE INDEX idx_events_stream_id_version ON events(stream_id, version);
CREATE INDEX idx_events_occurred_at ON events(occurred_at);
CREATE INDEX idx_events_recorded_at ON events(recorded_at);
CREATE INDEX idx_events_type ON events(type);

-- スナップショットテーブル
CREATE TABLE snapshots (
    id UUID PRIMARY KEY,
    stream_id VARCHAR(255) NOT NULL,
    version INT NOT NULL,
    state JSONB NOT NULL,
    as_of TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE (stream_id, version)
);

CREATE INDEX idx_snapshots_stream_id ON snapshots(stream_id);
CREATE INDEX idx_snapshots_stream_id_as_of ON snapshots(stream_id, as_of);
```

### 8.2 Projectionテーブル例

```sql
-- 契約Projectionテーブル
CREATE TABLE contracts_projection (
    id VARCHAR(255) PRIMARY KEY,
    account_id VARCHAR(255) NOT NULL,
    plan_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,
    contract_type VARCHAR(50) NOT NULL,
    price_amount DECIMAL(15, 4) NOT NULL,
    price_currency VARCHAR(3) NOT NULL,
    billing_cycle VARCHAR(20) NOT NULL,
    current_period_start TIMESTAMP WITH TIME ZONE,
    current_period_end TIMESTAMP WITH TIME ZONE,
    trial_end_date TIMESTAMP WITH TIME ZONE,
    suspended_at TIMESTAMP WITH TIME ZONE,
    cancelled_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX idx_contracts_proj_account ON contracts_projection(account_id);
CREATE INDEX idx_contracts_proj_status ON contracts_projection(status);
CREATE INDEX idx_contracts_proj_plan ON contracts_projection(plan_id);
```

## 9. スナップショット戦略

### 9.1 スナップショット作成タイミング

| 戦略 | 説明 | 適用ケース |
|------|------|-----------|
| **N件ごと** | イベントがN件蓄積するたびに作成 | 標準的なケース |
| **時間ベース** | 一定時間ごとに作成 | 長期運用 |
| **オンデマンド** | 明示的な要求時のみ作成 | リソース節約 |

### 9.2 実装例

```go
// application/service/snapshot_service.go
package service

const DefaultSnapshotInterval = 100 // 100イベントごとにスナップショット

type SnapshotService struct {
    eventStore eventstore.Store
    clock      shared.Clock
    interval   int
}

func (s *SnapshotService) ShouldCreateSnapshot(currentVersion int) bool {
    return currentVersion > 0 && currentVersion%s.interval == 0
}

func (s *SnapshotService) CreateSnapshot(ctx context.Context, agg eventstore.AggregateRoot) error {
    state, err := json.Marshal(agg)
    if err != nil {
        return err
    }

    now := s.clock.Now()
    snapshot := eventstore.Snapshot{
        StreamID:  agg.ID(),
        Version:   agg.Version(),
        State:     state,
        AsOf:      now,
        CreatedAt: now,
    }

    return s.eventStore.SaveSnapshot(ctx, snapshot)
}
```

## 10. イベントバージョニング

### 10.1 スキーマバージョン管理

```go
// イベントにSchemaVersionフィールドを含める
type Event struct {
    // ...
    SchemaVersion int // 1, 2, 3, ...
    // ...
}
```

### 10.2 Upcaster（将来の拡張用）

```go
// eventstore/upcaster.go
package eventstore

// Upcaster 古いイベントを新しいスキーマに変換
type Upcaster interface {
    // CanUpcast このUpcasterが処理可能か判定
    CanUpcast(eventType EventType, fromVersion int) bool

    // Upcast イベントを新しいバージョンに変換
    Upcast(event Event) (Event, error)
}

// UpcasterChain 複数のUpcasterをチェーン
type UpcasterChain struct {
    upcasters []Upcaster
}

func (c *UpcasterChain) Upcast(event Event) (Event, error) {
    current := event
    for _, u := range c.upcasters {
        if u.CanUpcast(current.Type, current.SchemaVersion) {
            var err error
            current, err = u.Upcast(current)
            if err != nil {
                return event, err
            }
        }
    }
    return current, nil
}
```
