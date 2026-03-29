---
sidebar_position: 4
---

# Payment Gateway

Contract Billing Core defines a `PaymentGateway` interface that abstracts payment processing. You implement this interface for your payment provider (Stripe, Braintree, PayPay, etc.).

## Interface

```go
type PaymentGateway interface {
    ID() string
    SupportedMethods() []PaymentMethodType

    // Direct charge
    Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error)

    // Two-phase: authorize then capture
    Authorize(ctx context.Context, req *AuthorizeRequest) (*AuthorizeResponse, error)
    Capture(ctx context.Context, req *CaptureRequest) (*CaptureResponse, error)
    Void(ctx context.Context, req *VoidRequest) (*VoidResponse, error)

    // Refunds
    Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)
    Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)

    // Transaction queries
    GetTransaction(ctx context.Context, transactionID string) (*Transaction, error)

    // Payment method management
    RegisterPaymentMethod(ctx context.Context, req *RegisterPaymentMethodRequest) (*PaymentMethodDetail, error)
    DeletePaymentMethod(ctx context.Context, paymentMethodID string) error
    GetPaymentMethod(ctx context.Context, paymentMethodID string) (*PaymentMethodDetail, error)
    ListPaymentMethods(ctx context.Context, customerID string) ([]*PaymentMethodDetail, error)
}
```

## Payment Method Types

```go
const (
    PaymentMethodTypeCreditCard       PaymentMethodType = "credit_card"
    PaymentMethodTypeDebitCard        PaymentMethodType = "debit_card"
    PaymentMethodTypeBankTransfer     PaymentMethodType = "bank_transfer"
    PaymentMethodTypeConvenienceStore PaymentMethodType = "convenience_store"
    PaymentMethodTypeQRCode           PaymentMethodType = "qr_code"
    PaymentMethodTypeCarrier          PaymentMethodType = "carrier"
    PaymentMethodTypePostpay          PaymentMethodType = "postpay"
    PaymentMethodTypeDirectDebit      PaymentMethodType = "direct_debit"
)
```

## Charge Flow

The simplest payment flow — charge immediately:

```go
pmID := "pm-visa-1234"
resp, err := gateway.Charge(ctx, &port.ChargeRequest{
    CustomerID:      "cust-001",
    Amount:          invoiceTotal,
    PaymentMethodID: &pmID,
    IdempotencyKey:  "charge-inv-001",
    Metadata:        map[string]string{"invoice_id": "inv-001"},
})
```

## Authorize/Capture Flow

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

A common pattern for hosting/cloud providers where service activation depends on payment:

```
1. Contract: Draft     → Create contract
2. Contract: Active    → Activate
3. Contract: Suspended → Immediately suspend (awaiting payment)
4. Invoice: Finalized  → Generate and finalize invoice
5. Payment: Completed  → Process payment
6. Contract: Active    → Resume → Provision service
```

This reuses the `Suspended` state for both initial activation (awaiting first payment) and non-payment suspension. Both resolve the same way: payment → resume.

The `AfterChargeHook` or `OnContractResumeHook` can trigger service provisioning:

:::note
`AfterChargeHook` receives a `PaymentContext` where `Contract()` returns `nil` by default. To access contract information, you need to resolve it via `Invoice.ContractID()` and look up the contract yourself. For this reason, the recommended approach for payment-gated provisioning is to handle the Resume in your application code (not in a hook) after `ProcessPayment` succeeds, as shown in the [Payment Integration Guide](../guides/payment-integration).
:::

```go
func (p *ProvisioningPlugin) OnContractResume(ctx *plugin.Context, c *contract.ContractAggregate) error {
    // Provision or re-activate the service
    return p.provisioningService.Activate(ctx.Context(), c.ContractID())
}
```

:::caution Known Limitation
`OnContractResumeHook` cannot distinguish between **initial activation** (first payment received) and **re-activation** (payment after suspension). This is because `SuspensionConfiguration` is cleared to `nil` in the aggregate's `Apply()` method before the hook fires.

**Workarounds:**
- Track provisioning state externally (e.g., a "provisioned" flag in your database)
- Use the `plugin.Context` metadata to pass the suspension reason from the orchestrating code
- Check if the service already exists before provisioning

See [Issue #5](https://github.com/contract-to-cash/core/issues/5) for details.
:::

## Payment Method Fallback Resolution

Payment methods are resolved hierarchically (Stripe-style):

```
1. Explicit PaymentMethodID in ProcessPaymentInput
2. Invoice.PaymentMethodID
3. Contract.PaymentMethodID
4. Customer.DefaultPaymentMethodID
```

:::note
The Contract and Customer levels of the fallback chain require `contractRepo` to be passed to `NewPaymentService`. If `contractRepo` is `nil`, resolution stops at the Invoice level.
:::

More specific levels override less specific ones. This enables:
- **Per-contract payment methods** (B2B with separate cards per subscription)
- **Auto-charge** without caller specifying payment method
- **Dunning retry** that picks up updated default payment method

## Implementing a Gateway

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
