package escalatemodel

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

func TestRegistration(t *testing.T) {
	k, ok := advice.Get(KindID)
	if !ok {
		t.Fatalf("advice.Get(%q) not found — init() did not register escalate_model", KindID)
	}
	if k.PromptVersion == "" {
		t.Error("PromptVersion is empty")
	}
	if k.SafetyClass != advice.SafetySuggestOnly {
		t.Errorf("SafetyClass = %q, want %q (a model switch changes cost/behaviour — spec §3)", k.SafetyClass, advice.SafetySuggestOnly)
	}
	if k.Extract == nil {
		t.Error("Extract is nil")
	}
	if k.RenderPrompt == nil {
		t.Error("RenderPrompt is nil")
	}
}

func TestExtract_WrongInputTypeErrors(t *testing.T) {
	if _, err := Extract("nope"); err == nil {
		t.Fatal("Extract(string) = nil error, want a type-mismatch error")
	}
}

func TestExtract_ZeroThresholdDefaultsToAgentgraphConstant(t *testing.T) {
	got, err := Extract(Snapshot{DoomLoopThreshold: 0})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	f := got.(Features)
	if f.DoomLoopThreshold != agentgraph.DefaultDoomLoopThreshold {
		t.Errorf("DoomLoopThreshold = %d, want agentgraph.DefaultDoomLoopThreshold (%d) when unset",
			f.DoomLoopThreshold, agentgraph.DefaultDoomLoopThreshold)
	}
}

func TestExtract_ExplicitThresholdPreserved(t *testing.T) {
	got, err := Extract(Snapshot{DoomLoopThreshold: 7})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	f := got.(Features)
	if f.DoomLoopThreshold != 7 {
		t.Errorf("DoomLoopThreshold = %d, want 7 (explicit value must not be overridden)", f.DoomLoopThreshold)
	}
}

func TestExtract_CopiesErrorKindCountsDefensively(t *testing.T) {
	src := map[string]int{"timeout": 2}
	got, err := Extract(Snapshot{ErrorKindCounts: src})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	f := got.(Features)
	src["timeout"] = 999
	if f.ErrorKindCounts["timeout"] != 2 {
		t.Errorf("Features.ErrorKindCounts aliases the caller's map — mutating the source changed it to %d", f.ErrorKindCounts["timeout"])
	}
}

func TestHeuristic_WrongFeaturesTypeErrors(t *testing.T) {
	if _, _, _, err := Heuristic("nope"); err == nil {
		t.Fatal("Heuristic(string) = nil error, want a type-mismatch error")
	}
}

func TestHeuristic_ConfidenceMapping(t *testing.T) {
	const threshold = 3
	cases := []struct {
		name                    string
		consecutiveToolFailures int
		retriesInWindow         int
		doomLoopRepeatCount     int
		wantConfidence          int
		wantDecision            bool
	}{
		{"no trouble", 0, 0, 0, 0, false},
		{"one failure of three", 1, 0, 0, 33, false},
		{"two failures of three", 2, 0, 0, 67, false},
		{"streak reaches threshold", 3, 0, 0, 100, true},
		{"streak exceeds threshold clamps at 100", 5, 0, 0, 100, true},
		{"retries alone reach threshold", 0, 3, 0, 100, true},
		{"doom-loop proximity alone reaches threshold", 0, 0, 3, 100, true},
		{"worst-of-three: doom loop dominates", 1, 0, 3, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Features{
				ConsecutiveToolFailures: tc.consecutiveToolFailures,
				RetriesInWindow:         tc.retriesInWindow,
				DoomLoopRepeatCount:     tc.doomLoopRepeatCount,
				DoomLoopThreshold:       threshold,
			}
			decision, confidence, modelID, err := Heuristic(f)
			if err != nil {
				t.Fatalf("Heuristic: %v", err)
			}
			if confidence != tc.wantConfidence {
				t.Errorf("confidence = %d, want %d", confidence, tc.wantConfidence)
			}
			if decision != tc.wantDecision {
				t.Errorf("decision = %v, want %v", decision, tc.wantDecision)
			}
			if modelID != ModelID {
				t.Errorf("modelID = %q, want %q", modelID, ModelID)
			}
			if err := advice.ValidateConfidence(confidence); err != nil {
				t.Errorf("Heuristic produced an out-of-range confidence: %v", err)
			}
		})
	}
}

func TestHeuristic_ZeroThresholdFallsBackToAgentgraphDefault(t *testing.T) {
	// Heuristic itself also defends against a zero threshold reaching it
	// directly (e.g. a Features value built by hand in a test, bypassing
	// Extract's own defaulting).
	f := Features{ConsecutiveToolFailures: agentgraph.DefaultDoomLoopThreshold, DoomLoopThreshold: 0}
	decision, confidence, _, err := Heuristic(f)
	if err != nil {
		t.Fatalf("Heuristic: %v", err)
	}
	if !decision || confidence != 100 {
		t.Errorf("got (%v, %d), want (true, 100) once ConsecutiveToolFailures reaches the default threshold", decision, confidence)
	}
}

func TestRenderPrompt_WrongFeaturesTypeReturnsEmpty(t *testing.T) {
	sys, user := RenderPrompt(42)
	if sys != "" || user != "" {
		t.Errorf("RenderPrompt(wrong type) = (%q, %q), want empty strings", sys, user)
	}
}

func TestRenderPrompt_ProducesNonEmptyHalves(t *testing.T) {
	sys, user := RenderPrompt(Features{ConsecutiveToolFailures: 1})
	if sys == "" || user == "" {
		t.Errorf("RenderPrompt = (%q, %q), want both non-empty", sys, user)
	}
}

func TestExtract_ThroughHeuristicAdvisor(t *testing.T) {
	k, ok := advice.Get(KindID)
	if !ok {
		t.Fatal("kind not registered")
	}
	adv := advice.NewHeuristicAdvisor()
	adv.RegisterHeuristic(KindID, Heuristic)

	snap := Snapshot{ConsecutiveToolFailures: agentgraph.DefaultDoomLoopThreshold}
	features, err := k.Extract(snap)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	rec, err := adv.Recommend(context.Background(), k, features, advice.SessionContext{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if !rec.Decision {
		t.Errorf("Recommend at the default doom-loop threshold: Decision=false, want true")
	}
	if rec.Rung != advice.RungHeuristic || !rec.Unbenchmarked || rec.Model != ModelID {
		t.Errorf("got %+v", rec)
	}
}
