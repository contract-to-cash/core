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

// TestPriorityOrdering_StableWithinSamePriority verifies that plugins sharing
// the same Priority are returned in registration order — a stable sort keeps
// the relative order deterministic instead of leaving it to the unstable-sort
// internals (issue #162 P1).
func TestPriorityOrdering_StableWithinSamePriority(t *testing.T) {
	r := NewRegistry()

	names := []string{"a", "b", "c", "d", "e"}
	for _, n := range names {
		p := &discountOnlyPlugin{
			basePlugin: basePlugin{name: n, version: "1.0.0", priority: PriorityNormal},
		}
		if err := r.Register(p); err != nil {
			t.Fatalf("unexpected error registering %q: %v", n, err)
		}
	}

	// Multiple calls must yield the identical registration order every time.
	for iter := 0; iter < 5; iter++ {
		hooks := r.GetDiscountHooks()
		if len(hooks) != len(names) {
			t.Fatalf("expected %d hooks, got %d", len(names), len(hooks))
		}
		for i, n := range names {
			if hooks[i].Name() != n {
				t.Fatalf("iter %d: expected hook %d to be %q (registration order), got %q",
					iter, i, n, hooks[i].Name())
			}
		}
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

// failingPlugin lets a test force Initialize/Shutdown to fail and records how
// many times each was invoked (for rollback assertions, issue #197).
type failingPlugin struct {
	basePlugin
	failInitialize bool
	failShutdown   bool
	initCount      int
	shutdownCount  int
}

func (f *failingPlugin) Initialize(ctx context.Context, config Config) error {
	f.initCount++
	if f.failInitialize {
		return errBoom
	}
	f.initialized = true
	return nil
}

func (f *failingPlugin) Shutdown(ctx context.Context) error {
	f.shutdownCount++
	if f.failShutdown {
		return errBoom
	}
	f.shutDown = true
	return nil
}

var errBoom = errorString("boom")

type errorString string

func (e errorString) Error() string { return string(e) }

// TestInitializeAll_PartialFailure_RollsBackInitializedPrefix verifies that when
// a later plugin's Initialize fails, the earlier-initialized plugins are shut
// down (rollback) so no half-initialized plugins leak (issue #197).
func TestInitializeAll_PartialFailure_RollsBackInitializedPrefix(t *testing.T) {
	r := NewRegistry()
	// good has the lower priority so it initializes first; bad fails second.
	good := &failingPlugin{basePlugin: basePlugin{name: "good", version: "1.0.0", priority: PriorityHigh}}
	bad := &failingPlugin{basePlugin: basePlugin{name: "bad", version: "1.0.0", priority: PriorityLow}, failInitialize: true}

	if err := r.Register(good); err != nil {
		t.Fatalf("register good: %v", err)
	}
	if err := r.Register(bad); err != nil {
		t.Fatalf("register bad: %v", err)
	}

	err := r.InitializeAll(context.Background(), nil)
	if err == nil {
		t.Fatal("expected InitializeAll to fail")
	}

	if good.initCount != 1 {
		t.Errorf("good.initCount = %d, want 1", good.initCount)
	}
	// The already-initialized "good" plugin must have been rolled back.
	if good.shutdownCount != 1 {
		t.Errorf("good.shutdownCount = %d, want 1 (rollback)", good.shutdownCount)
	}
	if !good.shutDown {
		t.Error("expected good plugin to be shut down during rollback")
	}
	// The failed plugin's Initialize failed, so it must NOT be shut down (it was
	// never added to the initialized set).
	if bad.shutdownCount != 0 {
		t.Errorf("bad.shutdownCount = %d, want 0 (never initialized)", bad.shutdownCount)
	}
}

// TestShutdownAll_ContinuesOnError verifies that ShutdownAll attempts every
// plugin even when one fails, and returns the collected error (issue #197).
func TestShutdownAll_ContinuesOnError(t *testing.T) {
	r := NewRegistry()
	first := &failingPlugin{basePlugin: basePlugin{name: "first", version: "1.0.0", priority: PriorityHigh}}
	failer := &failingPlugin{basePlugin: basePlugin{name: "failer", version: "1.0.0", priority: PriorityNormal}, failShutdown: true}
	last := &failingPlugin{basePlugin: basePlugin{name: "last", version: "1.0.0", priority: PriorityLow}}

	for _, p := range []Plugin{first, failer, last} {
		if err := r.Register(p); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	err := r.ShutdownAll(context.Background())
	if err == nil {
		t.Fatal("expected ShutdownAll to return the failing plugin's error")
	}

	// Every plugin must have been attempted despite the middle one failing.
	if !first.shutDown {
		t.Error("expected first plugin to be shut down")
	}
	if !last.shutDown {
		t.Error("expected last plugin to be shut down despite an earlier failure")
	}
	if failer.shutdownCount != 1 {
		t.Errorf("failer.shutdownCount = %d, want 1", failer.shutdownCount)
	}
}

// Ensure unused imports are referenced in tests.
var _ = (*contract.ContractAggregate)(nil)
var _ = (*invoice.Invoice)(nil)
var _ = big.NewRat(1, 1)
