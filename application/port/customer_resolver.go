package port

import (
	"context"

	"github.com/contract-to-cash/core/domain/shared"
)

// CustomerIDResolver maps an internal account ID to the payment provider's
// customer ID (issue #231).
//
// PaymentService historically sent the internal shared.AccountID verbatim as
// ChargeRequest.CustomerID. That identity mapping only works on gateways that
// accept caller-chosen customer identifiers (e.g. GMO PG's MemberID, which the
// integrator registers under its own ID scheme). It does NOT work on gateways
// that mint their own customer identifiers — Stripe is the canonical example:
// a Stripe Customer ID is an opaque "cus_..." value assigned by Stripe at
// creation time, and a charge referencing an internal account ID as the
// customer will fail (or worse, hit the wrong customer if the ID happens to
// collide).
//
// Deployments on such gateways wire an implementation of this interface via
// service.WithCustomerIDResolver. PaymentService then resolves the gateway
// customer ID BEFORE every gateway call that carries one, and aborts the
// operation (before any money moves) when resolution fails. When no resolver
// is wired, the legacy identity mapping (string(accountID)) is preserved.
//
// Implementations typically look the mapping up from the same store that
// recorded the gateway customer at registration time (e.g. the row written
// when CustomerGateway.CreateCustomer returned the provider-assigned ID).
type CustomerIDResolver interface {
	// ResolveCustomerID returns the gateway-side customer ID for accountID.
	//
	// A non-nil error (or an empty resolved ID) aborts the calling payment
	// operation before the gateway is invoked — returning an error here is
	// money-safe. Implementations should return a not-found style error when
	// no mapping exists rather than falling back to the internal ID silently.
	ResolveCustomerID(ctx context.Context, accountID shared.AccountID) (string, error)
}
