---
sidebar_position: 6
---

# 決済統合

実際の決済ゲートウェイの統合と決済ゲート付きプロビジョニングの実装について解説します。完全な`PaymentGateway`インターフェース定義は[サービスAPIリファレンス](../api/services#paymentservice)を参照してください。

## PaymentGatewayの実装

`PaymentGateway`インターフェースは直接チャージとオーソリ/キャプチャの両方をサポート：

```go
type MyStripeGateway struct {
    apiKey string
}

func (g *MyStripeGateway) ID() string { return "stripe" }

func (g *MyStripeGateway) SupportedMethods() []port.PaymentMethodType {
    return []port.PaymentMethodType{
        port.PaymentMethodTypeCreditCard,
        port.PaymentMethodTypeDebitCard,
    }
}

func (g *MyStripeGateway) Charge(ctx context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
    // Stripe APIにマッピング
    pi, err := g.client.PaymentIntents.New(&stripe.PaymentIntentParams{
        Amount:        stripe.Int64(req.Amount.Amount().Num().Int64()),
        Currency:      stripe.String(string(req.Currency)),
        PaymentMethod: stripe.String(req.PaymentMethodID),
        Confirm:       stripe.Bool(true),
    })
    // レスポンスをマッピング...
}
```

対応する決済手段タイプ: `credit_card`, `debit_card`, `bank_transfer`, `convenience_store`, `qr_code`, `carrier`, `postpay`, `direct_debit`

## 決済処理フロー

```go
paymentService := service.NewPaymentService(
    gateway, paymentRepo, invoiceRepo, contractRepo,
    eventStore, registry, clock,
)

// 請求書に対する決済処理
payment, err := paymentService.ProcessPayment(ctx, invoiceID, service.ProcessPaymentInput{
    PaymentMethodID: "pm-visa-1234",      // 契約にデフォルトがあればオプション
    Amount:          invoice.AmountDue(),
    Currency:        shared.CurrencyJPY,
    IdempotencyKey:  "pay-" + invoiceID,  // 重複課金を防止
})
```

処理フロー：
1. **BeforeChargeHook** — バリデーション、コンテキスト補完
2. **Gateway.Charge** — 外部決済プロバイダー呼び出し
3. **AfterChargeHook**（成功時）または **OnPaymentFailedHook**（失敗時）
4. 請求書ステータスの更新

### オーソリ/キャプチャフロー

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

決済完了後にのみアクセスを許可するサービス（ホスティング、クラウドリソースなど）向け。契約の`Suspended`状態を統一的な「サービス非稼働」状態として使用：

| ステップ | Contract | Invoice | 説明 |
|---------|----------|---------|------|
| 1 | Draft | — | 契約作成 |
| 2 | Draft | Draft | 請求書生成（ステータスガードでDraft許可） |
| 3 | Active | Finalized | ユーザー確認後、両方を確定 |
| 4 | Suspended | Finalized | 即座にSuspend（決済待ち） |
| 5 | Active | Paid | 決済確認 → Resume → サービス開始 |

```go
// 1. 契約作成（ドラフト）
agg.Create(cmd, metadata)

// 2. ドラフト状態で請求書を生成
inv, _ := billingService.GenerateInvoice(ctx, contractID, period)

// 3. 契約を有効化し、即座に停止
agg.Activate(metadata)
agg.Suspend(contract.SuspensionConfiguration{
    BillingBehavior: contract.SuspensionBillingSkip,
    Reason:          "初回決済待ち",
}, metadata)
contractRepo.Save(ctx, agg)

// 4. 請求書を確定し決済処理
inv.Finalize()
invoiceRepo.Save(ctx, inv)
payment, _ := paymentService.ProcessPayment(ctx, inv.ID(), input)

// 5. 決済成功時に再開（プロビジョニングフックがトリガー）
if payment.Status() == "completed" {
    agg, _ = contractRepo.FindByID(ctx, contractID)
    agg.Resume(metadata)
    contractRepo.Save(ctx, agg) // OnContractResumeHookが発火 → サービスプロビジョニング
}
```

このパターンは`Suspended`状態を初回有効化（初回決済待ち）と未払い停止の両方に再利用します。両方とも同じ方法で解決: 決済完了 → 再開 → サービス有効化。

> **注意:** これは推奨パターンであり、必須ではありません。`Draft → Activate → 請求書生成 → 決済処理`のシンプルなフローも選択可能です。

:::caution
このパターンでは、**新規サービスのプロビジョニング**か**既存サービスの再有効化**かをアプリケーションコードで判定する必要があります。`OnContractResumeHook`は停止理由がフック発火前にクリアされるため、これらのケースを区別できません。

一般的なアプローチとして、プロビジョニング状態を別途追跡します:

```go
if !provisioningStore.IsProvisioned(contractID) {
    provisioningService.CreateServer(ctx, contractID)
    provisioningStore.MarkProvisioned(contractID)
} else {
    provisioningService.StartServer(ctx, contractID)
}
```

詳細は[Issue #5](https://github.com/contract-to-cash/core/issues/5)を参照。
:::

## 冪等性

重複課金を防ぐため、常に`IdempotencyKey`を提供：

```go
service.ProcessPaymentInput{
    IdempotencyKey: fmt.Sprintf("pay-%s-%d", invoiceID, attempt),
}
```

## Webhookの処理

決済プロバイダーは非同期通知を送信します。決済ステータスを更新して処理：

```go
func handleWebhook(ctx context.Context, event WebhookEvent) error {
    switch event.Type {
    case "payment_intent.succeeded":
        payment, _ := paymentRepo.FindByGatewayTransactionID(ctx, event.TransactionID)
        payment.Complete()
        paymentRepo.Save(ctx, payment)

    case "payment_intent.payment_failed":
        payment, _ := paymentRepo.FindByGatewayTransactionID(ctx, event.TransactionID)
        payment.Fail(event.FailureMessage)
        paymentRepo.Save(ctx, payment)
    }
    return nil
}
```

## 部分払いと返金

```go
// 部分返金
gateway.Refund(ctx, &port.RefundRequest{
    TransactionID: payment.GatewayTransactionID(),
    Amount:        partialAmount, // 元の請求額より少ない
})

// 返金ステータスを追跡
payment.MarkPartiallyRefunded(partialAmount)
paymentRepo.Save(ctx, payment)
```
