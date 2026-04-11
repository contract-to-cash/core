# Contract-to-Cash Core

SaaS契約課金のOSSライブラリ（Go）。Event Sourcing + Plugin Architectureで契約・請求・決済を管理する。

## クイックリファレンス

```bash
make build              # ビルド
make test               # 全テスト（race detector有効）
make test-unit          # ユニットテスト（tests/除外）
make test-integration   # 統合テスト
make lint               # vet + gofmt + golangci-lint
make check              # build + lint + test（CI相当）
```

## アーキテクチャ（厳守事項）

### レイヤー構造（Clean Architecture + DDD）

```
domain/          → 純粋なドメインロジック。外部依存ゼロ
application/     → ユースケース。domainのみに依存
  service/       →   BillingService, PaymentService, CreditNoteService, SnapshotService
  port/          →   外部連携IF（PaymentGateway, WebhookHandler等）
  query/         →   時点再構築クエリ（TemporalQueryService）
  projection/    →   Projection更新（同期/非同期選択可能）
  tx/            →   トランザクション管理（TxManager, Saga）
eventstore/      → Event Sourcing基盤（Store, EventRegistry, Snapshot, Upcaster）
plugin/          → プラグインシステム基盤（Registry, 20種のHook IF）
plugins/         → 公式プラグイン実装（coupon, tax, invoicecleanup）
batch/           → バッチ処理（ContractRenewal等。スケジューラはサービス側の責務）
infrastructure/  → domain/applicationのインターフェース実装（inmemory/テスト用）
```

**絶対に守るルール:**
- `domain/` は外部パッケージに依存してはならない（標準ライブラリ + `ulid` のみ）
- `application/` は `domain/` のみに依存。`infrastructure/` に依存してはならない
- 依存の方向は常に外→内（Dependency Inversion）
- インターフェースは `domain/` または `application/port/` に定義し、実装は `infrastructure/` に置く

### ドメインモデル

| エンティティ | 種別 | 特記 |
|---|---|---|
| Contract | Event Sourced Aggregate | 状態遷移: Draft→Trialing→Active→PastDue/Suspended→Cancelled/Expired |
| Invoice | Entity | 改訂チェーン（void-and-recreate）対応 |
| CreditNote | Entity | 行項目レベルの調整 |
| Payment | Entity | 冪等性キー必須 |
| Price | Immutable Entity | Flat/Tiered(Graduated,Volume)/Usage の価格モデル |
| Product | Entity | 「何を売るか」を定義。Price（「どう課金するか」）と分離 |
| BalanceEntry | Entity | FIFO消費、有効期限対応 |

- `contract.BillingCycle` は `pricing.BillingCycle` のエイリアス（定義元は `pricing`）
- 契約は `CreateContractCommand.PriceID` で Price を指定する

### Event Sourcing

- イベントは不変・追記のみ（append-only）
- `EventRegistry` で型安全なデシリアライズ。**新イベント追加時は必ず `Register()` と `Apply()` の両方を更新**
- `SchemaVersion` フィールドでイベントスキーマのバージョン管理
- スナップショット: N件ごと（デフォルト100）で最適化
- 楽観的ロック（version-based）で並行制御

### プラグインシステム

コアが会計基準に則った計算順序を構造的に保証する:
```
BeforeCalculation → 価格計算 → Discount → Subtotal → Tax → Total → Credit適用 → Invoice生成 → AfterCalculation
```

- ISP準拠：必要なHookインターフェースのみ実装。空メソッドの強制実装は不要
- `Priority` は同一Hook内の実行順序のみ制御（Hook種別間の順序はコアが保証）

**フックカテゴリ一覧（全20種）:**

| カテゴリ | フック | 用途 |
|---|---|---|
| 請求計算 | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` | 割引・税計算、計算前後処理 |
| 契約ライフサイクル | `OnContractCreate/Activate/Suspend/Resume/Cancel/Renew/TrialEndHook` | 契約の各イベントに個別対応 |
| 支払い | `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentFailedHook`, `OnRefundHook` | 課金前後、失敗時、返金時 |
| メトリクス | `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` | KPI収集 |
| クレジットノート | `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` | CN発行、請求書差替 |
| 請求書生成 | `InvoiceGenerationHook` | PDF生成・送付（BuildDocument/AfterRender/AfterDelivery） |

## コーディング規約

### 必須パターン

- **時刻**: `time.Now()` は絶対に使わない。必ず `shared.Clock` インターフェース経由
- **金額**: `big.Rat` ベースの `shared.Money` を使用（浮動小数点は使用禁止）
- **ID生成**: `ulid` を使用（`github.com/oklog/ulid/v2`）
- **エラー**: `shared.DomainError` + `ErrorCode` で構造化。ビジネスエラーと技術エラーを区別
- **タイムゾーン**: すべてUTC

### 命名規則

- 型・公開関数: `PascalCase`
- 非公開フィールド/メソッド: `camelCase`
- ID型: 具体的な名前（`ContractID`, `InvoiceID` など。汎用 `ID` は使わない）
- イベント型名: `"domain.action"` 形式（例: `"contract.created"`, `"contract.activated"`）

### テスト

- `shared.FixedClock` で時刻を固定化
- `infrastructure/inmemory/` のリポジトリ実装をテストで使用
- `-race` フラグを常に有効化
- テストファイルの配置: ユニットテストは対象パッケージ内、統合テストは `tests/integration/`

### Lint

- golangci-lintの設定: `.golangci.yml` 参照
- `exhaustive` リンター有効（switchの網羅性チェック）
- `nolintlint`: `//nolint` には理由と対象リンター指定が必須
- `forbidigo` リンター有効: `ToSnapshot` / `FromSnapshot` / `CreditNoteFromSnapshot` は persistence adapter 専用。`domain/*/snapshot*.go`, `infrastructure/`, `tests/` 以外から呼び出すと CI が落ちる（issue #100）。`ContractAggregate.MarshalSnapshot` / `LoadFromSnapshot` は event-sourced 用の別 API なので対象外。
- `examples/` ディレクトリはlint除外
- 新規 lint ルール追加時は `tests/lintcheck/` に enforcement 検証テストを置く（`make test-lint-rules`）

## 変更時の注意事項

### 破壊的変更の回避

- イベントスキーマの変更は `SchemaVersion` + Upcasterで対応。既存イベントは絶対に変更しない
- ドメインの状態遷移ルールを変更する場合、既存のイベント履歴からの再構築が壊れないか確認
- プラグインHookのシグネチャ変更はすべてのプラグイン実装に影響する

### 追加・変更チェックリスト

- [ ] `domain/` に外部依存を持ち込んでいないか
- [ ] 新しいドメインイベントを `EventRegistry` に登録したか
- [ ] 新しいドメインイベントを `Apply()` の型スイッチに追加したか
- [ ] `Money` 演算で通貨の一致を検証しているか
- [ ] 状態遷移が既存のフローと矛盾しないか
- [ ] `Clock` インターフェースを使っているか（`time.Now()` を使っていないか）
- [ ] テストが `-race` で通るか
- [ ] `ProductID` + `PriceID` を使っているか（`PlanID` は廃止済み）

### 既知の課題（コードレビュー 2026/03/27時点）

**対応推奨（高）:**
- `NewUsageRecord` / `NewLineItem` が負の `quantity` を受け入れる（バリデーション不足）
- `UsagePrice.CalculatePrice` が負のusageで負の金額を返す
- `domain/payment` の状態遷移メソッド（Complete/Fail/MarkRefunded等）が未テスト
- `application/port/webhook.go` の WebhookProcessor が未テスト
- `application/query/` の TemporalQueryService が未テスト

**改善推奨（中）:**
- `BalanceEntry.sourceType` が未型付きstring（タイポが検出されない）
- `Product.AddFeature/AddUsageMetric` に重複チェックがない

詳細: @docs/internals/codebase-review-20260327.md

## 詳細ドキュメント

- @docs/architecture.md - Architecture overview
- @docs/decisions/design-decisions.md - Design decisions and rationale
- @docs/internals/domain-model.md - Domain model detailed spec
- @docs/internals/event-sourcing.md - Event sourcing detailed spec
- @docs/internals/plugin-system.md - Plugin system detailed spec
- @docs/internals/payment-gateway.md - Payment gateway detailed spec
- @docs/internals/metrics-invoicegen.md - Metrics & invoice generation spec
- @docs/guides/integration.md - Integration guide for service developers
