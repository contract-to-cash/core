package port

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// WebhookDeduplicator checks and records webhook event IDs for idempotency.
type WebhookDeduplicator interface {
	// IsDuplicate returns true if the event ID has already been processed within TTL.
	IsDuplicate(ctx context.Context, eventID string, ttl time.Duration) (bool, error)
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
}

// NewWebhookProcessor creates a new WebhookProcessor.
// Returns an error if config contains invalid values (negative durations, negative retries).
// Zero values are valid and will use defaults.
func NewWebhookProcessor(
	handler WebhookHandler,
	deduplicator WebhookDeduplicator,
	dlq WebhookDeadLetterQueue,
	clock shared.Clock,
	config WebhookProcessorConfig,
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
	return &WebhookProcessor{
		handler:      handler,
		deduplicator: deduplicator,
		dlq:          dlq,
		clock:        clock,
		config:       config,
	}, nil
}

// ProcessWebhook parses, validates, deduplicates, and processes a webhook request.
func (p *WebhookProcessor) ProcessWebhook(
	ctx context.Context,
	req *WebhookRequest,
	handler func(ctx context.Context, event *WebhookEvent) error,
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

	// Step 3: Deduplication
	dup, err := p.deduplicator.IsDuplicate(ctx, event.ID, p.config.DeduplicationTTL)
	if err != nil {
		return fmt.Errorf("deduplication check failed: %w", err)
	}
	if dup {
		return nil // already processed
	}

	// Step 4: Event handler invocation with retry
	var lastErr error
	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		lastErr = handler(ctx, event)
		if lastErr == nil {
			return nil
		}
		// Only retry if error is retryable
		if !isWebhookRetryable(lastErr) {
			break
		}
		if attempt < p.config.MaxRetries {
			// Exponential backoff with jitter to prevent thundering herd
			base := p.config.RetryBackoff * time.Duration(1<<uint(attempt))
			jitter := time.Duration(rand.Int63n(int64(base / 10)))
			delay := base + jitter
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	// Step 5: DLQ on exhaustion
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
	}

	return lastErr
}

func isWebhookRetryable(err error) bool {
	var retryable *WebhookRetryableError
	return errors.As(err, &retryable)
}
