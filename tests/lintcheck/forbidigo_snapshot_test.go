// Package lintcheck contains tests that verify lint rules configured in
// .golangci.yml are actually enforced. These tests do not exercise business
// logic; they create isolated fixture modules and run golangci-lint against
// them to assert that specific violations are reported.
//
// Issue #100: enforce that Snapshot APIs (ToSnapshot / FromSnapshot /
// InvoiceFromSnapshot / CreditNoteFromSnapshot) cannot be called from code
// outside persistence adapters, domain snapshot files, and integration tests.
package lintcheck_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestForbidigoBlocksSnapshotAPIOutsidePersistenceAdapters verifies that the
// forbidigo lint rule configured in .golangci.yml prevents calls to the
// Snapshot API family from code outside persistence adapters.
//
// The test sets up an isolated two-package Go module in a temp directory:
//
//	dom/      — mimics a domain package, defines Snapshot APIs and a fake
//	            aggregate with LoadFromSnapshot (which must NOT be flagged
//	            because it belongs to the event-sourced ContractAggregate
//	            API family, not the state-based snapshot family).
//	consumer/ — imports dom and exercises both the forbidden cross-package
//	            calls and the LoadFromSnapshot negative case.
//
// The fixture deliberately exercises the shape that real application code
// would take (package-qualified identifiers like `dom.FromSnapshot(...)`),
// because forbidigo v2 with analyze-types=false matches the textual identifier
// expression. An earlier iteration of this test used only intra-package bare
// identifiers and failed to catch a regression where the anchored pattern
// `^(FromSnapshot|CreditNoteFromSnapshot)$` was silently failing to match
// cross-package calls — the primary threat model.
//
// This test exists because the Snapshot APIs deliberately bypass construction-
// time invariants and must only be used by persistence adapters. Enforcement
// via documentation alone is insufficient; CI must fail when application code
// accidentally calls these APIs.
func TestForbidigoBlocksSnapshotAPIOutsidePersistenceAdapters(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not installed; skipping lint rule test")
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("failed to find repo root: %v", err)
	}
	configBytes, err := os.ReadFile(filepath.Join(repoRoot, ".golangci.yml"))
	if err != nil {
		t.Fatalf("failed to read .golangci.yml: %v", err)
	}

	tmp := t.TempDir()

	// Minimal module so golangci-lint treats tmp as a Go module.
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module lintfixture\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".golangci.yml"), configBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// dom package: mimics a real domain package. Names match the real API
	// shape: state-based packages expose `FromSnapshot`, `domain/invoice`
	// exposes `InvoiceFromSnapshot` and `CreditNoteFromSnapshot`, and
	// ContractAggregate exposes a method `LoadFromSnapshot`.
	if err := os.MkdirAll(filepath.Join(tmp, "dom"), 0o755); err != nil {
		t.Fatal(err)
	}
	domFixture := `package dom

type Snap struct{}

type Entity struct{}

type Agg struct{}

func (Entity) ToSnapshot() Snap { return Snap{} }

func (Agg) LoadFromSnapshot(Snap) error { return nil }

func FromSnapshot(Snap) Entity           { return Entity{} }
func InvoiceFromSnapshot(Snap) Entity    { return Entity{} }
func CreditNoteFromSnapshot(Snap) Entity { return Entity{} }
`
	if err := os.WriteFile(filepath.Join(tmp, "dom", "dom.go"), []byte(domFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	// consumer package: cross-package calls are the primary threat model.
	// The identifiers seen by forbidigo are `e.ToSnapshot`, `dom.FromSnapshot`,
	// `dom.InvoiceFromSnapshot`, `dom.CreditNoteFromSnapshot`, and
	// `agg.LoadFromSnapshot` — the last must NOT be flagged.
	if err := os.MkdirAll(filepath.Join(tmp, "consumer"), 0o755); err != nil {
		t.Fatal(err)
	}
	consumerFixture := `package consumer

import "lintfixture/dom"

func ViolatesToSnapshot() {
	e := dom.Entity{}
	_ = e.ToSnapshot()
}

func ViolatesFromSnapshot() {
	_ = dom.FromSnapshot(dom.Snap{})
}

func ViolatesInvoiceFromSnapshot() {
	_ = dom.InvoiceFromSnapshot(dom.Snap{})
}

func ViolatesCreditNoteFromSnapshot() {
	_ = dom.CreditNoteFromSnapshot(dom.Snap{})
}

// LoadFromSnapshotIsAllowed exercises the negative case: ContractAggregate's
// event-sourced LoadFromSnapshot method must NOT be flagged, because the
// forbidigo rule targets only the state-based Snapshot API family.
func LoadFromSnapshotIsAllowed() error {
	agg := dom.Agg{}
	return agg.LoadFromSnapshot(dom.Snap{})
}
`
	if err := os.WriteFile(filepath.Join(tmp, "consumer", "consumer.go"), []byte(consumerFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("golangci-lint", "run", "--config", ".golangci.yml", "./...")
	cmd.Dir = tmp
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	combined := stdout.String() + stderr.String()
	t.Logf("golangci-lint output:\n%s", combined)

	// Non-zero exit is expected because forbidigo violations are reported.
	if runErr == nil {
		t.Fatalf("golangci-lint exited 0; expected forbidigo violations. output:\n%s", combined)
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("unexpected error running golangci-lint: %v\noutput:\n%s", runErr, combined)
	}

	// Extract only the forbidigo lines from the output. Other linters (e.g.
	// `unused`) may also emit issues; those are ignored by the assertions
	// below. The format is `path:line:col: use of `...` forbidden because "..." (forbidigo)`.
	var forbidigoLines []string
	for _, line := range strings.Split(combined, "\n") {
		if strings.Contains(line, "(forbidigo)") {
			forbidigoLines = append(forbidigoLines, line)
		}
	}
	forbidigoJoined := strings.Join(forbidigoLines, "\n")

	// Positive assertions: every forbidden call site must be flagged. Each
	// expectation pins a specific identifier to a specific fixture file on
	// the SAME LINE of golangci-lint output. Checking file + identifier
	// separately against the joined output would allow a regression where,
	// for example, the identifier is flagged in dom/dom.go (the declaration
	// site) while consumer/consumer.go (the usage site) appears on another
	// line — the assertion would still pass silently. Requiring same-line
	// match pins the location precisely and catches exclusion drift.
	type expected struct {
		desc       string
		identifier string // must appear inside backticks in the forbidigo message
		file       string // must appear as the file path prefix on the same line
	}
	expectedViolations := []expected{
		{"method form on receiver", "e.ToSnapshot", "consumer/consumer.go"},
		{"cross-package FromSnapshot", "dom.FromSnapshot", "consumer/consumer.go"},
		{"cross-package InvoiceFromSnapshot", "dom.InvoiceFromSnapshot", "consumer/consumer.go"},
		{"cross-package CreditNoteFromSnapshot", "dom.CreditNoteFromSnapshot", "consumer/consumer.go"},
	}
	for _, exp := range expectedViolations {
		wantIdent := "`" + exp.identifier + "`"
		found := false
		for _, line := range forbidigoLines {
			if strings.HasPrefix(line, exp.file+":") && strings.Contains(line, wantIdent) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: expected forbidigo to flag %s in %s (same-line match); forbidigo output was:\n%s",
				exp.desc, wantIdent, exp.file, forbidigoJoined)
		}
	}

	// Both custom messages must appear so a regression that replaces only
	// one pattern is caught.
	mustContainMessages := []string{
		"Snapshot APIs are for persistence adapters only",                // ToSnapshot pattern
		"Snapshot reconstruction APIs are for persistence adapters only", // FromSnapshot-family pattern
	}
	for _, want := range mustContainMessages {
		if !strings.Contains(forbidigoJoined, want) {
			t.Errorf("expected forbidigo output to contain %q, got:\n%s", want, forbidigoJoined)
		}
	}

	// Negative assertion: LoadFromSnapshot must NOT be flagged. This pins
	// the word-boundary behavior of the pattern and prevents a future
	// "simplification" from making the rule over-broad.
	if strings.Contains(forbidigoJoined, "LoadFromSnapshot") {
		t.Errorf("LoadFromSnapshot must NOT be flagged by forbidigo (it belongs to the event-sourced API family); forbidigo output:\n%s", forbidigoJoined)
	}
}

// findRepoRoot walks up from the current working directory until it finds a
// directory containing .golangci.yml, which marks the repository root.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".golangci.yml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
