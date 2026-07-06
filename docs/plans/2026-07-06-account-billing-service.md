# アカウント管理 + 契約請求決済サービス 設計計画

- 日付: 2026-07-06
- ステータス: Draft（技術選定確定済み、実装リポジトリ未作成）
- 関連: `docs/architecture.md`, `docs/internals/plugin-system.md`, `contract-to-cash/adapters`, `contract-to-cash/console`

## 1. サービス概要

toB 向けのマネージドサービス。利用企業（テナント）は本サービスに
**エンドユーザーのアカウント管理（OIDC）** と **契約・請求・決済管理** を移譲し、
自社のコア事業価値に注力できる。クーポン・トライアル・ダニング等の複雑な課金処理を
サービス立ち上げ初日から利用できる。

```
テナントのアプリ ──(OIDC RP)──> Keycloak（realm-per-tenant）
        │                            │ Admin API / イベント連携
        │ REST API                   ▼
        └──────────────> 本サービス（新規開発）
                          ├─ Control Plane: テナント管理・プロビジョニング
                          ├─ Billing API: core 統合層（契約/請求/決済/クーポン/トライアル）
                          │    └─ contract-to-cash/core + plugins(coupon, tax)
                          ├─ Webhook Bridge: core の 20 フック → テナント登録エンドポイントへ配送
                          ├─ console 埋め込み（テナント別管理画面）
                          └─ adapters/postgres（テナント別 DB）+ adapters/stripe
```

## 2. 確定した技術選定

| 項目 | 決定 | 理由 |
|---|---|---|
| アイデンティティ | **Keycloak**（realm-per-tenant） | 独自実装を避けコストを下げる。OIDC OP / ユーザー管理 / MFA / ソーシャルログインを既製品で賄う |
| 決済ゲートウェイ | **Stripe**（`adapters/stripe`） | カード / JPY・USD・EUR 対応済み。Webhook 検証・冪等性・エラーマッピング実装済み |
| テナント分離 | **DB-per-tenant**（PostgreSQL） | core は設計上マルチテナントを利用者に委ねる。物理分離が最も安全で、`adapters/postgres` をテナントごとに素直に使える |
| フック拡張 | **全 20 フックをテナント登録 Webhook として公開** | テナントはプラグインを Go で書かずに、自社エンドポイントの URL 登録だけで課金パイプラインに介入・追従できる |

## 3. アイデンティティ層（Keycloak）

### 3.1 方針: 独自実装ゼロ

OIDC プロバイダ機能・ユーザー CRUD・パスワード/MFA・セッション管理はすべて Keycloak に委譲する。
本サービスが書くのは以下の薄い接着層のみ:

1. **プロビジョニング**: テナント作成時に Keycloak Admin API で realm・クライアント・
   ロールを作成する（Terraform/keycloak-config-cli の宣言的構成も可）
2. **アカウントマッピング**: Keycloak のユーザー/組織 ↔ core の `shared.AccountID` の
   対応表（テナント DB 内）。B2B2B のため「アカウント = テナントの顧客企業」を単位とし、
   Keycloak 側は organization（または group）で表現、配下ユーザーは organization メンバー
3. **エンティタイトルメント API**: `GET /v1/entitlements?account_id=...` —
   契約状態（Active/Trialing/Suspended/PastDue）から利用可否・プラン内容を返す。
   テナントアプリはログイン後にこれを参照してアクセス制御する

### 3.2 未払い時のアクセス制御

core の Payment-Gated Provisioning フロー（architecture.md §5、既存の状態遷移のみで実現）
をそのまま使う: 初回入金確認まで `Active → Suspended`、入金確認で `Resume`。
認可判断は **Keycloak のトークンに課金状態を埋め込まない**（トークン寿命と課金状態の
鮮度が合わない）。エンティタイトルメント API を正とし、必要なら将来 Keycloak SPI /
protocol mapper でクレーム化を検討する。

### 3.3 運用者（console）の認証

console は OIDC を自前実装しない設計（`Deps.Identity` に委譲）。運用者用 realm を
1 つ用意し、oauth2-proxy（または in-app go-oidc）を console の前段に置いて
`Identity` リゾルバでヘッダから運用者を解決する。`Authorize` でテナント境界と
ロール（billing-support 等）を強制し、`Audit` は既存の `NewSlogAuditLog` + 永続化。

## 4. テナントモデル（DB-per-tenant）

### 4.1 データ配置

| データ | 置き場所 |
|---|---|
| テナント台帳・プロビジョニング状態・DB 接続情報（暗号化）・Stripe/Keycloak 参照 | **Control Plane DB**（1つ） |
| core の全テーブル（イベントストア、契約/請求/決済/残高/価格/商品/使用量、read model） | **テナント DB**（`adapters/postgres` のマイグレーション） |
| アカウントマッピング、Webhook エンドポイント設定、Webhook アウトボックス、監査ログ | **テナント DB**（本サービス独自マイグレーションを追加） |

### 4.2 プロビジョニングフロー（テナントオンボーディング）

```
1. Control Plane にテナント登録
2. テナント DB 作成 → postgres.Migrate() + 本サービス独自マイグレーション
3. Keycloak realm 作成（Admin API）
4. テナントの Stripe シークレットキー / Webhook シークレット登録（BYO Stripe アカウント）
5. スモークテスト（ヘルスチェック）→ 有効化
```

各ステップは冪等に実装し、途中失敗は再実行で収束させる（Saga は不要、前進のみ）。

### 4.3 ランタイム

- リクエストの テナント解決（サブドメイン or パス `/t/{tenant}` or API キー）
  → 接続プールキャッシュ → **テナント別サービスコンテナ**
  （`BillingService` / `PaymentService` / `CreditNoteService` / `plugin.Registry`）を
  lazy 構築してキャッシュ
- `plugin.Registry` はテナントごとに独立。公式プラグイン（coupon / tax）+
  Webhook Bridge プラグイン（§6）をテナント設定に応じて登録する
- バッチ（InvoiceGenerator / ContractRenewal / PaymentRetry / TrialExpiration /
  BalanceExpiration）はスケジューラ（cron / Cloud Scheduler）が
  テナント台帳を走査して各テナント DB に対して実行。処理ロジックは core の `batch/` を使用

### 4.4 Stripe の持ち方: BYO アカウント

テナント自身の Stripe アカウント（シークレットキー持ち込み）で運用する。
資金はテナント ↔ その顧客間で直接動き、本サービスは資金を預からない
（資金移動業の論点を回避）。`adapters/stripe` はキーごとにインスタンス化するだけで対応可能。
将来マーケットプレイス型が必要になれば Stripe Connect を別途検討。

Stripe Webhook はテナント別エンドポイント `/t/{tenant}/stripe/webhook` で受け、
`stripe.WebhookHandler`（署名検証）→ core `port.WebhookProcessor`（時刻検証・ID デデュープ）
に流す。ダニングバッチ等の長期リトライは Stripe の冪等キー窓（約24h）を超えるため、
adapters README の推奨どおり core `port.IdempotencyStore` を併用する。

## 5. Billing API（core 統合層）

core の思想（アプリケーションサービスがある操作はそれを呼ぶ、ない操作は集約を直接呼ぶ）
に従い、REST API として公開する。統合者責務のフック発火を必ず実装する:

- **契約ライフサイクル 5 種**（Create/Activate/Suspend/Resume/Cancel）は core に
  アプリケーションサービスがないため、本サービスの API ハンドラが
  集約操作 → 保存 → `registry.GetOnContract*Hooks()` ループ発火を行う
  （参照実装: `examples/hosting-integration-demo/main.go`）
- `OnContractRenew` / `OnContractTrialEnd` / `OnContractChange` は core の
  バッチプロセッサが発火するため追加実装不要
- `InvoiceGenerationHook` は請求書レンダリング（PDF 等）を実装するフェーズで
  本サービスのレンダリングアダプタが発火する

クーポン（`plugins/coupon`）・トライアル（`TrialConfiguration`）・部分入金・
クレジットノート等は core の機能をそのまま API として面出しする。

## 6. Webhook Bridge（フックのテナント公開）— 本サービスの中核機能

### 6.1 設計方針

テナント DB ごとに **WebhookBridge プラグイン**（core の全 20 フック IF を実装する
単一プラグイン）を `plugin.Registry` に登録する。テナントは管理 API/UI から
「エンドポイント URL + シークレット + 購読フック種別」を登録するだけで、
フック発火時に自社エンドポイントが呼ばれる。

フックは性質が 2 系統に分かれるため、配送モデルを分ける:

### 6.2 通知型フック（15 種）→ 非同期配送（アウトボックス + at-least-once）

対象: `OnContractCreate/Activate/Suspend/Resume/Cancel/Renew/TrialEnd`、
`AfterCharge`、`OnPaymentFailed`、`OnRefund`、`OnInvoiceIssued`、`OnPaymentProcessed`、
`OnContractChange`、`OnCreditNoteIssued`、`OnInvoiceRevised`

これらは core 側で「非致命（エラーはログのみ）」として発火される通知イベント。
HTTP を同期で叩くのは禁物（plugin-system.md 5.3 の注意: tx ジョイン時は外側コミット
**前**に発火し得る = 未確定データの通知リスク）。よって:

1. フック実装は **テナント DB のアウトボックステーブルに行を書くだけ**
   （プラグインはコアと同一 tx で動くため、コミットと原子的に確定し、
   ロールバック時はイベントも消える。「保存されていないのに通知済み」が構造的に起きない）
2. 配送ワーカーがコミット済みアウトボックスをポーリングし、HTTP POST で配送
3. **at-least-once + コンシューマ側デデュープ前提**。イベント ID は決定的に生成
   （`フック種別 + エンティティID + エンティティ状態バージョン` のハッシュ）。
   core の冪等リプレイで同一フックが複数回発火してもアウトボックスの一意制約で収束
4. リトライは指数バックオフ（例: 1m→5m→30m→2h→8h、最大 3 日）。連続失敗で
   エンドポイントを自動無効化し、テナントに通知
5. 署名: Stripe 互換方式（`t={timestamp},v1=HMAC-SHA256(secret, "{timestamp}.{body}")`）。
   タイムスタンプ許容窓でリプレイ攻撃を防ぐ
6. ペイロードは JSON。金額は `shared.Money`（big.Rat）を **文字列 decimal** で表現し、
   浮動小数点化しない。`schema_version` フィールドで将来の変更に備える

### 6.3 計算介入型フック（5 種）→ 同期リクエスト/レスポンス（オプトイン）

対象: `DiscountHook.CalculateDiscount`、`TaxHook.CalculateTax`、
`InvoiceLifecycleHook.BeforeCalculation/AfterCalculation`、`BeforeChargeHook.BeforeCharge`

これらは戻り値（金額）が請求額に反映される / エラーで処理を中断できるフック。
Webhook 化する場合は課金パイプラインの中で同期 HTTP を呼ぶことになるため、
**デフォルト無効・エンドポイント単位のオプトイン** とし、ガードを義務付ける:

- タイムアウト: デフォルト 3 秒（設定上限 10 秒）
- 失敗ポリシーをテナントが選択:
  - `fail_open`（既定）: タイムアウト/エラー時は割引ゼロ・税ゼロ・処理続行として扱う
  - `fail_closed`: 請求生成/課金を中断（後続リトライはテナント運用に委ねる）
- レスポンススキーマ例（discount/tax）: `{"amount": "1000", "currency": "JPY"}` —
  文字列 decimal、通貨不一致は拒否。割引上限ガード（subtotal 超過 cap）は core が既に保証
- `BeforeCharge` は boolean 判断（`{"allow": false, "reason": "..."}` で課金中止）
- 冪等: 同一請求計算内での再試行に備え、リクエストに決定的な `calculation_id` を含める

`InvoiceGenerationHook`（BuildDocument/AfterRender/AfterDelivery）は請求書レンダリング
実装フェーズで扱う: BuildDocument はドキュメントへの追記を許す同期型、
AfterRender/AfterDelivery は通知型としてアウトボックスに乗せる。

### 6.4 登録・管理

- Webhook エンドポイント CRUD は本サービスの管理 API + 管理 UI で提供する。
  console は read-only 設計（書き込みは Phase 3 のコマンドポート待ち）のため、
  console には組み込まず本サービス側 UI に置く。console Phase 3 が来たら移設を検討
- エンドポイントごとに: URL / シークレット（表示は登録時のみ）/ 購読フック種別の集合 /
  同期フックのオプトイン + 失敗ポリシー / 有効・無効
- 配送ログ（試行履歴・レスポンスコード・所要時間）と手動再送を提供する

## 7. console の組み込み

- テナント別に `/t/{tenant}/admin/` へマウント。`Deps` にはそのテナントの
  リポジトリ実装（adapters/postgres）とイベントストアを渡す
- `port.CustomerDirectory` はテナント DB のアカウントマッピング +
  Keycloak Admin API（メール/名前検索）で実装する
- `Identity` / `Authorize` / `Audit` は §3.3 のとおり

## 8. 実装フェーズ

| フェーズ | 内容 | 主な成果物 |
|---|---|---|
| 1 | Control Plane + プロビジョニング | テナント CRUD、テナント DB 作成 + `postgres.Migrate`、Keycloak realm 自動作成 |
| 2 | Billing API 縦切り | 契約作成→有効化→請求書生成→Stripe 決済の一連 + ライフサイクルフック発火 + Stripe Webhook 受信 |
| 3 | Webhook Bridge（通知型） | アウトボックス + 配送ワーカー + 署名 + リトライ + 登録 API/UI |
| 4 | console 組み込み + エンティタイトルメント API + Payment-Gated Provisioning |
| 5 | Webhook Bridge（同期型オプトイン） | discount/tax/before-charge の同期 Webhook |
| 6 | バッチ運用（更新・トライアル終了・ダニング・残高失効）+ クーポン/トライアル API 面出し |

フェーズ 2 の骨格生成には `c2c init` を利用できる（in-memory 実装を adapters/postgres に
差し替える起点として）。

## 9. 未決事項

1. **実装リポジトリ**: 本サービスのコードを置く新リポジトリ（例:
   `contract-to-cash/platform`）が必要。現行 4 リポジトリ（core/adapters/console/cli）は
   OSS として汎用に保ち、サービス固有コードは分離する
2. テナント解決方式の最終決定（サブドメイン vs パス vs API キーのみ）
3. Keycloak の organization 機能（KC 26+）vs group ベースのアカウント表現
4. 同期 Webhook の SLA・課金プランへの反映（タイムアウトはテナント起因の遅延要因になる）
