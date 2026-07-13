package inmemory

import (
	"context"
	"sync"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
)

// Compile-time interface checks: the collecting writer implements BOTH outbox
// writer ports, so a single instance can be wired into PaymentService and
// BillingService in tests.
var (
	_ port.PaymentOutboxWriter = (*InMemoryOutboxWriter)(nil)
	_ port.InvoiceOutboxWriter = (*InMemoryOutboxWriter)(nil)
)

// PaymentOutboxEntry records one OnPaymentRecorded invocation.
type PaymentOutboxEntry struct {
	Payment *payment.Payment
	Invoice *invoice.Invoice
}

// InvoiceOutboxEntry records one OnInvoiceFinalized invocation.
type InvoiceOutboxEntry struct {
	Invoice *invoice.Invoice
}

// InMemoryOutboxWriter is a thread-safe, collecting test double implementing
// both [port.PaymentOutboxWriter] and [port.InvoiceOutboxWriter] (issue #248).
// It records every invocation so tests can assert firing/skip behaviour, and
// supports error injection to exercise the veto → rollback (and, on the payment
// gateway path, saga compensation) semantics.
//
// It is intended for tests, examples, and single-node development. A production
// writer would instead piggy-back an INSERT onto the transaction carried on the
// ctx (see [port.PaymentOutboxWriter]); this double ignores the transaction and
// simply appends to in-memory slices.
type InMemoryOutboxWriter struct {
	mu sync.Mutex

	paymentEntries []PaymentOutboxEntry
	invoiceEntries []InvoiceOutboxEntry

	// err, when non-nil, is returned from every call (both ports).
	err error
	// failNext, when > 0, makes the next failNext calls (across both ports)
	// return failErr, decrementing each time; subsequent calls succeed. It is
	// independent of err (err always fails).
	failNext int
	failErr  error
	// panicNext, when true, makes the next call (either port) panic instead of
	// returning, to exercise the SafeInvoke panic-isolation path. It is reset
	// after firing once.
	panicNext bool
	panicVal  any
}

// NewInMemoryOutboxWriter creates an empty collecting writer.
func NewInMemoryOutboxWriter() *InMemoryOutboxWriter {
	return &InMemoryOutboxWriter{}
}

// FailWith makes every subsequent call (both ports) return err. Passing nil
// clears the sticky failure.
func (w *InMemoryOutboxWriter) FailWith(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}

// FailNext makes the next n calls (across both ports) return err before
// succeeding again.
func (w *InMemoryOutboxWriter) FailNext(n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failNext = n
	w.failErr = err
}

// PanicNext makes the next call (either port) panic with val, to exercise the
// SafeInvoke panic-isolation path. It fires once and then resets.
func (w *InMemoryOutboxWriter) PanicNext(val any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.panicNext = true
	w.panicVal = val
}

// nextErr evaluates the configured failure policy under the lock. Caller must
// hold w.mu. It also honours a pending panic request.
func (w *InMemoryOutboxWriter) nextErr() error {
	if w.panicNext {
		w.panicNext = false
		panic(w.panicVal)
	}
	if w.err != nil {
		return w.err
	}
	if w.failNext > 0 {
		w.failNext--
		return w.failErr
	}
	return nil
}

// OnPaymentRecorded records the (payment, invoice) pair unless a failure/panic
// is configured, in which case nothing is recorded and the error is returned
// (or a panic is raised).
func (w *InMemoryOutboxWriter) OnPaymentRecorded(_ context.Context, p *payment.Payment, inv *invoice.Invoice) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.nextErr(); err != nil {
		return err
	}
	w.paymentEntries = append(w.paymentEntries, PaymentOutboxEntry{Payment: p, Invoice: inv})
	return nil
}

// OnInvoiceFinalized records the finalized invoice unless a failure/panic is
// configured.
func (w *InMemoryOutboxWriter) OnInvoiceFinalized(_ context.Context, inv *invoice.Invoice) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.nextErr(); err != nil {
		return err
	}
	w.invoiceEntries = append(w.invoiceEntries, InvoiceOutboxEntry{Invoice: inv})
	return nil
}

// PaymentEntries returns a copy of the recorded payment invocations.
func (w *InMemoryOutboxWriter) PaymentEntries() []PaymentOutboxEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]PaymentOutboxEntry, len(w.paymentEntries))
	copy(out, w.paymentEntries)
	return out
}

// InvoiceEntries returns a copy of the recorded invoice invocations.
func (w *InMemoryOutboxWriter) InvoiceEntries() []InvoiceOutboxEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]InvoiceOutboxEntry, len(w.invoiceEntries))
	copy(out, w.invoiceEntries)
	return out
}

// PaymentCount returns how many times OnPaymentRecorded successfully recorded.
func (w *InMemoryOutboxWriter) PaymentCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.paymentEntries)
}

// InvoiceCount returns how many times OnInvoiceFinalized successfully recorded.
func (w *InMemoryOutboxWriter) InvoiceCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.invoiceEntries)
}
