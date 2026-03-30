---
sidebar_position: 2
---

# サービスリファレンス

## BillingService

プラグインパイプラインによる請求書生成をオーケストレーション。

```go
import "github.com/contract-to-cash/core/application/service"

billingService := service.NewBillingService(
    contractRepo,  // contract.Repository
    invoiceRepo,   // invoice.Repository
    usageRepo,     // usage.Repository
    balanceConfig,  // balance.BalanceConfig
    priceRepo,     // pricing.PriceRepository
    productRepo,   // product.Repository
    registry,      // *plugin.Registry
    billingConfig, // service.BillingConfig
    clock,         // shared.Clock
    // オプション:
    service.WithBalanceRepo(balanceRepo),       // balance.Repository（オプション）
    service.WithBillingTxManager(txManager),    // tx.TxManager（オプション、デフォルト: NoopTxManager）
    service.WithBillingLogger(logger),          // *slog.Logger（オプション、デフォルト: slog.Default()）
)
```

### BillingConfig

```go
type BillingConfig struct {
    GracePeriod      time.Duration // 支払い猶予期間
    DaysUntilDue     int           // 請求書作成から支払期限までの日数
    CollectionMethod string        // "send_invoice" または "auto_charge"
}
```

### GenerateInvoice

```go
func (s *BillingService) GenerateInvoice(
    ctx context.Context,
    contractID shared.ContractID,
    billingPeriod shared.DateRange,
) (*invoice.Invoice, error)
```

14ステップの課金パイプラインを実行：
1. 契約ロード → 2. BeforeCalculationフック → 3. 小計計算（Price参照） → 4. DiscountHook適用 → 5. 割引キャップ → 6. 割引後小計 → 7. TaxHook適用 → 8. 合計計算 → 9. クレジット適用（FIFO） → 10. 請求金額 → 11. 請求書作成 → 12. 保存 → 13. AfterCalculationフック → 14. 返却

---

## PaymentService

ゲートウェイ抽象化とフック付きの決済処理。

```go
paymentService := service.NewPaymentService(
    gateway,       // port.PaymentGateway
    paymentRepo,   // payment.Repository
    invoiceRepo,   // invoice.Repository
    contractRepo,  // contract.Repository
    eventStore,    // eventstore.Store
    registry,      // *plugin.Registry
    clock,         // shared.Clock
    // オプション:
    service.WithCustomerGateway(customerGateway),  // port.CustomerGateway — フォールバック解決用
    service.WithPaymentTxManager(txManager),       // tx.TxManager（デフォルト: NoopTxManager）
    service.WithPaymentLogger(logger),             // *slog.Logger（デフォルト: slog.Default()）
)
```

### ProcessPayment

```go
type ProcessPaymentInput struct {
    PaymentMethodID string       // オプション（未指定時はフォールバックチェーンで解決）
    Amount          shared.Money
    Currency        shared.Currency
    IdempotencyKey  string       // 重複防止のため必須
    Metadata        map[string]string
}

func (s *PaymentService) ProcessPayment(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    input ProcessPaymentInput,
) (*payment.Payment, error)
```

フロー: BeforeChargeHook → Gateway.Charge → AfterChargeHook（成功） / OnPaymentFailedHook（失敗）

### Refund

```go
type RefundInput struct {
    Amount *shared.Money   // nil の場合、未返金残額を全額返金
    Reason port.RefundReason
}

func (s *PaymentService) Refund(
    ctx context.Context,
    paymentID shared.PaymentID,
    input RefundInput,
) error
```

支払いの返金を処理します。`Amount`がnilの場合、未返金残額を全額返金します。返金は決済ゲートウェイを通じて発行され、トランザクション内で支払いエンティティに記録されます。コミット後に`OnRefundHook`フックが実行されます。

---

## CreditNoteService

クレジットノートの作成、発行、請求書リビジョンをオーケストレーション。

```go
creditNoteService := service.NewCreditNoteService(
    invoiceRepo,    // invoice.Repository
    creditNoteRepo, // invoice.CreditNoteRepository
    registry,       // *plugin.Registry
    clock,          // shared.Clock
    // オプション:
    service.WithBillingService(billingService),     // *BillingService — ReissueInvoiceに必要
    service.WithCreditNoteTxManager(txManager),     // tx.TxManager（デフォルト: NoopTxManager）
    service.WithCreditNoteLogger(logger),           // *slog.Logger（デフォルト: slog.Default()）
)
```

### CreateCreditNote

```go
func (s *CreditNoteService) CreateCreditNote(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    reason invoice.CreditNoteReason,
    items []invoice.CreditNoteItem,
    memo string,
) (*invoice.CreditNote, error)
```

対象請求書のステータスが `issued`, `paid`, `partial_paid`, `overdue` の場合のみ作成可能。クレジットノートの合計が元請求書の合計を超えることはできない。

### IssueCreditNote

```go
func (s *CreditNoteService) IssueCreditNote(
    ctx context.Context,
    creditNoteID shared.CreditNoteID,
) (*invoice.CreditNote, error)
```

draft → issued に遷移。発行後に `OnCreditNoteIssuedHook` を実行。

### ApplyCreditNote

```go
func (s *CreditNoteService) ApplyCreditNote(
    ctx context.Context,
    creditNoteID shared.CreditNoteID,
    creditAmount shared.Money,
) (*invoice.CreditNote, error)
```

issued → applied に遷移（アカウントクレジットとして適用）。

### RefundCreditNote

```go
func (s *CreditNoteService) RefundCreditNote(
    ctx context.Context,
    creditNoteID shared.CreditNoteID,
    refundAmount shared.Money,
) (*invoice.CreditNote, error)
```

issued → refunded に遷移（決済返金として処理）。

### ReissueInvoice

```go
func (s *CreditNoteService) ReissueInvoice(
    ctx context.Context,
    originalInvoiceID shared.InvoiceID,
    reason string,
) (*invoice.Invoice, error)
```

元の請求書を無効化（VoidWithReason）し、BillingServiceで代替請求書を生成。代替請求書には `revisionOf`（直接の親）と `originalInvoiceID`（チェーンのルート）が設定される。全ての書き込みはTxManager内のトランザクションで実行。完了後に `OnInvoiceRevisedHook` を実行。

---

## SnapshotService

パフォーマンスのためのイベントソーシングスナップショット管理。

```go
snapshotService := service.NewSnapshotService(
    eventStore, // eventstore.Store
    clock,      // shared.Clock
    interval,   // int — Nイベントごとにスナップショット
)

snapshotService.CreateSnapshot(ctx, aggregate) error
```

---

## TemporalQueryService

イベントソース集約の時間旅行クエリ。

```go
import "github.com/contract-to-cash/core/application/query"

queryService := query.NewTemporalQueryService(eventStore, clock)

// 特定時点の状態を再構築
agg, err := queryService.GetContractAsOf(ctx, contractID, asOf time.Time)

// 完全な変更履歴
history, err := queryService.GetContractHistory(ctx, contractID)
// 戻り値: []ContractHistoryEntry{EventType, OccurredAt, UserID, Data}
```

---

## ProjectionService

イベントから読み取り最適化ビューを構築。

```go
import "github.com/contract-to-cash/core/application/projection"

type Projector interface {
    Project(ctx context.Context, event eventstore.Event) error
    Rebuild(ctx context.Context, until time.Time) error
}

projService := projection.NewProjectionService(eventStore, projection.ProjectionOptions{
    SyncMode:   true,
    BatchSize:  100,
    MaxRetries: 3,
    RetryDelay: time.Second,
    Logger:     logger, // *slog.Logger（デフォルト: slog.Default()）
})

projService.RegisterProjector(myProjector)
projService.Start(ctx) // ブロッキング — イベントを購読
projService.ProcessEvent(ctx, event) // 手動処理
```

---

## BatchProcessor

```go
import "github.com/contract-to-cash/core/batch"

type BatchProcessor interface {
    Process(ctx context.Context, opts BatchOptions) (*BatchResult, error)
}

type BatchOptions struct {
    DryRun          bool // ドライラン
    ContinueOnError bool // エラー時も続行
    Concurrency     int  // 並行数
}

type BatchResult struct {
    Total     int
    Succeeded int
    Failed    int
    Errors    []error
}
```
