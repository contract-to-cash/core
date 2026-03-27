---
sidebar_position: 4
---

# Payment Integration Guide

This guide covers integrating a real payment gateway and implementing payment-gated provisioning.

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
    // 1. Create a PaymentIntent
    // 2. Confirm with the payment method
    // 3. Map response to ChargeResponse
    return &port.ChargeResponse{
        TransactionID: stripePaymentIntent.ID,
        Status:        mapStripeStatus(stripePaymentIntent.Status),
        Amount:        req.Amount,
        CreatedAt:     time.Now(),
    }, nil
}
```

## Payment Processing Flow

```go
paymentService := service.NewPaymentService(
    gateway, paymentRepo, invoiceRepo, contractRepo,
    nil, eventStore, registry, clock,
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

## Payment-Gated Provisioning

For services where access should only be granted after payment (e.g., hosting, cloud resources):

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

This pattern reuses the `Suspended` state for both:
- **Initial activation**: new contract awaiting first payment
- **Non-payment suspension**: existing contract with overdue payment

Both resolve the same way: payment completes → Resume → service activated.

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
