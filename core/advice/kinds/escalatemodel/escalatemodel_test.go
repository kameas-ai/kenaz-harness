package escalatemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
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

func TestExtract_ErrorKindOneHot(t *testing.T) {
	cases := []struct {
		name   string
		counts map[string]int
		want   [5]int // auth, transient, cancelled, budget, unknown
	}{
		{"no errors", nil, [5]int{}},
		{"single kind", map[string]int{"transient": 2}, [5]int{0, 1, 0, 0, 0}},
		{"dominant wins", map[string]int{"auth": 1, "budget": 3}, [5]int{0, 0, 0, 1, 0}},
		{"tie -> earlier vocabulary entry", map[string]int{"cancelled": 2, "auth": 2}, [5]int{1, 0, 0, 0, 0}},
		{"unknown keys bucket into unknown", map[string]int{"timeout": 2, "weird": 2, "auth": 3}, [5]int{0, 0, 0, 0, 1}},
		{"non-positive counts ignored", map[string]int{"auth": 0, "budget": -4}, [5]int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract(Snapshot{ErrorKindCounts: tc.counts})
			if err != nil {
				t.Fatal(err)
			}
			f := got.(Features)
			have := [5]int{f.ErrorKindAuth, f.ErrorKindTransient, f.ErrorKindCancelled, f.ErrorKindBudget, f.ErrorKindUnknown}
			if have != tc.want {
				t.Errorf("one-hot = %v, want %v", have, tc.want)
			}
		})
	}
}

// TestFeatures_MatchTheEngineContract pins the wire vector to kenaz-ml's
// escalate_model contract (advice/contracts.py ESCALATE_MODEL_FEATURES,
// design §4's catalog) — names, order, and nothing else: the engine
// refuses a batch with any unexpected or (for a complete row) missing
// name with 409 names_mismatch. Every value must be a JSON number.
func TestFeatures_MatchTheEngineContract(t *testing.T) {
	want := []string{
		"consecutive_tool_failures", "retries_in_window", "turn_latency_trend", "current_rung",
		"error_kind_auth", "error_kind_transient", "error_kind_cancelled", "error_kind_budget", "error_kind_unknown",
		"budget_remaining_fraction",
	}
	f, _ := Extract(Snapshot{ConsecutiveToolFailures: 2, CurrentRung: 3, ErrorKindCounts: map[string]int{"auth": 1},
		DoomLoopRepeatCount: 2, FeaturesIncomplete: true})
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil { // {
		t.Fatal(err)
	}
	var got []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		if _, isNum := v.(json.Number); !isNum {
			t.Errorf("feature %v = %#v, want a JSON number", tok, v)
		}
		got = append(got, tok.(string))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("wire vector =\n  %v\nwant the engine contract\n  %v", got, want)
	}
}

func TestErrorKinds_MatchFleetCategories(t *testing.T) {
	fleetOrder := []fleet.ErrorCategory{fleet.ErrorCategoryAuth, fleet.ErrorCategoryTransient,
		fleet.ErrorCategoryCancelled, fleet.ErrorCategoryBudget, fleet.ErrorCategoryUnknown}
	for i, c := range fleetOrder {
		if ErrorKinds[i] != string(c) {
			t.Errorf("ErrorKinds[%d] = %q, want fleet's %q", i, ErrorKinds[i], c)
		}
	}
}

func TestPromptVersionIsV2(t *testing.T) {
	if k, _ := advice.Get(KindID); k.PromptVersion != "v2" {
		t.Fatalf("PromptVersion = %q, want v2 (the catalog rewrite must stay distinguishable from v1 label rows)", k.PromptVersion)
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
