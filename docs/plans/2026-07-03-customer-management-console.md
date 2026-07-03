# 顧客管理コンソール（Customer Management Console）実装計画

- **ステータス**: 計画（Draft）
- **作成日**: 2026-07-03
- **対象**: 新規独立リポジトリ `github.com/contract-to-cash/console`
- **関連リポジトリ**: `contract-to-cash/core`, `contract-to-cash/adapters`, `contract-to-cash/cli`

## 1. 背景と目的

core は BYO DB / BYO Gateway 型の課金ライブラリであり、契約・請求・入金のデータは
利用者のサービス内に蓄積される。しかし現状、そのデータを**人間が見るための手段**が
存在しない。サポート担当や経理担当が「この顧客の契約はどうなっているか」
「未入金の請求書はどれか」を確認するには、利用者ごとに管理画面を自作する必要がある。

本計画では、**core を組み込んだサービスに数行の配線で接続できる顧客管理 Web UI** を
独立リポジトリとして提供する。

### 提供する機能（ユーザーストーリー）

- 顧客（ユーザー）を名前・メール・AccountID で検索できる
- 顧客詳細ページで、契約・請求書・入金・クレジット残高・使用量を一覧できる
- 契約詳細ページで、状態遷移の履歴（イベントタイムライン）を確認できる
- 請求書詳細ページで、金額内訳（割引・税・クレジット充当）・入金状況・クレジットノートを確認できる
- ステータス別の横断ビュー（延滞請求書一覧、更新期限が近い契約、終了間際のトライアル）を見られる

## 2. ゴールと非ゴール

### ゴール

1. **接続の容易さが最優先**: core の利用者が既に持っているリポジトリ実装
   （`contract.Repository` 等）をそのまま渡せば動く。追加のデータストアを要求しない
2. **read-only から始める**: Phase 1〜2 は閲覧専用。書き込み操作（契約停止・請求書確定等）は
   Phase 3 のオプトイン機能
3. **core の設計原則を引き継ぐ**: BYO 思想（顧客マスタ・認証はポート IF で持ち込み）、
   マルチテナント非対応（ホスト側でフィルタ）、UTC 固定

### 非ゴール

- 顧客マスタ（名前・メール等）の**永続化を console 側で持つこと**
  → core と同じく BYO。ホストが `CustomerDirectory` ポートを実装する
- メータリング・課金操作の代替 UI（billing の主体はあくまでホストサービス）
- 汎用 BI / レポーティング基盤（KPI 集計はメトリクスフックの領域）
- マルチテナント分離・組織/権限管理（認証ミドルウェアごとホストに委譲）

## 3. 現状分析（3 リポジトリ調査結果の要約）

計画の前提となる事実。

### 3.1 core の読み取りサーフェス

- **Account / Customer エンティティは存在しない**。`shared.AccountID` は不透明な ULID 文字列で、
  名前・メール等の属性は core のどこにもない。「ユーザー検索」の実現には外部から
  顧客ディレクトリを持ち込む必要がある
- リポジトリ IF は account / contract スコープの `Find*` が充実している一方、
  **全メソッドにページネーションがない**。また「全顧客一覧」「全契約一覧」のような
  スコープなし一覧の入口が存在しない
- `payment.Repository` には `FindByAccountID` がない（invoice 経由で辿る必要がある）
- `application/query.TemporalQueryService.GetContractHistory` が契約単位の
  監査タイムライン（EventType / OccurredAt / UserID / Data）をそのまま返せる
- `eventstore.Store.LoadAll(fromPosition, limit)` はモジュール内で唯一
  カーソルページネーションを備えたグローバル読み取り。ただしイベントには
  AccountID タグがないため、アカウント別タイムラインには stream→account の
  対応付け（projection）が必要
- `application/projection` は Projector IF と実行基盤（同期/非同期、RebuildAll）のみ提供。
  具体的な read model は core には存在しない

### 3.2 adapters

- postgres / mysql が core の全リポジトリ IF + eventstore + TxManager + projector を実装済み。
  fincode は PaymentGateway / WebhookHandler のみ
- スキーマには **`contract_read_models` / `invoice_read_models`** という非正規化テーブルが既にあり、
  account_id / status / 日付にインデックスが張られている。ただしこれを引く
  ページネーション付きのクエリ API は存在しない
- **accounts / customers テーブルは存在しない**。account_id はカラムとして各テーブルに
  散在するのみ
- `port.CustomerGateway`（core 側 IF）にはアダプタ実装がない

### 3.3 cli（c2c）

- ランタイムではなく**スキャフォールディングツール**。`c2c init` が core を組み込んだ
  サービス雛形（HTTP サーバ・in-memory 配線付き）を生成する
- 生成された `internal/infrastructure/setup.go` が DI の集約点であり、
  console の配線を差し込む自然な拡張ポイントになる

### 3.4 計画への含意

| 事実 | 設計判断 |
|------|---------|
| 顧客エンティティがない | console が `CustomerDirectory` ポート IF を定義し、ホストが実装（BYO） |
| リポジトリにページネーションがない | console が独自の read ポート IF を定義し、複数の実装戦略を提供（§5.2） |
| adapters に read model テーブルがある | 公式アダプタ利用者向けに SQL 直読の高速実装を adapters 側に追加できる |
| 契約履歴 API が既にある | タイムライン UI は Phase 1 から低コストで実現可能 |
| cli が雛形生成する | `c2c` に console 配線の生成を追加して「数行で接続」を新規プロジェクトでも保証 |

## 4. 全体アーキテクチャ

### 4.1 リポジトリと配布形態

新リポジトリ: **`github.com/contract-to-cash/console`**（Go 1.25）

2 つの利用形態を提供する。**主は (a) 埋め込みライブラリ**。

**(a) 埋め込み `http.Handler`（推奨・主形態）**

ホストサービスが既に持っているリポジトリ実装を渡して `http.Handler` を作り、
自分の mux にマウントする。core 利用者にとって最も接続が簡単で、
DB 接続・認証・デプロイをすべてホストの既存基盤に相乗りできる。

```go
import "github.com/contract-to-cash/console"

h, err := console.New(console.Deps{
    Contracts:   contractRepo,   // ホストが既に持っている core IF の実装
    Invoices:    invoiceRepo,
    CreditNotes: creditNoteRepo,
    Payments:    paymentRepo,
    Balances:    balanceRepo,
    Usage:       usageRepo,
    Products:    productRepo,
    Prices:      priceRepo,
    EventStore:  eventStore,     // 省略可: タイムライン機能が無効になる
    Customers:   myCustomerDirectory, // 省略可: AccountID 直接入力のみになる
    Clock:       clock,
})
mux.Handle("/admin/", http.StripPrefix("/admin", h))
```

**(b) スタンドアロンバイナリ `cmd/console`（公式アダプタ利用者向け）**

postgres / mysql の DSN を設定するだけで単独プロセスとして起動する。
adapters を内部で配線する薄い main であり、コア部分は (a) と完全に共通。
サポートチームが本番 DB のリードレプリカに繋いで使う、といった運用を想定。

```bash
console serve --driver postgres --dsn "$DATABASE_URL" --listen :8686
```

### 4.2 依存方向

```
console (コア部分)  →  core のドメイン IF のみ（adapters に依存しない）
console/cmd/console →  console + adapters（スタンドアロン時のみ）
console/sqlread     →  adapters のスキーマ知識（オプションサブパッケージ、§5.2 戦略B）
ホストサービス       →  console + 自前のリポジトリ実装
```

core への依存はドメイン IF と `application/query` / `application/projection` /
`eventstore` の読み取り面に限定する。console から core への**書き込みは Phase 3 まで一切行わない**。

### 4.3 技術スタック

| レイヤ | 選定 | 理由 |
|--------|------|------|
| バックエンド | Go 標準 `net/http`（JSON API） | core と同じ思想。フレームワーク依存を持ち込まない |
| フロントエンド | React + TypeScript + Vite の SPA | 一覧・詳細・検索中心の UI に十分。エコシステムが安定 |
| 配布 | `go:embed` でビルド済み静的アセットを同梱 | `go get` だけで完結。利用者に Node.js を要求しない |
| API 形式 | 内部 JSON API（`/api/v1/...`）。公開 API とはみなさない | UI 専用のため互換性負担を最小化（semver 対象は Go の公開 IF のみ） |

CI でフロントエンドをビルドして embed 済みアセットをコミット（またはリリース時生成）する。
リポジトリ利用者のビルドフローに npm を混入させないことを必須要件とする。

## 5. 設計の要点

### 5.1 CustomerDirectory ポート（ユーザー検索の実現）

core に顧客エンティティがないため、console が検索用の最小 IF を定義し、
ホストが自分のユーザー DB を接続する。

```go
package console // (port パッケージ)

type Customer struct {
    AccountID shared.AccountID
    Name      string
    Email     string
    // 表示専用の追加属性
    Attributes map[string]string
}

type CustomerDirectory interface {
    // Search 名前・メール・ID の部分一致検索（実装はホストの自由）
    Search(ctx context.Context, query string, page PageRequest) (Page[Customer], error)
    // Get AccountID から顧客情報を引く（詳細画面のヘッダ表示用）
    Get(ctx context.Context, id shared.AccountID) (*Customer, error)
}
```

- `Customers` 未提供時のフォールバック: 検索ボックスは AccountID 直接入力として動作し、
  read ポート（§5.2）が返す account_id 一覧から匿名顧客としてナビゲートできる
- 将来、core の `port.CustomerGateway`（決済ゲートウェイの顧客）との連携表示は
  オプション機能として検討（未決事項 §10）

### 5.2 Read ポートと 3 つの実装戦略（ページネーション問題への回答）

core のリポジトリ IF にはページネーションがないため、console は
**UI が必要とする形の read ポート IF** を自分で定義する。

```go
type PageRequest struct{ Cursor string; Limit int }
type Page[T any] struct{ Items []T; NextCursor string }

type ContractReader interface {
    ListByAccount(ctx context.Context, id shared.AccountID, p PageRequest) (Page[ContractSummary], error)
    ListByStatus(ctx context.Context, s contract.ContractStatus, p PageRequest) (Page[ContractSummary], error)
    Get(ctx context.Context, id shared.ContractID) (*ContractDetail, error)
    History(ctx context.Context, id shared.ContractID) ([]TimelineEntry, error)
}
// InvoiceReader / PaymentReader / BalanceReader / UsageReader も同様
```

この IF に対して 3 つの実装を段階的に提供する。

| 戦略 | 実装 | 対象 | 提供時期 |
|------|------|------|---------|
| **A. core-repo バックド**（デフォルト） | ホストから受け取った core リポジトリ IF を呼び、メモリ上でソート・カーソル切り出し | 全 BYO DB 利用者。中小規模データなら十分 | Phase 1 |
| **B. SQL 直読**（`console/sqlread` または adapters 側） | adapters の `contract_read_models` / `invoice_read_models` 等をページネーション付き SQL で直接引く | 公式 postgres / mysql アダプタ利用者。大規模データ | Phase 2 |
| **C. 独自 projection** | core の `projection.Projector` IF で console 専用 read model（account 別タイムライン等）を構築 | アカウント横断のアクティビティフィードが必要な場合 | Phase 3（オプション） |

戦略 A の注意点を明示的に仕様化する:

- `payment.Repository` に `FindByAccountID` がないため、アカウントの入金一覧は
  「invoice.FindByAccountID → 各 invoice の payment.FindByInvoiceID」で合成する
- 一覧はリポジトリが全件返す前提のため、`Limit` 超過時は console 内でカーソル処理する。
  データ量が閾値を超える利用者には戦略 B / C を案内する（ドキュメント化）
- balance は per-currency API のため、UI は通貨タブ or 通貨横断サマリ（複数回呼び）で表示

### 5.3 画面構成（Phase 1〜2 のスコープ）

| 画面 | 内容 | データソース |
|------|------|------------|
| 顧客検索 | 検索ボックス + 結果一覧 | `CustomerDirectory.Search` |
| 顧客詳細 | プロフィール / 契約一覧 / 請求書一覧 / 入金一覧 / クレジット残高 | 各 Reader `ListByAccount` + `balance.GetBalance` |
| 契約詳細 | 状態・期間・価格・トライアル情報 / **イベントタイムライン** / 使用量サマリ | `ContractReader.Get` + `History`（= `TemporalQueryService.GetContractHistory`）+ `UsageReader` |
| 請求書詳細 | 金額内訳（小計・割引・税・クレジット充当・請求額）/ 入金履歴 / クレジットノート / 改訂チェーン | `InvoiceReader` + `PaymentReader` + CreditNote |
| 運用ビュー | 延滞請求書 / 更新期限が近い契約 / 終了間際トライアル / ステータス別契約 | `FindOverdue` / `FindDueForRenewal` / `FindTrialsEndingSoon` / `ListByStatus` |

タイムラインは core のイベントメタデータ（UserID 必須・OccurredAt）をそのまま
「誰がいつ何をしたか」の監査ビューとして表示する。金額はすべて `shared.Money` を
文字列化して表示（浮動小数点変換禁止を UI 層まで貫徹）。時刻は UTC で保持し、
ローカルタイム変換はブラウザ側で行う（core の設計決定に従う）。

### 5.4 認証・認可

- **埋め込み形態**: console は認証を実装しない。ホストが自前のミドルウェアで包む
  （マルチテナントのフィルタリングも同じ層でホストが行う）。
  ドキュメントに「認証なしで公開ネットワークにマウントしてはならない」と明記する
- **スタンドアロン形態**: 最低限の Basic 認証 + 将来 OIDC。読み取り専用 DSN
  （リードレプリカ）での接続を推奨手順とする

### 5.5 書き込み操作（Phase 3・オプトイン）

閲覧だけでなく「この画面から契約を停止したい」という要望は必ず出るが、
core の設計上、**契約ライフサイクル 5 種（Create/Activate/Suspend/Resume/Cancel）の
フック発火責任は統合者にある**（plugin-system.md §5.3）。console が集約メソッドを
直接呼ぶと、ホストが登録したフックの発火やホスト固有の業務処理を迂回してしまう。

したがって書き込みは**コマンドポート委譲方式**とする:

```go
type ContractCommands interface {
    Suspend(ctx context.Context, id shared.ContractID, cfg contract.SuspensionConfiguration) error
    Resume(ctx context.Context, id shared.ContractID) error
    Cancel(ctx context.Context, id shared.ContractID, reason string) error
}
```

- ホストが自分のアプリケーションサービス（フック発火・監査・権限チェック込み）で実装して渡す
- 未提供の操作は UI 上に表示されない（IF 単位のオプトイン）
- `BillingService.FinalizeInvoice` のようにコアのサービスが存在する操作も、
  直接呼ばず同じコマンドポート経由に統一する（トランザクション境界・冪等性の責任をホストに残す）

## 6. リポジトリ構成案

```
console/
├── go.mod                      # module github.com/contract-to-cash/console
├── console.go                  # New(Deps) → http.Handler（公開 API の中心）
├── port/                       # CustomerDirectory, Reader IF, Commands IF, Page 型
├── internal/
│   ├── api/                    # JSON API ハンドラ（/api/v1/...）
│   ├── readmodel/              # 戦略A: core リポジトリバックドの Reader 実装
│   ├── timeline/               # TemporalQueryService / eventstore ラッパ
│   └── webui/                  # go:embed される静的アセット + SPA 配信
├── sqlread/                    # 戦略B: adapters スキーマ直読 Reader（別 go.mod にするか検討）
├── cmd/console/                # スタンドアロンバイナリ（adapters を配線）
├── web/                        # フロントエンドソース（React + TS + Vite）
├── examples/embed-demo/        # core の examples と同型式の統合デモ
└── docs/
```

- `sqlread` と `cmd/console` は adapters に依存するため、コア部分の依存を汚さないよう
  **マルチモジュール構成（サブディレクトリに独立 go.mod）を第一候補**とする
  （adapters リポジトリが core を pin する構図と同じ）

## 7. 既存リポジトリへの影響

### core — Phase 1 では変更ゼロ（重要な検証ポイント）

console が既存 IF だけで成立することが「core の抽象が正しい」ことの実証になる。
その上で、開発中に見つかる不足は console 側で吸収せず issue 化して以下を検討:

- （Phase 2 提案）ページネーション付き一覧の**オプショナル拡張 IF**
  （例: `contract.PagedRepository`。実装していれば console が自動検出して戦略 A を高速化）
- （Phase 2 提案）`payment.Repository.FindByAccountID` の追加（破壊的変更にならない形で）
- docs/guides/integration.md に console 接続ガイドへのリンクを追加

### adapters — Phase 2 で read 実装を追加（オプション）

- `postgres/consoleread` / `mysql/consoleread`: console の Reader IF を
  `*_read_models` テーブル + LIMIT/カーソルで実装（戦略 B の本体）
- 既存スキーマの変更は不要。必要ならインデックス追加のみ

### cli — Phase 2 でスキャフォールディング対応

- `c2c init` のウィザードに「管理コンソールを含める」オプションを追加し、
  `setup.go` テンプレートに `console.New(...)` + mux マウントを生成
- `c2c add console`: 既存生成プロジェクトへの後付け配線 + `CustomerDirectory` スタブ生成

## 8. フェーズ計画

### Phase 0: リポジトリ立ち上げ（〜1 週）

- `contract-to-cash/console` リポジトリ作成、CI（lint / test / フロントビルド + embed 検証）、
  core と同水準の Makefile / golangci 設定
- port パッケージの IF 確定（このドキュメントの §5 を API 提案として issue 化しレビュー）

### Phase 1: 閲覧 MVP — 埋め込み形態のみ（2〜3 週）

- 戦略 A の Reader 実装（in-memory 実装でユニットテスト、core の `infrastructure/inmemory` を活用）
- JSON API + SPA: 顧客検索（CustomerDirectory + AccountID 直接入力）、顧客詳細、
  契約詳細 + タイムライン、請求書詳細、入金一覧
- `examples/embed-demo`（core の hosting-integration-demo と同じ in-memory 配線に console を載せる）
- 統合ガイド初版
- **Done の定義**: core 利用者が 10 行以内の diff で自サービスに閲覧 UI を追加できる

### Phase 2: 実運用対応（2〜3 週）

- 運用ビュー（延滞・更新期限・トライアル終了）
- 戦略 B: adapters 側 `consoleread` 実装 + スタンドアロンバイナリ `cmd/console`
- cli 対応（`c2c add console`）
- core への拡張 IF 提案（ページネーション、payment の account 検索）を issue + PR 化

### Phase 3: 書き込み操作 + 拡張（オプトイン、2 週〜）

- コマンドポート（§5.5）と UI 上の操作ボタン（提供 IF のみ表示）
- 戦略 C: アカウント横断アクティビティフィード用 projection
- スタンドアロン形態の認証強化（OIDC）

## 9. リスクと対応

| リスク | 対応 |
|--------|------|
| 戦略 A が大規模データで性能劣化（全件ロード + メモリページング） | ドキュメントで規模の目安を明記し、閾値超過は戦略 B/C へ誘導。Reader IF が同一なので移行はホスト側 1 行 |
| フロントエンド資産が Go リポジトリの運用を複雑化 | embed 済みアセットを CI で生成・検証し、Go 利用者には npm を一切要求しない。SPA は依存最小構成を維持 |
| 書き込み機能がホストのフック発火・監査を迂回する事故 | Phase 3 まで read-only を堅持。書き込みはコマンドポート委譲のみとし、console から集約メソッド・コアサービスを直接呼ばない |
| 「管理画面」の要望肥大化（権限管理、テナント分離、BI…） | 非ゴール（§2）を README に明記。core と同じ「基盤は提供、ポリシーはホスト」の線引きを守る |
| console 用 API を安定 API と誤解される | JSON API は internal 扱いと明記。semver の対象は Go の公開 IF（port パッケージ）のみ |

## 10. 未決事項（実装前に決める）

1. **リポジトリ／プロダクト名**: `console` / `admin` / `backoffice`。本計画では `console` を仮置き
2. **フロントエンド技術の最終確認**: React SPA か、依存をさらに絞るなら htmx + server-side rendering。
   UI の対話性（検索のインクリメンタル表示、タイムライン）を考慮し React を第一候補とする
3. **sqlread の置き場所**: console リポジトリ内サブモジュール vs adapters リポジトリ内。
   スキーマ知識の所有権の観点では adapters 側が自然（第一候補）
4. **core `port.CustomerGateway` との関係**: 決済ゲートウェイ上の顧客・支払い手段の表示を
   Phase 2 に含めるか
5. **i18n**: UI の日英対応をいつ入れるか（最低限、金額・日付の locale 表示は Phase 1 で対応）
