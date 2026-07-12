// Package tx provides transaction management interfaces for the billing engine.
//
// The core abstraction is TxManager, which runs a closure with transaction-scoped
// repositories. Library users implement TxManager for their database; the library
// provides NoopTxManager for in-memory/testing use.
package tx

import (
	"context"
	"errors"
	"log/slog"

	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
)

// ErrVersionConflict is returned when an optimistic lock conflict is detected.
// RetryOnConflict will retry on this error.
//
// Two encodings of an optimistic-lock conflict coexist in the codebase and both
// are recognised by RetryOnConflict / IsVersionConflict:
//   - this sentinel, returned by the state-stored repositories
//     (invoice / balance / credit-note optimistic-locking Saves); and
//   - a *shared.DomainError with Code == shared.ErrCodeVersionConflict, returned
//     by the event store's Append when the expected stream version does not
//     match (see infrastructure/inmemory/event_store.go). The event-sourced
//     contract repository surfaces that DomainError directly from Save.
//
// The two cannot be unified by making the DomainError Unwrap() to this sentinel:
// shared lives in the domain layer and must not import application/tx (the
// dependency rule points inward). Instead tx — which already depends on shared —
// matches the DomainError by its code. See IsVersionConflict.
var ErrVersionConflict = errors.New("version conflict")

// Repos holds transaction-scoped repositories for write operations.
// Read-only repositories (pricing, product, usage) are excluded — they have
// no writes to transact and reads complete outside the transaction.
type Repos struct {
	Contracts   contract.Repository
	Invoices    invoice.Repository
	Payments    payment.Repository
	Balances    balance.Repository
	CreditNotes invoice.CreditNoteRepository
}

// TxManager manages transaction boundaries.
// fn receives repositories scoped to a single transaction.
// nil return from fn → commit, error return → rollback.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, repos Repos) error) error
}

// NoopTxManager is a TxManager that executes the closure without transaction
// wrapping. Used by in-memory implementations and tests.
//
// A NoopTxManager provides NO atomicity: if a multi-write flow fails partway
// (e.g. the billing pipeline consumes credit ledger entries and then the invoice
// Save fails), earlier writes are NOT rolled back and the data is left corrupt.
// Production deployments that wire real database repositories MUST supply a real
// TxManager. Because omitting one leaves the code compiling and passing
// happy-path tests, write-side services and batch processors emit a Warn-level
// log at construction when they fall back to a default NoopTxManager
// (see WarnIfDefaultNoop). Intentional in-memory/test/demo usage should opt in
// via NewNoopTxManagerExplicit (or a service's WithoutTransactions option) to
// acknowledge the trade-off and suppress that warning.
type NoopTxManager struct {
	r Repos
	// explicit records that this noop manager was chosen deliberately (via
	// NewNoopTxManagerExplicit), which suppresses the non-atomic warning that
	// WarnIfDefaultNoop would otherwise emit for a silently-defaulted noop.
	explicit bool
}

// NewNoopTxManager creates a NoopTxManager that passes the given repos through.
//
// This is the DEFAULT, non-explicit variant: a write-side service or batch
// processor that falls back to it because no TxManager was wired will warn via
// WarnIfDefaultNoop. For intentional in-memory/test/demo usage that should not
// warn, use NewNoopTxManagerExplicit.
func NewNoopTxManager(r Repos) *NoopTxManager {
	return &NoopTxManager{r: r}
}

// NewNoopTxManagerExplicit creates a NoopTxManager marked as a deliberate,
// acknowledged choice. It behaves identically to NewNoopTxManager at runtime but
// suppresses the non-atomic warning emitted by WarnIfDefaultNoop. Use it in
// in-memory demos, examples, and tests where the absence of real transactions is
// intentional and understood.
func NewNoopTxManagerExplicit(r Repos) *NoopTxManager {
	return &NoopTxManager{r: r, explicit: true}
}

// IsNoop reports whether m is a NoopTxManager — i.e. provides no transactional
// atomicity guarantees. Callers use this (rather than fragile string or
// reflection checks) to detect a missing real transaction manager.
func IsNoop(m TxManager) bool {
	_, ok := m.(*NoopTxManager)
	return ok
}

// IsExplicitNoop reports whether m is a NoopTxManager that was constructed via
// NewNoopTxManagerExplicit (a deliberate opt-in). It is false for the default
// NewNoopTxManager, which is what a service falls back to when no TxManager was
// wired.
func IsExplicitNoop(m TxManager) bool {
	n, ok := m.(*NoopTxManager)
	return ok && n.explicit
}

// WarnIfDefaultNoop emits a single Warn-level log if txm is a default
// (non-explicit) NoopTxManager, signalling that multi-write operations will run
// without atomicity. Write-side services and batch processors call this ONCE at
// construction so a consumer who wires real repositories but forgets the
// TxManager option gets a loud runtime signal instead of silent corruption under
// partial failure.
//
// component names the constructing service/processor; remedy is a short hint
// naming the concrete option or argument to wire (e.g.
// "wire WithBillingTxManager(...)"). Explicit noop managers
// (NewNoopTxManagerExplicit) and real TxManagers produce no log.
func WarnIfDefaultNoop(logger *slog.Logger, txm TxManager, component, remedy string) {
	if logger == nil {
		logger = slog.Default()
	}
	if IsNoop(txm) && !IsExplicitNoop(txm) {
		logger.Warn(
			"running without a transaction manager: multi-write operations are NOT atomic and can corrupt data under partial failure",
			"component", component,
			"remedy", remedy,
		)
	}
}

// RunInTx calls fn directly with the stored repos (no transaction).
func (m *NoopTxManager) RunInTx(ctx context.Context, fn func(context.Context, Repos) error) error {
	return fn(ctx, m.r)
}

// Repos exposes the manager's static repositories so a joined nested Run can
// supplement repos the outer transaction did not provide. Implements reposProvider.
func (m *NoopTxManager) Repos() Repos {
	return m.r
}

// txReposKey is the context key under which an in-progress transaction's repos
// are stored, enabling nested Run calls to join the outer transaction.
type txReposKey struct{}

// reposProvider is optionally implemented by a TxManager (e.g. NoopTxManager)
// that can expose a static set of repositories. It lets a JOINED nested Run
// supplement repo handles the outer transaction did not provide (see Run).
type reposProvider interface {
	Repos() Repos
}

// Run executes fn within a transaction managed by mgr.
//
// If ctx already carries an in-progress transaction (because an outer Run is
// active), fn JOINS that transaction using the existing transaction-scoped
// repos instead of opening a new, independent one. This prevents two separate
// transactions when one transactional service calls another (e.g.
// CreditNoteService.ReissueInvoice invoking BillingService.GenerateInvoice) —
// see review #4. In that nested case mgr.RunInTx is not called.
//
// When joining, any repo the outer transaction left nil is filled from mgr's own
// repos if mgr implements reposProvider (the in-memory NoopTxManager does). This
// keeps a nested inner service (e.g. credit/balance application in the billing
// pipeline) functional even when the outer service's manager only wired a subset
// of repos. A real database TxManager yields a complete, transaction-scoped Repos
// set from RunInTx, so the outer repos are already complete and no fill occurs.
//
// IMPORTANT: all services that share a propagated ctx must target the SAME
// transactional backend. Run joins whatever transaction is stamped on ctx
// regardless of which manager started it; mixing two independent backends under
// one ctx would silently route the inner writes through the outer backend.
func Run(ctx context.Context, mgr TxManager, fn func(ctx context.Context, repos Repos) error) error {
	if existing, ok := reposFromContext(ctx); ok {
		// Already inside a transaction — join it, filling any missing repos.
		if rp, ok := mgr.(reposProvider); ok {
			existing = existing.withFallback(rp.Repos())
		}
		return fn(ctx, existing)
	}
	return mgr.RunInTx(ctx, func(txCtx context.Context, repos Repos) error {
		return fn(withRepos(txCtx, repos), repos)
	})
}

// withFallback returns a copy of r with any nil repo filled from fb.
func (r Repos) withFallback(fb Repos) Repos {
	if r.Contracts == nil {
		r.Contracts = fb.Contracts
	}
	if r.Invoices == nil {
		r.Invoices = fb.Invoices
	}
	if r.Payments == nil {
		r.Payments = fb.Payments
	}
	if r.Balances == nil {
		r.Balances = fb.Balances
	}
	if r.CreditNotes == nil {
		r.CreditNotes = fb.CreditNotes
	}
	return r
}

// withRepos stamps the transaction-scoped repos onto ctx so nested Run calls can
// detect and join the active transaction.
func withRepos(ctx context.Context, repos Repos) context.Context {
	return context.WithValue(ctx, txReposKey{}, repos)
}

// reposFromContext returns the active transaction's repos, if any.
func reposFromContext(ctx context.Context) (Repos, bool) {
	r, ok := ctx.Value(txReposKey{}).(Repos)
	return r, ok
}

// ReposFromContext exposes the active transaction's transaction-scoped repos, if
// any. Services that perform READS inside a transaction (e.g. duplicate checks or
// loading an aggregate that a caller mutated earlier in the same transaction)
// must route those reads through these repos rather than their own field repos:
// on a real DB the field repos run on a separate connection and cannot see the
// transaction's uncommitted writes. Returns false when no transaction is active,
// in which case callers fall back to their field repos.
func ReposFromContext(ctx context.Context) (Repos, bool) {
	return reposFromContext(ctx)
}

// InTransaction reports whether ctx carries an in-progress transaction started
// by an outer Run — i.e. a nested Run on this ctx would JOIN the caller's
// transaction instead of opening a new one. Use it when the intent is only the
// yes/no probe, not access to the transaction-scoped repos (use
// ReposFromContext for that). Services use it to detect "joined" mode, where a
// failed statement may have left the CALLER-OWNED ambient transaction aborted
// (e.g. Postgres 25P02), making further reads on ctx unsafe until the caller
// rolls back (see PaymentService's duplicate-key convergence, issue #233).
func InTransaction(ctx context.Context) bool {
	_, ok := reposFromContext(ctx)
	return ok
}

// IsVersionConflict reports whether err represents an optimistic-lock conflict,
// regardless of which of the two encodings it uses: the ErrVersionConflict
// sentinel (state-stored repositories) or a *shared.DomainError carrying
// shared.ErrCodeVersionConflict (the event store's version check, surfaced by
// the event-sourced contract repository's Save). Both are treated as retriable
// so a contract-save conflict wrapped in RetryOnConflict is retried the same way
// an invoice-save conflict is.
func IsVersionConflict(err error) bool {
	if errors.Is(err, ErrVersionConflict) {
		return true
	}
	var de *shared.DomainError
	if errors.As(err, &de) && de.Code == shared.ErrCodeVersionConflict {
		return true
	}
	return false
}

// RetryOnConflict runs fn up to maxAttempts times (the FIRST call plus retries),
// retrying only when fn returns an optimistic-lock conflict (see
// IsVersionConflict — either the ErrVersionConflict sentinel or a
// shared.ErrCodeVersionConflict DomainError). Non-conflict errors are returned
// immediately without retry. No backoff is applied — optimistic lock conflicts
// resolve on immediate retry.
//
// The parameter is the total attempt COUNT, not the number of retries after the
// first call: maxAttempts=1 runs fn exactly once with no retry, maxAttempts=3
// runs it at most three times. It was renamed from the misleading maxRetries to
// match the loop's actual semantics (issue #162 L4).
//
// A maxAttempts <= 0 is clamped to 1, so fn always runs at least once. Returning
// nil without ever invoking fn (the previous behaviour) is a silent no-op that
// reports success while doing no work — a subtle correctness hazard; running once
// is the least-surprise behaviour (issue #187).
func RetryOnConflict(maxAttempts int, fn func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !IsVersionConflict(err) {
			return err
		}
	}
	return err
}
