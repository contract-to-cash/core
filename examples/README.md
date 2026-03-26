# Contract-to-Cash Core Examples

Contract-to-Cash Core is a Go library for building billing systems with **event sourcing**, an **extensible plugin system**, and a **rich domain model** covering contracts, invoices, payments, credits, and pricing.

These examples demonstrate the library's key capabilities. Each example is a standalone `main.go` that runs without any external dependencies (database, network, etc.) -- everything uses in-memory implementations.

## Examples

| Example | Focus | Key Concepts |
|---------|-------|--------------|
| [billing-demo](billing-demo/) | Basic billing flow | Contract -> Invoice -> Payment end-to-end |
| [event-sourcing-demo](event-sourcing-demo/) | Time travel & audit trail | Temporal queries, snapshots, event replay |
| [plugin-pipeline-demo](plugin-pipeline-demo/) | Extensible plugin system | Discount/tax hooks, priority ordering, custom plugins |
| [lifecycle-demo](lifecycle-demo/) | Contract lifecycle & credits | Trial, suspend/resume, cancel, FIFO credit application |
| [hosting-integration-demo](hosting-integration-demo/) | External service integration | Server provisioning/suspension via lifecycle hooks |
| [multi-service-demo](multi-service-demo/) | Multiple service types | PlanID-based plugin routing for VPS/SSL/Domain |
| [pricing-models-demo](pricing-models-demo/) | Flexible pricing | Flat, graduated tiered, volume tiered, usage-based |

## Prerequisites

- Go 1.22+

## Running

```bash
# Run any example
go run ./examples/billing-demo/
go run ./examples/event-sourcing-demo/
go run ./examples/plugin-pipeline-demo/
go run ./examples/lifecycle-demo/
go run ./examples/hosting-integration-demo/
go run ./examples/multi-service-demo/
go run ./examples/pricing-models-demo/
```

## Recommended Reading Order

1. **billing-demo** -- Start here. Covers the basic contract-to-cash flow (create contract, generate invoice with tax, process payment).
2. **event-sourcing-demo** -- See how event sourcing enables "time travel" queries and complete audit trails.
3. **plugin-pipeline-demo** -- Understand how the plugin system composes discount, tax, and lifecycle hooks with priority control.
4. **lifecycle-demo** -- Explore the full contract lifecycle (trial periods, suspension, credit management).
5. **hosting-integration-demo** -- See how billing events drive external service provisioning (the "so what?" of this library).
6. **multi-service-demo** -- See how multiple service types (VPS, SSL, Domain) coexist via PlanID-based plugin routing.
7. **pricing-models-demo** -- Compare different pricing models side by side (flat, tiered, usage-based).

## Architecture Overview

```mermaid
graph TD
    App["<b>Application Layer</b><br/>BillingService / PaymentService / QueryService"]

    App --> Domain
    App --> Plugin
    App --> ES

    Domain["<b>Domain</b><br/>Contract · Invoice · Payment<br/>Credit · Pricing · Usage"]
    Plugin["<b>Plugin System</b><br/>Registry · Hooks<br/>Tax · Coupon · Custom"]
    ES["<b>Event Store</b><br/>Store · Snapshot · Subscribe"]

    ES --> Infra["<b>Infrastructure</b><br/>InMemory* (swap to Postgres, etc.)"]

    style App fill:#4A90D9,color:#fff,stroke:none
    style Domain fill:#7B68EE,color:#fff,stroke:none
    style Plugin fill:#E67E22,color:#fff,stroke:none
    style ES fill:#27AE60,color:#fff,stroke:none
    style Infra fill:#95A5A6,color:#fff,stroke:none
```

Each layer depends only on the layers below it. The plugin system allows extending billing logic without modifying core code.

### Billing Pipeline (Plugin Hooks)

```mermaid
flowchart LR
    S["Subtotal"] --> LC1["🔌 BeforeCalculation<br/><i>InvoiceLifecycleHook</i>"]
    LC1 --> D["🔌 DiscountHooks<br/><i>Coupon, Loyalty, ...</i>"]
    D --> T["🔌 TaxHooks<br/><i>Japanese Tax 10%</i>"]
    T --> CR["Credit Application<br/><i>FIFO</i>"]
    CR --> LC2["🔌 AfterCalculation<br/><i>InvoiceLifecycleHook</i>"]
    LC2 --> INV["📄 Invoice"]

    style S fill:#3498DB,color:#fff,stroke:none
    style LC1 fill:#E67E22,color:#fff,stroke:none
    style D fill:#E67E22,color:#fff,stroke:none
    style T fill:#E67E22,color:#fff,stroke:none
    style CR fill:#27AE60,color:#fff,stroke:none
    style LC2 fill:#E67E22,color:#fff,stroke:none
    style INV fill:#2ECC71,color:#fff,stroke:none
```

### Contract Lifecycle (State Machine)

```mermaid
stateDiagram-v2
    [*] --> Draft: Create
    Draft --> Trialing: StartTrial
    Draft --> Active: Activate
    Trialing --> Active: EndTrial(converted)
    Trialing --> Cancelled: EndTrial(not converted)
    Active --> Suspended: Suspend
    Active --> Cancelled: Cancel
    Active --> PastDue
    Suspended --> Active: Resume
    PastDue --> Suspended: Suspend
    PastDue --> Cancelled: Cancel
    Cancelled --> [*]
    Expired --> [*]
```
