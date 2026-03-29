// Package tx provides transaction management interfaces for the billing engine.
//
// The core abstraction is TxManager, which runs a closure with transaction-scoped
// repositories. Library users implement TxManager for their database; the library
// provides NoopTxManager for in-memory/testing use.
package tx

import (
	"context"
	"errors"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/credit"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
)

// ErrVersionConflict is returned when an optimistic lock conflict is detected.
// RetryOnConflict will retry on this error.
var ErrVersionConflict = errors.New("version conflict")

// Repos holds transaction-scoped repositories for write operations.
// Read-only repositories (pricing, product, usage) are excluded — they have
// no writes to transact and reads complete outside the transaction.
type Repos struct {
	Contracts contract.Repository
	Invoices  invoice.Repository
	Payments  payment.Repository
	Credits   credit.Repository
}

// TxManager manages transaction boundaries.
// fn receives repositories scoped to a single transaction.
// nil return from fn → commit, error return → rollback.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, repos Repos) error) error
}

// NoopTxManager is a TxManager that executes the closure without transaction
// wrapping. Used by in-memory implementations and tests.
type NoopTxManager struct {
	r Repos
}

// NewNoopTxManager creates a NoopTxManager that passes the given repos through.
func NewNoopTxManager(r Repos) *NoopTxManager {
	return &NoopTxManager{r: r}
}

// RunInTx calls fn directly with the stored repos (no transaction).
func (m *NoopTxManager) RunInTx(ctx context.Context, fn func(context.Context, Repos) error) error {
	return fn(ctx, m.r)
}

// RetryOnConflict retries fn up to maxRetries times when ErrVersionConflict
// is returned. Non-conflict errors are returned immediately without retry.
// No backoff is applied — optimistic lock conflicts resolve on immediate retry.
func RetryOnConflict(maxRetries int, fn func() error) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrVersionConflict) {
			return err
		}
	}
	return err
}
