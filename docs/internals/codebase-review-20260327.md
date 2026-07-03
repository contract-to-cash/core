---
sidebar_label: Codebase Review (2026-03-27)
---

# コードベース調査レポート: Contract-to-Cash Core

> **⚠️ 注意（2026-07 追記）**: このドキュメントは **2026-03-27 時点のスナップショット** であり、
> 現状のコードベースとは乖離している。指摘事項の多く（#1〜#3、#10 等）は既に修正済みで、
> 本文中の「17種のフック」も現在は **20種**（`plugin/` 配下と `docs/internals/plugin-system.md` 参照）。
> 数値・未修正指摘・フック数は当時のものとして読み、**現状は常にコードを正とする**こと。

**調査日**: 2026-03-27
**対象パス**: contract-to-cash/core
**調査テーマ**: コード品質、設計パターン、テストカバレッジ、改善点

---

## プロジェクト概要

| 項目 | 値 |
|------|-----|
| 言語 | Go 1.25 |
| アーキテクチャ | DDD + Event Sourcing + CQRS + Plugin Architecture |
| テスト | Go標準 testing パッケージ (280テスト関数) |
| CI | GitHub Actions (build, test -race, lint, vet, fmt) |
| DB | インメモリ実装のみ (ポート定義でPostgreSQL/MySQL等に対応可) |
| 外部依存 | `github.com/oklog/ulid/v2` のみ |
| ソースファイル | 86ファイル / テストファイル 41ファイル / 総行数 18,383行 |

---

## ディレクトリ構成

```
core/
├── domain/                    # ドメイン層（外部依存ゼロ）
│   ├── billing/               #   請求計算インターフェース
│   ├── contract/              #   契約アグリゲート（Event Sourced）
│   ├── invoice/               #   請求書エンティティ
│   ├── payment/               #   支払いエンティティ・督促
│   ├── pricing/               #   料金モデル（定額/従量/段階）
│   ├── product/               #   商品エンティティ
│   ├── balance/                #   クレジット管理
│   ├── usage/                 #   利用量記録
│   └── shared/                #   共有値オブジェクト（Money, Clock等）
├── application/               # アプリケーション層
│   ├── service/               #   BillingService, PaymentService, SnapshotService
│   ├── port/                  #   外部ゲートウェイインターフェース
│   ├── query/                 #   時間旅行クエリサービス
│   ├── projection/            #   プロジェクション（読み取りモデル）
│   └── tx/                    #   トランザクション管理
├── eventstore/                # イベントソーシング基盤
├── plugin/                    # プラグインシステム（17種のフック）
├── plugins/                   # 公式プラグイン実装
│   ├── coupon/                #   クーポン/割引
│   ├── tax/                   #   税金計算（日本消費税対応）
│   └── invoicecleanup/        #   請求書クリーンアップ
├── infrastructure/inmemory/   # インメモリリポジトリ実装
├── batch/                     # バッチ処理（契約更新等）
├── examples/                  # 7つの実行可能デモ
├── tests/integration/         # 統合テスト
└── docs/                      # アーキテクチャドキュメント
```

---

## 主要コンポーネント

### ContractAggregate（契約アグリゲート）

**責務**: 契約ライフサイクル全体をイベントソーシングで管理
**ファイル**: `domain/contract/aggregate.go` (699行)

- 状態遷移: Draft → Trialing → Active → (PastDue/Suspended) → Cancelled/Expired
- 15種以上のドメインイベント（Created, Activated, PriceChanged, Renewed等）
- イベントアップキャスト対応（スキーマ進化）
- 価格変更ポリシー（即時 / 期間終了時）
- プロレーション（日割り計算）対応

### BillingService（請求サービス）

**責務**: 14ステップの請求フローをプラグインフックと共にオーケストレーション
**ファイル**: `application/service/billing_service.go` (470行)

- 契約読み込み → バリデーション → 基本料金計算 → 割引適用 → 税計算 → クレジット適用 → 請求書生成
- 各ステップでプラグインフック呼び出し
- サブスク/従量/ハイブリッド課金対応

### Plugin System（プラグインシステム）

**責務**: 拡張ポイントを提供し、ビジネスロジックのカスタマイズを可能にする
**ファイル**: `plugin/registry.go`, `plugin/hooks.go` 他

- 17種のフックインターフェース（ISP準拠）
- 優先度ベースの実行順制御
- スレッドセーフなレジストリ（sync.RWMutex）
- 初期化/シャットダウンのライフサイクル管理

### EventStore（イベントストア）

**責務**: イベントの永続化と時間旅行クエリ
**ファイル**: `eventstore/` (9ファイル)

- 楽観的ロック（バージョンベース）
- スナップショット対応
- 時間ベース・バージョンベース・範囲ベースのクエリ
- サブスクリプション（イベント購読）

### Pricing Models（料金モデル）

**責務**: 多様な課金方式の計算ロジック
**ファイル**: `domain/pricing/`

- Strategy パターンで3種のモデルを実装: FlatPrice, TieredPrice, UsagePrice
- TieredPriceはGraduated（段階）とVolume（ボリューム）の2モード対応
- `big.Rat`による精密な金額計算（浮動小数点誤差なし）

---

## テストカバレッジ

### パッケージ別カバレッジ

| パッケージ | カバレッジ | 評価 |
|-----------|-----------|------|
| domain/billing | 100.0% | ★★★ |
| domain/product | 100.0% | ★★★ |
| domain/usage | 90.9% | ★★★ |
| plugins/coupon | 87.4% | ★★★ |
| domain/contract | 81.6% | ★★☆ |
| domain/shared | 69.2% | ★★☆ |
| application/service | 68.0% | ★★☆ |
| batch | 68.4% | ★★☆ |
| domain/pricing | 67.8% | ★★☆ |
| eventstore | 66.7% | ★★☆ |
| domain/invoice | 66.3% | ★★☆ |
| plugin | 55.2% | ★☆☆ |
| domain/balance | 47.8% | ★☆☆ |
| plugins/tax | 42.9% | ★☆☆ |
| plugins/invoicecleanup | 42.1% | ★☆☆ |
| domain/payment | 35.3% | ★☆☆ |
| infrastructure/inmemory | 35.2% | ★☆☆ |
| application/port | 0.0% | ☆☆☆ |
| application/projection | 0.0% | ☆☆☆ |
| application/query | 0.0% | ☆☆☆ |

---

## 精査済み指摘事項

各指摘事項をコードベースで実際に検証し、深刻度と対応優先度を再評価した結果。

### 深刻度: 高（対応推奨）

| # | カテゴリ | 内容 | 根拠 |
|---|---------|------|------|
| 1 | バリデーション | `NewUsageRecord`が負の`quantity`を受け入れる | `domain/usage/entity.go:21-38` — バリデーションなし。負の使用量レコードが生成され、請求計算に伝播する |
| 2 | バリデーション | `UsagePrice.CalculatePrice`が負のusageで負の金額を返す | `domain/pricing/usage.go:17-29` — `usage * UnitPrice`を直接計算。意図しない返金が発生しうる |
| 3 | バリデーション | `NewLineItem`が負の`quantity`を受け入れる | `domain/invoice/entity.go:47-62` — バリデーションなし。負の請求書明細が作成される |
| 4 | テスト | `domain/payment` の5つの状態遷移メソッドが未テスト | `Complete()`, `Fail()`, `MarkRefunded()`, `MarkPartiallyRefunded()`, `MarkChargedBack()` — ビジネスクリティカルな状態遷移ロジック |
| 5 | テスト | `application/port/webhook.go` のWebhookProcessorが未テスト | リトライ（指数バックオフ+ジッター）、DLQ、重複排除、タイムスタンプ検証など約100行のロジック |
| 6 | テスト | `application/query/` のTemporalQueryServiceが未テスト | スナップショット後のイベントフィルタリングなど約45行のロジック |
| 7 | テスト | `application/projection/` のProjectionServiceが未テスト | リトライロジック+sync/async切替など約55行のロジック |

### 深刻度: 中（改善推奨）

| # | カテゴリ | 内容 | 根拠 |
|---|---------|------|------|
| 8 | バリデーション | `Product.AddFeature/AddUsageMetric`に重複チェックがない | `domain/product/entity.go:92-100` — blindにappend。同名のfeature/metricが重複登録される |
| 9 | 型安全性 | `BalanceEntry.sourceType`が未型付きstring | `domain/balance/entity.go:27` — `BalanceReason`はtyped stringだが`sourceType`はraw string。タイポが検出されない |
| 10 | 型安全性 | `UsageRecord.metricName`が未型付きstring | `domain/usage/entity.go:13` — `"api_calls"`と`"apiCalls"`が別メトリックとして扱われ、課金ミスにつながる |
| 11 | テスト | `infrastructure/inmemory/` の7ファイルにテストなし | payment, usage, invoice, product, contract repository等。ただし統合テストで間接的に一部カバー |

### 深刻度: 低（Nice to have）

| # | カテゴリ | 内容 | 根拠 |
|---|---------|------|------|
| 12 | 可観測性 | PaymentServiceの2箇所でエラーが `_ = hookErr` で破棄 | `application/service/payment_service.go:172,222` — 意図的な非致命的エラー処理。全クリティカルパスのエラーは適切にreturnされている |
| 13 | プラグイン | 同一優先度のプラグイン実行順が暗黙的（登録順依存） | `plugin/registry.go:319-321` — stable sortで登録順を保持するが、明示的な依存関係グラフはない。現状の使い方では問題にならない |

### 指摘取り下げ（過大評価だったもの）

| 元# | 内容 | 取り下げ理由 |
|-----|------|-------------|
| - | ContractAggregateの長いif-chain | `Cancel()`で5状態チェックがあるが、これは正当なステートマシンコード。Go言語では標準的な書き方 |
| - | PostgreSQL/MySQL実装が未提供 | ライブラリとしてポートインターフェースを定義するのが責務。DB実装を同梱しないのは適切な設計判断 |
| - | WebhookValidatorインターフェースの参照実装が未提供 | `WebhookHandler.ParseAndVerify()`で検証は行われており、分離が必要な実運用シーンは限定的 |
| - | CorrelationID/CausationIDが未活用 | 定義済みだが未使用。将来のトレーシング統合に向けた準備であり、現時点では問題ではない |
| - | TieredPrice.CalculatePriceの負値処理 | `usage <= 0`で明示的にゼロを返す防御的コード。上流のバリデーション(#1)で対処すべき |

---

## 設計パターン分析

### 適用されているパターン

| パターン | 実装箇所 | 評価 |
|---------|---------|------|
| Event Sourcing | ContractAggregate, EventStore | 完全な監査証跡・時間旅行 |
| Aggregate Root | Contract (DDD) | トランザクション境界の明確化 |
| Repository | 全エンティティ | インターフェース駆動の永続化 |
| Strategy | PricingModel (Flat/Tiered/Usage) | 課金方式の柔軟な切り替え |
| Value Object | Money, DateRange, Currency | 型安全な概念モデリング |
| Functional Options | Invoice構築 | 柔軟なコンストラクタ |
| Plugin/Hook | 17種のフックインターフェース | 変更なしで拡張可能 |
| State Machine | Contract, Invoice, Payment | 厳密な状態遷移バリデーション |
| Ports & Adapters | application/port | 外部システムの抽象化 |
| Optimistic Locking | EventStore | 同時実行制御 |

### SOLID準拠度

| 原則 | 評価 | 根拠 |
|------|------|------|
| S - 単一責務 | ★★★ | BillingService/PaymentService等の明確な責務分離 |
| O - 開放閉鎖 | ★★★ | プラグインフックで変更なく拡張可能 |
| L - リスコフ置換 | ★★★ | 全プラグイン・リポジトリがインターフェース準拠 |
| I - インターフェース分離 | ★★★ | 17フックを個別インターフェースで定義 |
| D - 依存性逆転 | ★★★ | ドメイン層が外部パッケージに一切依存しない |

### 依存関係の健全性

```
domain/shared （基盤 - 全パッケージから参照）
  ↓
domain/* （各ドメインパッケージ - 相互依存なし）
  ↑
eventstore （イベントソーシング基盤）
  ↑
application/* （アプリケーション層 - ドメイン層を利用）
  ↑
infrastructure/* （インフラ層 - ポートの実装）
  ↑
plugin/plugins （プラグイン - フックの実装）
```

- ドメイン層から外部パッケージへの依存: **ゼロ**（stdlib + ulid のみ）
- 循環依存: **なし**
- レイヤー違反: **なし**

---

## まとめ

### 総合評価: **A-（優秀）**

- **技術的特徴**: DDD + Event Sourcing + Plugin Architecture の教科書的な実装。`big.Rat`による精密な金額計算、17種のプラグインフック、時間旅行クエリなど、契約課金ドメインに求められる機能を網羅
- **品質状態**: 全テストがrace detector付きでパス、golangci-lintで警告ゼロ。コアドメインのテストカバレッジは67-100%と良好
- **精査結果**: 初期レビューの12件中、実コード検証の結果 **7件が対応推奨（高）、4件が改善推奨（中）、2件がNice-to-have（低）**。5件は過大評価として取り下げ
- **最優先対応**: 負値バリデーション不足（#1-3）は課金ドメインとして致命的になりうるため、最優先で対処すべき
