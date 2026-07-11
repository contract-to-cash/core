package plugin

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"

	"github.com/contract-to-cash/core/domain/shared"
)

func TestSafeInvoke_PassesThroughNil(t *testing.T) {
	if err := SafeInvoke("Hook", "p", func() error { return nil }); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestSafeInvoke_PassesThroughError(t *testing.T) {
	sentinel := errors.New("boom")
	err := SafeInvoke("Hook", "p", func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
	if _, ok := AsPanic(err); ok {
		t.Fatalf("normal error must not be classified as panic")
	}
}

func TestSafeInvoke_RecoversPanic(t *testing.T) {
	err := SafeInvoke("DiscountHook.CalculateDiscount", "evil", func() error {
		panic("kaboom")
	})
	if err == nil {
		t.Fatal("expected error from recovered panic")
	}
	pe, ok := AsPanic(err)
	if !ok {
		t.Fatalf("expected *PluginPanicError, got %T: %v", err, err)
	}
	if pe.PluginName != "evil" {
		t.Errorf("plugin name = %q, want evil", pe.PluginName)
	}
	if pe.HookType != "DiscountHook.CalculateDiscount" {
		t.Errorf("hook type = %q", pe.HookType)
	}
	if len(pe.Stack) == 0 {
		t.Error("expected non-empty stack")
	}
	if !strings.Contains(err.Error(), "evil") || !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("error message missing context: %q", err.Error())
	}
}

func TestSafeInvoke_RecoversPanicWithErrorValue(t *testing.T) {
	inner := errors.New("panic-as-error")
	err := SafeInvoke("Hook", "p", func() error { panic(inner) })
	pe, ok := AsPanic(err)
	if !ok {
		t.Fatalf("expected panic error, got %v", err)
	}
	if gotErr, isErr := pe.Value.(error); !isErr || !errors.Is(gotErr, inner) {
		t.Errorf("recovered value not preserved: %v", pe.Value)
	}
}

func TestSafeInvokeMoney_ReturnsValue(t *testing.T) {
	want := shared.NewMoney(big.NewRat(500, 1), shared.CurrencyJPY)
	got, err := SafeInvokeMoney("TaxHook", "p", func() (shared.Money, error) {
		return want, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Amount().Cmp(want.Amount()) != 0 {
		t.Errorf("got %v, want %v", got.Amount(), want.Amount())
	}
}

func TestSafeInvokeMoney_RecoversPanic(t *testing.T) {
	got, err := SafeInvokeMoney("TaxHook.CalculateTax", "evil", func() (shared.Money, error) {
		panic("tax exploded")
	})
	if _, ok := AsPanic(err); !ok {
		t.Fatalf("expected panic error, got %v", err)
	}
	// Zero value returned on panic.
	if got != (shared.Money{}) {
		t.Errorf("expected zero Money on panic, got %v", got)
	}
}

func TestLogNonFatalHookError_EscalatesPanicToError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	panicErr := SafeInvoke("AfterChargeHook.AfterCharge", "evil", func() error { panic("nope") })
	LogNonFatalHookError(logger, "AfterCharge hook failed", panicErr, "paymentID", "pay-1")

	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("panic should log at ERROR level, got: %s", out)
	}
	if !strings.Contains(out, "stack=") {
		t.Errorf("panic log must include stack, got: %s", out)
	}
	if !strings.Contains(out, "paymentID=pay-1") {
		t.Errorf("caller attrs missing, got: %s", out)
	}
}

func TestLogNonFatalHookError_OrdinaryErrorAtWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	LogNonFatalHookError(logger, "hook failed", errors.New("plain"), "id", "x")

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("ordinary error should log at WARN, got: %s", out)
	}
	if strings.Contains(out, "stack=") {
		t.Errorf("ordinary error must not include a stack, got: %s", out)
	}
}

// --- panicking plugin lifecycle ---

type panicInitPlugin struct{ basePlugin }

func (p *panicInitPlugin) Initialize(_ context.Context, _ Config) error { panic("init boom") }

type panicShutdownPlugin struct{ basePlugin }

func (p *panicShutdownPlugin) Shutdown(_ context.Context) error { panic("shutdown boom") }

func TestInitializeAll_PanicBecomesError(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&panicInitPlugin{basePlugin{name: "bad-init"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := r.InitializeAll(context.Background(), nil)
	if err == nil {
		t.Fatal("expected InitializeAll to return an error for a panicking Initialize")
	}
	if _, ok := AsPanic(err); !ok {
		t.Errorf("expected wrapped panic error, got %v", err)
	}
	if !strings.Contains(err.Error(), "bad-init") {
		t.Errorf("error should name the plugin, got %v", err)
	}
}

func TestShutdownAll_PanicBecomesError(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&panicShutdownPlugin{basePlugin{name: "bad-shutdown"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := r.ShutdownAll(context.Background())
	if err == nil {
		t.Fatal("expected ShutdownAll to return an error for a panicking Shutdown")
	}
	if _, ok := AsPanic(err); !ok {
		t.Errorf("expected wrapped panic error, got %v", err)
	}
}
