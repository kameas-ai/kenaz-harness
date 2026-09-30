package advice_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

func testKind(id string) advice.AdviceKind {
	return advice.AdviceKind{
		ID:            id,
		PromptVersion: "v1",
		SafetyClass:   advice.SafetyReversible,
		Extract:       func(input any) (advice.Features, error) { return input, nil },
		RenderPrompt:  func(f advice.Features) (string, string) { return "sys", "user" },
	}
}

func TestFakeAdvisor_ScriptedRecommendationReturned(t *testing.T) {
	f := advice.NewFakeAdvisor().ScriptFor("branch_now", advice.Recommendation{Decision: true, Confidence: 95})
	got, err := f.Recommend(context.Background(), testKind("branch_now"), "features", advice.SessionContext{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if got.Confidence != 95 || !got.Decision {
		t.Errorf("got %+v, want Decision=true Confidence=95", got)
	}
	if f.CallCount() != 1 {
		t.Fatalf("CallCount = %d, want 1", f.CallCount())
	}
}

func TestFakeAdvisor_DefaultAppliesToUnscriptedKind(t *testing.T) {
	f := advice.NewFakeAdvisor().ScriptDefault(advice.Recommendation{Confidence: 10})
	got, err := f.Recommend(context.Background(), testKind("anything_else"), "features", advice.SessionContext{})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if got.Confidence != 10 {
		t.Errorf("Confidence = %d, want 10 (default)", got.Confidence)
	}
}

func TestFakeAdvisor_ScriptedErrorSurfaces(t *testing.T) {
	sentinel := errors.New("advisor down")
	f := advice.NewFakeAdvisor().ScriptErrorFor("slow_kind", sentinel)
	_, err := f.Recommend(context.Background(), testKind("slow_kind"), "features", advice.SessionContext{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recommend error = %v, want wrapping %v", err, sentinel)
	}
}

// TestFakeAdvisor_OutOfRangeConfidenceIsAnErrorNotAClamp mirrors
// risk.TestFakeRater_OutOfRangeScoreIsAnErrorNotAClamp: a fake that
// silently clamped an out-of-range scripted confidence would hide the
// exact bug class the real contract exists to catch.
func TestFakeAdvisor_OutOfRangeConfidenceIsAnErrorNotAClamp(t *testing.T) {
	for _, bad := range []int{-1, 101, 1000} {
		f := advice.NewFakeAdvisor().ScriptFor("x", advice.Recommendation{Confidence: bad})
		got, err := f.Recommend(context.Background(), testKind("x"), "features", advice.SessionContext{})
		if err == nil {
			t.Fatalf("confidence %d: Recommend returned nil error (want an error, not a clamp); got %+v", bad, got)
		}
	}
}

// TestFakeAdvisor_DismissSuppressesFutureRecommend is AC-03's proof at
// the fake level, mirroring the LLMAdvisor-level proof in
// llmadvisor_test.go so a kind's own tests (WP04-06) can rely on
// FakeAdvisor for the same guarantee without a fake LLM registry.
func TestFakeAdvisor_DismissSuppressesFutureRecommend(t *testing.T) {
	f := advice.NewFakeAdvisor().ScriptFor("branch_now", advice.Recommendation{Decision: true, Confidence: 80})
	sess := advice.SessionContext{SessionID: "s1"}
	kind := testKind("branch_now")

	if _, err := f.Recommend(context.Background(), kind, "features", sess); err != nil {
		t.Fatalf("first Recommend: %v", err)
	}
	f.Dismiss(sess, kind, "features")

	_, err := f.Recommend(context.Background(), kind, "features", sess)
	if !errors.Is(err, advice.ErrNoAdvice) {
		t.Fatalf("Recommend after Dismiss: err = %v, want ErrNoAdvice", err)
	}
	if f.CallCount() != 1 {
		t.Fatalf("CallCount after dismiss+re-ask = %d, want 1", f.CallCount())
	}
}

func TestFakeAdvisor_MootSkipsWithoutRecordingACall(t *testing.T) {
	f := advice.NewFakeAdvisor().ScriptDefault(advice.Recommendation{Decision: true, Confidence: 50})
	_, err := f.Recommend(context.Background(), testKind("moot_kind"), "features", advice.SessionContext{SessionID: "s1", Moot: true})
	if !errors.Is(err, advice.ErrNoAdvice) {
		t.Fatalf("Recommend with Moot=true: err = %v, want ErrNoAdvice", err)
	}
	if f.CallCount() != 0 {
		t.Fatalf("CallCount = %d, want 0 for a moot session", f.CallCount())
	}
}

// TestFakeAdvisor_CallsRaceSafe drives Recommend and Calls concurrently
// under `go test -race` — CLAUDE.md's mutex+snapshot contract.
func TestFakeAdvisor_CallsRaceSafe(t *testing.T) {
	f := advice.NewFakeAdvisor().ScriptDefault(advice.Recommendation{Confidence: 5})
	kind := testKind("concurrent_kind")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = f.Recommend(context.Background(), kind, n, advice.SessionContext{SessionID: "s"})
			_ = f.Calls()
			_ = f.CallCount()
		}(i)
	}
	wg.Wait()
	if got := f.CallCount(); got != 50 {
		t.Fatalf("CallCount = %d, want 50", got)
	}
}

func TestFakeAdvisor_RecordsCallArguments(t *testing.T) {
	f := advice.NewFakeAdvisor().ScriptDefault(advice.Recommendation{Confidence: 5})
	_, _ = f.Recommend(context.Background(), testKind("bash_kind"), "feat", advice.SessionContext{SessionID: "sess-1"})
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("len(Calls()) = %d, want 1", len(calls))
	}
	if calls[0].KindID != "bash_kind" || calls[0].SessionID != "sess-1" {
		t.Errorf("recorded call = %+v, want KindID=bash_kind SessionID=sess-1", calls[0])
	}
}
