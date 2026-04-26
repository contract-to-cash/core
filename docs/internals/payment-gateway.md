---
sidebar_label: Payment Gateway
---

# Payment Gateway Interface 設計

## 1. 概要

決済サービス（Stripe, PayPay, GMO, Square等）に依存しない抽象化レイヤーを提供する。

```mermaid
graph TB
    subgraph ServiceA[サービスA]
        subgraph DL[Domain Layer]
            PE[Payment Entity]
            PR[Payment Repository IF]
        end

        subgraph AL[Application Layer]
            PUC[PaymentUseCase]
            PS[PaymentService - OSS]
            PG[port.PaymentGateway - interface]
            PUC --> PS
            PS --> PG
            PS --> PR
        end

        subgraph IL[Infrastructure Layer]
            SG[Stripe Gateway]
            PPG[PayPay Gateway]
            GMOG[GMO Gateway]
            SQG[Square Gateway]
        end

        PG -.->|implements| SG
        PG -.->|implements| PPG
        PG -.->|implements| GMOG
        PG -.->|implements| SQG
    end
```

### パッケージ配置方針

| パッケージ | 配置するもの | 根拠 |
|-----------|-------------|------|
| `domain/payment/` | Payment エンティティ、ドメインイベント、Repository IF | 純粋なドメイン概念のみ |
| `application/port/` | PaymentGateway IF, CustomerGateway IF, WebhookHandler IF, GatewayRouter IF, リクエスト/レスポンス型 | 外部決済サービスとの統合境界（ポート） |
| `infrastructure/gateway/` | DefaultGatewayRouter, 各ゲートウェイ実装 | 具象実装（アダプタ） |

> `docs/architecture.md` の「Domain層は外部依存なし」原則に準拠するため、
> HTTP ヘッダー・リダイレクト URL・生レスポンスバイト列等のインフラ詳細を
> `domain/payment/` から `application/port/` に移動した。

---

## 2. 決済の種類と対応範囲

### 2.1 決済方法

| 決済方法 | 説明 | 対応 |
|----------|------|------|
| クレジットカード | Visa, Master, JCB等 | ✅ |
| デビットカード | 即時引落 | ✅ |
| 銀行振込 | 振込依頼→入金確認 | ✅ |
| コンビニ払い | 払込票/バーコード | ✅ |
| QRコード決済 | PayPay, LINE Pay等 | ✅ |
| キャリア決済 | docomo, au, SoftBank | ✅ |
| 後払い | Paidy, NP後払い等 | ✅ |
| 口座振替 | 定期引落 | ✅ |

### 2.2 決済フロー

```mermaid
stateDiagram-v2
    [*] --> Authorize: オーソリ
    
    Authorize --> Capture: 売上確定
    Authorize --> Void: オーソリ取消
    
    Capture --> Complete: 完了
    Capture --> Cancel: キャンセル
    
    Complete --> Refund: 返金
    Complete --> [*]
    
    Refund --> [*]
    Void --> [*]
    Cancel --> [*]
    
    note right of Authorize
        即時決済の場合:
        Authorize + Capture を同時に行う
    end note
```

---

## 3. コアインターフェース

### 3.1 PaymentGateway（メインインターフェース）

```go
// application/port/gateway.go
package port

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// PaymentGateway 決済ゲートウェイインターフェース
// 各決済サービス（Stripe, PayPay等）はこのインターフェースを実装する
// 旧 domain/payment/gateway.go から application/port/ に移動
type PaymentGateway interface {
    // ============================================================
    // 基本情報
    // ============================================================
    
    // ゲートウェイ識別子（例: "stripe", "paypay", "gmo"）
    ID() string
    
    // サポートする決済方法
    SupportedMethods() []PaymentMethodType

    // ============================================================
    // 決済処理
    // ============================================================

    // Charge 即時決済（オーソリ+キャプチャ同時）
    Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error)

    // Authorize オーソリのみ（与信枠確保）
    Authorize(ctx context.Context, req *AuthorizeRequest) (*AuthorizeResponse, error)

    // Capture 売上確定（オーソリ後）
    Capture(ctx context.Context, req *CaptureRequest) (*CaptureResponse, error)

    // Void オーソリ取消
    Void(ctx context.Context, req *VoidRequest) (*VoidResponse, error)

    // Refund 返金
    Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)

    // Cancel キャンセル（売上確定後）
    Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)

    // ============================================================
    // 決済状態
    // ============================================================

    // GetTransaction トランザクション詳細取得
    GetTransaction(ctx context.Context, transactionID string) (*Transaction, error)

    // ============================================================
    // 支払い方法管理
    // ============================================================

    // RegisterPaymentMethod 支払い方法登録（カード情報等）
    RegisterPaymentMethod(ctx context.Context, req *RegisterPaymentMethodRequest) (*PaymentMethodDetail, error)

    // DeletePaymentMethod 支払い方法削除
    DeletePaymentMethod(ctx context.Context, paymentMethodID string) error

    // GetPaymentMethod 支払い方法取得
    GetPaymentMethod(ctx context.Context, paymentMethodID string) (*PaymentMethodDetail, error)

    // ListPaymentMethods 支払い方法一覧
    ListPaymentMethods(ctx context.Context, customerID string) ([]*PaymentMethodDetail, error)
}

// ============================================================
// 決済方法タイプ
// ============================================================

type PaymentMethodType string

const (
    PaymentMethodTypeCreditCard       PaymentMethodType = "credit_card"
    PaymentMethodTypeDebitCard        PaymentMethodType = "debit_card"
    PaymentMethodTypeBankTransfer     PaymentMethodType = "bank_transfer"
    PaymentMethodTypeConvenienceStore PaymentMethodType = "convenience_store"
    PaymentMethodTypeQRCode           PaymentMethodType = "qr_code"
    PaymentMethodTypeCarrier          PaymentMethodType = "carrier"        // キャリア決済
    PaymentMethodTypePostpay          PaymentMethodType = "postpay"        // 後払い
    PaymentMethodTypeDirectDebit      PaymentMethodType = "direct_debit"   // 口座振替
)
```

### 3.2 リクエスト/レスポンス型

```go
// application/port/gateway_types.go
package port

import (
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// ============================================================
// Charge（即時決済）
// ============================================================

type ChargeRequest struct {
    // 必須
    Amount      shared.Money
    CustomerID  string           // ゲートウェイ側の顧客ID
    Description string

    // 支払い方法（いずれか必須）
    PaymentMethodID *string      // 登録済みの支払い方法ID
    Token           *string      // ワンタイムトークン（決済GWのJS SDKで取得）

    // オプション
    IdempotencyKey  string       // 冪等性キー
    Metadata        map[string]string
    StatementDescriptor string   // 明細表示名

    // 3Dセキュア
    ThreeDSecure    *ThreeDSecureRequest
}

type ChargeResponse struct {
    TransactionID   string
    Status          TransactionStatus
    Amount          shared.Money
    Fee             *shared.Money    // 決済手数料
    Net             *shared.Money    // 手数料差引後
    PaymentMethodID string
    CreatedAt       time.Time
    Metadata        map[string]string
    // デバッグ用の生レスポンスはインフラ層の実装側でログに記録する
}

// ============================================================
// Authorize（オーソリ）
// ============================================================

type AuthorizeRequest struct {
    Amount          shared.Money
    CustomerID      string
    PaymentMethodID *string
    Token           *string      // ワンタイムトークン
    IdempotencyKey  string
    Metadata        map[string]string

    // オーソリ有効期限（指定しない場合はゲートウェイのデフォルト）
    ExpiresIn       *time.Duration
}

type AuthorizeResponse struct {
    AuthorizationID string
    TransactionID   string
    Status          TransactionStatus
    Amount          shared.Money
    ExpiresAt       *time.Time
    CreatedAt       time.Time
    Metadata        map[string]string
}

// ============================================================
// Capture（売上確定）
// ============================================================

type CaptureRequest struct {
    AuthorizationID string
    Amount          *shared.Money   // nilの場合はオーソリ全額
    IdempotencyKey  string
    Metadata        map[string]string
}

type CaptureResponse struct {
    TransactionID   string
    AuthorizationID string
    Status          TransactionStatus
    Amount          shared.Money
    Fee             *shared.Money
    Net             *shared.Money
    CapturedAt      time.Time
}

// ============================================================
// Void（オーソリ取消）
// ============================================================

type VoidRequest struct {
    AuthorizationID string
    IdempotencyKey  string
}

type VoidResponse struct {
    AuthorizationID string
    Status          TransactionStatus
    VoidedAt        time.Time
}

// ============================================================
// Refund（返金）
// ============================================================

type RefundRequest struct {
    TransactionID   string
    Amount          *shared.Money   // nilの場合は全額返金
    Reason          RefundReason
    IdempotencyKey  string
    Metadata        map[string]string
}

type RefundReason string

const (
    RefundReasonDuplicate           RefundReason = "duplicate"
    RefundReasonFraudulent          RefundReason = "fraudulent"
    RefundReasonRequestedByCustomer RefundReason = "requested_by_customer"
    RefundReasonOther               RefundReason = "other"
)

type RefundResponse struct {
    RefundID        string
    TransactionID   string
    Status          RefundStatus
    Amount          shared.Money
    Reason          RefundReason
    RefundedAt      time.Time
}

type RefundStatus string

const (
    RefundStatusPending   RefundStatus = "pending"
    RefundStatusSucceeded RefundStatus = "succeeded"
    RefundStatusFailed    RefundStatus = "failed"
    RefundStatusCanceled  RefundStatus = "canceled"
)

// ============================================================
// Cancel（キャンセル）
// ============================================================

type CancelRequest struct {
    TransactionID  string
    IdempotencyKey string
}

type CancelResponse struct {
    TransactionID string
    Status        TransactionStatus
    CanceledAt    time.Time
}

// ============================================================
// Transaction（トランザクション）
// ============================================================

type Transaction struct {
    ID              string
    GatewayID       string            // どのゲートウェイか
    Type            TransactionType
    Status          TransactionStatus
    Amount          shared.Money
    Fee             *shared.Money
    Net             *shared.Money
    CustomerID      string
    PaymentMethodID string
    Description     string
    Metadata        map[string]string
    
    // 関連ID
    AuthorizationID *string
    RefundIDs       []string
    
    // タイムスタンプ
    CreatedAt       time.Time
    UpdatedAt       time.Time
    CapturedAt      *time.Time
    RefundedAt      *time.Time
    
    // エラー情報
    FailureCode     *string
    FailureMessage  *string
}

type TransactionType string

const (
    TransactionTypeCharge       TransactionType = "charge"
    TransactionTypeAuthorize    TransactionType = "authorize"
    TransactionTypeCapture      TransactionType = "capture"
    TransactionTypeRefund       TransactionType = "refund"
    TransactionTypeVoid         TransactionType = "void"
)

type TransactionStatus string

const (
    TransactionStatusPending    TransactionStatus = "pending"
    TransactionStatusAuthorized TransactionStatus = "authorized"
    TransactionStatusCaptured   TransactionStatus = "captured"
    TransactionStatusSucceeded  TransactionStatus = "succeeded"
    TransactionStatusFailed     TransactionStatus = "failed"
    TransactionStatusCanceled   TransactionStatus = "canceled"
    TransactionStatusRefunded   TransactionStatus = "refunded"
    TransactionStatusPartiallyRefunded TransactionStatus = "partially_refunded"
)

// ============================================================
// PaymentMethodDetail（支払い方法詳細 — ポート層）
// ============================================================

type PaymentMethodDetail struct {
    ID          string
    CustomerID  string
    Type        PaymentMethodType
    IsDefault   bool
    CreatedAt   time.Time
    
    // タイプ別詳細（どれか1つが設定される）
    Card        *CardDetails
    BankAccount *BankAccountDetails
    QRCode      *QRCodeDetails
}

type CardDetails struct {
    Brand       CardBrand
    Last4       string
    ExpMonth    int
    ExpYear     int
    Fingerprint string   // カード識別子（重複検知用）
    Country     string
    Funding     string   // credit, debit, prepaid
}

type CardBrand string

const (
    CardBrandVisa       CardBrand = "visa"
    CardBrandMastercard CardBrand = "mastercard"
    CardBrandJCB        CardBrand = "jcb"
    CardBrandAmex       CardBrand = "amex"
    CardBrandDiners     CardBrand = "diners"
    CardBrandDiscover   CardBrand = "discover"
    CardBrandUnknown    CardBrand = "unknown"
)

type BankAccountDetails struct {
    BankName      string
    BankCode      string
    BranchName    string
    BranchCode    string
    AccountType   string  // 普通, 当座
    AccountNumber string  // マスク済み（下4桁等）
    AccountHolder string
}

type QRCodeDetails struct {
    Provider string  // paypay, linepay, etc.
    UserID   string
}

// ============================================================
// セキュリティ方針: カード情報の非通過型設計
// ============================================================
//
// 本ライブラリはPCI DSS SAQ A相当の「非通過型」設計を採用する。
// カード番号・CVC等のセンシティブ情報はサーバーサイドのコードに
// 一切登場しない。
//
// 決済フロー:
//   1. クライアント側で決済GWのJS SDK（Stripe Elements, payjp.js v2,
//      GMO MpToken.js, Square Web Payments SDK等）を使用
//   2. カード情報は決済GWサーバーに直接送信されトークン化
//   3. サーバーにはトークン（またはPaymentMethodID）のみが送信される
//
// CardSource型（カード番号・CVCの直接受信）は意図的に提供しない。
// これにより:
//   - PCI DSSスコープを最小化（SAQ A: 約30要件）
//   - 改正割賦販売法（2018年施行）の非保持化要件に準拠
//   - OSSライブラリ利用者のセキュリティリスクを排除
//
// 参考:
//   - PCI DSS 4.0 要件3.2.2: 認証後のCVC保持は禁止
//   - 改正割賦販売法: PCI DSS非準拠の加盟店は非通過型が義務
//   - Square: カード番号直接送信APIを提供しない設計を採用

// ============================================================
// 3Dセキュア
// ============================================================

// ThreeDSecureRequest は application/port/ に配置
// ReturnURL（リダイレクトURL）はインフラ寄りの概念だが、
// ゲートウェイIF のリクエストパラメータとしてポート層に含める
type ThreeDSecureRequest struct {
    Required    bool
    ReturnURL   string  // 認証後のリダイレクトURL
}

type ThreeDSecureResult struct {
    Status      ThreeDSecureStatus
    RedirectURL *string  // 3DS認証ページURL
}

type ThreeDSecureStatus string

const (
    ThreeDSecureStatusSucceeded       ThreeDSecureStatus = "succeeded"
    ThreeDSecureStatusAttempted       ThreeDSecureStatus = "attempted"
    ThreeDSecureStatusFailed          ThreeDSecureStatus = "failed"
    ThreeDSecureStatusNotSupported    ThreeDSecureStatus = "not_supported"
    ThreeDSecureStatusRequired        ThreeDSecureStatus = "required"
)

// ============================================================
// 支払い方法登録
// ============================================================

type RegisterPaymentMethodRequest struct {
    CustomerID    string
    Type          PaymentMethodType
    SetAsDefault  bool

    // トークン（必須）
    // 決済GWのJS SDKでカード情報/銀行口座情報をトークン化したもの
    Token         string

    // 請求先住所（3Dセキュア等で使用）
    BillingAddress *Address
}

// BankAccountSource は削除。
// 銀行口座情報もトークン化して RegisterPaymentMethodRequest.Token で渡す。
// ゲートウェイ実装が口座振替対応の場合、各GWのJS SDKまたは
// ホスト型フォームで口座情報をトークン化する。

type Address struct {
    PostalCode string
    Country    string
    State      string
    City       string
    Line1      string
    Line2      string
}
```

### 3.3 顧客管理インターフェース

```go
// application/port/customer.go
package port

import (
    "context"
    "time"
)

// CustomerGateway 顧客管理インターフェース
// 決済ゲートウェイ側の顧客情報を管理
// 旧 domain/payment/customer.go から application/port/ に移動
type CustomerGateway interface {
    // 顧客作成
    CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*Customer, error)
    
    // 顧客更新
    UpdateCustomer(ctx context.Context, req *UpdateCustomerRequest) (*Customer, error)
    
    // 顧客取得
    GetCustomer(ctx context.Context, customerID string) (*Customer, error)
    
    // 顧客削除
    DeleteCustomer(ctx context.Context, customerID string) error
}

type CreateCustomerRequest struct {
    Email       string
    Name        string
    Description string
    Phone       string
    Address     *Address
    Metadata    map[string]string
    
    // 内部ID（自サービスのAccountIDなど）
    InternalID  string
}

type UpdateCustomerRequest struct {
    CustomerID  string
    Email       *string
    Name        *string
    Description *string
    Phone       *string
    Address     *Address
    Metadata    map[string]string
}

type Customer struct {
    ID          string
    Email       string
    Name        string
    Description string
    Phone       string
    Address     *Address
    Metadata    map[string]string
    CreatedAt   time.Time
    UpdatedAt   time.Time
    
    // 関連する支払い方法
    DefaultPaymentMethodID *string
}
```

### 3.4 Webhookインターフェース

```go
// application/port/webhook.go
package port

import (
    "context"
    "encoding/json"
    "time"

    "github.com/contract-to-cash/core/domain/shared"
)

// WebhookHandler Webhookハンドラインターフェース
// ゲートウェイごとに実装する。署名検証・タイムスタンプ検証・パースを担当。
// 旧 domain/payment/webhook.go から application/port/ に移動
type WebhookHandler interface {
    // ParseAndVerify Webhookリクエストの検証とパースを一括で行う
    // 以下を順に実行する:
    //   1. 署名検証（HMAC-SHA256等、ゲートウェイ固有）
    //   2. タイムスタンプ検証（許容範囲外のイベントを拒否）
    //   3. ペイロードのパース
    ParseAndVerify(ctx context.Context, req *WebhookRequest) (*WebhookEvent, error)
}

// WebhookRequest HTTP Webhookリクエスト
type WebhookRequest struct {
    Headers map[string]string
    Body    []byte
}

// WebhookEvent パース済みWebhookイベント
type WebhookEvent struct {
    ID        string           // イベント一意ID（重複検出に使用）
    Type      WebhookEventType
    CreatedAt time.Time        // イベント発生時刻（UTC必須、タイムスタンプ検証対象）
    Data      json.RawMessage
    RawData   []byte
}

// ============================================================
// Webhook処理サービス（アプリケーション層）
// ============================================================
//
// WebhookHandlerはゲートウェイ固有のパース・検証を担当する。
// 以下の横断的関心事はアプリケーション層の WebhookProcessor が担当する:
//   - タイムスタンプ双方向検証（リプレイ攻撃防止）
//   - イベント重複検出（冪等性保証）
//   - リトライ可能/不可能エラーの分類
//   - Dead Letter Queue（リトライ超過時の追跡）
//
// 設計根拠:
//   - Standard Webhooks仕様に準拠した双方向タイムスタンプ検証
//   - Stripeは最長3日間リトライするため、重複検出TTLは72時間が必要
//   - 決済GWは「2xxか否か」でリトライ判定するため、HTTPステータスの使い分けが重要

// デフォルト値
const (
    DefaultTimestampTolerance = 5 * time.Minute   // Standard Webhooks仕様準拠
    DefaultDeduplicationTTL   = 72 * time.Hour     // Stripeの最大リトライ期間(3日)に合わせる
    DefaultMaxRetries         = 3
    DefaultRetryBackoff       = 1 * time.Second
)

// WebhookProcessor Webhook処理サービス
type WebhookProcessor struct {
    handler      WebhookHandler
    deduplicator WebhookDeduplicator
    dlq          WebhookDeadLetterQueue // nil許容（DLQなしでも動作）
    clock        shared.Clock           // テスト時にタイムスタンプ検証の「現在時刻」を制御
    config       WebhookProcessorConfig
}

// WebhookProcessorConfig Webhook処理設定
type WebhookProcessorConfig struct {
    // TimestampTolerance タイムスタンプの許容範囲（双方向）
    // 過去・未来ともにこの範囲外のイベントを拒否する
    // デフォルト: 5分（Standard Webhooks仕様準拠）
    TimestampTolerance time.Duration

    // DeduplicationTTL 重複検出レコードの保持期間
    // Stripeは最長3日間リトライするため、72時間以上を推奨
    // デフォルト: 72時間
    DeduplicationTTL time.Duration

    // MaxRetries リトライ可能エラー発生時の最大リトライ回数
    // デフォルト: 3
    MaxRetries int

    // RetryBackoff リトライ間隔の基準値（指数バックオフ + ジッター）
    // 実際の間隔: backoff * 2^(attempt-1) + random(0, backoff/10)
    // デフォルト: 1秒
    RetryBackoff time.Duration
}

// ============================================================
// 重複検出インターフェース
// ============================================================

// WebhookDeduplicator イベント重複検出インターフェース
// 利用者がストレージ実装を提供する（Redis SETNX + TTL、RDB UPSERT等）
type WebhookDeduplicator interface {
    // IsDuplicate イベントIDが処理済みかチェックし、未処理なら記録する
    // ttl: レコード保持期間。期限切れ後は自動削除される
    // 原子的に「チェック＆マーク」を行うこと（Redis: SETNX+EXPIRE、RDB: INSERT ON CONFLICT）
    IsDuplicate(ctx context.Context, eventID string, ttl time.Duration) (bool, error)
}

// 推奨ストレージ実装:
//
// Redis（高速・推奨）:
//   SET webhook:{eventID} 1 NX EX {ttl_seconds}
//   → OK: 初回（処理実行）、nil: 重複（スキップ）
//
// RDB（永続・監査対応）:
//   INSERT INTO processed_webhooks (event_id, processed_at, expires_at)
//   VALUES ($1, NOW(), NOW() + $2)
//   ON CONFLICT (event_id) DO NOTHING
//   → affected=1: 初回、affected=0: 重複
//
// ハイブリッド（推奨）:
//   Redis で高速フィルタ（第一防衛線）
//   + RDB で永続記録（監査ログ兼第二防衛線）

// ============================================================
// Dead Letter Queue インターフェース
// ============================================================

// WebhookDeadLetterQueue リトライ超過イベントの記録先
// リトライ上限を超えたイベントを保存し、手動/バッチでの再処理を可能にする
type WebhookDeadLetterQueue interface {
    // Send 処理失敗したイベントをDLQに記録する
    Send(ctx context.Context, entry *WebhookDLQEntry) error
}

// WebhookDLQEntry DLQエントリ
type WebhookDLQEntry struct {
    EventID    string
    EventType  WebhookEventType
    Payload    []byte           // 元のWebhookペイロード
    LastError  string           // 最後のエラーメッセージ
    RetryCount int              // 実行したリトライ回数
    CreatedAt  time.Time        // DLQ投入時刻
}

// ============================================================
// エラー分類
// ============================================================

// WebhookRetryableError リトライ可能なエラーをラップする
// ネットワークエラー、DB一時障害等に使用する
type WebhookRetryableError struct {
    Err error
}

func (e *WebhookRetryableError) Error() string { return e.Err.Error() }
func (e *WebhookRetryableError) Unwrap() error { return e.Err }

// isRetryable エラーがリトライ可能か判定する
// WebhookRetryableError でラップされている場合のみリトライする
// それ以外（ビジネスロジックエラー等）は即座にDLQへ送る
func isRetryable(err error) bool {
    var retryable *WebhookRetryableError
    return errors.As(err, &retryable)
}

// エラー分類ガイド:
//
// リトライ可能（WebhookRetryableErrorでラップ）:
//   - DB接続エラー、タイムアウト
//   - 外部API一時障害（5xx）
//   - Rate Limit（429）
//
// リトライ不可（素のerrorで返す）:
//   - 存在しない顧客ID参照
//   - 不整合なステート遷移
//   - バリデーションエラー

// ============================================================
// Webhook処理メインロジック
// ============================================================

// ProcessWebhook Webhookイベントを安全に処理する
//
// 処理フロー:
//   1. ParseAndVerify（署名検証 + パース）
//   2. タイムスタンプ双方向検証（過去・未来の両方をチェック）
//   3. 重複検出（冪等性保証、TTL付き）
//   4. イベントハンドラ呼び出し（リトライ可能エラーのみリトライ）
//   5. リトライ超過時はDLQに送信
func (p *WebhookProcessor) ProcessWebhook(
    ctx context.Context,
    req *WebhookRequest,
    handler func(ctx context.Context, event *WebhookEvent) error,
) error {
    // 1. パースと署名検証（ゲートウェイ固有）
    event, err := p.handler.ParseAndVerify(ctx, req)
    if err != nil {
        return &WebhookError{Code: WebhookErrorCodeInvalidSignature, Cause: err}
    }

    // 2. タイムスタンプ双方向検証（Standard Webhooks仕様準拠）
    //    過去方向: リプレイ攻撃防止
    //    未来方向: クロックスキュー攻撃防止
    tolerance := p.config.TimestampTolerance
    if tolerance == 0 {
        tolerance = DefaultTimestampTolerance
    }
    now := p.clock.Now()
    diff := now.Sub(event.CreatedAt)
    if diff > tolerance {
        return &WebhookError{
            Code: WebhookErrorCodeInvalidPayload,
            Cause:  fmt.Errorf("event %s is %v old (tolerance: %v)", event.ID, diff, tolerance),
        }
    }
    if diff < -tolerance {
        return &WebhookError{
            Code: WebhookErrorCodeInvalidPayload,
            Cause:  fmt.Errorf("event %s is %v in the future (tolerance: %v)", event.ID, -diff, tolerance),
        }
    }

    // 3. 重複検出（冪等性保証）
    ttl := p.config.DeduplicationTTL
    if ttl == 0 {
        ttl = DefaultDeduplicationTTL
    }
    isDup, err := p.deduplicator.IsDuplicate(ctx, event.ID, ttl)
    if err != nil {
        return &WebhookError{Code: WebhookErrorCodeDuplicate, Cause: err}
    }
    if isDup {
        return nil // 重複イベントは正常応答（HTTP 200）で無視
    }

    // 4. イベント処理（リトライ付き）
    maxRetries := p.config.MaxRetries
    if maxRetries == 0 {
        maxRetries = DefaultMaxRetries
    }
    backoff := p.config.RetryBackoff
    if backoff == 0 {
        backoff = DefaultRetryBackoff
    }

    var lastErr error
    for attempt := 0; attempt <= maxRetries; attempt++ {
        // コンテキストキャンセルチェック
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        if attempt > 0 {
            // 指数バックオフ + ジッター（同時大量失敗時のスパイク防止）
            delay := backoff * time.Duration(1<<uint(attempt-1))
            jitter := time.Duration(rand.Intn(int(delay / 10))) // 10%ジッター
            time.Sleep(delay + jitter)
        }

        if err := handler(ctx, event); err != nil {
            lastErr = err
            // リトライ不可能なエラーは即座に中断
            if !isRetryable(err) {
                break
            }
            continue
        }
        return nil // 処理成功
    }

    // 5. リトライ超過 or 非リトライエラー → DLQに送信
    //    DLQ送信失敗はログ出力するが、呼び出し元にはProcessingFailedを返す
    if p.dlq != nil {
        if dlqErr := p.dlq.Send(ctx, &WebhookDLQEntry{
            EventID:    event.ID,
            EventType:  event.Type,
            Payload:    event.RawData,
            LastError:  lastErr.Error(),
            RetryCount: maxRetries,
            CreatedAt:  p.clock.Now(),
        }); dlqErr != nil {
            // DLQ送信失敗は致命的ではないが、必ずログに記録する
            // 実装時: log.Error("failed to send to DLQ", "event_id", event.ID, "error", dlqErr)
        }
    }
    return &WebhookError{
        Code: WebhookErrorCodeProcessingFailed,
        Cause:  fmt.Errorf("after %d attempts: %w", maxRetries+1, lastErr),
    }
}

// ============================================================
// Webhookエラー型とHTTPステータスマッピング
// ============================================================

// WebhookErrorCode Webhookエラーコード
type WebhookErrorCode string

const (
    WebhookErrorCodeInvalidSignature WebhookErrorCode = "invalid_signature"
    WebhookErrorCodeInvalidPayload   WebhookErrorCode = "invalid_payload"
    WebhookErrorCodeUnsupportedEvent WebhookErrorCode = "unsupported_event"
    WebhookErrorCodeDuplicate        WebhookErrorCode = "duplicate_event"
    WebhookErrorCodeProcessingFailed WebhookErrorCode = "processing_failed"
)

// WebhookError Webhook処理エラー
type WebhookError struct {
    Code    WebhookErrorCode
    Message string
    Cause   error
}

func (e *WebhookError) Error() string {
    if e.Cause != nil {
        return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
    }
    return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}
func (e *WebhookError) Unwrap() error { return e.Cause }

// MapWebhookErrorToHTTP WebhookエラーをHTTPステータスコードに変換する
//
// 決済GWは「2xxか否か」でリトライ判定するため注意:
//   - リトライさせたい障害 → 503を返す（GW側がリトライする）
//   - リトライさせたくないエラー → 200を返す（内部でDLQ/アラート対応）
//
// | 状況                        | HTTPステータス | GW側の挙動    | 備考                    |
// |----------------------------|-------------|------------|------------------------|
// | 処理成功                     | 200         | リトライしない | —                      |
// | 重複イベント（正常系）          | 200         | リトライしない | ProcessWebhookがnil返却  |
// | 非リトライエラー（DLQ行き）     | 200         | リトライしない | DLQで追跡               |
// | 署名検証失敗                  | 401         | リトライする  | 不正リクエスト             |
// | タイムスタンプ範囲外            | 400         | リトライする  | リプレイ攻撃 or クロックスキュー |
// | 重複検出ストレージ障害          | 503         | リトライする  | Redis/DB一時障害         |
// | リトライ可能な内部エラー         | 503         | リトライする  | ※ProcessWebhook内で処理済 |
//
// ※タイムスタンプ範囲外はリトライしても同じ結果になるが、
//   400を返すことでGW側のダッシュボードに明確なエラーを表示させる
func MapWebhookErrorToHTTP(err error) int {
    var webhookErr *WebhookError
    if !errors.As(err, &webhookErr) {
        return 500 // 想定外エラー
    }
    switch webhookErr.Code {
    case WebhookErrorCodeInvalidSignature:
        return 401
    case WebhookErrorCodeInvalidPayload:
        return 400
    case WebhookErrorCodeDuplicate:
        return 200 // 重複イベント → GW側のリトライは不要
    case WebhookErrorCodeProcessingFailed:
        return 200 // DLQに送信済み → GW側のリトライは不要
    default:
        return 500
    }
}

type WebhookEventType string

const (
    // 決済イベント
    WebhookEventPaymentSucceeded      WebhookEventType = "payment.succeeded"
    WebhookEventPaymentFailed         WebhookEventType = "payment.failed"
    WebhookEventPaymentPending        WebhookEventType = "payment.pending"
    
    // 返金イベント
    WebhookEventRefundSucceeded       WebhookEventType = "refund.succeeded"
    WebhookEventRefundFailed          WebhookEventType = "refund.failed"
    
    // チャージバック
    WebhookEventChargebackCreated     WebhookEventType = "chargeback.created"
    WebhookEventChargebackUpdated     WebhookEventType = "chargeback.updated"
    WebhookEventChargebackClosed      WebhookEventType = "chargeback.closed"
    
    // 支払い方法
    WebhookEventPaymentMethodAttached WebhookEventType = "payment_method.attached"
    WebhookEventPaymentMethodDetached WebhookEventType = "payment_method.detached"
    WebhookEventPaymentMethodExpiring WebhookEventType = "payment_method.expiring"
    
    // サブスクリプション（ゲートウェイ側で管理する場合）
    WebhookEventSubscriptionCreated   WebhookEventType = "subscription.created"
    WebhookEventSubscriptionUpdated   WebhookEventType = "subscription.updated"
    WebhookEventSubscriptionCanceled  WebhookEventType = "subscription.canceled"
    
    // 銀行振込・コンビニ払い
    WebhookEventPaymentInstructionCreated WebhookEventType = "payment_instruction.created"
    WebhookEventPaymentReceived       WebhookEventType = "payment.received"
)

```

### 3.5 定期課金インターフェース（オプション）

```go
// domain/payment/subscription_gateway.go
package payment

import (
    "context"
    "time"
)

// SubscriptionGateway 定期課金管理インターフェース
// ゲートウェイ側でサブスクリプションを管理する場合に使用
// （自前で管理する場合は不要）
type SubscriptionGateway interface {
    // サブスクリプション作成
    CreateSubscription(ctx context.Context, req *CreateSubscriptionRequest) (*Subscription, error)
    
    // サブスクリプション更新
    UpdateSubscription(ctx context.Context, req *UpdateSubscriptionRequest) (*Subscription, error)
    
    // サブスクリプションキャンセル
    CancelSubscription(ctx context.Context, subscriptionID string, cancelAtPeriodEnd bool) (*Subscription, error)
    
    // サブスクリプション取得
    GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error)
    
    // サブスクリプション一覧
    ListSubscriptions(ctx context.Context, customerID string) ([]*Subscription, error)
}

type CreateSubscriptionRequest struct {
    CustomerID        string
    PriceID           string           // ゲートウェイ側の価格ID
    PaymentMethodID   *string
    BillingCycleAnchor *time.Time
    TrialEnd          *time.Time
    Metadata          map[string]string
}

type UpdateSubscriptionRequest struct {
    SubscriptionID    string
    PriceID           *string
    PaymentMethodID   *string
    CancelAtPeriodEnd *bool
    Metadata          map[string]string
}

type Subscription struct {
    ID                string
    CustomerID        string
    Status            SubscriptionStatus
    PriceID           string
    Amount            shared.Money
    Interval          string
    IntervalCount     int
    CurrentPeriodStart time.Time
    CurrentPeriodEnd  time.Time
    TrialStart        *time.Time
    TrialEnd          *time.Time
    CancelAt          *time.Time
    CanceledAt        *time.Time
    EndedAt           *time.Time
    CreatedAt         time.Time
}

type SubscriptionStatus string

const (
    SubscriptionStatusTrialing       SubscriptionStatus = "trialing"
    SubscriptionStatusActive         SubscriptionStatus = "active"
    SubscriptionStatusPastDue        SubscriptionStatus = "past_due"
    SubscriptionStatusCanceled       SubscriptionStatus = "canceled"
    SubscriptionStatusUnpaid         SubscriptionStatus = "unpaid"
    SubscriptionStatusIncomplete     SubscriptionStatus = "incomplete"
    SubscriptionStatusIncompleteExpired SubscriptionStatus = "incomplete_expired"
)
```

---

## 4. エラー定義

```go
// application/port/errors.go
package port

import "fmt"

// 決済エラーコード
type ErrorCode string

const (
    // カード関連
    ErrorCodeCardDeclined           ErrorCode = "card_declined"
    ErrorCodeCardExpired            ErrorCode = "card_expired"
    ErrorCodeInsufficientFunds      ErrorCode = "insufficient_funds"
    ErrorCodeInvalidCard            ErrorCode = "invalid_card"
    ErrorCodeInvalidCVC             ErrorCode = "invalid_cvc"
    ErrorCodeInvalidExpiryMonth     ErrorCode = "invalid_expiry_month"
    ErrorCodeInvalidExpiryYear      ErrorCode = "invalid_expiry_year"

    // 処理関連
    ErrorCodeProcessingError        ErrorCode = "processing_error"
    ErrorCodeRateLimitExceeded      ErrorCode = "rate_limit_exceeded"
    ErrorCodeAuthenticationRequired ErrorCode = "authentication_required"
    ErrorCodeDuplicateTransaction   ErrorCode = "duplicate_transaction"
    ErrorCodeAmountTooSmall         ErrorCode = "amount_too_small"
    ErrorCodeAmountTooLarge         ErrorCode = "amount_too_large"
    ErrorCodeCurrencyNotSupported   ErrorCode = "currency_not_supported"
    ErrorCodeMethodNotSupported     ErrorCode = "method_not_supported"
    ErrorCodeCustomerNotFound       ErrorCode = "customer_not_found"

    // ゲートウェイ関連
    ErrorCodeGatewayUnavailable     ErrorCode = "gateway_unavailable"
    ErrorCodeGatewayTimeout         ErrorCode = "gateway_timeout"
    ErrorCodeFraudSuspected         ErrorCode = "fraud_suspected"
    ErrorCodeTestModeTransaction    ErrorCode = "test_mode_transaction"

    // その他
    ErrorCodeUnknown                ErrorCode = "unknown"
)

// GatewayError 決済ゲートウェイエラー
type GatewayError struct {
    Code        ErrorCode
    Message     string
    DeclineCode string            // ゲートウェイ固有の拒否コード
    Param       string            // エラーの原因となったパラメータ
    Retryable   bool              // リトライ可能か
    RawError    error             // 元のエラー
}

func (e *GatewayError) Error() string {
    if e.RawError != nil {
        return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.RawError)
    }
    return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *GatewayError) Unwrap() error {
    return e.RawError
}
```

---

## 5. 複合ゲートウェイ（ルーティング）

複数の決済ゲートウェイを使い分けるためのルーター。

### 5.1 GatewayRouter インターフェース（ポート層）

```go
// application/port/router.go
package port

import (
    "context"

    "github.com/contract-to-cash/core/domain/shared"
)

// GatewayRouter 決済ゲートウェイルーター
// 条件に応じて適切なゲートウェイを選択
// 旧 domain/payment/router.go から application/port/ に移動
type GatewayRouter interface {
    // 条件に基づいてゲートウェイを選択
    Route(ctx context.Context, criteria RoutingCriteria) (PaymentGateway, error)
}

type RoutingCriteria struct {
    Amount        shared.Money
    Currency      shared.Currency
    PaymentMethod PaymentMethodType
    CustomerID    string
    Country       string
    Metadata      map[string]string
}

type RoutingRule struct {
    GatewayID      string
    Priority       int
    PaymentMethods []PaymentMethodType
    Currencies     []shared.Currency
    Countries      []string
    MinAmount      *shared.Money
    MaxAmount      *shared.Money
}
```

### 5.2 DefaultGatewayRouter 具象実装（インフラ層）

```go
// infrastructure/gateway/router.go
package gateway

import (
    "context"
    "errors"

    "github.com/contract-to-cash/core/application/port"
)

// DefaultGatewayRouter デフォルト実装
// 旧 domain/payment/router.go の実装部分を infrastructure/ に移動
type DefaultGatewayRouter struct {
    gateways map[string]gatewayWithRules
    fallback port.PaymentGateway
}

type gatewayWithRules struct {
    gateway port.PaymentGateway
    rules   []port.RoutingRule
}

func NewGatewayRouter() *DefaultGatewayRouter {
    return &DefaultGatewayRouter{
        gateways: make(map[string]gatewayWithRules),
    }
}

func (r *DefaultGatewayRouter) Register(gw port.PaymentGateway, rules []port.RoutingRule) {
    r.gateways[gw.ID()] = gatewayWithRules{
        gateway: gw,
        rules:   rules,
    }
}

func (r *DefaultGatewayRouter) SetFallback(gw port.PaymentGateway) {
    r.fallback = gw
}

func (r *DefaultGatewayRouter) Route(ctx context.Context, criteria port.RoutingCriteria) (port.PaymentGateway, error) {
    var bestMatch port.PaymentGateway
    var bestPriority int = -1

    for _, gw := range r.gateways {
        for _, rule := range gw.rules {
            if r.matches(rule, criteria) && rule.Priority > bestPriority {
                bestMatch = gw.gateway
                bestPriority = rule.Priority
            }
        }
    }

    if bestMatch != nil {
        return bestMatch, nil
    }

    if r.fallback != nil {
        return r.fallback, nil
    }

    return nil, errors.New("no gateway available for criteria")
}

func (r *DefaultGatewayRouter) matches(rule port.RoutingRule, criteria port.RoutingCriteria) bool {
    // PaymentMethod チェック
    if len(rule.PaymentMethods) > 0 {
        found := false
        for _, t := range rule.PaymentMethods {
            if t == criteria.PaymentMethod {
                found = true
                break
            }
        }
        if !found {
            return false
        }
    }

    // 金額チェック（big.Rat の比較は Cmp を使用）
    if rule.MinAmount != nil && criteria.Amount.Amount().Cmp(rule.MinAmount.Amount()) < 0 {
        return false
    }
    if rule.MaxAmount != nil && criteria.Amount.Amount().Cmp(rule.MaxAmount.Amount()) > 0 {
        return false
    }

    // 国チェック
    if len(rule.Countries) > 0 {
        found := false
        for _, c := range rule.Countries {
            if c == criteria.Country {
                found = true
                break
            }
        }
        if !found {
            return false
        }
    }

    return true
}
```

---

## 6. 決済サービス（アプリケーション層）

```go
// application/service/payment_service.go
package service

import (
    "context"
    "time"

    "github.com/contract-to-cash/core/application/port"
    "github.com/contract-to-cash/core/domain/invoice"
    "github.com/contract-to-cash/core/domain/payment"
    "github.com/contract-to-cash/core/domain/shared"
    "github.com/contract-to-cash/core/eventstore"
    "github.com/contract-to-cash/core/plugin"
)

// PaymentService 決済サービス
type PaymentService struct {
    gateway         port.PaymentGateway
    paymentRepo     payment.Repository
    invoiceRepo     invoice.Repository
    contractRepo    contract.Repository    // 支払い方法フォールバック解決用
    customerGateway port.CustomerGateway   // オプション: 顧客デフォルト支払い方法の参照用
    eventStore      eventstore.Store
    pluginRegistry  *plugin.Registry
    clock           shared.Clock
}

// PaymentServiceOption NewPaymentServiceのオプション引数
type PaymentServiceOption func(*PaymentService)

// WithCustomerGateway 顧客ゲートウェイを設定するオプション
func WithCustomerGateway(gw port.CustomerGateway) PaymentServiceOption {
    return func(s *PaymentService) { s.customerGateway = gw }
}

func NewPaymentService(
    gateway port.PaymentGateway,
    paymentRepo payment.Repository,
    invoiceRepo invoice.Repository,
    contractRepo contract.Repository,
    eventStore eventstore.Store,
    pluginRegistry *plugin.Registry,
    clock shared.Clock,
    opts ...PaymentServiceOption,
) *PaymentService {
    s := &PaymentService{
        gateway:        gateway,
        paymentRepo:    paymentRepo,
        invoiceRepo:    invoiceRepo,
        contractRepo:   contractRepo,
        eventStore:     eventStore,
        pluginRegistry: pluginRegistry,
        clock:          clock,
    }
    for _, opt := range opts {
        opt(s)
    }
    return s
}

// ProcessPayment 請求書の支払いを処理
func (s *PaymentService) ProcessPayment(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    input ProcessPaymentInput,
) (*payment.Payment, error) {
    // 1. 請求書取得
    inv, err := s.invoiceRepo.FindByID(ctx, invoiceID)
    if err != nil {
        return nil, err
    }

    // 2. 支払い金額決定
    amount := inv.Total()
    if input.Amount != nil {
        amount = *input.Amount  // 一部支払い
    }

    // 3. プラグインフック: BeforeCharge（ISP分離後のIF）
    pluginCtx := plugin.NewContext(ctx)
    for _, hook := range s.pluginRegistry.GetBeforeChargeHooks() {
        if err := hook.BeforeCharge(pluginCtx, amount); err != nil {
            return nil, err
        }
    }

    // 4. 決済実行
    chargeReq := &payment.ChargeRequest{
        Amount:          amount,
        CustomerID:      input.CustomerID,
        PaymentMethodID: &input.PaymentMethodID,
        IdempotencyKey:  input.IdempotencyKey,
        Description:     "Invoice: " + invoiceID.String(),
        Metadata: map[string]string{
            "invoice_id": invoiceID.String(),
        },
    }

    chargeResp, err := s.gateway.Charge(ctx, chargeReq)
    if err != nil {
        // 失敗時のプラグインフック（ISP分離後のIF）
        for _, hook := range s.pluginRegistry.GetOnPaymentFailedHooks() {
            hook.OnPaymentFailed(pluginCtx, nil, err) // Payment未生成のためnil
        }
        return nil, err
    }

    // 5. 支払い記録作成
    p := payment.NewPayment(
        invoiceID,
        amount,
        input.PaymentMethodID,
        chargeResp.TransactionID,
    )
    p.MarkCompleted(chargeResp.TransactionID)

    if err := s.paymentRepo.Save(ctx, p); err != nil {
        return nil, err
    }

    // 6. 請求書に支払い反映
    if err := inv.RecordPayment(amount); err != nil {
        return nil, err
    }
    if err := s.invoiceRepo.Save(ctx, inv); err != nil {
        return nil, err
    }

    // 7. イベント発行（event-sourcing.md の Event 構造体に準拠）
    eventData, _ := json.Marshal(p)
    event := eventstore.Event{
        ID:            shared.NewID(),
        StreamID:      p.ID().String(),
        Type:          string(payment.EventTypePaymentCompleted),
        Version:       1,
        SchemaVersion: 1,
        Data:          eventData,
        Metadata:      eventstore.EventMetadata{},
        OccurredAt:    s.clock.Now(),
    }
    s.eventStore.Append(ctx, p.ID().String(), []eventstore.Event{event}, 0)

    // 8. プラグインフック: AfterCharge（ISP分離後のIF）
    for _, hook := range s.pluginRegistry.GetAfterChargeHooks() {
        hook.AfterCharge(pluginCtx, p)
    }

    return p, nil
}

type ProcessPaymentInput struct {
    CustomerID      string
    PaymentMethodID string
    Amount          *shared.Money  // nil = 全額
    IdempotencyKey  string
    Metadata        map[string]string
}

// Refund 返金処理
func (s *PaymentService) Refund(
    ctx context.Context,
    paymentID shared.PaymentID,
    input RefundInput,
) (*payment.RefundResponse, error) {
    // 1. 支払い記録取得
    p, err := s.paymentRepo.FindByID(ctx, paymentID)
    if err != nil {
        return nil, err
    }

    // 2. 返金リクエスト
    refundReq := &payment.RefundRequest{
        TransactionID:  p.ExternalTransactionID(),
        Amount:         input.Amount,
        Reason:         input.Reason,
        IdempotencyKey: input.IdempotencyKey,
    }

    refundResp, err := s.gateway.Refund(ctx, refundReq)
    if err != nil {
        return nil, err
    }

    // 3. 状態更新
    p.MarkRefunded(refundResp.Amount)
    if err := s.paymentRepo.Save(ctx, p); err != nil {
        return nil, err
    }

    // 4. イベント発行
    // ...

    return refundResp, nil
}

type RefundInput struct {
    Amount         *shared.Money
    Reason         payment.RefundReason
    IdempotencyKey string
}
```

---

## 6.1 Saga 補償と冪等性ストア（Issue #87 対応）

### 6.1.1 解決する課題

`ProcessPayment` は「Gateway Charge 成功 → ローカル DB 保存失敗」時にサガ補償で `Refund` を発火する。このとき、呼び出し元が **同じ `IdempotencyKey`** でリトライすると次の不整合が起きる:

1. Gateway は冪等性ヘッダのキャッシュにより「元の成功レスポンス」をそのまま返す（ただし実際のトランザクションは補償 Refund 済み）
2. `PaymentService` はこの「成功」をそのまま信じて Payment レコードを completed で保存
3. **Invoice は "支払済" になるが、Gateway にはお金がない**

これが Issue #87 で報告された競合。業界プラクティスの調査(`docs/research/2026-04-10-payment-idempotency-patterns.md` 参照)からの結論は「補償が発火したキーは**同じ論理オペレーション**としては再利用不可。**新しい effective key**で新規 Charge として実行する」。

### 6.1.2 IdempotencyStore インターフェース

```go
// application/port/idempotency_store.go
package port

// IdempotencyStore は補償済みキーと置換 effective key の対応を永続化する。
type IdempotencyStore interface {
    // MarkCompensated は (originalKey → effectiveKey) を記録する。
    // first-call-wins: 同じ originalKey への 2 回目以降の呼び出しは no-op。
    // 空 originalKey はエラー。
    MarkCompensated(ctx context.Context, originalKey, effectiveKey string) error

    // ResolveEffectiveKey は originalKey に対応する effective key を返す。
    // マッピングが存在しない場合は ("", false, nil)。
    ResolveEffectiveKey(ctx context.Context, originalKey string) (effectiveKey string, ok bool, err error)
}
```

### 6.1.3 ProcessPayment のフロー変更

```
Charge 前:
  if store != nil && input.IdempotencyKey != "" {
      if eff, ok := store.ResolveEffectiveKey(input.IdempotencyKey); ok {
          effectiveKey = eff   // 補償マーカーあり → 新キーで Charge
      }
  }

Charge(effectiveKey) → 成功
  ↓
Payment.SetIdempotencyKey(effectiveKey)  // DB 側も effective key で一貫管理
  ↓
RunInTx:
  既存の Payment を effectiveKey で検索
    あり → 既存を返却（冪等）
    なし → 新規保存

RunInTx 失敗 → Saga 補償 Refund
  ↓
  if store != nil && input.IdempotencyKey != "" {
      newEffectiveKey := newRetryEffectiveKey(input.IdempotencyKey) // originalKey + "-" + ULID
      store.MarkCompensated(input.IdempotencyKey, newEffectiveKey)
      // 失敗はログ出力のみ。補償自体は成功済み。
  }
```

### 6.1.4 設計ポイント

| 設計判断 | 理由 |
|---|---|
| **opt-in** (`WithIdempotencyStore`) | 後方互換。store 未設定なら完全に従来挙動 |
| **first-call-wins** | 並行リトライでも effective key が収束する。「同じ original key → 同じ effective key」の関係を保証 |
| **空 key はスキップ** | 空キーはそもそも冪等性が機能しない（gateway 側でも追跡できない）ため store 呼び出し不要 |
| **MarkCompensated 失敗は非致命的** | 補償自体は成功済み。ここで追加エラーを返すと呼び出し元が本質(local save failed)を見失う。ログ監視で検知 |
| **3DS (`requires_action`) 経路は影響なし** | 3DS では補償 Refund が発火しないためマーカーは書かれない。既存の pending retry は生キーで引き続き動作 |
| **Payment.idempotencyKey は effective key** | RunInTx 内冪等性チェックとgateway 側キーを一致させ、2回目 retry でも正しく既存 Payment を返す |
| **生 input.IdempotencyKey は Refund reason等に使わない** | effective key のみが gateway と DB をつなぐ「実キー」 |

### 6.1.5 業界プラクティスとの整合

| PSP | 挙動 | 本ライブラリの対応 |
|---|---|---|
| Stripe | v1: 成功・失敗問わず元レスポンスを 24h キャッシュ / v2: failed request は re-execute | effective key で新規 Charge → v1/v2 どちらでも動作 |
| Adyen | キー 7日以上保持 / `transient-error: true` で同キー retry 可 | transient-error は将来対応。補償発火時は本実装の新キー戦略で対応 |
| PayPal | `PayPal-Request-Id` を最大 45 日保持 / 同キー再送で元レスポンス | 同上 |
| GMO PG | 同キー retry で同じレスポンス | 同上 |

補償発火後の **新キー発行戦略** は全 PSP で安全（gateway は新キーを「別の論理オペレーション」として扱う）。

### 6.1.6 実装例

```go
// 利用者側セットアップ
store := postgres.NewPostgresIdempotencyStore(db)  // または inmemory.NewInMemoryIdempotencyStore()

paymentService := service.NewPaymentService(
    stripeGateway,
    paymentRepo,
    invoiceRepo,
    contractRepo,
    eventStore,
    pluginRegistry,
    clock,
    service.WithIdempotencyStore(store),  // ★ これ
    service.WithPaymentTxManager(txm),
)
```

### 6.1.7 Postgres 実装の推奨スキーマ

```sql
CREATE TABLE compensated_idempotency_keys (
    original_key   TEXT PRIMARY KEY,
    effective_key  TEXT NOT NULL,
    compensated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- MarkCompensated(originalKey, effectiveKey)
INSERT INTO compensated_idempotency_keys (original_key, effective_key)
VALUES ($1, $2)
ON CONFLICT (original_key) DO NOTHING;

-- ResolveEffectiveKey(originalKey)
SELECT effective_key FROM compensated_idempotency_keys WHERE original_key = $1;
```

プロダクション配備では、Payment repository と同一 DB に置き、可能ならサガ補償トランザクションと同一 tx 内で `INSERT` することで「補償成功 ∧ マーカー記録成功」を atomic に保証する（`TxManager.RunInTx` 内から `store.MarkCompensated` を呼ぶ拡張は future work）。

### 6.1.8 既知の制限

- **呼び出し元の連続 retry (2回以上)**: first-call-wins のため、同じ生キーの複数回 retry は**すべて同じ effective key** に収束する。2回目以降の retry も gateway 側で idempotent replay される(= Stripe v1 なら元の失敗結果、v2 なら再実行)。実運用では 1回の retry で成功することがほとんどなので問題にならないが、恒久的な失敗状況では呼び出し元が新しい生キーで試行する必要がある
- **TTL 管理**: 本 IF は TTL を強制しない。各実装側で gateway の key 保持期間(Stripe 24h / Adyen 7日 / PayPal 45日)より長く保持する reaper を設定することを推奨
- **MarkCompensated の DB 書き込み失敗**: 補償は成功しているのでデータ不整合は起きないが、次回リトライで再度 #87 の race が発生しうる。エラーログで監視すること
- **Effective key の長さ**: `originalKey + "-" + ULID(26 chars)` = original + 27 bytes。各 gateway の idempotency key 長制限(Stripe/PayPal/GMO PG = 255 bytes、**Adyen = 64 bytes**)に合わせて、呼び出し元は生キーを短く保つ必要がある。Adyen を使う場合は生キーを 37 bytes 以内にすること。ライブラリ側での truncation や validation は行わない(gateway 依存のため)が、compensation 時および store resolve 時に 64 bytes を超えた場合は warning ログを emit する
- **並行 ProcessPayment の成功 race** (#97): 同じ `IdempotencyKey` で**両方が成功経路**に入る並行呼び出しの正しさは、**`payment.Repository.Save` が idempotency_key に対する unique 制約を保証すること**に依存する。プロダクション実装の要件は以下:
    - **Postgres / MySQL**: `idempotency_key` に UNIQUE INDEX、または `TxManager.RunInTx` 内での `SELECT ... FOR UPDATE`
    - **DynamoDB**: `ConditionExpression` で key 既存時を拒否
    - **その他**: 同等の CAS 保証

  制約違反時、`Save` は `errors.Is(err, payment.ErrDuplicateIdempotencyKey)` を満たすエラー(典型的には `*payment.DuplicateIdempotencyKeyError`)を返す契約。`shared.ErrCodeDuplicateRequest` は使わない — その shared コードはコードベース内の他の duplicate-request 用途(usage record 等)で使われており、payment 用途と混同するとサガ補償が暴発しうる。詳細な根拠は `domain/payment/errors.go` の `WHY A PAYMENT-SCOPED SENTINEL` コメント参照。

  `PaymentService.ProcessPayment` はこのエラーを race 敗者のシグナルと解釈し、**RunInTx クロージャ内では何もせず内部 sentinel `errDuplicateKeyRaceSignal` を返してトランザクションを ROLLBACK させ、その後 OUTER ctx (= 新しい tx) で `FindByIdempotencyKey` を呼んで勝者のレコードを取得する**。saga compensation は発火させない — 両 goroutine は同一の gateway transaction を共有しており、refund すると勝者の有効な決済が取り消されてしまうため。

  失敗 tx 内で `FindByIdempotencyKey` を呼ばない理由は重要：Postgres の UNIQUE 違反は接続を `in_failed_sql_transaction` 状態(SQLSTATE 25P02)にし、同 tx 上の後続クエリは全て失敗する。失敗 tx 内で勝者を読みに行く設計だと、その読み出しも失敗 → 通常エラー扱い → `saga.Compensate` 発火 → 勝者の charge を refund、というサイレント・リグレッションになる。アダプタ実装者が Postgres 等で `Save` 失敗時に SAVEPOINT を張って outer tx を生かす必要は **無い**(application 側で fresh-tx 読み出しに切り替えているため)。

  Fresh-tx 読み出しで勝者がまだ visible でない場合(read-replica lag、MVCC スナップショット順序など)は、`shared.ErrCodeConflict` の transient エラーを返してリトライを促す。この場合も saga compensation は発火させない(勝者の gateway charge は実在し、refund してはならないため)。

  InMemory 実装 (`infrastructure/inmemory/payment_repository.go`) は unique 制約をシミュレートし、この契約を満たす。統合テスト `TestPaymentIdempotency_ConcurrentSuccess_Race_Integration` および `TestPaymentIdempotency_ConcurrentSuccess_AbortedTxSimulation_Integration` (Postgres aborted-tx シミュレーション) で end-to-end を検証済み。

  並行**失敗** (両方が compensation 発火) の safety は `comp-refund-{txnID}` の決定性で別途保証されており、`TestPaymentIdempotency_Concurrent_CompensationRace_Integration` で検証済み。
- **Compensation-after-3DS の orphan pending record** (別 issue: #98): 「Call 1 が 3DS requires_action で pending payment を保存 → Call 2 が Captured で tx commit fail → compensation が txn を refund + store がマーカー書き込み → Call 3 が新しい effective key で fresh charge として成功」のシーケンスで、Call 1 の pending payment record は誰も参照しない状態で repo に残る(orphan)。money flow は正しい(gateway 側は refund 済み、local invoice は Call 3 の新 txn で正しく recorded)が、repo に「死んだ pending record」がゴミとして残る。これは reconciliation job の責務とし、本ライブラリのスコープ外とする

---

## 7. ディレクトリ構成（決済追加後）

```
github.com/contract-to-cash/core/
├── domain/
│   ├── contract/
│   ├── invoice/
│   ├── payment/
│   │   ├── entity.go           # Payment エンティティ
│   │   ├── repository.go       # Payment リポジトリIF
│   │   ├── errors.go           # エラー定義
│   │   └── events.go
│   ├── balance/
│   ├── billing/
│   ├── pricing/
│   ├── product/
│   ├── usage/
│   └── shared/
│
├── application/
│   ├── port/                   # ★ 外部サービスとの統合境界
│   │   ├── gateway.go          # PaymentGateway IF（13メソッド）
│   │   ├── gateway_types.go    # リクエスト/レスポンス型
│   │   ├── customer.go         # CustomerGateway IF
│   │   ├── webhook.go          # WebhookHandler IF
│   │   ├── idempotency_store.go # IdempotencyStore IF（補償マーカー、#87対応）
│   │   └── router.go           # GatewayRouter IF
│   ├── query/
│   ├── projection/
│   ├── tx/
│   └── service/
│       ├── billing_service.go
│       ├── payment_service.go  # ★ 決済サービス
│       └── subscription_service.go
│
├── plugin/
│
├── eventstore/
│
├── plugins/
│   ├── coupon/
│   ├── tax/
│   └── invoicecleanup/
│
└── infrastructure/             # 参照実装（オプション）
    ├── gateway/
    │   ├── stripe/             # Stripe実装例
    │   │   ├── gateway.go
    │   │   ├── customer.go
    │   │   └── webhook.go
    │   └── mock/               # テスト用モック
    │       └── gateway.go
    └── inmemory/
```

---

## 8. サービスAでの実装例（Stripe）

```go
// サービスA: internal/infrastructure/gateway/stripe/gateway.go
package stripe

import (
    "context"

    "github.com/stripe/stripe-go/v76"
    "github.com/stripe/stripe-go/v76/charge"
    "github.com/stripe/stripe-go/v76/paymentintent"
    "github.com/stripe/stripe-go/v76/refund"

    "github.com/contract-to-cash/core/domain/payment"
    "github.com/contract-to-cash/core/domain/shared"
)

// Gateway Stripe決済ゲートウェイ実装
type Gateway struct {
    apiKey string
}

func NewGateway(apiKey string) *Gateway {
    stripe.Key = apiKey
    return &Gateway{apiKey: apiKey}
}

func (g *Gateway) ID() string { return "stripe" }

func (g *Gateway) SupportedMethods() []payment.PaymentMethodType {
    return []payment.PaymentMethodType{
        payment.PaymentMethodTypeCreditCard,
        payment.PaymentMethodTypeDebitCard,
    }
}

func (g *Gateway) Charge(ctx context.Context, req *payment.ChargeRequest) (*payment.ChargeResponse, error) {
    params := &stripe.PaymentIntentParams{
        Amount:   stripe.Int64(req.Amount.Amount()),
        Currency: stripe.String(string(req.Amount.Currency())),
        Customer: stripe.String(req.CustomerID),
        Confirm:  stripe.Bool(true),
    }

    if req.PaymentMethodID != nil {
        params.PaymentMethod = req.PaymentMethodID
    }

    if req.IdempotencyKey != "" {
        params.SetIdempotencyKey(req.IdempotencyKey)
    }

    // メタデータ
    for k, v := range req.Metadata {
        params.AddMetadata(k, v)
    }

    pi, err := paymentintent.New(params)
    if err != nil {
        return nil, g.convertError(err)
    }

    return &payment.ChargeResponse{
        TransactionID:   pi.ID,
        Status:          g.convertStatus(pi.Status),
        Amount:          req.Amount,
        PaymentMethodID: pi.PaymentMethod.ID,
        CreatedAt:       time.Unix(pi.Created, 0),
    }, nil
}

func (g *Gateway) Refund(ctx context.Context, req *payment.RefundRequest) (*payment.RefundResponse, error) {
    params := &stripe.RefundParams{
        PaymentIntent: stripe.String(req.TransactionID),
    }

    if req.Amount != nil {
        params.Amount = stripe.Int64(req.Amount.Amount())
    }

    if req.IdempotencyKey != "" {
        params.SetIdempotencyKey(req.IdempotencyKey)
    }

    r, err := refund.New(params)
    if err != nil {
        return nil, g.convertError(err)
    }

    return &payment.RefundResponse{
        RefundID:      r.ID,
        TransactionID: req.TransactionID,
        Status:        payment.RefundStatusSucceeded,
        Amount:        shared.NewMoney(r.Amount, shared.Currency(r.Currency)),
        RefundedAt:    time.Unix(r.Created, 0),
    }, nil
}

// Authorize, Capture, Void, Cancel, GetTransaction... 実装

func (g *Gateway) convertError(err error) error {
    stripeErr, ok := err.(*stripe.Error)
    if !ok {
        return payment.NewGatewayError(payment.ErrorCodeUnknown, err.Error())
    }

    var code payment.ErrorCode
    switch stripeErr.Code {
    case stripe.ErrorCodeCardDeclined:
        code = payment.ErrorCodeCardDeclined
    case stripe.ErrorCodeExpiredCard:
        code = payment.ErrorCodeCardExpired
    case stripe.ErrorCodeInsufficientFunds:
        code = payment.ErrorCodeInsufficientFunds
    default:
        code = payment.ErrorCodeUnknown
    }

    return &payment.GatewayError{
        Code:        code,
        Message:     stripeErr.Msg,
        DeclineCode: stripeErr.DeclineCode,
        RawError:    err,
    }
}

func (g *Gateway) convertStatus(status stripe.PaymentIntentStatus) payment.TransactionStatus {
    switch status {
    case stripe.PaymentIntentStatusSucceeded:
        return payment.TransactionStatusSucceeded
    case stripe.PaymentIntentStatusProcessing:
        return payment.TransactionStatusPending
    case stripe.PaymentIntentStatusRequiresPaymentMethod:
        return payment.TransactionStatusFailed
    default:
        return payment.TransactionStatusPending
    }
}
```

---

## 9. サービスAでのDI設定

```go
// サービスA: cmd/api/main.go
func main() {
    // ...

    // 決済ゲートウェイ（サービスAがStripeを使う場合）
    stripeGateway := stripe.NewGateway(os.Getenv("STRIPE_API_KEY"))
    
    // または複数ゲートウェイをルーティング
    router := gateway.NewGatewayRouter()
    router.Register(stripeGateway, []port.RoutingRule{
        {PaymentMethods: []port.PaymentMethodType{port.PaymentMethodTypeCreditCard}, Priority: 10},
    })

    paypayGateway := paypay.NewGateway(...)
    router.Register(paypayGateway, []port.RoutingRule{
        {PaymentMethods: []port.PaymentMethodType{port.PaymentMethodTypeQRCode}, Priority: 10},
    })
    
    router.SetFallback(stripeGateway)

    // 決済サービス初期化
    paymentService := service.NewPaymentService(
        router,          // or stripeGateway directly
        paymentRepo,
        invoiceRepo,
        contractRepo,    // 支払い方法フォールバック解決用
        eventStore,
        pluginRegistry,
        clock,
        service.WithCustomerGateway(customerGateway), // オプション: 顧客デフォルト支払い方法の参照用
    )

    // ...
}
```

---

## 10. まとめ

```mermaid
graph TB
    subgraph OSS[OSS が提供]
        subgraph PortLayer[application/port/]
            GW[port.PaymentGateway IF]
            CGW[port.CustomerGateway IF]
            WH[port.WebhookHandler IF]
            GR[port.GatewayRouter IF]
            RT[リクエスト/レスポンス型]
        end
        subgraph DomainLayer[domain/payment/]
            ENT[Payment エンティティ]
            EVT[ドメインイベント]
            REPO[Repository IF]
        end
        subgraph InfraLayer[infrastructure/gateway/]
            DR[DefaultGatewayRouter 実装]
        end
        EC[エラーコード定義]
        PS[PaymentService<br/>アプリケーション層]
        TM[テスト用モック実装]
    end

    subgraph ServiceImpl[サービスA が実装]
        SG[StripeGateway<br/>Stripe SDK]
        PPG[PayPayGateway<br/>PayPay API]
        GMOG[GMOGateway<br/>GMO API]
        ETC[etc...]
    end

    PortLayer -.->|implements| ServiceImpl
    PortLayer -.->|implements| InfraLayer
```
