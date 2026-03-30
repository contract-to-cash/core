---
sidebar_position: 7
---

# システム統合

このガイドでは、Contract Billing Coreをあなたのサービスに統合する方法を説明します。

## 前提条件

- Go 1.25以上
- イベントストアとリポジトリ用のデータベース（PostgreSQL、MySQL、DynamoDBなど）
- 決済ゲートウェイの実装（[決済統合](./payment-integration)を参照）

## ステップ 1: リポジトリインターフェースの実装

Contract Billing Coreはドメイン層でリポジトリインターフェースを定義しています。あなたのデータベース向けに実装してください。完全なインターフェース定義は[ドメイン型APIリファレンス](../api/domain-types)を参照。

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
        events, _ := r.eventStore.LoadUntilVersion(ctx, string(id), snap.Version)
        // ... 残りのイベントをリプレイ
    } else {
        events, _ := r.eventStore.Load(ctx, string(id))
        agg.LoadFromHistory(events)
    }
    return agg, nil
}
```

同様に`invoice.Repository`, `payment.Repository`, `balance.Repository`, `usage.Repository`, `pricing.PriceRepository`, `product.Repository`も実装します。

## ステップ 2: Event Storeの実装

`eventstore.Store`インターフェースをデータベース向けに実装。完全なインターフェースは[Event Store APIリファレンス](../api/event-store)を参照。

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

## ステップ 3: 全体の結線

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
    // ... カスタมプラグインを登録
    registry.InitializeAll(ctx, configs)

    billingService := service.NewBillingService(
        contractRepo, invoiceRepo, usageRepo,
        balance.BalanceConfig{
            DowngradePolicy:    balance.BalancePolicyLedger,
            CancellationPolicy: balance.BalancePolicyLedger,
        },
        priceRepo, productRepo, registry,
        service.BillingConfig{DaysUntilDue: 30},
        clock,
        service.WithBalanceRepo(balanceRepo),
    )

    paymentService := service.NewPaymentService(
        gateway, paymentRepo, invoiceRepo, contractRepo,
        eventStore, registry, clock,
    )

    return &BillingModule{
        BillingService:  billingService,
        PaymentService:  paymentService,
    }
}
```

## ステップ 4: バッチジョブの設定

定期実行するバッチプロセッサを設定：

```go
// 契約更新（毎日実行）
renewalProcessor := batch.NewContractRenewalProcessor(contractRepo, registry, clock)
renewalProcessor.Process(ctx, batch.BatchOptions{
    ContinueOnError: true,
    Concurrency:     4,
})

// スナップショット作成（定期的に実行してパフォーマンス向上）
snapshotService := service.NewSnapshotService(eventStore, clock, 50) // 50イベントごと
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
