package labels

import (
	"context"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

// spyStore is a race-safe (mutex + snapshot, per CLAUDE.md's canonical
// pattern) test double that counts calls into Insert/UpdateAction — the
// call-count proof AC-06 requires ("capture toggle OFF => zero rows
// written, call-count proof, not row-count only"). A row-count-only
// assertion cannot distinguish "the store was never called" from "the
// store was called and correctly wrote nothing"; this double makes the
// distinction observable.
type spyStore struct {
	mu            sync.Mutex
	insertCalls   int
	updateCalls   int
	lastInsertRow Row
	lastUpdate    struct {
		sessionID, kindID, featuresHash string
		action                          UserAction
	}
}

func (s *spyStore) Insert(_ context.Context, row Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.insertCalls++
	s.lastInsertRow = row
	return nil
}

func (s *spyStore) UpdateAction(_ context.Context, sessionID, kindID, featuresHash string, action UserAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	s.lastUpdate.sessionID, s.lastUpdate.kindID, s.lastUpdate.featuresHash, s.lastUpdate.action = sessionID, kindID, featuresHash, action
	return nil
}

func (s *spyStore) snapshot() (inserts, updates int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insertCalls, s.updateCalls
}

var _ Store = (*spyStore)(nil)

func testKind(id string, safety advice.SafetyClass) advice.AdviceKind {
	return advice.AdviceKind{
		ID:            id,
		PromptVersion: "v1",
		SafetyClass:   safety,
		Extract:       func(input any) (advice.Features, error) { return input, nil },
		RenderPrompt:  func(advice.Features) (string, string) { return "sys", "usr" },
	}
}

type testFeatures struct {
	A int `json:"a"`
}

func TestCaptureAdvisor_Recommend_CapturesShownAndNotShown(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)

	// Below the ≥75 chip gate: still captured, shown=false (spec §2d).
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: true, Confidence: 60, Model: "heuristic/x", Rung: advice.RungHeuristic})
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	if _, err := ca.Recommend(context.Background(), kind, testFeatures{A: 1}, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	inserts, _ := spy.snapshot()
	if inserts != 1 {
		t.Fatalf("insert calls = %d, want 1", inserts)
	}
	if spy.lastInsertRow.Shown {
		t.Errorf("Shown = true for a confidence-60 recommendation, want false (below ≥75 gate)")
	}
	if spy.lastInsertRow.UserAction != ActionIgnored {
		t.Errorf("UserAction = %q, want %q (default)", spy.lastInsertRow.UserAction, ActionIgnored)
	}

	// Above the gate: shown=true.
	inner.ScriptFor("compact_now", advice.Recommendation{Decision: true, Confidence: 90, Model: "heuristic/y", Rung: advice.RungHeuristic})
	compactKind := testKind("compact_now", advice.SafetySuggestOnly)
	if _, err := ca.Recommend(context.Background(), compactKind, testFeatures{A: 2}, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	inserts, _ = spy.snapshot()
	if inserts != 2 {
		t.Fatalf("insert calls = %d, want 2", inserts)
	}
	if !spy.lastInsertRow.Shown {
		t.Errorf("Shown = false for a confidence-90 decision=true recommendation, want true")
	}
}

// TestCaptureAdvisor_CaptureOff_ZeroStoreCalls is AC-06's headline proof:
// a disabled capture toggle produces ZERO calls into the store — not
// merely zero rows the store chooses not to persist.
func TestCaptureAdvisor_CaptureOff_ZeroStoreCalls(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: true, Confidence: 95, Model: "heuristic/x", Rung: advice.RungHeuristic})
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return false })

	if _, err := ca.Recommend(context.Background(), kind, testFeatures{A: 1}, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	ca.Dismiss(advice.SessionContext{SessionID: "s1"}, kind, testFeatures{A: 1})
	ca.RecordAction(context.Background(), advice.SessionContext{SessionID: "s1"}, kind, testFeatures{A: 1}, ActionAccepted)

	inserts, updates := spy.snapshot()
	if inserts != 0 {
		t.Errorf("insert calls = %d, want 0 (capture disabled)", inserts)
	}
	if updates != 0 {
		t.Errorf("update calls = %d, want 0 (capture disabled)", updates)
	}
}

func TestCaptureAdvisor_Recommend_CacheHitNotRecaptured(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)
	// FakeAdvisor never sets CacheHit itself; simulate a cache-hit
	// scenario by scripting a Recommendation with CacheHit already true
	// (a real HeuristicAdvisor does this on its second call for
	// materially identical features — see heuristic_test.go's own
	// cache-hit coverage). CaptureAdvisor must not double-capture it.
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: true, Confidence: 95, Model: "heuristic/x", Rung: advice.RungHeuristic, CacheHit: true})
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	if _, err := ca.Recommend(context.Background(), kind, testFeatures{A: 1}, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	inserts, _ := spy.snapshot()
	if inserts != 0 {
		t.Errorf("insert calls = %d for a CacheHit recommendation, want 0", inserts)
	}
}

func TestCaptureAdvisor_Recommend_ErrorNotCaptured(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)
	inner.ScriptErrorFor(kind.ID, advice.ErrNoAdvice)
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	if _, err := ca.Recommend(context.Background(), kind, testFeatures{A: 1}, advice.SessionContext{SessionID: "s1"}); err == nil {
		t.Fatal("Recommend: want error, got nil")
	}
	inserts, _ := spy.snapshot()
	if inserts != 0 {
		t.Errorf("insert calls = %d for an errored Recommend, want 0", inserts)
	}
}

func TestCaptureAdvisor_Dismiss_UpdatesActionAndCallsInner(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	sess := advice.SessionContext{SessionID: "s1"}
	ca.Dismiss(sess, kind, testFeatures{A: 1})

	_, updates := spy.snapshot()
	if updates != 1 {
		t.Fatalf("update calls = %d, want 1", updates)
	}
	if spy.lastUpdate.action != ActionDismissed {
		t.Errorf("recorded action = %q, want %q", spy.lastUpdate.action, ActionDismissed)
	}

	// inner.Dismiss must ALSO have fired (AC-03's cache tombstone) — a
	// second Recommend for the same features is now suppressed by the
	// FAKE's own dismissed-key tracking.
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: true, Confidence: 99})
	if _, err := inner.Recommend(context.Background(), kind, testFeatures{A: 1}, sess); err == nil {
		t.Error("inner.Recommend after Dismiss: want ErrNoAdvice (dismissed), got a recommendation")
	}
}

func TestRecordActionIfSupported_NonCaptureAdvisor_IsNoop(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)
	// inner is a plain *advice.FakeAdvisor, not a *CaptureAdvisor.
	RecordActionIfSupported(context.Background(), inner, advice.SessionContext{SessionID: "s1"}, kind, testFeatures{A: 1}, ActionAccepted)
	// No panic, no observable effect — nothing to assert beyond "did not crash".
}

func TestRecordActionIfSupported_CaptureAdvisor_RecordsAction(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind("branch_now", advice.SafetyReversible)
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	RecordActionIfSupported(context.Background(), advice.Advisor(ca), advice.SessionContext{SessionID: "s1"}, kind, testFeatures{A: 1}, ActionAutoActed)

	_, updates := spy.snapshot()
	if updates != 1 {
		t.Fatalf("update calls = %d, want 1", updates)
	}
	if spy.lastUpdate.action != ActionAutoActed {
		t.Errorf("recorded action = %q, want %q", spy.lastUpdate.action, ActionAutoActed)
	}
}
