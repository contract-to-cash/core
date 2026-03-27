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
    creditRepo,    // credit.Repository
    creditConfig,  // credit.CreditConfig
    priceRepo,     // pricing.PriceRepository
    productRepo,   // product.Repository
    registry,      // *plugin.Registry
    billingConfig, // service.BillingConfig
    clock,         // shared.Clock
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
    routingConfig, // *RoutingConfig（オプション）
    eventStore,    // eventstore.Store
    registry,      // *plugin.Registry
    clock,         // shared.Clock
)
```

### ProcessPayment

```go
type ProcessPaymentInput struct {
    PaymentMethodID string       // オプション（未指定時はフォールバックチェーンで解決）
    Amount          shared.Money
    Currency        shared.Currency
    IdempotencyKey  string       // 重複防止のため必須
}

func (s *PaymentService) ProcessPayment(
    ctx context.Context,
    invoiceID shared.InvoiceID,
    input ProcessPaymentInput,
) (*payment.Payment, error)
```

フロー: BeforeChargeHook → Gateway.Charge → AfterChargeHook（成功） / OnPaymentFailedHook（失敗）

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
