---
sidebar_position: 6
---

# Payment Integration

This guide covers implementing a payment gateway and the payment-gated provisioning pattern. For the full `PaymentGateway` interface definition, see the [Services API Reference](../api/services#paymentservice).

## Implementing PaymentGateway

The `PaymentGateway` interface supports both direct charge and authorize/capture flows:

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
    // Map to Stripe API call
    pi, err := g.client.PaymentIntents.New(&stripe.PaymentIntentParams{
        Amount:        stripe.Int64(req.Amount.Amount().Num().Int64()),
        Currency:      stripe.String(string(req.Currency)),
        PaymentMethod: stripe.String(req.PaymentMethodID),
        Confirm:       stripe.Bool(true),
    })
    // Map response back...
}
```

Supported payment method types: `credit_card`, `debit_card`, `bank_transfer`, `convenience_store`, `qr_code`, `carrier`, `postpay`, `direct_debit`.

## Payment Processing Flow

```go
paymentService := service.NewPaymentService(
    gateway, paymentRepo, invoiceRepo, contractRepo,
    eventStore, registry, clock,
)

// Process a payment for an invoice
payment, err := paymentService.ProcessPayment(ctx, invoiceID, service.ProcessPaymentInput{
    PaymentMethodID: "pm-visa-1234",      // Optional if contract has default
    Amount:          invoice.AmountDue(),
    Currency:        shared.CurrencyJPY,
    IdempotencyKey:  "pay-" + invoiceID,  // Prevents duplicate charges
})
```

The payment flow:
1. **BeforeChargeHook** — Validate, enrich context
2. **Gateway.Charge** — Call external payment provider
3. **AfterChargeHook** (success) or **OnPaymentFailedHook** (failure)
4. Update invoice status

### Authorize/Capture Flow

For payment-gated provisioning where you need to confirm the charge before providing service:

```go
// 1. Authorize (reserve funds)
pmID := "pm-visa-1234"
authResp, _ := gateway.Authorize(ctx, &port.AuthorizeRequest{
    CustomerID:      "cust-001",
    Amount:          invoiceTotal,
    PaymentMethodID: &pmID,
    IdempotencyKey:  "auth-inv-001",
})

// 2. Provision service...

// 3. Capture (finalize the charge)
captureResp, _ := gateway.Capture(ctx, &port.CaptureRequest{
    AuthorizationID: authResp.AuthorizationID,
    Amount:          &invoiceTotal, // Can be less for partial capture
})
```

## Payment-Gated Provisioning

For services where access should only be granted after payment (e.g., hosting, cloud resources), the contract's `Suspended` state serves as a unified "service inactive" state:

| Step | Contract | Invoice | Description |
|------|----------|---------|-------------|
| 1 | Draft | — | Create contract |
| 2 | Draft | Draft | Generate invoice (status guard allows Draft) |
| 3 | Active | Finalized | User confirms, both finalized |
| 4 | Suspended | Finalized | Immediately suspend (awaiting payment) |
| 5 | Active | Paid | Payment confirmed → Resume → Service starts |

```go
// 1. Create contract (draft)
agg.Create(cmd, metadata)

// 2. Generate invoice while still in draft
inv, _ := billingService.GenerateInvoice(ctx, contractID, period)

// 3. Activate contract, then immediately suspend
agg.Activate(metadata)
agg.Suspend(contract.SuspensionConfiguration{
    BillingBehavior: contract.SuspensionBillingSkip,
    Reason:          "awaiting initial payment",
}, metadata)
contractRepo.Save(ctx, agg)

// 4. Finalize invoice and process payment
inv.Finalize()
invoiceRepo.Save(ctx, inv)
payment, _ := paymentService.ProcessPayment(ctx, inv.ID(), input)

// 5. On successful payment, resume (triggers provisioning hook)
if payment.Status() == "completed" {
    agg, _ = contractRepo.FindByID(ctx, contractID)
    agg.Resume(metadata)
    contractRepo.Save(ctx, agg) // OnContractResumeHook fires → provision service
}
```

This pattern reuses the `Suspended` state for both initial activation (awaiting first payment) and non-payment suspension. Both resolve the same way: payment → resume → service activated.

> **Note:** This is a recommended pattern, not a requirement. You can also use a simpler flow: `Draft → Activate → Generate Invoice → Process Payment`.

:::caution
In this pattern, your application code must determine whether to **provision a new service** or **re-activate an existing one**. The `OnContractResumeHook` cannot distinguish between these cases because the suspension reason is cleared before the hook fires.

A common approach is to track provisioning state separately:

```go
if !provisioningStore.IsProvisioned(contractID) {
    provisioningService.CreateServer(ctx, contractID)
    provisioningStore.MarkProvisioned(contractID)
} else {
    provisioningService.StartServer(ctx, contractID)
}
```

See [Issue #5](https://github.com/contract-to-cash/core/issues/5) for details.
:::

## Idempotency

Always provide an `IdempotencyKey` to prevent duplicate charges:

```go
service.ProcessPaymentInput{
    IdempotencyKey: fmt.Sprintf("pay-%s-%d", invoiceID, attempt),
}
```

## Handling Webhooks

Payment providers send asynchronous notifications. Handle them by updating payment status:

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

## Partial Payments and Refunds

```go
// Partial refund
gateway.Refund(ctx, &port.RefundRequest{
    TransactionID: payment.GatewayTransactionID(),
    Amount:        partialAmount, // Less than original charge
})

// Payment entity tracks refund status
payment.MarkPartiallyRefunded(partialAmount)
paymentRepo.Save(ctx, payment)
```
