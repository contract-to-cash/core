---
sidebar_position: 2
---

# イベントソーシングデモ

この例では、イベントソーシングの時間旅行と監査機能を実演します。

## サンプルの実行

```bash
go run ./examples/event-sourcing-demo/
```

## 処理内容

契約を作成し、5ヶ月にわたり複数の状態変更を適用：

| 時点 | アクション | 状態 |
|------|----------|------|
| 4月1日 (T1) | 作成・有効化 | active, ¥3,000/月 |
| 5月1日 (T2) | 価格変更 | active, ¥5,000/月 |
| 6月1日 (T3) | 停止（支払い遅延） | suspended |
| 7月1日 (T4) | 再開 | active, ¥5,000/月 |
| 8月1日 (T5) | 解約 | cancelled |

`TemporalQueryService`で各時点の契約状態をクエリ：

```go
queryService := query.NewTemporalQueryService(eventStore, clock)

// 過去の任意の時点に時間旅行
historical, _ := queryService.GetContractAsOf(ctx, contractID, may1)
// Status: active, Price: ¥3,000（価格変更前）
```

### スナップショット復元

T3（停止後）にスナップショットを作成。その後の復元ではスナップショット以降のイベントのみリプレイ：

```
全イベントリプレイ:     6イベント
スナップショット + リプレイ: 3イベント（v3のスナップショット + 残り3）
```

## ポイント

- すべての状態変更が不変イベントとして記録
- `GetContractAsOf`で過去の任意の時点の状態を再構築
- スナップショットにより長期間の集約のリプレイコストを大幅に削減
- 完全な監査証跡で誰がいつ何を変更したかを記録
