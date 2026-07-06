---
sidebar_label: Event Sourcing
---

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
    // 時点再構築用: OccurredAt（ビジネス時刻）ベースでフィルタする。
    // RecordedAt（システム記録時刻）ではないことに注意。
    LoadUntil(ctx context.Context, streamID string, until time.Time) ([]Event, error)

    // 期間指定でのイベント取得
    // OccurredAt ベースで from <= OccurredAt <= to のイベントを返す。
    LoadRange(ctx context.Context, streamID string, from, to time.Time) ([]Event, error)
    
    // ストリーム横断の全イベント取得（Projection Rebuild用）
    // fromPosition は排他的（この位置より後のイベントを返す）。limit <= 0 は無制限。
    LoadAll(ctx context.Context, fromPosition int64, limit int) ([]Event, error)

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
    ID             string          // イベント一意ID
    StreamID       string          // 集約ID
    Type           EventType       // イベントタイプ（型付き）
    Version        int             // ストリーム内のバージョン
    SchemaVersion  int             // イベントスキーマバージョン（将来のupcaster用）
    Data           json.RawMessage // イベントデータ
    Metadata       EventMetadata   // メタデータ
    OccurredAt     time.Time       // イベント発生時刻（ビジネス時刻）
    RecordedAt     time.Time       // イベント記録時刻（システム時刻）
    GlobalPosition int64           // ストリーム横断のグローバル位置（Projection Rebuild用）
}

// EventMetadata イベントメタデータ
type EventMetadata struct {
    UserID      string  // 操作ユーザーID（必須）
    IPAddress   *string // IPアドレス（オプション）
    UserAgent   *string // User-Agent（オプション）
}
// 注: CorrelationID / CausationID は未活用のため Issue #116（Option B）で削除。
// 具体的な利用者が現れた段階で非破壊的に再導入する。
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
    LoadFromSnapshot(snapshot Snapshot) error // スナップショットから状態を復元
}

// Evolver 状態進化インターフェース（純粋関数）
// Apply は型付き DomainEvent を受け取り、集約の状態を更新する。
// デシリアライズ（json.RawMessage → DomainEvent）は呼び出し元
// （LoadFromHistory やリポジトリ層）の責務であり、Apply 内では行わない。
type Evolver interface {
    Apply(event DomainEvent) error
}
```

### 3.2 基本実装

```go
// eventstore/aggregate_base.go
package eventstore

import (
    "encoding/json"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// BaseAggregate 集約ルートの基本実装
// フィールドは unexported（カプセル化）。外部パッケージからは
// NewBaseAggregate() コンストラクタ経由で生成する。
type BaseAggregate struct {
    id                string
    version           int
    uncommittedEvents []Event
    clock             shared.Clock
}

// NewBaseAggregate 外部パッケージから BaseAggregate を生成するコンストラクタ
// id: 集約ID, clock: 時刻生成（テスト時に FixedClock を注入可能）
func NewBaseAggregate(id string, clock shared.Clock) BaseAggregate {
    return BaseAggregate{
        id:    id,
        clock: clock,
    }
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

// SchemaVersioned イベントが現行ペイロードのスキーマバージョンを自己申告する
// オプショナルIF。RaiseEvent はこれを実装するイベントを申告値で刻み、未実装なら 1。
type SchemaVersioned interface {
    CurrentSchemaVersion() int
}

// RaiseEvent 型付きドメインイベントを発行
// DomainEvent から EventType() を取得するため、文字列指定が不要
func (a *BaseAggregate) RaiseEvent(domainEvent DomainEvent, metadata EventMetadata) error {
    jsonData, err := json.Marshal(domainEvent)
    if err != nil {
        return err
    }

    // 現行ペイロードが v1 から進化しているイベント（対応する Upcaster を持つ）は
    // SchemaVersioned を実装して真のバージョンを刻む。これにより新規イベントは
    // リプレイ時に Upcaster をスキップ（CanUpcast が false）し、非冪等な Upcaster を
    // 将来導入しても書きたてのイベントが破損しない（issue #153）。未実装なら 1。
    schemaVersion := 1
    if sv, ok := domainEvent.(SchemaVersioned); ok {
        schemaVersion = sv.CurrentSchemaVersion()
    }

    event := Event{
        ID:            GenerateID(),
        StreamID:      a.id,
        Type:          domainEvent.EventType(),
        Version:       a.version + len(a.uncommittedEvents) + 1,
        SchemaVersion: schemaVersion,
        Data:          jsonData,
        Metadata:      metadata,
        // OccurredAt はイベントの記録時刻（システム時刻）。
        // ビジネス上の時刻（CreatedAt, ActivatedAt 等）は集約が Clock IF 経由で設定し、
        // Data フィールド（DomainEvent）内に含める。
        // RecordedAt は Event Store 側で設定されるため、ここでは OccurredAt のみ設定。
        // Clock IF 経由で取得（time.Now() の直接呼び出しは禁止）。
        OccurredAt:    a.Clock().Now(),
    }

    a.uncommittedEvents = append(a.uncommittedEvents, event)
    return nil
}

// Clock Clock IF を返す（埋め込み先の集約がビジネス時刻取得に使用）
func (a *BaseAggregate) Clock() shared.Clock {
    return a.clock
}

func (a *BaseAggregate) IncrementVersion() {
    a.version++
}

// SetVersion スナップショット復元時にバージョンを直接設定する
func (a *BaseAggregate) SetVersion(v int) {
    a.version = v
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
// 同一 EventType が既に登録済みの場合はエラーを返す（黙って上書きしない）。
// 重複登録はプログラマエラー（別の Go 型が同じ EventType を主張、または二重配線）で、
// 上書きするとデシリアライズが誤った型にルーティングされ得る（plugin.Registry.Register と同方針、issue #153）。
func (r *EventRegistry) Register(event DomainEvent) error {
    if _, exists := r.types[event.EventType()]; exists {
        return fmt.Errorf("event type %q already registered", event.EventType())
    }
    r.types[event.EventType()] = reflect.TypeOf(event)
    return nil
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
    // clock は BaseAggregate に統合済み（RaiseEvent で使用）。
    // 集約固有のビジネス時刻取得にも BaseAggregate 経由の Clock() を使用する。

    // 状態
    accountID     shared.AccountID
    status        ContractStatus
    price         Money
    interval      BillingInterval
    currentPeriod DateRange
    createdAt     time.Time
    updatedAt     time.Time
}

// NewContractAggregate 新しい契約集約を作成
// clock は BaseAggregate に渡され、RaiseEvent の OccurredAt 設定と
// 集約内のビジネス時刻取得の両方に使用される
func NewContractAggregate(id string, clock shared.Clock) *ContractAggregate {
    return &ContractAggregate{
        BaseAggregate: eventstore.NewBaseAggregate(id, clock),
    }
}

// Create 契約を作成
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error {
    if a.status != "" {
        return shared.NewDomainError(shared.ErrCodeConflict, "contract already exists")
    }

    now := a.Clock().Now()
    event := &ContractCreatedEvent{
        ContractID:   a.ID(),
        AccountID:    cmd.AccountID,
        PriceID:      cmd.PriceID,
        Price:        cmd.Price,
        Interval:     cmd.Interval,
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
        ActivatedAt: a.Clock().Now(),
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
        SuspendedAt:     a.Clock().Now(),
        BillingBehavior: config.BillingBehavior,
        ResumeDate:      config.ResumeDate,
        Reason:          config.Reason,
    }

    return a.RaiseEvent(event, metadata)
}

// LoadFromHistory イベント履歴から状態を復元
// デシリアライズ（Event.Data → DomainEvent）は LoadFromHistory の責務。
// Apply には型付き DomainEvent のみを渡す（Apply は純粋関数）。
func (a *ContractAggregate) LoadFromHistory(events []eventstore.Event) error {
    for _, event := range events {
        domainEvent, err := contractEventRegistry.Deserialize(event.Type, event.Data)
        if err != nil {
            return err
        }
        if err := a.Apply(domainEvent); err != nil {
            return err
        }
        a.IncrementVersion()
    }
    return nil
}

// LoadFromSnapshot スナップショットから集約状態を復元
func (a *ContractAggregate) LoadFromSnapshot(snapshot eventstore.Snapshot) error {
    // スナップショットの JSON データから状態を復元
    if err := json.Unmarshal(snapshot.State, a); err != nil {
        return err
    }
    // BaseAggregate のバージョンをスナップショット時点に直接設定
    a.SetVersion(snapshot.Version)
    return nil
}

// Apply 型付き DomainEvent を適用して状態を更新（純粋関数）
// デシリアライズ済みの DomainEvent を受け取る。
// 型スイッチにより、新しいイベント型の追加忘れは exhaustive lint ツール
// (github.com/nishanths/exhaustive) で検出可能。
// default clause は未知のイベント型に対する防御として保持する。
func (a *ContractAggregate) Apply(event eventstore.DomainEvent) error {
    switch e := event.(type) {
    case *ContractCreatedEvent:
        a.accountID = e.AccountID
        a.price = e.Price
        a.interval = e.Interval
        a.status = ContractStatusDraft
        a.createdAt = e.CreatedAt
        a.updatedAt = e.CreatedAt

    case *ContractActivatedEvent:
        a.status = ContractStatusActive
        a.updatedAt = e.ActivatedAt

    case *ContractSuspendedEvent:
        a.status = ContractStatusSuspended
        a.updatedAt = e.SuspendedAt

    default:
        return shared.NewDomainError(
            shared.ErrCodeUnknownEvent,
            fmt.Sprintf("unknown event type: %T", event),
        )
    }

    return nil
}

// contractEventRegistry 契約集約のイベントレジストリ
// 重要: 新しいイベント型を追加する場合、以下の2箇所を必ず更新すること:
//   1. このレジストリに Register() で型を登録
//   2. ContractAggregate.Apply() の switch case に対応を追加
// exhaustive lint ツールで Apply 側の漏れは検出可能だが、Register 漏れは検出できない。
var contractEventRegistry = func() *eventstore.EventRegistry {
    r := eventstore.NewEventRegistry()
    // Register はエラーを返す（重複登録を拒否）。パッケージ初期化時の重複は
    // プログラマエラーなので mustRegister で panic して fail-fast する。
    mustRegister := func(e eventstore.DomainEvent) {
        if err := r.Register(e); err != nil {
            panic(fmt.Sprintf("contract event registry: %v", err))
        }
    }
    mustRegister(&ContractCreatedEvent{})
    mustRegister(&ContractActivatedEvent{})
    mustRegister(&ContractSuspendedEvent{})
    mustRegister(&ContractResumedEvent{})
    mustRegister(&ContractCancelledEvent{})
    mustRegister(&PriceChangedEvent{})
    mustRegister(&TrialStartedEvent{})
    mustRegister(&TrialEndedEvent{})
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
    EventTypeTrialStarted      eventstore.EventType = "contract.trial_started"
    EventTypeTrialEnded        eventstore.EventType = "contract.trial_ended"
)

// ============================================================
// イベント構造体（全て DomainEvent IF を実装）
// ============================================================

// ContractCreatedEvent 契約作成イベント
type ContractCreatedEvent struct {
    ContractID   shared.ContractID `json:"contract_id"`
    AccountID    shared.AccountID  `json:"account_id"`
    PriceID      shared.PriceID    `json:"price_id"`
    Price        Money             `json:"price"`
    Interval     BillingInterval   `json:"interval,omitempty"`
    CreatedAt    time.Time         `json:"created_at"`
}

func (e ContractCreatedEvent) EventType() eventstore.EventType { return EventTypeContractCreated }

// ContractActivatedEvent 契約有効化イベント
type ContractActivatedEvent struct {
    ContractID  shared.ContractID `json:"contract_id"`
    ActivatedAt time.Time `json:"activated_at"`
}

func (e ContractActivatedEvent) EventType() eventstore.EventType { return EventTypeContractActivated }

// ContractSuspendedEvent 契約一時停止イベント
type ContractSuspendedEvent struct {
    ContractID      shared.ContractID         `json:"contract_id"`
    SuspendedAt     time.Time                 `json:"suspended_at"`
    BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
    ResumeDate      *time.Time                `json:"resume_date,omitempty"`
    Reason          string                    `json:"reason"`
}

func (e ContractSuspendedEvent) EventType() eventstore.EventType { return EventTypeContractSuspended }

// ContractResumedEvent 契約再開イベント
type ContractResumedEvent struct {
    ContractID shared.ContractID `json:"contract_id"`
    ResumedAt  time.Time         `json:"resumed_at"`
}

func (e ContractResumedEvent) EventType() eventstore.EventType { return EventTypeContractResumed }

// ContractCancelledEvent 契約解約イベント
type ContractCancelledEvent struct {
    ContractID  shared.ContractID `json:"contract_id"`
    CancelledAt time.Time         `json:"cancelled_at"`
    Reason      string            `json:"reason"`
}

func (e ContractCancelledEvent) EventType() eventstore.EventType { return EventTypeContractCancelled }

// PriceChangedEvent 価格変更イベント
type PriceChangedEvent struct {
    ContractID  shared.ContractID `json:"contract_id"`
    OldPrice    Money             `json:"old_price"`
    NewPrice    Money             `json:"new_price"`
    ChangedAt   time.Time         `json:"changed_at"`
    EffectiveAt time.Time         `json:"effective_at"`
}

func (e PriceChangedEvent) EventType() eventstore.EventType { return EventTypePriceChanged }

// TrialStartedEvent トライアル開始イベント
type TrialStartedEvent struct {
    ContractID   shared.ContractID  `json:"contract_id"`
    TrialConfig  TrialConfiguration `json:"trial_config"`
    StartedAt    time.Time          `json:"started_at"`
}

func (e TrialStartedEvent) EventType() eventstore.EventType { return EventTypeTrialStarted }

// TrialEndedEvent トライアル終了イベント
type TrialEndedEvent struct {
    ContractID    shared.ContractID `json:"contract_id"`
    EndedAt       time.Time         `json:"ended_at"`
    Converted     bool              `json:"converted"`      // 本契約に移行したか
    CurrentPeriod shared.DateRange  `json:"current_period"` // 転換時の初期課金期間（SchemaVersion 2 で追加、issue #146）
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
// LoadAll でストリーム横断のイベントをグローバル位置順に取得し、
// 全登録 Projector に ProcessEvent で配信する。
// Projector.Rebuild（各Projector固有の再構築）とは異なり、
// クロスストリームの順序保証が必要な場合に使用する。
// 呼び出し元は事前に既存の Projection データをクリアすること。
func (s *ProjectionService) RebuildAll(ctx context.Context) error {
    batchSize := s.options.BatchSize
    if batchSize <= 0 {
        batchSize = 1000
    }
    var fromPosition int64
    for {
        events, err := s.eventStore.LoadAll(ctx, fromPosition, batchSize)
        if err != nil {
            return err
        }
        if len(events) == 0 {
            break
        }
        for _, event := range events {
            if err := s.ProcessEvent(ctx, event); err != nil {
                return err
            }
        }
        fromPosition = events[len(events)-1].GlobalPosition
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
        (id, account_id, status, price_amount, price_currency, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $6)
    `, e.ContractID, e.AccountID, "draft", 
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
    global_position BIGSERIAL NOT NULL,
    
    -- ストリーム内でのバージョン一意性を保証（楽観的ロック）
    UNIQUE (stream_id, version)
);

-- インデックス
CREATE INDEX idx_events_stream_id ON events(stream_id);
CREATE INDEX idx_events_stream_id_version ON events(stream_id, version);
CREATE INDEX idx_events_occurred_at ON events(occurred_at);
CREATE INDEX idx_events_recorded_at ON events(recorded_at);
CREATE INDEX idx_events_type ON events(type);
CREATE INDEX idx_events_global_position ON events(global_position);

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

// Upcast は不動点（どの Upcaster もこれ以上バージョンを進めない状態）まで
// ループする。単一パスは登録順に依存し、v1→v2 が v2→v3 の後に登録されると
// v1 イベントが v2 で止まる。不動点まで反復することで登録順非依存になる。
// 進捗は SchemaVersion の厳密な増加で判定し、maxUpcastIterations で非収束を防ぐ（issue #153）。
func (c *UpcasterChain) Upcast(event Event) (Event, error) {
    result := event
    for iter := 0; iter < maxUpcastIterations; iter++ {
        advanced := false
        for _, u := range c.upcasters {
            if !u.CanUpcast(result.Type, result.SchemaVersion) {
                continue
            }
            before := result.SchemaVersion
            var err error
            result, err = u.Upcast(result)
            if err != nil {
                return Event{}, err
            }
            if result.SchemaVersion > before {
                advanced = true
            }
        }
        if !advanced {
            return result, nil
        }
    }
    return Event{}, fmt.Errorf("upcaster chain did not converge for %q", event.Type)
}
```

### 10.3 契約イベントの登録済み Upcaster

`domain/contract/upcaster.go` の `NewContractUpcasterChain()` が以下を登録する。
すべて冪等。versioned なイベントは `SchemaVersioned.CurrentSchemaVersion()` で
現行バージョンを自己申告するため（`contract.created` は v3、
`contract.price_changed` / `contract.trial_ended` / `contract.renewed` は v2）、
`RaiseEvent` が新規イベントを現行バージョンで刻む。よって**新規イベントは各
Upcaster の `CanUpcast` が false となりチェーンを素通り**し、履歴上の旧バージョン
イベントだけが変換される（issue #153）。v1 の `contract.created` は
チェーンの不動点ループにより v1→v2→v3 と 2 段で変換される。

| Upcaster | 対象イベント | 変換内容 |
|----------|------------|---------|
| `PriceChangedEventUpcaster` | `contract.price_changed` | Money ベース v1 → PriceID ベース v2（`policy` / `*_price_id` を補完） |
| `ContractCreatedEventUpcaster` | `contract.created` | 旧 `billing_cycle` → `interval`（v1 → v2） |
| `ContractCreatedIdempotencyKeyUpcaster` | `contract.created` | v2 → v3（`idempotency_key` 追加、issue #159）。SchemaVersion を上げるのみ — 歴史的イベントのキーは復元不能（記録されていない）ため空のまま。Apply が空を許容する |
| `ContractRenewedEventUpcaster` | `contract.renewed` | 旧 `old/new_billing_cycle` → `old/new_interval` |
| `TrialEndedEventUpcaster` | `contract.trial_ended` | v1 → v2（`current_period` 追加、issue #146）。SchemaVersion を上げるのみ |

**`TrialEndedEventUpcaster` の設計上の注意**: 転換時の初期課金期間 `current_period` を
計算するには `interval` が必要だが、`interval` はこのイベントではなく先行する
`ContractCreatedEvent` に載っているため、Upcaster（単一イベントの JSON しか見えない）では
埋められない。したがって Upcaster は SchemaVersion を 2 に上げるだけで、期間の復元は
集約の `Apply` が担う: `Converted=true` かつ `current_period` がゼロ値なら、リプレイ済みの
`interval` を `EndedAt` に加算して期間を決定的に導出する。これによりリプレイは決定的で、
追記専用履歴を書き換えない。
