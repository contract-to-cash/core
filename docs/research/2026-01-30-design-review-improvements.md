---
sidebar_label: Design Review Improvements (2026-01-30)
---

# 設計レビュー: 改善計画

**作成日**: 2026-01-30
**対象**: Contract Billing Core 全体アーキテクチャ

---

## 概要

既存設計ドキュメント全体のレビューを行い、実装前に対処すべき5つの構造的問題を特定した。
本ドキュメントでは各問題の詳細と改善設計を記述する。

### 改善一覧

| # | 改善 | 影響範囲 | 重要度 |
|---|------|---------|--------|
| 1 | ドメイン層とインフラ層の境界修正 | `domain/payment/`, `application/` | Critical |
| 2 | プラグインシステムのフック分離 | `plugin/` | High |
| 3 | イベントソーシングの集約設計改善 | `eventstore/`, `domain/contract/` | High |
| 4 | パッケージ構成と循環依存の解消 | 全パッケージ | Critical |
| 5 | ドメインエラーの構造化 | `domain/shared/`, 全ドメイン | Medium |

---

## 改善1: ドメイン層とインフラ層の境界修正

> **注**: 本改善は設計段階で先行適用済み。
> - `docs/design/payment-gateway.md`: Gateway IF → `application/port/gateway.go` (PaymentGateway)、
>   WebhookHandler → `application/port/webhook.go`、CustomerGateway → `application/port/customer_gateway.go`、
>   GatewayRouter IF → `application/port/gateway_router.go`、
>   DefaultGatewayRouter → `infrastructure/gateway/router.go` に移動
> - RawResponse を全レスポンス型から削除
> - domain/payment/ にはエンティティ・イベント・リポジトリIFのみ残す方針を明記
> 以下は改善の根拠を記録として残す。

### 問題

`domain/payment/` パッケージに以下のインフラ詳細が混在している:

- `Gateway` インターフェース: `ThreeDSecureRequest`（リダイレクトURL）、`RawResponse`（生レスポンスバイト列）
  - 注: `CardSource`（生カード番号）は非通過型設計への移行により削除済み
- `GatewayRouter`: ルーティングロジックと具象実装 (`DefaultGatewayRouter`)
- `WebhookHandler`: HTTP固有の概念（ヘッダー、ボディ、署名検証）
- `CustomerGateway`: 外部決済サービスの顧客管理

`docs/architecture.md` で「Domain層は外部依存なし」と明記しているが、実際にはHTTPヘッダーやカード番号のような外部インフラ概念がドメインに侵入している。

### 改善設計

#### ドメイン層に残すもの

`domain/payment/` には純粋なドメイン概念のみ残す:

```go
// domain/payment/entity.go
// Payment エンティティ（現状維持）
type Payment struct {
    id            PaymentID
    invoiceID     shared.InvoiceID
    amount        shared.Money
    method        PaymentMethod
    status        PaymentStatus
    // gatewayTransactionID は削除 → PaymentResult として外部から受け取る
    failureReason *string
    processedAt   time.Time
    metadata      map[string]string
}

// domain/payment/events.go
// ドメインイベント（現状維持）

// domain/payment/repository.go
// リポジトリインターフェース（現状維持）
```

#### アプリケーション層に移動するもの

`application/port/` を新設し、外部決済サービスとの統合インターフェースを配置する:

```go
// application/port/gateway.go
package port

// PaymentGateway 決済ゲートウェイインターフェース
// 旧 domain/payment/gateway.go から移動
type PaymentGateway interface {
    ID() string
    SupportedMethods() []PaymentMethodType
    Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error)
    Authorize(ctx context.Context, req *AuthorizeRequest) (*AuthorizeResponse, error)
    Capture(ctx context.Context, req *CaptureRequest) (*CaptureResponse, error)
    Void(ctx context.Context, req *VoidRequest) (*VoidResponse, error)
    Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)
    Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)
    GetTransaction(ctx context.Context, transactionID string) (*Transaction, error)
    RegisterPaymentMethod(ctx context.Context, req *RegisterPaymentMethodRequest) (*PaymentMethodDetail, error)
    DeletePaymentMethod(ctx context.Context, paymentMethodID string) error
    GetPaymentMethod(ctx context.Context, paymentMethodID string) (*PaymentMethodDetail, error)
    ListPaymentMethods(ctx context.Context, customerID string) ([]*PaymentMethodDetail, error)
}

// application/port/gateway_types.go
// ChargeRequest, ChargeResponse, ThreeDSecureRequest 等
// 全リクエスト/レスポンス型を移動

// application/port/webhook.go
// WebhookHandler, WebhookRequest, WebhookEvent 等を移動

// application/port/customer_gateway.go
// CustomerGateway, CreateCustomerRequest 等を移動

// application/port/gateway_router.go
// GatewayRouter インターフェースを移動
// DefaultGatewayRouter 実装は infrastructure/ に移動
```

#### 具象実装の移動

```go
// infrastructure/gateway/router.go
// DefaultGatewayRouter の実装を移動（旧 domain/payment/router.go の実装部分）
```

### 影響を受けるドキュメント

- `docs/design/payment-gateway.md`: パッケージパスの修正
- `docs/architecture.md`: パッケージ構成図の更新

---

## 改善2: プラグインシステムのフック分離

> **注**: 本改善は設計段階で先行適用済み。`docs/design/plugin-system.md` は
> 初版から DiscountHook / TaxHook / InvoiceLifecycleHook の分離設計を採用しており、
> InvoiceCalculationHook は存在しない。以下は改善の根拠を記録として残す。

### 問題

#### 2A: 全メソッド実装の強制

`InvoiceCalculationHook` が割引と税の両方を含むため、関係ないメソッドの空実装が必要:

```go
// クーポンプラグインは CalculateTax で常にゼロを返す
func (p *CouponPlugin) CalculateTax(ctx *Context, subtotal Money) (Money, error) {
    return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
}

// 税プラグインは CalculateDiscount で常にゼロを返す
func (p *TaxPlugin) CalculateDiscount(ctx *Context, subtotal Money) (Money, error) {
    return shared.NewMoney(big.NewRat(0, 1), subtotal.Currency()), nil
}
```

#### 2B: 実行順序の脆さ

割引と税の順序が Priority の整数値だけで制御されており、誤った順序でプラグインが登録されるリスクがある。

#### 2C: Plugin Context の型安全性不足

`metadata map[string]interface{}` でプラグイン間のデータ受け渡しを行っており、型安全でない。

### 改善設計

#### フックインターフェースの分離

```go
// plugin/hooks.go

// DiscountHook 割引計算フック
type DiscountHook interface {
    Plugin
    // CalculateDiscount 割引額を計算して返す
    CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}

// TaxHook 税計算フック
type TaxHook interface {
    Plugin
    // CalculateTax 税額を計算して返す
    CalculateTax(ctx *CalculationContext) (shared.Money, error)
}

// InvoiceLifecycleHook 請求書ライフサイクルフック（BeforeCalculation / AfterCalculation）
type InvoiceLifecycleHook interface {
    Plugin
    BeforeCalculation(ctx *CalculationContext) error
    AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}

// ContractLifecycleHook 契約ライフサイクルフック（現状維持）
// PaymentHook 支払いフック（現状維持）
// MetricsHook メトリクス収集フック（現状維持）
// InvoiceGenerationHook 請求書生成フック（現状維持）
```

#### コアが計算順序を保証

```go
// application/service/billing_service.go

func (s *BillingService) GenerateInvoice(ctx context.Context, contractID string) (*invoice.Invoice, error) {
    // ...

    calcCtx := plugin.NewCalculationContext(ctx, contract)

    // 1. BeforeCalculation（InvoiceLifecycleHook）
    for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
        if err := hook.BeforeCalculation(calcCtx); err != nil {
            return nil, err
        }
    }

    // 2. 基本料金計算
    subtotal := s.calculateBasePrice(contract)

    // 3. 割引計算（DiscountHook のみ）
    //    コアがこのステップで DiscountHook だけを呼ぶため、
    //    Priority による順序ミスが起きない
    var totalDiscount shared.Money
    for _, hook := range s.registry.GetDiscountHooks() {
        discount, err := hook.CalculateDiscount(calcCtx)
        if err != nil {
            return nil, err
        }
        totalDiscount, _ = totalDiscount.Add(discount)
    }

    // 4. 小計算出
    afterDiscount, _ := subtotal.Subtract(totalDiscount)

    // 5. 税計算（TaxHook のみ、割引後に対して）
    //    コアが割引後の金額で TaxHook を呼ぶため、
    //    会計基準の順序が構造的に保証される
    calcCtx.SetSubtotalAfterDiscount(afterDiscount)
    var totalTax shared.Money
    for _, hook := range s.registry.GetTaxHooks() {
        tax, err := hook.CalculateTax(calcCtx)
        if err != nil {
            return nil, err
        }
        totalTax, _ = totalTax.Add(tax)
    }

    // 6. 合計算出・請求書作成
    // ...

    // 7. AfterCalculation（InvoiceLifecycleHook）
    for _, hook := range s.registry.GetInvoiceLifecycleHooks() {
        if err := hook.AfterCalculation(calcCtx, inv); err != nil {
            return nil, err
        }
    }

    return inv, nil
}
```

#### 型安全な CalculationContext

```go
// plugin/context.go

// CalculationContext 請求計算コンテキスト
type CalculationContext struct {
    ctx                   context.Context
    contract              *contract.Contract
    invoice               *invoice.Invoice
    subtotal              shared.Money
    subtotalAfterDiscount shared.Money
    appliedDiscounts      []AppliedDiscount  // 型付き
}

// AppliedDiscount 適用された割引の記録
type AppliedDiscount struct {
    PluginName string
    Code       string       // クーポンコード等
    Amount     shared.Money
}

func (c *CalculationContext) Contract() *contract.Contract {
    return c.contract
}

func (c *CalculationContext) SubtotalAfterDiscount() shared.Money {
    return c.subtotalAfterDiscount
}

func (c *CalculationContext) RecordDiscount(d AppliedDiscount) {
    c.appliedDiscounts = append(c.appliedDiscounts, d)
}

func (c *CalculationContext) AppliedDiscounts() []AppliedDiscount {
    return c.appliedDiscounts
}
```

#### Registry の変更

```go
// plugin/registry.go

type Registry struct {
    mu      sync.RWMutex
    plugins map[string]Plugin

    // フック別（分離後）
    discountHooks          []DiscountHook
    taxHooks               []TaxHook
    invoiceLifecycleHooks  []InvoiceLifecycleHook
    contractLifecycleHooks []ContractLifecycleHook
    paymentHooks           []PaymentHook
    metricsHooks           []MetricsHook
    invoiceGenerationHooks []InvoiceGenerationHook
}

func (r *Registry) GetDiscountHooks() []DiscountHook { ... }
func (r *Registry) GetTaxHooks() []TaxHook { ... }
func (r *Registry) GetInvoiceLifecycleHooks() []InvoiceLifecycleHook { ... }
```

### 影響を受けるドキュメント

- `docs/design/plugin-system.md`: フック定義の全面改訂
- `docs/architecture.md`: プラグイン実行順序の説明更新

---

## 改善3: イベントソーシングの集約設計改善

> **注**: 本改善は設計段階で先行適用済み。
> - `docs/design/event-sourcing.md`: 型付きイベント（DomainEvent IF, EventType定数, EventRegistry）、
>   型スイッチによるApply、Clock IF注入（shared/clock.go）を適用済み
> - `docs/design/payment-gateway.md`: WebhookProcessor, PaymentService に clock IF を注入、
>   time.Now() を clock.Now() に置換済み
> - `docs/design/plugin-system.md`: CouponPlugin に clock IF を注入、テスト例を FixedClock に更新済み
> 以下は改善の根拠を記録として残す。

### 問題

#### 3A: 文字列ベースのイベントディスパッチ

```go
func (a *ContractAggregate) Apply(event eventstore.Event) error {
    switch event.Type {
    case "ContractCreated":    // 文字列リテラル → タイプミスがコンパイルで検出されない
    case "ContractActivated":
    // ...
    }
}
```

新しいイベント追加時に `Apply` の更新を忘れてもコンパイルエラーにならない。

#### 3B: テスト不可能な時刻生成

```go
func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata EventMetadata) error {
    event := ContractCreatedEvent{
        CreatedAt: time.Now().UTC(),  // テスト時に制御不能
    }
}
```

### 改善設計

#### 3A: 型付きイベントとイベントレジストリ

```go
// eventstore/event_registry.go

// EventType イベントタイプ定数
type EventType string

// DomainEvent 型付きドメインイベントインターフェース
type DomainEvent interface {
    EventType() EventType
}

// EventRegistry イベントの型情報を管理
type EventRegistry struct {
    types map[EventType]reflect.Type
}

func NewEventRegistry() *EventRegistry {
    return &EventRegistry{types: make(map[EventType]reflect.Type)}
}

// Register イベント型を登録
func (r *EventRegistry) Register(event DomainEvent) {
    r.types[event.EventType()] = reflect.TypeOf(event)
}

// Deserialize イベントデータから型付きイベントを復元
func (r *EventRegistry) Deserialize(eventType EventType, data json.RawMessage) (DomainEvent, error) {
    t, ok := r.types[eventType]
    if !ok {
        return nil, fmt.Errorf("unknown event type: %s", eventType)
    }
    event := reflect.New(t).Interface().(DomainEvent)
    if err := json.Unmarshal(data, event); err != nil {
        return nil, err
    }
    return event, nil
}
```

```go
// domain/contract/events.go

const (
    EventTypeContractCreated   eventstore.EventType = "contract.created"
    EventTypeContractActivated eventstore.EventType = "contract.activated"
    EventTypeContractSuspended eventstore.EventType = "contract.suspended"
    EventTypeContractResumed   eventstore.EventType = "contract.resumed"
    EventTypeContractCancelled eventstore.EventType = "contract.cancelled"
    EventTypePriceChanged      eventstore.EventType = "contract.price_changed"
    EventTypePlanChanged       eventstore.EventType = "contract.plan_changed"
    EventTypeTrialStarted      eventstore.EventType = "contract.trial_started"
    EventTypeTrialEnded        eventstore.EventType = "contract.trial_ended"
)

type ContractCreatedEvent struct {
    ContractID   string       `json:"contract_id"`
    AccountID    string       `json:"account_id"`
    PlanID       string       `json:"plan_id"`
    Price        Money        `json:"price"`
    BillingCycle BillingCycle `json:"billing_cycle"`
    CreatedAt    time.Time    `json:"created_at"`
}

func (e ContractCreatedEvent) EventType() eventstore.EventType {
    return EventTypeContractCreated
}

// 他のイベントも同様に EventType() を実装
```

```go
// domain/contract/aggregate.go

// Apply は型付きイベントを受け取る
func (a *ContractAggregate) Apply(event eventstore.DomainEvent) error {
    switch e := event.(type) {
    case *ContractCreatedEvent:
        a.accountID = e.AccountID
        a.planID = e.PlanID
        a.price = e.Price
        a.billingCycle = e.BillingCycle
        a.status = ContractStatusDraft
        a.createdAt = e.CreatedAt
        a.updatedAt = e.CreatedAt

    case *ContractActivatedEvent:
        a.status = ContractStatusActive
        a.updatedAt = e.ActivatedAt

    case *ContractSuspendedEvent:
        a.status = ContractStatusSuspended
        a.updatedAt = e.SuspendedAt

    // 型スイッチにより、新しいイベント型の追加忘れは
    // exhaustive lint ツールで検出可能
    default:
        return shared.NewDomainError(
            shared.ErrCodeUnknownEvent,
            fmt.Sprintf("unknown event type: %T", event),
        )
    }
    return nil
}
```

#### 3B: Clock インターフェース

```go
// domain/shared/clock.go

// Clock 時刻生成インターフェース
type Clock interface {
    Now() time.Time
}

// SystemClock 本番用（実時刻）
type SystemClock struct{}

func (c SystemClock) Now() time.Time {
    return time.Now().UTC()
}

// FixedClock テスト用（固定時刻）
type FixedClock struct {
    FixedTime time.Time
}

func (c FixedClock) Now() time.Time {
    return c.FixedTime
}
```

```go
// domain/contract/aggregate.go

type ContractAggregate struct {
    eventstore.BaseAggregate
    clock shared.Clock  // 注入

    // 状態フィールド...
}

func NewContractAggregate(id string, clock shared.Clock) *ContractAggregate {
    return &ContractAggregate{
        BaseAggregate: eventstore.BaseAggregate{id: id},
        clock:         clock,
    }
}

func (a *ContractAggregate) Create(cmd CreateContractCommand, metadata eventstore.EventMetadata) error {
    if a.status != "" {
        return shared.NewDomainError(shared.ErrCodeConflict, "contract already exists")
    }

    now := a.clock.Now()
    event := &ContractCreatedEvent{
        ContractID:   a.ID(),
        AccountID:    cmd.AccountID,
        PlanID:       cmd.PlanID,
        Price:        cmd.Price,
        BillingCycle: cmd.BillingCycle,
        CreatedAt:    now,
    }

    return a.RaiseEvent(event, metadata)
}
```

```go
// eventstore/aggregate_base.go

// RaiseEvent を型付きイベントに対応
func (a *BaseAggregate) RaiseEvent(domainEvent DomainEvent, metadata EventMetadata) error {
    jsonData, err := json.Marshal(domainEvent)
    if err != nil {
        return err
    }

    event := Event{
        ID:            GenerateID(),
        StreamID:      a.id,
        Type:          string(domainEvent.EventType()),
        Version:       a.version + len(a.uncommittedEvents) + 1,
        SchemaVersion: 1,
        Data:          jsonData,
        Metadata:      metadata,
        OccurredAt:    time.Now().UTC(), // RecordedAt と同等の記録時刻
    }

    a.uncommittedEvents = append(a.uncommittedEvents, event)
    return nil
}
```

### 影響を受けるドキュメント

- `docs/design/event-sourcing.md`: 集約ルート、イベント定義セクションの改訂
- `docs/design/domain-model.md`: 契約集約のコード例更新

---

## 改善4: パッケージ構成と循環依存の解消

> **注**: 本改善は設計段階で先行適用済み。
> - `domain-model.md`: 全ID型を `shared/identifier.go` に集約済み、循環依存解消済み
> - `contract/engine.go`（Engine IF）: 削除済み、`application/service/billing_service.go` に移行
> - importパス: 全ドキュメントで `github.com/contract-to-cash/core` に統一済み
> 以下は改善の根拠を記録として残す。

### 問題

#### 循環依存の発生箇所

```
contract/entity.go    → account.AccountID     (OK)
contract/engine.go    → invoice.Invoice        (問題: contract → invoice)
invoice/entity.go     → contract.ContractID    (問題: invoice → contract)
invoice/repository.go → contract.ContractID    (問題: invoice → contract)
```

`contract → invoice` かつ `invoice → contract` で循環依存が発生する。

#### plugin/context.go の密結合

```go
type Context struct {
    contract  *contract.Contract   // domain/contract への直接依存
    invoice   *invoice.Invoice     // domain/invoice への直接依存
}
```

### 改善設計

#### 共有ID型の集約

```go
// domain/shared/identifier.go

package shared

// 全ドメインで使用するID型を一箇所に集約
type AccountID string
type ContractID string
type InvoiceID string
type PaymentID string
type UsageRecordID string
type PlanID string

// ID生成ヘルパー
func NewAccountID() AccountID   { return AccountID(generateULID()) }
func NewContractID() ContractID { return ContractID(generateULID()) }
func NewInvoiceID() InvoiceID   { return InvoiceID(generateULID()) }
func NewPaymentID() PaymentID   { return PaymentID(generateULID()) }
```

これにより各ドメインパッケージは `shared` のみに依存し、互いを参照しない:

```go
// domain/contract/entity.go
type Contract struct {
    id        shared.ContractID   // 自パッケージの型ではなく shared を使う
    accountID shared.AccountID
    // invoice への参照なし
}

// domain/invoice/entity.go
type Invoice struct {
    id         shared.InvoiceID
    accountID  shared.AccountID
    contractID shared.ContractID  // shared の型を使うため contract パッケージへの依存なし
}
```

#### Engine の除去と billing ドメインサービスの導入

```go
// domain/billing/service.go
package billing

// Calculator 請求計算ドメインサービス
// contract と invoice を橋渡しする
type Calculator interface {
    // GenerateInvoice 契約から請求書を生成
    GenerateInvoice(ctx context.Context, contractID shared.ContractID) (*invoice.Invoice, error)

    // CalculateProration 日割り計算
    CalculateProration(ctx context.Context, contractID shared.ContractID, newPrice shared.Money) (*ProrationResult, error)
}

type ProrationResult struct {
    CreditAmount  shared.Money
    ChargeAmount  shared.Money
    EffectiveDate time.Time
}
```

`domain/contract/engine.go` は削除する。`SubscriptionEngine`, `UsageBasedEngine` のロジックは `application/service/` に移動し、`billing.Calculator` を実装する。

#### 改善後の依存グラフ

```
shared（ID型、Money、Clock、Errors）
  ↑
  ├── account（shared のみ依存）
  ├── contract（shared のみ依存）
  ├── invoice（shared のみ依存）
  ├── payment（shared のみ依存）
  ├── usage（shared のみ依存）
  ├── pricing（shared のみ依存）
  └── billing（shared + contract + invoice に依存 ← ドメインサービスなので許容）
```

循環は発生しない。`billing` だけが複数ドメインを参照するが、これはドメインサービスの役割として適切。

#### 最終パッケージ構成

```
github.com/contract-to-cash/core/
├── domain/
│   ├── shared/
│   │   ├── money.go             # Money 値オブジェクト
│   │   ├── datetime.go          # DateRange 値オブジェクト
│   │   ├── identifier.go        # 全ID型（AccountID, ContractID, ...）
│   │   ├── errors.go            # ドメインエラー型（改善5）
│   │   └── clock.go             # Clock インターフェース（改善3B）
│   ├── account/
│   │   └── entity.go
│   ├── contract/
│   │   ├── entity.go            # Contract エンティティ（Engine 除去）
│   │   ├── aggregate.go         # ContractAggregate（型付きイベント対応）
│   │   ├── events.go            # 型付きイベント定義（定数 + DomainEvent 実装）
│   │   ├── repository.go
│   │   ├── trial.go
│   │   ├── suspension.go
│   │   └── proration.go
│   ├── invoice/
│   │   ├── entity.go            # shared.ContractID を使用（contract パッケージ非依存）
│   │   └── repository.go
│   ├── payment/
│   │   ├── entity.go            # ドメイン概念のみ（Gateway 等は除去）
│   │   ├── events.go
│   │   ├── repository.go
│   │   └── dunning.go
│   ├── usage/
│   │   ├── entity.go
│   │   └── repository.go
│   ├── pricing/
│   │   └── model.go
│   └── billing/
│       └── service.go           # ドメインサービス（contract ↔ invoice の橋渡し）
│
├── application/
│   ├── command/                  # コマンドハンドラ
│   ├── query/                   # クエリハンドラ（TemporalQueryService 等）
│   ├── service/                 # アプリケーションサービス
│   │   ├── billing_service.go   # 請求サービス（プラグイン統合）
│   │   ├── payment_service.go   # 決済サービス
│   │   └── snapshot_service.go  # スナップショットサービス
│   └── port/                    # 外部サービスとの統合ポート
│       ├── gateway.go           # PaymentGateway IF
│       ├── gateway_types.go     # リクエスト/レスポンス型
│       ├── webhook.go           # WebhookHandler IF
│       ├── customer_gateway.go  # CustomerGateway IF
│       └── gateway_router.go    # GatewayRouter IF
│
├── eventstore/
│   ├── store.go                 # Store インターフェース
│   ├── event.go                 # Event 構造体
│   ├── event_registry.go        # EventRegistry（型付きイベント管理）
│   ├── aggregate.go             # AggregateRoot IF, Evolver IF
│   ├── aggregate_base.go        # BaseAggregate 実装
│   ├── snapshot.go              # Snapshot 構造体
│   └── upcaster.go              # Upcaster IF
│
├── plugin/
│   ├── plugin.go                # Plugin 基本 IF
│   ├── registry.go              # Registry（フック分離後）
│   ├── hooks.go                 # DiscountHook, TaxHook, InvoiceLifecycleHook, ...
│   ├── context.go               # CalculationContext（型安全）
│   │
│   ├── metrics/                 # メトリクス Adapter
│   │   ├── types.go
│   │   ├── adapter.go
│   │   ├── hook.go
│   │   └── query_service.go
│   │
│   └── invoicegen/              # インボイス発行 Adapter
│       ├── types.go
│       ├── adapter.go
│       ├── hook.go
│       └── service.go
│
├── batch/
│   └── processor.go             # BatchProcessor IF
│
├── infrastructure/
│   ├── gateway/
│   │   ├── stripe/              # Stripe 実装
│   │   ├── mock/                # テスト用モック
│   │   └── router.go            # DefaultGatewayRouter 実装
│   ├── postgres/
│   ├── mysql/
│   └── inmemory/
│
├── plugins/                     # 公式プラグイン
│   ├── coupon/                  # クーポン（DiscountHook 実装）
│   ├── tax/                     # 税計算（TaxHook 実装）
│   ├── metrics-basic/
│   └── invoicegen-pdf/
│
└── api/
    ├── http/
    └── grpc/
```

### 影響を受けるドキュメント

- `docs/architecture.md`: パッケージ構成の全面改訂
- `docs/design/domain-model.md`: 全エンティティのID型変更、Engine 除去
- `docs/design/payment-gateway.md`: パッケージパスの変更

---

## 改善5: ドメインエラーの構造化

### 問題

ドメイン全体で `errors.New("文字列")` が使われており:

- 呼び出し側がエラーの種類を判定できない（文字列比較が必要）
- ドメインルール違反と技術的エラーの区別がつかない
- API層でHTTPステータスコードを適切に返せない

### 改善設計

#### ドメインエラー型

```go
// domain/shared/errors.go
package shared

import "fmt"

// ErrorCode ドメインエラーコード
type ErrorCode string

const (
    // 状態遷移エラー
    ErrCodeInvalidStateTransition ErrorCode = "invalid_state_transition"

    // バリデーションエラー
    ErrCodeValidation       ErrorCode = "validation_error"
    ErrCodeCurrencyMismatch ErrorCode = "currency_mismatch"
    ErrCodeInvalidDateRange ErrorCode = "invalid_date_range"

    // 存在系エラー
    ErrCodeNotFound ErrorCode = "not_found"
    ErrCodeConflict ErrorCode = "conflict"

    // 冪等性エラー
    ErrCodeDuplicateRequest ErrorCode = "duplicate_request"

    // イベント系エラー
    ErrCodeUnknownEvent  ErrorCode = "unknown_event"
    ErrCodeVersionConflict ErrorCode = "version_conflict"

    // ビジネスルール違反
    ErrCodeBusinessRule ErrorCode = "business_rule_violation"
)

// DomainError ドメインエラー
type DomainError struct {
    Code    ErrorCode
    Message string
    Cause   error
}

func (e *DomainError) Error() string {
    if e.Cause != nil {
        return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
    }
    return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *DomainError) Unwrap() error {
    return e.Cause
}

// NewDomainError ドメインエラー生成
func NewDomainError(code ErrorCode, message string) *DomainError {
    return &DomainError{Code: code, Message: message}
}

// WrapDomainError 原因を含むドメインエラー生成
func WrapDomainError(code ErrorCode, message string, cause error) *DomainError {
    return &DomainError{Code: code, Message: message, Cause: cause}
}

// IsErrorCode エラーコードの判定ヘルパー
func IsErrorCode(err error, code ErrorCode) bool {
    var domainErr *DomainError
    if errors.As(err, &domainErr) {
        return domainErr.Code == code
    }
    return false
}
```

#### ドメインでの使用例

```go
// domain/shared/money.go
func (m Money) Add(other Money) (Money, error) {
    if m.currency != other.currency {
        return Money{}, NewDomainError(
            ErrCodeCurrencyMismatch,
            fmt.Sprintf("cannot add %s to %s", other.currency, m.currency),
        )
    }
    result := new(big.Rat).Add(m.amount, other.amount)
    return NewMoney(result, m.currency), nil
}

// domain/shared/datetime.go
func NewDateRange(start, end time.Time) (DateRange, error) {
    if end.Before(start) {
        return DateRange{}, NewDomainError(
            ErrCodeInvalidDateRange,
            "end must be after start",
        )
    }
    return DateRange{start: start.UTC(), end: end.UTC()}, nil
}

// domain/contract/aggregate.go
func (a *ContractAggregate) Activate(metadata eventstore.EventMetadata) error {
    if a.status != ContractStatusDraft && a.status != ContractStatusTrialing {
        return NewDomainError(
            ErrCodeInvalidStateTransition,
            fmt.Sprintf("cannot activate contract in status %s", a.status),
        )
    }
    // ...
}
```

#### API層でのマッピング

```go
// api/http/error_handler.go

func mapDomainErrorToHTTP(err error) (int, interface{}) {
    var domainErr *shared.DomainError
    if !errors.As(err, &domainErr) {
        return http.StatusInternalServerError, ErrorResponse{
            Code:    "internal_error",
            Message: "an unexpected error occurred",
        }
    }

    status := mapErrorCodeToHTTPStatus(domainErr.Code)
    return status, ErrorResponse{
        Code:    string(domainErr.Code),
        Message: domainErr.Message,
    }
}

func mapErrorCodeToHTTPStatus(code shared.ErrorCode) int {
    switch code {
    case shared.ErrCodeNotFound:
        return http.StatusNotFound
    case shared.ErrCodeConflict, shared.ErrCodeDuplicateRequest:
        return http.StatusConflict
    case shared.ErrCodeValidation, shared.ErrCodeCurrencyMismatch,
         shared.ErrCodeInvalidDateRange, shared.ErrCodeInvalidStateTransition:
        return http.StatusUnprocessableEntity
    case shared.ErrCodeVersionConflict:
        return http.StatusConflict
    case shared.ErrCodeBusinessRule:
        return http.StatusUnprocessableEntity
    default:
        return http.StatusInternalServerError
    }
}
```

### 影響を受けるドキュメント

- `docs/design/domain-model.md`: 全エラー箇所の更新
- `docs/design/event-sourcing.md`: 集約のエラー処理更新
- `docs/architecture.md`: エラーハンドリング戦略の追記

---

## 既存ドキュメントへの影響まとめ

| ドキュメント | 影響する改善 | 変更内容 |
|-------------|------------|---------|
| `docs/architecture.md` | 1, 2, 4, 5 | パッケージ構成図の全面改訂、エラーハンドリング戦略追記 |
| `docs/design/domain-model.md` | 3, 4, 5 | ID型の変更、Engine除去、型付きイベント、エラー型変更 |
| `docs/design/event-sourcing.md` | 3, 5 | 集約ルート設計改訂、EventRegistry追加、Clock導入 |
| `docs/design/plugin-system.md` | 2 | フック分離（DiscountHook / TaxHook）、Context型安全化 |
| `docs/design/payment-gateway.md` | 1, 4 | パッケージパスを `application/port/` に変更 |
| `docs/design/metrics-invoicegen.md` | 2 | フック名の変更に合わせた更新 |
| `docs/decisions/design-decisions.md` | 全て | 本改善を新しいADRとして追記 |

---

## 実装優先順序

改善間に依存関係があるため、以下の順序で実装する:

1. **改善4: パッケージ構成と循環依存の解消** — 全ての基盤
2. **改善5: ドメインエラーの構造化** — 他の改善で使用
3. **改善1: ドメイン/インフラ境界修正** — 改善4のパッケージ構成に基づく
4. **改善3: イベントソーシング改善** — Clock、型付きイベント導入
5. **改善2: プラグインフック分離** — 最後に適用（既存プラグインの書き換えが必要）
