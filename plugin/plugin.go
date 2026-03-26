// Package plugin provides the plugin system for extending billing calculations,
// contract lifecycle, payment processing, metrics collection, and invoice generation.
package plugin

import "context"

// Config is a generic configuration map for plugin initialization.
type Config map[string]interface{}

// Plugin is the base interface that all plugins must implement.
type Plugin interface {
	// Name returns the unique name of the plugin.
	Name() string
	// Version returns the plugin version string.
	Version() string
	// Initialize initializes the plugin with the given configuration.
	Initialize(ctx context.Context, config Config) error
	// Shutdown gracefully shuts down the plugin.
	Shutdown(ctx context.Context) error
	// Priority returns the execution priority (lower = higher priority).
	Priority() int
}
