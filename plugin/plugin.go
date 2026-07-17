// Package plugin provides the plugin system for extending billing calculations,
// contract lifecycle, payment processing, metrics collection, and invoice generation.
package plugin

import "context"

// Config is a generic configuration map for plugin initialization.
type Config map[string]interface{}

// Plugin is the base interface that all plugins must implement. Every hook
// interface (DiscountHook, TaxHook, the OnContract*Hook family, etc.) embeds
// Plugin; a plugin implements Plugin plus whichever hook interfaces it needs,
// and Registry.Register classifies it into the matching hook lists via type
// assertion. Priority orders execution only among hooks of the same type —
// ordering between hook types is fixed by the core pipeline.
//
// Name and Priority are called outside the SafeInvoke panic isolation (as
// SafeInvoke arguments and during priority sorting), so they must be trivial
// and panic-free (return a field or constant).
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
