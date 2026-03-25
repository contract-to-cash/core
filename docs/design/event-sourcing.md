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

// Event ドメインイベント
type Event struct {
    ID            string          // イベント一意ID
    StreamID      string          // 集約ID
    Type          string          // イベントタイプ
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

// RaiseEvent 新しいイベントを発行
func (a *BaseAggregate) RaiseEvent(eventType string, data interface{}, metadata EventMetadata) error {
    jsonData, err := json.Marshal(data)
    if err != nil {
        return err
    }
    
    event := Event{
        ID:         GenerateID(),
        StreamID:   a.id,
        Type:       eventType,
        Version:    a.version + len(a.uncommittedEvents) + 1,
        Data:       jsonData,
        Metadata:   metadata,
        OccurredAt: time.Now().UTC(),
    }
    
    a.uncommittedEvents = append(a.uncommittedEvents, event)
    return nil
}

func (a *BaseAggregate) IncrementVersion() {
    a.version++
}
```

## 4. 契約集約の実装例

### 4.1 契約集約

```go
// domain/contract/aggregate.go
package contract

import (
    "encoding/json"
    "errors"
    "time"
    
    "github.com/contract-to-cash/core/eventstore"
)

// ContractAggregate 契約集約
type ContractAggregate struct {
    eventstore.BaseAggregate
    
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
func NewContractAggregate(id string) *ContractAggregate {
    return &ContractAggregate{
        BaseAggregate: eventstore.BaseAggregate{id: id},
    }
}

// Create 契約を作成
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error {
    if a.status != "" {
        return errors.New("contract already exists")
    }
    
    event := ContractCreatedEvent{
        ContractID:   a.ID(),
        AccountID:    cmd.AccountID,
        PlanID:       cmd.PlanID,
        Price:        cmd.Price,
        BillingCycle: cmd.BillingCycle,
        CreatedAt:    time.Now().UTC(),
    }
    
    return a.RaiseEvent("ContractCreated", event, metadata)
}

// Activate 契約を有効化
func (a *ContractAggregate) Activate(metadata eventstore.EventMetadata) error {
    if a.status != ContractStatusDraft && a.status != ContractStatusTrialing {
        return errors.New("contract cannot be activated")
    }
    
    event := ContractActivatedEvent{
        ContractID:  a.ID(),
        ActivatedAt: time.Now().UTC(),
    }
    
    return a.RaiseEvent("ContractActivated", event, metadata)
}

// Suspend 契約を一時停止
func (a *ContractAggregate) Suspend(config SuspensionConfiguration, metadata eventstore.EventMetadata) error {
    if a.status != ContractStatusActive {
        return errors.New("only active contracts can be suspended")
    }
    
    event := ContractSuspendedEvent{
        ContractID:      a.ID(),
        SuspendedAt:     time.Now().UTC(),
        BillingBehavior: config.BillingBehavior,
        ResumeDate:      config.ResumeDate,
        Reason:          config.Reason,
    }
    
    return a.RaiseEvent("ContractSuspended", event, metadata)
}

// LoadFromHistory イベント履歴から状態を復元
func (a *ContractAggregate) LoadFromHistory(events []eventstore.Event) error {
    for _, event := range events {
        if err := a.Apply(event); err != nil {
            return err
        }
        a.IncrementVersion()
    }
    return nil
}

// Apply イベントを適用して状態を更新
func (a *ContractAggregate) Apply(event eventstore.Event) error {
    switch event.Type {
    case "ContractCreated":
        var e ContractCreatedEvent
        if err := json.Unmarshal(event.Data, &e); err != nil {
            return err
        }
        a.accountID = e.AccountID
        a.planID = e.PlanID
        a.price = e.Price
        a.billingCycle = e.BillingCycle
        a.status = ContractStatusDraft
        a.createdAt = e.CreatedAt
        a.updatedAt = e.CreatedAt
        
    case "ContractActivated":
        var e ContractActivatedEvent
        if err := json.Unmarshal(event.Data, &e); err != nil {
            return err
        }
        a.status = ContractStatusActive
        a.updatedAt = e.ActivatedAt
        
    case "ContractSuspended":
        var e ContractSuspendedEvent
        if err := json.Unmarshal(event.Data, &e); err != nil {
            return err
        }
        a.status = ContractStatusSuspended
        a.updatedAt = e.SuspendedAt
        
    // 他のイベントタイプも同様に処理
    }
    
    return nil
}
```

### 4.2 ドメインイベント定義

```go
// domain/contract/events.go
package contract

import "time"

// ContractCreatedEvent 契約作成イベント
type ContractCreatedEvent struct {
    ContractID   string       `json:"contract_id"`
    AccountID    string       `json:"account_id"`
    PlanID       string       `json:"plan_id"`
    Price        Money        `json:"price"`
    BillingCycle BillingCycle `json:"billing_cycle"`
    CreatedAt    time.Time    `json:"created_at"`
}

// ContractActivatedEvent 契約有効化イベント
type ContractActivatedEvent struct {
    ContractID  string    `json:"contract_id"`
    ActivatedAt time.Time `json:"activated_at"`
}

// ContractSuspendedEvent 契約一時停止イベント
type ContractSuspendedEvent struct {
    ContractID      string                    `json:"contract_id"`
    SuspendedAt     time.Time                 `json:"suspended_at"`
    BillingBehavior SuspensionBillingBehavior `json:"billing_behavior"`
    ResumeDate      *time.Time                `json:"resume_date,omitempty"`
    Reason          string                    `json:"reason"`
}

// ContractResumedEvent 契約再開イベント
type ContractResumedEvent struct {
    ContractID string    `json:"contract_id"`
    ResumedAt  time.Time `json:"resumed_at"`
}

// ContractCancelledEvent 契約解約イベント
type ContractCancelledEvent struct {
    ContractID  string    `json:"contract_id"`
    CancelledAt time.Time `json:"cancelled_at"`
    Reason      string    `json:"reason"`
}

// PriceChangedEvent 価格変更イベント
type PriceChangedEvent struct {
    ContractID string    `json:"contract_id"`
    OldPrice   Money     `json:"old_price"`
    NewPrice   Money     `json:"new_price"`
    ChangedAt  time.Time `json:"changed_at"`
    EffectiveAt time.Time `json:"effective_at"`
}

// PlanChangedEvent プラン変更イベント
type PlanChangedEvent struct {
    ContractID   string            `json:"contract_id"`
    OldPlanID    string            `json:"old_plan_id"`
    NewPlanID    string            `json:"new_plan_id"`
    Proration    *ProrationResult  `json:"proration,omitempty"`
    ChangedAt    time.Time         `json:"changed_at"`
}

// TrialStartedEvent トライアル開始イベント
type TrialStartedEvent struct {
    ContractID   string             `json:"contract_id"`
    TrialConfig  TrialConfiguration `json:"trial_config"`
    StartedAt    time.Time          `json:"started_at"`
}

// TrialEndedEvent トライアル終了イベント
type TrialEndedEvent struct {
    ContractID string    `json:"contract_id"`
    EndedAt    time.Time `json:"ended_at"`
    Converted  bool      `json:"converted"` // 本契約に移行したか
}
```

## 5. 時点再構築（Temporal Query）

### 5.1 時点指定クエリサービス

```go
// application/query/temporal_query_service.go
package query

import (
    "context"
    "time"
    
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/eventstore"
)

// TemporalQueryService 時点指定クエリサービス
type TemporalQueryService struct {
    eventStore eventstore.Store
}

func NewTemporalQueryService(store eventstore.Store) *TemporalQueryService {
    return &TemporalQueryService{eventStore: store}
}

// GetContractAsOf 指定時点の契約状態を取得
func (s *TemporalQueryService) GetContractAsOf(
    ctx context.Context,
    contractID string,
    asOf time.Time,
) (*contract.ContractAggregate, error) {
    // スナップショットの読み込み（指定時点より前の最新）
    snapshot, err := s.eventStore.LoadSnapshotBefore(ctx, contractID, asOf)
    
    agg := contract.NewContractAggregate(contractID)
    
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
    EventType  string
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

## 6. Projection（読み取りモデル）

### 6.1 Projection更新サービス

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

### 6.2 契約Projectionの実装例

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
    case "ContractCreated":
        return p.handleContractCreated(ctx, event)
    case "ContractActivated":
        return p.handleContractActivated(ctx, event)
    case "ContractSuspended":
        return p.handleContractSuspended(ctx, event)
    // 他のイベントタイプ
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

## 7. データベーススキーマ

### 7.1 Event Store テーブル

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

### 7.2 Projectionテーブル例

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

## 8. スナップショット戦略

### 8.1 スナップショット作成タイミング

| 戦略 | 説明 | 適用ケース |
|------|------|-----------|
| **N件ごと** | イベントがN件蓄積するたびに作成 | 標準的なケース |
| **時間ベース** | 一定時間ごとに作成 | 長期運用 |
| **オンデマンド** | 明示的な要求時のみ作成 | リソース節約 |

### 8.2 実装例

```go
// application/service/snapshot_service.go
package service

const DefaultSnapshotInterval = 100 // 100イベントごとにスナップショット

type SnapshotService struct {
    eventStore eventstore.Store
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
    
    snapshot := eventstore.Snapshot{
        StreamID:  agg.ID(),
        Version:   agg.Version(),
        State:     state,
        AsOf:      time.Now().UTC(),
        CreatedAt: time.Now().UTC(),
    }
    
    return s.eventStore.SaveSnapshot(ctx, snapshot)
}
```

## 9. イベントバージョニング

### 9.1 スキーマバージョン管理

```go
// イベントにSchemaVersionフィールドを含める
type Event struct {
    // ...
    SchemaVersion int // 1, 2, 3, ...
    // ...
}
```

### 9.2 Upcaster（将来の拡張用）

```go
// eventstore/upcaster.go
package eventstore

// Upcaster 古いイベントを新しいスキーマに変換
type Upcaster interface {
    // CanUpcast このUpcasterが処理可能か判定
    CanUpcast(eventType string, fromVersion int) bool
    
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
