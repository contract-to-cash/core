package plugin

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

// mockDiscountPlugin is a minimal DiscountHook implementation for benchmarking.
type mockDiscountPlugin struct {
	name     string
	priority int
}

func (p *mockDiscountPlugin) Name() string                                 { return p.name }
func (p *mockDiscountPlugin) Version() string                              { return "1.0.0" }
func (p *mockDiscountPlugin) Initialize(_ context.Context, _ Config) error { return nil }
func (p *mockDiscountPlugin) Shutdown(_ context.Context) error             { return nil }
func (p *mockDiscountPlugin) Priority() int                                { return p.priority }
func (p *mockDiscountPlugin) CalculateDiscount(_ *CalculationContext) (shared.Money, error) {
	return shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY), nil
}

// Compile-time check.
var _ DiscountHook = (*mockDiscountPlugin)(nil)

func BenchmarkGetDiscountHooks_10Plugins(b *testing.B) {
	b.ReportAllocs()

	reg := NewRegistry()
	for i := 0; i < 10; i++ {
		p := &mockDiscountPlugin{
			name:     fmt.Sprintf("discount-%d", i),
			priority: (i + 1) * 100,
		}
		if err := reg.Register(p); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			hooks := reg.GetDiscountHooks()
			if len(hooks) != 10 {
				b.Fatalf("expected 10 hooks, got %d", len(hooks))
			}
		}
	})
}

func BenchmarkRegister_Sequential(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		reg := NewRegistry()
		b.StartTimer()

		for j := 0; j < 20; j++ {
			p := &mockDiscountPlugin{
				name:     fmt.Sprintf("plugin-%d", j),
				priority: j * 50,
			}
			_ = reg.Register(p)
		}
	}
}

func BenchmarkGetDiscountHooks_ConcurrentReadWrite(b *testing.B) {
	b.ReportAllocs()

	reg := NewRegistry()
	for i := 0; i < 5; i++ {
		p := &mockDiscountPlugin{
			name:     fmt.Sprintf("initial-%d", i),
			priority: i * 100,
		}
		if err := reg.Register(p); err != nil {
			b.Fatal(err)
		}
	}

	// Writer goroutine registers additional plugins during benchmark
	var counter int
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				mu.Lock()
				counter++
				n := counter
				mu.Unlock()
				p := &mockDiscountPlugin{
					name:     fmt.Sprintf("concurrent-%d", n),
					priority: n * 10,
				}
				_ = reg.Register(p)
			}
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = reg.GetDiscountHooks()
		}
	})
	b.StopTimer()
	close(done)
}
