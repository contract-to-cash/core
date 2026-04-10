// Package balance — snapshot.go
//
// Snapshot / Reconstruct pattern for BalanceEntry.
//
// DANGER ZONE — PERSISTENCE ADAPTERS ONLY
//
// This file provides unified reconstitution for BalanceEntry. It supersedes
// the scattered setters (SetSourceType, SetExpiresAt, SetVersion) for the
// persistence-adapter use case, although those setters remain available for
// backward compatibility.
//
// Application code MUST NOT use these APIs. Use NewBalanceEntry and Consume
// for normal operations.
//
// See issue #94 for the design rationale.

package balance

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// BalanceEntrySnapshot is the flat persistence representation of a BalanceEntry.
//
// Note that Version is included because BalanceEntry uses optimistic locking
// (see Consume / SetVersion / LoadedVersion).
type BalanceEntrySnapshot struct {
	ID              shared.BalanceEntryID
	AccountID       shared.AccountID
	OriginalAmount  shared.Money
	RemainingAmount shared.Money
	Reason          BalanceReason
	SourceType      BalanceSourceType
	SourceID        string
	Description     string
	ExpiresAt       *time.Time
	CreatedAt       time.Time
	Version         int
}

// ToSnapshot returns a flat, independent copy of the entry's internal state.
//
// For persistence adapters only.
func (e *BalanceEntry) ToSnapshot() BalanceEntrySnapshot {
	var expiresAt *time.Time
	if e.expiresAt != nil {
		v := *e.expiresAt
		expiresAt = &v
	}
	return BalanceEntrySnapshot{
		ID:              e.id,
		AccountID:       e.accountID,
		OriginalAmount:  e.originalAmount,
		RemainingAmount: e.remainingAmount,
		Reason:          e.reason,
		SourceType:      e.sourceType,
		SourceID:        e.sourceID,
		Description:     e.description,
		ExpiresAt:       expiresAt,
		CreatedAt:       e.createdAt,
		Version:         e.version,
	}
}

// FromSnapshot reconstructs a BalanceEntry from a persistence snapshot.
//
// The loadedVersion is set to Version so that the next Save through a
// repository with optimistic locking will compare against the correct
// baseline.
//
// For persistence adapters only.
func FromSnapshot(s BalanceEntrySnapshot) (*BalanceEntry, error) {
	if s.ID == "" {
		return nil, shared.NewDomainError(shared.ErrCodeValidation,
			"balance entry snapshot: ID must not be empty")
	}

	var expiresAt *time.Time
	if s.ExpiresAt != nil {
		v := *s.ExpiresAt
		expiresAt = &v
	}

	return &BalanceEntry{
		id:              s.ID,
		accountID:       s.AccountID,
		originalAmount:  s.OriginalAmount,
		remainingAmount: s.RemainingAmount,
		reason:          s.Reason,
		sourceType:      s.SourceType,
		sourceID:        s.SourceID,
		description:     s.Description,
		expiresAt:       expiresAt,
		createdAt:       s.CreatedAt,
		version:         s.Version,
		loadedVersion:   s.Version,
	}, nil
}
