# Contract Billing Core - 利用ガイド

## 1. 利用パターン概要

### サービスAの全体構成

```
┌─────────────────────────────────────────────────────────────────────┐
│                         サービスA                                    │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    サービスA 固有ドメイン                      │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │   │
│  │  │    User     │  │   Content   │  │   Notification      │  │   │
│  │  │   Domain    │  │   Domain    │  │      Domain         │  │   │
│  │  └─────────────┘  └─────────────┘  └─────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              │                                      │
│                              │ 連携                                 │
│                              ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              contract-billing-core (OSS)                     │   │
│  │  ┌───────────┐ ┌───────────┐ ┌───────────┐ ┌─────────────┐  │   │
│  │  │ Contract  │ │  Invoice  │ │  Payment  │ │   Plugin    │  │   │
│  │  │  Domain   │ │  Domain   │ │  Domain   │ │   System    │  │   │
│  │  └───────────┘ └───────────┘ └───────────┘ └─────────────┘  │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                              │                                      │
│                              │ implements                           │
│                              ▼                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                サービスA インフラ層                           │   │
│  │  ┌─────────────────────────────────────────────────────┐    │   │
│  │  │  billing リポジトリ実装（PostgreSQL/MySQL/etc）       │    │   │
│  │  └─────────────────────────────────────────────────────┘    │   │
│  │  ┌─────────────────────────────────────────────────────┐    │   │
│  │  │  サービスA 固有リポジトリ実装                          │    │   │
│  │  └─────────────────────────────────────────────────────┘    │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 利用方法は3パターン

| パターン | 説明 | 適したケース |
|----------|------|-------------|
| **A. ライブラリ利用** | go getして自サービスに組み込み | 一般的な利用 |
| **B. フォーク利用** | forkしてカスタマイズ | 大幅な改変が必要な場合 |
| **C. 参照実装** | 設計だけ参考にして自前実装 | 学習目的、特殊要件 |

**推奨は A. ライブラリ利用** です。

---

## 2. サービスAでの導入手順

### Step 1: パッケージ取得

```bash
# サービスAのプロジェクトで
go get github.com/yourorg/contract-billing-core
go get github.com/yourorg/contract-billing-core/plugins/coupon  # 必要なら
```

### Step 2: ディレクトリ構成

```
service-a/
├── cmd/
│   └── api/
│       └── main.go
│
├── internal/
│   ├── domain/                      # サービスA固有ドメイン
│   │   ├── user/
│   │   │   ├── entity.go
│   │   │   └── repository.go
│   │   ├── content/
│   │   └── subscription/            # billing-coreとの連携層
│   │       ├── service.go           # ★ ここでOSSを使う
│   │       └── adapter.go
│   │
│   ├── application/
│   │   ├── usecase/
│   │   │   ├── signup_usecase.go
│   │   │   ├── subscribe_usecase.go # ★ 契約作成ユースケース
│   │   │   └── billing_usecase.go   # ★ 請求ユースケース
│   │   └── service/
│   │
│   ├── infrastructure/
│   │   ├── postgres/
│   │   │   ├── user_repository.go
│   │   │   ├── content_repository.go
│   │   │   ├── billing_repository.go  # ★ OSSインターフェースの実装
│   │   │   └── migrations/
│   │   │       ├── 001_users.sql
│   │   │       ├── 002_contents.sql
│   │   │       └── 003_billing.sql    # ★ OSSのテーブル
│   │   │
│   │   └── external/
│   │       ├── stripe/                # 決済ゲートウェイ
│   │       └── sendgrid/              # メール通知
│   │
│   ├── interface/
│   │   └── http/
│   │       ├── handler/
│   │       └── router.go
│   │
│   └── plugin/                        # ★ サービスA固有プラグイン
│       ├── loyalty_point/             # 例: ポイント付与プラグイン
│       └── notification/              # 例: 請求通知プラグイン
│
├── go.mod
└── go.sum
```

### Step 3: go.mod

```go
module github.com/yourcompany/service-a

go 1.22

require (
    github.com/yourorg/contract-billing-core v1.0.0
    github.com/yourorg/contract-billing-core/plugins/coupon v1.0.0
    // ... その他の依存
)
```

---

## 3. 具体的な統合コード

### 3.1 インフラ層：リポジトリ実装

OSSが定義するインターフェースを、サービスAのDBで実装する。

```go
// internal/infrastructure/postgres/billing_repository.go
package postgres

import (
    "context"
    "database/sql"

    // OSSのドメインをインポート
    "github.com/yourorg/contract-billing-core/domain/contract"
    "github.com/yourorg/contract-billing-core/domain/invoice"
    "github.com/yourorg/contract-billing-core/domain/shared"
    "github.com/yourorg/contract-billing-core/eventstore"
)

// ============================================================
// 契約リポジトリ実装
// ============================================================

// ContractRepository はOSSの contract.Repository を実装
type ContractRepository struct {
    db *sql.DB
}

func NewContractRepository(db *sql.DB) contract.Repository {
    return &ContractRepository{db: db}
}

// OSSのインターフェースを実装
func (r *ContractRepository) Save(ctx context.Context, c *contract.Contract) error {
    query := `
        INSERT INTO billing_contracts (id, account_id, type, status, ...)
        VALUES ($1, $2, $3, $4, ...)
        ON CONFLICT (id) DO UPDATE SET ...
    `
    _, err := r.db.ExecContext(ctx, query, 
        c.ID().String(),
        c.AccountID().String(),
        // ...
    )
    return err
}

func (r *ContractRepository) FindByID(ctx context.Context, id shared.ContractID) (*contract.Contract, error) {
    // 実装
}

func (r *ContractRepository) FindByAccountID(ctx context.Context, accountID shared.AccountID, opts ...contract.QueryOption) ([]*contract.Contract, error) {
    // 実装
}

// ... 他のメソッド

// ============================================================
// 請求書リポジトリ実装
// ============================================================

type InvoiceRepository struct {
    db *sql.DB
}

func NewInvoiceRepository(db *sql.DB) invoice.Repository {
    return &InvoiceRepository{db: db}
}

// ... 実装

// ============================================================
// Event Store 実装
// ============================================================

type EventStore struct {
    db *sql.DB
}

func NewEventStore(db *sql.DB) eventstore.Store {
    return &EventStore{db: db}
}

// ... 実装
```

### 3.2 アプリケーション層：ユースケース

```go
// internal/application/usecase/subscribe_usecase.go
package usecase

import (
    "context"
    "time"

    // OSSのドメイン
    "github.com/yourorg/contract-billing-core/domain/contract"
    "github.com/yourorg/contract-billing-core/domain/shared"

    // サービスA固有ドメイン
    "github.com/yourcompany/service-a/internal/domain/user"
)

// SubscribeUseCase サブスクリプション契約ユースケース
type SubscribeUseCase struct {
    userRepo     user.Repository          // サービスA固有
    contractRepo contract.Repository      // ★ OSSのインターフェース
}

func NewSubscribeUseCase(
    userRepo user.Repository,
    contractRepo contract.Repository,
) *SubscribeUseCase {
    return &SubscribeUseCase{
        userRepo:     userRepo,
        contractRepo: contractRepo,
    }
}

// Input
type SubscribeInput struct {
    UserID    string
    PlanID    string
    StartDate time.Time
}

// Output
type SubscribeOutput struct {
    ContractID string
    Status     string
}

// Execute サブスクリプション契約を作成
func (uc *SubscribeUseCase) Execute(ctx context.Context, input SubscribeInput) (*SubscribeOutput, error) {
    // 1. ユーザー存在確認（サービスA固有）
    u, err := uc.userRepo.FindByID(ctx, input.UserID)
    if err != nil {
        return nil, err
    }

    // 2. プランから契約アイテム構築
    items := []contract.ContractItem{
        contract.NewContractItem(
            shared.ProductID(input.PlanID),
            1,
            shared.NewMoney(980, shared.JPY),  // プランから取得した価格
            contract.FlatPricing{Price: shared.NewMoney(980, shared.JPY)},
        ),
    }

    // 3. 契約作成（OSSのドメインモデルを使用）
    billingCycle := &contract.BillingCycle{
        Interval:      contract.BillingIntervalMonth,
        IntervalCount: 1,
        AnchorDate:    input.StartDate,
    }

    c, err := contract.NewContract(
        shared.AccountID(u.ID),  // サービスAのUserIDをAccountIDにマッピング
        contract.ContractTypeSubscription,
        items,
        input.StartDate,
        billingCycle,
    )
    if err != nil {
        return nil, err
    }

    // 4. 契約を有効化
    if err := c.Activate(); err != nil {
        return nil, err
    }

    // 5. 保存（OSSのリポジトリインターフェース経由）
    if err := uc.contractRepo.Save(ctx, c); err != nil {
        return nil, err
    }

    return &SubscribeOutput{
        ContractID: c.ID().String(),
        Status:     string(c.Status()),
    }, nil
}
```

### 3.3 請求処理ユースケース

```go
// internal/application/usecase/billing_usecase.go
package usecase

import (
    "context"

    // OSSのアプリケーションサービス
    "github.com/yourorg/contract-billing-core/application/service"
    "github.com/yourorg/contract-billing-core/domain/shared"
)

// BillingUseCase 請求ユースケース
type BillingUseCase struct {
    billingService *service.BillingService  // ★ OSSのサービスをそのまま使う
}

func NewBillingUseCase(billingService *service.BillingService) *BillingUseCase {
    return &BillingUseCase{billingService: billingService}
}

// GenerateMonthlyInvoices 月次請求書生成
func (uc *BillingUseCase) GenerateMonthlyInvoices(ctx context.Context) error {
    // OSSのサービスを呼び出すだけ
    // ...
    return nil
}

// ApplyCoupon クーポン適用
func (uc *BillingUseCase) ApplyCoupon(
    ctx context.Context,
    invoiceID string,
    couponCode string,
) error {
    _, err := uc.billingService.ApplyCoupon(
        ctx,
        shared.InvoiceID(invoiceID),
        couponCode,
    )
    return err
}
```

### 3.4 DI（依存性注入）の組み立て

```go
// cmd/api/main.go
package main

import (
    "database/sql"
    "log"
    "net/http"

    _ "github.com/lib/pq"

    // OSSパッケージ
    billingService "github.com/yourorg/contract-billing-core/application/service"
    "github.com/yourorg/contract-billing-core/plugin"
    couponPlugin "github.com/yourorg/contract-billing-core/plugins/coupon"

    // サービスA
    "github.com/yourcompany/service-a/internal/application/usecase"
    "github.com/yourcompany/service-a/internal/infrastructure/postgres"
    serviceAPlugin "github.com/yourcompany/service-a/internal/plugin/notification"
)

func main() {
    // ============================================================
    // 1. DB接続
    // ============================================================
    db, err := sql.Open("postgres", "postgres://...")
    if err != nil {
        log.Fatal(err)
    }

    // ============================================================
    // 2. リポジトリ初期化（OSSインターフェースの実装を注入）
    // ============================================================
    
    // サービスA固有
    userRepo := postgres.NewUserRepository(db)
    contentRepo := postgres.NewContentRepository(db)
    
    // OSS用（サービスAが実装したものを注入）
    contractRepo := postgres.NewContractRepository(db)    // contract.Repository を実装
    invoiceRepo := postgres.NewInvoiceRepository(db)      // invoice.Repository を実装
    eventStore := postgres.NewEventStore(db)              // eventstore.Store を実装

    // ============================================================
    // 3. プラグイン設定
    // ============================================================
    eventBus := NewEventBus()
    logger := NewLogger()
    
    registry := plugin.NewRegistry(eventBus, logger)

    // クーポンプラグイン（OSS提供）
    couponDefRepo := postgres.NewCouponDefinitionRepository(db)
    couponInstRepo := postgres.NewCouponInstanceRepository(db)
    couponUsageRepo := postgres.NewCouponUsageRepository(db)
    
    cp := couponPlugin.NewPlugin(couponDefRepo, couponInstRepo, couponUsageRepo)
    registry.Register(cp)
    registry.Enable(ctx, "coupon", map[string]interface{}{
        "max_coupons_per_invoice": 1,
    })

    // 請求通知プラグイン（サービスA独自）
    notificationPlugin := serviceAPlugin.NewNotificationPlugin(sendgridClient)
    registry.Register(notificationPlugin)
    registry.Enable(ctx, "notification", nil)

    // ============================================================
    // 4. OSSサービス初期化
    // ============================================================
    billingSvc := billingService.NewBillingService(
        contractRepo,
        invoiceRepo,
        eventStore,
        registry,
    )

    // ============================================================
    // 5. ユースケース初期化
    // ============================================================
    subscribeUC := usecase.NewSubscribeUseCase(userRepo, contractRepo)
    billingUC := usecase.NewBillingUseCase(billingSvc)

    // ============================================================
    // 6. HTTPハンドラ設定
    // ============================================================
    handler := setupRouter(subscribeUC, billingUC)
    
    log.Println("Starting server on :8080")
    http.ListenAndServe(":8080", handler)
}
```

---

## 4. サービスA固有プラグインの作成

OSSのプラグインインターフェースを実装して、サービスA独自の機能を追加できる。

```go
// internal/plugin/notification/plugin.go
package notification

import (
    "context"

    "github.com/yourorg/contract-billing-core/domain/invoice"
    "github.com/yourorg/contract-billing-core/plugin"
)

const PluginID = "service-a-notification"

// Plugin 請求書発行時にメール通知を送るプラグイン
type Plugin struct {
    emailClient EmailClient
    logger      plugin.Logger
}

func NewNotificationPlugin(emailClient EmailClient) *Plugin {
    return &Plugin{emailClient: emailClient}
}

// Plugin interface 実装
func (p *Plugin) ID() string             { return PluginID }
func (p *Plugin) Version() string        { return "1.0.0" }
func (p *Plugin) Dependencies() []string { return nil }

func (p *Plugin) OnInstall(ctx context.Context, pc *plugin.Context) error  { return nil }
func (p *Plugin) OnUninstall(ctx context.Context, pc *plugin.Context) error { return nil }
func (p *Plugin) OnEnable(ctx context.Context, pc *plugin.Context) error {
    p.logger = pc.Logger
    return nil
}
func (p *Plugin) OnDisable(ctx context.Context, pc *plugin.Context) error { return nil }

// InvoiceCalculationHook 実装
func (p *Plugin) BeforeCalculation(ctx context.Context, calcCtx *plugin.InvoiceCalculationContext) error {
    return nil
}

func (p *Plugin) CalculateAdjustments(ctx context.Context, calcCtx *plugin.InvoiceCalculationContext) ([]invoice.Adjustment, error) {
    // 調整は行わない
    return nil, nil
}

func (p *Plugin) AfterCalculation(ctx context.Context, calcCtx *plugin.InvoiceCalculationContext, inv *invoice.Invoice) error {
    // 請求書生成後にメール通知
    err := p.emailClient.SendInvoiceNotification(ctx, SendInvoiceNotificationInput{
        AccountID: calcCtx.Account.ID.String(),
        InvoiceID: inv.ID().String(),
        Total:     inv.Total().Amount(),
        DueDate:   inv.DueDate(),
    })
    if err != nil {
        p.logger.Error("failed to send invoice notification", "error", err)
        // 通知失敗は請求処理を止めない
    }
    return nil
}
```

---

## 5. AccountID のマッピング戦略

OSSは `shared.AccountID` を使うが、サービスAには `user.UserID` がある。

### 戦略1: 同一IDを使用（推奨）

```go
// サービスAのユーザーIDをそのままAccountIDとして使用
accountID := shared.AccountID(user.ID)
```

### 戦略2: マッピングテーブル

```go
// サービスA固有のマッピングテーブル
// user_billing_accounts (user_id, billing_account_id)

type AccountMapper struct {
    db *sql.DB
}

func (m *AccountMapper) GetBillingAccountID(ctx context.Context, userID string) (shared.AccountID, error) {
    var accountID string
    err := m.db.QueryRowContext(ctx, `
        SELECT billing_account_id FROM user_billing_accounts WHERE user_id = $1
    `, userID).Scan(&accountID)
    
    if err == sql.ErrNoRows {
        // 新規作成
        accountID = shared.NewAccountID().String()
        _, err = m.db.ExecContext(ctx, `
            INSERT INTO user_billing_accounts (user_id, billing_account_id) VALUES ($1, $2)
        `, userID, accountID)
    }
    
    return shared.AccountID(accountID), err
}
```

---

## 6. マイグレーション戦略

### OSSが提供するマイグレーションをサービスAに組み込む

```
service-a/
└── internal/infrastructure/postgres/migrations/
    ├── 001_users.sql           # サービスA固有
    ├── 002_contents.sql        # サービスA固有
    ├── 003_billing_core.sql    # ★ OSSからコピーまたは参照
    └── 004_billing_coupon.sql  # ★ クーポンプラグイン用
```

```sql
-- 003_billing_core.sql
-- OSSの提供するスキーマを使用（プレフィックス付与も可）

CREATE TABLE billing_contracts (
    id UUID PRIMARY KEY,
    account_id UUID NOT NULL,
    contract_type VARCHAR(50) NOT NULL,
    -- ...
);

CREATE TABLE billing_invoices (
    -- ...
);

CREATE TABLE billing_events (
    -- ...
);
```

---

## 7. 利用パターンまとめ

```
┌────────────────────────────────────────────────────────────┐
│                     サービスA が書くコード                   │
├────────────────────────────────────────────────────────────┤
│                                                            │
│  1. リポジトリ実装                                          │
│     └─ OSSのインターフェースをPostgreSQL等で実装            │
│                                                            │
│  2. ユースケース                                            │
│     └─ OSSのドメインモデル・サービスを呼び出す              │
│                                                            │
│  3. 固有プラグイン（オプション）                            │
│     └─ OSSのフックを実装してカスタム処理を追加              │
│                                                            │
│  4. DI組み立て                                              │
│     └─ main.goで全てを結合                                 │
│                                                            │
│  5. マイグレーション                                        │
│     └─ OSSのスキーマを自サービスのDBに適用                  │
│                                                            │
└────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────┐
│                     OSS が提供するもの                       │
├────────────────────────────────────────────────────────────┤
│                                                            │
│  • ドメインモデル（Contract, Invoice, Payment...）          │
│  • リポジトリインターフェース                               │
│  • アプリケーションサービス（BillingService等）             │
│  • プラグインシステム                                       │
│  • 公式プラグイン（Coupon, Tax等）                          │
│  • Event Store インターフェース                             │
│  • 参照用マイグレーションSQL                                │
│                                                            │
└────────────────────────────────────────────────────────────┘
```

### サービスAの実装量イメージ

| 項目 | 実装量 | 備考 |
|------|--------|------|
| リポジトリ実装 | 中 | OSSのインターフェースに沿って実装 |
| ユースケース | 小 | OSSサービスを呼ぶだけの薄いラッパー |
| 固有プラグイン | 小〜中 | 必要な分だけ |
| DI/設定 | 小 | main.goで組み立て |
| マイグレーション | 小 | OSSのSQLをコピー |

**結論**: サービスAは「リポジトリ実装」が主な作業。ドメインロジックはOSSを再利用。
