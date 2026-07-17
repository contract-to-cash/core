---
sidebar_position: 3
---

# Architecture Overview

**Event Sourcing + Plugin Architecture for contract-to-cash billing**

## 1. Overview

### 1.1 Purpose

Contract and billing logic is fundamentally similar across SaaS and service businesses. This package provides:

- Contract lifecycle management (one-time / subscription / usage-based)
- Complete audit trail via Event Sourcing
- Extensibility through a Plugin Architecture (coupons, discounts, tax, etc.)

### 1.2 System Diagram

```mermaid
graph TB
    subgraph Application
        subgraph Plugins
            CP[Coupon Plugin]
            TP[Tax Plugin]
            CUP[Custom Plugin]
        end

        CP --> PAI
        TP --> PAI
        CUP --> PAI
        PAI[Plugin Adapter Interface]

        subgraph ContractCore[Contract Core]
            SE[Subscription Engine]
            OE[One-Time Engine]
            UE[Usage-Based Engine]
        end

        PAI --> ContractCore

        ES[(Event Store\nAppend-Only Log)]
        ContractCore --> ES
    end
```

## 2. Design Principles

### 2.1 Layer Architecture (Clean Architecture + DDD)

```mermaid
graph TB
    subgraph PL[Presentation Layer]
        PL_DESC[HTTP Handler, gRPC, CLI]
    end

    subgraph AL[Application Layer]
        AL_DESC[UseCase, Command/Query Handler]
    end

    subgraph DL[Domain Layer]
        DL_DESC[Entity, Value Object,\nDomain Service, Repository Interface]
        DL_NOTE[No external dependencies]
    end

    subgraph IL[Infrastructure Layer]
        IL_DESC[PostgreSQL, MySQL,\nDynamoDB, EventStore...]
    end

    PL -->|depends on| AL
    AL -->|depends on| DL
    IL -.->|implements| DL
```

**Strict rules:**

- `domain/` takes no third-party external dependencies: only the stdlib, `github.com/oklog/ulid/v2`, and the same-module infrastructure-free `eventstore/` interfaces (the event-sourced `domain/contract` aggregate embeds `eventstore.BaseAggregate` and implements `eventstore.DomainEvent`/`EventRegistry`; `eventstore/` itself depends only on `domain/shared`, so no cycle). No other `domain/*` package imports `eventstore/`.
- `application/` depends on `domain/` plus the infrastructure-free base packages `eventstore/` and `plugin/` (see the dependency graph in 2.2), never on `infrastructure/`
- Dependencies always point inward (Dependency Inversion)
- Interfaces are defined in `domain/` or `application/port/`; implementations live in `infrastructure/`

### 2.2 Dependency Graph

```mermaid
graph BT
    shared["domain/shared"]
    contract["domain/contract"] --> shared
    invoice["domain/invoice"] --> shared
    payment["domain/payment"] --> shared
    balance["domain/balance"] --> shared
    usage["domain/usage"] --> shared
    product["domain/product"] --> shared
    pricing["domain/pricing"] --> shared
    contract --> pricing
    eventstore["eventstore/"] --> shared
    contract --> eventstore
    plugin["plugin/"] --> contract
    plugin --> invoice
    plugin --> payment
    appService["application/service/"] --> contract
    appService --> invoice
    appService --> usage
    appService --> balance
    appService --> pricing
    appService --> product
    appService --> plugin
    appService --> eventstore
    port["application/port/"] --> shared
    infra["infrastructure/inmemory/"] -.->|implements| contract
    infra -.->|implements| invoice
    plugins["plugins/"] --> plugin
    batch["batch/"] --> appService
```

### 2.3 CQRS (Command Query Responsibility Segregation)

| Item | Decision |
|------|----------|
| CQRS | **Simplified CQRS** |
| Projection updates | **Consumer's choice** |
| Description | Uses projection tables in the same DB. Sync/async selectable via options |

### 2.4 Other Design Decisions

| Item | Decision | Notes |
|------|----------|-------|
| Multi-tenancy | Delegated to consumer | Not handled by this library |
| Timezone | **UTC only** | All event timestamps are UTC |
| Billing cycles | Provided by library | Daily / Weekly / Monthly / Yearly |

## 3. Package Structure

```
github.com/contract-to-cash/core/
├── domain/                      # Domain layer (no external deps)
│   ├── contract/                #   Event Sourced aggregate
│   ├── invoice/                 #   Invoice + CreditNote entities
│   ├── payment/                 #   Payment entity + Dunning
│   ├── balance/                 #   Credit ledger
│   ├── pricing/                 #   Immutable Price, pricing models
│   ├── product/                 #   Product definition
│   ├── usage/                   #   Usage record + summary
│   └── shared/                  #   Shared value objects (Money, Clock, etc.)
│
├── application/                 # Application layer
│   ├── port/                    #   External integration IFs (PaymentGateway, etc.)
│   ├── query/                   #   Temporal query service
│   ├── projection/              #   Projection service (sync/async)
│   ├── tx/                      #   Transaction management (TxManager, Saga)
│   └── service/                 #   BillingService, PaymentService, SnapshotService, CreditNoteService
│
├── plugin/                      # Plugin system core
├── eventstore/                  # Event Store interfaces
├── batch/                       # Batch processing (ContractRenewal, etc.)
├── infrastructure/inmemory/     # In-memory implementations (test/demo)
└── plugins/                     # Official plugins
    ├── coupon/
    ├── tax/
    └── invoicecleanup/
```

## 4. Core Components

### 4.1 Domain Entities

| Entity | Kind | Notes |
|--------|------|-------|
| **Contract** | Event Sourced Aggregate | States: Draft -> Trialing -> Active -> PastDue/Suspended -> Cancelled/Expired |
| **Invoice** | Entity | Revision chain support (void-and-recreate) |
| **CreditNote** | Entity | Line-item-level adjustments |
| **Payment** | Entity | Idempotency key required |
| **Price** | Immutable Entity | Flat / Tiered (Graduated, Volume) / Usage pricing models |
| **Product** | Entity | Defines "what to sell"; separated from Price ("how to charge") |
| **BalanceEntry** | Entity | FIFO consumption, expiration support |

### 4.2 Contract Types

| Type | Description |
|------|-------------|
| `one_time` | One-time purchase |
| `subscription` | Recurring billing |
| `usage_based` | Metered billing |

### 4.3 Contract State Machine

```mermaid
stateDiagram-v2
    [*] --> Draft : Create
    Draft --> Active : Activate
    Draft --> Trialing : StartTrial
    Draft --> Cancelled : Cancel
    Trialing --> Active : EndTrial(converted=true)
    Trialing --> Cancelled : EndTrial(converted=false) / Cancel
    Active --> PastDue : Payment failure (Dunning)
    Active --> Suspended : Suspend
    Active --> Cancelled : Cancel
    Active --> Expired : Term end (autoRenew=false)
    PastDue --> Active : Payment success
    PastDue --> Suspended : Max retries reached
    PastDue --> Cancelled : Cancel
    Suspended --> Active : Resume
    Suspended --> Cancelled : Cancel
    Cancelled --> [*]
    Expired --> [*]
```

## 5. Payment-Gated Provisioning (Recommended Flow)

A pattern where service access is withheld until the first payment succeeds. This uses existing state transitions only -- no new statuses needed.

### 5.1 Flow

| Step | Contract Status | Invoice Status | Description |
|------|----------------|----------------|-------------|
| 1 | Draft | (none) | Create contract |
| 2 | Draft | Draft | Generate invoice (Draft status allows this) |
| 3 | Active | Finalized | User confirms; finalize both contract and invoice |
| 4 | Suspended | Finalized | Immediately suspend (awaiting payment) |
| 5 | Active | Paid | Payment confirmed -> Resume -> Service starts |

### 5.2 Design Points

- **Unified Suspended state**: Used for both "awaiting first payment" and "payment failure suspension"
- **No new transitions needed**: Active -> Suspended -> Active (Resume) already exists
- **Payment gates service access**: Activate then immediately Suspend; Resume only after payment confirmation

A simpler flow (`Draft -> Activate -> Generate Invoice -> Process Payment`) is also supported. Choose based on business requirements.

## 6. Plugin System

### 6.1 Extension Points

All hooks follow ISP (Interface Segregation Principle). Implement only the hooks you need.

| Category | Hooks | Purpose |
|----------|-------|---------|
| **Billing calculation** | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` | Discounts, tax, pre/post calculation |
| **Contract lifecycle** | `OnContractCreate/Activate/Suspend/Resume/Cancel/CancelScheduled/CancelUnscheduled/Renew/TrialEndHook` | React to individual contract events |
| **Payment** | `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentFailedHook`, `OnRefundHook`, `OnCompensationExecutedHook` | Pre/post charge, failure, refund, saga compensation (charge reversal) |
| **Credit notes** | `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` | CN issuance, invoice revision |
| **Metrics** | `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` | KPI collection |
| **Invoice generation** | `InvoiceGenerationHook` | PDF generation, delivery |

### 6.2 Hook Firing Responsibility

Not every hook is fired by the core. Of the 23 hook interfaces, 15 are invoked
automatically by core services/batch processors; the rest are fired by the
integrator or by an adapter (see `docs/internals/plugin-system.md` section 5.3
for the per-hook detail):

| Fired by | Hooks | Where |
|----------|-------|-------|
| **Core** (15) | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook`, `OnInvoiceIssuedHook` | `BillingService` (billing pipeline; `FinalizeInvoice` fires OnInvoiceIssued) |
| | `BeforeChargeHook`, `AfterChargeHook`, `OnPaymentProcessedHook`, `OnPaymentFailedHook`, `OnRefundHook`, `OnCompensationExecutedHook` | `PaymentService` (`OnCompensationExecutedHook` fires non-fatally after saga compensation of a gateway charge, on both the reversed and the failed/manual-reconciliation outcome; issue #257) |
| | `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` | `CreditNoteService` |
| | `OnContractRenewHook`, `OnContractTrialEndHook`, `OnContractChangeHook` | `batch.ContractRenewalProcessor`, `batch.TrialExpirationProcessor` |
| **Integrator** (7) | `OnContractCreate/Activate/Suspend/Resume/Cancel/CancelScheduled/CancelUnscheduled Hook` | Contract lifecycle operations (including `ScheduleCancellation`/`UnscheduleCancellation`) call aggregate methods directly (no core application service), so the integrator fires the matching hooks. Reference: `examples/hosting-integration-demo/main.go` |
| **Adapter** (1) | `InvoiceGenerationHook` | Invoice rendering/delivery is out of core scope; the consumer's invoice-generation adapter fires BuildDocument/AfterRender/AfterDelivery |

**Transactional outbox extension (issue #248).** Separately from the 23 hooks
(#248 itself added no hooks), the core also calls two *integrator ports* —
`PaymentOutboxWriter` and `InvoiceOutboxWriter` (in `application/port`) — *inside*
the bookkeeping transaction, immediately after the payment/invoice row is saved
and before commit. Wired via `WithPaymentOutboxWriter` / `WithInvoiceOutboxWriter`
(nil = skipped), they let an integrator write a durable notification row in the
SAME transaction as the write, closing the event-loss window that the post-commit
hooks cannot. A writer error vetoes (rolls back) the transaction; on the payment
path that reverses the gateway charge via saga compensation. See
`docs/internals/plugin-system.md` §11.

### 6.3 Billing Pipeline

The core structurally guarantees the accounting-correct calculation order. Plugin `Priority` values only control execution order *within* the same hook type.

```mermaid
flowchart LR
    subgraph Pipeline["BillingService Invoice Generation"]
        A["1. Load contract"] --> B["2. BeforeCalculation hook (ctx.Subtotal()=0)"]
        B --> C["3. Populate subtotal on context"]
        C --> D["4. DiscountHook"]
        D --> E["5. Discount cap guard"]
        E --> F["6. Subtotal after discount"]
        F --> G["7. TaxHook"]
        G --> H["8. Total = subtotal + tax"]
        H --> I["9. Credit application (FIFO)"]
        I --> J["10. Create draft invoice"]
        J --> K["11. AfterCalculation hook"]
        K --> L["12. Save"]
    end
```

**Calculation order detail** (plugin-observable; matches `executeBillingPipeline` in `application/service/billing_service.go`):

> Every amount the pipeline persists is quantized to the currency's minor unit with
> `BillingConfig.TaxRoundingMode` (default `RoundDown`) so the invoice reconciles exactly
> against integer-only gateways (issue #189): the subtotal is rounded up front, the summed
> discount is rounded before the cap guard, and the summed tax is rounded once per invoice.
> All hooks are fired via `plugin.SafeInvoke`/`SafeInvokeMoney`, which converts a plugin
> panic into a `*PluginPanicError` (fatality policy in `docs/internals/plugin-system.md` §5.4).

1. `InvoiceLifecycleHook.BeforeCalculation()` -- Pre-calculation processing.
   **`ctx.Subtotal()` returns ZERO here** — the core creates the `CalculationContext`
   with a zero subtotal and only calls `SetSubtotal` *after* this hook. (`ctx.ProductID()`
   and `ctx.BillingPeriod()` are already available; ProductID is resolved from the
   contract's Price and the billing period is set before any calculation hook runs.)
2. Base price is computed (core, branched by contract type), rounded to the minor unit,
   and populated onto the context; from here `ctx.Subtotal()` returns the base price.
   - subscription: fixed price
   - usage_based: UsageRecord aggregation -> included allowance deduction -> PricingModel
   - one_time: fixed price (once)
   - hybrid: base price + usage charge
3. `DiscountHook.CalculateDiscount()` -- Discount calculation (`ctx.Subtotal()` = base price)
   - Boundary validation: a negative discount aborts with `ErrCodeBusinessRule`, a
     currency mismatch with `ErrCodeCurrencyMismatch` (both name the plugin; issue #188)
   - Summed discount rounded to the minor unit (issue #189)
   - Discount cap guard: total discount is capped at subtotal
4. Subtotal after discount (core: subtotal - totalDiscount) -> `ctx.SetSubtotalAfterDiscount()`
5. `TaxHook.CalculateTax()` -- Tax on `ctx.SubtotalAfterDiscount()`
   - Boundary validation: a negative tax aborts with `ErrCodeBusinessRule` (issue #188)
   - Summed tax rounded to the minor unit, once per invoice (issue #189)
6. Total computation (core: afterDiscount + totalTax)
7. Credit ledger application (core, inside the transaction) -- FIFO deduction from balance
8. Create draft invoice (core, inside the transaction) -> finalize after GracePeriod via `FinalizeInvoice`
9. `InvoiceLifecycleHook.AfterCalculation()` -- Post-calculation processing, fired **before Save**
   (the invoice the plugin receives is not yet persisted; use `OnInvoiceIssuedHook`, fired
   after the save in `FinalizeInvoice`, for persistence-dependent work)
10. Save (core, inside the transaction)

## 7. Related Documents

| Document | Contents |
|----------|----------|
| [Domain Model](./concepts/domain-model.md) | Entities, value objects, detailed design |
| [Event Sourcing](./concepts/event-sourcing.md) | Event Store, temporal reconstruction |
| [Plugin System](./concepts/plugin-system.md) | Plugin implementation guide |
| [Payment Gateway](./concepts/payment-gateway.md) | Payment interface design |
| [Integration Guide](./guides/integration.md) | How to integrate into your service |
