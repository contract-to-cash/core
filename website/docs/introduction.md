---
sidebar_position: 1
slug: /introduction
---

# Introduction

Contract Billing Core is an open-source Go package for building contract-based billing systems. It provides event sourcing, a plugin architecture, and domain-driven design patterns for SaaS and subscription businesses.

## Why Contract Billing Core?

Building a billing system from scratch is complex. You need to handle contract lifecycles, invoice generation, payment processing, tax calculation, discounts, usage metering, and audit trails — all while maintaining data consistency.

Contract Billing Core provides these building blocks as a composable library, not an opinionated framework. You bring your own database, payment gateway, and business rules. The library provides the domain model, event sourcing infrastructure, and extension points.

## Key Features

- **Event Sourcing** — Every state change is recorded as an immutable event. Reconstruct any entity's state at any point in time. Full audit trail out of the box.
- **Plugin Architecture** — Extend billing logic through well-defined hooks: discounts, taxes, invoice lifecycle, contract lifecycle, payment processing, and metrics collection. Implement only the interfaces you need (ISP-compliant).
- **Product/Price Separation** — Following the Stripe model, products (what you sell) and prices (how you charge) are separate entities. Prices are immutable — price revisions create new Price objects, enabling grandfathering and clean migrations.
- **Multiple Billing Models** — One-time purchases, recurring subscriptions, and usage-based billing. Supports flat pricing, tiered pricing, volume pricing, and per-contract overrides.
- **Contract Renewal** — Automatic renewal with pending price promotion. Schedule price changes for end-of-term with `pendingPriceID`, or apply immediately with proration.
- **Payment Gateway Abstraction** — Pluggable interface for charge, authorize/capture, void, refund, and payment method management. Hierarchical fallback resolution (Invoice → Contract → Customer).
- **Credit Notes & Invoice Revision** — Issue credit notes against invoices (for duplicates, order changes, cancellations, etc.), apply as account credit or process refunds. Void-and-recreate invoices with full revision chain tracking (`originalInvoiceID` / `revisionOf`).
- **Credit Ledger** — FIFO-based credit system for prorations, cancellation credits, and manual adjustments.
- **Temporal Queries** — Query contract state at any past point in time using event replay or snapshot recovery.

## Architecture Overview

```mermaid
graph TD
    subgraph Application Layer
        BS[BillingService]
        PS[PaymentService]
        CNS[CreditNoteService]
        SS[SnapshotService]
        TQ[TemporalQueryService]
        PJ[ProjectionService]
    end
    subgraph Domain Layer
        C[Contract Aggregate]
        I[Invoice]
        CN[CreditNote]
        P[Payment]
        CR[Credit]
        U[Usage]
        PR[Product / Pricing]
    end
    subgraph Plugin System
        DH[DiscountHook]
        TH[TaxHook]
        ILH[InvoiceLifecycleHook]
        CLH[ContractLifecycleHooks]
        PH[PaymentHooks]
        MH[MetricsHooks]
        CNH[CreditNoteHooks]
    end
    subgraph Infrastructure Layer
        ES[EventStore]
        RP[Repositories]
        PG[PaymentGateway Port]
    end
    Application Layer --> Domain Layer
    Application Layer --> Plugin System
    Infrastructure Layer --> Domain Layer
```

## Design Principles

1. **Domain-Driven Design** — Clear bounded contexts with Contract as the primary aggregate root.
2. **Dependency Inversion** — Core domain depends on interfaces, not implementations. You provide repository and gateway implementations.
3. **Interface Segregation** — Plugins implement only the hooks they need. A tax plugin doesn't need to implement discount hooks.
4. **Immutable Events** — Once recorded, events cannot be modified. Schema evolution is handled through upcasters.
5. **Immutable Prices** — Price entities cannot be modified after creation. Price changes create new Price objects, preserving history.

## Who Is This For?

- **SaaS/Subscription Platforms** building billing from scratch or replacing a monolithic billing system
- **Hosting/Cloud Providers** needing contract lifecycle management with provisioning hooks
- **B2B Platforms** requiring multi-contract, multi-currency billing with audit trails
- **Developers** who want a billing domain model without vendor lock-in to a specific payment processor

## Next Steps

- [Quick Start](./quick-start) — Install and run your first billing flow in minutes
- [Architecture](./architecture) — Deep dive into the system design
- [Examples](./examples/billing-flow) — Working code examples
