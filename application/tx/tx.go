// Package tx provides transaction coordination for the billing engine.
//
// Library users implement TxManager to integrate with their database.
// The RunInTx closure receives a Repos struct whose repositories all
// participate in the same transaction. If the closure returns nil the
// transaction is committed; if it returns an error it is rolled back.
package tx

import (
	"context"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
)

// Repos groups repository interfaces that participate in a single transaction.
// Inside RunInTx, callers must use these repositories (not the service-level
// ones) to ensure all writes share the same transactional boundary.
type Repos struct {
	Contracts contract.Repository
	Invoices  invoice.Repository
	Payments  payment.Repository
	Credits   credit.Repository
}

// TxManager abstracts transaction lifecycle. Library users implement this
// for their specific database (e.g., *sql.DB, pgx pool, MongoDB session).
type TxManager interface {
	// RunInTx executes fn within a transaction.
	// The Repos passed to fn are scoped to the transaction.
	// If fn returns nil the transaction is committed.
	// If fn returns an error the transaction is rolled back.
	RunInTx(ctx context.Context, fn func(ctx context.Context, repos Repos) error) error
}

// NoopTxManager runs the closure without any transactional envelope.
// It passes through the pre-configured repositories as-is.
// Used by in-memory implementations and tests.
type NoopTxManager struct {
	R Repos
}

// RunInTx simply invokes fn with the pre-configured repositories.
func (m *NoopTxManager) RunInTx(ctx context.Context, fn func(ctx context.Context, repos Repos) error) error {
	return fn(ctx, m.R)
}
