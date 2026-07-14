---
sidebar_position: 3
---

# プラグインフックリファレンス

## 基本Pluginインターフェース

```go
type Plugin interface {
    Name() string
    Version() string
    Initialize(ctx context.Context, config Config) error
    Shutdown(ctx context.Context) error
    Priority() int
}

type Config map[string]interface{}
```

### 優先度定数

| 定数 | 値 | ユースケース |
|-----|-----|-----------|
| `PriorityHighest` | 0 | 監査ログ、バリデーション |
| `PriorityHigh` | 100 | コアビジネスロジック |
| `PriorityNormal` | 500 | デフォルトプラグイン |
| `PriorityLow` | 900 | 後処理（税） |
| `PriorityLowest` | 1000 | 最終手段 |

---

## 課金計算フック

### DiscountHook

```go
type DiscountHook interface {
    Plugin
    CalculateDiscount(ctx *CalculationContext) (shared.Money, error)
}
```

請求書生成のステップ4で呼び出し。複数のDiscountHookが優先度順に呼び出される。

### TaxHook

```go
type TaxHook interface {
    Plugin
    CalculateTax(ctx *CalculationContext) (shared.Money, error)
}
```

ステップ7で呼び出し。`ctx.SubtotalAfterDiscount()`を使って計算。

### InvoiceLifecycleHook

```go
type InvoiceLifecycleHook interface {
    Plugin
    BeforeCalculation(ctx *CalculationContext) error
    AfterCalculation(ctx *CalculationContext, invoice *invoice.Invoice) error
}
```

---

## CalculationContext

```go
type CalculationContext struct { ... }

ctx.Context() context.Context
ctx.Contract() *contract.ContractAggregate
ctx.ContractID() shared.ContractID
ctx.Subtotal() shared.Money
ctx.SubtotalAfterDiscount() shared.Money
ctx.AppliedDiscounts() []AppliedDiscount
ctx.Invoice() *invoice.Invoice

ctx.SetSubtotal(s shared.Money)
ctx.SetSubtotalAfterDiscount(s shared.Money)
ctx.RecordDiscount(d AppliedDiscount)
ctx.SetInvoice(inv *invoice.Invoice)
```

### AppliedDiscount

```go
type AppliedDiscount struct {
    PluginName string       // プラグイン名
    Code       string       // クーポンコード、プロモ名など
    Amount     shared.Money // 割引額
}
```

---

## 契約ライフサイクルフック

すべて `*plugin.Context` と契約集約を受け取ります。

```go
type OnContractCreateHook interface {
    Plugin
    OnContractCreate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractActivateHook interface {
    Plugin
    OnContractActivate(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractSuspendHook interface {
    Plugin
    OnContractSuspend(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractResumeHook interface {
    Plugin
    OnContractResume(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractCancelHook interface {
    Plugin
    OnContractCancel(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractCancelScheduledHook interface {
    Plugin
    OnContractCancelScheduled(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractCancelUnscheduledHook interface {
    Plugin
    OnContractCancelUnscheduled(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractRenewHook interface {
    Plugin
    OnContractRenew(ctx *Context, contract *contract.ContractAggregate) error
}

type OnContractTrialEndHook interface {
    Plugin
    OnContractTrialEnd(ctx *Context, contract *contract.ContractAggregate, converted bool) error
}
```

### plugin.Context

```go
type Context struct { ... }

ctx.Context() context.Context
ctx.SetMetadata(key string, value interface{})
ctx.GetMetadata(key string) (interface{}, bool)
```

---

## 決済フック

```go
type BeforeChargeHook interface {
    Plugin
    BeforeCharge(ctx *PaymentContext, amount shared.Money) error
}

type AfterChargeHook interface {
    Plugin
    AfterCharge(ctx *PaymentContext) error
}

type OnPaymentFailedHook interface {
    Plugin
    OnPaymentFailed(ctx *PaymentContext, err error) error
}

type OnRefundHook interface {
    Plugin
    OnRefund(ctx *PaymentContext, refundAmount shared.Money) error
}

type CompensationMethod string
// void, refund, none

type CompensationReason string
// local_save_failed, outbox_veto

type CompensationResult struct {
    TransactionID      string             // 元課金の gateway transaction ID
    Amount             shared.Money       // 元課金額
    Method             CompensationMethod // 実際に効いた取消手段（none = 両方失敗）
    Reason             CompensationReason // 補償が走った理由
    CompensationErr    error              // 非 nil = Void も Refund も失敗（人手リコンサイルが必要）
    MarkCompensatedErr error              // 冪等キーのマーカー書き込み失敗（issue #87）
}

type OnCompensationExecutedHook interface {
    Plugin
    OnCompensationExecuted(ctx *PaymentContext, result CompensationResult) error
}
```

`OnCompensationExecutedHook`（issue #257）は、`PaymentService.ProcessPayment` が
成功したゲートウェイ課金のサガ補償（課金成功 → ローカルトランザクション失敗）を
試行した後に非致命で発火します。**両方の結果**で発火します — Void または Refund
フォールバックで課金が取り消された場合と、双方失敗の人手リコンサイル状態の
両方です。これにより統合者はログをスクレイプせずに課金取消をアラートできます。
フックの失敗はログされますが、`ProcessPayment` の結果は変わりません。

### PaymentContext

```go
type PaymentContext struct { ... }

ctx.Context() context.Context
ctx.Payment() *payment.Payment
ctx.Invoice() *invoice.Invoice
ctx.Contract() *contract.ContractAggregate
ctx.ContractID() shared.ContractID
ctx.AccountID() shared.AccountID
ctx.SetContract(c *contract.ContractAggregate)
```

---

## メトリクスフック

```go
type ContractChangeType string
// created, activated, suspended, resumed, cancelled, renewed, trial_end, expired

type ContractChangeEvent struct {
    ContractID shared.ContractID
    ChangeType ContractChangeType
    OldStatus  *contract.ContractStatus
    NewStatus  *contract.ContractStatus
    OldPriceID *shared.PriceID
    NewPriceID *shared.PriceID
    MRRChange  *shared.Money
    Timestamp  time.Time
}

type OnContractChangeHook interface {
    Plugin
    OnContractChange(ctx *Context, event ContractChangeEvent) error
}

type OnInvoiceIssuedHook interface {
    Plugin
    OnInvoiceIssued(ctx *Context, invoice *invoice.Invoice) error
}

type OnPaymentProcessedHook interface {
    Plugin
    OnPaymentProcessed(ctx *PaymentContext) error
}
```

`OnPaymentProcessedHook` は支払いフックと同じ `*PaymentContext` を受け取ります
（issue #223）。`ctx.Payment()` が処理済みの支払い、`ctx.Invoice()` /
`ctx.ContractID()` / `ctx.AccountID()` で追加の参照なしに支払いを契約・
アカウントへ帰属できます。

---

## 請求書生成フック

```go
type InvoiceDocument struct {
    InvoiceID     string
    InvoiceNumber string
}

type DeliveryResult struct {
    DeliveryID string
    Status     string
    SentAt     *time.Time
    Error      *string
}

type InvoiceGenerationHook interface {
    Plugin
    BuildDocument(ctx *Context, invoice *invoice.Invoice, doc *InvoiceDocument) error
    AfterRender(ctx *Context, doc *InvoiceDocument, rendered []byte) error
    AfterDelivery(ctx *Context, doc *InvoiceDocument, result *DeliveryResult) error
}
```

---

## クレジットノートフック

```go
type OnCreditNoteIssuedHook interface {
    Plugin
    OnCreditNoteIssued(ctx *Context, creditNote *invoice.CreditNote) error
}

type OnInvoiceRevisedHook interface {
    Plugin
    OnInvoiceRevised(ctx *Context, original *invoice.Invoice, replacement *invoice.Invoice) error
}
```

`OnCreditNoteIssuedHook` はクレジットノートが発行された際に呼び出されます。`OnInvoiceRevisedHook` は請求書が無効化され代替請求書が作成された際に呼び出されます。

---

## プラグインレジストリ

```go
registry := plugin.NewRegistry()

registry.Register(p Plugin) error
registry.InitializeAll(ctx context.Context, configs map[string]Config) error
registry.ShutdownAll(ctx context.Context) error

// ゲッター（優先度順にソートされたコピーを返却）
registry.GetDiscountHooks() []DiscountHook
registry.GetTaxHooks() []TaxHook
registry.GetInvoiceLifecycleHooks() []InvoiceLifecycleHook
registry.GetOnContractCreateHooks() []OnContractCreateHook
registry.GetOnContractActivateHooks() []OnContractActivateHook
registry.GetOnContractSuspendHooks() []OnContractSuspendHook
registry.GetOnContractResumeHooks() []OnContractResumeHook
registry.GetOnContractCancelHooks() []OnContractCancelHook
registry.GetOnContractCancelScheduledHooks() []OnContractCancelScheduledHook
registry.GetOnContractCancelUnscheduledHooks() []OnContractCancelUnscheduledHook
registry.GetOnContractRenewHooks() []OnContractRenewHook
registry.GetOnContractTrialEndHooks() []OnContractTrialEndHook
registry.GetBeforeChargeHooks() []BeforeChargeHook
registry.GetAfterChargeHooks() []AfterChargeHook
registry.GetOnPaymentFailedHooks() []OnPaymentFailedHook
registry.GetOnRefundHooks() []OnRefundHook
registry.GetOnCompensationExecutedHooks() []OnCompensationExecutedHook
registry.GetOnContractChangeHooks() []OnContractChangeHook
registry.GetOnInvoiceIssuedHooks() []OnInvoiceIssuedHook
registry.GetOnPaymentProcessedHooks() []OnPaymentProcessedHook
registry.GetInvoiceGenerationHooks() []InvoiceGenerationHook
registry.GetOnCreditNoteIssuedHooks() []OnCreditNoteIssuedHook
registry.GetOnInvoiceRevisedHooks() []OnInvoiceRevisedHook
```
