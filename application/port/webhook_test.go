package port

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	markErr     error

	// recorded tracks event IDs passed to MarkProcessed, so tests can assert
	// that the marker is written only after the handler succeeds.
	recorded []string
}

func (m *mockDeduplicator) IsDuplicate(ctx context.Context, eventID string, ttl time.Duration) (bool, error) {
	return m.isDuplicate, m.err
}

func (m *mockDeduplicator) MarkProcessed(ctx context.Context, eventID string, ttl time.Duration) error {
	if m.markErr != nil {
		return m.markErr
	}
	m.recorded = append(m.recorded, eventID)
	return nil
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

func newTestProcessor(handler WebhookHandler, dedup WebhookDeduplicator, dlq WebhookDeadLetterQueue, clock shared.Clock, config WebhookProcessorConfig, opts ...WebhookProcessorOption) *WebhookProcessor {
	p, err := NewWebhookProcessor(handler, dedup, dlq, clock, config, opts...)
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

// TestWebhookProcessor_SubTenNanoBackoff_NoPanic guards review #2: a tiny but
// valid (non-negative) RetryBackoff makes base/10 == 0 on the first retry, and
// rand.Int63n(0) panics. The retry loop must not panic for any config that
// passes Validate().
func TestWebhookProcessor_SubTenNanoBackoff_NoPanic(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_jitter", Type: WebhookEventPaymentFailed, CreatedAt: now}
	cfg := defaultConfig()
	cfg.MaxRetries = 2
	cfg.RetryBackoff = 1 // 1ns: base/10 == 0 on the first retry

	if err := cfg.Validate(); err != nil {
		t.Fatalf("config should be valid: %v", err)
	}

	callCount := 0
	handler := func(_ context.Context, _ *WebhookEvent) error {
		callCount++
		return &WebhookRetryableError{Err: fmt.Errorf("temporary failure")}
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		&mockDeduplicator{},
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		cfg,
	)

	// Must not panic; retries should still run and exhaust.
	_ = p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if callCount != cfg.MaxRetries+1 {
		t.Fatalf("expected handler called %d times, got %d", cfg.MaxRetries+1, callCount)
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

// TestWebhookProcessor_HandlerSuccess_MarksProcessed verifies the dedup marker
// is recorded only AFTER the handler succeeds (two-phase dedup).
func TestWebhookProcessor_HandlerSuccess_MarksProcessed(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_mark_ok", Type: WebhookEventPaymentSucceeded, CreatedAt: now}
	dedup := &mockDeduplicator{}

	markedBeforeHandler := false
	handler := func(_ context.Context, _ *WebhookEvent) error {
		// At the moment the handler runs, nothing must have been recorded yet.
		if len(dedup.recorded) != 0 {
			markedBeforeHandler = true
		}
		return nil
	}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		dedup,
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)
	if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if markedBeforeHandler {
		t.Fatal("dedup marker was recorded BEFORE the handler ran; must record only after success")
	}
	if len(dedup.recorded) != 1 || dedup.recorded[0] != "evt_mark_ok" {
		t.Fatalf("expected event to be marked processed once, got recorded=%v", dedup.recorded)
	}
}

// TestWebhookProcessor_HandlerFailure_NotMarked_GatewayRetryReprocessed is the
// core regression for issue #155: a handler failure must NOT record the dedup
// marker, so the gateway's redelivery is reprocessed rather than swallowed as a
// duplicate.
func TestWebhookProcessor_HandlerFailure_NotMarked_GatewayRetryReprocessed(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_155", Type: WebhookEventPaymentSucceeded, CreatedAt: now}
	// Shared deduplicator persists across both "deliveries" like a real store.
	dedup := &mockDeduplicator{}
	cfg := defaultConfig()
	cfg.MaxRetries = 1

	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		dedup,
		nil, // nil DLQ: recovery must come from gateway redelivery, not the DLQ
		shared.FixedClock{FixedTime: now},
		cfg,
	)

	// First delivery: transient failure (e.g. DB outage).
	firstCalls := 0
	failing := func(_ context.Context, _ *WebhookEvent) error {
		firstCalls++
		return fmt.Errorf("transient DB outage")
	}
	if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, failing); err == nil {
		t.Fatal("expected error from failing handler, got nil")
	}
	if firstCalls == 0 {
		t.Fatal("expected handler to be invoked on first delivery")
	}
	if len(dedup.recorded) != 0 {
		t.Fatalf("handler failed, so nothing must be marked processed; got recorded=%v", dedup.recorded)
	}

	// Gateway redelivery: because no marker was written, the check must NOT
	// treat it as a duplicate, and the (now-recovered) handler must run.
	secondCalls := 0
	recovered := func(_ context.Context, _ *WebhookEvent) error {
		secondCalls++
		return nil
	}
	if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, recovered); err != nil {
		t.Fatalf("expected redelivery to succeed, got: %v", err)
	}
	if secondCalls != 1 {
		t.Fatalf("expected recovered handler to run on redelivery (not swallowed), got %d calls", secondCalls)
	}
	if len(dedup.recorded) != 1 || dedup.recorded[0] != "evt_155" {
		t.Fatalf("expected marker recorded after successful redelivery, got recorded=%v", dedup.recorded)
	}
}

// TestWebhookProcessor_HandlerSuccess_ThenDuplicateDeduped verifies that once a
// handler succeeds and the marker is recorded, a second delivery is deduped and
// the handler is not called again.
func TestWebhookProcessor_HandlerSuccess_ThenDuplicateDeduped(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_dedup", Type: WebhookEventPaymentSucceeded, CreatedAt: now}
	dedup := &mockDeduplicator{}
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		dedup,
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
	)

	calls := 0
	handler := func(_ context.Context, _ *WebhookEvent) error {
		calls++
		return nil
	}
	if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler); err != nil {
		t.Fatalf("first delivery: unexpected error: %v", err)
	}

	// Simulate the store now reporting the event as already processed.
	dedup.isDuplicate = true
	if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler); err != nil {
		t.Fatalf("second delivery: expected nil for duplicate, got: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected handler called exactly once (second delivery deduped), got %d", calls)
	}
}

// TestWebhookProcessor_NilDLQ_Failure_LoudLog verifies that a handler failure
// with no DLQ is observable (logged at error level) and returns the error, so
// the failure is neither silent nor unrecoverable (issue #155 acceptance).
func TestWebhookProcessor_NilDLQ_Failure_LoudLog(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_loud", Type: WebhookEventPaymentSucceeded, CreatedAt: now}
	dedup := &mockDeduplicator{}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := defaultConfig()
	cfg.MaxRetries = 0
	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		dedup,
		nil, // no DLQ
		shared.FixedClock{FixedTime: now},
		cfg,
		WithWebhookLogger(logger),
	)

	handler := func(_ context.Context, _ *WebhookEvent) error {
		return fmt.Errorf("payment confirmation lost")
	}
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err == nil {
		t.Fatal("expected error when handler fails with nil DLQ, got nil")
	}
	logged := buf.String()
	if !strings.Contains(logged, "level=ERROR") {
		t.Fatalf("expected an ERROR-level log for nil-DLQ handler failure, got: %s", logged)
	}
	if !strings.Contains(logged, "evt_loud") {
		t.Fatalf("expected the failure log to include the event ID, got: %s", logged)
	}
	if len(dedup.recorded) != 0 {
		t.Fatalf("failure must not record a dedup marker, got recorded=%v", dedup.recorded)
	}
}

// TestWebhookProcessor_DLQFailurePath_NotMarked verifies the DLQ-configured
// failure path is unchanged (event sent to DLQ, original error returned) and,
// crucially, still does not record the dedup marker.
func TestWebhookProcessor_DLQFailurePath_NotMarked(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_dlq", Type: WebhookEventPaymentFailed, CreatedAt: now, RawData: []byte(`{}`)}
	dedup := &mockDeduplicator{}
	dlq := &mockDLQ{}
	cfg := defaultConfig()
	cfg.MaxRetries = 1

	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		dedup,
		dlq,
		shared.FixedClock{FixedTime: now},
		cfg,
	)
	handler := func(_ context.Context, _ *WebhookEvent) error {
		return fmt.Errorf("handler error")
	}
	err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler)
	if err == nil {
		t.Fatal("expected error after DLQ send, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "handler error") {
		t.Fatalf("expected original error returned, got: %s", got)
	}
	if len(dlq.entries) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(dlq.entries))
	}
	if len(dedup.recorded) != 0 {
		t.Fatalf("DLQ'd failure must not record a dedup marker, got recorded=%v", dedup.recorded)
	}
}

// TestWebhookProcessor_MarkProcessedFailure_NonFatal verifies that a failure to
// record the marker AFTER a successful handler is non-fatal (returns nil) and
// is logged, consistent with the at-least-once contract.
func TestWebhookProcessor_MarkProcessedFailure_NonFatal(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_markfail", Type: WebhookEventPaymentSucceeded, CreatedAt: now}
	dedup := &mockDeduplicator{markErr: fmt.Errorf("redis down")}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	p := newTestProcessor(
		&mockWebhookHandler{event: event},
		dedup,
		&mockDLQ{},
		shared.FixedClock{FixedTime: now},
		defaultConfig(),
		WithWebhookLogger(logger),
	)
	if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, noopHandler); err != nil {
		t.Fatalf("MarkProcessed failure must be non-fatal (handler already succeeded), got: %v", err)
	}
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("expected a WARN log for the dropped dedup marker, got: %s", buf.String())
	}
}

// TestWebhookProcessor_ConcurrentSameEvent documents the concurrency contract:
// two concurrent deliveries of the same event can both pass IsDuplicate before
// either marks, so both invoke the handler. This is accepted because handlers
// are required to be idempotent (at-least-once). The test also runs under -race
// to catch data races in the processor itself.
func TestWebhookProcessor_ConcurrentSameEvent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	event := &WebhookEvent{ID: "evt_concurrent", Type: WebhookEventPaymentSucceeded, CreatedAt: now}

	var mu sync.Mutex
	handlerCalls := 0
	handler := func(_ context.Context, _ *WebhookEvent) error {
		mu.Lock()
		handlerCalls++
		mu.Unlock()
		return nil
	}

	// Each goroutine uses its own processor + deduplicator (mirroring two nodes
	// racing on the same never-yet-recorded event ID); neither sees a duplicate.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := newTestProcessor(
				&mockWebhookHandler{event: event},
				&mockDeduplicator{},
				&mockDLQ{},
				shared.FixedClock{FixedTime: now},
				defaultConfig(),
			)
			if err := p.ProcessWebhook(context.Background(), &WebhookRequest{}, handler); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	// Per the documented at-least-once contract, both concurrent deliveries run
	// the handler.
	if handlerCalls != 2 {
		t.Fatalf("expected both concurrent deliveries to invoke the idempotent handler, got %d", handlerCalls)
	}
}
