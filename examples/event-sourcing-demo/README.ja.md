# イベントソーシングデモ

イベントソーシングの「時間旅行」機能を実演します。契約が5ヶ月にわたり5回状態変更され、`TemporalQueryService` が過去の任意時点の状態を正確に復元します。

## 実行結果

```
=== Time Travel: Contract State at Each Point ===

  Date           Status         Price
  ------------------------------------------
  T1 (Apr 1)     active         ¥3000
  T2 (May 1)     active         ¥5000      <- 価格変更
  T3 (Jun 1)     suspended      ¥5000      <- 支払い遅延
  T4 (Jul 1)     active         ¥5000      <- 再開
  T5 (Aug 1)     cancelled      ¥5000      <- 解約

=== Snapshot Recovery ===
  Snapshot at v4 -> 6イベント中2つだけリプレイすればOK
```

## 主要コンセプト

| コンセプト | 説明 |
|-----------|------|
| **TemporalQueryService.GetContractAsOf()** | 任意時点の集約状態を復元 |
| **GetContractHistory()** | タイムスタンプ・ユーザーID付きの完全なイベント履歴 |
| **LoadFromHistory()** | 生イベントをリプレイして状態を再構築 |
| **Snapshot** | 集約状態を定期保存し、古いイベントのリプレイを省略 |
| **SnapshotService.CreateSnapshot()** | 集約状態をシリアライズして永続化 |

## なぜ重要か

- **監査対応**: 「6月15日時点の契約状態は？」に確実に回答可能
- **デバッグ**: 推測なしに過去の任意の状態を再現
- **パフォーマンス**: スナップショットによりリプレイコストをO(n)からO(1)+直近イベントに削減

## 実行

```bash
go run ./examples/event-sourcing-demo/
```
