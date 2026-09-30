package advice

import (
	"context"
	"errors"
	"testing"
)

// fixedHeuristic scripts a constant (decision, confidence, modelID)
// triple and counts how many times it was invoked — the HeuristicAdvisor
// analogue of llmadvisor_test.go's countingAdviceRegistry.
type fixedHeuristic struct {
	decision   bool
	confidence int
	modelID    string
	err        error
	calls      int
}

func (f *fixedHeuristic) fn(_ Features) (bool, int, string, error) {
	f.calls++
	if f.err != nil {
		return false, 0, "", f.err
	}
	return f.decision, f.confidence, f.modelID, nil
}

func TestHeuristicAdvisor_HappyPath(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_happy_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{decision: true, confidence: 72, modelID: "heuristic/toy-v1"}
	adv.RegisterHeuristic(kind.ID, fh.fn)

	rec, err := adv.Recommend(context.Background(), kind, "features-a", SessionContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if !rec.Decision || rec.Confidence != 72 {
		t.Errorf("got Decision=%v Confidence=%d, want true/72", rec.Decision, rec.Confidence)
	}
	if rec.Model != "heuristic/toy-v1" || rec.Rung != RungHeuristic || !rec.Unbenchmarked {
		t.Errorf("got %+v, want Model=heuristic/toy-v1 Rung=%s Unbenchmarked=true", rec, RungHeuristic)
	}
	if fh.calls != 1 {
		t.Fatalf("heuristic called %d times, want 1", fh.calls)
	}
}

// TestHeuristicAdvisor_CacheHit mirrors TestLLMAdvisor_CacheHit: two
// identical Recommend calls (same session, kind, features) must produce
// exactly one real heuristic call.
func TestHeuristicAdvisor_CacheHit(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_cache_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{decision: false, confidence: 30, modelID: "heuristic/toy-v1"}
	adv.RegisterHeuristic(kind.ID, fh.fn)
	sess := SessionContext{SessionID: "sess-cache"}

	r1, err := adv.Recommend(context.Background(), kind, "same-features", sess)
	if err != nil {
		t.Fatalf("first Recommend: %v", err)
	}
	r2, err := adv.Recommend(context.Background(), kind, "same-features", sess)
	if err != nil {
		t.Fatalf("second Recommend: %v", err)
	}
	if fh.calls != 1 {
		t.Fatalf("heuristic called %d times for two identical dispatches, want 1 (cache should have hit)", fh.calls)
	}
	if r1.CacheHit {
		t.Errorf("first Recommend() reported CacheHit=true; it was the fresh compute")
	}
	if !r2.CacheHit {
		t.Errorf("second Recommend() reported CacheHit=false; it should have been served from cache")
	}
	r2.CacheHit = r1.CacheHit
	if r1 != r2 {
		t.Errorf("cached recommendation differs beyond CacheHit: %+v vs %+v", r1, r2)
	}
}

// TestHeuristicAdvisor_DismissSkipsFutureCalls mirrors AC-03's LLMAdvisor
// proof.
func TestHeuristicAdvisor_DismissSkipsFutureCalls(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_dismiss_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{decision: true, confidence: 90, modelID: "heuristic/toy-v1"}
	adv.RegisterHeuristic(kind.ID, fh.fn)
	sess := SessionContext{SessionID: "sess-dismiss"}

	if _, err := adv.Recommend(context.Background(), kind, "features-x", sess); err != nil {
		t.Fatalf("first Recommend: %v", err)
	}
	adv.Dismiss(sess, kind, "features-x")

	_, err := adv.Recommend(context.Background(), kind, "features-x", sess)
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend after Dismiss: err = %v, want ErrNoAdvice", err)
	}
	if fh.calls != 1 {
		t.Fatalf("heuristic called %d times across dismiss+re-ask, want 1 (no re-show, no re-call)", fh.calls)
	}
}

// TestHeuristicAdvisor_MootSkipsCallEntirely mirrors the second LLMAdvisor
// skip path.
func TestHeuristicAdvisor_MootSkipsCallEntirely(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_moot_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{decision: true, confidence: 50, modelID: "heuristic/toy-v1"}
	adv.RegisterHeuristic(kind.ID, fh.fn)

	_, err := adv.Recommend(context.Background(), kind, "features-moot", SessionContext{SessionID: "sess-moot", Moot: true})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend with Moot=true: err = %v, want ErrNoAdvice", err)
	}
	if fh.calls != 0 {
		t.Fatalf("heuristic called %d times for a moot session, want 0", fh.calls)
	}
}

// TestHeuristicAdvisor_KindGateDisablesIndependently is spec AC-02's
// "kinds are independently disableable" proof: a gate returning false for
// ONE kind must not affect another kind registered on the SAME advisor.
func TestHeuristicAdvisor_KindGateDisablesIndependently(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kindA := toyAdviceKind("hx_gate_kind_a", "v1", SafetyReversible)
	kindB := toyAdviceKind("hx_gate_kind_b", "v1", SafetyReversible)
	fhA := &fixedHeuristic{decision: true, confidence: 80, modelID: "heuristic/a-v1"}
	fhB := &fixedHeuristic{decision: true, confidence: 80, modelID: "heuristic/b-v1"}
	adv.RegisterHeuristic(kindA.ID, fhA.fn)
	adv.RegisterHeuristic(kindB.ID, fhB.fn)

	disabled := true
	adv.SetKindGate(kindA.ID, func() bool { return !disabled })

	sess := SessionContext{SessionID: "sess-gate"}
	if _, err := adv.Recommend(context.Background(), kindA, "f", sess); !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend(kindA) while gated off: err = %v, want ErrNoAdvice", err)
	}
	if fhA.calls != 0 {
		t.Fatalf("kindA heuristic called %d times while gated off, want 0", fhA.calls)
	}
	if _, err := adv.Recommend(context.Background(), kindB, "f", sess); err != nil {
		t.Fatalf("Recommend(kindB), an UNGATED kind sharing the same advisor: %v", err)
	}
	if fhB.calls != 1 {
		t.Fatalf("kindB heuristic called %d times, want 1 (gating kindA must not affect kindB)", fhB.calls)
	}

	// Flip the gate live (mirrors a Settings toggle changing mid-process)
	// and confirm kindA now serves.
	disabled = false
	if _, err := adv.Recommend(context.Background(), kindA, "f", sess); err != nil {
		t.Fatalf("Recommend(kindA) after re-enabling: %v", err)
	}
	if fhA.calls != 1 {
		t.Fatalf("kindA heuristic called %d times after re-enabling, want 1", fhA.calls)
	}
}

func TestHeuristicAdvisor_NoBackendRegisteredDegradesToNoAdvice(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_unregistered_kind", "v1", SafetyReversible)

	_, err := adv.Recommend(context.Background(), kind, "f", SessionContext{SessionID: "s"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend with no registered heuristic: err = %v, want ErrNoAdvice", err)
	}
}

func TestHeuristicAdvisor_HeuristicErrorDegradesToNoAdvice(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_error_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{err: errors.New("boom")}
	adv.RegisterHeuristic(kind.ID, fh.fn)

	_, err := adv.Recommend(context.Background(), kind, "f", SessionContext{SessionID: "s"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend with an erroring heuristic: err = %v, want ErrNoAdvice", err)
	}
}

func TestHeuristicAdvisor_OutOfRangeConfidenceDegradesToNoAdvice(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_badconf_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{decision: true, confidence: 150, modelID: "heuristic/toy-v1"}
	adv.RegisterHeuristic(kind.ID, fh.fn)

	_, err := adv.Recommend(context.Background(), kind, "f", SessionContext{SessionID: "s"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend with confidence=150: err = %v, want ErrNoAdvice", err)
	}
}

func TestHeuristicAdvisor_NilAdvisorRecommendDegradesToNoAdvice(t *testing.T) {
	var adv *HeuristicAdvisor
	kind := toyAdviceKind("hx_nil_kind", "v1", SafetyReversible)
	_, err := adv.Recommend(context.Background(), kind, "f", SessionContext{SessionID: "s"})
	if !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("nil *HeuristicAdvisor.Recommend: err = %v, want ErrNoAdvice", err)
	}
	// Dismiss and RegisterHeuristic/SetKindGate must also be nil-safe —
	// none of these should panic.
	adv.Dismiss(SessionContext{}, kind, "f")
	adv.RegisterHeuristic(kind.ID, func(Features) (bool, int, string, error) { return false, 0, "", nil })
	adv.SetKindGate(kind.ID, func() bool { return true })
}

func TestHeuristicAdvisor_DuplicateRegisterPanics(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_dup_kind", "v1", SafetyReversible)
	adv.RegisterHeuristic(kind.ID, func(Features) (bool, int, string, error) { return false, 0, "", nil })

	defer func() {
		if recover() == nil {
			t.Fatal("second RegisterHeuristic for the same kind id did not panic")
		}
	}()
	adv.RegisterHeuristic(kind.ID, func(Features) (bool, int, string, error) { return true, 0, "", nil })
}

// TestHeuristicAdvisor_CacheNeverCrossesSessions mirrors LLMAdvisor's own
// cross-session isolation proof.
func TestHeuristicAdvisor_CacheNeverCrossesSessions(t *testing.T) {
	adv := NewHeuristicAdvisor()
	kind := toyAdviceKind("hx_session_isolation_kind", "v1", SafetyReversible)
	fh := &fixedHeuristic{decision: true, confidence: 80, modelID: "heuristic/toy-v1"}
	adv.RegisterHeuristic(kind.ID, fh.fn)

	if _, err := adv.Recommend(context.Background(), kind, "same-features", SessionContext{SessionID: "sess-a"}); err != nil {
		t.Fatalf("sess-a Recommend: %v", err)
	}
	adv.Dismiss(SessionContext{SessionID: "sess-a"}, kind, "same-features")

	rec, err := adv.Recommend(context.Background(), kind, "same-features", SessionContext{SessionID: "sess-b"})
	if err != nil {
		t.Fatalf("sess-b Recommend after sess-a dismissed the SAME features: %v, want a fresh recommendation", err)
	}
	if rec.CacheHit {
		t.Errorf("sess-b's first ask reported CacheHit=true — sessions must not share cache entries")
	}
	if fh.calls != 2 {
		t.Fatalf("heuristic called %d times across two distinct sessions, want 2 (no cross-session cache reuse)", fh.calls)
	}
}
