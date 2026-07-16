# Add Order Aggregate to Support Shopping Cart (Multi-Product Checkout)

## Summary

The current design enforces a 1:1 relationship between Contract, Price, and Invoice, which means each purchase results in a separate invoice and payment. This makes it impossible to bundle multiple products (e.g., domain + hosting + SSL) into a single checkout and payment.

We propose introducing an **Order aggregate** to act as a shopping cart that groups multiple products into a single purchase, while keeping the existing Contract model unchanged for ongoing billing.

## Problem

| Constraint | Location | Description |
|---|---|---|
| `CreateContractCommand.PriceID` | `domain/contract/aggregate.go` | Accepts only a single PriceID |
| `Invoice.contractID` | `domain/invoice/entity.go` | Bound to a single Contract |
| `BillingService.GenerateInvoice()` | `application/service/billing_service.go` | Takes a single contractID |

**Use case not supported:** A customer wants to purchase a domain registration (one-time), a hosting plan (subscription), and an SSL certificate (one-time) in a single checkout with one payment.

## Proposed Solution

Introduce a new `Order` aggregate under `domain/order/` that serves as a cart/checkout boundary.

### New Entities

**Order** (Aggregate Root)

- `OrderID`, `AccountID`, `Status` (draft / confirmed / fulfilled / cancelled)
- Contains one or more `OrderItem`s
- Generates a single `Invoice` upon confirmation

**OrderItem**

- `OrderItemID`, `PriceID`, `Quantity`, `UnitPrice`, `Amount`
- References `Price` (which in turn references `Product` — no direct ProductID to maintain 3NF)
- Generates a `Contract` upon order confirmation

### Changes to Existing Entities

| Entity | Change | Details |
|---|---|---|
| `Invoice` | Add `OrderID` field | Nullable. Mutually exclusive with `ContractID` — one or the other is set |
| `LineItem` | Add `OrderItemID` field | Nullable. Traces back to the originating OrderItem for cart purchases |

**No changes** to Contract, Payment, Price, Product, or other existing entities.

### Data Model (ER)

```mermaid
erDiagram
    Account ||--o{ Order : places
    Account ||--o{ Contract : holds
    Account ||--o{ BalanceEntry : credits

    Order ||--|{ OrderItem : contains
    Order ||--o| Invoice : generates
    Order {
        OrderID id PK
        AccountID account_id FK
        OrderStatus status
        Money total_amount
        string idempotency_key
        time created_at
        time confirmed_at
    }

    OrderItem }o--|| Price : "priced by"
    OrderItem ||--o| Contract : "creates on confirm"
    OrderItem {
        OrderItemID id PK
        OrderID order_id FK
        PriceID price_id FK
        int quantity
        Money unit_price
        Money amount
        ContractID contract_id FK
    }

    Product ||--o{ Price : "has pricing"
    Product {
        ProductID id PK
        string name
        string description
        ProductStatus status
    }

    Price {
        PriceID id PK
        ProductID product_id FK
        Money amount
        Currency currency
        BillingCycle billing_cycle
        PricingModel pricing_model
        PriceStatus status
    }

    Contract }o--|| Price : uses
    Contract ||--o{ Invoice : "billed via"
    Contract ||--o{ UsageRecord : "tracks usage"
    Contract {
        ContractID id PK
        AccountID account_id FK
        PriceID price_id FK
        ContractStatus status
        ContractType contract_type
        BillingCycle billing_cycle
        DateRange current_period
        Money price
        int version
    }

    Invoice ||--|{ LineItem : "itemized as"
    Invoice ||--o{ Payment : "paid by"
    Invoice ||--o{ CreditNote : "adjusted by"
    Invoice {
        InvoiceID id PK
        AccountID account_id FK
        ContractID contract_id FK
        OrderID order_id FK
        InvoiceStatus status
        Money subtotal
        Money discount_amount
        Money tax_amount
        Money total
        Money applied_balance
        Money amount_due
        DateRange billing_period
        time due_date
    }

    LineItem {
        string id PK
        string description
        int64 quantity
        Money unit_price
        Money amount
        PriceID price_id FK
        OrderItemID order_item_id FK
    }

    Payment {
        PaymentID id PK
        InvoiceID invoice_id FK
        Money amount
        PaymentStatus status
        string idempotency_key
    }

    CreditNote {
        CreditNoteID id PK
        InvoiceID invoice_id FK
        CreditNoteStatus status
        Money total
    }

    BalanceEntry {
        BalanceEntryID id PK
        AccountID account_id FK
        Money remaining_amount
        BalanceReason reason
    }

    UsageRecord {
        UsageRecordID id PK
        ContractID contract_id FK
        string metric_name
        int64 quantity
    }
```

**Reference path (3NF compliant):**

- `OrderItem -> Price -> Product` (no transitive dependency)
- `Contract -> Price -> Product` (same pattern, already in place)

### Flow

```
1. Create Order (draft), add OrderItems
     - Domain:  PriceID=price_1, qty=1
     - Hosting: PriceID=price_2, qty=1
     - SSL:     PriceID=price_3, qty=1

2. Order.Confirm()
     - Create Contract per OrderItem (one_time or subscription based on Price.BillingCycle)
     - Generate single Invoice with LineItems mapped from OrderItems
     - Apply DiscountHook -> TaxHook -> Credit (existing plugin flow)

3. Single Payment for the Invoice

4. Subsequent billing (e.g., hosting renewal) uses existing
   Contract-based BillingService.GenerateInvoice() -- no change needed
```

### Scope of Changes

| Area | Work |
|---|---|
| `domain/order/` (new) | `Order` aggregate, `OrderItem` entity, `OrderStatus`, repository interface |
| `domain/invoice/` | Add optional `OrderID` to `Invoice`, optional `OrderItemID` to `LineItem` |
| `application/service/` | Add `OrderService` for cart management and checkout orchestration |
| `infrastructure/inmemory/` | In-memory `OrderRepository` for testing |
| `tests/` | Unit + integration tests for Order lifecycle and multi-product checkout |

### Design Decisions

1. **Order is separate from Contract** — Order represents "a purchase at a point in time"; Contract represents "an ongoing billing relationship". Mixing them would violate SRP.
2. **Invoice.ContractID and Invoice.OrderID are mutually exclusive** — An invoice is either for a single contract's recurring billing OR for a cart checkout, never both.
3. **No ProductID on OrderItem or Contract** — `PriceID -> ProductID` is a transitive functional dependency. Product is always resolved via Price to maintain 3NF.
4. **Existing plugin hooks work unchanged** — The calculation flow (DiscountHook -> TaxHook -> Credit -> Invoice) applies to Order-based invoices the same way.

## Out of Scope

- Saved carts / wishlist functionality
- Order editing after confirmation
- Splitting an order into multiple shipments/invoices
- Order-level discount hooks (can be added later as `OrderDiscountHook`)

## Checklist

- [ ] `domain/order/` does not depend on external packages (stdlib + ulid only)
- [ ] New domain events registered in `EventRegistry` and `Apply()` (if Order is event-sourced)
- [ ] `Money` operations validate currency consistency across OrderItems
- [ ] `Clock` interface used for all timestamps
- [ ] Tests pass with `-race`
- [ ] No use of `PlanID` in new code — use `ProductID` + `PriceID`
