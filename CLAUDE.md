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
infrastructure/  → domain/applicationのインターフェース実装
plugin/          → プラグインシステム基盤
plugins/         → 公式プラグイン実装
```

**絶対に守るルール:**
- `domain/` は外部パッケージに依存してはならない（標準ライブラリのみ）
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
| BalanceEntry | Entity | FIFO消費、有効期限対応 |

### Event Sourcing

- イベントは不変・追記のみ（append-only）
- `EventRegistry` で型安全なデシリアライズ
- `SchemaVersion` フィールドでイベントスキーマのバージョン管理
- スナップショット: N件ごと（デフォルト100）で最適化
- 楽観的ロック（version-based）で並行制御

### プラグインの計算順序（コアが保証）

```
BeforeCalculation → 価格計算 → Discount → Subtotal → Tax → Total → Credit適用 → Invoice生成 → AfterCalculation
```

- プラグインはISP準拠：必要なHookインターフェースのみ実装
- 空メソッドの強制実装は不要
- `Priority` は同一Hook内の実行順序のみ制御

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
- `examples/` ディレクトリはlint除外

## 変更時の注意事項

### 破壊的変更の回避

- イベントスキーマの変更は `SchemaVersion` + Upcasterで対応。既存イベントは絶対に変更しない
- ドメインの状態遷移ルールを変更する場合、既存のイベント履歴からの再構築が壊れないか確認
- プラグインHookのシグネチャ変更はすべてのプラグイン実装に影響する

### 追加・変更チェックリスト

- [ ] `domain/` に外部依存を持ち込んでいないか
- [ ] 新しいドメインイベントを `EventRegistry` に登録したか
- [ ] `Money` 演算で通貨の一致を検証しているか
- [ ] 状態遷移が既存のフローと矛盾しないか
- [ ] `Clock` インターフェースを使っているか（`time.Now()` を使っていないか）
- [ ] テストが `-race` で通るか

## 詳細ドキュメント

- @docs/architecture.md - アーキテクチャ全体像
- @docs/decisions/design-decisions.md - 設計判断とその理由
- @docs/design/domain-model.md - ドメインモデル詳細
- @docs/design/event-sourcing.md - Event Sourcing設計
- @docs/design/plugin-system.md - プラグインシステム設計
- @docs/design/payment-gateway.md - 決済ゲートウェイ設計
- @docs/guides/usage-guide.md - サービス側での組み込みガイド
- @docs/reviews/codebase-review-20260327.md - コードレビュー結果・既知の課題
