---
sidebar_position: 1
---

# Domain Model

The domain model consists of entities, value objects, and aggregates that represent the billing domain.

## Entity Relationship Diagram

```mermaid
erDiagram
    Account ||--o{ Contract : "holds"
    Account ||--o{ BalanceEntry : "owns"
    Product ||--o{ Price : "priced by"
    Product ||--o{ Feature : "includes"
    Product ||--o{ UsageMetric : "tracks"
    Contract ||--o{ Invoice : "generates"
    Contract ||--o{ UsageRecord : "records"
    Contract ||--o| TrialConfiguration : "trial config"
    Contract ||--o| SuspensionConfiguration : "suspension config"
    Invoice ||--o{ LineItem : "contains"
    Invoice ||--o{ CreditNote : "adjusted by"
    Invoice ||--o{ Payment : "paid by"
    CreditNote ||--o{ CreditNoteItem : "contains"
    Invoice ||--o| Invoice : "revision of"

    Account {
        AccountID id PK
    }

    Product {
        ProductID id PK
        string name
        string description
        ProductStatus status "active | archived"
    }

    Feature {
        string name
        bool included
        int64 limit "optional"
    }

    UsageMetric {
        string name
        int64 includedQuantity
    }

    Price {
        PriceID id PK
        ProductID productID FK
        Money amount
        Currency currency
        BillingInterval interval "{unit, count} 例: {month, 3}"
        PricingModel pricingModel "flat | tiered | usage"
        PriceStatus status "active | archived"
    }

    Contract {
        ContractID id PK
        AccountID accountID FK
        PriceID priceID FK
        ContractStatus status "draft | trialing | active | past_due | suspended | cancelled | expired"
        ContractType contractType "one_time | subscription | usage_based"
        BillingInterval interval
        DateRange currentPeriod
        Money price
        bool autoRenew
        bool cancelAtPeriodEnd
        PriceID pendingPriceID "optional"
        int version
    }

    TrialConfiguration {
        timestamp trialEndDate
        bool autoConvert
        bool requirePaymentMethod
    }

    SuspensionConfiguration {
        timestamp suspendedAt
        timestamp resumeDate "optional"
        SuspensionBillingBehavior billingBehavior "skip | defer | continue"
        string reason
    }

    Invoice {
        InvoiceID id PK
        AccountID accountID FK
        ContractID contractID FK
        InvoiceStatus status "draft | finalized | issued | paid | partial_paid | overdue | voided | refunded"
        Money subtotal
        Money taxAmount
        Money discountAmount
        Money total
        Money appliedBalance
        Money amountDue
        DateRange billingPeriod
        timestamp dueDate
        InvoiceID revisionOf "optional"
    }

    LineItem {
        string id PK
        string description
        int64 quantity
        Money unitPrice
        Money amount
        Decimal taxRate
        PriceID priceID FK "optional"
    }

    Payment {
        PaymentID id PK
        InvoiceID invoiceID FK
        Money amount
        Money refundedAmount
        PaymentMethod method "credit_card | bank_transfer | direct_debit"
        PaymentStatus status "pending | completed | failed | refunded | charged_back"
        string gatewayTransactionID
        string idempotencyKey
    }

    BalanceEntry {
        BalanceEntryID id PK
        AccountID accountID FK
        Money originalAmount
        Money remainingAmount
        BalanceReason reason "proration | cancellation | manual_adjustment | goodwill"
        timestamp expiresAt "optional"
        int version "optimistic lock"
    }

    UsageRecord {
        UsageRecordID id PK
        ContractID contractID FK
        string metricName
        int64 quantity
        string idempotencyKey
        timestamp timestamp
    }

    CreditNote {
        CreditNoteID id PK
        InvoiceID invoiceID FK
        AccountID accountID FK
        ContractID contractID FK
        CreditNoteStatus status "draft | issued | applied | refunded | voided"
        CreditNoteReason reason "duplicate | order_change | cancellation | other"
        Money total
    }

    CreditNoteItem {
        string invoiceLineItemID FK
        string description
        Money amount
        Money taxAmount
    }
```

:::info Legend
- **Account** is an external boundary (managed outside this domain)
- **Contract** is the event-sourced aggregate root (`ContractAggregate`)
- **Money** is a `big.Rat`-based value object; IDs are ULID-generated
- **TrialConfiguration / SuspensionConfiguration** are value objects (no persistence ID)
:::

## Contract Aggregate

The contract is the central entity, modeled as an event-sourced aggregate root. It manages the full lifecycle of a billing agreement.

### States

| Status | Description |
|--------|-------------|
| `draft` | Contract created but not yet active |
| `trialing` | In trial period |
| `active` | Currently active and billable |
| `past_due` | Payment overdue (dunning in progress) |
| `suspended` | Temporarily suspended |
| `cancelled` | Permanently cancelled |
| `expired` | Term ended without renewal |

### Contract Types

| Type | Description |
|------|-------------|
| `one_time` | Single purchase, no recurring billing |
| `subscription` | Recurring billing at a fixed interval |
| `usage_based` | Charges based on metered usage |

### Operations

The aggregate supports lifecycle management, price changes, trials, and scheduled cancellations. All operations emit domain events for the event store.

> For complete struct definitions, commands, and getters, see [Domain Types Reference](../api/domain-types.md#contract).

## Invoice

Invoices are generated by `BillingService` and track the billing calculation result.

### States

```mermaid
stateDiagram-v2
    [*] --> draft
    draft --> finalized
    finalized --> issued
    issued --> paid
    issued --> overdue
    issued --> partial_paid
    overdue --> voided
    partial_paid --> paid
```

Invoices support a **revision chain** (void-and-recreate) for corrections: `originalInvoiceID` points to the chain root, `revisionOf` points to the direct parent.

> For construction options, methods, and line item details, see [Domain Types Reference](../api/domain-types.md#invoice).

## Payment

Tracks individual payment transactions with idempotency keys for duplicate prevention.

States: `pending` → `completed` / `failed` → `partially_refunded` / `refunded` / `charged_back`

> For complete type definitions, see [Domain Types Reference](../api/domain-types.md#payment).

## Product and Price

Following the Stripe pattern of separating **what you sell** from **how you charge**:

- **Product** — Defines the offering: name, features, usage metrics
- **Price** — Defines billing: amount, currency, cycle, pricing model. **Immutable after creation** — to change pricing, create a new Price

Pricing models: `FlatPrice`, `TieredPrice` (graduated or volume), `UsagePrice`

> For complete type definitions and pricing model details, see [Domain Types Reference](../api/domain-types.md#product).

## Credit Ledger

FIFO-based credit system for handling prorations, cancellation credits, and adjustments. Credits are automatically consumed during invoice generation, oldest first.

Reasons: `proration`, `cancellation`, `manual_adjustment`, `refund_conversion`, `goodwill`

> For complete type definitions, see [Domain Types Reference](../api/domain-types.md#credit).

## Shared Value Objects

| Type | Description |
|------|-------------|
| **Money** | Arbitrary-precision (`big.Rat`) monetary value with currency. Currencies: JPY, USD, EUR |
| **DateRange** | Half-open interval `[start, end)` for billing periods |
| **ID Types** | ULID-based: `ContractID`, `InvoiceID`, `PaymentID`, `ProductID`, `PriceID`, etc. |
| **Clock** | Time abstraction (`SystemClock` for production, `FixedClock` for tests) |
| **DomainError** | Structured errors with `ErrorCode` for business vs technical errors |

> For complete value object APIs, see [Domain Types Reference](../api/domain-types.md#shared-value-objects).
