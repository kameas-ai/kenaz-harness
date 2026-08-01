package eval_test

import (
	"context"
	"path/filepath"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"

	"github.com/kameas-ai/kenaz-harness/core/eval"
)

// buildAssistantCapture writes a minimal capture JSONL with one LLM
// request/response pair and one assistant KindMessage carrying assistantText.
// This is the same shape as eval_test.go's buildFixtureCapture, duplicated
// locally (with a caller-supplied assistant text) so this file can build two
// captures — a baseline and a candidate — with deliberately different
// content to prove the gate can actually distinguish them.
func buildAssistantCapture(t *testing.T, capDir, sessionID, assistantText string) string {
	t.Helper()
	rec := eval.NewRecorder(capDir, "test-build")
	ctx := context.Background()
	if err := rec.StartCapture(ctx, sessionID); err != nil {
		t.Fatalf("StartCapture: %v", err)
	}

	req := corellm.GenerationRequest{
		ProfileID: "test-profile",
		Model:     "test-model",
		System:    "You are a helpful assistant.",
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "Say hello"}}},
		},
	}
	rec.AppendLLMRequest(sessionID, req)
	fp := eval.FingerprintRequest(req)
	rec.AppendLLMResponse(sessionID, fp, corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: assistantText}},
		FinishReason: "end_turn",
		Usage:        corellm.Usage{InputTokens: 5, OutputTokens: 3},
	})
	rec.AppendMessage(sessionID, "assistant", []corellm.ContentBlock{{Type: "text", Text: assistantText}})

	if err := rec.StopCapture(ctx, sessionID); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}
	return filepath.Join(capDir, sessionID+".jsonl")
}

// TestGateModelProfilePromotion_RegressingSuiteFails is the non-vacuous
// proof required by versioned-model-profile-01PMDL04 WP03: a candidate
// session whose assistant output diverges from the baseline must cause the
// gate to refuse, naming the failing case.
func TestGateModelProfilePromotion_RegressingSuiteFails(t *testing.T) {
	dir := t.TempDir()
	capDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")

	baselineSID := "wf-baseline"
	candidateSID := "wf-candidate-regressed"
	buildAssistantCapture(t, capDir, baselineSID, "Hello there, how can I help?")
	buildAssistantCapture(t, capDir, candidateSID, "ERROR: unable to comply.")

	suite := eval.ModelProfileSuite{
		ID:      "chat-default-suite",
		Version: "1",
		Cases: []eval.ModelProfileSuiteCase{
			{
				Label:              "chat-default",
				CandidateSessionID: candidateSID,
				BaselineSessionID:  baselineSID,
			},
		},
		MinOverallScore: 0.9,
	}

	result, err := eval.GateModelProfilePromotion(context.Background(), capDir, runsDir, suite)
	if err != nil {
		t.Fatalf("GateModelProfilePromotion returned an error (expected a clean refusal, not an error): %v", err)
	}
	if result.Passed {
		t.Fatalf("expected the regressing suite to fail the gate, got Passed=true (results=%+v)", result.Results)
	}
	if result.FailingCase != "chat-default" {
		t.Errorf("expected FailingCase %q, got %q", "chat-default", result.FailingCase)
	}
	if result.Reason == "" {
		t.Error("expected a non-empty Reason naming the regression")
	}
	t.Logf("gate refusal reason: %s", result.Reason)
}

// TestGateModelProfilePromotion_EquivalentSuitePasses proves the gate is not
// simply "always refuse": a candidate whose output matches the baseline
// passes.
func TestGateModelProfilePromotion_EquivalentSuitePasses(t *testing.T) {
	dir := t.TempDir()
	capDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")

	baselineSID := "wf-baseline-2"
	candidateSID := "wf-candidate-equivalent"
	const text = "Hello there, how can I help?"
	buildAssistantCapture(t, capDir, baselineSID, text)
	buildAssistantCapture(t, capDir, candidateSID, text)

	suite := eval.ModelProfileSuite{
		ID:      "chat-default-suite",
		Version: "2",
		Cases: []eval.ModelProfileSuiteCase{
			{
				Label:              "chat-default",
				CandidateSessionID: candidateSID,
				BaselineSessionID:  baselineSID,
			},
		},
		MinOverallScore: 0.9,
	}

	result, err := eval.GateModelProfilePromotion(context.Background(), capDir, runsDir, suite)
	if err != nil {
		t.Fatalf("GateModelProfilePromotion: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected the equivalent suite to pass, got Passed=false reason=%q", result.Reason)
	}
}

// TestModelProfileSuiteStore_LoadResolve exercises the (ID, Version)
// keyed store the gate resolves EvalManifest references against.
func TestModelProfileSuiteStore_LoadResolve(t *testing.T) {
	store := eval.NewModelProfileSuiteStore()
	if _, found := store.Resolve("nope", "1"); found {
		t.Fatal("expected Resolve on an empty store to report not found")
	}

	suite := eval.ModelProfileSuite{ID: "s1", Version: "1"}
	if err := store.Load(suite); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, found := store.Resolve("s1", "1")
	if !found {
		t.Fatal("expected Resolve to find the loaded suite")
	}
	if got.ID != "s1" || got.Version != "1" {
		t.Errorf("unexpected resolved suite: %+v", got)
	}

	if err := store.Load(eval.ModelProfileSuite{Version: "1"}); err == nil {
		t.Error("expected Load to reject an empty ID")
	}
	if err := store.Load(eval.ModelProfileSuite{ID: "s2"}); err == nil {
		t.Error("expected Load to reject an empty Version")
	}
}

// TestModelProfileGate_Check_NoManifestIsInert proves the *eval.ModelProfileGate
// adapter itself treats a nil EvalManifest as an unconditional pass, even
// with no suite store configured — the second layer of the "opt-in, inert
// by default" guarantee (the first layer lives in
// bundleartifact.ModelProfileHandler.Activate, tested separately).
func TestModelProfileGate_Check_NoManifestIsInert(t *testing.T) {
	gate := &eval.ModelProfileGate{} // no Suites store, no dirs configured
	err := gate.Check(context.Background(), corellm.ModelProfile{ID: "m", Version: "1"})
	if err != nil {
		t.Fatalf("expected nil-EvalManifest profile to pass unconditionally, got: %v", err)
	}
}

// TestModelProfileGate_Check_UnknownManifestErrors proves a *present*
// EvalManifest reference that the suite store cannot resolve is a loud
// configuration error, not a silent pass.
func TestModelProfileGate_Check_UnknownManifestErrors(t *testing.T) {
	gate := &eval.ModelProfileGate{Suites: eval.NewModelProfileSuiteStore()}
	p := corellm.ModelProfile{
		ID:      "m",
		Version: "1",
		EvalManifest: &corellm.EvalManifestRef{
			ID:      "does-not-exist",
			Version: "1",
		},
	}
	if err := gate.Check(context.Background(), p); err == nil {
		t.Fatal("expected an error for an unresolvable eval manifest reference")
	}
}

// TestRunMatrix_CompactionOnlyUnaffected re-confirms the pre-WP03 shape of
// MatrixCase (SessionID + Overrides, no BaselineSessionID) still diffs a
// session against its own capture and reports success — the
// compaction-strategy matrix must not regress from this change.
func TestRunMatrix_CompactionOnlyUnaffected(t *testing.T) {
	dir := t.TempDir()
	sid := "wf-compaction-only"
	capDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")
	buildAssistantCapture(t, capDir, sid, "Hello there")

	cases := []eval.MatrixCase{
		{SessionID: sid, Label: "baseline", CachedOnly: true},
		{
			SessionID:  sid,
			Label:      "aggressive-compaction",
			Overrides:  []eval.StrategyOverride{{Key: "compaction.tier", Value: "aggressive"}},
			CachedOnly: true,
		},
	}
	results, table, err := eval.RunMatrix(context.Background(), capDir, runsDir, cases, eval.DiffModeBytes)
	if err != nil {
		t.Fatalf("RunMatrix: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("case %s failed: %v", r.Label, r.Err)
		}
		if r.OverallScore < 0.99 {
			t.Errorf("case %s: expected ~1.0 self-diff score (no BaselineSessionID set), got %.3f", r.Label, r.OverallScore)
		}
	}
	if table == "" {
		t.Error("expected non-empty markdown table")
	}
}
