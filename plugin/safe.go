package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/contract-to-cash/core/domain/shared"
)

// PluginPanicError wraps a panic value recovered from a third-party plugin hook
// invocation. It carries the offending plugin name, the hook type that was
// running, the recovered panic value, and the stack trace captured at recovery
// time. Converting a panic into a structured error lets the core apply the
// documented fatality policy (§5.4 of docs/internals/plugin-system.md): a panic
// in a veto-capable hook aborts the operation exactly like a returned error,
// while a panic in a non-fatal hook is logged (with the stack) and skipped.
type PluginPanicError struct {
	// PluginName is the Name() of the plugin whose hook panicked.
	PluginName string
	// HookType identifies the hook method that was executing (e.g. "DiscountHook").
	HookType string
	// Value is the recovered panic value (whatever was passed to panic()).
	Value any
	// Stack is the stack trace captured at recovery time (runtime/debug.Stack).
	Stack []byte
}

// Error implements the error interface.
func (e *PluginPanicError) Error() string {
	return fmt.Sprintf("plugin %q panicked in %s: %v", e.PluginName, e.HookType, e.Value)
}

// SafeInvoke runs fn, recovering any panic it raises and converting it into a
// *PluginPanicError tagged with hookType and pluginName. A normal (non-panic)
// error returned by fn is passed through unchanged, so callers can treat "the
// hook returned an error" and "the hook panicked" uniformly.
//
// The stack is captured inside the deferred recover so it reflects the panic
// site rather than this helper.
func SafeInvoke(hookType, pluginName string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PluginPanicError{
				PluginName: pluginName,
				HookType:   hookType,
				Value:      r,
				Stack:      debug.Stack(),
			}
		}
	}()
	return fn()
}

// SafeInvokeMoney is the (shared.Money, error) variant of SafeInvoke for the
// billing-calculation hooks (DiscountHook.CalculateDiscount,
// TaxHook.CalculateTax). On panic it returns a zero Money and a
// *PluginPanicError; otherwise it returns fn's own result.
func SafeInvokeMoney(hookType, pluginName string, fn func() (shared.Money, error)) (money shared.Money, err error) {
	err = SafeInvoke(hookType, pluginName, func() error {
		var innerErr error
		money, innerErr = fn()
		return innerErr
	})
	return money, err
}

// AsPanic reports whether err is, or wraps, a *PluginPanicError.
func AsPanic(err error) (*PluginPanicError, bool) {
	var pe *PluginPanicError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// LogNonFatalHookError logs a non-fatal hook failure with a consistent shape.
// A recovered panic (*PluginPanicError) is escalated to Error level with the
// captured stack attached; any other hook error is logged at Warn. The err is
// always attached under the "error" key, and callers may append their own
// contextual attrs (e.g. "invoiceID", id). A nil logger falls back to
// slog.Default().
func LogNonFatalHookError(logger *slog.Logger, msg string, err error, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	logAttrs := make([]any, 0, len(attrs)+4)
	logAttrs = append(logAttrs, "error", err)
	logAttrs = append(logAttrs, attrs...)

	level := slog.LevelWarn
	if pe, ok := AsPanic(err); ok {
		level = slog.LevelError
		logAttrs = append(logAttrs, "stack", string(pe.Stack))
	}
	logger.Log(context.Background(), level, msg, logAttrs...)
}
