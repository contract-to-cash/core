// pointer_isolation_test.go — see issue #96.
package balance

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// TestBalanceEntry_ExpiresAt_GetterIsDefensivelyCopied verifies that
// mutating the *time.Time returned by BalanceEntry.ExpiresAt() does NOT
// alter the entry's internal state.
func TestBalanceEntry_ExpiresAt_GetterIsDefensivelyCopied(t *testing.T) {
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := NewBalanceEntry(shared.NewAccountID(), amount, BalanceReasonGoodwill, createdAt)

	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	e.SetExpiresAt(&expires)

	got := e.ExpiresAt()
	if got == nil {
		t.Fatal("ExpiresAt must not be nil")
	}
	*got = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

	// Compare against a fresh value (not the original `expires` variable,
	// which may have been mutated above if the getter leaks its pointer).
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	again := e.ExpiresAt()
	if again == nil || !again.Equal(want) {
		t.Errorf("BalanceEntry.ExpiresAt() leaks internal pointer: got %v, want %v", again, want)
	}
}

// TestBalanceEntry_SetExpiresAt_IntakeIsDefensivelyCopied verifies that
// mutating the caller-owned *time.Time after SetExpiresAt does NOT alter
// the entry's internal state. Pattern C.
func TestBalanceEntry_SetExpiresAt_IntakeIsDefensivelyCopied(t *testing.T) {
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := NewBalanceEntry(shared.NewAccountID(), amount, BalanceReasonGoodwill, createdAt)

	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	e.SetExpiresAt(&expires)

	expires = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

	got := e.ExpiresAt()
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if got == nil || !got.Equal(want) {
		t.Errorf("SetExpiresAt does not defend at intake: got %v, want %v", got, want)
	}
}

// TestBalanceEntry_ExpiresAt_NilSafe verifies that a nil expiresAt returns
// nil without panicking.
func TestBalanceEntry_ExpiresAt_NilSafe(t *testing.T) {
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := NewBalanceEntry(shared.NewAccountID(), amount, BalanceReasonGoodwill, createdAt)

	if e.ExpiresAt() != nil {
		t.Errorf("expected nil ExpiresAt, got %v", e.ExpiresAt())
	}
}

// TestBalanceEntry_SetExpiresAt_NilIsAccepted ensures that passing nil to
// SetExpiresAt (to clear the expiration) works and ExpiresAt returns nil.
func TestBalanceEntry_SetExpiresAt_NilIsAccepted(t *testing.T) {
	amount := shared.NewMoney(big.NewRat(1000, 1), shared.CurrencyJPY)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := NewBalanceEntry(shared.NewAccountID(), amount, BalanceReasonGoodwill, createdAt)

	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	e.SetExpiresAt(&expires)
	e.SetExpiresAt(nil)

	if e.ExpiresAt() != nil {
		t.Errorf("expected nil after SetExpiresAt(nil), got %v", e.ExpiresAt())
	}
}
