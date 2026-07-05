// event-sourcing-demo demonstrates the "time travel" capability of event sourcing.
//
// It creates a contract, applies multiple state changes over time, then uses
// TemporalQueryService to reconstruct the contract state at any past point.
// It also demonstrates snapshots for fast state recovery.
//
// Run: go run ./examples/event-sourcing-demo/
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/contract-to-cash/core/application/query"
	"github.com/contract-to-cash/core/application/service"
	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/pricing"
	"github.com/contract-to-cash/core/domain/shared"
	"github.com/contract-to-cash/core/eventstore"
	"github.com/contract-to-cash/core/infrastructure/inmemory"
)

func main() {
	ctx := context.Background()

	// We use a mutable clock so each operation happens at a different "time"
	clock := &advancingClock{current: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}
	eventStore := inmemory.NewInMemoryEventStore(clock)
	contractRepo := inmemory.NewInMemoryContractRepository(eventStore, clock)

	metadata := eventstore.EventMetadata{UserID: "admin"}
	contractID := shared.NewContractID()

	fmt.Println("=== Event Sourcing Demo: Time Travel ===")
	fmt.Println()

	// ── T1: April 1 - Create and activate contract (¥3,000/month) ──
	agg := contract.NewContractAggregate(contractID, clock)
	must("create", agg.Create(contract.CreateContractCommand{
		AccountID:    shared.AccountID("acct-001"),
		ContractType: contract.ContractTypeSubscription,
		Interval:     pricing.Monthly(),
		Price:        moneyJPY(3000),
		BasePrice:    moneyJPY(3000),
	}, metadata))
	must("activate", agg.Activate(metadata))
	must("save", contractRepo.Save(ctx, agg))
	t1 := clock.current
	printStep("T1: %s", "Contract created & activated (¥3,000/month)", t1.Format("2006-01-02"))

	// ── T2: May 1 - Price change to ¥5,000 ──
	clock.Advance(30 * 24 * time.Hour) // May 1
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("change price", agg.ChangePrice("price-5000", contract.ChangePolicyImmediate, nil, metadata))
	must("save", contractRepo.Save(ctx, agg))
	t2 := clock.current
	printStep("T2: %s", "Price changed to ¥5,000/month", t2.Format("2006-01-02"))

	// ── T3: June 1 - Suspend (payment overdue) ──
	clock.Advance(31 * 24 * time.Hour) // June 1
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("suspend", agg.Suspend(contract.SuspensionConfiguration{
		BillingBehavior: contract.SuspensionBillingSkip,
		Reason:          "payment overdue",
	}, metadata))
	must("save", contractRepo.Save(ctx, agg))
	t3 := clock.current

	// ── Create a snapshot at this point ──
	snapshotService := service.NewSnapshotService(eventStore, clock, 3) // interval=3 for demo
	must("create snapshot", snapshotService.CreateSnapshot(ctx, agg))
	printStep("T3: %s", "Suspended (payment overdue) + Snapshot created", t3.Format("2006-01-02"))

	// ── T4: July 1 - Resume ──
	clock.Advance(30 * 24 * time.Hour) // July 1
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("resume", agg.Resume(metadata))
	must("save", contractRepo.Save(ctx, agg))
	t4 := clock.current
	printStep("T4: %s", "Resumed", t4.Format("2006-01-02"))

	// ── T5: August 1 - Cancel ──
	clock.Advance(31 * 24 * time.Hour) // August 1
	agg, _ = contractRepo.FindByID(ctx, contractID)
	must("cancel", agg.Cancel("customer requested downgrade", metadata))
	must("save", contractRepo.Save(ctx, agg))
	t5 := clock.current
	printStep("T5: %s", "Cancelled (customer requested downgrade)", t5.Format("2006-01-02"))

	// ── Time Travel: Query state at each point ──
	fmt.Println()
	fmt.Println("=== Time Travel: Contract State at Each Point ===")
	fmt.Println()
	fmt.Printf("  %-14s %-14s %-14s\n", "Date", "Status", "Price")
	fmt.Printf("  %s\n", strings.Repeat("-", 42))

	queryService := query.NewTemporalQueryService(eventStore, clock)

	points := []struct {
		label string
		at    time.Time
	}{
		{"T1 (Apr 1)", t1.Add(time.Second)},
		{"T2 (May 1)", t2.Add(time.Second)},
		{"T3 (Jun 1)", t3.Add(time.Second)},
		{"T4 (Jul 1)", t4.Add(time.Second)},
		{"T5 (Aug 1)", t5.Add(time.Second)},
	}

	for _, p := range points {
		historical, err := queryService.GetContractAsOf(ctx, contractID, p.at)
		if err != nil {
			fatal("query as-of", err)
		}
		price := "N/A"
		if historical.Price().Amount() != nil {
			price = "¥" + historical.Price().Amount().RatString()
		}
		fmt.Printf("  %-14s %-14s %-14s\n", p.label, historical.Status(), price)
	}

	// ── Full Event History ──
	fmt.Println()
	fmt.Println("=== Full Event History (Audit Trail) ===")
	fmt.Println()
	history, _ := queryService.GetContractHistory(ctx, contractID)
	for i, entry := range history {
		fmt.Printf("  [%d] %-25s at %s  (by %s)\n",
			i+1, entry.EventType, entry.OccurredAt.Format("2006-01-02"), entry.UserID)
	}

	// ── Demonstrate Event Replay ──
	fmt.Println()
	fmt.Println("=== Event Replay: Reconstruct from Raw Events ===")
	fmt.Println()
	events, _ := eventStore.Load(ctx, string(contractID))
	replayAgg := contract.NewContractAggregate(contractID, clock)
	must("replay", replayAgg.LoadFromHistory(events))
	fmt.Printf("  Replayed %d events -> Status=%s, Price=¥%s\n",
		len(events), replayAgg.Status(), replayAgg.Price().Amount().RatString())

	// ── Demonstrate Snapshot Recovery ──
	fmt.Println()
	fmt.Println("=== Snapshot Recovery ===")
	fmt.Println()
	snap, _ := eventStore.LoadSnapshot(ctx, string(contractID))
	if snap != nil {
		snapAgg := contract.NewContractAggregate(contractID, clock)
		must("load snapshot", snapAgg.LoadFromSnapshot(*snap))
		fmt.Printf("  Snapshot at v%d -> Status=%s, Price=¥%s\n",
			snap.Version, snapAgg.Status(), snapAgg.Price().Amount().RatString())
		fmt.Printf("  (Only need to replay %d events instead of %d to reach current state)\n",
			len(events)-snap.Version, len(events))
	}

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

// ── Helpers ──

type advancingClock struct {
	current time.Time
}

func (c *advancingClock) Now() time.Time {
	return c.current
}

func (c *advancingClock) Advance(d time.Duration) {
	c.current = c.current.Add(d)
}

func moneyJPY(amount int64) shared.Money {
	return shared.NewMoney(new(big.Rat).SetInt64(amount), shared.CurrencyJPY)
}

func must(action string, err error) {
	if err != nil {
		fatal(action, err)
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "ERROR [%s]: %v\n", action, err)
	os.Exit(1)
}

func printStep(format, detail string, args ...interface{}) {
	fmt.Printf("  "+format+"\n", args...)
	fmt.Printf("     %s\n\n", detail)
}
