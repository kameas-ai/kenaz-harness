package workflows

// secrets_promise_test.go — automation-actually-runs-01PMZ404 UNIT-12
// (AC-013, branch (b)). types.go's WorkflowSecretRef / Workflow.Secrets
// docstrings claimed the workflow engine "asserts all declared locators
// are exposed and fails fast" at run start, checked "against the live
// ExposureIndex". Neither Engine.Run nor anything on the run path reads
// wf.Secrets or an ExposureIndex — schema.go's Validate does SHAPE
// validation of the locator string only (non-empty, no whitespace, no
// @secret: prefix). This mission takes branch (b): rewrite the promise
// rather than build the assertion (the session-scoped seam U5 built
// would make branch (a) possible, but AC-013 requires picking exactly
// one branch and stating it, not building both).

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestSecretsDocstring_NoRunStartAssertionClaimed is AC-013 branch (b)'s
// grep obligation: the words "asserts" / "fails fast" must be gone from
// the Workflow.Secrets / WorkflowSecretRef doc comments in types.go —
// the promise a run-start check exists must not survive alongside the
// fact that it doesn't run.
func TestSecretsDocstring_NoRunStartAssertionClaimed(t *testing.T) {
	data := readOwnPackageFile(t, "types.go")
	// Isolate the doc comment block immediately above `type Workflow`
	// and the WorkflowSecretRef struct, rather than grepping the whole
	// file — "asserts" / "fails fast" could legitimately appear
	// elsewhere in types.go describing an unrelated field.
	idxSecretRef := strings.Index(data, "type WorkflowSecretRef struct")
	idxSecretsField := strings.Index(data, "Secrets       []WorkflowSecretRef")
	if idxSecretRef < 0 {
		t.Fatal("could not locate WorkflowSecretRef in types.go — has it moved/renamed?")
	}
	if idxSecretsField < 0 {
		t.Fatal("could not locate the Secrets field in types.go — has it moved/renamed?")
	}
	// Doc comments precede their declarations; take a window ending at
	// each declaration and starting a reasonable distance before it.
	docWindow := func(declIdx, back int) string {
		start := declIdx - back
		if start < 0 {
			start = 0
		}
		return data[start:declIdx]
	}
	banned := []string{"asserts", "fails fast"}
	for _, window := range []string{docWindow(idxSecretRef, 600), docWindow(idxSecretsField, 900)} {
		for _, b := range banned {
			if strings.Contains(strings.ToLower(window), b) {
				t.Errorf("doc comment window contains banned run-start-assertion phrase %q:\n%s", b, window)
			}
		}
	}
}

const secretsPromiseProbeYAML = `
id: zz-secrets-promise-probe
name: "secrets promise probe"
version: 1
secrets:
  - locator: user:nonexistent-locator-nobody-exposed
    description: "declared but never resolved against anything"
steps:
  - name: greet
    kind: transform
    template: "hello"
`

// TestSecretsField_DeclaredLocator_DoesNotBlockRun is the behavioural
// half of branch (b): a workflow that declares a Secrets locator
// nothing exposes still runs to completion. If a future change adds
// the run-start assertion this docstring now says does NOT exist, this
// test must be updated (and the docstring restored) together — AC-013
// forbids claiming both branches at once.
func TestSecretsField_DeclaredLocator_DoesNotBlockRun(t *testing.T) {
	wf, err := LoadYAML([]byte(secretsPromiseProbeYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v (a workflow with a well-formed-but-unresolved secrets locator must still validate)", err)
	}
	if len(wf.Secrets) != 1 {
		t.Fatalf("got %d secrets entries, want 1", len(wf.Secrets))
	}

	engine := NewEngineWithDeps(Deps{})
	run, err := engine.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want completed — an unresolved Secrets locator must not fail the run (no run-start assertion exists)", run.Status)
	}
}

// readOwnPackageFile reads a file from this test's own package
// directory. `go test` sets the working directory to the package
// directory, so a bare relative name resolves to types.go alongside
// this test file regardless of where the `go test` invocation itself
// runs from.
func readOwnPackageFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}
