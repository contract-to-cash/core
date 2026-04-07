# Add tests for remaining inmemory repository implementations

**Labels**: `testing`, `medium-priority`, `infrastructure`
**Source**: codebase-review-20260327 item #11

## Problem

`infrastructure/inmemory/` has 8 repository implementations but only 4 have dedicated test files:

| Repository | Test File | Status |
|-----------|-----------|--------|
| balance_repository.go | balance_repository_test.go | **Tested** |
| event_store.go | event_store_test.go | **Tested** |
| price_repository.go | price_repository_test.go | **Tested** |
| product_repository.go | product_repository_test.go | **Tested** |
| payment_repository.go | — | **Missing** |
| invoice_repository.go | — | **Missing** |
| usage_repository.go | — | **Missing** |
| contract_repository.go | — | **Missing** |

While integration tests cover some of these indirectly, the inmemory implementations serve as the **reference adapter** for users implementing their own DB-backed repositories. Bugs here propagate to all downstream testing.

## Acceptance Criteria

### payment_repository_test.go
- [ ] Save and FindByID
- [ ] FindByInvoiceID (multiple payments per invoice)
- [ ] FindByIdempotencyKey (found and not-found cases)
- [ ] Overwrite on duplicate Save

### invoice_repository_test.go
- [ ] Save and FindByID
- [ ] FindByContractID, FindByAccountID
- [ ] FindByStatus, FindByContractAndStatus
- [ ] FindByContractAndPeriod (period matching)
- [ ] FindUnpaidByContract
- [ ] FindOverdue
- [ ] FindByIDAsOf (temporal query)

### usage_repository_test.go
- [ ] Record and GetRecords
- [ ] GetSummary (aggregation correctness)
- [ ] Idempotency key deduplication
- [ ] Time range filtering

### contract_repository_test.go
- [ ] Save and FindByID
- [ ] FindByAccountID
- [ ] FindExpiring, FindTrialsEndingSoon, FindDueForRenewal
- [ ] FindByIDAsOf (event-sourced temporal query)

### General
- [ ] All tests use `shared.FixedClock`
- [ ] All tests run with `-race`
