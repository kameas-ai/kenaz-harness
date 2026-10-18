package risk_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/risk"
)

func TestFakeRater_ScriptedRatingReturned(t *testing.T) {
	f := risk.NewFakeRater().ScriptFor("rm_rf", risk.Rating{Score: 95, Model: "test-model", PromptVersion: "v1"})
	got, err := f.Rate(context.Background(), "rm_rf", `{}`, risk.SessionContext{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if got.Score != 95 {
		t.Errorf("Score = %d, want 95", got.Score)
	}
	if f.CallCount() != 1 {
		t.Fatalf("CallCount = %d, want 1", f.CallCount())
	}
}

func TestFakeRater_DefaultAppliesToUnscriptedTool(t *testing.T) {
	f := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 10})
	got, err := f.Rate(context.Background(), "anything_else", `{}`, risk.SessionContext{})
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if got.Score != 10 {
		t.Errorf("Score = %d, want 10 (default)", got.Score)
	}
}

func TestFakeRater_ScriptedErrorSurfaces(t *testing.T) {
	sentinel := errors.New("rater timeout")
	f := risk.NewFakeRater().ScriptErrorFor("slow_tool", sentinel)
	_, err := f.Rate(context.Background(), "slow_tool", `{}`, risk.SessionContext{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Rate error = %v, want wrapping %v", err, sentinel)
	}
}

// TestFakeRater_OutOfRangeScoreIsAnErrorNotAClamp is the WP04 proof:
// tasks.md requires "out-of-range and unparseable both surface as
// errors (which WP02's resolver turns into confirm)" — a fake that
// silently clamped an out-of-range scripted score would hide exactly
// the bug class this contract exists to catch.
func TestFakeRater_OutOfRangeScoreIsAnErrorNotAClamp(t *testing.T) {
	for _, bad := range []int{-1, 101, 1000} {
		f := risk.NewFakeRater().ScriptFor("x", risk.Rating{Score: bad})
		got, err := f.Rate(context.Background(), "x", `{}`, risk.SessionContext{})
		if err == nil {
			t.Fatalf("score %d: Rate returned nil error (want an error, not a clamp); got Rating %+v", bad, got)
		}
	}
}

// TestFakeRater_CallsRaceSafe drives Rate and Calls concurrently under
// `go test -race` — the CLAUDE.md mutex+snapshot contract this fake
// must honour since it receives writes from what would be a dispatch
// goroutine and is read by the test body.
func TestFakeRater_CallsRaceSafe(t *testing.T) {
	f := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 5})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = f.Rate(context.Background(), "concurrent_tool", `{}`, risk.SessionContext{SessionID: "s"})
			_ = f.Calls()
			_ = f.CallCount()
		}(i)
	}
	wg.Wait()
	if got := f.CallCount(); got != 50 {
		t.Fatalf("CallCount = %d, want 50", got)
	}
}

func TestFakeRater_RecordsCallArguments(t *testing.T) {
	f := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 5})
	_, _ = f.Rate(context.Background(), "bash", `{"pattern":"rm -rf /"}`, risk.SessionContext{SessionID: "sess-1"})
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("len(Calls()) = %d, want 1", len(calls))
	}
	if calls[0].Tool != "bash" || calls[0].SessionID != "sess-1" {
		t.Errorf("recorded call = %+v, want Tool=bash SessionID=sess-1", calls[0])
	}
}
