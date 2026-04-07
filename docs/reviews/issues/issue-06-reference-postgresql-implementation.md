# Provide reference PostgreSQL implementation for repository interfaces

**Labels**: `infrastructure`, `high-priority`, `framework`, `help-wanted`

## Problem

The library defines 8+ repository interfaces but provides only `inmemory` implementations. Users must implement all repository interfaces from scratch for production use. This is the single biggest barrier to adoption.

The SQL schema exists in documentation (`docs/design/event-sourcing.md` section 8) but:
- No `.sql` migration files
- No Go implementation of any repository against a real database
- No example of how to implement `eventstore.Store` with PostgreSQL
- No guidance on transaction management with real databases

## Proposal

### Phase 1: Migration Files
Provide versioned SQL migration files:
```
infrastructure/postgres/migrations/
  001_event_store.up.sql        -- events + snapshots tables
  001_event_store.down.sql
  002_contracts_projection.up.sql
  002_contracts_projection.down.sql
  003_invoices.up.sql
  003_invoices.down.sql
  004_payments.up.sql
  004_payments.down.sql
  005_balance_entries.up.sql
  005_balance_entries.down.sql
  006_usage_records.up.sql
  006_usage_records.down.sql
```

### Phase 2: Reference Implementation (at minimum EventStore)
```
infrastructure/postgres/
  event_store.go           -- eventstore.Store implementation
  tx_manager.go            -- application/tx.TxManager implementation
  README.md                -- Setup and usage guide
```

The EventStore is the most critical piece — it's the foundation of the event sourcing architecture and the hardest for users to implement correctly (optimistic locking, subscription, snapshot management).

### Phase 3 (optional): Repository Implementations
```
infrastructure/postgres/
  contract_repository.go
  invoice_repository.go
  payment_repository.go
  balance_repository.go
  ...
```

## Acceptance Criteria

### Phase 1 (minimum viable)
- [ ] SQL migration files for all tables documented in event-sourcing.md
- [ ] Compatible with golang-migrate or similar tool
- [ ] Includes indexes for common query patterns
- [ ] README explaining how to run migrations

### Phase 2 (recommended)
- [ ] PostgreSQL `eventstore.Store` implementation with:
  - Optimistic locking via `UNIQUE (stream_id, version)`
  - Snapshot save/load with `LoadSnapshotBefore`
  - `Subscribe()` via PostgreSQL LISTEN/NOTIFY or polling
- [ ] `TxManager` implementation wrapping `database/sql` transactions
- [ ] Integration test using testcontainers-go or similar

### Phase 3 (nice to have)
- [ ] At least `contract_repository.go` as a reference for event-sourced aggregate persistence
- [ ] `balance_repository.go` demonstrating `SELECT FOR UPDATE` for credit consumption

## Alternatives Considered

- **Document-only approach** (current): Users implement from docs. High barrier, error-prone.
- **Code generation**: Generate repository implementations from interface definitions. Over-engineered for current stage.
- **Multiple DB support** (Postgres + MySQL + MongoDB): Scope too large. Start with PostgreSQL as the most common choice for billing systems.

## Impact

This is likely the **highest-impact improvement** for framework adoption. A working PostgreSQL EventStore + TxManager would reduce the "time to production" from weeks to days.
