package compactnow

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

func TestRegistration(t *testing.T) {
	k, ok := advice.Get(KindID)
	if !ok {
		t.Fatalf("advice.Get(%q) not found — init() did not register compact_now", KindID)
	}
	if k.PromptVersion == "" {
		t.Error("PromptVersion is empty")
	}
	if k.SafetyClass != advice.SafetySuggestOnly {
		t.Errorf("SafetyClass = %q, want %q (compaction discards context — spec §3)", k.SafetyClass, advice.SafetySuggestOnly)
	}
	if k.Extract == nil {
		t.Error("Extract is nil")
	}
	if k.RenderPrompt == nil {
		t.Error("RenderPrompt is nil")
	}
}

func TestExtract_WrongInputTypeErrors(t *testing.T) {
	if _, err := Extract(42); err == nil {
		t.Fatal("Extract(int) = nil error, want a type-mismatch error")
	}
}

func TestExtract_PassesThroughSnapshot(t *testing.T) {
	snap := Snapshot{
		ContextFillFraction:        0.82,
		TokensInSpan:               4000,
		TurnsSinceLastCompaction:   15,
		ToolResultTokenFraction:    0.4,
		ModelContextLimit:          200000,
		HistoricalCompressionRatio: 0.3,
	}
	got, err := Extract(snap)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	f, ok := got.(Features)
	if !ok {
		t.Fatalf("Extract returned %T, want Features", got)
	}
	if f != (Features)(snap) {
		t.Errorf("Extract(%+v) = %+v, want a straight passthrough", snap, f)
	}
}

func TestHeuristic_WrongFeaturesTypeErrors(t *testing.T) {
	if _, _, _, err := Heuristic(42); err == nil {
		t.Fatal("Heuristic(int) = nil error, want a type-mismatch error")
	}
}

func TestHeuristic_ConfidenceMapping(t *testing.T) {
	cases := []struct {
		name                     string
		fill                     float64
		turnsSinceLastCompaction int
		wantConfidence           int
		wantDecision             bool
	}{
		{"empty context", 0.0, 0, 0, false},
		{"half full, fresh", 0.5, 0, 50, false},
		{"at threshold, fresh", 0.75, 0, 75, true},
		{"above threshold", 0.90, 0, 90, true},
		{"below threshold but very stale gets a bonus, still no decision", 0.70, 25, 80, false},
		{"above threshold and stale caps at 100", 0.95, 25, 100, true},
		{"out-of-range fraction clamps low", -0.5, 0, 0, false},
		{"out-of-range fraction clamps high", 1.5, 0, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Features{ContextFillFraction: tc.fill, TurnsSinceLastCompaction: tc.turnsSinceLastCompaction}
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

func TestRenderPrompt_WrongFeaturesTypeReturnsEmpty(t *testing.T) {
	sys, user := RenderPrompt(42)
	if sys != "" || user != "" {
		t.Errorf("RenderPrompt(wrong type) = (%q, %q), want empty strings", sys, user)
	}
}

func TestRenderPrompt_ProducesNonEmptyHalves(t *testing.T) {
	sys, user := RenderPrompt(Features{ContextFillFraction: 0.5})
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

	snap := Snapshot{ContextFillFraction: 0.9}
	features, err := k.Extract(snap)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	ctx := context.Background()
	rec, err := adv.Recommend(ctx, k, features, advice.SessionContext{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if !rec.Decision {
		t.Errorf("Recommend at 90%% fill: Decision=false, want true")
	}
	if rec.Rung != advice.RungHeuristic || !rec.Unbenchmarked || rec.Model != ModelID {
		t.Errorf("got %+v", rec)
	}

	// RequireCanAutoAct must refuse this kind (suggest-only) even at the
	// Autonomous tier — spec §3 / AC-04.
	if err := advice.RequireCanAutoAct(KindID); !errors.Is(err, advice.ErrSuggestOnlyCannotAutoAct) {
		t.Fatalf("RequireCanAutoAct(%q) = %v, want ErrSuggestOnlyCannotAutoAct", KindID, err)
	}
}
