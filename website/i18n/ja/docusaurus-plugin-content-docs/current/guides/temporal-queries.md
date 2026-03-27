---
sidebar_position: 3
---

# 時間旅行クエリ

時間旅行クエリにより、過去の任意の時点での契約状態を再構築でき、監査、デバッグ、履歴分析が可能になります。

## GetContractAsOf

特定のタイムスタンプでの契約状態を再構築：

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// 2026年5月15日時点の契約状態は？
may15 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
historical, err := queryService.GetContractAsOf(ctx, contractID, may15)
if err != nil {
    log.Fatal(err)
}

fmt.Println(historical.Status())  // "active"
fmt.Println(historical.Price())   // ¥3,000（6月1日の価格変更前）
```

内部処理：
1. 対象時刻より前の最新スナップショットのロードを試行
2. スナップショット（またはストリーム開始）から対象時刻までのイベントをリプレイ
3. 再構築された集約を返却

## GetContractHistory

契約の完全な変更履歴を時系列で取得：

```go
history, _ := queryService.GetContractHistory(ctx, contractID)
for _, entry := range history {
    fmt.Printf("[%s] %s by %s\n",
        entry.OccurredAt.Format(time.RFC3339),
        entry.EventType,
        entry.UserID,
    )
}
```

出力例：
```
[2026-04-01T00:00:00Z] contract.created by admin
[2026-04-01T00:00:00Z] contract.activated by admin
[2026-05-01T00:00:00Z] contract.price_changed by admin
[2026-06-01T00:00:00Z] contract.suspended by system
[2026-07-01T00:00:00Z] contract.resumed by admin
```

## ユースケース

| ユースケース | 方法 |
|------------|------|
| 課金紛争の調査 | 紛争のある課金日時で`GetContractAsOf` |
| 規制監査 | `GetContractHistory`で完全な監査証跡 |
| 状態問題のデバッグ | 異なる時点の状態を比較 |
| 価格変更分析 | 価格改定前後の状態をクエリ |
| コンプライアンス報告 | 誰がいつ何を変更したかを再構築 |

## パフォーマンスに関する考慮事項

- **スナップショット**により、多くのイベントを持つ契約のパフォーマンスが大幅に向上。`SnapshotService`で定期的にスナップショットを作成。
- **LoadUntil**は対象時刻でイベントリプレイを停止するため、最近の状態のクエリはより高速。
- 大量の履歴クエリには、個別集約のクエリではなくプロジェクションの構築を検討。
