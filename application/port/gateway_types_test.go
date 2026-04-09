package port

import (
	"testing"
)

func TestTransactionStatusRequiresAction_Exists(t *testing.T) {
	// TransactionStatusRequiresAction should be defined to distinguish
	// 3D Secure authentication-pending from generic "pending" status.
	status := TransactionStatusRequiresAction
	if status != "requires_action" {
		t.Errorf("expected TransactionStatusRequiresAction to be %q, got %q", "requires_action", status)
	}
}

func TestChargeResponse_HasThreeDSecureField(t *testing.T) {
	redirectURL := "https://bank.example.com/3ds"
	resp := ChargeResponse{
		TransactionID: "txn-001",
		Status:        TransactionStatusRequiresAction,
		ThreeDSecure: &ThreeDSecureResult{
			Status:      ThreeDSecureStatusRequired,
			RedirectURL: &redirectURL,
		},
	}

	if resp.ThreeDSecure == nil {
		t.Fatal("ChargeResponse.ThreeDSecure should not be nil")
	}
	if resp.ThreeDSecure.Status != ThreeDSecureStatusRequired {
		t.Errorf("expected 3DS status %q, got %q", ThreeDSecureStatusRequired, resp.ThreeDSecure.Status)
	}
	if *resp.ThreeDSecure.RedirectURL != redirectURL {
		t.Errorf("expected redirect URL %q, got %q", redirectURL, *resp.ThreeDSecure.RedirectURL)
	}
}

func TestAuthorizeResponse_HasThreeDSecureField(t *testing.T) {
	redirectURL := "https://bank.example.com/3ds-auth"
	resp := AuthorizeResponse{
		AuthorizationID: "auth-001",
		TransactionID:   "txn-002",
		Status:          TransactionStatusRequiresAction,
		ThreeDSecure: &ThreeDSecureResult{
			Status:      ThreeDSecureStatusRequired,
			RedirectURL: &redirectURL,
		},
	}

	if resp.ThreeDSecure == nil {
		t.Fatal("AuthorizeResponse.ThreeDSecure should not be nil")
	}
	if resp.ThreeDSecure.Status != ThreeDSecureStatusRequired {
		t.Errorf("expected 3DS status %q, got %q", ThreeDSecureStatusRequired, resp.ThreeDSecure.Status)
	}
	if *resp.ThreeDSecure.RedirectURL != redirectURL {
		t.Errorf("expected redirect URL %q, got %q", redirectURL, *resp.ThreeDSecure.RedirectURL)
	}
}

func TestChargeResponse_ThreeDSecureNil_WhenNotUsed(t *testing.T) {
	// When 3DS is not involved, ThreeDSecure should be nil.
	resp := ChargeResponse{
		TransactionID: "txn-003",
		Status:        TransactionStatusSucceeded,
	}

	if resp.ThreeDSecure != nil {
		t.Error("ChargeResponse.ThreeDSecure should be nil when 3DS is not used")
	}
}

func TestAuthorizeResponse_ThreeDSecureNil_WhenNotUsed(t *testing.T) {
	resp := AuthorizeResponse{
		AuthorizationID: "auth-002",
		TransactionID:   "txn-004",
		Status:          TransactionStatusAuthorized,
	}

	if resp.ThreeDSecure != nil {
		t.Error("AuthorizeResponse.ThreeDSecure should be nil when 3DS is not used")
	}
}
