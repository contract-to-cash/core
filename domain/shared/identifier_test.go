package shared

import (
	"testing"
)

func TestIDGeneration_Unique(t *testing.T) {
	ids := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := NewContractID().String()
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestIDGeneration_NonEmpty(t *testing.T) {
	if NewAccountID().String() == "" {
		t.Error("AccountID should not be empty")
	}
	if NewContractID().String() == "" {
		t.Error("ContractID should not be empty")
	}
	if NewInvoiceID().String() == "" {
		t.Error("InvoiceID should not be empty")
	}
	if NewPaymentID().String() == "" {
		t.Error("PaymentID should not be empty")
	}
	if GenerateID() == "" {
		t.Error("GenerateID should not be empty")
	}
}

// TestIDGeneration_MonotonicWithinMillisecond verifies that IDs generated in a
// tight loop are STRICTLY increasing lexicographically (issue #197 follow-up).
// 1000 iterations complete in well under a millisecond on any modern machine,
// so many consecutive IDs share the same ULID timestamp — the strict ordering
// can only hold if the entropy source is monotonic within a millisecond.
// Before the fix (plain crypto/rand entropy), two same-ms ULIDs had random
// relative order and this test would flake ~50% per same-ms pair.
func TestIDGeneration_MonotonicWithinMillisecond(t *testing.T) {
	const n = 1000
	prev := generateULID()
	for i := 1; i < n; i++ {
		id := generateULID()
		if id <= prev {
			t.Fatalf("ID %d is not strictly greater than its predecessor:\n prev=%s\n   id=%s", i, prev, id)
		}
		prev = id
	}
}

// TestIDGeneration_MonotonicAcrossTypes verifies the ordering guarantee holds
// for the typed constructors too (they all share generateULID and its
// process-wide monotonic entropy source).
func TestIDGeneration_MonotonicAcrossTypes(t *testing.T) {
	a := NewInvoiceID().String()
	b := NewInvoiceID().String()
	c := NewContractID().String()
	if a >= b || b >= c {
		t.Errorf("expected strictly increasing IDs across consecutive calls: %s, %s, %s", a, b, c)
	}
}
