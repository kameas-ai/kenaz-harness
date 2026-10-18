package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	settingsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

func newModelCallsAPI(t *testing.T) *API {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	a := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(a.Shutdown)
	if a.modelCalls == nil {
		t.Fatal("New wired no model-call store over a real DataDir")
	}
	return a
}

// TestModelCallsWiring_SettingsReachConsumers drives the three Settings
// dials through the wiring New() builds (real sqlite under a real
// DataDir) to their consumers:
//   - ModelCallRetentionDays decides what the wired Pruner deletes;
//   - ModelCallStatsPullDisabled decides whether the wired StatsPuller
//     queues a pull for an eligible OpenRouter call (the puller IS wired:
//     the production registry implements the stats seam).
func TestModelCallsWiring_SettingsReachConsumers(t *testing.T) {
	t.Parallel()
	a := newModelCallsAPI(t)
	ctx := context.Background()
	w := a.modelCalls
	if w.puller == nil {
		t.Fatal("the production LLM registry did not satisfy modelcalls.StatsSource — no stats pull is wired")
	}

	now := time.Now()
	old := genaitrace.CallRecord{SpanID: "old", Start: now.Add(-10 * 24 * time.Hour), End: now.Add(-10 * 24 * time.Hour), OK: true}
	fresh := genaitrace.CallRecord{SpanID: "fresh", Start: now.Add(-time.Hour), End: now.Add(-time.Hour), OK: true}
	if err := w.store.Insert(ctx, []genaitrace.CallRecord{old, fresh}); err != nil {
		t.Fatal(err)
	}
	if n, err := w.pruner.PruneOnce(ctx); err != nil || n != 0 {
		t.Fatalf("default 30-day retention pruned %d, %v; want 0", n, err)
	}
	if err := a.settingsImpl.SetModelCallTelemetry(ctx, settingsview.ModelCallTelemetrySettings{
		StatsPullEnabled: false, RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := w.pruner.PruneOnce(ctx); err != nil || n != 1 {
		t.Fatalf("7-day retention pruned %d, %v; want 1 (the 10-day-old call)", n, err)
	}

	eligible := genaitrace.CallRecord{SpanID: "or", OK: true, Provider: "openrouter", ResponseID: "gen-x", ProfileID: "p"}
	w.puller.Consider(eligible)
	if got := w.puller.Pending(); got != 0 {
		t.Fatalf("stats pull OFF still queued %d pull(s)", got)
	}
	if err := a.settingsImpl.SetModelCallTelemetry(ctx, settingsview.ModelCallTelemetrySettings{
		StatsPullEnabled: true, RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	w.puller.Consider(eligible)
	if got := w.puller.Pending(); got != 1 {
		t.Fatalf("stats pull ON queued %d pull(s), want 1", got)
	}
	got, err := a.settingsImpl.GetModelCallTelemetry(ctx)
	if err != nil || !got.StatsPullEnabled || got.RetentionDays != 7 || got.EffectiveRetentionDays != 7 || got.ShareWithFleet {
		t.Fatalf("GetModelCallTelemetry = %+v, %v", got, err)
	}
	if err := a.settingsImpl.SetModelCallTelemetry(ctx, settingsview.ModelCallTelemetrySettings{RetentionDays: -1}); err == nil {
		t.Fatal("a negative retention must be refused")
	}
}

// TestBindings_SessionsModelCalls: the binding lists the session's stored
// calls by the per-install keyed hash of the raw id it is given — the
// store never sees the raw id — and another session's calls stay out.
func TestBindings_SessionsModelCalls(t *testing.T) {
	t.Parallel()
	a := newModelCallsAPI(t)
	ctx := context.Background()
	start := time.UnixMilli(1_760_000_000_000)
	if err := a.modelCalls.store.Insert(ctx, []genaitrace.CallRecord{
		{SpanID: "s1-a", Start: start, End: start.Add(time.Second), OK: true, SessionHash: genaitrace.SessionHash("sess-1"), RequestModel: "m-a"},
		{SpanID: "s1-b", Start: start.Add(time.Minute), End: start.Add(time.Minute), OK: false, ErrorType: "timeout", SessionHash: genaitrace.SessionHash("sess-1")},
		{SpanID: "s2-a", Start: start, End: start, OK: true, SessionHash: genaitrace.SessionHash("sess-2")},
	}); err != nil {
		t.Fatal(err)
	}
	b := NewBindings(a)
	got, err := b.Sessions_ModelCalls("sess-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SpanID != "s1-b" || got[0].ErrorType != "timeout" || got[1].RequestModel != "m-a" || got[1].DurationMS != 1000 {
		t.Fatalf("Sessions_ModelCalls(sess-1) = %+v", got)
	}
	if none, err := b.Sessions_ModelCalls("", 10); err != nil || len(none) != 0 {
		t.Fatalf("empty session id: %v %v", none, err)
	}
}
