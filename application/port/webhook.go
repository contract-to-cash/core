package port

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// WebhookHandler parses and verifies incoming webhook payloads.
type WebhookHandler interface {
	// ParseAndVerify parses a raw webhook request, verifies its signature,
	// and returns a structured WebhookEvent.
	ParseAndVerify(ctx context.Context, req *WebhookRequest) (*WebhookEvent, error)
}

// WebhookEventHandler processes a single parsed, verified, non-duplicate
// webhook event. It is the business callback passed to
// [WebhookProcessor.ProcessWebhook].
//
// Handlers MUST be idempotent. WebhookProcessor provides AT-LEAST-ONCE
// delivery: the deduplication marker is recorded only AFTER the handler
// succeeds (see [WebhookDeduplicator]), so the same event may be delivered to
// the handler more than once — for example when the process crashes between a
// successful handler run and [WebhookDeduplicator.MarkProcessed], when
// MarkProcessed itself fails, or when two redeliveries of the same event race
// through the duplicate check concurrently. A handler that, say, confirms a
// payment must therefore no-op (rather than double-apply) when it observes the
// effect has already been applied.
type WebhookEventHandler func(ctx context.Context, event *WebhookEvent) error

// WebhookRequest represents a raw incoming webhook HTTP request.
type WebhookRequest struct {
	Headers map[string]string
	Body    []byte
}

// WebhookEventType identifies the kind of webhook event.
type WebhookEventType string

const (
	WebhookEventPaymentSucceeded          WebhookEventType = "payment.succeeded"
	WebhookEventPaymentFailed             WebhookEventType = "payment.failed"
	WebhookEventPaymentPending            WebhookEventType = "payment.pending"
	WebhookEventRefundSucceeded           WebhookEventType = "refund.succeeded"
	WebhookEventRefundFailed              WebhookEventType = "refund.failed"
	WebhookEventChargebackCreated         WebhookEventType = "chargeback.created"
	WebhookEventChargebackUpdated         WebhookEventType = "chargeback.updated"
	WebhookEventChargebackClosed          WebhookEventType = "chargeback.closed"
	WebhookEventPaymentMethodAttached     WebhookEventType = "payment_method.attached"
	WebhookEventPaymentMethodDetached     WebhookEventType = "payment_method.detached"
	WebhookEventPaymentMethodExpiring     WebhookEventType = "payment_method.expiring"
	WebhookEventSubscriptionCreated       WebhookEventType = "subscription.created"
	WebhookEventSubscriptionUpdated       WebhookEventType = "subscription.updated"
	WebhookEventSubscriptionCanceled      WebhookEventType = "subscription.canceled"
	WebhookEventPaymentInstructionCreated WebhookEventType = "payment_instruction.created"
	WebhookEventPaymentReceived           WebhookEventType = "payment.received"
)

// WebhookEvent represents a parsed and verified webhook event.
type WebhookEvent struct {
	ID        string
	Type      WebhookEventType
	CreatedAt time.Time
	Data      json.RawMessage
	RawData   []byte
}

// WebhookDeduplicator provides two-phase idempotency for webhook processing:
// a CHECK phase ([WebhookDeduplicator.IsDuplicate]) and a separate RECORD phase
// ([WebhookDeduplicator.MarkProcessed]).
//
// The two phases are deliberately split so that [WebhookProcessor] records the
// dedup marker ONLY AFTER the event handler succeeds. This is what makes a
// transient handler failure recoverable: because nothing is recorded on
// failure, a gateway redelivery is reprocessed rather than silently swallowed
// as a duplicate. The cost is that delivery becomes AT-LEAST-ONCE — see
// [WebhookEventHandler] for the idempotency requirement this imposes on
// handlers.
//
// Concurrency: [WebhookProcessor] does not serialize deliveries of the same
// event ID. Two concurrent redeliveries can therefore both observe
// IsDuplicate == false (neither has reached MarkProcessed yet) and both invoke
// the handler. This is accepted by design: handlers are required to be
// idempotent, so a double invocation is safe. An implementation that wants
// stronger deduplication MAY make IsDuplicate perform an atomic check-and-set
// (reserve the event ID and report a duplicate if it was already reserved);
// the interface does not require it, and callers must not rely on it.
type WebhookDeduplicator interface {
	// IsDuplicate reports whether the event ID has already been marked as
	// processed within TTL. By default this is a pure read; an implementation
	// MAY additionally reserve the ID (check-and-set) for stronger dedup, but
	// callers must not depend on that behavior.
	IsDuplicate(ctx context.Context, eventID string, ttl time.Duration) (bool, error)

	// MarkProcessed records that the event ID has been successfully processed,
	// so that a subsequent IsDuplicate within TTL returns true. WebhookProcessor
	// calls this ONLY after the handler succeeds. Implementations should make
	// the marker expire after ttl to bound storage growth.
	MarkProcessed(ctx context.Context, eventID string, ttl time.Duration) error
}

// WebhookDLQEntry represents a failed webhook event for the dead letter queue.
type WebhookDLQEntry struct {
	EventID    string
	EventType  WebhookEventType
	Payload    []byte
	LastError  string
	RetryCount int
	CreatedAt  time.Time
}

// WebhookDeadLetterQueue stores webhook events that could not be processed.
type WebhookDeadLetterQueue interface {
	Send(ctx context.Context, entry *WebhookDLQEntry) error
}

// WebhookRetryableError wraps an error to indicate it is retryable.
type WebhookRetryableError struct {
	Err error
}

func (e *WebhookRetryableError) Error() string { return e.Err.Error() }
func (e *WebhookRetryableError) Unwrap() error { return e.Err }

// WebhookProcessorConfig configures the WebhookProcessor.
type WebhookProcessorConfig struct {
	TimestampTolerance time.Duration // default: 5 minutes
	DeduplicationTTL   time.Duration // default: 72 hours
	MaxRetries         int           // default: 3
	RetryBackoff       time.Duration // default: 1 second
}

// Validate checks that all WebhookProcessorConfig fields have valid values.
// Zero values are valid and will use defaults at runtime.
func (c WebhookProcessorConfig) Validate() error {
	if c.TimestampTolerance < 0 {
		return fmt.Errorf("TimestampTolerance must not be negative, got %v", c.TimestampTolerance)
	}
	if c.DeduplicationTTL < 0 {
		return fmt.Errorf("DeduplicationTTL must not be negative, got %v", c.DeduplicationTTL)
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("MaxRetries must not be negative, got %d", c.MaxRetries)
	}
	if c.RetryBackoff < 0 {
		return fmt.Errorf("RetryBackoff must not be negative, got %v", c.RetryBackoff)
	}
	return nil
}

// WebhookProcessor processes incoming webhook events with deduplication,
// timestamp validation, retry, and DLQ.
type WebhookProcessor struct {
	handler      WebhookHandler
	deduplicator WebhookDeduplicator
	dlq          WebhookDeadLetterQueue
	clock        shared.Clock
	config       WebhookProcessorConfig
	logger       *slog.Logger
}

// WebhookProcessorOption configures optional dependencies of WebhookProcessor.
type WebhookProcessorOption func(*WebhookProcessor)

// WithWebhookLogger sets a structured logger for the WebhookProcessor.
// If not provided, slog.Default() is used. The logger is used to make
// otherwise-silent failures observable — in particular a handler failure when
// no DLQ is configured, and a dedup-marker write that fails after the handler
// already succeeded.
func WithWebhookLogger(l *slog.Logger) WebhookProcessorOption {
	return func(p *WebhookProcessor) {
		p.logger = l
	}
}

// NewWebhookProcessor creates a new WebhookProcessor.
// Returns an error if config contains invalid values (negative durations, negative retries).
// Zero values are valid and will use defaults.
//
// The dlq is optional and may be nil. When it is nil and a handler exhausts its
// retries, the processor logs the failure at error level and returns the error
// so the gateway retries; recovery no longer depends on the DLQ (see
// ProcessWebhook).
func NewWebhookProcessor(
	handler WebhookHandler,
	deduplicator WebhookDeduplicator,
	dlq WebhookDeadLetterQueue,
	clock shared.Clock,
	config WebhookProcessorConfig,
	opts ...WebhookProcessorOption,
) (*WebhookProcessor, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid webhook processor config: %w", err)
	}
	if config.TimestampTolerance == 0 {
		config.TimestampTolerance = 5 * time.Minute
	}
	if config.DeduplicationTTL == 0 {
		config.DeduplicationTTL = 72 * time.Hour
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryBackoff == 0 {
		config.RetryBackoff = 1 * time.Second
	}
	p := &WebhookProcessor{
		handler:      handler,
		deduplicator: deduplicator,
		dlq:          dlq,
		clock:        clock,
		config:       config,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p, nil
}

// ProcessWebhook parses, validates, deduplicates, and processes a webhook request.
//
// Delivery is AT-LEAST-ONCE: the deduplication marker is recorded via
// [WebhookDeduplicator.MarkProcessed] ONLY AFTER the handler succeeds. A handler
// that fails (after exhausting retries) leaves no marker, so a gateway
// redelivery is reprocessed rather than silently swallowed as a duplicate. The
// supplied handler MUST therefore be idempotent (see [WebhookEventHandler]).
//
// On handler failure the DLQ is a backstop for poison messages, not the sole
// recovery path:
//   - if a DLQ is configured, the event is sent to it and the original error is
//     returned (so the gateway also retries; a DLQ consumer should dedup by
//     event ID);
//   - if no DLQ is configured, the failure is logged at error level and the
//     error is returned so recovery proceeds via gateway redelivery.
func (p *WebhookProcessor) ProcessWebhook(
	ctx context.Context,
	req *WebhookRequest,
	handler WebhookEventHandler,
) error {
	// Step 1: ParseAndVerify
	event, err := p.handler.ParseAndVerify(ctx, req)
	if err != nil {
		return fmt.Errorf("webhook verification failed: %w", err)
	}

	// Step 2: Timestamp validation (bidirectional)
	now := p.clock.Now()
	if event.CreatedAt.Before(now.Add(-p.config.TimestampTolerance)) {
		return fmt.Errorf("webhook timestamp too old: %v", event.CreatedAt)
	}
	if event.CreatedAt.After(now.Add(p.config.TimestampTolerance)) {
		return fmt.Errorf("webhook timestamp too new: %v", event.CreatedAt)
	}

	// Step 3: Deduplication CHECK (record happens only after handler success).
	dup, err := p.deduplicator.IsDuplicate(ctx, event.ID, p.config.DeduplicationTTL)
	if err != nil {
		return fmt.Errorf("deduplication check failed: %w", err)
	}
	if dup {
		return nil // already processed
	}

	// Step 4: Event handler invocation with retry.
	var lastErr error
	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		lastErr = handler(ctx, event)
		if lastErr == nil {
			// Step 5a: handler succeeded — NOW record the dedup marker so a
			// redelivery is suppressed. A MarkProcessed failure is non-fatal:
			// the work is done, and under at-least-once a future redelivery
			// simply gets reprocessed (the handler is idempotent). Log loudly
			// so the missed marker is observable.
			if markErr := p.deduplicator.MarkProcessed(ctx, event.ID, p.config.DeduplicationTTL); markErr != nil {
				p.logger.Warn("webhook dedup marker not recorded after successful handling; a redelivery may be reprocessed",
					"event_id", event.ID,
					"event_type", event.Type,
					"error", markErr,
				)
			}
			return nil
		}
		// Only retry if error is retryable
		if !isWebhookRetryable(lastErr) {
			break
		}
		if attempt < p.config.MaxRetries {
			// Exponential backoff with jitter to prevent thundering herd.
			// Guard against base/10 == 0 (sub-10ns backoff): rand.Int63n panics
			// on a non-positive argument (review #2).
			base := p.config.RetryBackoff * time.Duration(1<<uint(attempt))
			var jitter time.Duration
			if jitterMax := int64(base / 10); jitterMax > 0 {
				jitter = time.Duration(rand.Int63n(jitterMax))
			}
			delay := base + jitter
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	// Step 5b: handler failed after retries. Deliberately do NOT record the
	// dedup marker, so a gateway redelivery is reprocessed instead of being
	// swallowed as a duplicate.
	if p.dlq != nil {
		dlqEntry := &WebhookDLQEntry{
			EventID:    event.ID,
			EventType:  event.Type,
			Payload:    event.RawData,
			LastError:  lastErr.Error(),
			RetryCount: p.config.MaxRetries + 1,
			CreatedAt:  p.clock.Now(),
		}
		if dlqErr := p.dlq.Send(ctx, dlqEntry); dlqErr != nil {
			return fmt.Errorf("DLQ send failed: %w (original: %v)", dlqErr, lastErr)
		}
		return lastErr
	}

	// No DLQ configured: log loudly so the failure is observable, then return
	// the error so the gateway retries (recovery no longer depends on the DLQ).
	p.logger.Error("webhook handler failed and no DLQ is configured; relying on gateway redelivery for recovery",
		"event_id", event.ID,
		"event_type", event.Type,
		"error", lastErr,
	)
	return lastErr
}

func isWebhookRetryable(err error) bool {
	var retryable *WebhookRetryableError
	return errors.As(err, &retryable)
}
