# 技術調査レポート: 決済における冪等性キーと補償後リトライの業界プラクティス

**調査日**: 2026-04-10
**トピック数**: 6
**目的**: Issue #87 (補償Refund後の同一IdempotencyKeyリトライによる不整合) の設計判断のための業界調査

---

## エグゼクティブサマリー

| 論点 | 業界の回答 |
|---|---|
| 補償後の同一キー再利用は可能か | **NO**。Stripe/PayPal/Adyen/GMO すべて、キーは「1論理オペレーションにつき1つ」。補償後の retry は新しいキーで実行する前提 |
| 「同じキーで retry」は何を意味するか | ネットワークタイムアウト等で**結果が不明なまま**失敗した1回の呼び出しをもう一度試す、という意味。**補償が成功して完了した後**のリトライは対象外 |
| 補償後の正しいリトライ方法 | **新しい idempotency key を発行して新規チャージとして実行する** (業界標準) |
| ローカル DB save 失敗のリカバリ戦略 | Brandur の atomic phases + recovery points パターン、または Temporal の confirm-before-repeat パターン、または webhook ベースの reconciliation |
| 本ライブラリで `GetTransaction` による状態推論は必要か | **不要**。DB 永続化された補償マーカーをチェックして新キー発行するのがシンプルかつ堅牢 |

**結論**: Issue #87 の初期実装(GetTransactionベース)ではなく、**補償マーカー方式 (新キー発行)** が業界標準と一致する。これはセルフレビューで推奨した方針と完全に一致。

---

## 1. Stripe: idempotency key の仕様と refund 後の挙動

### 概要

Stripe は `Idempotency-Key` HTTP ヘッダで冪等性を提供。キーは**24時間**キャッシュされ、その間の再リクエストは元の結果(HTTPステータス・ボディ)を**そのまま**返す。

### 仕様

| 項目 | 内容 |
|---|---|
| ヘッダ名 | `Idempotency-Key` |
| 推奨形式 | V4 UUID |
| TTL | 24 時間 (最初にキーを受け取った時刻基準) |
| API v1 挙動 | 成功・失敗問わず、最初のレスポンス(status code + body)を完全にそのまま返す |
| API v2 挙動 | 初回成功なら新しい状態を返す。初回失敗なら再実行する |
| レスポンスヘッダ | `Idempotent-Replayed: true` (リプレイ時に付与) |
| 異なる params で同じキー | エラー返却 |

### 補償 Refund 後の同一キー再利用は想定外

Stripe の公式ドキュメントとブログでは、「Refund 後に同じ idempotency_key で `/v1/charges` や `/v1/payment_intents` を叩いたらどうなるか」は**明示的に定義されていない**。なぜなら**想定外のユースケース**だから:

- Stripe の idempotency key は「タイムアウト等で結果不明になった1回の呼び出しのリトライ」のために設計されている
- 「Charge 成功 → Refund → 同じキーで retry」は設計意図に反するユースケース
- 24h 以内は cached レスポンス(元の Charge 成功結果)がそのまま返ってくる = Issue #87 の現象

### Stripe 公式ブログの設計思想

> "Clients can safely retry requests that include an idempotency key as long as the second request occurs within 24 hours from when you first receive the key"

→ キーは「同じ論理オペレーションを最大24時間内でリトライする」ためのもの。補償後は別の論理オペレーションとして**新しいキー**を発行すべき。

### 実装例 (stripe-node SDK の挙動)

```typescript
// stripe-node SDK のリトライは "ネットワーク失敗 → 同じキーで再試行" のみ
// 補償後のリトライは呼び出し元アプリの責務
async function charge(params, options) {
  const idempotencyKey = options.idempotencyKey ?? `stripe-node-retry-${uuid()}`;
  // 最大 maxNetworkRetries 回、同じキーで指数バックオフリトライ
  // - ECONNRESET, ECONNREFUSED, 500 等のみリトライ対象
  // - 成功レスポンス(2xx or 4xx idempotent replay) は即座に返却
}
```

### 参考URL

- [Stripe API Reference - Idempotent requests](https://docs.stripe.com/api/idempotent_requests)
- [Stripe Blog - Designing robust and predictable APIs with idempotency](https://stripe.com/blog/idempotency)
- [DeepWiki - stripe-node Idempotency and Retry Logic](https://deepwiki.com/stripe/stripe-node/3.5-idempotency-and-retry-logic)

---

## 2. Brandur Leach の Stripe-like atomic phases パターン

### 概要

Stripe の元エンジニア Brandur Leach が公開した、Postgres ベースの冪等性キー実装パターン。現代の決済サガ設計の**業界標準リファレンス**。Rocket Rides という仮想配車アプリを題材に、**atomic phases** と **recovery points** の概念を定義。

### 核心概念: Atomic Phase と Recovery Point

- **Atomic phase**: 外部 API 呼び出しと外部 API 呼び出しの**間**にあるローカル DB 変更の塊。1 DB トランザクションで実行
- **Recovery point**: 各 atomic phase 完了後にキーに記録するチェックポイント

### テーブルスキーマ

```sql
CREATE TABLE idempotency_keys (
    id              BIGSERIAL PRIMARY KEY,
    idempotency_key TEXT      NOT NULL,
    user_id         BIGINT    NOT NULL,
    locked_at       TIMESTAMP,            -- 並行リトライ防止
    request_method  TEXT      NOT NULL,
    request_params  JSONB     NOT NULL,
    request_path    TEXT      NOT NULL,
    response_code   INT,
    response_body   JSONB,
    recovery_point  TEXT      NOT NULL,   -- 状態マシン
    created_at      TIMESTAMP NOT NULL,
    UNIQUE (user_id, idempotency_key)
);
```

`recovery_point` の状態遷移 (Rocket Rides の例):

```
started ─tx2─> ride_created ─tx3─> charge_created ─tx4─> finished
  ↑           ↑                   ↑                     ↑
  tx1:        ローカルDB挿入        Stripe Charge API     メール送信
  key挿入                          (外部)               ジョブキュー
```

### 失敗時のリカバリ

- リトライ時はキーを参照して `recovery_point` を読み、そこから再開
- 24h TTL を超えた古いキーは **reaper** ジョブで削除
- 並行リトライは `locked_at` で排他制御し 409 を返す

### 補償の扱い

Brandur 記事の重要な指摘: **「一度外部システムに書き込んだら引き返せない。補償を避ける設計を優先せよ」**

> "once we make our first foreign state mutation, we're committed one way or another. We've pushed data into a system beyond our own boundaries."

つまり**補償 Refund は例外的な安全弁**であり、発生したら「このキーで表される論理オペレーションは失敗扱いで終了」と見なすのが正しい設計。リトライしたいなら**新しい論理オペレーション = 新しいキー**で実行する。

### この設計の本ライブラリへの応用

本ライブラリは Stripe 風のフル atomic phase は過剰だが、**「補償が発火したキーはもう使わない」** というエッセンスだけ借りればよい。これが「補償マーカーテーブル」方式の原点。

### 参考URL

- [Implementing Stripe-like Idempotency Keys in Postgres — brandur.org](https://brandur.org/idempotency-keys)
- [GitHub - brandur/rocket-rides-atomic (リファレンス実装)](https://github.com/brandur/rocket-rides-atomic)
- [sorg/idempotency-keys.md (ソース)](https://github.com/brandur/sorg/blob/master/content/articles/idempotency-keys.md)

---

## 3. Temporal / Restate: サガでの per-step idempotency key

### 概要

Temporal (ワークフローエンジン) のコミュニティでは、**サガのステップごとに個別の idempotency key を発行する**のが標準プラクティス。補償ステップも含めて、**全ステップが独自のキーを持つ**。

### 核心原則: "Stable keys generated in workflow state, not in activity"

```go
// ❌ BAD: activity 内で UUID 生成 → リトライで毎回新しいキー → 重複課金
func ChargeActivity(ctx context.Context, invoiceID string) error {
    key := uuid.New().String()  // リトライ毎に変わる!
    return stripe.Charge(key, ...)
}

// ✅ GOOD: workflow 状態でキーを生成 → activity に渡す
func PaymentWorkflow(ctx workflow.Context, invoiceID string) error {
    chargeKey := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
        return uuid.New().String()
    }).Get(...)  // 決定的、リトライ間で安定
    
    workflow.ExecuteActivity(ctx, ChargeActivity, invoiceID, chargeKey)
}
```

### Confirm-Before-Repeat パターン

Temporal コミュニティの推奨: **タイムアウト時は即座に再試行せず、まず照会する**:

```
タイムアウト発生
  ↓
照会: "do you have a charge for idempotency_key = X?"
  ├── YES → 成功扱い (既に課金済み)
  └── NO  → 再試行 (新しいキー or 同じキー)
```

これは **Issue #87 の初期実装で採用した「GetTransaction で状態確認」と同じ思想**。ただし Temporal でもこれは**タイムアウト直後の1回限り**のリカバリであり、「補償完了後のリトライ」用ではない。

### Per-Step キー命名の典型例

```
saga_{sagaId}_step_{stepName}_{attemptNumber}

例:
  saga_abc123_step_charge_1         (初回 charge)
  saga_abc123_step_refund_1         (補償 refund)
  saga_abc123_step_charge_2         ← 補償後の再試行は別ステップ扱い = 新キー
```

**補償後のリトライは「別のステップ」として扱い、新しいキーで実行する**のが Temporal/Restate 流。これが業界のベストプラクティス。

### 「補償も冪等であるべき」原則

> "Compensation must be idempotent. Compensation is just another money movement."

補償 Refund 自体にも idempotency key が必要。本ライブラリは既に `comp-refund-{txnID}` という形で実装済み(`payment_service.go:270`)、ここは問題なし。

### 参考URL

- [Temporal workflows: 10 idempotency slips that duplicate money (Medium)](https://medium.com/@bhagyarana80/temporal-workflows-10-idempotency-slips-that-duplicate-money-30e2fb8d5d90)
- [Restate - Sagas Guide](https://docs.restate.dev/guides/sagas)
- [Temporal Blog - Saga Pattern Made Easy](https://temporal.io/blog/saga-pattern-made-easy)

---

## 4. Adyen / PayPal / GMO: 主要決済サービスの横並び比較

### 比較表

| 項目 | Stripe | Adyen | PayPal | GMO PG |
|---|---|---|---|---|
| ヘッダ名 | `Idempotency-Key` | `Idempotency-Key` | `PayPal-Request-Id` | `Idempotency-Key` |
| 推奨形式 | UUID v4 | UUID v4 (max 64 chars) | 任意の一意 ID | UUID v4 |
| TTL | 24 時間 | **最低 7 日** | **最大 45 日** | 未公開 (OpenAPI タイプで対応) |
| スコープ | アカウント単位 | **company account 単位** | アカウント単位 | 加盟店単位 |
| 特殊ヘッダ | `Idempotent-Replayed` | `transient-error` (再試行可能な一時エラー明示) | - | - |
| 補償後の同一キー再利用 | 動作未定義(キャッシュ返却) | 動作未定義 | 動作未定義(最大45日保持) | 動作未定義 |

### Adyen 固有の特徴: Transient Error

Adyen は「再試行可能なエラー」を HTTP ヘッダで明示する独自機構を持つ:

```
HTTP/1.1 503 Service Unavailable
transient-error: true
```

→ `transient-error: true` なら**同じキー**で再試行可。それ以外は再試行せず、**新しいキーで別オペレーションとして**試行する。これが業界の標準パターンを具現化した良い例。

### PayPal の 45 日保持

PayPal は TTL が**最大 45 日**と異常に長い。これはリトライ用というより**監査ログ兼重複防止**としての性格が強い。この期間に同じキーで叩くと常に元のレスポンスが返る。

### GMO Payment Gateway (日本)

> 「同じ idempotency key を設定した決済リクエストは何度リトライしても決済処理は1回のみ実行され、同じレスポンスが返却される」

GMO も他社と同じ「タイムアウト時のリトライ」用途を明示。PGマルチペイメント OpenAPI タイプでのみサポート。

### 3 社の共通メッセージ

**全社が「キーは1論理オペレーションに1つ」と明示または示唆**しており、**補償後の再利用を想定していない**。本ライブラリは**新キー発行**方式で統一すれば全ゲートウェイ対応可能。

### 参考URL

- [Adyen - API idempotency](https://docs.adyen.com/development-resources/api-idempotency)
- [PayPal - Idempotency](https://developer.paypal.com/reference/guidelines/idempotency/)
- [GMO PG - PGマルチペイメントサービス OpenAPIタイプ APIリファレンス](https://static.mul-pay.jp/doc/openapi-type/)
- [Medium - Preventing Duplicate Payments (Stripe/PayPal/Adyen 比較)](https://medium.com/@sahintalha1/the-way-psps-such-as-paypal-stripe-and-adyen-prevent-duplicate-payment-idempotency-keys-615845c185bf)

---

## 5. ローカル DB save 失敗からの復旧パターン

業界では 3 つの主要アプローチが並行して使われている。

### パターン A: Webhook + Reconciliation (Stripe 推奨)

```
Client → Stripe.Charge (success)
Client → Local DB Save (FAIL)
              ↓
         何もしない (Stripe 側にのみ記録あり)
              ↓
Stripe → Webhook (charge.succeeded)
Client ← Webhook Handler → Local DB Save (retry)
```

- **pros**: サガ補償不要。シンプル
- **cons**: webhook delivery の遅延(数秒〜数分)あり、ユーザーに即座のフィードバックができない
- **Stripe 公式推奨**: webhook は最大 3 日間指数バックオフで retry。クライアント側はフロント画面で "処理中" 表示し、webhook で完了通知

### パターン B: Saga Compensation (本ライブラリの現行方式)

```
Client → Stripe.Charge (success)
Client → Local DB Save (FAIL)
Client → Stripe.Refund (compensation)
              ↓
ユーザーに "失敗" を即座に返す
ユーザーが同じ IdempotencyKey で retry → Issue #87 発生!
```

- **pros**: 即座のフィードバック可能。ローカル DB の一貫性を強く保証
- **cons**: Issue #87 の問題。**補償マーカーで新キー発行しないと機能しない**

### パターン C: Atomic Phases (Brandur/Rocket Rides)

```
atomic phase 1 (tx): idempotency_keys 挿入 (recovery_point = 'started')
atomic phase 2 (tx): ローカル DB 前処理 (recovery_point = 'prepared')
──foreign──────> Stripe.Charge
atomic phase 3 (tx): Charge ID 保存 (recovery_point = 'charge_created')
atomic phase 4 (tx): 後処理 (recovery_point = 'finished')
```

- **pros**: **補償不要**。リトライは常に recovery_point から再開
- **cons**: 実装複雑。全外部 API が冪等性対応必須
- **Brandur の意見**: "compensation は最後の手段。まず補償を必要としない設計を考えよ"

### 本ライブラリの選択肢

| 選択肢 | 難易度 | 効果 | 既存コードへの影響 |
|---|---|---|---|
| **A だけ** (補償廃止 + webhook 推奨) | 中 | ◎ | 大 (Saga 削除) |
| **B + 補償マーカー** (現行 + Issue #87 修正) | 低 | ○ | 小 |
| **C (atomic phases)** | 高 | ◎◎ | 極大 (全面書き直し) |

→ **現実解は B + 補償マーカー**。A への移行は将来の大改修で検討。

### 参考URL

- [Stripe - Process undelivered webhook events](https://docs.stripe.com/webhooks/process-undelivered-events)
- [Stripe - Handle payment events with webhooks](https://docs.stripe.com/webhooks/handling-payment-events)
- [brandur.org - Atomic phases](https://brandur.org/idempotency-keys)

---

## 6. 結論: Issue #87 の最終方針確認

### 業界プラクティスからの答え

業界のコンセンサス = **「補償が発火したキーはもう使わない。次回のオペレーションは新キーで実行する」**

- Stripe/PayPal/Adyen/GMO: 全社「キーは1論理オペレーションに1つ」
- Brandur (Stripe 元エンジニア): 「外部書き込み後の引き返しは例外。補償したらそのキーは完了扱い」
- Temporal コミュニティ: 「per-step idempotency key」「compensation is another money movement」
- 初期実装の GetTransaction 方式: **業界の誰もやっていない**

### 採用方針 (セルフレビュー時の方針と一致)

1. **補償マーカー (IdempotencyStore) を新設**: 補償 Refund 発火時に `key` を記録
2. **リトライ時**: `IsCompensated(key)` が true なら、`effectiveKey = key + "-" + ULID()` で**新キー発行**してゲートウェイに新規 Charge として送信
3. **既存の FindByIdempotencyKey 冪等性チェック**: 生 `input.IdempotencyKey` で継続(Save成功済みの同一キー retry は引き続き既存 Payment 返却)
4. **`GetTransaction` 方式は完全廃止**: 業界プラクティスに合わない・過剰・gateway 依存・レート制限リスク

### この方針の業界整合性

| チェック項目 | 整合性 |
|---|---|
| Stripe の設計思想 | ✅ (補償後は新キー) |
| Brandur の atomic phases の補完 | ✅ (補償マーカー = "this key is done" の永続化) |
| Temporal の per-step キー戦略 | ✅ (charge-retry-1, charge-retry-2... と実質同じ) |
| Adyen の transient-error との共存 | ✅ (transient-error は同一キー、それ以外は新キー) |
| 3DS (requires_action) との共存 | ✅ (3DS 経路は補償発火しないのでマーカー無影響) |
| gateway 非依存性 | ✅ (ローカル DB で判断、API 呼び出し不要) |

### 追加で取り入れるべきプラクティス (将来対応)

1. **Webhook 駆動 reconciliation (パターン A)**: 大規模運用時は補償廃止 + webhook を検討
2. **atomic phases (パターン C)**: 完全版は過剰だが、recovery_point の概念を Payment entity に部分導入するのはアリ
3. **Adyen 風 transient-error**: `port.PaymentGateway` の Response に `IsTransient bool` フィールドを足し、リトライ可否を gateway ごとに表現できるようにする

### 参考URL

- [Stripe - Designing robust APIs with idempotency](https://stripe.com/blog/idempotency)
- [brandur.org - Idempotency keys in Postgres](https://brandur.org/idempotency-keys)
- [Temporal - 10 idempotency slips that duplicate money](https://medium.com/@bhagyarana80/temporal-workflows-10-idempotency-slips-that-duplicate-money-30e2fb8d5d90)
- [Zuplo - Implementing Idempotency Keys in REST APIs](https://zuplo.com/learning-center/implementing-idempotency-keys-in-rest-apis-a-complete-guide)

---

## 付録: 業界プラクティスまとめ一覧表

| 課題 | 業界のアプローチ | 本ライブラリの適用 |
|---|---|---|
| ネットワークタイムアウトでの retry | 同じキーで再試行 | ✅ 既存実装のまま |
| 補償 Refund 後の retry | **新しいキー**で新規オペレーション | ⬅️ Issue #87 で修正 |
| キーの TTL 超過後 | 新キーで別オペレーション扱い | ✅ 問題なし |
| Charge 成功・Local Save 失敗 | (A) webhook reconciliation / (B) 補償 + 新キー / (C) atomic phases | ✅ (B) を採用中 |
| 補償自体の冪等性 | 補償にも独自 key (`comp-refund-{txnID}`) | ✅ 既に実装済み |
| ゲートウェイ状態を API で問い合わせ | confirm-before-repeat (タイムアウト時のみ) | ❌ 初期実装は全Chargeで呼んでいた(廃止) |
| 並行リトライ防止 | `locked_at` 列 or DB 行ロック | ⚠️ 本ライブラリ未実装 (future work) |
| 並行実行の transient-error | Adyen 風ヘッダ | ⚠️ 未対応 (future work) |
