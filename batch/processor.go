// Package batch provides batch processing abstractions.
package batch

import "context"

// BatchProcessor defines the interface for batch operations.
type BatchProcessor interface {
	// Process executes the batch operation with the given options.
	Process(ctx context.Context, opts BatchOptions) (*BatchResult, error)
}

// BatchOptions configures batch processing behavior.
type BatchOptions struct {
	// DryRun skips side effects and only reports what would be done.
	DryRun bool
	// ContinueOnError keeps processing remaining items when an error occurs.
	ContinueOnError bool
	// Concurrency is the number of items processed in parallel.
	Concurrency int
	// Limit caps how many due items a single Process run loads and processes
	// (issue #197). A positive value is passed through to the repository finder
	// (FindDueForRenewal / FindTrialsEndingBefore / FindExpired), which returns at
	// most that many rows — oldest-eligible first — so a run against a large due
	// set does not load the entire backlog into memory. Zero (the default) means
	// "no limit" and preserves the original unbounded behaviour. Schedule Process
	// on a cadence (or in a loop until BatchResult.Total < Limit) to drain a
	// backlog larger than Limit across multiple runs.
	Limit int
}

// DryRunAction records, for a dry run (BatchOptions.DryRun=true), the action a
// real run would take for a single would-succeed item (issue #242). Processors
// define their own action labels (e.g. RenewalActionRenew / RenewalActionExpire
// / RenewalActionCancel for ContractRenewalProcessor).
type DryRunAction struct {
	// ItemID identifies the item (e.g. contract ID).
	ItemID string
	// Action is the processor-specific action label.
	Action string
}

// BatchResult summarizes the outcome of a batch operation.
//
// Accounting invariant: Total == Succeeded + Failed + Skipped (issue #242).
type BatchResult struct {
	Total     int
	Succeeded int
	Failed    int
	// Skipped counts items that were not attempted because the run stopped
	// early — after a failure with ContinueOnError=false (including in-flight
	// concurrent items aborted by the internal early-stop cancellation, which
	// is not a genuine per-item failure and is not recorded in Errors), or
	// because the caller's context was cancelled. An external cancellation is
	// additionally surfaced as a non-nil error from Process, so a cancelled
	// run is never mistaken for a clean one.
	Skipped int
	Errors  []error
	// DryRunActions lists, for dry runs only, the action a real run would
	// take for each would-succeed item (one entry per Succeeded item, in
	// processing order). Real runs leave it empty.
	DryRunActions []DryRunAction
}
