package balance

import (
	"math/big"
	"testing"
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

func BenchmarkConsume_Sequential(b *testing.B) {
	b.ReportAllocs()

	consumeAmount := shared.NewMoney(big.NewRat(100, 1), shared.CurrencyJPY)

	// Pre-allocate entries for each iteration to avoid StopTimer/StartTimer overhead
	entries := make([]*BalanceEntry, b.N)
	for i := 0; i < b.N; i++ {
		entries[i] = NewBalanceEntry(
			shared.AccountID("acc-bench"),
			shared.NewMoney(big.NewRat(1000000, 1), shared.CurrencyJPY),
			BalanceReasonManualAdjustment,
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Consume in small increments to exercise the version increment path
		for j := 0; j < 100; j++ {
			_, _ = entries[i].Consume(consumeAmount)
		}
	}
}

func BenchmarkConsume_FullyConsumed(b *testing.B) {
	b.ReportAllocs()

	fullAmount := shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY)

	// Pre-allocate entries
	entries := make([]*BalanceEntry, b.N)
	for i := 0; i < b.N; i++ {
		entries[i] = NewBalanceEntry(
			shared.AccountID("acc-bench"),
			shared.NewMoney(big.NewRat(10000, 1), shared.CurrencyJPY),
			BalanceReasonProration,
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = entries[i].Consume(fullAmount)
	}
}

func BenchmarkIsExpired(b *testing.B) {
	b.ReportAllocs()

	expires := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	entry := NewBalanceEntry(
		shared.AccountID("acc-bench"),
		shared.NewMoney(big.NewRat(5000, 1), shared.CurrencyJPY),
		BalanceReasonGoodwill,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	entry.SetExpiresAt(&expires)
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = entry.IsExpired(now)
	}
}
