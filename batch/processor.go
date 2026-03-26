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
}

// BatchResult summarizes the outcome of a batch operation.
type BatchResult struct {
	Total     int
	Succeeded int
	Failed    int
	Errors    []error
}
