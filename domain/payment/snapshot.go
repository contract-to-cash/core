// Package payment — snapshot.go
//
// Snapshot / Reconstruct pattern for Payment.
//
// DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// The types and functions in this file deliberately bypass business rules
// enforced by NewPayment / Complete / Fail / RecordRefund. They exist solely
// so that persistence adapters can rehydrate payments whose state was
// already validated in the past.
//
// Application code MUST NOT use these APIs. Use NewPayment and the
// state-transition methods (Complete, Fail, RecordRefund, ...) instead.
//
// See issue #94 for the design rationale.

package payment

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// PaymentSnapshot is the flat persistence representation of a Payment.
//
// WARNING: This type bypasses state-transition invariants. Use ONLY in
// persistence adapters.
type PaymentSnapshot struct {
	ID                   shared.PaymentID
	InvoiceID            shared.InvoiceID
	Amount               shared.Money
	RefundedAmount       shared.Money
	Method               PaymentMethod
	Status               PaymentStatus
	GatewayTransactionID string
	IdempotencyKey       string
	FailureReason        *string
	ProcessedAt          time.Time
	Metadata             map[string]string
}

// ToSnapshot returns a flat, independent copy of the payment's internal state.
//
// For persistence adapters only.
func (p *Payment) ToSnapshot() PaymentSnapshot {
	metadata := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		metadata[k] = v
	}

	var failureReason *string
	if p.failureReason != nil {
		v := *p.failureReason
		failureReason = &v
	}

	return PaymentSnapshot{
		ID:                   p.id,
		InvoiceID:            p.invoiceID,
		Amount:               p.amount,
		RefundedAmount:       p.refundedAmount,
		Method:               p.method,
		Status:               p.status,
		GatewayTransactionID: p.gatewayTransactionID,
		IdempotencyKey:       p.idempotencyKey,
		FailureReason:        failureReason,
		ProcessedAt:          p.processedAt,
		Metadata:             metadata,
	}
}

// FromSnapshot reconstructs a Payment from a persistence snapshot.
//
// Performs only the minimal validation required to detect corrupted DB rows
// (non-empty ID). Does NOT re-run state-transition invariants.
//
// For persistence adapters only.
func FromSnapshot(s PaymentSnapshot) (*Payment, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"payment snapshot: ID must not be empty")
	}

	metadata := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		metadata[k] = v
	}

	var failureReason *string
	if s.FailureReason != nil {
		v := *s.FailureReason
		failureReason = &v
	}

	return &Payment{
		id:                   s.ID,
		invoiceID:            s.InvoiceID,
		amount:               s.Amount,
		refundedAmount:       s.RefundedAmount,
		method:               s.Method,
		status:               s.Status,
		gatewayTransactionID: s.GatewayTransactionID,
		idempotencyKey:       s.IdempotencyKey,
		failureReason:        failureReason,
		processedAt:          s.ProcessedAt,
		metadata:             metadata,
	}, nil
}
