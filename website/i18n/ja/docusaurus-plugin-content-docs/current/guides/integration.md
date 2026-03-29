---
sidebar_position: 1
---

# 統合ガイド

このガイドでは、Contract Billing Coreをあなたのサービスに統合する方法を説明します。

## 前提条件

- Go 1.22以上
- イベントストアとリポジトリ用のデータベース（PostgreSQL、MySQL、DynamoDBなど）
- 決済ゲートウェイの実装

## ステップ 1: リポジトリインターフェースの実装

Contract Billing Coreはドメイン層でリポジトリインターフェースを定義しています。あなたのデータベース向けに実装してください：

```go
// domain/contract/repository.go
type Repository interface {
    Save(ctx context.Context, aggregate *ContractAggregate) error
    FindByID(ctx context.Context, id shared.ContractID) (*ContractAggregate, error)
    FindByAccountID(ctx context.Context, accountID shared.AccountID) ([]*ContractAggregate, error)
    FindActiveByPlanID(ctx context.Context, planID shared.PlanID) ([]*ContractAggregate, error)
    FindExpiring(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindTrialsEndingSoon(ctx context.Context, before time.Time) ([]*ContractAggregate, error)
    FindByIDAsOf(ctx context.Context, id shared.ContractID, asOf time.Time) (*ContractAggregate, error)
    FindDueForRenewal(ctx context.Context, asOf time.Time) ([]*ContractAggregate, error)
}
```

同様に`invoice.Repository`, `payment.Repository`, `credit.Repository`, `usage.Repository`, `pricing.PriceRepository`, `product.Repository`も実装します。

### 例: PostgreSQLの契約リポジトリ

```go
type PostgresContractRepository struct {
    db         *sql.DB
    eventStore eventstore.Store
    clock      shared.Clock
}

func (r *PostgresContractRepository) Save(ctx context.Context, agg *contract.ContractAggregate) error {
    events := agg.UncommittedEvents()
    if len(events) == 0 {
        return nil
    }
    return r.eventStore.Append(ctx, string(agg.ContractID()), events, agg.Version()-len(events))
}

func (r *PostgresContractRepository) FindByID(ctx context.Context, id shared.ContractID) (*contract.ContractAggregate, error) {
    // まずスナップショットからロードを試行
    snap, _ := r.eventStore.LoadSnapshot(ctx, string(id))

    agg := contract.NewContractAggregate(id, r.clock)
    if snap != nil {
        agg.LoadFromSnapshot(*snap)
        // スナップショット以降のイベントのみロード
    } else {
        events, _ := r.eventStore.Load(ctx, string(id))
        agg.LoadFromHistory(events)
    }
    return agg, nil
}
```

## ステップ 2: Event Storeの実装

`eventstore.Store`インターフェースをデータベース向けに実装：

```go
type PostgresEventStore struct {
    db    *sql.DB
    clock shared.Clock
}

func (s *PostgresEventStore) Append(ctx context.Context, streamID string, events []eventstore.Event, expectedVersion int) error {
    tx, _ := s.db.BeginTx(ctx, nil)
    defer tx.Rollback()

    // 現在のバージョンを確認（楽観的ロック）
    var currentVersion int
    tx.QueryRowContext(ctx,
        "SELECT COALESCE(MAX(version), 0) FROM events WHERE stream_id = $1", streamID,
    ).Scan(&currentVersion)

    if currentVersion != expectedVersion {
        return fmt.Errorf("楽観的ロック: 期待バージョン %d, 実際 %d", expectedVersion, currentVersion)
    }

    // イベントを挿入
    for _, e := range events {
        tx.ExecContext(ctx,
            "INSERT INTO events (id, stream_id, type, version, data, metadata, occurred_at, recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
            e.ID, streamID, e.Type, e.Version, e.Data, e.Metadata, e.OccurredAt, s.clock.Now(),
        )
    }

    return tx.Commit()
}
```

## ステップ 3: 決済ゲートウェイの実装

`application/port.PaymentGateway`を決済プロバイダー向けに実装：

```go
type MyGateway struct { /* ... */ }

func (g *MyGateway) ID() string { return "my-gateway" }
func (g *MyGateway) SupportedMethods() []port.PaymentMethodType {
    return []port.PaymentMethodType{port.PaymentMethodTypeCreditCard}
}
func (g *MyGateway) Charge(ctx context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
    // 決済プロバイダーAPIを呼び出し
}
// ... 残りのメソッドも実装
```

## ステップ 4: 全体の結線

```go
func NewBillingModule(db *sql.DB, gateway port.PaymentGateway) *BillingModule {
    clock := &shared.SystemClock{}
    eventStore := NewPostgresEventStore(db, clock)

    contractRepo := NewPostgresContractRepository(db, eventStore, clock)
    invoiceRepo := NewPostgresInvoiceRepository(db, clock)
    paymentRepo := NewPostgresPaymentRepository(db)
    balanceRepo := NewPostgresCreditRepository(db, clock)
    usageRepo := NewPostgresUsageRepository(db)
    priceRepo := NewPostgresPriceRepository(db)
    productRepo := NewPostgresProductRepository(db)

    // プラグイン
    registry := plugin.NewRegistry()
    registry.Register(tax.NewTaxPlugin(&tax.JapaneseTaxCalculator{}))
    // ... カスタムプラグインを登録
    registry.InitializeAll(ctx, configs)

    billingService := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo, balanceRepo,
        credit.BalanceConfig{
            DowngradePolicy:    credit.BalancePolicyLedger,
            CancellationPolicy: credit.BalancePolicyLedger,
        },
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30},
        clock,
    )

    paymentService := service.NewPaymentService(
        gateway, paymentRepo, invoiceRepo, contractRepo,
        nil, eventStore, registry, clock,
    )

    return &BillingModule{
        BillingService:  billingService,
        PaymentService:  paymentService,
    }
}
```

## ステップ 5: バッチジョブの設定

定期実行するバッチプロセッサを設定：

```go
// 契約更新（毎日実行）
renewalProcessor := batch.NewContractRenewalProcessor(contractRepo, registry, clock)
renewalProcessor.Process(ctx, batch.BatchOptions{
    ContinueOnError: true,
    Concurrency:     4,
})

// スナップショット作成（定期的に実行してパフォーマンス向上）
snapshotService := service.NewSnapshotService(eventStore, clock, 50)
```

## ディレクトリ構成

Contract Billing Coreを使用するサービスの典型的な構成：

```
your-service/
├── cmd/
│   └── server/main.go
├── internal/
│   ├── billing/
│   │   ├── module.go           # 結線
│   │   ├── gateway_stripe.go   # PaymentGateway実装
│   │   └── plugins/
│   │       └── my_discount.go  # カスタムプラグイン
│   ├── infrastructure/
│   │   ├── postgres/
│   │   │   ├── event_store.go
│   │   │   ├── contract_repo.go
│   │   │   ├── invoice_repo.go
│   │   │   └── ...
│   │   └── migrations/
│   └── api/
│       └── billing_handler.go  # HTTP/gRPCハンドラ
└── go.mod
```
