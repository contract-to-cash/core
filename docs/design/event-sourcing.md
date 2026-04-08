# イベントソーシング設計

> **ソースコード参照**: インターフェース定義は `eventstore/` パッケージ、
> 契約集約の実装は `domain/contract/aggregate.go` を参照のこと。

## 1. 概要

### イベントソーシングの利点

| 利点 | 説明 |
|------|------|
| 完全な監査証跡 | いつ、何が起こったかを完全に記録 |
| 時点再構築 | 任意の過去時点の状態を再現可能 |
| デバッグ容易性 | 問題発生時にイベントを追跡して原因特定 |
| イベント駆動統合 | 他システムとの疎結合な連携が可能 |
| Projection再構築 | 読み取りモデルを何度でも再構築可能 |

## 2. Event Store

ソース: `eventstore/store.go`

### 主要メソッド

| メソッド | 説明 |
|---------|------|
| `Append` | イベントの永続化（楽観的ロック付き） |
| `Load` | ストリームの全イベント取得 |
| `LoadUntilVersion` | 特定バージョンまでのイベント取得 |
| `LoadUntil` | 特定時点（`OccurredAt` ベース）までのイベント取得 |
| `LoadRange` | 期間指定でのイベント取得 |
| `Subscribe` | 全ストリームのイベント購読（Projection用） |
| `SaveSnapshot` / `LoadSnapshot` | スナップショットの保存・読み込み |
| `LoadSnapshotBefore` | 指定時点より前の最新スナップショット取得 |

### Event 構造

ソース: `eventstore/event.go`

- `ID`: イベント一意ID
- `StreamID`: 集約ID
- `Type`: `EventType`（型付き定数）
- `Version`: ストリーム内のバージョン（楽観的ロック用）
- `SchemaVersion`: イベントスキーマバージョン（upcaster用）
- `Data`: `json.RawMessage`（イベントデータ）
- `Metadata`: `UserID`（必須）、`IPAddress`/`UserAgent`（オプション）、`CorrelationID`/`CausationID`
- `OccurredAt`: ビジネス時刻（Clock IF 経由で設定）
- `RecordedAt`: システム記録時刻（Event Store 側で設定）

### DomainEvent インターフェース

各集約のイベント型が実装する。`EventType()` メソッドで型定数を返す。

## 3. 集約ルート

ソース: `eventstore/aggregate.go`, `eventstore/aggregate_base.go`

### BaseAggregate

- `RaiseEvent(domainEvent, metadata)`: 型付きDomainEventを発行。EventType() を自動取得
- `Clock()`: ビジネス時刻取得用（`time.Now()` 直接呼び出し禁止）
- `IncrementVersion()` / `SetVersion()`: バージョン管理

### Evolver インターフェース

`Apply(event DomainEvent) error` — 型付きDomainEventを受け取り状態を更新する純粋関数。
デシリアライズ（`json.RawMessage` → `DomainEvent`）は `LoadFromHistory` の責務。

### EventRegistry

ソース: `eventstore/event_registry.go`

`EventType` → Go型のマッピングを管理。`Register()` で型を登録、`Deserialize()` で復元。

**重要**: 新しいイベント型を追加する場合、以下の2箇所を必ず更新すること:
1. `EventRegistry` に `Register()` で型を登録
2. `ContractAggregate.Apply()` の switch case に対応を追加

`exhaustive` lint ツールで `Apply` 側の漏れは検出可能だが、`Register` 漏れは検出できない。

## 4. 契約集約のイベント型

ソース: `domain/contract/events.go`

| イベント | 説明 |
|---------|------|
| `contract.created` | 契約作成 |
| `contract.activated` | 契約有効化 |
| `contract.suspended` | 契約一時停止 |
| `contract.resumed` | 契約再開 |
| `contract.cancelled` | 契約解約 |
| `contract.price_changed` | 価格変更（即時適用） |
| `contract.price_change_scheduled` | 価格変更予約（期間終了時適用） |
| `contract.price_change_unscheduled` | 価格変更予約取消 |
| `contract.trial_started` | トライアル開始 |
| `contract.trial_ended` | トライアル終了 |
| `contract.payment_method_changed` | 決済手段変更 |
| `contract.renewed` | 契約更新 |
| `contract.expired` | 契約期限切れ |
| `contract.cancellation_scheduled` | 解約予約 |
| `contract.cancellation_unscheduled` | 解約予約取消 |

## 5. 時点再構築（Temporal Query）

ソース: `application/query/temporal_query_service.go`

### GetContractAsOf

指定時点の契約状態を取得。スナップショット + 差分イベントで効率的に復元:
1. `LoadSnapshotBefore(contractID, asOf)` でスナップショット取得
2. スナップショット以降〜asOf までのイベントを `LoadRange` で取得
3. `LoadFromSnapshot` → `LoadFromHistory` で状態を復元

### GetContractHistory / CompareStates

変更履歴の取得、2時点間の状態比較。

## 6. Projection（読み取りモデル）

ソース: `application/projection/service.go`

### ProjectionOptions

| オプション | 説明 |
|-----------|------|
| `SyncMode` | true: 同期更新（即座の一貫性）、false: 非同期更新（スループット重視） |
| `BatchSize` | 非同期時のバッチサイズ |
| `MaxRetries` / `RetryDelay` | リトライ設定 |

### Projector インターフェース

- `Project(ctx, event)`: イベントをProjectionに反映
- `Rebuild(ctx, until)`: 指定時点までのProjectionを再構築

## 7. データベーススキーマ

### Event Store テーブル

```sql
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
    UNIQUE (stream_id, version)
);

CREATE TABLE snapshots (
    id UUID PRIMARY KEY,
    stream_id VARCHAR(255) NOT NULL,
    version INT NOT NULL,
    state JSONB NOT NULL,
    as_of TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (stream_id, version)
);
```

## 8. スナップショット戦略

- デフォルト: N件（100）ごとにスナップショット作成
- `SnapshotService` が `ShouldCreateSnapshot()` で判定、`CreateSnapshot()` で作成
- ソース: `application/service/snapshot_service.go`

## 9. イベントバージョニング

- `SchemaVersion` フィールドで管理
- `Upcaster` インターフェースで古いイベントを新しいスキーマに変換
- `UpcasterChain` で複数のUpcasterをチェーン可能
- ソース: `eventstore/upcaster.go`
