# マルチサービスデモ

単一の課金システムに複数の独立したサービスプラグインが共存し、PlanIDベースのフィルタリングで各自の契約種別にのみ反応する仕組みを示します。

## シナリオ

3つの商品を販売するホスティング会社:

| 商品 | PlanIDプレフィックス | プラグイン | 操作内容 |
|------|---------------------|-----------|---------|
| VPSサーバー | `plan-vps-*` | ServerPlugin | VM起動/停止/再起動/削除 |
| SSL証明書 | `plan-ssl-*` | SSLPlugin | 証明書発行/一時停止/再有効化/失効 |
| ドメイン名 | `plan-domain-*` | DomainPlugin | ドメイン登録/一時停止/復元/解放 |

## 実行結果

```
Phase 1: 顧客が3つの商品を購入
  🟢 [Server] VMプロビジョニング ...
  🔒 [SSL] 証明書発行 ...
  🌐 [Domain] ドメイン登録 ...

Phase 2: VPSの決済失敗 -> VPSだけ停止
  🔴 [Server] VM停止 ...
  （SSLとドメインは影響なし）

Phase 3: VPSの決済成功 -> VPS再開
  🟢 [Server] VM再起動 ...

Phase 4: 顧客がドメインだけ解約
  🚫 [Domain] ドメイン解放 ...
  （VPSとSSLは稼働継続）

最終状態:
  🟢 VPSサーバー      active
  🟢 SSL証明書        active
  ⚫ ドメイン名        cancelled
```

## 仕組み

3つのプラグインが同時に登録されます。全契約イベントが全プラグインに配信されますが、各プラグインはPlanIDプレフィックスでフィルタします:

```go
func (p *ServerPlugin) handles(c *contract.ContractAggregate) bool {
    return strings.HasPrefix(string(c.PlanID()), "plan-vps-")
}

func (p *ServerPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
    if !p.handles(c) {
        return nil  // 自分の管轄外
    }
    return p.stopServer(c)
}
```

## 主要コンセプト

| コンセプト | 説明 |
|-----------|------|
| **PlanIDベースのルーティング** | 各プラグインがPlanIDプレフィックスでイベントをフィルタ -- シンプルかつ明示的 |
| **単一責任** | 各プラグインは正確に1つのサービス種別を担当 |
| **ゼロ結合** | プラグイン同士は互いを知らない。課金コアもサービスを知らない |
| **追加型の拡張性** | 新商品 = 新プラグイン。既存コードの変更不要 |

## 実行

```bash
go run ./examples/multi-service-demo/
```
