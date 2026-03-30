---
sidebar_position: 4
---

# イベントソーシング

Contract Billing Coreは、Contract集約にイベントソーシングを使用し、完全な監査証跡と時間旅行機能を提供します。完全なStoreインターフェースとイベント型の定義については[Event Store APIリファレンス](../api/event-store)を参照してください。

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

すべてのイベントには監査とトレーサビリティのためのメタデータ（UserID、CorrelationID、CausationIDなど）が含まれます。`expectedVersion`による楽観的ロックで並行書き込みの競合を防止します。

## 時間旅行クエリ

過去の任意の時点での契約状態をクエリ：

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// 2026年5月15日時点の契約状態は？
historical, _ := queryService.GetContractAsOf(ctx, contractID, may15)
historical.Status() // "active"
historical.Price()  // ¥3,000（価格変更前）

// 完全な変更履歴
history, _ := queryService.GetContractHistory(ctx, contractID)
for _, entry := range history {
    fmt.Printf("[%s] %s by %s\n", entry.OccurredAt.Format(time.RFC3339), entry.EventType, entry.UserID)
}
```

| ユースケース | 方法 |
|------------|------|
| 課金紛争の調査 | 紛争のある課金日時で`GetContractAsOf` |
| 規制監査 | `GetContractHistory`で完全な監査証跡 |
| 状態問題のデバッグ | 異なる時点の状態を比較 |
| 価格変更分析 | 価格改定前後の状態をクエリ |
| コンプライアンス報告 | 誰がいつ何を変更したかを再構築 |

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

### パフォーマンスに関する考慮事項

- **スナップショット**により、多くのイベントを持つ契約のパフォーマンスが大幅に向上。`SnapshotService`で定期的にスナップショットを作成。
- **LoadUntil**は対象時刻でイベントリプレイを停止するため、最近の状態のクエリはより高速。
- 大量の履歴クエリには、個別集約のクエリではなくプロジェクションの構築を検討。

## プロジェクション

プロジェクションサービスでイベントから読み取り最適化ビューを構築。完全なAPIは[ProjectionService](../api/services#projectionservice)を参照。

```go
projectionService := projection.NewProjectionService(eventStore, projection.ProjectionOptions{
    SyncMode:   true,
    BatchSize:  100,
    MaxRetries: 3,
})
projectionService.RegisterProjector(myProjector)
projectionService.Start(ctx)
```

## 例: 時間旅行デモ

この例では、5ヶ月にわたる契約変更の時間旅行を実演します：

| 時点 | アクション | 状態 |
|------|----------|------|
| 4月1日 (T1) | 作成・有効化 | active, ¥3,000/月 |
| 5月1日 (T2) | 価格変更 | active, ¥5,000/月 |
| 6月1日 (T3) | 停止（支払い遅延） | suspended |
| 7月1日 (T4) | 再開 | active, ¥5,000/月 |
| 8月1日 (T5) | 解約 | cancelled |

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// 過去の任意の時点に時間旅行
historical, _ := queryService.GetContractAsOf(ctx, contractID, may1)
// Status: active, Price: ¥5,000（5月1日の価格変更後）
```

T3（停止後）にスナップショットを作成。その後の復元ではスナップショット以降のイベントのみリプレイ：

```
全イベントリプレイ:     6イベント
スナップショット + リプレイ: 3イベント（v3のスナップショット + 残り3）
```

```bash
go run ./examples/event-sourcing-demo/
```
