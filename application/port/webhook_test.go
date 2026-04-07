package port

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// --- Mock implementations ---

type mockWebhookHandler struct {
	event *WebhookEvent
	err   error
}

func (m *mockWebhookHandler) ParseAndVerify(ctx context.Context, req *WebhookRequest) (*WebhookEvent, error) {
	return m.event, m.err
}

type mockDeduplicator struct {
	isDuplicate bool
	err         error
}

func (m *mockDeduplicator) IsDuplicate(ctx context.Context, eventID string, ttl time.Duration) (bool, error) {
	return m.isDuplicate, m.err
}

type mockDLQ struct {
	entries []*WebhookDLQEntry
	err     error
}

func (m *mockDLQ) Send(ctx context.Context, entry *WebhookDLQEntry) error {
	m.entries = append(m.entries, entry)
	return m.err
}

// --- Helper ---

func newTestProcessor(handler WebhookHandler, dedup WebhookDeduplicator, dlq WebhookDeadLetterQueue, clock shared.Clock, config WebhookProcessorConfig) *WebhookProcessor {
	p, err := NewWebhookProcessor(handler, dedup, dlq, clock, config)
	if err != nil {
		panic(fmt.Sprintf("newTestProcessor: %v", err))
	}
	return p
}

func defaultConfig() WebhookProcessorConfig {
	return WebhookProcessorConfig{
		TimestampTolerance: 5 * time.Minute,
		DeduplicationTTL:   72 * time.Hour,
		MaxRetries:         2,
		RetryBackoff:       1 * time.Millisecond,
	}
}

func noopHandler(_ context.Context, _ *WebhookEvent) error {
	return nil
}

// --- Tests ---

func TestWebhookProcessor_TimestampTooOld(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_1",
		Type:      WebhookEventPaymentSucceeded,
		CreatedAt: now.Add(-10 * time.Minute), // 10 min ago
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
	if err == nil {
		t.Fatal("expected error for timestamp too old, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "too old") {
		t.Fatalf("expected error containing 'too old', got: %s", got)
	}
}

func TestWebhookProcessor_TimestampTooNew(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_2",
		Type:      WebhookEventPaymentSucceeded,
		CreatedAt: now.Add(10 * time.Minute), // 10 min in future
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
	if err == nil {
		t.Fatal("expected error for timestamp too new, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "too new") {
		t.Fatalf("expected error containing 'too new', got: %s", got)
	}
}

func TestWebhookProcessor_TimestampValid(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_3",
		Type:      WebhookEventPaymentSucceeded,
		CreatedAt: now.Add(-2 * time.Minute), // within tolerance
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestWebhookProcessor_DuplicateEvent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_4",
		Type:      WebhookEventPaymentSucceeded,
		CreatedAt: now,
	}
	handlerCalled := false
	handler := func(_ context.Context, _ *WebhookEvent) error {
		handlerCalled = true
		return nil
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{isDuplicate: true},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err != nil {
		t.Fatalf("expected nil for duplicate event, got: %v", err)
	}
	if handlerCalled {
		t.Fatal("handler should not be called for duplicate event")
	}
}

func TestWebhookProcessor_DeduplicatorError(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_5",
		Type:      WebhookEventPaymentSucceeded,
		CreatedAt: now,
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{err: fmt.Errorf("redis down")},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
	if err == nil {
		t.Fatal("expected error from deduplicator, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "deduplication check failed") {
		t.Fatalf("expected error containing 'deduplication check failed', got: %s", got)
	}
}

func TestWebhookProcessor_RetryableError_Exhausted(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_6",
		Type:      WebhookEventPaymentFailed,
		CreatedAt: now,
		RawData:   []byte(`{"test":"data"}`),
	}
	dlq := &mockDLQ{}
	cfg := defaultConfig()
	cfg.MaxRetries = 2

	callCount := 0
	handler := func(_ context.Context, _ *WebhookEvent) error {
		callCount++
		return &WebhookRetryableError{Err: fmt.Errorf("temporary failure")}
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		dlq,
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	// After DLQ send succeeds, ProcessWebhook returns lastErr
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	// handler called MaxRetries+1 times (initial + retries)
	expectedCalls := cfg.MaxRetries + 1
	if callCount != expectedCalls {
		t.Fatalf("expected handler called %d times, got %d", expectedCalls, callCount)
	}
	if len(dlq.entries) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(dlq.entries))
	}
	entry := dlq.entries[0]
	if entry.EventID != "evt_6" {
		t.Fatalf("expected DLQ entry EventID 'evt_6', got '%s'", entry.EventID)
	}
	if entry.EventType != WebhookEventPaymentFailed {
		t.Fatalf("expected DLQ entry EventType 'payment.failed', got '%s'", entry.EventType)
	}
	if entry.LastError != "temporary failure" {
		t.Fatalf("expected DLQ LastError 'temporary failure', got '%s'", entry.LastError)
	}
	if entry.RetryCount != cfg.MaxRetries+1 {
		t.Fatalf("expected DLQ RetryCount %d, got %d", cfg.MaxRetries+1, entry.RetryCount)
	}
	if !bytes.Equal(entry.Payload, event.RawData) {
		t.Fatalf("expected DLQ Payload %q, got %q", event.RawData, entry.Payload)
	}
	if entry.CreatedAt != now {
		t.Fatalf("expected DLQ CreatedAt %v, got %v", now, entry.CreatedAt)
	}
}

func TestWebhookProcessor_NonRetryableError_NoDLQRetry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_7",
		Type:      WebhookEventPaymentFailed,
		CreatedAt: now,
		RawData:   []byte(`{"test":"data"}`),
	}
	dlq := &mockDLQ{}
	cfg := defaultConfig()
	cfg.MaxRetries = 3

	callCount := 0
	handler := func(_ context.Context, _ *WebhookEvent) error {
		callCount++
		return fmt.Errorf("permanent failure")
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		dlq,
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err == nil {
		t.Fatal("expected error for non-retryable failure, got nil")
	}
	// Non-retryable: handler called only once, no retry
	if callCount != 1 {
		t.Fatalf("expected handler called 1 time (no retry), got %d", callCount)
	}
	// Should still send to DLQ
	if len(dlq.entries) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(dlq.entries))
	}
}

func TestWebhookProcessor_RetryableError_SucceedsOnRetry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_8",
		Type:      WebhookEventPaymentSucceeded,
		CreatedAt: now,
	}
	dlq := &mockDLQ{}
	cfg := defaultConfig()
	cfg.MaxRetries = 3

	callCount := 0
	handler := func(_ context.Context, _ *WebhookEvent) error {
		callCount++
		if callCount == 1 {
			return &WebhookRetryableError{Err: fmt.Errorf("temporary failure")}
		}
		return nil
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		dlq,
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err != nil {
		t.Fatalf("expected nil after successful retry, got: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected handler called 2 times, got %d", callCount)
	}
	if len(dlq.entries) != 0 {
		t.Fatalf("expected no DLQ entries, got %d", len(dlq.entries))
	}
}

func TestWebhookProcessor_DLQSendFailure(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_9",
		Type:      WebhookEventPaymentFailed,
		CreatedAt: now,
		RawData:   []byte(`{"test":"data"}`),
	}
	dlq := &mockDLQ{err: fmt.Errorf("DLQ unavailable")}
	cfg := defaultConfig()
	cfg.MaxRetries = 1

	handler := func(_ context.Context, _ *WebhookEvent) error {
		return fmt.Errorf("handler error")
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		dlq,
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err == nil {
		t.Fatal("expected error when DLQ send fails, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "DLQ send failed") {
		t.Fatalf("expected error containing 'DLQ send failed', got: %s", got)
	}
}

func TestWebhookProcessor_NilDLQ(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_10",
		Type:      WebhookEventPaymentFailed,
		CreatedAt: now,
	}
	cfg := defaultConfig()
	cfg.MaxRetries = 1

	handler := func(_ context.Context, _ *WebhookEvent) error {
		return fmt.Errorf("handler error")
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		nil, // no DLQ
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err == nil {
		t.Fatal("expected error when handler fails with nil DLQ, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "handler error") {
		t.Fatalf("expected error containing 'handler error', got: %s", got)
	}
}

func TestWebhookProcessor_ParseAndVerifyFailure(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p := newTestProcessor(
		&mockWebhookHandler{err: fmt.Errorf("invalid signature")},
		&mockDeduplicator{},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
	if err == nil {
		t.Fatal("expected error from ParseAndVerify, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "webhook verification failed") {
		t.Fatalf("expected error containing 'webhook verification failed', got: %s", got)
	}
}

func TestWebhookProcessor_TimestampAtExactBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tolerance := 5 * time.Minute

	t.Run("exactly at past boundary", func(t *testing.T) {
		event := &WebhookEvent{
			ID:        "evt_boundary_old",
			Type:      WebhookEventPaymentSucceeded,
			CreatedAt: now.Add(-tolerance), // exactly 5min ago
		}
		p := newTestProcessor(
			&mockWebhookHandler{event: event},
			&mockDeduplicator{},
			&mockDLQ{},
			shared.FixedClock{FixedTime: now},
			defaultConfig(),
		)
		// Before/After are strict: exactly at boundary should pass
		err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
		if err != nil {
			t.Fatalf("expected no error at exact past boundary, got: %v", err)
		}
	})

	t.Run("exactly at future boundary", func(t *testing.T) {
		event := &WebhookEvent{
			ID:        "evt_boundary_new",
			Type:      WebhookEventPaymentSucceeded,
			CreatedAt: now.Add(tolerance), // exactly 5min ahead
		}
		p := newTestProcessor(
			&mockWebhookHandler{event: event},
			&mockDeduplicator{},
			&mockDLQ{},
			shared.FixedClock{FixedTime: now},
			defaultConfig(),
		)
		err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler)
		if err != nil {
			t.Fatalf("expected no error at exact future boundary, got: %v", err)
		}
	})
}

func TestWebhookProcessor_ContextCancelDuringRetry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{
		ID:        "evt_cancel",
		Type:      WebhookEventPaymentFailed,
		CreatedAt: now,
	}
	cfg := defaultConfig()
	cfg.MaxRetries = 5
	cfg.RetryBackoff = 1 * time.Hour // large value ensures select always picks ctx.Done() over time.After

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())
	handler := func(_ context.Context, _ *WebhookEvent) error {
		callCount++
		if callCount == 1 {
			cancel() // cancel after first attempt
		}
		return &WebhookRetryableError{Err: fmt.Errorf("temporary failure")}
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	err := p.ProcessWebhook(ctx, &WebhookRequest{}, handler)
	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
