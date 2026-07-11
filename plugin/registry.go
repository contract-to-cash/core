package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry manages plugin registration and retrieval.
// It is thread-safe via sync.RWMutex.
type Registry struct {
	mu sync.RWMutex

	plugins map[string]Plugin

	// Billing calculation hooks
	discountHooks         []DiscountHook
	taxHooks              []TaxHook
	invoiceLifecycleHooks []InvoiceLifecycleHook

	// Contract lifecycle hooks
	onContractCreateHooks   []OnContractCreateHook
	onContractActivateHooks []OnContractActivateHook
	onContractSuspendHooks  []OnContractSuspendHook
	onContractResumeHooks   []OnContractResumeHook
	onContractCancelHooks   []OnContractCancelHook
	onContractRenewHooks    []OnContractRenewHook
	onContractTrialEndHooks []OnContractTrialEndHook

	// Payment hooks
	beforeChargeHooks    []BeforeChargeHook
	afterChargeHooks     []AfterChargeHook
	onPaymentFailedHooks []OnPaymentFailedHook
	onRefundHooks        []OnRefundHook

	// Metrics hooks
	onContractChangeHooks   []OnContractChangeHook
	onInvoiceIssuedHooks    []OnInvoiceIssuedHook
	onPaymentProcessedHooks []OnPaymentProcessedHook

	// Invoice generation hooks
	invoiceGenerationHooks []InvoiceGenerationHook

	// Credit note hooks
	onCreditNoteIssuedHooks []OnCreditNoteIssuedHook
	onInvoiceRevisedHooks   []OnInvoiceRevisedHook
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
	}
}

// Register registers a plugin and classifies it by hook type via type assertion.
// Returns an error if a plugin with the same name is already registered.
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q is already registered", name)
	}
	r.plugins[name] = p

	// Billing calculation hooks
	if h, ok := p.(DiscountHook); ok {
		r.discountHooks = append(r.discountHooks, h)
	}
	if h, ok := p.(TaxHook); ok {
		r.taxHooks = append(r.taxHooks, h)
	}
	if h, ok := p.(InvoiceLifecycleHook); ok {
		r.invoiceLifecycleHooks = append(r.invoiceLifecycleHooks, h)
	}

	// Contract lifecycle hooks
	if h, ok := p.(OnContractCreateHook); ok {
		r.onContractCreateHooks = append(r.onContractCreateHooks, h)
	}
	if h, ok := p.(OnContractActivateHook); ok {
		r.onContractActivateHooks = append(r.onContractActivateHooks, h)
	}
	if h, ok := p.(OnContractSuspendHook); ok {
		r.onContractSuspendHooks = append(r.onContractSuspendHooks, h)
	}
	if h, ok := p.(OnContractResumeHook); ok {
		r.onContractResumeHooks = append(r.onContractResumeHooks, h)
	}
	if h, ok := p.(OnContractCancelHook); ok {
		r.onContractCancelHooks = append(r.onContractCancelHooks, h)
	}
	if h, ok := p.(OnContractRenewHook); ok {
		r.onContractRenewHooks = append(r.onContractRenewHooks, h)
	}
	if h, ok := p.(OnContractTrialEndHook); ok {
		r.onContractTrialEndHooks = append(r.onContractTrialEndHooks, h)
	}

	// Payment hooks
	if h, ok := p.(BeforeChargeHook); ok {
		r.beforeChargeHooks = append(r.beforeChargeHooks, h)
	}
	if h, ok := p.(AfterChargeHook); ok {
		r.afterChargeHooks = append(r.afterChargeHooks, h)
	}
	if h, ok := p.(OnPaymentFailedHook); ok {
		r.onPaymentFailedHooks = append(r.onPaymentFailedHooks, h)
	}
	if h, ok := p.(OnRefundHook); ok {
		r.onRefundHooks = append(r.onRefundHooks, h)
	}

	// Metrics hooks
	if h, ok := p.(OnContractChangeHook); ok {
		r.onContractChangeHooks = append(r.onContractChangeHooks, h)
	}
	if h, ok := p.(OnInvoiceIssuedHook); ok {
		r.onInvoiceIssuedHooks = append(r.onInvoiceIssuedHooks, h)
	}
	if h, ok := p.(OnPaymentProcessedHook); ok {
		r.onPaymentProcessedHooks = append(r.onPaymentProcessedHooks, h)
	}

	// Invoice generation hooks
	if h, ok := p.(InvoiceGenerationHook); ok {
		r.invoiceGenerationHooks = append(r.invoiceGenerationHooks, h)
	}

	// Credit note hooks
	if h, ok := p.(OnCreditNoteIssuedHook); ok {
		r.onCreditNoteIssuedHooks = append(r.onCreditNoteIssuedHooks, h)
	}
	if h, ok := p.(OnInvoiceRevisedHook); ok {
		r.onInvoiceRevisedHooks = append(r.onInvoiceRevisedHooks, h)
	}

	return nil
}

// InitializeAll initializes all registered plugins with their respective configs.
// Plugins are initialized in priority order (lowest value first).
//
// Partial-failure cleanup (issue #197): initialization is all-or-nothing. If any
// plugin's Initialize fails, the plugins already initialized in this call are
// rolled back by calling Shutdown on them in REVERSE order before the error is
// returned. Without this, a failed InitializeAll would leak half-initialized
// plugins holding open resources (connections, goroutines, file handles) that
// the caller has no handle to release — the caller only saw an error, not the
// set of plugins that succeeded first. Shutdown errors during rollback are
// joined onto the returned error so they are observable but do not mask the
// original initialization failure.
func (r *Registry) InitializeAll(ctx context.Context, configs map[string]Config) error {
	r.mu.RLock()
	plugins := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, p)
	}
	r.mu.RUnlock()

	sortByPriority(plugins)

	initialized := make([]Plugin, 0, len(plugins))
	for _, p := range plugins {
		cfg := configs[p.Name()]
		if cfg == nil {
			cfg = Config{}
		}
		// A panicking Initialize is converted to an error (not an unrecoverable
		// crash), so a single bad plugin fails startup cleanly instead of taking
		// the process down (issue #193).
		if err := SafeInvoke("Plugin.Initialize", p.Name(), func() error {
			return p.Initialize(ctx, cfg)
		}); err != nil {
			initErr := fmt.Errorf("failed to initialize plugin %q: %w", p.Name(), err)
			// Roll back the already-initialized prefix in reverse order so a
			// failed startup does not leak initialized plugins (issue #197).
			if rbErr := shutdownInReverse(ctx, initialized); rbErr != nil {
				return errors.Join(initErr, fmt.Errorf("rollback shutdown after init failure: %w", rbErr))
			}
			return initErr
		}
		initialized = append(initialized, p)
	}
	return nil
}

// ShutdownAll shuts down all registered plugins in reverse priority order.
//
// Unlike InitializeAll, ShutdownAll does NOT abort on the first error (issue
// #197): every plugin's Shutdown is attempted so one failing plugin cannot
// leave later ones un-shut-down (leaking their resources). All errors are
// collected and returned joined via errors.Join, matching the canonical
// contract in docs/internals/plugin-system.md §4.1.
func (r *Registry) ShutdownAll(ctx context.Context) error {
	r.mu.RLock()
	plugins := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, p)
	}
	r.mu.RUnlock()

	sortByPriority(plugins)

	return shutdownInReverse(ctx, plugins)
}

// shutdownInReverse shuts down the given plugins in reverse of their slice
// order (so a priority-ascending slice is shut down highest-priority-value
// first). It attempts EVERY plugin, collecting errors instead of aborting on
// the first, and returns them joined. A panicking Shutdown is converted to an
// error rather than being allowed to unwind the caller (issue #193).
func shutdownInReverse(ctx context.Context, plugins []Plugin) error {
	var errs []error
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		if err := SafeInvoke("Plugin.Shutdown", p.Name(), func() error {
			return p.Shutdown(ctx)
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown plugin %q: %w", p.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// --- Hook getters (return priority-sorted copies) ---

// GetDiscountHooks returns discount hooks sorted by priority.
func (r *Registry) GetDiscountHooks() []DiscountHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.discountHooks)
}

// GetTaxHooks returns tax hooks sorted by priority.
func (r *Registry) GetTaxHooks() []TaxHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.taxHooks)
}

// GetInvoiceLifecycleHooks returns invoice lifecycle hooks sorted by priority.
func (r *Registry) GetInvoiceLifecycleHooks() []InvoiceLifecycleHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.invoiceLifecycleHooks)
}

// GetOnContractCreateHooks returns contract create hooks sorted by priority.
func (r *Registry) GetOnContractCreateHooks() []OnContractCreateHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractCreateHooks)
}

// GetOnContractActivateHooks returns contract activate hooks sorted by priority.
func (r *Registry) GetOnContractActivateHooks() []OnContractActivateHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractActivateHooks)
}

// GetOnContractSuspendHooks returns contract suspend hooks sorted by priority.
func (r *Registry) GetOnContractSuspendHooks() []OnContractSuspendHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractSuspendHooks)
}

// GetOnContractResumeHooks returns contract resume hooks sorted by priority.
func (r *Registry) GetOnContractResumeHooks() []OnContractResumeHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractResumeHooks)
}

// GetOnContractCancelHooks returns contract cancel hooks sorted by priority.
func (r *Registry) GetOnContractCancelHooks() []OnContractCancelHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractCancelHooks)
}

// GetOnContractRenewHooks returns contract renew hooks sorted by priority.
func (r *Registry) GetOnContractRenewHooks() []OnContractRenewHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractRenewHooks)
}

// GetOnContractTrialEndHooks returns contract trial end hooks sorted by priority.
func (r *Registry) GetOnContractTrialEndHooks() []OnContractTrialEndHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractTrialEndHooks)
}

// GetBeforeChargeHooks returns before-charge hooks sorted by priority.
func (r *Registry) GetBeforeChargeHooks() []BeforeChargeHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.beforeChargeHooks)
}

// GetAfterChargeHooks returns after-charge hooks sorted by priority.
func (r *Registry) GetAfterChargeHooks() []AfterChargeHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.afterChargeHooks)
}

// GetOnPaymentFailedHooks returns payment-failed hooks sorted by priority.
func (r *Registry) GetOnPaymentFailedHooks() []OnPaymentFailedHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onPaymentFailedHooks)
}

// GetOnRefundHooks returns refund hooks sorted by priority.
func (r *Registry) GetOnRefundHooks() []OnRefundHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onRefundHooks)
}

// GetOnContractChangeHooks returns contract change metrics hooks sorted by priority.
func (r *Registry) GetOnContractChangeHooks() []OnContractChangeHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onContractChangeHooks)
}

// GetOnInvoiceIssuedHooks returns invoice issued metrics hooks sorted by priority.
func (r *Registry) GetOnInvoiceIssuedHooks() []OnInvoiceIssuedHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onInvoiceIssuedHooks)
}

// GetOnPaymentProcessedHooks returns payment processed metrics hooks sorted by priority.
func (r *Registry) GetOnPaymentProcessedHooks() []OnPaymentProcessedHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onPaymentProcessedHooks)
}

// GetInvoiceGenerationHooks returns invoice generation hooks sorted by priority.
func (r *Registry) GetInvoiceGenerationHooks() []InvoiceGenerationHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.invoiceGenerationHooks)
}

// GetOnCreditNoteIssuedHooks returns credit note issued hooks sorted by priority.
func (r *Registry) GetOnCreditNoteIssuedHooks() []OnCreditNoteIssuedHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onCreditNoteIssuedHooks)
}

// GetOnInvoiceRevisedHooks returns invoice revised hooks sorted by priority.
func (r *Registry) GetOnInvoiceRevisedHooks() []OnInvoiceRevisedHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedCopy(r.onInvoiceRevisedHooks)
}

// sortByPriority sorts a slice of Plugin by Priority() in ascending order.
//
// A stable sort is used so that plugins sharing the same Priority keep their
// relative input order. For InitializeAll/ShutdownAll the input is gathered
// from the plugins map (unordered), so the tie-break is only meaningful within
// a single call; see sortedCopy for the registration-order guarantee that
// applies to the per-hook getters.
func sortByPriority(plugins []Plugin) {
	sort.SliceStable(plugins, func(i, j int) bool {
		return plugins[i].Priority() < plugins[j].Priority()
	})
}

// sortedCopy returns a priority-sorted copy of a hook slice.
// The type constraint ensures the element implements Plugin.
//
// The sort is STABLE and the source slice is kept in registration order (hooks
// are appended in Register call order), so plugins that share the same Priority
// execute in the order they were registered. This makes same-priority ordering
// deterministic instead of depending on Go's unstable-sort internals (issue
// #162 P1).
func sortedCopy[T Plugin](hooks []T) []T {
	if len(hooks) == 0 {
		return nil
	}
	cp := make([]T, len(hooks))
	copy(cp, hooks)
	sort.SliceStable(cp, func(i, j int) bool {
		return cp[i].Priority() < cp[j].Priority()
	})
	return cp
}
