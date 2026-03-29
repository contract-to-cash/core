---
sidebar_position: 4
---

# 決済統合ガイド

実際の決済ゲートウェイの統合と決済ゲート付きプロビジョニングの実装について解説します。

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
    // 1. PaymentIntentを作成
    // 2. 決済手段で確認
    // 3. レスポンスをChargeResponseにマッピング
    return &port.ChargeResponse{
        TransactionID: stripePaymentIntent.ID,
        Status:        mapStripeStatus(stripePaymentIntent.Status),
        Amount:        req.Amount,
        CreatedAt:     time.Now(),
    }, nil
}
```

## 決済処理フロー

```go
paymentService := service.NewPaymentService(
    gateway, paymentRepo, invoiceRepo, contractRepo,
    eventStore, registry, clock,
)

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

## 決済ゲート付きプロビジョニング

決済完了後にのみアクセスを許可するサービス（ホスティング、クラウドリソースなど）向け：

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

このパターンは`Suspended`状態を以下の両方に再利用：
- **初回有効化**: 初回決済を待つ新規契約
- **未払い停止**: 支払い遅延の既存契約

両方とも同じ方法で解決: 決済完了 → 再開 → サービス有効化。

:::caution
このパターンでは、**新規サービスのプロビジョニング**か**既存サービスの再有効化**かをアプリケーションコードで判定する必要があります。`OnContractResumeHook`は停止理由がフック発火前にクリアされるため、これらのケースを区別できません。

一般的なアプローチとして、プロビジョニング状態を別途追跡します:

```go
// 決済成功とResume後
if !provisioningStore.IsProvisioned(contractID) {
    provisioningService.CreateServer(ctx, contractID)
    provisioningStore.MarkProvisioned(contractID)
} else {
    provisioningService.StartServer(ctx, contractID)
}
```
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

payment.MarkPartiallyRefunded(partialAmount)
paymentRepo.Save(ctx, payment)
```
