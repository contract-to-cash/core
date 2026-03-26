package plugin

import (
	"context"
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

	return nil
}

// InitializeAll initializes all registered plugins with their respective configs.
// Plugins are initialized in priority order (lowest value first).
func (r *Registry) InitializeAll(ctx context.Context, configs map[string]Config) error {
	r.mu.RLock()
	plugins := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, p)
	}
	r.mu.RUnlock()

	sortByPriority(plugins)

	for _, p := range plugins {
		cfg := configs[p.Name()]
		if cfg == nil {
			cfg = Config{}
		}
		if err := p.Initialize(ctx, cfg); err != nil {
			return fmt.Errorf("failed to initialize plugin %q: %w", p.Name(), err)
		}
	}
	return nil
}

// ShutdownAll shuts down all registered plugins in reverse priority order.
func (r *Registry) ShutdownAll(ctx context.Context) error {
	r.mu.RLock()
	plugins := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, p)
	}
	r.mu.RUnlock()

	sortByPriority(plugins)

	// Shutdown in reverse order
	for i := len(plugins) - 1; i >= 0; i-- {
		if err := plugins[i].Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown plugin %q: %w", plugins[i].Name(), err)
		}
	}
	return nil
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

// sortByPriority sorts a slice of Plugin by Priority() in ascending order.
func sortByPriority(plugins []Plugin) {
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Priority() < plugins[j].Priority()
	})
}

// sortedCopy returns a priority-sorted copy of a hook slice.
// The type constraint ensures the element implements Plugin.
func sortedCopy[T Plugin](hooks []T) []T {
	if len(hooks) == 0 {
		return nil
	}
	cp := make([]T, len(hooks))
	copy(cp, hooks)
	sort.Slice(cp, func(i, j int) bool {
		return cp[i].Priority() < cp[j].Priority()
	})
	return cp
}
