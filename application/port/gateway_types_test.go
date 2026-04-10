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

func TestAuthorizeRequest_HasThreeDSecureField(t *testing.T) {
	// AuthorizeRequest should support 3DS just like ChargeRequest,
	// since 3DS authentication can also be triggered during authorization.
	req := AuthorizeRequest{
		CustomerID: "cust-001",
		ThreeDSecure: &ThreeDSecureRequest{
			Required:  true,
			ReturnURL: "https://example.com/3ds-callback",
		},
	}

	if req.ThreeDSecure == nil {
		t.Fatal("AuthorizeRequest.ThreeDSecure should not be nil")
	}
	if !req.ThreeDSecure.Required {
		t.Error("expected ThreeDSecure.Required to be true")
	}
	if req.ThreeDSecure.ReturnURL != "https://example.com/3ds-callback" {
		t.Errorf("expected ReturnURL %q, got %q", "https://example.com/3ds-callback", req.ThreeDSecure.ReturnURL)
	}
}

func TestTransaction_HasThreeDSecureField(t *testing.T) {
	// Transaction should include 3DS result so that GetTransaction()
	// can return 3DS state for requires_action transactions.
	redirectURL := "https://bank.example.com/3ds-verify"
	txn := Transaction{
		ID:     "txn-005",
		Status: TransactionStatusRequiresAction,
		ThreeDSecure: &ThreeDSecureResult{
			Status:      ThreeDSecureStatusRequired,
			RedirectURL: &redirectURL,
		},
	}

	if txn.ThreeDSecure == nil {
		t.Fatal("Transaction.ThreeDSecure should not be nil")
	}
	if txn.ThreeDSecure.Status != ThreeDSecureStatusRequired {
		t.Errorf("expected 3DS status %q, got %q", ThreeDSecureStatusRequired, txn.ThreeDSecure.Status)
	}
}

func TestThreeDSecureResult_RedirectURLNil_WhenCompleted(t *testing.T) {
	// When 3DS authentication is completed, RedirectURL should be nil.
	result := ThreeDSecureResult{
		Status:      ThreeDSecureStatusSucceeded,
		RedirectURL: nil,
	}

	if result.RedirectURL != nil {
		t.Error("RedirectURL should be nil when 3DS is completed")
	}
	if result.Status != ThreeDSecureStatusSucceeded {
		t.Errorf("expected status %q, got %q", ThreeDSecureStatusSucceeded, result.Status)
	}
}
