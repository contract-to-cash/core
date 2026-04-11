// Package lintcheck contains tests that verify lint rules configured in
// .golangci.yml are actually enforced. These tests do not exercise business
// logic; they create isolated fixture modules and run golangci-lint against
// them to assert that specific violations are reported.
//
// Issue #100: enforce that Snapshot APIs (ToSnapshot / FromSnapshot /
// CreditNoteFromSnapshot) cannot be called from code outside persistence
// adapters, domain snapshot files, and tests.
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
// forbidigo lint rule configured in .golangci.yml prevents calls to
// ToSnapshot / FromSnapshot / CreditNoteFromSnapshot from code outside
// persistence adapters.
//
// The test creates an isolated Go module in a temp directory, writes a
// fixture file that deliberately violates the rule, copies the repository's
// .golangci.yml, and asserts golangci-lint reports the expected violations
// with the custom messages.
//
// This test exists because the Snapshot APIs deliberately bypass construction-
// time invariants and must only be used by persistence adapters. Enforcement
// via documentation alone is insufficient; we want CI to fail when application
// code accidentally calls these APIs.
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

	// Fixture file with deliberate violations. Using a local type avoids
	// cross-module imports while still triggering forbidigo's identifier-
	// pattern matching on ".ToSnapshot(" and "FromSnapshot(".
	fixture := `package lintfixture

type fakeSnapshot struct{}

type fakeEntity struct{}

func (fakeEntity) ToSnapshot() fakeSnapshot { return fakeSnapshot{} }

func FromSnapshot(fakeSnapshot) fakeEntity { return fakeEntity{} }

func CreditNoteFromSnapshot(fakeSnapshot) fakeEntity { return fakeEntity{} }

func violatesToSnapshot() {
	e := fakeEntity{}
	_ = e.ToSnapshot()
}

func violatesFromSnapshot() {
	_ = FromSnapshot(fakeSnapshot{})
}

func violatesCreditNoteFromSnapshot() {
	_ = CreditNoteFromSnapshot(fakeSnapshot{})
}
`
	if err := os.WriteFile(filepath.Join(tmp, "bad_snapshot_calls.go"), []byte(fixture), 0o644); err != nil {
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

	// We expect a non-zero exit code (violations reported by forbidigo).
	// On RED (rule not yet configured) this will be nil and the test fails.
	if runErr == nil {
		t.Fatalf("golangci-lint exited 0; expected forbidigo violations. output:\n%s", combined)
	}

	// Sanity check: the non-zero exit must come from forbidigo.
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("unexpected error running golangci-lint: %v\noutput:\n%s", runErr, combined)
	}

	// Each violation site must be flagged by forbidigo with our custom
	// messages. Assert all three distinct messages appear.
	wantFragments := []string{
		"bad_snapshot_calls.go", // file referenced
		"forbidigo",             // linter name
		"Snapshot APIs are for persistence adapters only",                // message for .ToSnapshot(
		"Snapshot reconstruction APIs are for persistence adapters only", // message for FromSnapshot(
	}
	for _, want := range wantFragments {
		if !strings.Contains(combined, want) {
			t.Errorf("expected golangci-lint output to contain %q, got:\n%s", want, combined)
		}
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
