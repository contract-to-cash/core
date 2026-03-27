package e2e

import (
	"testing"

	"github.com/k1LoW/runn"
)

// Each test function creates its own isolated testEnv with fresh in-memory
// repositories, ensuring tests can run in parallel without interference.

// --- Smoke test (replaces 5 runbooks redundant with integration tests) ---

func TestE2E_SmokeTest(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/smoke_test.yml")
}

// --- Multi-step cross-service scenarios ---

func TestE2E_PaymentAndRefund(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/payment_and_refund.yml")
}

func TestE2E_PaymentFailure(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/payment_failure.yml")
}

func TestE2E_PartialPayment(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/partial_payment.yml")
}

// --- Contract lifecycle scenarios ---

func TestE2E_ContractRenewal(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/contract_renewal.yml")
}

func TestE2E_ScheduledCancellation(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/scheduled_cancellation.yml")
}

func TestE2E_PriceChange(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/price_change.yml")
}

func TestE2E_TrialConversion(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/trial_conversion.yml")
}

// --- Invoice scenarios ---

func TestE2E_InvoiceVoid(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/invoice_void.yml")
}

// --- Credit scenarios ---

func TestE2E_MultipleCreditsFIFO(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/multiple_credits_fifo.yml")
}

// --- Plugin scenarios ---

func TestE2E_CouponDiscount(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/coupon_discount.yml")
}

// --- Service provisioning ---

func TestE2E_ProvisioningOnPayment(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/provisioning_on_payment.yml")
}

func TestE2E_SuspendStopsServer(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/suspend_stops_server.yml")
}

func TestE2E_CancelTerminatesServer(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/cancel_terminates_server.yml")
}

// --- Event sourcing ---

func TestE2E_EventSourcingHistory(t *testing.T) {
	t.Parallel()
	env := newTestEnv()
	ts := newTestServer(env)
	t.Cleanup(ts.Close)

	runSingleRunbook(t, ts.URL, "runbooks/event_sourcing_history.yml")
}

func runSingleRunbook(t *testing.T, baseURL, path string) {
	t.Helper()
	opts := []runn.Option{
		runn.T(t),
		runn.Runner("req", baseURL),
	}

	o, err := runn.Load(path, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.RunN(t.Context()); err != nil {
		t.Fatal(err)
	}
}
