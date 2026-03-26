package plugin

import (
	"context"
	"math/big"
	"testing"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/domain/shared"
)

// --- test helpers ---

// basePlugin provides a reusable base for test plugins.
type basePlugin struct {
	name        string
	version     string
	priority    int
	initialized bool
	shutDown    bool
}

func (b *basePlugin) Name() string    { return b.name }
func (b *basePlugin) Version() string { return b.version }
func (b *basePlugin) Initialize(ctx context.Context, config Config) error {
	b.initialized = true
	return nil
}
func (b *basePlugin) Shutdown(ctx context.Context) error {
	b.shutDown = true
	return nil
}
func (b *basePlugin) Priority() int { return b.priority }

// discountOnlyPlugin implements DiscountHook only.
type discountOnlyPlugin struct {
	basePlugin
}

func (d *discountOnlyPlugin) CalculateDiscount(ctx *CalculationContext) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}

// multiHookPlugin implements both DiscountHook and TaxHook.
type multiHookPlugin struct {
	basePlugin
}

func (m *multiHookPlugin) CalculateDiscount(ctx *CalculationContext) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}

func (m *multiHookPlugin) CalculateTax(ctx *CalculationContext) (shared.Money, error) {
	return shared.Zero(shared.CurrencyJPY), nil
}

// --- tests ---

func TestRegister_SinglePlugin(t *testing.T) {
	r := NewRegistry()
	p := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "test-discount", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := r.GetDiscountHooks()
	if len(hooks) != 1 {
		t.Fatalf("expected 1 discount hook, got %d", len(hooks))
	}
	if hooks[0].Name() != "test-discount" {
		t.Errorf("expected hook name %q, got %q", "test-discount", hooks[0].Name())
	}

	// Should not appear in tax hooks
	taxHooks := r.GetTaxHooks()
	if len(taxHooks) != 0 {
		t.Errorf("expected 0 tax hooks, got %d", len(taxHooks))
	}
}

func TestRegister_MultipleHooks(t *testing.T) {
	r := NewRegistry()
	p := &multiHookPlugin{
		basePlugin: basePlugin{name: "multi", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	discountHooks := r.GetDiscountHooks()
	if len(discountHooks) != 1 {
		t.Fatalf("expected 1 discount hook, got %d", len(discountHooks))
	}

	taxHooks := r.GetTaxHooks()
	if len(taxHooks) != 1 {
		t.Fatalf("expected 1 tax hook, got %d", len(taxHooks))
	}
}

func TestRegister_DuplicateName(t *testing.T) {
	r := NewRegistry()
	p1 := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "dup", version: "1.0.0", priority: PriorityNormal},
	}
	p2 := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "dup", version: "2.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p1); err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}

	err := r.Register(p2)
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestPriorityOrdering(t *testing.T) {
	r := NewRegistry()
	low := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "low", version: "1.0.0", priority: PriorityLow},
	}
	high := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "high", version: "1.0.0", priority: PriorityHigh},
	}

	// Register low-priority first
	if err := r.Register(low); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.Register(high); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := r.GetDiscountHooks()
	if len(hooks) != 2 {
		t.Fatalf("expected 2 discount hooks, got %d", len(hooks))
	}
	if hooks[0].Name() != "high" {
		t.Errorf("expected first hook to be %q (high priority), got %q", "high", hooks[0].Name())
	}
	if hooks[1].Name() != "low" {
		t.Errorf("expected second hook to be %q (low priority), got %q", "low", hooks[1].Name())
	}
}

func TestInitializeAll(t *testing.T) {
	r := NewRegistry()
	p := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "init-test", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configs := map[string]Config{
		"init-test": {"key": "value"},
	}
	if err := r.InitializeAll(context.Background(), configs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !p.initialized {
		t.Error("expected plugin to be initialized")
	}
}

func TestShutdownAll(t *testing.T) {
	r := NewRegistry()
	p := &discountOnlyPlugin{
		basePlugin: basePlugin{name: "shutdown-test", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := r.ShutdownAll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !p.shutDown {
		t.Error("expected plugin to be shut down")
	}
}

// Ensure unused imports are referenced in tests.
var _ = (*contract.ContractAggregate)(nil)
var _ = (*invoice.Invoice)(nil)
var _ = big.NewRat(1, 1)
