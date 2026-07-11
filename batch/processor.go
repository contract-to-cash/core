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

// BatchResult summarizes the outcome of a batch operation.
type BatchResult struct {
	Total     int
	Succeeded int
	Failed    int
	Errors    []error
}
