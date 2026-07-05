---
sidebar_position: 4
---

# Payment Gateway

Contract Billing Core defines a `PaymentGateway` interface that abstracts payment processing. You implement this interface for your payment provider (Stripe, Braintree, PayPay, etc.).

:::note Canonical reference
This page is an English summary. The canonical, in-depth specification is
[`docs/internals/payment-gateway.md`](../internals/payment-gateway.md) (see the
[Documentation Map](../README.md)). When details differ, the internals spec and the code win.
:::

## Interface Overview

The gateway supports direct charge, two-phase (authorize/capture), refunds, and payment method management:

```go
type PaymentGateway interface {
    ID() string
    SupportedMethods() []PaymentMethodType

    Charge(ctx, req *ChargeRequest) (*ChargeResponse, error)
    Authorize(ctx, req *AuthorizeRequest) (*AuthorizeResponse, error)
    Capture(ctx, req *CaptureRequest) (*CaptureResponse, error)
    Void(ctx, req *VoidRequest) (*VoidResponse, error)
    Refund(ctx, req *RefundRequest) (*RefundResponse, error)
    Cancel(ctx, req *CancelRequest) (*CancelResponse, error)
    GetTransaction(ctx, transactionID string) (*Transaction, error)

    RegisterPaymentMethod(ctx, req) (*PaymentMethodDetail, error)
    DeletePaymentMethod(ctx, paymentMethodID string) error
    GetPaymentMethod(ctx, paymentMethodID string) (*PaymentMethodDetail, error)
    ListPaymentMethods(ctx, customerID string) ([]*PaymentMethodDetail, error)
}
```

Supported payment methods: credit card, debit card, bank transfer, convenience store, QR code, carrier, postpay, direct debit.

## Payment Flows

### Direct Charge

The simplest flow — charge immediately:

```go
resp, err := gateway.Charge(ctx, &port.ChargeRequest{
    CustomerID:      "cust-001",
    Amount:          invoiceTotal,
    PaymentMethodID: &pmID,
    IdempotencyKey:  "charge-inv-001",
})
```

### Authorize/Capture

For cases where you need to reserve funds before finalizing:

```
1. Authorize → reserve funds
2. Provision service
3. Capture → finalize charge (can be partial)
```

## Payment Method Fallback Resolution

Payment methods are resolved hierarchically:

```
1. Explicit PaymentMethodID in ProcessPaymentInput
2. Invoice.PaymentMethodID
3. Contract.PaymentMethodID
4. Customer.DefaultPaymentMethodID
```

More specific levels override less specific ones. This enables per-contract payment methods (B2B), auto-charge without specifying a method, and dunning retry with updated defaults.

## Payment-Gated Provisioning

A recommended pattern for services where access depends on payment:

| Step | Contract Status | Action |
|------|----------------|--------|
| 1 | Draft | Create contract |
| 2 | Active | Activate |
| 3 | Suspended | Immediately suspend (awaiting payment) |
| 4 | — | Generate and finalize invoice |
| 5 | — | Process payment |
| 6 | Active | Resume → Provision service |

This reuses the `Suspended` state for both initial activation and non-payment suspension. Both resolve the same way: payment → resume.

:::caution Known Limitation
`OnContractResumeHook` cannot distinguish between initial activation and re-activation because `SuspensionConfiguration` is cleared before the hook fires. Track provisioning state externally to handle this.
:::

> For implementation details and code examples, see the [Payment Integration Guide](../guides/payment-integration.md).

## Next Steps

- [Payment Integration Guide](../guides/payment-integration.md) — Step-by-step implementation with code
- [Billing Flow Example](../examples/billing-flow.md) — End-to-end payment-gated provisioning demo
