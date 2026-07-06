package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/contract-to-cash/core/application/port"
	"github.com/contract-to-cash/core/application/query"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/batch"
	"github.com/contract-to-cash/core/domain/balance"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/payment"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
	"github.com/contract-to-cash/core/plugin"
	couponplugin "github.com/contract-to-cash/core/plugins/coupon"
	taxplugin "github.com/contract-to-cash/core/plugins/tax"
)

// testEnv holds all in-memory repositories and services for a test server.
type testEnv struct {
	mu            sync.Mutex // protects clock mutation
	clock         *shared.FixedClock
	eventStore    *inmemory.InMemoryEventStore
	contractRepo  *inmemory.InMemoryContractRepository
	invoiceRepo   *inmemory.InMemoryInvoiceRepository
	paymentRepo   *inmemory.InMemoryPaymentRepository
	usageRepo     *inmemory.InMemoryUsageRepository
	balanceRepo   *inmemory.InMemoryBalanceRepository
	priceRepo     *inmemory.InMemoryPriceRepository
	productRepo   *inmemory.InMemoryProductRepository
	registry      *plugin.Registry
	billingSvc    *service.BillingService
	paymentSvc    *service.PaymentService
	renewalProc   *batch.ContractRenewalProcessor
	temporalSvc   *query.TemporalQueryService
	gateway       *mockGateway
	serverTracker *serverStateTracker
}

// mockGateway is a simple PaymentGateway for testing.
type mockGateway struct {
	shouldFail atomic.Bool
}

func (g *mockGateway) ID() string                                 { return "mock-gateway" }
func (g *mockGateway) SupportedMethods() []port.PaymentMethodType { return nil }
func (g *mockGateway) Charge(_ context.Context, req *port.ChargeRequest) (*port.ChargeResponse, error) {
	if g.shouldFail.Load() {
		return nil, &port.GatewayError{Code: port.ErrorCodeCardDeclined, Message: "card declined"}
	}
	return &port.ChargeResponse{
		TransactionID: shared.GenerateID(),
		Status:        port.TransactionStatusSucceeded,
		Amount:        req.Amount,
		CreatedAt:     time.Now(),
	}, nil
}
func (g *mockGateway) Authorize(_ context.Context, _ *port.AuthorizeRequest) (*port.AuthorizeResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) Capture(_ context.Context, _ *port.CaptureRequest) (*port.CaptureResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) Void(_ context.Context, _ *port.VoidRequest) (*port.VoidResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) Refund(_ context.Context, _ *port.RefundRequest) (*port.RefundResponse, error) {
	return &port.RefundResponse{
		TransactionID: shared.GenerateID(),
		Status:        port.RefundStatusSucceeded,
	}, nil
}
func (g *mockGateway) Cancel(_ context.Context, _ *port.CancelRequest) (*port.CancelResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) GetTransaction(_ context.Context, _ string) (*port.Transaction, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) RegisterPaymentMethod(_ context.Context, _ *port.RegisterPaymentMethodRequest) (*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) DeletePaymentMethod(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}
func (g *mockGateway) GetPaymentMethod(_ context.Context, _ string) (*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *mockGateway) ListPaymentMethods(_ context.Context, _ string) ([]*port.PaymentMethodDetail, error) {
	return nil, fmt.Errorf("not implemented")
}

// mockDiscountPlugin implements DiscountHook with a fixed percentage discount.
type mockDiscountPlugin struct {
	rate     *big.Rat
	priority int
}

func (p *mockDiscountPlugin) Name() string                                        { return "mock-discount" }
func (p *mockDiscountPlugin) Version() string                                     { return "1.0.0" }
func (p *mockDiscountPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *mockDiscountPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *mockDiscountPlugin) Priority() int                                       { return p.priority }
func (p *mockDiscountPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
	return ctx.Subtotal().Multiply(p.rate), nil
}

func newTestEnv() *testEnv {
	clock := &shared.FixedClock{FixedTime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	es := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(es, clock)
	invoiceRepo := inmemory.NewInMemoryInvoiceRepository(clock)
	paymentRepo := inmemory.NewInMemoryPaymentRepository()
	usageRepo := inmemory.NewInMemoryUsageRepository()
	balanceRepo := inmemory.NewInMemoryBalanceRepository(clock)
	priceRepo := inmemory.NewInMemoryPriceRepository()
	productRepo := inmemory.NewInMemoryProductRepository()
	registry := plugin.NewRegistry()
	gw := &mockGateway{}

	billingSvc := service.NewBillingService(
		contractRepo, invoiceRepo, usageRepo,
		balance.BalanceConfig{}, priceRepo, productRepo, registry,
		// AllowPartialPayment opts generated invoices into partial payments so the
		// partial_payment runbook can pay in installments (the partial-payment
		// opt-in is now enforced — see design-decisions 3.1). Full payments in the
		// other runbooks are unaffected.
		service.BillingConfig{DaysUntilDue: 30, AllowPartialPayment: true}, clock,
		service.WithBalanceRepo(balanceRepo),
	)

	paymentSvc := service.NewPaymentService(
		gw, paymentRepo, invoiceRepo, contractRepo, es, registry, clock,
	)

	renewalProc := batch.NewContractRenewalProcessor(contractRepo, priceRepo, registry, clock, nil, nil)
	temporalSvc := query.NewTemporalQueryService(es, clock)

	return &testEnv{
		clock:        clock,
		eventStore:   es,
		contractRepo: contractRepo,
		invoiceRepo:  invoiceRepo,
		paymentRepo:  paymentRepo,
		usageRepo:    usageRepo,
		balanceRepo:  balanceRepo,
		priceRepo:    priceRepo,
		productRepo:  productRepo,
		registry:     registry,
		billingSvc:   billingSvc,
		paymentSvc:   paymentSvc,
		renewalProc:  renewalProc,
		temporalSvc:  temporalSvc,
		gateway:      gw,
	}
}

func moneyJPY(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func emptyMetadata() eventstore.EventMetadata {
	return eventstore.EventMetadata{UserID: "e2e-test"}
}

// newTestServer creates an httptest.Server with all E2E endpoints.
func newTestServer(env *testEnv) *httptest.Server {
	mux := http.NewServeMux()
	registerHandlers(mux, env)
	return httptest.NewServer(mux)
}

func registerHandlers(mux *http.ServeMux, env *testEnv) {
	// --- Contract ---
	mux.HandleFunc("POST /contracts", handleCreateContract(env))
	mux.HandleFunc("POST /contracts/{id}/activate", handleActivateContract(env))
	mux.HandleFunc("POST /contracts/{id}/suspend", handleSuspendContract(env))
	mux.HandleFunc("POST /contracts/{id}/resume", handleResumeContract(env))
	mux.HandleFunc("POST /contracts/{id}/cancel", handleCancelContract(env))
	mux.HandleFunc("POST /contracts/{id}/start-trial", handleStartTrial(env))
	mux.HandleFunc("POST /contracts/{id}/end-trial", handleEndTrial(env))
	mux.HandleFunc("GET /contracts/{id}", handleGetContract(env))

	// --- Invoice ---
	mux.HandleFunc("POST /invoices/generate", handleGenerateInvoice(env))
	mux.HandleFunc("POST /invoices/{id}/finalize", handleFinalizeInvoice(env))
	mux.HandleFunc("GET /invoices/{id}", handleGetInvoice(env))
	mux.HandleFunc("GET /contracts/{id}/invoices", handleListInvoicesByContract(env))

	// --- Payment ---
	mux.HandleFunc("POST /invoices/{id}/pay", handleProcessPayment(env))
	mux.HandleFunc("POST /payments/{id}/refund", handleRefund(env))
	mux.HandleFunc("GET /payments/{id}", handleGetPayment(env))

	// --- Credit ---
	mux.HandleFunc("POST /balances", handleCreateBalance(env))

	// --- Contract (additional) ---
	mux.HandleFunc("POST /contracts/{id}/renew", handleRenewContract(env))
	mux.HandleFunc("POST /contracts/{id}/schedule-cancellation", handleScheduleCancellation(env))
	mux.HandleFunc("POST /contracts/{id}/unschedule-cancellation", handleUnscheduleCancellation(env))
	mux.HandleFunc("POST /contracts/{id}/change-price", handleChangePrice(env))

	// --- Invoice (additional) ---
	mux.HandleFunc("POST /invoices/{id}/void", handleVoidInvoice(env))

	// --- Batch ---
	mux.HandleFunc("POST /batch/renewals", handleBatchRenewals(env))

	// --- Temporal query ---
	mux.HandleFunc("GET /contracts/{id}/history", handleContractHistory(env))
	mux.HandleFunc("GET /contracts/{id}/as-of", handleContractAsOf(env))

	// --- Clock ---
	mux.HandleFunc("POST /clock/advance", handleClockAdvance(env))

	// --- Gateway control ---
	mux.HandleFunc("POST /gateway/fail", handleGatewayFail(env))
	mux.HandleFunc("POST /gateway/succeed", handleGatewaySucceed(env))

	// --- Provisioning (server state) ---
	mux.HandleFunc("POST /plugins/provisioning/register", handleRegisterProvisioningPlugin(env))
	mux.HandleFunc("GET /servers/{contract_id}", handleGetServerState(env))

	// --- Plugin ---
	mux.HandleFunc("POST /plugins/discount/register", handleRegisterDiscountPlugin(env))
	mux.HandleFunc("POST /plugins/tax/register", handleRegisterTaxPlugin(env))
	mux.HandleFunc("POST /plugins/coupon/register", handleRegisterCouponPlugin(env))

	// --- Health ---
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// --- Contract handlers ---

type createContractRequest struct {
	AccountID    string `json:"account_id"`
	ProductID    string `json:"product_id"`
	ContractType string `json:"contract_type"`
	BillingCycle string `json:"billing_cycle"`
	Price        int64  `json:"price"`
	AutoRenew    *bool  `json:"auto_renew,omitempty"`
}

type contractResponse struct {
	ID                string `json:"id"`
	AccountID         string `json:"account_id"`
	Status            string `json:"status"`
	ContractType      string `json:"contract_type"`
	BillingCycle      string `json:"billing_cycle"`
	Price             int64  `json:"price"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
}

func contractToResponse(agg *contract.ContractAggregate) contractResponse {
	return contractResponse{
		ID:                string(agg.ContractID()),
		AccountID:         string(agg.AccountID()),
		Status:            string(agg.Status()),
		ContractType:      string(agg.GetContractType()),
		BillingCycle:      string(agg.GetInterval().ToBillingCycle()),
		Price:             agg.Price().Int64(),
		CancelAtPeriodEnd: agg.CancelAtPeriodEnd(),
	}
}

func handleCreateContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createContractRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Create a Price entity so BillingService.calculateSubtotal can look it up.
		price := pricing.NewPrice(
			shared.ProductID(req.ProductID),
			moneyJPY(req.Price),
			shared.CurrencyJPY,
			pricing.BillingCycle(req.BillingCycle),
			nil, // no usage-based pricing model
			env.clock.Now(),
		)
		if err := env.priceRepo.Save(r.Context(), price); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		contractID := shared.NewContractID()
		agg := contract.NewContractAggregate(contractID, env.clock)

		autoRenew := req.AutoRenew == nil || *req.AutoRenew // default true
		err := agg.Create(contract.CreateContractCommand{
			AccountID:    shared.AccountID(req.AccountID),
			PriceID:      price.ID(),
			ContractType: contract.ContractType(req.ContractType),
			Interval:     pricing.BillingCycleToInterval(pricing.BillingCycle(req.BillingCycle)),
			Price:        moneyJPY(req.Price),
			BasePrice:    moneyJPY(req.Price),
			AutoRenew:    autoRenew,
		}, emptyMetadata())
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, contractToResponse(agg))
	}
}

func handleActivateContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		if err := agg.Activate(emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleSuspendContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		var req struct {
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := agg.Suspend(contract.SuspensionConfiguration{
			BillingBehavior: contract.SuspensionBillingSkip,
			Reason:          req.Reason,
		}, emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		// Fire OnContractSuspend hooks
		pluginCtx := plugin.NewContext(r.Context())
		for _, h := range env.registry.GetOnContractSuspendHooks() {
			_ = h.OnContractSuspend(pluginCtx, agg)
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleResumeContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		if err := agg.Resume(emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		// Fire OnContractResume hooks
		pluginCtx := plugin.NewContext(r.Context())
		for _, h := range env.registry.GetOnContractResumeHooks() {
			_ = h.OnContractResume(pluginCtx, agg)
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleCancelContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		var req struct {
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := agg.Cancel(req.Reason, emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		// Fire OnContractCancel hooks
		pluginCtx := plugin.NewContext(r.Context())
		for _, h := range env.registry.GetOnContractCancelHooks() {
			_ = h.OnContractCancel(pluginCtx, agg)
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleStartTrial(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		var req struct {
			TrialDays   int  `json:"trial_days"`
			AutoConvert bool `json:"auto_convert"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		trialEnd := env.clock.Now().Add(time.Duration(req.TrialDays) * 24 * time.Hour)
		if err := agg.StartTrial(contract.TrialConfiguration{
			TrialEndDate: trialEnd,
			AutoConvert:  req.AutoConvert,
		}, emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleEndTrial(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		var req struct {
			Converted bool `json:"converted"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := agg.EndTrial(req.Converted, emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleGetContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

// --- Invoice handlers ---

type invoiceResponse struct {
	ID             string `json:"id"`
	AccountID      string `json:"account_id"`
	ContractID     string `json:"contract_id"`
	Status         string `json:"status"`
	Subtotal       int64  `json:"subtotal"`
	DiscountAmount int64  `json:"discount_amount"`
	TaxAmount      int64  `json:"tax_amount"`
	Total          int64  `json:"total"`
	AppliedBalance int64  `json:"applied_balance"`
	AmountDue      int64  `json:"amount_due"`
	PaidAmount     int64  `json:"paid_amount"`
}

func invoiceToResponse(inv *invoice.Invoice) invoiceResponse {
	return invoiceResponse{
		ID:             string(inv.ID()),
		AccountID:      string(inv.AccountID()),
		ContractID:     string(inv.ContractID()),
		Status:         string(inv.Status()),
		Subtotal:       inv.Subtotal().Int64(),
		DiscountAmount: inv.DiscountAmount().Int64(),
		TaxAmount:      inv.TaxAmount().Int64(),
		Total:          inv.Total().Int64(),
		AppliedBalance: inv.AppliedBalance().Int64(),
		AmountDue:      inv.AmountDue().Int64(),
		PaidAmount:     inv.PaidAmount().Int64(),
	}
}

func handleGenerateInvoice(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ContractID  string `json:"contract_id"`
			PeriodStart string `json:"period_start"`
			PeriodEnd   string `json:"period_end"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		start, err := time.Parse("2006-01-02", req.PeriodStart)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid period_start: "+err.Error())
			return
		}
		end, err := time.Parse("2006-01-02", req.PeriodEnd)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid period_end: "+err.Error())
			return
		}
		dr, err := shared.NewDateRange(start, end)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		inv, err := env.billingSvc.GenerateInvoice(r.Context(), shared.ContractID(req.ContractID), dr)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, invoiceToResponse(inv))
	}
}

func handleFinalizeInvoice(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.InvoiceID(r.PathValue("id"))
		inv, err := env.invoiceRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		if err := inv.Finalize(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.invoiceRepo.Save(r.Context(), inv); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, invoiceToResponse(inv))
	}
}

func handleGetInvoice(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.InvoiceID(r.PathValue("id"))
		inv, err := env.invoiceRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, invoiceToResponse(inv))
	}
}

func handleListInvoicesByContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		invoices, err := env.invoiceRepo.FindByContractID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]invoiceResponse, len(invoices))
		for i, inv := range invoices {
			resp[i] = invoiceToResponse(inv)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// --- Payment handlers ---

type paymentResponse struct {
	ID            string  `json:"id"`
	InvoiceID     string  `json:"invoice_id"`
	Amount        int64   `json:"amount"`
	Status        string  `json:"status"`
	Method        string  `json:"method"`
	FailureReason *string `json:"failure_reason,omitempty"`
}

func paymentToResponse(p *payment.Payment) paymentResponse {
	return paymentResponse{
		ID:            string(p.ID()),
		InvoiceID:     string(p.InvoiceID()),
		Amount:        p.Amount().Int64(),
		Status:        string(p.Status()),
		Method:        string(p.Method()),
		FailureReason: p.FailureReason(),
	}
}

func handleProcessPayment(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invoiceID := shared.InvoiceID(r.PathValue("id"))

		var req struct {
			Amount          int64  `json:"amount"`
			PaymentMethodID string `json:"payment_method_id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		input := service.ProcessPaymentInput{
			PaymentMethodID: req.PaymentMethodID,
			Amount:          moneyJPY(req.Amount),
			Currency:        shared.CurrencyJPY,
		}

		p, err := env.paymentSvc.ProcessPayment(r.Context(), invoiceID, input)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, paymentToResponse(p))
	}
}

func handleRefund(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paymentID := shared.PaymentID(r.PathValue("id"))

		var req struct {
			Amount *int64 `json:"amount,omitempty"`
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		input := service.RefundInput{
			Reason: port.RefundReason(req.Reason),
		}
		if req.Amount != nil {
			m := moneyJPY(*req.Amount)
			input.Amount = &m
		}

		if err := env.paymentSvc.Refund(r.Context(), paymentID, input); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		p, err := env.paymentRepo.FindByID(r.Context(), paymentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, paymentToResponse(p))
	}
}

func handleGetPayment(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.PaymentID(r.PathValue("id"))
		p, err := env.paymentRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, paymentToResponse(p))
	}
}

// --- Credit handlers ---

func handleCreateBalance(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID string `json:"account_id"`
			Amount    int64  `json:"amount"`
			Reason    string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		entry, _ := balance.NewBalanceEntry(
			shared.AccountID(req.AccountID),
			moneyJPY(req.Amount),
			balance.BalanceReason(req.Reason),
			env.clock.Now(),
		)
		if err := env.balanceRepo.Save(r.Context(), entry); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"id":         entry.ID(),
			"account_id": req.AccountID,
			"amount":     req.Amount,
			"reason":     req.Reason,
		})
	}
}

// --- Plugin registration handlers ---

func handleRegisterDiscountPlugin(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Rate int `json:"rate_percent"` // e.g. 10 for 10%
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		p := &mockDiscountPlugin{
			rate:     new(big.Rat).SetFrac64(int64(req.Rate), 100),
			priority: plugin.PriorityNormal,
		}
		if err := env.registry.Register(p); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
	}
}

func handleRegisterTaxPlugin(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := taxplugin.NewTaxPlugin(&taxplugin.JapaneseTaxCalculator{})
		if err := p.Initialize(r.Context(), plugin.Config{}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := env.registry.Register(p); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
	}
}

// --- Contract renewal / schedule / price change handlers ---

func handleRenewContract(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		if err := agg.RenewWithInterval(agg.GetInterval(), emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleScheduleCancellation(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		var req struct {
			Reason string `json:"reason"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := agg.ScheduleCancellation(req.Reason, emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleUnscheduleCancellation(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		if err := agg.UnscheduleCancellation(emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

func handleChangePrice(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		agg, err := env.contractRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		var req struct {
			NewPrice int64  `json:"new_price"`
			Policy   string `json:"policy"` // "immediate" or "end_of_term"
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Look up the current price to get the ProductID
		currentPrice, err := env.priceRepo.FindByID(r.Context(), agg.PriceID())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "current price not found: "+err.Error())
			return
		}

		// Create a new Price entity for the new price
		newPrice := pricing.NewPrice(
			currentPrice.ProductID(),
			moneyJPY(req.NewPrice),
			shared.CurrencyJPY,
			agg.GetInterval().ToBillingCycle(),
			nil,
			env.clock.Now(),
		)
		if err := env.priceRepo.Save(r.Context(), newPrice); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		policy := contract.ChangePolicyImmediate
		if req.Policy == "end_of_term" {
			policy = contract.ChangePolicyEndOfTerm
		}

		if err := agg.ChangePrice(newPrice.ID(), policy, nil, emptyMetadata()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.contractRepo.Save(r.Context(), agg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

// --- Invoice void handler ---

func handleVoidInvoice(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.InvoiceID(r.PathValue("id"))
		inv, err := env.invoiceRepo.FindByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}

		if err := inv.Void(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		if err := env.invoiceRepo.Save(r.Context(), inv); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, invoiceToResponse(inv))
	}
}

// --- Batch renewal handler ---

func handleBatchRenewals(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DryRun          bool `json:"dry_run"`
			ContinueOnError bool `json:"continue_on_error"`
			Concurrency     int  `json:"concurrency"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Concurrency <= 0 {
			req.Concurrency = 1
		}

		result, err := env.renewalProc.Process(r.Context(), batch.BatchOptions{
			DryRun:          req.DryRun,
			ContinueOnError: req.ContinueOnError,
			Concurrency:     req.Concurrency,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		errStrs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			errStrs[i] = e.Error()
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"total":     result.Total,
			"succeeded": result.Succeeded,
			"failed":    result.Failed,
			"errors":    errStrs,
		})
	}
}

// --- Temporal query handlers ---

func handleContractHistory(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		entries, err := env.temporalSvc.GetContractHistory(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entries)
	}
}

func handleContractAsOf(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := shared.ContractID(r.PathValue("id"))
		asOfStr := r.URL.Query().Get("at")
		asOf, err := time.Parse(time.RFC3339, asOfStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'at' parameter: "+err.Error())
			return
		}

		agg, err := env.temporalSvc.GetContractAsOf(r.Context(), id, asOf)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, contractToResponse(agg))
	}
}

// --- Clock control handler ---

func handleClockAdvance(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Days  int `json:"days"`
			Hours int `json:"hours"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		env.mu.Lock()
		env.clock.FixedTime = env.clock.FixedTime.
			AddDate(0, 0, req.Days).
			Add(time.Duration(req.Hours) * time.Hour)
		now := env.clock.FixedTime
		env.mu.Unlock()

		writeJSON(w, http.StatusOK, map[string]string{
			"now": now.Format(time.RFC3339),
		})
	}
}

// --- Gateway control handlers ---

func handleGatewayFail(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		env.gateway.shouldFail.Store(true)
		writeJSON(w, http.StatusOK, map[string]string{"status": "gateway_will_fail"})
	}
}

func handleGatewaySucceed(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		env.gateway.shouldFail.Store(false)
		writeJSON(w, http.StatusOK, map[string]string{"status": "gateway_will_succeed"})
	}
}

// --- Coupon plugin registration ---

// inMemoryCouponRepo implements couponplugin.CouponRepository for E2E tests.
type inMemoryCouponRepo struct {
	mu           sync.RWMutex
	coupons      map[couponplugin.CouponID]*couponplugin.Coupon
	usage        map[string]int // "couponID:contractID" -> count
	accountUsage map[string]int // "couponID:accountID" -> count
	redemptions  []*couponplugin.Redemption
}

func newInMemoryCouponRepo() *inMemoryCouponRepo {
	return &inMemoryCouponRepo{
		coupons:      make(map[couponplugin.CouponID]*couponplugin.Coupon),
		usage:        make(map[string]int),
		accountUsage: make(map[string]int),
	}
}

func (r *inMemoryCouponRepo) FindByCode(_ context.Context, code string) (*couponplugin.Coupon, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.coupons {
		if c.Code() == code {
			return c, nil
		}
	}
	return nil, fmt.Errorf("coupon not found: %s", code)
}

func (r *inMemoryCouponRepo) FindApplicable(_ context.Context, q couponplugin.CouponQuery) ([]*couponplugin.Coupon, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*couponplugin.Coupon
	for _, c := range r.coupons {
		if c.IsValid(q.At) && c.IsApplicableToProduct(q.ProductID) {
			result = append(result, c)
		}
	}
	return result, nil
}

func (r *inMemoryCouponRepo) Save(_ context.Context, c *couponplugin.Coupon) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.coupons[c.ID()] = c
	return nil
}

func (r *inMemoryCouponRepo) RecordUsage(_ context.Context, couponID couponplugin.CouponID, contractID shared.ContractID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(couponID) + ":" + string(contractID)
	r.usage[key]++
	return nil
}

func (r *inMemoryCouponRepo) FindUsageByAccount(_ context.Context, couponID couponplugin.CouponID, accountID shared.AccountID) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := string(couponID) + ":" + string(accountID)
	return r.accountUsage[key], nil
}

func (r *inMemoryCouponRepo) SaveRedemption(_ context.Context, redemption *couponplugin.Redemption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redemptions = append(r.redemptions, redemption)
	// Also increment account usage
	key := string(redemption.CouponID()) + ":" + string(redemption.AccountID())
	r.accountUsage[key]++
	return nil
}

func (r *inMemoryCouponRepo) FindRedemptions(_ context.Context, couponID couponplugin.CouponID, accountID *shared.AccountID) ([]*couponplugin.Redemption, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*couponplugin.Redemption
	for _, rd := range r.redemptions {
		if rd.CouponID() == couponID {
			if accountID == nil || rd.AccountID() == *accountID {
				result = append(result, rd)
			}
		}
	}
	return result, nil
}

func handleRegisterCouponPlugin(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Coupons []struct {
				Code       string   `json:"code"`
				Type       string   `json:"type"`  // "percentage" or "fixed"
				Value      float64  `json:"value"` // percentage: 10 = 10%, fixed: amount
				ValidFrom  string   `json:"valid_from"`
				ValidUntil string   `json:"valid_until"`
				MaxUses    *int     `json:"max_uses"`
				Products   []string `json:"products"`
			} `json:"coupons"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		repo := newInMemoryCouponRepo()
		for _, c := range req.Coupons {
			validFrom, err := time.Parse("2006-01-02", c.ValidFrom)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid valid_from: "+err.Error())
				return
			}
			validUntil, err := time.Parse("2006-01-02", c.ValidUntil)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid valid_until: "+err.Error())
				return
			}

			var value *big.Rat
			if c.Type == "percentage" {
				value = new(big.Rat).SetFrac64(int64(c.Value), 100)
			} else {
				value = new(big.Rat).SetInt64(int64(c.Value))
			}

			productIDs := make([]shared.ProductID, len(c.Products))
			for i, p := range c.Products {
				productIDs[i] = shared.ProductID(p)
			}
			coupon := couponplugin.NewCoupon(
				couponplugin.CouponID(shared.GenerateID()),
				c.Code,
				couponplugin.CouponType(c.Type),
				value,
				shared.CurrencyJPY,
				nil, nil,
				validFrom, validUntil,
				c.MaxUses,
				0,
				productIDs,
			)
			if err := repo.Save(r.Context(), coupon); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		p := couponplugin.NewCouponPlugin(repo, env.clock)
		if err := p.Initialize(r.Context(), plugin.Config{}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := env.registry.Register(p); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
	}
}

// --- Server provisioning plugin for E2E testing ---

// serverStateTracker tracks server states keyed by contractID.
type serverStateTracker struct {
	mu      sync.RWMutex
	servers map[string]string // contractID -> state ("none","running","stopped","terminated")
}

func newServerStateTracker() *serverStateTracker {
	return &serverStateTracker{servers: make(map[string]string)}
}

func (s *serverStateTracker) Set(contractID, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers[contractID] = state
}

func (s *serverStateTracker) Get(contractID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.servers[contractID]
	if !ok {
		return "none"
	}
	return st
}

// provisioningPlugin implements AfterCharge, OnPaymentFailed, OnContractSuspend,
// OnContractResume, and OnContractCancel hooks.
type provisioningPlugin struct {
	tracker  *serverStateTracker
	priority int
}

func (p *provisioningPlugin) Name() string                                        { return "provisioning" }
func (p *provisioningPlugin) Version() string                                     { return "1.0.0" }
func (p *provisioningPlugin) Initialize(_ context.Context, _ plugin.Config) error { return nil }
func (p *provisioningPlugin) Shutdown(_ context.Context) error                    { return nil }
func (p *provisioningPlugin) Priority() int                                       { return p.priority }

// AfterCharge: payment succeeded -> provision or confirm server
func (p *provisioningPlugin) AfterCharge(ctx *plugin.PaymentContext) error {
	cid := string(ctx.ContractID())
	if p.tracker.Get(cid) == "none" {
		p.tracker.Set(cid, "running")
	}
	return nil
}

// OnPaymentFailed: payment failed (informational only, suspend is separate)
func (p *provisioningPlugin) OnPaymentFailed(_ *plugin.PaymentContext, _ error) error {
	return nil
}

// OnContractSuspend: stop the server
func (p *provisioningPlugin) OnContractSuspend(_ *plugin.Context, c *contract.ContractAggregate) error {
	p.tracker.Set(string(c.ContractID()), "stopped")
	return nil
}

// OnContractResume: restart the server
func (p *provisioningPlugin) OnContractResume(_ *plugin.Context, c *contract.ContractAggregate) error {
	p.tracker.Set(string(c.ContractID()), "running")
	return nil
}

// OnContractCancel: terminate the server
func (p *provisioningPlugin) OnContractCancel(_ *plugin.Context, c *contract.ContractAggregate) error {
	p.tracker.Set(string(c.ContractID()), "terminated")
	return nil
}

func handleRegisterProvisioningPlugin(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tracker := newServerStateTracker()
		p := &provisioningPlugin{tracker: tracker, priority: plugin.PriorityNormal}
		if err := env.registry.Register(p); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		env.mu.Lock()
		env.serverTracker = tracker
		env.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
	}
}

func handleGetServerState(env *testEnv) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contractID := r.PathValue("contract_id")
		env.mu.Lock()
		tracker := env.serverTracker
		env.mu.Unlock()
		if tracker == nil {
			writeError(w, http.StatusNotFound, "provisioning plugin not registered")
			return
		}
		state := tracker.Get(contractID)
		writeJSON(w, http.StatusOK, map[string]string{
			"contract_id": contractID,
			"state":       state,
		})
	}
}
