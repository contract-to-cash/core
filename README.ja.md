# Contract Billing Core

[![Go Reference](https://pkg.go.dev/badge/github.com/contract-to-cash/core.svg)](https://pkg.go.dev/github.com/contract-to-cash/core)
[![CI](https://github.com/contract-to-cash/core/actions/workflows/ci.yml/badge.svg)](https://github.com/contract-to-cash/core/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

[English](README.md) | [ドキュメント](https://contract-to-cash.github.io/core/ja/)

SaaS・サブスクリプションビジネス向けのイベントソーシング課金エンジン。プラグインアーキテクチャによる拡張性を備え、ドメインモデル、課金パイプライン、拡張ポイントを提供します。データベースと決済ゲートウェイはあなた自身が用意します。

## 特徴

- **イベントソーシング** — 完全な監査証跡、時間旅行クエリ、スナップショット復元
- **プラグインアーキテクチャ** — 割引、税、ライフサイクル、決済、メトリクスフックで拡張（ISP準拠）
- **Product/Price分離** — Stripeスタイルの不変Price。グランドファザリング対応
- **複数の課金モデル** — 買い切り、サブスクリプション、従量課金（定額、段階制、ボリューム）
- **契約更新** — `pendingPriceID`による保留価格プロモーション付き自動更新
- **決済ゲートウェイ抽象化** — チャージ、オーソリ/キャプチャ、返金、階層型フォールバック
- **クレジット台帳** — 日割り、解約、調整のためのFIFOベースクレジット
- **クレジットノート・請求書再発行** — クレジットノート発行（ドラフト/発行/適用/返金）、請求書のvoid＆再発行（リビジョンチェーン追跡付き）
- **時間旅行クエリ** — 過去の任意の時点での契約状態を再構築

## クイックスタート

```bash
go get github.com/contract-to-cash/core
```

**Go 1.25**以上が必要です。

```go
package main

import (
    "context"
    "math/big"
    "time"

    "github.com/contract-to-cash/core/application/service"
    "github.com/contract-to-cash/core/domain/contract"
    "github.com/contract-to-cash/core/domain/balance"
    "github.com/contract-to-cash/core/domain/pricing"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
    "github.com/contract-to-cash/core/infrastructure/inmemory"
    "github.com/contract-to-cash/core/plugin"
    "github.com/contract-to-cash/core/plugins/tax"
)

func main() {
    ctx := context.Background()
    clock := shared.FixedClock{FixedTime: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}

    // インフラ（本番環境では独自の実装に置き換え）
    es := inmemory.NewInMemoryEventStore(clock)
    contractRepo := inmemory.NewInMemoryContractRepository(es, clock)
    invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
    balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
    usageRepo := inmemory.NewInMemoryUsageRepository()
    priceRepo := inmemory.NewInMemoryPriceRepository()
    productRepo := inmemory.NewInMemoryProductRepository()

    // プラグイン登録
    registry := plugin.NewRegistry()
    registry.Register(tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{}))
    registry.InitializeAll(ctx, map[string]plugin.Config{
        "tax": {"priority": plugin.PriorityLow},
    })
    defer registry.ShutdownAll(ctx)

    // PriceとContractの作成
    price := shared.NewMoney(new(big.Rat).SetInt64(3000), shared.CurrencyJPY)
    pe, err := pricing.NewPrice(shared.NewProductID(), price, shared.CurrencyJPY, pricing.BillingCycleMonthly, nil, clock.Now())
    if err != nil {
        panic(err)
    }
    priceRepo.Save(ctx, pe)

    cID := shared.NewContractID()
    agg := contract.NewContractAggregate(cID, clock)
    agg.Create(contract.CreateContractCommand{
        AccountID: shared.AccountID("acct-001"),
        PriceID: pe.ID(), ContractType: contract.ContractTypeSubscription,
        Interval: pricing.Monthly(), Price: price, BasePrice: price,
    }, eventstore.EventMetadata{UserID: "system"})
    agg.Activate(eventstore.EventMetadata{UserID: "system"})
    contractRepo.Save(ctx, agg)

    // 請求書生成（¥3,000 + 消費税10% = ¥3,300）
    bs := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo,
        balance.BalanceConfig{},
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30}, clock,
        service.WithBalanceRepo(balanceRepo),
    )
    inv, _ := bs.GenerateInvoice(ctx, cID, agg.CurrentPeriod())
    // inv.Total() => ¥3,300
    _ = inv
}
```

## サンプル

すべてのサンプルはインメモリ実装を使用し、外部依存はありません。

```bash
go run ./examples/billing-demo/           # 契約から決済までの完全フロー
go run ./examples/event-sourcing-demo/    # 時間旅行とスナップショット
go run ./examples/plugin-pipeline-demo/   # マルチプラグイン課金パイプライン
go run ./examples/lifecycle-demo/         # トライアル、停止、解約、クレジット
go run ./examples/hosting-integration-demo/ # ライフサイクルフック経由のプロビジョニング
go run ./examples/multi-service-demo/     # マルチサービスルーティング
go run ./examples/pricing-models-demo/    # 定額、段階制、ボリューム、従量課金
```

## アーキテクチャ

```
domain/           # エンティティ、値オブジェクト、集約（依存ゼロ）
├── contract/     # イベントソース契約集約
├── invoice/      # 請求書エンティティ
├── payment/      # 支払いエンティティ
├── balance/       # クレジット台帳
├── billing/      # 請求計算インターフェース、日割り計算
├── usage/        # 使用量レコード
├── pricing/      # 不変Priceエンティティ、価格モデル
├── product/      # Productエンティティ
└── shared/       # Money, DateRange, ID, Clock, エラー

application/      # サービス、ポート、クエリ
├── service/      # BillingService, PaymentService, SnapshotService, CreditNoteService
├── port/         # PaymentGatewayインターフェース
├── query/        # TemporalQueryService
├── projection/   # イベントプロジェクション
└── tx/           # トランザクション管理（TxManager, Saga）

plugin/           # フックインターフェースとレジストリ
plugins/          # 公式プラグイン（税、クーポン、請求書クリーンアップ）
eventstore/       # Event Storeインターフェース、集約ベース、スナップショット
infrastructure/   # インメモリ実装（テスト・デモ用）
batch/            # バッチプロセッサ（契約更新）
```

## プラグインシステム

必要なフックだけを実装：

| カテゴリ | フック | 用途 |
|---------|--------|------|
| **課金計算** | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` | 割引、税計算、計算前後処理 |
| **契約** | `OnContractCreate/Activate/Suspend/Resume/Cancel/Renew/TrialEndHook` | ライフサイクル反応 |
| **決済** | `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentFailedHook`, `OnRefundHook` | 決済フロー |
| **クレジットノート** | `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` | クレジットノート発行、請求書再発行 |
| **メトリクス** | `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` | KPI収集 |
| **請求書生成** | `InvoiceGenerationHook` | PDFレンダリング、配信 |

公式プラグイン: **Tax**（消費税計算）、**Coupon**（パーセンテージ/固定、スタッキング、使用制限）、**InvoiceCleanup**（解約時に未発行請求書を自動void）

## ドキュメント

完全なドキュメントは **[contract-to-cash.github.io/core](https://contract-to-cash.github.io/core/ja/)** で利用可能です（英語・日本語）。

| セクション | 内容 |
|-----------|------|
| [はじめに](https://contract-to-cash.github.io/core/ja/docs/introduction) | 概要と設計原則 |
| [クイックスタート](https://contract-to-cash.github.io/core/ja/docs/quick-start) | インストールと最初の課金フロー |
| [アーキテクチャ](https://contract-to-cash.github.io/core/ja/docs/architecture) | システム設計の詳細 |
| [コア概念](https://contract-to-cash.github.io/core/ja/docs/concepts/domain-model) | ドメインモデル、イベントソーシング、プラグイン、決済ゲートウェイ |
| [ガイド](https://contract-to-cash.github.io/core/ja/docs/guides/integration) | 統合、カスタムプラグイン、時間旅行クエリ |
| [APIリファレンス](https://contract-to-cash.github.io/core/ja/docs/api/domain-types) | 型、サービス、フック、Event Store |

## 安定性（Stability）

本プロジェクトは **v1.0 未満（pre-v1.0）** です。タグ付きリリースは
[v0.2.0](https://github.com/contract-to-cash/core/releases)（最初のキュレーション済みリリース）から提供しています —
`main` の擬似バージョンではなくリリースタグを固定してください。
v1.0 に向けて API が変更される可能性があります。

- **バージョニング**は[セマンティックバージョニング](https://semver.org/lang/ja/)に従います。
  プラグイン API の互換性ポリシー（フック・コンテキストにおける破壊的変更の定義）は
  [Plugin System §10](docs/internals/plugin-system.md) に記載しています。
- **破壊的変更**は [CHANGELOG.md](CHANGELOG.md) に記録します。pre-v1.0 の破壊的変更は
  `BREAKING (pre-v1.0)` と明記します。
- 再現可能なビルドには[リリースタグ](https://github.com/contract-to-cash/core/releases)を
  固定してください。`main` の追跡は未リリースの変更を取り込む前提がある場合のみ推奨します。

セキュリティ報告と統合者向けのハードニングは [SECURITY.md](SECURITY.md) を参照してください。

## コントリビュート

コントリビューション歓迎です。プルリクエストを送る前に、まずイシューでアイデアを議論してください。

```bash
make lint   # リンター実行
make test   # テスト実行
```

## ライセンス

[MIT](LICENSE)
