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
