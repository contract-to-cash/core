package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/tx"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/plugin"
)

// adyenMaxIdempotencyKeyLen is the strictest effective key length ceiling
// among the gateways documented in application/port/idempotency_store.go.
// Effective keys longer than this cannot round-trip through Adyen; the
// service logs a warning when a derived key exceeds this bound so operators
// can shorten their original keys before the retry attempt reaches the
// gateway and fails with an opaque validation error.
const adyenMaxIdempotencyKeyLen = 64

// newRetryEffectiveKey derives a fresh effective idempotency key by appending
// an opaque ULID suffix to the caller's original key. The suffix is not
// exposed as a public helper anywhere in the tree because effective-key
// generation is an implementation detail of [PaymentService] — callers must
// not derive their own retry keys.
//
// Key length note: Stripe permits up to 255 bytes, GMO PG up to 255 bytes,
// PayPal up to 255 bytes, but Adyen caps idempotency keys at 64 bytes. The
// suffix adds 27 bytes ("-" + 26-char ULID), so original keys longer than
// 37 bytes will overflow Adyen's limit. Consumers targeting Adyen must keep
// their original IdempotencyKey values short. PaymentService emits a
// warning log when the derived key exceeds adyenMaxIdempotencyKeyLen.
func newRetryEffectiveKey(originalKey string) string {
	suffix := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
	return originalKey + "-" + suffix
}

// ErrRequiresAction is returned when the payment gateway indicates that
// additional customer action is required (e.g., 3D Secure authentication).
// The returned *payment.Payment is in Pending status with the gateway
// transaction ID set. Callers should check for this error with errors.Is()
// and redirect the customer to complete authentication.
var ErrRequiresAction = errors.New("payment requires action")

// ProcessPaymentInput holds the parameters for processing a payment.
// PaymentMethodID is optional — if empty, the service resolves it via the
// hierarchical fallback chain: Invoice → Contract → Customer.
// PaymentMethod is the type of payment method (e.g. bank_transfer, convenience_store).
// If not set, it is resolved from ChargeResponse.PaymentMethodType, falling back
// to credit_card for backward compatibility.
type ProcessPaymentInput struct {
	PaymentMethodID string
	PaymentMethod   payment.PaymentMethod
	Amount          shared.Money
	Currency        shared.Currency
	IdempotencyKey  string
	Metadata        map[string]string
}

// RefundInput holds the parameters for issuing a refund.
type RefundInput struct {
	Amount *shared.Money
	Reason port.RefundReason
}

// PaymentServiceOption configures optional dependencies of PaymentService.
type PaymentServiceOption func(*PaymentService)

// WithPaymentLogger sets a structured logger for the PaymentService.
// If not provided, slog.Default() is used.
func WithPaymentLogger(l *slog.Logger) PaymentServiceOption {
	return func(s *PaymentService) {
		s.logger = l
	}
}

// WithCustomerGateway sets the customer gateway used for payment method resolution.
func WithCustomerGateway(gw port.CustomerGateway) PaymentServiceOption {
	return func(s *PaymentService) {
		s.customerGateway = gw
	}
}

// WithPaymentTxManager sets the transaction manager for the PaymentService.
// If not provided, a NoopTxManager is used (no transaction wrapping).
func WithPaymentTxManager(tm tx.TxManager) PaymentServiceOption {
	return func(s *PaymentService) {
		s.txManager = tm
	}
}

// WithIdempotencyStore wires an [port.IdempotencyStore] into the PaymentService.
//
// When provided, ProcessPayment tracks idempotency keys that have been burned
// by a saga compensation refund. On retry with a previously compensated key,
// ProcessPayment transparently derives a fresh key (by appending a ULID
// suffix) so the gateway sees a brand-new charge rather than an idempotent
// replay of the refunded one. This resolves the race described in issue #87.
//
// If this option is omitted, ProcessPayment behaves as it did before the fix:
// compensation markers are neither read nor written, and callers remain
// exposed to the issue #87 race. Production deployments should provide a
// store backed by the same durable datastore as the payment repository.
func WithIdempotencyStore(store port.IdempotencyStore) PaymentServiceOption {
	return func(s *PaymentService) {
		s.idempotencyStore = store
	}
}

// PaymentService orchestrates payment processing with plugin hooks.
type PaymentService struct {
	gateway          port.PaymentGateway
	paymentRepo      payment.Repository
	invoiceRepo      invoice.Repository
	contractRepo     contract.Repository
	customerGateway  port.CustomerGateway
	eventStore       eventstore.Store
	registry         *plugin.Registry
	clock            shared.Clock
	logger           *slog.Logger
	txManager        tx.TxManager
	idempotencyStore port.IdempotencyStore
}

// NewPaymentService creates a new PaymentService.
// Required dependencies are positional arguments; optional dependencies
// (logger, customer gateway) are provided via PaymentServiceOption.
func NewPaymentService(
	gateway port.PaymentGateway,
	paymentRepo payment.Repository,
	invoiceRepo invoice.Repository,
	contractRepo contract.Repository,
	eventStore eventstore.Store,
	registry *plugin.Registry,
	clock shared.Clock,
	opts ...PaymentServiceOption,
) *PaymentService {
	s := &PaymentService{
		gateway:      gateway,
		paymentRepo:  paymentRepo,
		invoiceRepo:  invoiceRepo,
		contractRepo: contractRepo,
		eventStore:   eventStore,
		registry:     registry,
		clock:        clock,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.txManager == nil {
		s.txManager = tx.NewNoopTxManager(tx.Repos{
			Payments: paymentRepo,
			Invoices: invoiceRepo,
		})
	}
	return s
}

// ProcessPayment charges an invoice and records the payment.
func (s *PaymentService) ProcessPayment(ctx context.Context, invoiceID shared.InvoiceID, input ProcessPaymentInput) (*payment.Payment, error) {
	// Load invoice
	inv, err := s.invoiceRepo.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load invoice: %w", err)
	}

	amount := input.Amount
	if amount.IsZero() {
		amount = inv.AmountDue()
	}

	// Validate payment amount against invoice BEFORE charging the gateway.
	// ValidatePayment is side-effect-free, so the invoice state is not modified.
	err = inv.ValidatePayment(amount)
	if err != nil {
		return nil, fmt.Errorf("payment validation failed: %w", err)
	}

	// Resolve payment method via fallback chain if not explicitly provided
	pmID := input.PaymentMethodID
	if pmID == "" {
		resolved, resolveErr := s.ResolvePaymentMethod(ctx, inv)
		if resolveErr != nil {
			return nil, fmt.Errorf("failed to resolve payment method: %w", resolveErr)
		}
		pmID = resolved
	}

	// Issue #87: consult the IdempotencyStore to see whether this key was
	// burned by a prior saga compensation. If so, use the stored effective
	// key so the gateway treats this call as a brand-new charge rather than
	// replaying the cached response of the now-refunded original. Empty keys
	// cannot be tracked (and cannot collide at the gateway either), so we
	// skip the lookup entirely in that case.
	//
	// The effective key is stored atomically on first compensation and
	// reused on every subsequent retry, so repeated retries of the same
	// original key converge on one stable effective key — preserving
	// end-to-end idempotency across multiple retry attempts.
	effectiveKey := input.IdempotencyKey
	if s.idempotencyStore != nil && input.IdempotencyKey != "" {
		stored, ok, checkErr := s.idempotencyStore.ResolveEffectiveKey(ctx, input.IdempotencyKey)
		if checkErr != nil {
			return nil, fmt.Errorf("idempotency store check failed: %w", checkErr)
		}
		if ok {
			effectiveKey = stored
			s.logger.Info("idempotency key was previously compensated, using stored effective key",
				"originalKey", input.IdempotencyKey,
				"effectiveKey", effectiveKey,
				"invoiceID", invoiceID,
			)
			// Symmetric length warning: a store populated by an earlier
			// run (or by a different PaymentService instance with a
			// longer key) may hand us an effective key that already
			// exceeds the strictest gateway limit. Without this check,
			// the Charge below fails with an opaque gateway validation
			// error and operators must dig through logs to diagnose.
			if len(effectiveKey) > adyenMaxIdempotencyKeyLen {
				s.logger.Warn("stored effective key exceeds Adyen's 64-byte limit; Adyen retries will fail until the key is shortened",
					"originalKey", input.IdempotencyKey,
					"effectiveKeyLen", len(effectiveKey),
					"maxAllowed", adyenMaxIdempotencyKeyLen,
					"invoiceID", invoiceID,
				)
			}
		}
	}

	// Pre-charge idempotency check. This mirrors the established 3DS branch
	// pattern (local state first, gateway second) and short-circuits BEFORE
	// hitting the gateway when an existing payment is found under the
	// effective key. Without this check, a retry colliding with a terminal
	// existing payment would:
	//
	//   1. Issue an avoidable gateway Charge (idempotent-replayed by the
	//      gateway, so wasted API quota and latency).
	//   2. Reach the in-tx terminal-state rejection, return an error from
	//      RunInTx, and trigger saga.Compensate → a SPURIOUS Refund against
	//      the same transaction (which is already refunded in the Refunded
	//      case, and never captured in the Failed case).
	//   3. Write a spurious IdempotencyStore marker, burning the caller's
	//      original key for all future retries even though nothing needs
	//      compensating.
	//
	// Doing the lookup here also means that Completed existing payments
	// return an idempotent replay with zero gateway cost — matching
	// Stripe/Adyen/PayPal best practices of "check local state first."
	//
	// IMPORTANT: this runs BEFORE BeforeCharge hooks. BeforeCharge is a
	// pre-flight contract ("about to charge the gateway") and must not
	// fire when the call short-circuits without touching the gateway.
	// Otherwise plugins that reserve inventory, emit audit events, or
	// increment counters would leak phantom "charge attempt" activity
	// into consumer systems.
	//
	// NOTE: this is a best-effort check outside the payment repo's
	// transaction scope. A concurrent in-tx Save could race with this
	// read; the canonical defense is the in-tx check in the RunInTx
	// closure further down, which continues to guard terminal states.
	if effectiveKey != "" {
		existing, findErr := s.paymentRepo.FindByIdempotencyKey(ctx, effectiveKey)
		if findErr != nil {
			return nil, fmt.Errorf("pre-charge idempotency check failed: %w", findErr)
		}
		if existing != nil {
			switch existing.Status() {
			case payment.PaymentStatusCompleted:
				// Idempotent replay of a successfully completed payment.
				// No gateway call, no saga, no marker, no BeforeCharge.
				return existing, nil
			case payment.PaymentStatusPending:
				// Pending record from a prior 3DS or retry — let the
				// normal flow (3DS branch or in-tx upgrade path) handle
				// it. We intentionally do NOT short-circuit here: the
				// gateway may return Captured (user finished 3DS) and
				// we need to upgrade the Pending record to Completed.
				// BeforeCharge WILL fire below because we are about to
				// (re-)charge the gateway.
			case payment.PaymentStatusFailed,
				payment.PaymentStatusRefunded,
				payment.PaymentStatusPartiallyRefunded,
				payment.PaymentStatusChargedBack:
				// Terminal states that MUST NOT be silently replayed as
				// success. Returning a Completed result here would let
				// the caller act on a payment that has already been
				// unwound. Returning ErrCodeConflict is the right signal:
				// "the key you sent collides with a terminal record."
				return nil, shared.NewDomainError(
					shared.ErrCodeConflict,
					fmt.Sprintf("cannot replay payment in terminal state %q (idempotency key %q)",
						existing.Status(), effectiveKey),
				)
			}
		}
	}

	// Build PaymentContext with invoice (payment is nil at this stage for BeforeCharge)
	payCtx := plugin.NewPaymentContext(ctx, nil, inv)

	// Execute BeforeCharge hooks. Order matters: this runs AFTER the
	// pre-charge idempotency short-circuit so plugins never see
	// phantom charge attempts for retries that will not touch the gateway.
	for _, hook := range s.registry.GetBeforeChargeHooks() {
		err = hook.BeforeCharge(payCtx, amount)
		if err != nil {
			return nil, fmt.Errorf("BeforeCharge hook error: %w", err)
		}
	}

	// Charge via gateway.
	// NOTE: Charge is called BEFORE the in-transaction idempotency check.
	// This relies on the gateway honouring IdempotencyKey to prevent duplicate
	// charges when the same request is retried (e.g. after a transient DB failure).
	chargeResp, err := s.gateway.Charge(ctx, &port.ChargeRequest{
		Amount:          amount,
		CustomerID:      string(inv.AccountID()),
		PaymentMethodID: &pmID,
		Description:     fmt.Sprintf("Invoice %s", invoiceID),
		Metadata:        input.Metadata,
		IdempotencyKey:  effectiveKey,
	})

	// No ChargeResponse available on failure — resolve from input only
	inputMethodType := s.resolvePaymentMethodType("", input.PaymentMethod)

	if err != nil {
		// Create and persist a failed payment record for tracking
		failedPayment := payment.NewPayment(
			shared.NewPaymentID(),
			invoiceID,
			amount,
			inputMethodType,
			"",
			s.clock.Now(),
		)
		if failErr := failedPayment.Fail(err.Error()); failErr == nil {
			// Best-effort save of failed payment record
			_ = s.paymentRepo.Save(ctx, failedPayment)
		}
		// Execute OnPaymentFailed hooks with PaymentContext
		failCtx := plugin.NewPaymentContext(ctx, failedPayment, inv)
		for _, hook := range s.registry.GetOnPaymentFailedHooks() {
			if hookErr := hook.OnPaymentFailed(failCtx, err); hookErr != nil {
				s.logger.Warn("OnPaymentFailed hook failed",
					"paymentID", failedPayment.ID(),
					"invoiceID", invoiceID,
					"error", hookErr,
				)
			}
		}
		return nil, fmt.Errorf("gateway charge failed: %w", err)
	}

	// Handle requires_action (3D Secure authentication pending).
	// The payment is not yet captured — save a pending record and return
	// so the caller can redirect the customer to the 3DS authentication page.
	// Unlike the failed-payment best-effort save above, this save is critical:
	// the gateway has an active authorization, and the 3DS callback will need
	// this record to complete the payment flow.
	//
	// The pending payment and its idempotency lookup must both key off the
	// EFFECTIVE key, not the original input key. Otherwise a retry after a
	// prior compensation (which has rewritten the effective key via the
	// IdempotencyStore) would save the pending record under the original
	// key while the gateway charge landed under the effective key, causing
	// subsequent success-path retries to miss the pending record.
	if chargeResp.Status == port.TransactionStatusRequiresAction {
		// Idempotency check: if a pending payment already exists for this key,
		// return it instead of creating a duplicate authorization.
		if effectiveKey != "" {
			existing, findErr := s.paymentRepo.FindByIdempotencyKey(ctx, effectiveKey)
			if findErr != nil {
				return nil, fmt.Errorf("idempotency check failed for requires_action: %w", findErr)
			}
			if existing != nil {
				return existing, fmt.Errorf("%w: 3D Secure authentication required (transaction %s)", ErrRequiresAction, existing.GatewayTransactionID())
			}
		}

		pendingPayment := payment.NewPayment(
			shared.NewPaymentID(),
			invoiceID,
			amount,
			s.resolvePaymentMethodType(chargeResp.PaymentMethodType, input.PaymentMethod),
			chargeResp.TransactionID,
			s.clock.Now(),
		)
		if effectiveKey != "" {
			pendingPayment.SetIdempotencyKey(effectiveKey)
		}
		if err := s.paymentRepo.Save(ctx, pendingPayment); err != nil {
			s.logger.Error("failed to save pending payment for 3DS (gateway authorization exists without internal record)",
				"transactionID", chargeResp.TransactionID,
				"invoiceID", invoiceID,
				"error", err,
			)
			return nil, fmt.Errorf("failed to save pending payment for 3DS (transaction %s): %w", chargeResp.TransactionID, err)
		}
		return pendingPayment, fmt.Errorf("%w: 3D Secure authentication required (transaction %s)", ErrRequiresAction, chargeResp.TransactionID)
	}

	// Guard: only Captured and Succeeded are valid success statuses.
	// Any other status (Pending, Failed, Canceled, Authorized, etc.) without an
	// error from the gateway is unexpected and must not proceed to the success path.
	switch chargeResp.Status {
	case port.TransactionStatusCaptured, port.TransactionStatusSucceeded:
		// proceed to success path
	default:
		return nil, fmt.Errorf("unexpected charge status %q for transaction %s", chargeResp.Status, chargeResp.TransactionID)
	}

	// Phase 2: Saga compensation for gateway charge.
	// Charge is authorize+capture (one-step), so the transaction is already
	// captured. Void only works on pre-capture authorizations; we must use
	// Refund to reverse a captured charge.
	//
	// Load-bearing invariant: the compensation Refund's IdempotencyKey is
	// DETERMINISTIC on chargeResp.TransactionID, not a fresh UUID. This is
	// what makes concurrent compensations safe: if two goroutines both
	// trigger compensation for the same original charge (e.g. two parallel
	// ProcessPayment calls that both hit the gateway's idempotent replay
	// of a successful charge), they both derive the same refund key and
	// the gateway deduplicates the second Refund automatically. Changing
	// this key to something non-deterministic (a UUID, a timestamp, etc.)
	// would silently reintroduce double-refund risk under concurrency.
	saga := tx.NewSaga()
	saga.AddCompensation(func(compCtx context.Context) error {
		_, refundErr := s.gateway.Refund(compCtx, &port.RefundRequest{
			TransactionID:  chargeResp.TransactionID,
			Amount:         &chargeResp.Amount,
			Reason:         port.RefundReasonOther,
			IdempotencyKey: "comp-refund-" + chargeResp.TransactionID,
		})
		return refundErr
	})

	// Create payment record. The payment's idempotency key is the EFFECTIVE
	// key (which equals input.IdempotencyKey in the normal case, or the
	// stored post-compensation replacement if this is a retry after a burned
	// key). Using the effective key keeps the in-transaction idempotency
	// check below consistent with the gateway-facing key.
	//
	// This fresh allocation is used only when the in-tx lookup below finds
	// NO existing payment under the effective key. If an existing record is
	// found, the closure reassigns p to the existing instance (Pending →
	// Completed upgrade path or straight idempotent replay) and the fresh
	// ID here is discarded. This is cheap and keeps the happy path simple.
	p := payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		amount,
		s.resolvePaymentMethodType(chargeResp.PaymentMethodType, input.PaymentMethod),
		chargeResp.TransactionID,
		s.clock.Now(),
	)
	if effectiveKey != "" {
		p.SetIdempotencyKey(effectiveKey)
	}

	// Phase 3: All state mutations and local writes are atomic within a transaction.
	// p.Complete() and inv.RecordPayment() are inside RunInTx so that if the
	// transaction fails, saga.Compensate() fires (no early return before it).
	// Note: if RunInTx executes the closure but then rolls back the DB, the
	// in-memory state of p and inv will remain mutated. This is acceptable
	// because the caller returns an error and does not reuse these objects.
	err = s.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		// Idempotency check first: avoid mutating in-memory state if a
		// payment with the effective key was already persisted by a prior call.
		//
		// When the existing payment is in Pending state (i.e. it was saved
		// during a prior 3DS/requires_action flow) and the gateway has now
		// returned a terminal success status, we must upgrade the existing
		// record to Completed and record the payment on the invoice — not
		// silently return the pending record as if it were the terminal
		// result. Stripe PaymentIntents, Adyen, and Braintree all follow
		// this pattern: the same idempotency key returns `requires_action`
		// on the first call and then `captured`/`succeeded` on the retry
		// after the customer finishes 3DS.
		if effectiveKey != "" {
			existing, findErr := repos.Payments.FindByIdempotencyKey(txCtx, effectiveKey)
			if findErr != nil {
				return fmt.Errorf("idempotency check failed: %w", findErr)
			}
			if existing != nil {
				// Dispatch on the existing payment's state. Only Pending
				// (upgrade to Completed) and Completed (idempotent replay)
				// reach the in-tx path in practice: the pre-charge
				// lookup above short-circuits terminal states before
				// gateway.Charge runs. The terminal branches here are
				// the defence-in-depth against a race between the
				// pre-charge read and a concurrent writer.
				//
				// All PaymentStatus values are listed explicitly (no
				// default) so that `exhaustive` lint catches new states
				// introduced in domain/payment/entity.go.
				switch existing.Status() {
				case payment.PaymentStatusPending:
					// Pending → Completed upgrade path (e.g. 3DS finished).
					if completeErr := existing.Complete(); completeErr != nil {
						return fmt.Errorf("failed to upgrade pending payment to completed: %w", completeErr)
					}
					if recordErr := inv.RecordPayment(amount, s.clock.Now()); recordErr != nil {
						return fmt.Errorf("failed to record payment on invoice: %w", recordErr)
					}
					if saveErr := repos.Payments.Save(txCtx, existing); saveErr != nil {
						return fmt.Errorf("failed to save upgraded payment: %w", saveErr)
					}
					if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
						return fmt.Errorf("failed to save invoice after payment upgrade: %w", saveErr)
					}
					p = existing
					return nil
				case payment.PaymentStatusCompleted:
					// Idempotent replay of a successfully completed payment.
					p = existing
					return nil
				case payment.PaymentStatusFailed,
					payment.PaymentStatusRefunded,
					payment.PaymentStatusPartiallyRefunded,
					payment.PaymentStatusChargedBack:
					// Terminal states that must not be silently replayed.
					// Pre-charge lookup normally catches these; only a
					// rare read-then-write race between the pre-charge
					// read and this in-tx read reaches here.
					return shared.NewDomainError(
						shared.ErrCodeConflict,
						fmt.Sprintf("cannot replay payment in terminal state %q (idempotency key %q)",
							existing.Status(), effectiveKey),
					)
				}
			}
		}

		if completeErr := p.Complete(); completeErr != nil {
			return fmt.Errorf("failed to complete payment: %w", completeErr)
		}
		if recordErr := inv.RecordPayment(amount, s.clock.Now()); recordErr != nil {
			return fmt.Errorf("failed to record payment on invoice: %w", recordErr)
		}

		if saveErr := repos.Payments.Save(txCtx, p); saveErr != nil {
			return fmt.Errorf("failed to save payment: %w", saveErr)
		}
		if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
			return fmt.Errorf("failed to save invoice after payment: %w", saveErr)
		}
		return nil
	})
	if err != nil {
		// Local save failed — compensate by refunding the gateway charge
		if compErr := saga.Compensate(ctx); compErr != nil {
			s.logger.Error("local save failed and compensation also failed (MANUAL RECONCILIATION REQUIRED)",
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
				"saveError", err,
				"compensationError", compErr,
			)
			return nil, fmt.Errorf("local save failed: %w; compensation also failed: %v", err, compErr)
		}

		// Issue #87: record the compensation in the idempotency store so
		// that subsequent retries with the same original key are routed to a
		// fresh effective key, avoiding an idempotent replay of the refunded
		// charge at the gateway. MarkCompensated failures are logged but not
		// returned — the caller already sees the save failure, and surfacing
		// a second error would obscure the primary cause. The downside is
		// that a subsequent retry may hit the same race; operators should
		// monitor this log line and investigate store health.
		if s.idempotencyStore != nil && input.IdempotencyKey != "" {
			newEffectiveKey := newRetryEffectiveKey(input.IdempotencyKey)
			if len(newEffectiveKey) > adyenMaxIdempotencyKeyLen {
				// The derived key exceeds the strictest gateway limit
				// (Adyen = 64 bytes). Retries against Adyen will fail at
				// the gateway with an opaque validation error. Log a
				// warning so operators can shorten the caller-supplied
				// original key. The marker is still written so non-Adyen
				// deployments can proceed.
				s.logger.Warn("derived effective key exceeds Adyen's 64-byte limit; Adyen retries will fail until the original key is shortened",
					"originalKey", input.IdempotencyKey,
					"effectiveKeyLen", len(newEffectiveKey),
					"maxAllowed", adyenMaxIdempotencyKeyLen,
					"invoiceID", invoiceID,
				)
			}
			if markErr := s.idempotencyStore.MarkCompensated(ctx, input.IdempotencyKey, newEffectiveKey); markErr != nil {
				s.logger.Error("failed to mark idempotency key as compensated (retry may race — see issue #87)",
					"originalKey", input.IdempotencyKey,
					"invoiceID", invoiceID,
					"error", markErr,
				)
			}
		}

		return nil, fmt.Errorf("local save failed (gateway charge refunded): %w", err)
	}

	// Phase 4: AfterCharge hooks (non-fatal, outside transaction).
	// When the idempotency path returned an existing payment, these hooks
	// still fire. Hook implementations should be idempotent.
	successCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetAfterChargeHooks() {
		if hookErr := hook.AfterCharge(successCtx); hookErr != nil {
			s.logger.Warn("AfterCharge hook failed",
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
				"error", hookErr,
			)
		}
	}

	return p, nil
}

// Refund processes a refund for a payment.
func (s *PaymentService) Refund(ctx context.Context, paymentID shared.PaymentID, input RefundInput) error {
	// Load payment
	p, err := s.paymentRepo.FindByID(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("failed to load payment: %w", err)
	}

	refundReq := &port.RefundRequest{
		TransactionID: p.GatewayTransactionID(),
		Amount:        input.Amount,
		Reason:        input.Reason,
	}

	_, err = s.gateway.Refund(ctx, refundReq)
	if err != nil {
		return fmt.Errorf("gateway refund failed: %w", err)
	}

	// Determine refund amount: if not specified, refund the remaining unrefunded amount.
	var refundAmount shared.Money
	if input.Amount != nil {
		refundAmount = *input.Amount
	} else {
		remaining, subErr := p.Amount().Subtract(p.RefundedAmount())
		if subErr != nil {
			return fmt.Errorf("failed to calculate remaining refund amount: %w", subErr)
		}
		refundAmount = remaining
	}

	// Phase 3: local save in transaction
	err = s.txManager.RunInTx(ctx, func(txCtx context.Context, repos tx.Repos) error {
		if refundErr := p.RecordRefund(refundAmount); refundErr != nil {
			return refundErr
		}
		return repos.Payments.Save(txCtx, p)
	})
	if err != nil {
		// Gateway refund succeeded but local save failed — this requires manual reconciliation.
		// Refunds cannot be reversed, so we log at Error level.
		s.logger.Error("local save failed after gateway refund (MANUAL RECONCILIATION REQUIRED)",
			"paymentID", paymentID,
			"refundAmount", refundAmount,
			"error", err,
		)
		return fmt.Errorf("local save failed after gateway refund (MANUAL RECONCILIATION REQUIRED): %w", err)
	}

	// Phase 4: post-commit hooks (non-fatal)
	inv, invErr := s.invoiceRepo.FindByID(ctx, p.InvoiceID())
	if invErr != nil {
		s.logger.Warn("invoice lookup failed on refund",
			"paymentID", paymentID,
			"invoiceID", p.InvoiceID(),
			"error", invErr,
		)
		inv = nil
	}

	refundCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetOnRefundHooks() {
		if hookErr := hook.OnRefund(refundCtx, refundAmount); hookErr != nil {
			s.logger.Warn("OnRefund hook failed",
				"paymentID", paymentID,
				"error", hookErr,
			)
		}
	}

	return nil
}

// ResolvePaymentMethod walks the hierarchical fallback chain to determine
// which payment method to charge: Invoice → Contract → Customer.
func (s *PaymentService) ResolvePaymentMethod(ctx context.Context, inv *invoice.Invoice) (string, error) {
	// Level 1: Invoice-level override
	if inv.PaymentMethodID() != nil && *inv.PaymentMethodID() != "" {
		return *inv.PaymentMethodID(), nil
	}

	// Level 2: Contract-level default
	if s.contractRepo != nil {
		agg, err := s.contractRepo.FindByID(ctx, inv.ContractID())
		if err != nil {
			return "", fmt.Errorf("failed to load contract for payment method resolution: %w", err)
		}
		if agg.PaymentMethodID() != nil && *agg.PaymentMethodID() != "" {
			return *agg.PaymentMethodID(), nil
		}

		// Level 3: Customer-level default (via gateway)
		if s.customerGateway != nil {
			customer, err := s.customerGateway.GetCustomer(ctx, string(agg.AccountID()))
			if err != nil {
				return "", fmt.Errorf("failed to load customer for payment method resolution: %w", err)
			}
			if customer.DefaultPaymentMethodID != nil && *customer.DefaultPaymentMethodID != "" {
				return *customer.DefaultPaymentMethodID, nil
			}
		}
	}

	return "", shared.NewDomainError(shared.ErrCodeBusinessRule,
		fmt.Sprintf("no payment method found for invoice %s: checked invoice, contract %s, and customer default",
			inv.ID(), inv.ContractID()))
}

// resolvePaymentMethodType determines the payment method type to record on
// a Payment entity. The resolution order is:
//  1. ChargeResponse.PaymentMethodType (gateway knows the actual method used)
//  2. ProcessPaymentInput.PaymentMethod (caller-specified)
//  3. Default: credit_card (backward compatibility)
func (s *PaymentService) resolvePaymentMethodType(chargeMethodType port.PaymentMethodType, inputMethod payment.PaymentMethod) payment.PaymentMethod {
	if chargeMethodType != "" {
		method, known := portMethodToPaymentMethod(chargeMethodType)
		if !known {
			s.logger.Warn("unknown gateway payment method type, falling back to credit_card",
				"gatewayMethodType", chargeMethodType,
			)
		}
		return method
	}
	if inputMethod != "" {
		return inputMethod
	}
	return payment.PaymentMethodCreditCard
}

// portMethodToPaymentMethod converts a port.PaymentMethodType to a payment.PaymentMethod.
// The second return value indicates whether the type was recognized.
func portMethodToPaymentMethod(pmt port.PaymentMethodType) (payment.PaymentMethod, bool) {
	switch pmt {
	case port.PaymentMethodTypeCreditCard:
		return payment.PaymentMethodCreditCard, true
	case port.PaymentMethodTypeDebitCard:
		return payment.PaymentMethodDebitCard, true
	case port.PaymentMethodTypeBankTransfer:
		return payment.PaymentMethodBankTransfer, true
	case port.PaymentMethodTypeConvenienceStore:
		return payment.PaymentMethodConvenience, true
	case port.PaymentMethodTypeQRCode:
		return payment.PaymentMethodQRCode, true
	case port.PaymentMethodTypeDirectDebit:
		return payment.PaymentMethodDirectDebit, true
	case port.PaymentMethodTypeCarrier:
		return payment.PaymentMethodCarrier, true
	case port.PaymentMethodTypePostpay:
		return payment.PaymentMethodPostpay, true
	default:
		// Unknown gateway payment method types fall back to credit_card.
		// If a new PaymentMethodType is added to port/, add a case here.
		return payment.PaymentMethodCreditCard, false
	}
}
