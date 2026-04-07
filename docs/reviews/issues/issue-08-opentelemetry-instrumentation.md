# Add OpenTelemetry tracing and metrics instrumentation

**Labels**: `observability`, `medium-priority`, `framework`

## Problem

OpenTelemetry SDK dependencies exist in `go.mod` (indirect, from transitive deps), but there is **zero instrumentation** in the codebase:

- No span creation for invoice generation, payment processing, or event store operations
- No metric counters for business KPIs (invoices generated, payments processed, refunds issued)
- No trace context propagation through plugin hook chains
- `EventMetadata.CorrelationID` exists but is never connected to distributed traces

For a billing framework, observability is critical for:
- Debugging slow invoice generation in production
- Monitoring payment success/failure rates
- Tracking plugin hook latency
- Correlating billing events across distributed services

## Proposal

### Approach: Optional instrumentation via middleware/wrapper pattern

Do NOT make OpenTelemetry a hard dependency. Instead, provide an optional `otel` package:

```
infrastructure/otel/
  billing_service.go      -- Instrumented BillingService wrapper
  payment_service.go      -- Instrumented PaymentService wrapper
  event_store.go          -- Instrumented EventStore wrapper
  metrics.go              -- Metric definitions
```

### Example: BillingService wrapper
```go
type InstrumentedBillingService struct {
    inner  *service.BillingService
    tracer trace.Tracer
    meter  metric.Meter

    invoicesGenerated metric.Int64Counter
    invoiceLatency    metric.Float64Histogram
}

func (s *InstrumentedBillingService) GenerateInvoice(ctx context.Context, ...) (*invoice.Invoice, error) {
    ctx, span := s.tracer.Start(ctx, "billing.GenerateInvoice",
        trace.WithAttributes(
            attribute.String("contract.id", contractID),
        ))
    defer span.End()

    start := time.Now()
    inv, err := s.inner.GenerateInvoice(ctx, ...)

    s.invoiceLatency.Record(ctx, time.Since(start).Seconds())
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
    } else {
        s.invoicesGenerated.Add(ctx, 1)
        span.SetAttributes(attribute.String("invoice.id", string(inv.ID())))
    }
    return inv, err
}
```

### Proposed Metrics

| Metric | Type | Labels |
|--------|------|--------|
| `billing.invoices.generated` | Counter | contract_type, currency |
| `billing.invoices.generation_duration_seconds` | Histogram | contract_type |
| `billing.payments.processed` | Counter | method, status (success/failed) |
| `billing.payments.amount` | Histogram | method, currency |
| `billing.refunds.processed` | Counter | reason |
| `billing.credits.applied` | Counter | reason |
| `billing.plugins.hook_duration_seconds` | Histogram | hook_type, plugin_name |
| `billing.eventstore.events_appended` | Counter | event_type |
| `billing.eventstore.replay_duration_seconds` | Histogram | — |

## Acceptance Criteria

- [ ] `infrastructure/otel/` package with wrapper implementations
- [ ] OpenTelemetry as **optional** dependency (build tag or separate go module)
- [ ] Span creation for: GenerateInvoice, ProcessPayment, Refund, event store Append/Load
- [ ] At least 8 metrics covering invoices, payments, credits, plugins
- [ ] Plugin hook chain: child spans per hook execution
- [ ] CorrelationID from EventMetadata mapped to trace context
- [ ] Example in `examples/` showing how to enable instrumentation
- [ ] No performance impact when instrumentation is not enabled

## Non-Goals

- Specific exporter configuration (Jaeger, Datadog, etc.) — user's responsibility
- Dashboard templates
- Alerting rules
