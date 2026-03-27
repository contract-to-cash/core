---
sidebar_position: 4
---

# 決済ゲートウェイ

Contract Billing Coreは、決済処理を抽象化する`PaymentGateway`インターフェースを定義しています。お使いの決済プロバイダー（Stripe、Braintree、PayPayなど）向けにこのインターフェースを実装します。

## インターフェース

```go
type PaymentGateway interface {
    ID() string
    SupportedMethods() []PaymentMethodType

    // 直接チャージ
    Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error)

    // 2フェーズ: オーソリ → キャプチャ
    Authorize(ctx context.Context, req *AuthorizeRequest) (*AuthorizeResponse, error)
    Capture(ctx context.Context, req *CaptureRequest) (*CaptureResponse, error)
    Void(ctx context.Context, req *VoidRequest) (*VoidResponse, error)

    // 返金
    Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)
    Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)

    // トランザクションクエリ
    GetTransaction(ctx context.Context, transactionID string) (*Transaction, error)

    // 決済手段管理
    RegisterPaymentMethod(ctx context.Context, req *RegisterPaymentMethodRequest) (*PaymentMethodDetail, error)
    DeletePaymentMethod(ctx context.Context, paymentMethodID string) error
    GetPaymentMethod(ctx context.Context, paymentMethodID string) (*PaymentMethodDetail, error)
    ListPaymentMethods(ctx context.Context, customerID string) ([]*PaymentMethodDetail, error)
}
```

## 決済手段タイプ

```go
const (
    PaymentMethodTypeCreditCard       = "credit_card"       // クレジットカード
    PaymentMethodTypeDebitCard        = "debit_card"        // デビットカード
    PaymentMethodTypeBankTransfer     = "bank_transfer"     // 銀行振込
    PaymentMethodTypeConvenienceStore = "convenience_store" // コンビニ決済
    PaymentMethodTypeQRCode           = "qr_code"          // QRコード決済
    PaymentMethodTypeCarrier          = "carrier"           // キャリア決済
    PaymentMethodTypePostpay          = "postpay"           // 後払い
    PaymentMethodTypeDirectDebit      = "direct_debit"      // 口座振替
)
```

## チャージフロー

最もシンプルな決済フロー — 即時チャージ：

```go
pmID := "pm-visa-1234"
resp, err := gateway.Charge(ctx, &port.ChargeRequest{
    CustomerID:      "cust-001",
    Amount:          invoiceTotal,
    PaymentMethodID: &pmID,
    IdempotencyKey:  "charge-inv-001",
})
```

## オーソリ/キャプチャフロー

サービス提供前に決済を確認する必要がある場合（決済ゲート付きプロビジョニング）：

```go
// 1. オーソリ（資金を確保）
pmID := "pm-visa-1234"
authResp, _ := gateway.Authorize(ctx, &port.AuthorizeRequest{
    CustomerID:      "cust-001",
    Amount:          invoiceTotal,
    PaymentMethodID: &pmID,
    IdempotencyKey:  "auth-inv-001",
})

// 2. サービスプロビジョニング...

// 3. キャプチャ（チャージを確定）
captureResp, _ := gateway.Capture(ctx, &port.CaptureRequest{
    AuthorizationID: authResp.AuthorizationID,
    Amount:          &invoiceTotal, // 一部キャプチャも可能
})
```

## 決済ゲート付きプロビジョニング

ホスティング/クラウドプロバイダーで一般的なパターン。決済完了がサービス有効化の条件：

```
1. Contract: Draft     → 契約作成
2. Contract: Active    → 有効化
3. Contract: Suspended → 即時停止（決済待ち）
4. Invoice: Finalized  → 請求書の生成・確定
5. Payment: Completed  → 決済処理
6. Contract: Active    → 再開 → サービスプロビジョニング
```

`Suspended`状態を以下の両方に再利用：
- **初回有効化**: 初回決済を待つ新規契約
- **未払い停止**: 支払い遅延の既存契約（ダニング）

両方とも同じ方法で解決: 決済 → 再開。

`OnContractResumeHook`でサービスプロビジョニングをトリガー：

:::note
`AfterChargeHook`が受け取る`PaymentContext`では、`Contract()`はデフォルトで`nil`を返します。契約情報にアクセスするには、`Invoice.ContractID()`経由で自分で契約をルックアップする必要があります。そのため、決済ゲート付きプロビジョニングでは、フックではなく`ProcessPayment`成功後にアプリケーションコードでResumeを処理する方法が推奨されます（[決済統合ガイド](../guides/payment-integration)参照）。
:::

```go
func (p *ProvisioningPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    return p.provisioningService.Activate(ctx.Context(), c.ContractID())
}
```

:::caution 既知の制約
`OnContractResumeHook`は**初回有効化**（初回決済完了）と**再有効化**（停止後の決済完了）を区別できません。これは集約の`Apply()`メソッドで`SuspensionConfiguration`がフック発火前に`nil`にクリアされるためです。

**回避策:**
- プロビジョニング状態を外部で追跡（例: データベースに「プロビジョニング済み」フラグ）
- `plugin.Context`のメタデータを使って停止理由をオーケストレーションコードから渡す
- プロビジョニング前にサービスが既に存在するか確認する

詳細は[Issue #5](https://github.com/contract-to-cash/core/issues/5)を参照。
:::

## 決済手段のフォールバック解決

決済手段は階層的に解決されます（Stripeスタイル）：

```
1. ProcessPaymentInput内の明示的PaymentMethodID
2. Invoice.PaymentMethodID
3. Contract.PaymentMethodID
4. Customer.DefaultPaymentMethodID
```

:::note
ContractおよびCustomerレベルのフォールバックには、`NewPaymentService`に`contractRepo`を渡す必要があります。`contractRepo`が`nil`の場合、解決はInvoiceレベルで止まります。
:::

より具体的なレベルがより一般的なレベルをオーバーライドします。

## ゲートウェイの実装

```go
type StripeGateway struct {
    client *stripe.Client
}

func (g *StripeGateway) ID() string { return "stripe" }

func (g *StripeGateway) SupportedMethods() []port.PaymentMethodType {
    return []port.PaymentMethodType{
        port.PaymentMethodTypeCreditCard,
        port.PaymentMethodTypeDebitCard,
    }
}

func (g *StripeGateway) Charge(ctx context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
    // Stripe APIにマッピング
    // ...
}
```
