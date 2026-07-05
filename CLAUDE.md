# Contract-to-Cash Core

SaaS契約課金のOSSライブラリ（Go 1.25）。Event Sourcing + Plugin Architecture で契約・請求・決済を管理する。
利用者側がリポジトリ実装と決済ゲートウェイを持ち込む「BYO DB / BYO Gateway」型のライブラリ。

## 必須コマンド

```bash
make check              # build + lint + test（コミット前に必ず通す）
make test               # go test ./... -race -count=1
make test-unit          # tests/ 配下を除外した単体テスト
make test-integration   # tests/integration 配下
make lint               # vet + gofmt チェック + golangci-lint
```

詳細は `Makefile` を参照。`make cover` でカバレッジ、`make bench` でベンチマーク。

## アーキテクチャ（厳守事項）

### レイヤー構造（Clean Architecture + DDD）

```
domain/          純粋なドメインロジック。外部依存は stdlib + ulid のみ
application/     ユースケース層。domain にのみ依存
  service/         BillingService, PaymentService, CreditNoteService, SnapshotService
  port/            外部連携IF（PaymentGateway, WebhookHandler, IdempotencyStore, ...）
  query/           時点再構築クエリ（TemporalQueryService）
  projection/      Projection 更新（同期/非同期選択可能）
  tx/              トランザクション管理（TxManager, Saga）
eventstore/      Event Sourcing 基盤（Store, EventRegistry, Snapshot, Upcaster）
plugin/          プラグインシステム基盤（Registry と 20 種の Hook IF）
plugins/         公式プラグイン実装（coupon, tax, invoicecleanup）
batch/           バッチ処理ロジック（ContractRenewal 等。スケジューラは利用側）
infrastructure/  ドメイン IF の実装（現在は inmemory/ のみ。DB 実装は利用者が提供）
```

**絶対に守るルール:**

- `domain/` から外部パッケージへの依存は禁止（stdlib + `github.com/oklog/ulid/v2` のみ）
- `application/` は `domain/` のみに依存。`infrastructure/` には依存しない
- 依存の方向は常に外→内（Dependency Inversion）
- インターフェースは `domain/` または `application/port/` に定義し、実装は `infrastructure/` に置く
- パッケージ間の循環依存を絶対に作らない

### ドメインモデル

| エンティティ | 種別 | 特記 |
|---|---|---|
| Contract | Event Sourced Aggregate | 状態遷移: Draft→Trialing→Active→PastDue/Suspended→Cancelled/Expired |
| Invoice | Entity | 改訂チェーン（void-and-recreate）対応、2 レベルリンク（original / revisionOf） |
| CreditNote | Entity | 行項目レベルの調整。draft→issued→applied→refunded/voided |
| Payment | Entity | 冪等性キー必須、状態遷移あり |
| Price | Immutable Entity | Flat / Tiered(Graduated, Volume) / Usage の価格モデル |
| Product | Entity | 「何を売るか」を定義。Price（「どう課金するか」）と分離 |
| BalanceEntry | Entity | FIFO 消費、有効期限対応、楽観的ロック |

- 契約は `CreateContractCommand.PriceID` で Price を指定する（旧 `PlanID` は廃止済み）
- 課金サイクルは `pricing.BillingInterval`（`{unit, count}` の値オブジェクト）を使う。
  契約ドメインの `BillingCycle` スキャフォールディング（`CreateContractCommand.BillingCycle`、
  イベント/集約/スナップショットの `billing_cycle` フィールド、`GetBillingCycle()`、
  `Renew(BillingCycle)`、`domain/contract` の型エイリアス）は #111 で撤去済み。
  歴史的イベント（`billing_cycle` のみを持つペイロード）は `domain/contract/upcaster.go` の
  Upcaster が `interval` へ変換する（SchemaVersion 2）。
  `pricing.BillingCycle`（文字列型 + `Daily/Weekly/Monthly/Yearly` 定数、`BillingCycleToInterval` /
  `BillingInterval.ToBillingCycle`）は Price 構築・表示・アダプタ用に pricing パッケージ内でのみ残存する
- `shared.MetricName`（型付き string）を使用。生の string でメトリック名を渡さない

### Event Sourcing

- イベントは不変・追記のみ（append-only）
- `EventRegistry` で型安全なデシリアライズ
- **新イベントを追加するときは必ず `Register()` と集約の `Apply()` 型 switch の両方を更新する**
- `SchemaVersion` フィールドで将来的な Upcaster 対応
- スナップショット: `DefaultSnapshotInterval`（100 イベントごと、変更可能）
- 並行制御は version-based の楽観的ロック

### プラグインシステム

コアが会計基準に則った計算順序を構造的に保証する:

```
BeforeCalculation → 価格計算 → Discount → Subtotal → Tax → Total → Credit適用 → Invoice生成 → AfterCalculation
```

- **ISP 準拠**: 必要な Hook インターフェースのみ実装する。空メソッドの強制実装は不要
- `Priority` は **同一 Hook 種別内** の実行順序のみを制御する。Hook 種別間の順序はコアが保証する
- 全 20 種の Hook は 6 カテゴリに分類される（請求計算 3 / 契約ライフサイクル 7 / 支払い 4 / メトリクス 3 / クレジットノート 2 / 請求書生成 1）
- **発火責任は非対称**: コアが自動発火するのは 14 種のみ。契約ライフサイクル 5 種
  （Create/Activate/Suspend/Resume/Cancel）は統合者、`InvoiceGenerationHook` はアダプタが発火する
  （詳細は plugin-system.md セクション 5.3 の発火責任表）

Hook の完全な一覧と設計意図は @docs/internals/plugin-system.md を参照。

## コーディング規約

- **時刻**: `time.Now()` は使わない。必ず `shared.Clock` IF 経由（テストでは `shared.FixedClock`）
  - **唯一の例外**: `domain/shared/identifier.go` の `generateULID()`。ULID のタイムスタンプ部（ID をソート可能にするためだけの値）に `time.Now()` を直接使う。この時刻はドメインの時間ロジックには一切使われないため注入不要。他の箇所で `time.Now()` を直呼びしてはならない
- **金額**: `big.Rat` ベースの `shared.Money` を使う。浮動小数点演算は禁止。`Money.Float64()` は表示・ログ専用で金額演算に使わない（丸め誤差）。演算には `Add`/`Subtract`/`Multiply` を使う
- **ID 生成**: `github.com/oklog/ulid/v2`。ID 型は具体的な名前（`ContractID`, `InvoiceID` 等）を使い、汎用 `ID` は作らない
- **エラー**: `shared.DomainError` + `shared.ErrorCode` で構造化。ビジネスエラーと技術エラーを区別する
- **タイムゾーン**: すべて UTC。ローカルタイムへの変換は表示層の責務
- **命名**: 公開は `PascalCase`、非公開フィールド/メソッドは `camelCase`
- **イベント型名**: `"domain.action"` 形式（例: `"contract.created"`, `"contract.activated"`）

### テスト

- `-race` で常に実行（`make test` のデフォルト）
- 時刻は `shared.FixedClock{FixedTime: ...}` で固定
- リポジトリ依存は `infrastructure/inmemory/` の実装を利用
- ユニットテストは対象パッケージ内に配置、統合テストは `tests/integration/`、E2E は `tests/e2e/`

### Lint

golangci-lint の設定は `.golangci.yml`。`exhaustive` で switch の網羅性を強制し、`nolintlint` で `//nolint` に理由を要求している。Claude 側で style を気にする必要はない（lint に任せる）。`examples/` は lint 除外。
- `forbidigo` リンター有効: `ToSnapshot` / `FromSnapshot` / `InvoiceFromSnapshot` / `CreditNoteFromSnapshot` は persistence adapter 専用。`domain/*/snapshot*.go`, `infrastructure/`, `tests/integration/` 以外から呼び出すと CI が落ちる（issue #100）。`ContractAggregate.MarshalSnapshot` / `LoadFromSnapshot` は event-sourced 用の別 API なので対象外（word-boundary `\b` で除外済み）。
- 新規 lint ルール追加時は `tests/lintcheck/` に enforcement 検証テストを置く（`make test-lint-rules`）

## 変更時の注意事項

### 破壊的変更の回避

- **イベントスキーマの変更は既存イベントを壊さない**こと。`SchemaVersion` + Upcaster で対応する
- ドメインの状態遷移ルールを変更する場合、既存のイベント履歴からの再構築が壊れないか確認する
- プラグイン Hook のシグネチャ変更は全プラグイン実装に影響する

### 変更時のチェックリスト

- [ ] `domain/` に外部依存を持ち込んでいない
- [ ] 新しいドメインイベントを `EventRegistry.Register()` と集約の `Apply()` 両方に追加した
- [ ] `Money` 演算で通貨一致を検証している
- [ ] 状態遷移が既存フローと矛盾しない
- [ ] `shared.Clock` を使っている（`time.Now()` の直接呼び出しがない）
- [ ] テストが `-race` で通る
- [ ] `ProductID` + `PriceID` を使っている（`PlanID` は廃止済み）
- [ ] `make check` が通る

## 詳細ドキュメント

常時ロードされるリファレンス（メンタルモデル）:

- @docs/architecture.md — 全体アーキテクチャ概観
- @docs/decisions/design-decisions.md — 設計決定事項と選択理由

以下は分量が多いため **必要になったときだけ Read ツールで開く**（自動ロードしない）:

- `docs/internals/domain-model.md` — ドメインモデル詳細仕様
- `docs/internals/event-sourcing.md` — Event Sourcing 詳細仕様
- `docs/internals/plugin-system.md` — プラグインシステム詳細仕様（Hook IF 全 20 種の定義）
- `docs/internals/payment-gateway.md` — Payment Gateway 詳細仕様
- `docs/internals/metrics-invoicegen.md` — メトリクス集計 & 請求書生成 Adapter
- `docs/guides/integration.md` — サービス開発者向け統合ガイド
