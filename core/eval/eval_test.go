package eval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/eval"
)

// ── Fixture helpers ──────────────────────────────────────────────────────────

// buildFixtureCapture creates a minimal capture JSONL file under dir for
// sessionID and returns its path. It contains:
//
//   - KindCaptureStart
//   - KindLLMRequest  (fingerprint: fp1)
//   - KindLLMResponse (linked to fp1, text "Hello there")
//   - KindMessage     (role assistant, text "Hello there")
//   - KindStrategyDecision (compaction.tier=balanced)
//   - KindCaptureStop
func buildFixtureCapture(t *testing.T, dir, sessionID string) string {
	t.Helper()
	captureDir := filepath.Join(dir, "eval-captures")
	rec := eval.NewRecorder(captureDir, "test-build")

	ctx := context.Background()
	if err := rec.StartCapture(ctx, sessionID); err != nil {
		t.Fatalf("StartCapture: %v", err)
	}

	// LLM request + response
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
		Content:      []corellm.ContentBlock{{Type: "text", Text: "Hello there"}},
		FinishReason: "end_turn",
		Usage:        corellm.Usage{InputTokens: 5, OutputTokens: 3},
	})

	// Message
	rec.AppendMessage(sessionID, "assistant",
		[]corellm.ContentBlock{{Type: "text", Text: "Hello there"}})

	// Strategy decision
	rec.AppendStrategyDecision(sessionID, eval.StrategyEntry{
		Domain: "compaction",
		Key:    "compaction.tier",
		Value:  "balanced",
		Source: "config",
	})

	if err := rec.StopCapture(ctx, sessionID); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}
	return filepath.Join(captureDir, sessionID+".jsonl")
}

// ── Capture tests ────────────────────────────────────────────────────────────

func TestCaptureStartStop(t *testing.T) {
	dir := t.TempDir()
	path := buildFixtureCapture(t, dir, "sess-abc")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("capture file not written: %v", err)
	}

	// Count entry kinds.
	counts := make(map[eval.EntryKind]int)
	err := eval.ReadCapture(path, func(e eval.CaptureEntry) error {
		counts[e.Kind]++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadCapture: %v", err)
	}
	if counts[eval.KindCaptureStart] != 1 {
		t.Errorf("expected 1 KindCaptureStart, got %d", counts[eval.KindCaptureStart])
	}
	if counts[eval.KindCaptureStop] != 1 {
		t.Errorf("expected 1 KindCaptureStop, got %d", counts[eval.KindCaptureStop])
	}
	if counts[eval.KindLLMRequest] != 1 {
		t.Errorf("expected 1 KindLLMRequest, got %d", counts[eval.KindLLMRequest])
	}
	if counts[eval.KindLLMResponse] != 1 {
		t.Errorf("expected 1 KindLLMResponse, got %d", counts[eval.KindLLMResponse])
	}
}

func TestCaptureIdempotentStart(t *testing.T) {
	dir := t.TempDir()
	captureDir := filepath.Join(dir, "eval-captures")
	rec := eval.NewRecorder(captureDir, "v-test")
	ctx := context.Background()

	if err := rec.StartCapture(ctx, "sess-x"); err != nil {
		t.Fatal(err)
	}
	// Second start should be no-op.
	if err := rec.StartCapture(ctx, "sess-x"); err != nil {
		t.Fatalf("idempotent StartCapture: %v", err)
	}
	if !rec.IsCapturing("sess-x") {
		t.Error("expected IsCapturing true")
	}
	_ = rec.StopCapture(ctx, "sess-x")
	if rec.IsCapturing("sess-x") {
		t.Error("expected IsCapturing false after stop")
	}
}

func TestCaptureRedactsAPIKey(t *testing.T) {
	dir := t.TempDir()
	captureDir := filepath.Join(dir, "eval-captures")
	rec := eval.NewRecorder(captureDir, "v-test")
	ctx := context.Background()
	const sid = "sess-redact"

	_ = rec.StartCapture(ctx, sid)
	req := corellm.GenerationRequest{
		ProfileID: "p",
		System:    "key=sk-ant-api03-secrettoken1234567890abcdefghij",
		Messages:  []corellm.Message{{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "hi"}}}},
	}
	rec.AppendLLMRequest(sid, req)
	_ = rec.StopCapture(ctx, sid)

	path := filepath.Join(captureDir, sid+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(raw), "secrettoken") {
		t.Error("capture file contains plaintext secret token (redaction failed)")
	}
}

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && containsStr(s, sub)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Fingerprint tests ────────────────────────────────────────────────────────

func TestFingerprintDeterministic(t *testing.T) {
	req := corellm.GenerationRequest{
		ProfileID: "p1",
		Model:     "m1",
		System:    "sys",
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "hello"}}},
		},
	}
	fp1 := eval.FingerprintRequest(req)
	fp2 := eval.FingerprintRequest(req)
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %s vs %s", fp1, fp2)
	}
}

func TestFingerprintDiffersOnMsgChange(t *testing.T) {
	base := corellm.GenerationRequest{
		ProfileID: "p1",
		Model:     "m1",
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "A"}}},
		},
	}
	modified := corellm.GenerationRequest{
		ProfileID: "p1",
		Model:     "m1",
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "B"}}},
		},
	}
	if eval.FingerprintRequest(base) == eval.FingerprintRequest(modified) {
		t.Error("fingerprints should differ for different messages")
	}
}

// ── Strategy tests ───────────────────────────────────────────────────────────

func TestParseStrategyOverride(t *testing.T) {
	so, err := eval.ParseStrategyOverride("compaction.tier=aggressive")
	if err != nil {
		t.Fatal(err)
	}
	if so.Key != "compaction.tier" || so.Value != "aggressive" {
		t.Errorf("unexpected: %+v", so)
	}
}

func TestParseStrategyOverrideInvalid(t *testing.T) {
	for _, bad := range []string{"", "no-equals", "=value", "key="} {
		_, err := eval.ParseStrategyOverride(bad)
		if err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestApplyStrategyOverrides(t *testing.T) {
	defaults := eval.DefaultAppliedStrategy()
	overrides := []eval.StrategyOverride{
		{Key: "compaction.tier", Value: "aggressive"},
		{Key: "memory.enabled", Value: "false"},
	}
	result, err := eval.ApplyStrategyOverrides(defaults, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compaction != "aggressive" {
		t.Errorf("compaction.tier: want aggressive, got %s", result.Compaction)
	}
	if result.MemoryEnabled {
		t.Error("memory.enabled: want false")
	}
	if !result.BranchingAuto {
		t.Error("branching.auto: want true (unchanged)")
	}
}

func TestApplyStrategyOverridesUnknownKey(t *testing.T) {
	_, err := eval.ApplyStrategyOverrides(eval.DefaultAppliedStrategy(), []eval.StrategyOverride{
		{Key: "future.dial", Value: "x"},
	})
	// Unknown keys must NOT return an error — forward compat.
	if err != nil {
		t.Errorf("unexpected error for unknown key: %v", err)
	}
}

// ── Replay tests ─────────────────────────────────────────────────────────────

func TestReplayCachedOnly(t *testing.T) {
	dir := t.TempDir()
	const sid = "sess-replay"
	buildFixtureCapture(t, dir, sid)

	captureDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")
	replayer := eval.NewReplayer(captureDir, runsDir)

	result, err := replayer.Replay(context.Background(), sid, eval.ReplayOptions{
		CachedOnly: true,
		RunID:      "run-001",
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if result.RunID != "run-001" {
		t.Errorf("RunID: want run-001, got %s", result.RunID)
	}
	if result.ResponsesServed == 0 {
		t.Error("expected at least 1 cached response served")
	}

	// Check trace.jsonl exists.
	tracePath := filepath.Join(runsDir, "run-001", "trace.jsonl")
	if _, err := os.Stat(tracePath); err != nil {
		t.Errorf("trace.jsonl not found: %v", err)
	}
	// Check summary.json exists.
	summaryPath := filepath.Join(runsDir, "run-001", "summary.json")
	raw, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("summary.json not found: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("summary.json invalid JSON: %v", err)
	}
}

func TestReplayNoCaptureFile(t *testing.T) {
	dir := t.TempDir()
	replayer := eval.NewReplayer(filepath.Join(dir, "captures"), filepath.Join(dir, "runs"))
	_, err := replayer.Replay(context.Background(), "nonexistent", eval.ReplayOptions{CachedOnly: true})
	if err == nil {
		t.Error("expected error for missing capture file")
	}
}

// TestReplayStrategyOverrideRelabelsExistingDecision proves an override
// targeting a key the capture actually recorded a decision for genuinely
// changes the replay output: ResolvedStrategy reflects the resolved dial
// value (not the raw override string echoed back), and the trace's
// KindStrategyDecision entry is relabeled Source="override".
func TestReplayStrategyOverrideRelabelsExistingDecision(t *testing.T) {
	dir := t.TempDir()
	const sid = "sess-override-existing"
	buildFixtureCapture(t, dir, sid)
	captureDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")
	replayer := eval.NewReplayer(captureDir, runsDir)

	// Without an override: resolved strategy matches the default
	// (balanced), which is also what the fixture recorded.
	baseline, err := replayer.Replay(context.Background(), sid, eval.ReplayOptions{
		CachedOnly: true, RunID: "run-baseline",
	})
	if err != nil {
		t.Fatalf("Replay (baseline): %v", err)
	}
	if baseline.ResolvedStrategy.Compaction != "balanced" {
		t.Errorf("baseline ResolvedStrategy.Compaction = %q, want %q", baseline.ResolvedStrategy.Compaction, "balanced")
	}

	// With an override: ResolvedStrategy must reflect the resolved
	// (validated) dial, not just parrot the string back.
	overridden, err := replayer.Replay(context.Background(), sid, eval.ReplayOptions{
		CachedOnly: true,
		RunID:      "run-overridden",
		StrategyOverrides: []eval.StrategyOverride{
			{Key: "compaction.tier", Value: "aggressive"},
		},
	})
	if err != nil {
		t.Fatalf("Replay (overridden): %v", err)
	}
	if overridden.ResolvedStrategy.Compaction != "aggressive" {
		t.Errorf("overridden ResolvedStrategy.Compaction = %q, want %q", overridden.ResolvedStrategy.Compaction, "aggressive")
	}
	if overridden.ResolvedStrategy.Compaction == baseline.ResolvedStrategy.Compaction {
		t.Fatal("override produced the same ResolvedStrategy as no override — the dial had no effect")
	}

	// The trace itself must carry the effect too: the existing decision
	// entry is relabeled Source="override" with the new value.
	var found bool
	for _, e := range overridden.Entries {
		if e.Kind != eval.KindStrategyDecision {
			continue
		}
		var se eval.StrategyEntry
		if err := json.Unmarshal(e.Payload, &se); err != nil {
			continue
		}
		if se.Key == "compaction.tier" {
			found = true
			if se.Value != "aggressive" || se.Source != "override" {
				t.Errorf("relabeled decision = %+v, want value=aggressive source=override", se)
			}
		}
	}
	if !found {
		t.Fatal("expected a compaction.tier KindStrategyDecision entry in the overridden trace")
	}
}

// TestReplayStrategyOverrideSynthesizesMissingDecision proves that an
// override targeting a key the capture never recorded a decision for is
// no longer a silent no-op: it is synthesized into the trace, and it is
// reflected in ResolvedStrategy.
func TestReplayStrategyOverrideSynthesizesMissingDecision(t *testing.T) {
	dir := t.TempDir()
	const sid = "sess-override-missing"
	buildFixtureCapture(t, dir, sid) // fixture never records a memory.enabled decision
	captureDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")
	replayer := eval.NewReplayer(captureDir, runsDir)

	baseline, err := replayer.Replay(context.Background(), sid, eval.ReplayOptions{
		CachedOnly: true, RunID: "run-no-override",
	})
	if err != nil {
		t.Fatalf("Replay (baseline): %v", err)
	}

	overridden, err := replayer.Replay(context.Background(), sid, eval.ReplayOptions{
		CachedOnly: true,
		RunID:      "run-with-override",
		StrategyOverrides: []eval.StrategyOverride{
			{Key: "memory.enabled", Value: "false"},
		},
	})
	if err != nil {
		t.Fatalf("Replay (overridden): %v", err)
	}

	if overridden.ResolvedStrategy.MemoryEnabled {
		t.Error("ResolvedStrategy.MemoryEnabled = true, want false")
	}
	if len(overridden.Entries) != len(baseline.Entries)+1 {
		t.Fatalf("expected exactly one synthesized entry: baseline=%d overridden=%d",
			len(baseline.Entries), len(overridden.Entries))
	}

	var found bool
	for _, e := range overridden.Entries {
		if e.Kind != eval.KindStrategyDecision {
			continue
		}
		var se eval.StrategyEntry
		if err := json.Unmarshal(e.Payload, &se); err != nil {
			continue
		}
		if se.Key == "memory.enabled" {
			found = true
			if se.Value != "false" || se.Source != "override" || se.Domain != "memory" {
				t.Errorf("synthesized decision = %+v, want value=false source=override domain=memory", se)
			}
		}
	}
	if !found {
		t.Fatal("expected a synthesized memory.enabled KindStrategyDecision entry — the override must not be a silent no-op")
	}
}

// TestReplayStrategyOverrideInvalidValueErrors proves a bogus dial value
// now fails loudly instead of being silently accepted.
func TestReplayStrategyOverrideInvalidValueErrors(t *testing.T) {
	dir := t.TempDir()
	const sid = "sess-override-invalid"
	buildFixtureCapture(t, dir, sid)
	captureDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")
	replayer := eval.NewReplayer(captureDir, runsDir)

	_, err := replayer.Replay(context.Background(), sid, eval.ReplayOptions{
		CachedOnly: true,
		StrategyOverrides: []eval.StrategyOverride{
			{Key: "compaction.tier", Value: "bogus-tier"},
		},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid compaction.tier override value")
	}
}

// ── Diff tests ───────────────────────────────────────────────────────────────

func TestDiffIdentical(t *testing.T) {
	dir := t.TempDir()
	const sid = "sess-diff"
	capturePath := buildFixtureCapture(t, dir, sid)

	// Diff the file against itself — should yield score 1.0.
	runDir := filepath.Join(dir, "run-diff")
	_ = os.MkdirAll(runDir, 0o700)
	summary, err := eval.Diff(runDir, capturePath, capturePath, eval.DiffModeBytes)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if summary.OverallScore < 0.99 {
		t.Errorf("expected score ~1.0 for identical diff, got %.3f", summary.OverallScore)
	}
	if summary.ChangedCount != 0 {
		t.Errorf("expected 0 changes, got %d", summary.ChangedCount)
	}
}

// ── RunMatrix tests ──────────────────────────────────────────────────────────

func TestRunMatrix(t *testing.T) {
	dir := t.TempDir()
	const sid = "sess-matrix"
	buildFixtureCapture(t, dir, sid)

	captureDir := filepath.Join(dir, "eval-captures")
	runsDir := filepath.Join(dir, "eval-runs")

	cases := []eval.MatrixCase{
		{
			SessionID:  sid,
			Label:      "baseline",
			CachedOnly: true,
		},
		{
			SessionID: sid,
			Label:     "aggressive-compaction",
			Overrides: []eval.StrategyOverride{
				{Key: "compaction.tier", Value: "aggressive"},
			},
			CachedOnly: true,
		},
	}

	results, table, err := eval.RunMatrix(context.Background(), captureDir, runsDir, cases, eval.DiffModeBytes)
	if err != nil {
		t.Fatalf("RunMatrix: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("case %s failed: %v", r.Label, r.Err)
		}
	}
	if table == "" {
		t.Error("expected non-empty markdown table")
	}
	t.Log("RunMatrix table:\n" + table)
}
