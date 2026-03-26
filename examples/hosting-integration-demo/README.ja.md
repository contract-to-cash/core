# ホスティング連携デモ

最も実践的な例です。課金イベントがプラグインフックを通じて外部サービス（サーバープロビジョニング）を自動制御する仕組みを示します。**「決済完了がどうやってサービス提供に繋がるのか？」** という疑問に答えます。

## シナリオ

レンタルサーバー（VPS）事業:

| フェーズ | 課金イベント | サーバー操作 |
|---------|------------|------------|
| 申込 | 契約作成・有効化 | 決済待ち |
| 初回決済 | 決済完了 | サーバー起動 |
| 更新決済失敗 | カード拒否 | 契約一時停止 -> サーバー停止 |
| カード更新 | リトライ成功 | 契約再開 -> サーバー再開 |
| 解約 | 契約キャンセル | サーバー削除 |

## 実行結果

```
Phase 2: 初回決済 & サーバープロビジョニング
  >> [ServerManager] SERVER PROVISIONED: CPU/RAM/ディスク割当、OS導入
  🟢 Server [...]: running

Phase 3: 決済失敗 -> サーバー停止
  決済失敗: カード拒否: 残高不足
  >> [ServerManager] SERVER STOPPED: VM一時停止、データ保持
  🔴 Server [...]: stopped

Phase 4: 決済リトライ -> サーバー再開
  >> [ServerManager] SERVER RESTARTED: VM再開、サービス起動中
  🟢 Server [...]: running

Phase 5: 解約 -> サーバー削除
  >> [ServerManager] SERVER TERMINATED: VM削除、バックアップ作成、IP解放
  ⚫ Server [...]: terminated
```

## 仕組み

`ServerProvisioningPlugin` は1つのプラグインで6つのフックインターフェースを実装しています:

```go
// 契約ライフサイクル
func (p *...) OnContractCreate(...)   // リソース準備
func (p *...) OnContractActivate(...) // 準備完了マーク
func (p *...) OnContractSuspend(...)  // サーバー停止
func (p *...) OnContractResume(...)   // サーバー再開
func (p *...) OnContractCancel(...)   // サーバー削除

// 決済
func (p *...) AfterCharge(...)        // サーバー起動
```

他のプラグインと同様に登録するだけ。課金コアは疎結合のまま:

```go
registry.Register(newServerProvisioningPlugin(serverManager))
```

## 主要コンセプト

| コンセプト | 説明 |
|-----------|------|
| **マルチフックプラグイン** | 1つのプラグインが複数のフックインターフェースを実装 |
| **疎結合な連携** | 課金コアはサーバーについて一切知らない |
| **合成可能** | プロビジョニングと並行して通知・監視・DNSプラグインを追加可能 |
| **イベント駆動** | ポーリング不要。ライフサイクルイベントがアクションをトリガー |

## 実世界での応用

この手法はあらゆる課金連動サービスに応用可能です:

- **ホスティング**: サーバー起動、DNS設定、SSL証明書
- **SaaS**: シート割当、フィーチャーフラグ切替
- **ドメイン登録**: ドメイン登録・更新・移管
- **クラウド**: Kubernetes名前空間作成、リソースクォータ
- **CDN**: エッジ設定、キャッシュパージ

## 実行

```bash
go run ./examples/hosting-integration-demo/
```
