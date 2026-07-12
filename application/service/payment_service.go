package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

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

// paymentMaxRetries bounds the optimistic-lock retry loop around the payment
// bookkeeping transaction (issue #190). It mirrors creditNoteMaxRetries: a
// small number of attempts is enough because a version conflict means a
// concurrent writer already committed, and the re-read either converges or is
// rejected by a domain guard (e.g. RecordRefund's over-refund check) — neither
// of which benefits from many retries.
const paymentMaxRetries = 3

// newRetryEffectiveKey derives a fresh effective idempotency key by appending
// an opaque ULID suffix to the caller's original key. The suffix is not
// exposed as a public helper anywhere in the tree because effective-key
// generation is an implementation detail of [PaymentService] — callers must
// not derive their own retry keys.
//
// The ULID timestamp is sourced from the service's injected [shared.Clock],
// not time.Now(), so tests can reproduce effective keys deterministically
// and so the CLAUDE.md "no direct time.Now()" rule is respected throughout
// application code. The entropy source is crypto/rand.
//
// Key length note: Stripe permits up to 255 bytes, GMO PG up to 255 bytes,
// PayPal up to 255 bytes, but Adyen caps idempotency keys at 64 bytes. The
// suffix adds 27 bytes ("-" + 26-char ULID), so original keys longer than
// 37 bytes will overflow Adyen's limit. Consumers targeting Adyen must keep
// their original IdempotencyKey values short. PaymentService emits a
// warning log when the derived key exceeds adyenMaxIdempotencyKeyLen.
func (s *PaymentService) newRetryEffectiveKey(originalKey string) string {
	suffix := ulid.MustNew(ulid.Timestamp(s.clock.Now()), rand.Reader).String()
	return originalKey + "-" + suffix
}

// ErrRequiresAction is returned when the payment gateway indicates that
// additional customer action is required (e.g., 3D Secure authentication).
// The returned *payment.Payment is in Pending status with the gateway
// transaction ID set. Callers should check for this error with errors.Is()
// and redirect the customer to complete authentication.
var ErrRequiresAction = errors.New("payment requires action")

// errDuplicateKeyRaceSignal is an internal sentinel returned from the
// ProcessPayment RunInTx closure when [payment.Repository.Save] reports
// a duplicate idempotency-key collision. It instructs the outer code to
// roll back the in-flight transaction (so storage backends like Postgres
// exit the in_failed_sql_transaction state caused by the unique-violation)
// and re-read the winning payment record in a FRESH transaction context.
// Reading the winner inside the still-failed tx is unsafe on Postgres —
// every subsequent query would error with 25P02 and falsely route the
// race-loser through saga compensation, refunding the winner's charge.
//
// This sentinel is package-private; consumers cannot observe it.
var errDuplicateKeyRaceSignal = errors.New("duplicate idempotency key race; converge on winner via fresh tx")

// errTerminalStateReplay marks the conflict produced by the IN-TX idempotency
// switch when the effective key collides with an existing payment in a
// terminal state (Failed / Refunded / PartiallyRefunded / ChargedBack). The
// gateway Charge that preceded the transaction was an idempotent REPLAY of the
// transaction backing that terminal record — the gateway moved no new money —
// so the post-tx error handling must NOT route this through saga compensation:
// a compensation Refund would be a SECOND real reversal of the original
// transaction (worst on ChargedBack, where the funds were already pulled back
// by the network). Wrapped together with the caller-facing ErrCodeConflict
// DomainError so ProcessPayment can converge cleanly (issue #234).
var errTerminalStateReplay = errors.New("idempotency key collides with a terminal payment record")

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

	// IdempotencyKey optionally pins the gateway-facing idempotency key for
	// this refund. Leave empty (the common case) to let [PaymentService.Refund]
	// derive a deterministic key from the payment — see the Refund godoc for
	// the derivation scheme and its guarantees. Supplying an explicit key mirrors
	// [ProcessPaymentInput.IdempotencyKey] and is intended for callers that
	// already own an end-to-end idempotency token (e.g. a request ID threaded
	// from an HTTP/gRPC edge). The caller then owns the contract that RETRIES of
	// the same logical refund reuse the SAME key while DISTINCT refunds of the
	// same payment use DIFFERENT keys; violating it reintroduces the double-refund
	// window this field exists to close.
	IdempotencyKey string
}

// deriveRefundIdempotencyKey builds a deterministic gateway idempotency key for
// a refund of paymentID of refundAmount, where the payment's cumulative
// refunded total BEFORE this attempt is priorRefunded.
//
// Scheme: "refund-<paymentID>-<currency>-<priorRefunded>-<refundAmount>"
// (amounts as big.Rat strings).
//
// Why (prior cumulative total, amount) is the right discriminator:
//
//   - A payment's refundedAmount is monotonically non-decreasing: every
//     successful RecordRefund adds a positive amount (ValidateRefund rejects
//     non-positive amounts). So the pre-refund cumulative total identifies the
//     "slot" in the payment's refund sequence this attempt is filling.
//   - Two RETRIES of the SAME attempt (e.g. after a gateway timeout, or two
//     concurrent callers that both loaded the same un-refunded state and asked
//     for the SAME amount) observe the SAME (priorRefunded, refundAmount) →
//     derive the SAME key → the gateway collapses them into ONE real refund
//     (Stripe/Adyen/GMO PG/PayPal all dedupe on the idempotency key). This is
//     what closes the double-refund window.
//   - Two DISTINCT partial refunds (a 3000 refund followed by a later 2000
//     refund) observe DIFFERENT priorRefunded (0, then 3000) → derive DIFFERENT
//     keys → both legitimately reach the gateway.
//   - Two CONCURRENT partial refunds of DIFFERENT amounts (a 3000 and a 2000
//     that both loaded priorRefunded=0) derive DIFFERENT keys — binding the
//     amount into the key is what makes this true (issue #235). Under the old
//     prior-only scheme they collided: the gateway executed only the first
//     amount and replayed its cached response for the second, while the local
//     ledger recorded BOTH — booking a refund the gateway never moved. With
//     the amount bound, both are real gateway refunds and the ledger matches
//     the gateway-moved total.
//
// A concurrent refund with the same prior total AND the same amount is
// indistinguishable from a retry of the same logical refund and is deduped to
// one movement; callers that genuinely need two identical-amount refunds issue
// them sequentially (distinct prior totals) or supply explicit distinct
// [RefundInput.IdempotencyKey] values.
func deriveRefundIdempotencyKey(paymentID shared.PaymentID, priorRefunded, refundAmount shared.Money) string {
	return fmt.Sprintf("refund-%s-%s-%s-%s",
		paymentID, priorRefunded.Currency(), priorRefunded.Amount().RatString(), refundAmount.Amount().RatString())
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

// WithCustomerIDResolver wires a [port.CustomerIDResolver] that maps internal
// account IDs to gateway-side customer IDs (issue #231).
//
// When wired, the resolver runs before EVERY gateway call that carries a
// customer ID (the Charge in ProcessPayment and the CustomerGateway.GetCustomer
// lookup in ResolvePaymentMethod). A resolver error aborts the operation
// BEFORE the gateway is touched, so resolution failures are money-safe.
//
// When NOT wired, the service preserves the legacy identity mapping: the
// internal shared.AccountID is sent verbatim as the gateway customer ID. That
// only suits gateways that accept caller-chosen customer identifiers (e.g.
// GMO PG MemberID registered under the integrator's own ID scheme). It is
// WRONG for gateways that mint their own customer IDs — Stripe is the
// canonical example ("cus_..." values assigned by Stripe) — where charges
// referencing an internal account ID as the customer will fail. Deployments on
// such gateways MUST wire a resolver.
func WithCustomerIDResolver(r port.CustomerIDResolver) PaymentServiceOption {
	return func(s *PaymentService) {
		s.customerIDResolver = r
	}
}

// WithPaymentTxManager sets the transaction manager for the PaymentService.
// If not provided, a NoopTxManager is used (no transaction wrapping) and a
// Warn-level log is emitted at construction (see WithoutPaymentTransactions to
// opt out).
func WithPaymentTxManager(tm tx.TxManager) PaymentServiceOption {
	return func(s *PaymentService) {
		s.txManager = tm
	}
}

// WithoutPaymentTransactions explicitly opts the PaymentService into running
// without a transaction manager (payment + invoice writes will NOT be atomic).
// Use it for in-memory demos and tests where that trade-off is intentional; it
// suppresses the non-atomic warning that a silently-defaulted NoopTxManager
// would otherwise emit. Do NOT use it in production with real repositories.
func WithoutPaymentTransactions() PaymentServiceOption {
	return func(s *PaymentService) {
		s.suppressTxWarning = true
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
	gateway            port.PaymentGateway
	paymentRepo        payment.Repository
	invoiceRepo        invoice.Repository
	contractRepo       contract.Repository
	customerGateway    port.CustomerGateway
	customerIDResolver port.CustomerIDResolver
	eventStore         eventstore.Store
	registry           *plugin.Registry
	clock              shared.Clock
	logger             *slog.Logger
	txManager          tx.TxManager
	idempotencyStore   port.IdempotencyStore
	// suppressTxWarning records an explicit WithoutPaymentTransactions() opt-in so
	// the default-NoopTxManager warning is not emitted for intentional non-atomic use.
	suppressTxWarning bool
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
		repos := tx.Repos{
			Payments: paymentRepo,
			Invoices: invoiceRepo,
		}
		if s.suppressTxWarning {
			s.txManager = tx.NewNoopTxManagerExplicit(repos)
		} else {
			s.txManager = tx.NewNoopTxManager(repos)
		}
	}
	tx.WarnIfDefaultNoop(s.logger, s.txManager, "PaymentService", "wire WithPaymentTxManager(...) (or WithoutPaymentTransactions() to acknowledge non-atomic in-memory use)")
	return s
}

// resolveGatewayCustomerID maps an internal account ID to the gateway-side
// customer ID via the wired [port.CustomerIDResolver] (issue #231). Without a
// resolver it preserves the legacy identity mapping (see
// [WithCustomerIDResolver] for when that is — and is not — appropriate).
//
// Callers MUST invoke this before every gateway call that carries a customer
// ID, and MUST abort on error before touching the gateway: a resolution
// failure is money-safe only as long as nothing has been charged.
func (s *PaymentService) resolveGatewayCustomerID(ctx context.Context, accountID shared.AccountID) (string, error) {
	if s.customerIDResolver == nil {
		return string(accountID), nil
	}
	customerID, err := s.customerIDResolver.ResolveCustomerID(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("customer ID resolution failed for account %s: %w", accountID, err)
	}
	if customerID == "" {
		return "", shared.NewDomainError(shared.ErrCodeBusinessRule,
			fmt.Sprintf("customer ID resolver returned an empty customer ID for account %s", accountID))
	}
	return customerID, nil
}

// ProcessPayment charges an invoice and records the payment.
//
// SECURITY-CRITICAL — concurrency contract (issue #97).
//
// Under concurrent ProcessPayment calls with the SAME IdempotencyKey
// that both reach the success path, the safety of this method depends
// on TWO independent guarantees that consumers must wire correctly. If
// either is violated, the failure mode is silent: the code compiles,
// the happy path passes, and the regression only manifests under
// concurrency in production.
//
// 1. STORAGE UNIQUENESS — [payment.Repository.Save] MUST reject a write
// whose non-empty idempotency_key collides with a different existing
// PaymentID, surfacing the collision as an error matching
// errors.Is(err, [payment.ErrDuplicateIdempotencyKey]) (typically a
// [*payment.DuplicateIdempotencyKeyError]). Without this, the
// interleaving
//
//	G1: FindByIdempotencyKey(K) → nil
//	G2: FindByIdempotencyKey(K) → nil   // G1 hasn't saved yet
//	G1: Save(p1) → OK
//	G2: Save(p2) → OK                   // DUPLICATE — invoice double-paid
//
// is possible because the pre-save existence check and the write cannot
// be serialized at the application layer alone. Production deployments
// MUST implement [payment.Repository] with one of:
//
//   - Postgres/MySQL: UNIQUE INDEX on idempotency_key (or SELECT ... FOR
//     UPDATE inside the TxManager closure).
//   - DynamoDB: a ConditionExpression that rejects writes when the key
//     already exists.
//   - Any other backend: an equivalent compare-and-swap guarantee.
//
// Consumer-provided repositories MUST translate raw driver errors (e.g.
// Postgres 23505) to the sentinel before returning — a raw driver error
// will route the race-loser through saga compensation and refund the
// winner's legitimate charge. See docs/guides/postgres-payment-repository.md.
// The InMemoryPaymentRepository simulates this constraint and is the
// canonical reference behaviour.
//
// 2. GATEWAY-LEVEL IDEMPOTENCY REPLAY — when both goroutines call the
// payment gateway concurrently with the same effective key, the gateway
// MUST collapse them into a SINGLE underlying transaction (Stripe,
// Adyen, Braintree, GMO PG, and PayPal all support this when the
// IdempotencyKey is forwarded — see [port.PaymentGateway.Charge]). The
// race-loser convergence path in this method intentionally DOES NOT
// fire saga compensation: a refund would unwind the winner's legitimate
// charge. That choice is only safe because the gateway has merged the
// two charge attempts into one. If the consumer disables idempotent
// replay at the gateway, forgets to forward the key, or routes the
// goroutines through different gateway accounts, the two charge
// attempts produce TWO real authorisations — and refusing to compensate
// becomes a double-charge. Verify gateway-side idempotency on every
// supported gateway before deploying.
//
// On a duplicate-key save error inside the RunInTx closure, the closure
// returns an internal sentinel that rolls the transaction back and
// re-reads the winning payment record in a FRESH transaction context
// (issue #97 review follow-up). Reading the winner inside the still-
// failed tx is unsafe on Postgres because the unique-violation puts the
// connection into in_failed_sql_transaction (SQLSTATE 25P02), which
// would silently route the race-loser through saga compensation. If the
// fresh-tx read returns nil because the winner's commit is not yet
// visible (read-replica lag, MVCC ordering), ProcessPayment returns a
// transient [shared.ErrCodeConflict] error so the caller can retry
// instead of compensating against a real charge.
func (s *PaymentService) ProcessPayment(ctx context.Context, invoiceID shared.InvoiceID, input ProcessPaymentInput) (*payment.Payment, error) {
	// Boundary validation (issue #241): an empty IdempotencyKey silently
	// disables every duplicate-charge defence in this method — the pre-charge
	// and in-tx idempotency lookups are skipped, the storage uniqueness
	// contract cannot fire, and the gateway cannot collapse concurrent
	// retries into one charge. The domain model declares the key mandatory
	// (see the Payment entity table in CLAUDE.md), so reject up front instead
	// of degrading to an unprotected charge.
	if input.IdempotencyKey == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"ProcessPayment requires a non-empty IdempotencyKey (duplicate-charge protection depends on it)")
	}

	// Load invoice
	inv, err := s.invoiceRepo.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load invoice: %w", err)
	}
	// Defensive nil-guard (issue #197): invoice.Repository.FindByID is documented
	// to return an error (ErrCodeNotFound) for a missing invoice, but a BYO-DB
	// adapter that instead returns (nil, nil) would otherwise nil-panic on
	// inv.AmountDue() below. Convert it to a clean not-found domain error.
	if inv == nil {
		return nil, shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("invoice %s not found", invoiceID))
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

	// Resolve payment method via fallback chain if not explicitly provided.
	// Skipped for a zero-amount settlement (issue #197): a fully-discounted /
	// fully-credited invoice has nothing to charge, so requiring a payment method
	// (ResolvePaymentMethod errors when none is on file) would wrongly block
	// settlement of an invoice that needs no gateway at all.
	pmID := input.PaymentMethodID
	if pmID == "" && !amount.IsZero() {
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

	// Zero-amount settlement (issue #197): a fully-discounted or fully-credited
	// invoice has AmountDue()==0. Real gateways reject a zero-value Charge, so the
	// unconditional gateway call below would make such invoices unsettleable.
	// Settle it directly instead — record a completed zero-value payment and mark
	// the invoice paid — without touching the gateway or the saga. Idempotency is
	// still honoured (the pre-charge lookup above already short-circuited a prior
	// completed payment under the effective key; the in-tx check repeats it).
	if amount.IsZero() {
		return s.settleZeroAmountPayment(ctx, inv, invoiceID, amount, effectiveKey, input)
	}

	// Resolve the gateway-side customer ID BEFORE the BeforeCharge hooks and
	// the gateway Charge (issue #231). A resolution failure aborts here —
	// before any money moves and before plugins observe a phantom charge
	// attempt.
	customerID, err := s.resolveGatewayCustomerID(ctx, inv.AccountID())
	if err != nil {
		return nil, err
	}

	// Build PaymentContext with invoice (payment is nil at this stage for BeforeCharge)
	payCtx := plugin.NewPaymentContext(ctx, nil, inv)

	// Execute BeforeCharge hooks. Order matters: this runs AFTER the
	// pre-charge idempotency short-circuit so plugins never see
	// phantom charge attempts for retries that will not touch the gateway.
	for _, hook := range s.registry.GetBeforeChargeHooks() {
		// Veto-capable: a returned error OR a recovered panic aborts the charge
		// before the gateway is touched (plugin panic policy,
		// docs/internals/plugin-system.md §5.4).
		err = plugin.SafeInvoke("BeforeChargeHook.BeforeCharge", hook.Name(), func() error {
			return hook.BeforeCharge(payCtx, amount)
		})
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
		CustomerID:      customerID,
		PaymentMethodID: &pmID,
		Description:     fmt.Sprintf("Invoice %s", invoiceID),
		Metadata:        input.Metadata,
		IdempotencyKey:  effectiveKey,
	})

	// No ChargeResponse available on failure — resolve from input only
	inputMethodType := s.resolvePaymentMethodType("", input.PaymentMethod)

	if err != nil {
		// Create and persist a failed payment record for tracking.
		// amount was already validated non-negative by ValidatePayment above, so
		// this construction cannot fail on the sign guard (issue #148); handle the
		// error defensively regardless.
		failedPayment, npErr := payment.NewPayment(
			shared.NewPaymentID(),
			invoiceID,
			amount,
			inputMethodType,
			"",
			s.clock.Now(),
		)
		if npErr != nil {
			return nil, fmt.Errorf("failed to construct failed-payment record: %w", npErr)
		}
		if failErr := failedPayment.Fail(err.Error()); failErr == nil {
			// Best-effort save of failed payment record. A save failure here is
			// non-fatal (the gateway charge already failed, so there is nothing
			// to reconcile), but it must not be swallowed silently: without the
			// record, operators lose the audit trail of the failed attempt.
			// Log at Warn so the drop is observable (issue #162 L1).
			if saveErr := s.paymentRepo.Save(ctx, failedPayment); saveErr != nil {
				s.logger.Warn("failed to persist failed-payment record (audit trail dropped)",
					"paymentID", failedPayment.ID(),
					"invoiceID", invoiceID,
					"error", saveErr,
				)
			}
		}
		// Execute OnPaymentFailed hooks with PaymentContext
		failCtx := plugin.NewPaymentContext(ctx, failedPayment, inv)
		gatewayErr := err
		for _, hook := range s.registry.GetOnPaymentFailedHooks() {
			if hookErr := plugin.SafeInvoke("OnPaymentFailedHook.OnPaymentFailed", hook.Name(), func() error {
				return hook.OnPaymentFailed(failCtx, gatewayErr)
			}); hookErr != nil {
				plugin.LogNonFatalHookError(s.logger, "OnPaymentFailed hook failed", hookErr,
					"hook", hook.Name(),
					"paymentID", failedPayment.ID(),
					"invoiceID", invoiceID,
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

		pendingPayment, npErr := payment.NewPayment(
			shared.NewPaymentID(),
			invoiceID,
			amount,
			s.resolvePaymentMethodType(chargeResp.PaymentMethodType, input.PaymentMethod),
			chargeResp.TransactionID,
			s.clock.Now(),
		)
		if npErr != nil {
			return nil, fmt.Errorf("failed to construct pending payment record: %w", npErr)
		}
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

	// Phase 2: Saga compensation for gateway charge (issue #86).
	//
	// Charge is authorize+capture (one-step), so the transaction is already
	// captured from the caller's point of view. To reverse it we try Void
	// FIRST, then fall back to Refund:
	//
	//   1. Void — cancels the transaction while it is still pre-settlement.
	//      Some gateways/payment methods (bank transfer, convenience store,
	//      carrier billing, direct debit) do NOT settle instantly and REJECT
	//      an immediate Refund until settlement completes 24-48h later. For
	//      those, Void is the only reversal available inside the same request.
	//   2. Refund (fallback) — reverses an already-settled/captured charge.
	//      Void fails for a settled charge (it is only valid pre-capture),
	//      so a Void error is the signal to fall back to Refund. This is the
	//      common case for instantly-settling credit-card charges.
	//
	// MONEY-SAFETY INVARIANT: Void and Refund must NEVER both take effect for
	// one charge. On a SUCCESSFUL Void we return immediately and never call
	// Refund, so a non-idempotent gateway cannot double-reverse. Refund is
	// attempted ONLY when Void returns an error (i.e. nothing was voided).
	//
	// Load-bearing invariant: both compensation calls use a DETERMINISTIC
	// IdempotencyKey derived from chargeResp.TransactionID, not a fresh UUID.
	// This is what makes concurrent compensations safe: if two goroutines
	// both trigger compensation for the same original charge (e.g. two
	// parallel ProcessPayment calls that both hit the gateway's idempotent
	// replay of a successful charge), they derive the same void/refund keys
	// and the gateway deduplicates the replayed call, returning the SAME
	// outcome — so both goroutines take the same branch (both see Void
	// success → neither refunds, or both see the same Void error → both
	// refund under one deduplicated refund key). Changing either key to
	// something non-deterministic would silently reintroduce double-reverse
	// risk under concurrency.
	saga := tx.NewSaga()
	saga.AddCompensation(func(compCtx context.Context) error {
		// One-step Charge exposes no separate AuthorizationID; the transaction
		// ID identifies the (as-yet-unsettled) authorization for Void.
		_, voidErr := s.gateway.Void(compCtx, &port.VoidRequest{
			AuthorizationID: chargeResp.TransactionID,
			IdempotencyKey:  "comp-void-" + chargeResp.TransactionID,
		})
		if voidErr == nil {
			// Pre-settlement charge reversed via Void. MUST NOT also Refund.
			return nil
		}

		// Void failed — the charge is likely already captured/settled, where
		// Void is not permitted. Fall back to Refund.
		s.logger.Warn("saga compensation: Void failed, falling back to Refund (charge likely already settled)",
			"transactionID", chargeResp.TransactionID,
			"voidError", voidErr,
		)
		_, refundErr := s.gateway.Refund(compCtx, &port.RefundRequest{
			TransactionID:  chargeResp.TransactionID,
			Amount:         &chargeResp.Amount,
			Reason:         port.RefundReasonOther,
			IdempotencyKey: "comp-refund-" + chargeResp.TransactionID,
		})
		if refundErr != nil {
			return fmt.Errorf("compensation void failed (%v) and refund fallback also failed: %w", voidErr, refundErr)
		}
		return nil
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
	p, npErr := payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		amount,
		s.resolvePaymentMethodType(chargeResp.PaymentMethodType, input.PaymentMethod),
		chargeResp.TransactionID,
		s.clock.Now(),
	)
	if npErr != nil {
		return nil, fmt.Errorf("failed to construct payment record: %w", npErr)
	}
	if effectiveKey != "" {
		p.SetIdempotencyKey(effectiveKey)
	}

	// Phase 3: All state mutations and local writes are atomic within a transaction.
	// p.Complete() and inv.RecordPayment() are inside RunInTx so that if the
	// transaction fails, saga.Compensate() fires (no early return before it).
	// Note: if RunInTx executes the closure but then rolls back the DB, the
	// in-memory state of p and inv will remain mutated. This is acceptable
	// because the caller returns an error and does not reuse these objects.
	//
	// racedLoser is set to true by the #97 concurrent-success convergence
	// path when this goroutine lost the race against another ProcessPayment
	// call with the same IdempotencyKey. When set, the post-RunInTx
	// duplicate-key handler reassigns p to the WINNER's record (read in
	// a fresh tx, see the errDuplicateKeyRaceSignal branch), but `inv`
	// here is still the loser's local copy whose in-memory state was
	// mutated by RecordPayment and never persisted. The winner's tx is
	// expected to hold the authoritative invoice-paid state; under
	// snapshot-isolation backends the winner's commit may not yet be
	// visible to this goroutine when the convergence read happens, so
	// the post-tx invoice re-fetch may temporarily observe a stale
	// (pre-paid) snapshot. The AfterCharge hooks must therefore tolerate
	// stale invoice state on the race-loser path — we re-fetch the
	// invoice AFTER
	// RunInTx returns so the AfterCharge hooks see a fresh snapshot
	// consistent with the winner's payment.
	var racedLoser bool
	// Joined-tx detection (issue #233): when the caller invoked ProcessPayment
	// from inside its OWN transaction, tx.Run below joins it — there is no
	// rollback boundary between the duplicate-key violation and this method's
	// post-tx code. On backends like Postgres the violation leaves the AMBIENT
	// transaction aborted (SQLSTATE 25P02), so the "fresh" winner re-read would
	// fail spuriously and must not be attempted (see the duplicate-key handler
	// below).
	_, joinedTx := tx.ReposFromContext(ctx)
	// tx.Run (not raw RunInTx) so this stamps the transaction onto the context
	// and joins an outer transaction when one is active (review M2). The
	// duplicate-key convergence below re-reads the winner on the OUTER ctx (which
	// tx.Run leaves unstamped), so it remains a fresh read at the top level.
	err = tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		// Re-load the invoice through the transaction-scoped repository so the
		// RecordPayment + Save check-then-act runs against the current persisted
		// state, not the copy read before the gateway charge (issue #151). On a
		// backend honouring the invoice concurrency contract this converges with a
		// concurrent payment that already recorded on the invoice — RecordPayment
		// sees the fresh state and either records the remaining balance or is
		// rejected with a clean domain error — instead of the stale copy's Save
		// failing with an optimistic-lock conflict (issue #147 version bump).
		// Reassigning inv keeps the post-tx AfterCharge / OnPaymentProcessed hooks
		// consistent with what was persisted; on the race-loser path inv is
		// re-fetched again after convergence.
		invoiceRepo := repos.Invoices
		if invoiceRepo == nil {
			invoiceRepo = s.invoiceRepo
		}
		freshInv, invErr := invoiceRepo.FindByID(txCtx, invoiceID)
		if invErr != nil {
			return fmt.Errorf("failed to reload invoice in tx: %w", invErr)
		}
		// Defensive nil-guard (issue #197): a BYO-DB adapter returning (nil, nil)
		// would nil-panic on inv.RecordPayment below.
		if freshInv == nil {
			return shared.NewDomainError(shared.ErrCodeNotFound,
				fmt.Sprintf("invoice %s not found", invoiceID))
		}
		inv = freshInv

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
					//
					// Wrapped in errTerminalStateReplay so the post-tx
					// handling returns the conflict WITHOUT firing saga
					// compensation (issue #234): the Charge above was an
					// idempotent replay of the transaction that already
					// backs this terminal record — no new money moved, and
					// a compensation Refund would be a second real reversal
					// of the original charge.
					return fmt.Errorf("%w: %w", errTerminalStateReplay,
						shared.NewDomainError(
							shared.ErrCodeConflict,
							fmt.Sprintf("cannot replay payment in terminal state %q (idempotency key %q)",
								existing.Status(), effectiveKey),
						))
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
			// Issue #97: concurrent-success race. The DB's unique-key
			// guarantee on idempotency_key (Postgres UNIQUE INDEX,
			// DynamoDB condition expression, in-memory simulation here)
			// rejects the race loser's insert and surfaces as
			// [payment.ErrDuplicateIdempotencyKey] (possibly wrapped
			// inside a *payment.DuplicateIdempotencyKeyError). When
			// this happens, a concurrent ProcessPayment call has
			// already persisted the winning payment record under the
			// same effective key. Both goroutines share one underlying
			// gateway transaction (the gateway's own idempotency
			// replay collapsed them), so refunding via saga
			// compensation would undo the winner's legitimate charge.
			//
			// Why we surface a sentinel and exit the tx instead of
			// re-reading the winner here: on Postgres a unique-
			// violation aborts the surrounding transaction
			// (in_failed_sql_transaction / SQLSTATE 25P02). Every
			// subsequent query — including FindByIdempotencyKey on
			// txCtx — would error, and the application layer would
			// fall through to saga.Compensate and refund the winner's
			// charge. The post-RunInTx handler instead re-reads the
			// winner using the OUTER ctx after the failed tx is
			// rolled back, which is safe across Postgres, MySQL,
			// DynamoDB, and the in-memory backend.
			//
			// This branch only fires on a non-empty effective key —
			// empty keys cannot collide at the repository layer, and a
			// duplicate-key error with an empty effective key is
			// therefore spurious and must NOT be routed through the
			// winner-convergence path.
			if errors.Is(saveErr, payment.ErrDuplicateIdempotencyKey) && effectiveKey != "" {
				return errDuplicateKeyRaceSignal
			}
			return fmt.Errorf("failed to save payment: %w", saveErr)
		}
		if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
			return fmt.Errorf("failed to save invoice after payment: %w", saveErr)
		}
		return nil
	})

	// Issue #97 review follow-up: duplicate-key race convergence on a
	// FRESH transaction context. By the time we reach this branch, the
	// in-flight tx has already been rolled back by RunInTx (the closure
	// returned errDuplicateKeyRaceSignal), so on Postgres the connection
	// is no longer in in_failed_sql_transaction state and the outer ctx
	// is safe to query. We rely on s.paymentRepo (NOT repos.Payments,
	// which is tx-scoped) to read the winner's record.
	if errors.Is(err, errDuplicateKeyRaceSignal) {
		if joinedTx {
			// Joined-tx mode (issue #233): ProcessPayment ran inside the
			// CALLER's transaction, so nothing was rolled back when the
			// closure returned the sentinel — on Postgres the ambient
			// transaction is still aborted (25P02) and EVERY read on this
			// connection fails spuriously. Re-reading the winner here would
			// error, and compensating on that error would refund the winner's
			// legitimate charge. The duplicate-key violation itself already
			// proves a winner owns the gateway charge, so return a retryable
			// conflict WITHOUT reading and WITHOUT compensating; the caller
			// must roll back its transaction and retry (the retry converges
			// on the winner via the pre-charge idempotency check).
			s.logger.Warn("duplicate-key race detected inside a caller-owned transaction; returning retryable conflict (no compensation)",
				"effectiveKey", effectiveKey,
				"invoiceID", invoiceID,
			)
			return nil, shared.NewDomainError(
				shared.ErrCodeConflict,
				fmt.Sprintf("duplicate idempotency key %q detected inside a caller-owned transaction; roll back the enclosing transaction and retry to converge on the winning payment", effectiveKey),
			)
		}
		winner, findErr := s.paymentRepo.FindByIdempotencyKey(ctx, effectiveKey)
		if findErr != nil {
			// The winner re-read failed. This is an infrastructure hiccup,
			// NOT evidence that the gateway charge is orphaned: the
			// duplicate-key violation itself proves a concurrent winner's
			// payment record owns the charge (issue #233). Compensating here
			// would refund the winner's legitimate charge, so return a
			// retryable conflict instead and let a later call converge via
			// the pre-charge idempotency check.
			s.logger.Error("duplicate-key race detected; winner read failed (returning retryable conflict, NOT compensating)",
				"effectiveKey", effectiveKey,
				"invoiceID", invoiceID,
				"error", findErr,
			)
			return nil, shared.NewDomainErrorWithCause(
				shared.ErrCodeConflict,
				fmt.Sprintf("duplicate idempotency key %q detected but the winning payment could not be read; retry the operation", effectiveKey),
				findErr,
			)
		} else if winner == nil {
			// The unique-violation fired but the winner's record
			// is not yet visible — typically read-replica lag, MVCC
			// snapshot ordering, or a winner whose tx has not
			// committed yet. Refusing to compensate here is the
			// safe choice: the winner's gateway charge is real and
			// must not be refunded just because we cannot see the
			// local record yet. Return a transient conflict so the
			// caller retries (a follow-up call usually finds the
			// winner via the pre-charge idempotency check).
			s.logger.Warn("duplicate-key race detected but winner not yet visible on fresh tx; returning transient conflict",
				"effectiveKey", effectiveKey,
				"invoiceID", invoiceID,
			)
			return nil, shared.NewDomainError(
				shared.ErrCodeConflict,
				fmt.Sprintf("duplicate idempotency key %q detected but winner not yet visible; retry the operation", effectiveKey),
			)
		} else {
			// Converge on the winner's record. Saga compensation
			// must NOT fire because the underlying gateway charge
			// is backing the winner.
			p = winner
			racedLoser = true
			err = nil
		}
	}

	// Terminal-state conflict convergence (issue #234): the in-tx idempotency
	// switch found a terminal payment under the effective key. The Charge above
	// was an idempotent replay of the transaction backing that record — no new
	// money moved — so this MUST NOT fall into the generic compensation path
	// below (a compensation Refund would reverse the original transaction a
	// second time; worst on ChargedBack). Return the conflict to the caller
	// with no compensation and no idempotency-store marker.
	if errors.Is(err, errTerminalStateReplay) {
		return nil, err
	}

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
			newEffectiveKey := s.newRetryEffectiveKey(input.IdempotencyKey)
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

	// Issue #97: on the race-loser path, `inv` is still the pre-convergence
	// local clone whose paidAmount/status were mutated by RecordPayment
	// and never persisted. The winner's tx holds the authoritative
	// invoice state, so we re-fetch before firing hooks to prevent
	// plugins from observing a stale (loser-side) snapshot. Re-fetch
	// failures are logged and the stale copy is used as a fallback —
	// the payment itself succeeded, so we must not fail ProcessPayment
	// over a hook-input read error.
	if racedLoser {
		refreshed, refErr := s.invoiceRepo.FindByID(ctx, invoiceID)
		if refErr != nil {
			s.logger.Warn("invoice re-fetch after duplicate-key convergence failed; AfterCharge hooks will see a possibly-stale local copy",
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
				"error", refErr,
			)
		} else {
			inv = refreshed
		}
	}

	// Phase 4: AfterCharge hooks (non-fatal, outside transaction).
	// When the idempotency path returned an existing payment, these hooks
	// still fire. Hook implementations should be idempotent.
	successCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetAfterChargeHooks() {
		// Non-fatal: the gateway charge already succeeded, so a recovered panic
		// must NOT unwind through the local persistence path (that is exactly the
		// charged-but-unrecorded-payment failure from issue #193). Log with the
		// stack and continue (plugin panic policy §5.4).
		if hookErr := plugin.SafeInvoke("AfterChargeHook.AfterCharge", hook.Name(), func() error {
			return hook.AfterCharge(successCtx)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "AfterCharge hook failed", hookErr,
				"hook", hook.Name(),
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
			)
		}
	}

	// OnPaymentProcessed metrics hooks (non-fatal, outside transaction).
	// Fired alongside AfterCharge on the success path; the pre-charge
	// idempotent-replay short-circuit above intentionally skips these,
	// consistent with AfterCharge not firing there either. Two caveats,
	// both shared with AfterCharge: when ProcessPayment runs inside a
	// caller-supplied transaction the hooks fire before the outer commit,
	// and duplicate-key convergence (the raced-loser path above) can fire
	// them more than once for the same payment ID — implementations must
	// deduplicate by payment ID.
	// Fresh context per hook category (payment + invoice, issue #223): a
	// mutation (SetContract) by an AfterCharge plugin must not leak into
	// metrics hooks — the same isolation BeforeCharge already gets from its
	// own dedicated context.
	metricsCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetOnPaymentProcessedHooks() {
		if hookErr := plugin.SafeInvoke("OnPaymentProcessedHook.OnPaymentProcessed", hook.Name(), func() error {
			return hook.OnPaymentProcessed(metricsCtx)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "OnPaymentProcessed hook failed", hookErr,
				"hook", hook.Name(),
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
			)
		}
	}

	return p, nil
}

// settleZeroAmountPayment settles a zero-amount invoice without calling the
// payment gateway (issue #197). Real gateways reject a zero-value Charge, so a
// fully-discounted / fully-credited invoice (AmountDue()==0) cannot go through
// the normal gateway path. This records a completed zero-value payment and marks
// the invoice paid, inside a single transaction, with the same in-tx idempotency
// handling as the gateway path (minus the saga — there is no charge to reverse).
//
// Hook semantics (documented decision):
//   - BeforeCharge is NOT fired. BeforeCharge is a gateway pre-flight ("about to
//     charge the gateway"); a zero settlement never touches the gateway, so
//     firing it would leak a phantom charge attempt into plugins that reserve
//     inventory / emit audit events — the same reasoning that skips BeforeCharge
//     on the pre-charge idempotent-replay short-circuit.
//   - AfterCharge and OnPaymentProcessed ARE fired (non-fatal), because the
//     payment did complete and the invoice is now paid: provisioning / metrics
//     plugins must observe a zero-value settlement exactly as they observe a
//     paid gateway charge. They fire paired, as on every success path.
func (s *PaymentService) settleZeroAmountPayment(ctx context.Context, inv *invoice.Invoice, invoiceID shared.InvoiceID, amount shared.Money, effectiveKey string, input ProcessPaymentInput) (*payment.Payment, error) {
	// Build the completed zero-value payment. No gateway transaction ID — nothing
	// was charged. Method type resolves from the input only (no ChargeResponse).
	p, npErr := payment.NewPayment(
		shared.NewPaymentID(),
		invoiceID,
		amount,
		s.resolvePaymentMethodType("", input.PaymentMethod),
		"",
		s.clock.Now(),
	)
	if npErr != nil {
		return nil, fmt.Errorf("failed to construct zero-amount payment record: %w", npErr)
	}
	if effectiveKey != "" {
		p.SetIdempotencyKey(effectiveKey)
	}

	// racedLoser mirrors the gateway path's #97 convergence flag: set when this
	// goroutine's Save lost a duplicate-idempotency-key race and converged on
	// the winner's payment, leaving the local `inv` mutated but unpersisted.
	var racedLoser bool
	// Joined-tx detection, mirroring the gateway path (issue #233): inside a
	// caller-owned transaction the duplicate-key violation aborts the ambient
	// tx, so the winner re-read below must not be attempted.
	_, joinedTx := tx.ReposFromContext(ctx)
	err := tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
		invoiceRepo := repos.Invoices
		if invoiceRepo == nil {
			invoiceRepo = s.invoiceRepo
		}
		freshInv, invErr := invoiceRepo.FindByID(txCtx, invoiceID)
		if invErr != nil {
			return fmt.Errorf("failed to reload invoice in tx: %w", invErr)
		}
		if freshInv == nil {
			return shared.NewDomainError(shared.ErrCodeNotFound,
				fmt.Sprintf("invoice %s not found", invoiceID))
		}
		inv = freshInv

		// In-tx idempotency: converge on an existing payment under the effective
		// key rather than double-settling. Mirrors the gateway path's switch.
		if effectiveKey != "" {
			existing, findErr := repos.Payments.FindByIdempotencyKey(txCtx, effectiveKey)
			if findErr != nil {
				return fmt.Errorf("idempotency check failed: %w", findErr)
			}
			if existing != nil {
				switch existing.Status() {
				case payment.PaymentStatusPending:
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
					p = existing
					return nil
				case payment.PaymentStatusFailed,
					payment.PaymentStatusRefunded,
					payment.PaymentStatusPartiallyRefunded,
					payment.PaymentStatusChargedBack:
					return shared.NewDomainError(
						shared.ErrCodeConflict,
						fmt.Sprintf("cannot replay payment in terminal state %q (idempotency key %q)",
							existing.Status(), effectiveKey),
					)
				}
			}
		}

		if completeErr := p.Complete(); completeErr != nil {
			return fmt.Errorf("failed to complete zero-amount payment: %w", completeErr)
		}
		if recordErr := inv.RecordPayment(amount, s.clock.Now()); recordErr != nil {
			return fmt.Errorf("failed to record zero-amount payment on invoice: %w", recordErr)
		}
		if saveErr := repos.Payments.Save(txCtx, p); saveErr != nil {
			// A duplicate-key collision means a concurrent settlement won. There
			// is no gateway charge to reconcile for a zero payment, but the
			// convergence read must still happen AFTER this transaction exits
			// (issue #241 item 2, mirroring the gateway path's #97 handling): on
			// Postgres the unique-violation has aborted the transaction
			// (SQLSTATE 25P02), so converging — or even returning nil to commit —
			// from inside the aborted tx is backend-dependent and unsafe. Return
			// the same abort sentinel the gateway path uses so the tx rolls back
			// cleanly and the post-tx handler converges on the winner.
			if errors.Is(saveErr, payment.ErrDuplicateIdempotencyKey) && effectiveKey != "" {
				return errDuplicateKeyRaceSignal
			}
			return fmt.Errorf("failed to save zero-amount payment: %w", saveErr)
		}
		if saveErr := repos.Invoices.Save(txCtx, inv); saveErr != nil {
			return fmt.Errorf("failed to save invoice after zero-amount payment: %w", saveErr)
		}
		return nil
	})
	// Duplicate-key convergence on a FRESH context, after the failed tx has
	// rolled back (issue #241 item 2). There is no gateway charge behind a
	// zero-amount settlement, so there is never anything to compensate here —
	// the loser simply converges on the winner's record or asks the caller to
	// retry.
	if errors.Is(err, errDuplicateKeyRaceSignal) {
		if joinedTx {
			// Caller-owned transaction (issue #233): the ambient tx is aborted;
			// no reads are safe. Return a retryable conflict.
			return nil, shared.NewDomainError(
				shared.ErrCodeConflict,
				fmt.Sprintf("duplicate idempotency key %q detected for zero-amount settlement inside a caller-owned transaction; roll back the enclosing transaction and retry", effectiveKey),
			)
		}
		winner, findErr := s.paymentRepo.FindByIdempotencyKey(ctx, effectiveKey)
		if findErr != nil {
			return nil, shared.NewDomainErrorWithCause(
				shared.ErrCodeConflict,
				fmt.Sprintf("duplicate idempotency key %q detected for zero-amount settlement but the winning payment could not be read; retry", effectiveKey),
				findErr,
			)
		}
		if winner == nil {
			return nil, shared.NewDomainError(shared.ErrCodeConflict,
				fmt.Sprintf("duplicate idempotency key %q detected for zero-amount settlement; retry", effectiveKey))
		}
		p = winner
		racedLoser = true
		err = nil
	}
	if err != nil {
		return nil, err
	}

	// Issue #97 (mirroring the gateway path): on the race-loser path, `inv` is
	// still the pre-convergence local copy whose paidAmount/status were mutated
	// by RecordPayment and never persisted (the tx returned before Invoices.Save).
	// The winner's tx holds the authoritative invoice state, so we re-fetch
	// before firing hooks to prevent plugins from observing a stale (loser-side)
	// snapshot. Re-fetch failures are logged and the stale copy is used as a
	// fallback — the settlement itself succeeded, so we must not fail it over a
	// hook-input read error.
	if racedLoser {
		refreshed, refErr := s.invoiceRepo.FindByID(ctx, invoiceID)
		if refErr != nil {
			s.logger.Warn("invoice re-fetch after duplicate-key convergence failed; AfterCharge hooks will see a possibly-stale local copy",
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
				"error", refErr,
			)
		} else {
			inv = refreshed
		}
	}

	// AfterCharge + OnPaymentProcessed fire (non-fatal); BeforeCharge does not.
	successCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetAfterChargeHooks() {
		if hookErr := plugin.SafeInvoke("AfterChargeHook.AfterCharge", hook.Name(), func() error {
			return hook.AfterCharge(successCtx)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "AfterCharge hook failed", hookErr,
				"hook", hook.Name(),
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
			)
		}
	}
	// Fresh context per hook category (payment + invoice, issue #223): a
	// mutation (SetContract) by an AfterCharge plugin must not leak into
	// metrics hooks — the same isolation BeforeCharge already gets from its
	// own dedicated context.
	metricsCtx := plugin.NewPaymentContext(ctx, p, inv)
	for _, hook := range s.registry.GetOnPaymentProcessedHooks() {
		if hookErr := plugin.SafeInvoke("OnPaymentProcessedHook.OnPaymentProcessed", hook.Name(), func() error {
			return hook.OnPaymentProcessed(metricsCtx)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "OnPaymentProcessed hook failed", hookErr,
				"hook", hook.Name(),
				"paymentID", p.ID(),
				"invoiceID", invoiceID,
			)
		}
	}

	return p, nil
}

// Refund processes a refund for a payment (issue #150).
//
// # Gateway idempotency key (money-safety)
//
// The gateway refund is ALWAYS sent with a non-empty idempotency key. The key
// is either the caller-supplied [RefundInput.IdempotencyKey] or, when that is
// empty, a deterministic key derived from the payment, its pre-refund
// cumulative refunded total, AND the refund amount (see
// [deriveRefundIdempotencyKey]; the amount binding is issue #235). This is the
// core of the fix: a caller that retries after a gateway timeout, and two
// concurrent callers that both loaded the same un-refunded state and asked for
// the same amount, all derive the SAME key for the SAME logical refund, so the
// gateway collapses the duplicate calls into a SINGLE real refund instead of
// moving money twice. Distinct refunds of the same payment — sequential
// partials (different prior totals) or concurrent partials of DIFFERENT
// amounts — derive DIFFERENT keys and are not deduped, so each is a real
// gateway movement matched one-to-one by a ledger record.
//
// # Transaction boundary and gateway placement
//
// This method follows the same policy as [PaymentService.ProcessPayment]: the
// gateway call happens BEFORE the local transaction, never inside it. Two
// reasons:
//
//   - A refund cannot be compensated (unlike a charge, which ProcessPayment can
//     unwind via Void/Refund). Holding a DB transaction open across the slow,
//     failure-prone network call to the gateway — and then rolling it back —
//     would leave money moved with no local record and no way to reverse it.
//     Keeping the gateway call outside the tx means the ONLY thing the tx does
//     is the local, reversible bookkeeping.
//   - Safety under concurrency/retry is provided by the deterministic gateway
//     idempotency key above, exactly as ProcessPayment relies on the gateway's
//     idempotent replay of Charge.
//
// The local bookkeeping (re-load → RecordRefund → Save) runs INSIDE tx.Run so
// that the check-then-act is not split across the transaction boundary. Re-load
// happens through the transaction-scoped repository, so on a backend that
// honours the payment [payment.Repository] concurrency contract (row lock /
// SELECT ... FOR UPDATE / SERIALIZABLE / optimistic version), a concurrent second refund observes
// the winner's already-recorded state and RecordRefund rejects it with a domain
// error (invalid_state_transition or over-refund). The money never moved twice
// because the gateway deduped the two calls under one key. On a last-writer-wins
// backend the local guard is weaker, but the gateway key still prevents the
// double refund — the worst case is a redundant local write, not lost money.
//
// The key derivation, the gateway call, and the bookkeeping tx.Run all live
// inside one tx.RetryOnConflict closure (issues #190 / #235): RecordRefund
// bumps the payment's optimistic-locking version, so an optimistic-locking
// backend rejects a race-loser's Save with a version conflict instead of
// silently overwriting. On retry the closure re-reads the payment (now
// carrying the winner's refund), re-validates, and RE-DERIVES the gateway key
// from the fresh cumulative total. How the retried loser converges depends on
// what the concurrent winner did (issue #235):
//
//   - Winner already refunded everything → the fresh ValidateRefund rejects
//     with a clean over-refund / invalid-state domain error; nothing recorded.
//   - Winner consumed THIS attempt's exact key slot (same prior total, same
//     amount — the gateway REPLAYED our earlier call without moving money) →
//     the loser re-hits the gateway under the fresh key, so the record it then
//     makes is backed by a real movement.
//   - Winner recorded a DIFFERENT amount (distinct keys, so our earlier gateway
//     call was a real, still-unrecorded movement) → the loser does NOT hit the
//     gateway again (that would move the money twice) and records against the
//     earlier movement.
//
// An in-tx guard additionally refuses to record when the persisted cumulative
// total no longer matches the total the attempt derived its key from (it
// returns a version conflict so the attempt re-runs fresh) — this is what
// makes it impossible for the ledger to record a refund the gateway never
// executed, even on serialized backends where the loser's Save would not
// conflict. This mirrors CreditNoteService.RefundCreditNote's retry shape.
//
// The winner-detection above reasons from the cumulative total alone, so with
// THREE-plus writers racing one payment it can misclassify (e.g. two
// concurrent partials summing to exactly this attempt's amount look like a
// consumed key slot); such a misclassification surfaces as a reconciliation
// error rather than silent corruption. Callers needing stronger guarantees
// supply explicit [RefundInput.IdempotencyKey] values and serialize refunds.
//
// tx.Run (not raw RunInTx) joins an outer transaction if the caller already
// started one, and stamps the tx onto the context.
//
// # Hooks
//
// OnRefund hooks fire after successful persistence and are non-fatal.
func (s *PaymentService) Refund(ctx context.Context, paymentID shared.PaymentID, input RefundInput) error {
	// Load payment
	p, err := s.paymentRepo.FindByID(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("failed to load payment: %w", err)
	}
	// Defensive nil-guard (issue #197): payment.Repository.FindByID is documented
	// to error on a missing payment; a BYO-DB adapter returning (nil, nil) would
	// otherwise nil-panic on p.Amount() below.
	if p == nil {
		return shared.NewDomainError(shared.ErrCodeNotFound,
			fmt.Sprintf("payment %s not found", paymentID))
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

	// Pre-flight validation BEFORE the irreversible gateway refund. A refund that
	// fails domain validation (non-refundable state, non-positive amount, currency
	// mismatch, or over-refund) must be rejected up front — otherwise money moves
	// at the gateway and the subsequent local RecordRefund fails, forcing manual
	// reconciliation.
	if err = p.ValidateRefund(refundAmount); err != nil {
		return fmt.Errorf("refund validation failed: %w", err)
	}

	// Phase 2+3: key derivation, gateway refund, and local bookkeeping run as
	// one retriable attempt (issues #190 / #235). Each attempt derives the
	// gateway key from the freshest cumulative refunded total, calls the
	// gateway (when this invocation still owes a movement — see the decision
	// below), and records inside a transaction. A version conflict from the
	// bookkeeping re-runs the whole attempt against the winner's state.
	var (
		recorded       *payment.Payment
		recordRejected bool
		gatewayFailed  bool
		// State of THIS invocation's most recent gateway call, used by retry
		// attempts to decide whether that call actually moved money (real,
		// unique key) or was replayed by the gateway (key consumed by a
		// concurrent identical refund) — see the callGateway decision.
		gatewayCalled bool
		gatewayKey    string
		gatewayPrior  shared.Money
	)
	attempt := 0
	err = tx.RetryOnConflict(paymentMaxRetries, func() error {
		attempt++
		// Reset per attempt: a prior attempt that hit a version conflict must not
		// leak its outcome flags into this one.
		recordRejected = false
		gatewayFailed = false
		recorded = nil

		// The first attempt reuses the pre-flight load (already validated). A
		// retry attempt re-reads the fresh cumulative total (issue #235): the
		// version conflict proves a concurrent writer committed, so both the
		// re-validation and the key re-derivation must run against the winner's
		// state, not the stale pre-flight snapshot.
		fresh := p
		if attempt > 1 {
			reloaded, findErr := s.paymentRepo.FindByID(ctx, paymentID)
			if findErr != nil {
				return fmt.Errorf("failed to reload payment for refund retry: %w", findErr)
			}
			if reloaded == nil {
				return shared.NewDomainError(shared.ErrCodeNotFound,
					fmt.Sprintf("payment %s not found", paymentID))
			}
			fresh = reloaded
			if vErr := fresh.ValidateRefund(refundAmount); vErr != nil {
				// The winner's committed state no longer permits this refund
				// (e.g. it already refunded in full). Benign convergence, not a
				// persistence failure.
				recordRejected = true
				return vErr
			}
		}

		// Derive the gateway idempotency key from the fresh prior total (and
		// the amount — issue #235), unless the caller pinned an explicit key.
		priorForKey := fresh.RefundedAmount()
		attemptKey := input.IdempotencyKey
		if attemptKey == "" {
			attemptKey = deriveRefundIdempotencyKey(paymentID, priorForKey, refundAmount)
		}

		// Decide whether this attempt must hit the gateway (issue #235). The
		// invariant: every ledger record this invocation makes must be backed by
		// exactly one real gateway movement made by this invocation.
		//
		//   - No call yet → call.
		//   - Same key as our previous call → nothing new to execute; the
		//     previous call (real or replayed) already covers this slot.
		//   - DIFFERENT key (a concurrent writer advanced the total since our
		//     call): reason from the observed advance. If the total advanced by
		//     EXACTLY this refund's amount, the winner consumed our key slot
		//     (same prior, same amount ⇒ same derived key), meaning the gateway
		//     REPLAYED our earlier call without moving money — we must re-hit
		//     under the fresh key so our upcoming record is backed by a real
		//     movement. Any OTHER advance means the winner used different keys,
		//     so our earlier call was a real movement that is still unrecorded —
		//     re-hitting would move the money twice, so we record against the
		//     earlier movement instead.
		//
		// This reasons from the cumulative total alone; see the Refund godoc for
		// the multi-writer caveat.
		callGateway := true
		if gatewayCalled {
			if attemptKey == gatewayKey {
				callGateway = false
			} else {
				advance, subErr := priorForKey.Subtract(gatewayPrior)
				if subErr != nil || advance.Amount().Cmp(refundAmount.Amount()) != 0 {
					callGateway = false
				}
			}
		}
		if callGateway {
			// Send the RESOLVED refund amount to the gateway, not input.Amount
			// (issue #197). On a full refund input.Amount is nil; forwarding nil
			// would ask the gateway to refund "the full charge" by its own
			// reckoning while the local ledger records the amount computed here
			// (payment amount − already-refunded). Passing &refundAmount makes
			// the gateway and the ledger agree on exactly one figure.
			refundReq := &port.RefundRequest{
				TransactionID:  p.GatewayTransactionID(),
				Amount:         &refundAmount,
				Reason:         input.Reason,
				IdempotencyKey: attemptKey,
			}
			if _, gwErr := s.gateway.Refund(ctx, refundReq); gwErr != nil {
				// Gateway rejected/failed before any local write this attempt —
				// flag it so the outer error handling does not treat this as a
				// bookkeeping failure requiring reconciliation.
				gatewayFailed = true
				return fmt.Errorf("gateway refund failed: %w", gwErr)
			}
			gatewayCalled = true
			gatewayKey = attemptKey
			gatewayPrior = priorForKey
		}

		// Local bookkeeping inside a transaction. Re-load the payment through
		// the tx-scoped repository so the load + RecordRefund + Save is a single
		// check-then-act under the transaction's isolation (issue #150).
		return tx.Run(ctx, s.txManager, func(txCtx context.Context, repos tx.Repos) error {
			loaded, findErr := repos.Payments.FindByID(txCtx, paymentID)
			if findErr != nil {
				return fmt.Errorf("failed to reload payment for refund: %w", findErr)
			}
			// Defensive nil-guard (issue #197 pattern): a BYO-DB adapter
			// returning (nil, nil) for a missing payment would otherwise
			// nil-panic on RecordRefund below (issue #241).
			if loaded == nil {
				return shared.NewDomainError(shared.ErrCodeNotFound,
					fmt.Sprintf("payment %s not found", paymentID))
			}
			// Key/state consistency guard (issue #235, derived keys only): only
			// record when the persisted total still matches the total this
			// attempt derived its gateway key from. If a concurrent refund
			// committed in between, recording here could book a refund whose
			// gateway call was a deduped replay (no movement). Surface a version
			// conflict so RetryOnConflict re-runs the attempt against the fresh
			// state, where the callGateway decision above sorts real movements
			// from replays. Callers with an explicit key own that contract
			// themselves, so the guard is skipped for them (legacy behavior).
			if input.IdempotencyKey == "" && loaded.RefundedAmount().Amount().Cmp(priorForKey.Amount()) != 0 {
				return fmt.Errorf("refunded total moved from %s to %s between key derivation and record: %w",
					priorForKey.Amount().RatString(), loaded.RefundedAmount().Amount().RatString(), tx.ErrVersionConflict)
			}
			if refundErr := loaded.RecordRefund(refundAmount); refundErr != nil {
				// Domain rejection (already refunded / over-refund) — typically a
				// concurrent refund that recorded first. Flag it so the outer code
				// distinguishes this benign case from a genuine persistence failure.
				// This is NOT a version conflict, so RetryOnConflict will not retry
				// it — the error propagates straight out.
				recordRejected = true
				return refundErr
			}
			// A version conflict here (optimistic-locking backend, concurrent
			// writer committed first) is returned unwrapped so tx.IsVersionConflict
			// recognizes it and RetryOnConflict re-runs the whole attempt against
			// the winner's freshly-persisted state.
			if saveErr := repos.Payments.Save(txCtx, loaded); saveErr != nil {
				return saveErr
			}
			recorded = loaded
			return nil
		})
	})
	if err != nil {
		if gatewayFailed {
			// The gateway rejected the refund before any local write this
			// attempt; there is nothing to reconcile.
			return err
		}
		if recordRejected {
			// Re-validation rejected the refund against the concurrent winner's
			// committed state. When the winner shared this attempt's derived key,
			// the gateway collapsed the two calls into one movement and nothing
			// is owed. When it did not (winner refunded a different amount and
			// exhausted the headroom), this invocation's earlier gateway call
			// would have exceeded the charge's remaining balance — real gateways
			// reject such over-total refunds at the gateway itself (surfacing
			// above as gatewayFailed), so no unrecorded movement is expected.
			// Surface the domain error cleanly — not a reconciliation event.
			s.logger.Info("refund rejected on re-validation (concurrent refund recorded first)",
				"paymentID", paymentID,
				"refundAmount", refundAmount,
				"error", err,
			)
			return fmt.Errorf("refund not recorded (already refunded by a concurrent operation): %w", err)
		}
		if !gatewayCalled {
			// No gateway movement was made by this invocation (e.g. a reload
			// failure before the first gateway call) — nothing to reconcile.
			return err
		}
		// Gateway refund succeeded but local persistence failed — this requires
		// manual reconciliation. Refunds cannot be reversed, so we log at Error level.
		s.logger.Error("local save failed after gateway refund (MANUAL RECONCILIATION REQUIRED)",
			"paymentID", paymentID,
			"refundAmount", refundAmount,
			"error", err,
		)
		return fmt.Errorf("local save failed after gateway refund (MANUAL RECONCILIATION REQUIRED): %w", err)
	}
	p = recorded

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
		if hookErr := plugin.SafeInvoke("OnRefundHook.OnRefund", hook.Name(), func() error {
			return hook.OnRefund(refundCtx, refundAmount)
		}); hookErr != nil {
			plugin.LogNonFatalHookError(s.logger, "OnRefund hook failed", hookErr,
				"hook", hook.Name(),
				"paymentID", paymentID,
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
		// Defensive nil-guard (issue #197): contract.Repository.FindByID errors on
		// a missing contract; a BYO-DB adapter returning (nil, nil) would nil-panic
		// on agg.PaymentMethodID() below.
		if agg == nil {
			return "", shared.NewDomainError(shared.ErrCodeNotFound,
				fmt.Sprintf("contract %s not found", inv.ContractID()))
		}
		if agg.PaymentMethodID() != nil && *agg.PaymentMethodID() != "" {
			return *agg.PaymentMethodID(), nil
		}

		// Level 3: Customer-level default (via gateway). The lookup carries a
		// gateway-side customer ID, so it goes through the same resolution seam
		// as the Charge path (issue #231).
		if s.customerGateway != nil {
			gatewayCustomerID, resolveErr := s.resolveGatewayCustomerID(ctx, agg.AccountID())
			if resolveErr != nil {
				return "", resolveErr
			}
			customer, err := s.customerGateway.GetCustomer(ctx, gatewayCustomerID)
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
