package eval_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/eval"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/bundleartifact"
)

// TestModelProfileHandler_RealGateEndToEnd is the producer/consumer
// integration proof the WP07 lesson called for: the fake-gate tests in
// core/llm/bundleartifact/model_profile_gate_test.go prove Activate calls
// *some* ModelProfileGate correctly, but only a test that wires the *real*
// *eval.ModelProfileGate — driven by real eval-captures on disk, resolved
// through a real eval.ModelProfileSuiteStore — into a real
// bundleartifact.ModelProfileHandler proves the two packages' contracts
// actually fit together end-to-end (versioned-model-profile-01PMDL04 WP03).
func TestModelProfileHandler_RealGateEndToEnd(t *testing.T) {
	dir := t.TempDir()
	capDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")

	baselineSID := "wf-e2e-baseline"
	regressedSID := "wf-e2e-candidate-regressed"
	equivalentSID := "wf-e2e-candidate-equivalent"
	buildAssistantCapture(t, capDir, baselineSID, "Hello there, how can I help?")
	buildAssistantCapture(t, capDir, regressedSID, "ERROR: unable to comply.")
	buildAssistantCapture(t, capDir, equivalentSID, "Hello there, how can I help?")

	suites := eval.NewModelProfileSuiteStore()
	if err := suites.Load(eval.ModelProfileSuite{
		ID:      "e2e-chat-suite",
		Version: "1",
		Cases: []eval.ModelProfileSuiteCase{
			{Label: "chat-default", CandidateSessionID: regressedSID, BaselineSessionID: baselineSID},
		},
		MinOverallScore: 0.9,
	}); err != nil {
		t.Fatalf("Load regressing suite: %v", err)
	}
	if err := suites.Load(eval.ModelProfileSuite{
		ID:      "e2e-chat-suite",
		Version: "2",
		Cases: []eval.ModelProfileSuiteCase{
			{Label: "chat-default", CandidateSessionID: equivalentSID, BaselineSessionID: baselineSID},
		},
		MinOverallScore: 0.9,
	}); err != nil {
		t.Fatalf("Load equivalent suite: %v", err)
	}

	gate := &eval.ModelProfileGate{Suites: suites, CaptureDir: capDir, RunsDir: runsDir}

	// Regressing candidate: bundle references suite version "1" (backed by
	// the regressed capture) and must be refused promotion.
	store := corellm.NewModelProfileStore()
	h := bundleartifact.NewModelProfileHandlerWithGate(store, gate)

	const regressingYAML = `
id: e2e-model-*
version: "1"
eval_manifest:
  id: e2e-chat-suite
  version: "1"
`
	parsed, err := h.Parse([]byte(regressingYAML))
	if err != nil {
		t.Fatalf("Parse (regressing): %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("Validate (regressing): %v", err)
	}
	err = h.Activate(context.Background(), parsed)
	if err == nil {
		t.Fatal("expected the real gate to refuse promotion of the regressing candidate")
	}
	if !strings.Contains(err.Error(), "chat-default") {
		t.Errorf("expected refusal to name the failing case, got: %v", err)
	}
	if _, found, _ := store.Resolve("e2e-model-4", "1"); found {
		t.Fatal("regressing profile must not be installed into the store")
	}

	// Equivalent candidate: bundle references suite version "2" (backed by
	// the equivalent capture) and must be promoted normally.
	const equivalentYAML = `
id: e2e-model-*
version: "2"
eval_manifest:
  id: e2e-chat-suite
  version: "2"
`
	parsed, err = h.Parse([]byte(equivalentYAML))
	if err != nil {
		t.Fatalf("Parse (equivalent): %v", err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatalf("Validate (equivalent): %v", err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatalf("expected the real gate to pass the equivalent candidate, got: %v", err)
	}
	if _, found, _ := store.Resolve("e2e-model-4", "2"); !found {
		t.Fatal("expected the equivalent profile to be installed into the store")
	}
}
