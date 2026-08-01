package bundleartifact

import (
	"context"
	"errors"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeAlwaysRefuseGate always refuses, unconditionally. It exists to prove
// (a) Activate does call the configured gate when a profile carries an
// EvalManifest, and (b) Activate does NOT call the gate at all when a
// profile carries no EvalManifest — even a gate that would refuse
// everything must never fire for a profile that never opted in.
type fakeAlwaysRefuseGate struct {
	called bool
	err    error
}

func (g *fakeAlwaysRefuseGate) Check(_ context.Context, _ llm.ModelProfile) error {
	g.called = true
	if g.err != nil {
		return g.err
	}
	return errors.New(`case "workflow-x" scored 0.250, below threshold 0.900`)
}

// fakeAlwaysPassGate always permits promotion. Used to prove a gate being
// configured and actually invoked is not, by itself, sufficient to refuse —
// the gate's verdict is what matters.
type fakeAlwaysPassGate struct {
	called bool
}

func (g *fakeAlwaysPassGate) Check(_ context.Context, _ llm.ModelProfile) error {
	g.called = true
	return nil
}

const modelProfileWithManifestYAML = `
id: claude-sonnet-gate-*
version: "1"
eval_manifest:
  id: chat-default-suite
  version: "1"
`

// TestModelProfileHandler_ActivateRefusedByRegressingGate proves the WP03
// wiring: a profile carrying an EvalManifest, activated through a handler
// configured with a gate that refuses, is refused promotion and never
// lands in the store.
func TestModelProfileHandler_ActivateRefusedByRegressingGate(t *testing.T) {
	store := llm.NewModelProfileStore()
	gate := &fakeAlwaysRefuseGate{}
	h := NewModelProfileHandlerWithGate(store, gate)

	parsed, err := h.Parse([]byte(modelProfileWithManifestYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	err = h.Activate(context.Background(), parsed)
	if err == nil {
		t.Fatal("expected Activate to be refused by the regressing gate")
	}
	if !gate.called {
		t.Fatal("expected the gate to have been invoked for a profile carrying an EvalManifest")
	}

	// The refusal must actually name the failing case (propagated from the
	// gate's error), not just be a generic failure.
	if !strings.Contains(err.Error(), "workflow-x") {
		t.Errorf("expected refusal error to name the failing case, got: %v", err)
	}

	// And the store must NOT contain the refused profile.
	if _, found, _ := store.Resolve("claude-sonnet-gate-4", "1"); found {
		t.Fatal("refused profile must not be installed into the store")
	}
}

// TestModelProfileHandler_ActivatePassesEquivalentGate proves the pass
// path: a gate that permits promotion lets the profile install normally.
func TestModelProfileHandler_ActivatePassesEquivalentGate(t *testing.T) {
	store := llm.NewModelProfileStore()
	gate := &fakeAlwaysPassGate{}
	h := NewModelProfileHandlerWithGate(store, gate)

	parsed, err := h.Parse([]byte(modelProfileWithManifestYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatalf("expected Activate to succeed with a passing gate: %v", err)
	}
	if !gate.called {
		t.Fatal("expected the gate to have been invoked")
	}
	if _, found, _ := store.Resolve("claude-sonnet-gate-4", "1"); !found {
		t.Fatal("expected the passed profile to be installed into the store")
	}
}

// TestModelProfileHandler_ActivateNoManifestSkipsGateEntirely is the
// central "opt-in, inert by default" proof required by WP03: a profile
// with NO EvalManifest must activate exactly as it did before the gate
// existed, even when the handler is configured with a gate that would
// refuse *everything*. If Activate ever calls the gate here, the gate is
// no longer opt-in.
func TestModelProfileHandler_ActivateNoManifestSkipsGateEntirely(t *testing.T) {
	store := llm.NewModelProfileStore()
	gate := &fakeAlwaysRefuseGate{}
	h := NewModelProfileHandlerWithGate(store, gate)

	const noManifestYAML = `
id: claude-sonnet-no-gate-*
version: "1"
`
	parsed, err := h.Parse([]byte(noManifestYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatalf("expected a profile with no EvalManifest to activate unchanged, got: %v", err)
	}
	if gate.called {
		t.Fatal("expected the gate to never be invoked for a profile with no EvalManifest")
	}
	if _, found, _ := store.Resolve("claude-sonnet-no-gate-4", "1"); !found {
		t.Fatal("expected the no-manifest profile to be installed into the store")
	}
}

// TestModelProfileHandler_NilGateBehavesLikeNoGate proves
// NewModelProfileHandlerWithGate(store, nil) is equivalent to
// NewModelProfileHandler(store) — passing a nil gate is not itself an
// error, and a profile with an EvalManifest still activates when no gate
// is actually configured to check it.
func TestModelProfileHandler_NilGateBehavesLikeNoGate(t *testing.T) {
	store := llm.NewModelProfileStore()
	h := NewModelProfileHandlerWithGate(store, nil)

	parsed, err := h.Parse([]byte(modelProfileWithManifestYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatalf("expected a nil gate to behave like no gate at all, got: %v", err)
	}
}
