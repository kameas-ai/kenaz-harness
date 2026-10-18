package fleet

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock is a settable time source.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_800_000_000, 0)} }
func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const localSessionID = "01JSESSIONLOCALONLYNEVEREXPORTED"

func TestTracker_SegmentLifecycle_ReachesFleetPairedAndWithTotals(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	clock := newFakeClock()
	tr := NewConversationTracker(r.emitter, WithIdleTimeout(10*time.Minute), WithTrackerClock(clock.now))
	ctx := context.Background()

	// Three turns of one working session.
	tr.TurnStarted(ctx, localSessionID, "anthropic")
	tr.LLMResponse(ctx, localSessionID, 1000, 200, 0.01)
	tr.ToolInvoked(ctx, localSessionID, "kenaz__bash", 50*time.Millisecond, true)
	clock.advance(2 * time.Minute)
	tr.TurnStarted(ctx, localSessionID, "anthropic") // same segment — must NOT re-open
	tr.LLMResponse(ctx, localSessionID, 500, 100, 0.005)
	clock.advance(3 * time.Minute)
	tr.TurnStarted(ctx, localSessionID, "anthropic")
	tr.LLMResponse(ctx, localSessionID, 250, 50, 0.0025)

	// Not yet idle.
	clock.advance(9 * time.Minute)
	tr.Sweep(ctx)
	if tr.OpenSegments() != 1 {
		t.Fatalf("segment closed before the idle timeout")
	}
	// Idle.
	clock.advance(2 * time.Minute)
	tr.Sweep(ctx)
	if tr.OpenSegments() != 0 {
		t.Fatalf("segment still open past the idle timeout")
	}
	r.flush()

	events := decodeLogs(t, r.fleet.byPath("/otlp/v1/logs"))
	var started, ended []decodedEvent
	for _, e := range events {
		switch e.kind {
		case "harness.conversation_started":
			started = append(started, e)
		case "harness.conversation_ended":
			ended = append(ended, e)
		}
	}
	if len(started) != 1 || len(ended) != 1 {
		t.Fatalf("got %d started / %d ended, want exactly 1 / 1 for one contiguous segment", len(started), len(ended))
	}
	id := started[0].body["conversation_id"]
	if id != ended[0].body["conversation_id"] {
		t.Errorf("started id %v != ended id %v", id, ended[0].body["conversation_id"])
	}
	if got := ended[0].body["token_in"]; got != int64(1750) {
		t.Errorf("token_in = %v, want 1750", got)
	}
	if got := ended[0].body["token_out"]; got != int64(350) {
		t.Errorf("token_out = %v, want 350", got)
	}
	// Duration spans first turn → last activity (5m), not → the sweep.
	if got := ended[0].body["duration_ms"]; got != int64(5*time.Minute/time.Millisecond) {
		t.Errorf("duration_ms = %v, want %d", got, int64(5*time.Minute/time.Millisecond))
	}

	// The local session id is a key, never a value.
	if bytes.Contains(r.fleet.allBytes(), []byte(localSessionID)) {
		t.Error("the local session id reached the wire")
	}
	if s, _ := id.(string); len(s) != 36 || s == localSessionID {
		t.Errorf("conversation_id %q is not a fresh UUID", s)
	}
}

func TestTracker_NewSegmentAfterIdle_GetsAnUnlinkableID(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	clock := newFakeClock()
	tr := NewConversationTracker(r.emitter, WithIdleTimeout(time.Minute), WithTrackerClock(clock.now))
	ctx := context.Background()

	tr.TurnStarted(ctx, localSessionID, "openai")
	clock.advance(2 * time.Minute)
	tr.Sweep(ctx)
	tr.TurnStarted(ctx, localSessionID, "openai") // same local session, new segment
	tr.EndAll(ctx)
	r.flush()

	ids := map[any]int{}
	for _, e := range decodeLogs(t, r.fleet.byPath("/otlp/v1/logs")) {
		if e.kind == "harness.conversation_started" {
			ids[e.body["conversation_id"]]++
		}
	}
	if len(ids) != 2 {
		t.Errorf("two segments of one session share a conversation_id (%v); ids must not link segments", ids)
	}
}

func TestTracker_PairingInvariant_NoEndedWithoutAcceptedStarted(t *testing.T) {
	r := newActiveRig(t, ConsentNone, identityA, optIns(usageClasses...))
	clock := newFakeClock()
	tr := NewConversationTracker(r.emitter, WithIdleTimeout(time.Minute), WithTrackerClock(clock.now))
	ctx := context.Background()

	// Consent is none: the segment opens locally but is not reported.
	tr.TurnStarted(ctx, localSessionID, "openai")
	tr.LLMResponse(ctx, localSessionID, 100, 10, 0)

	// Consent is granted mid-segment. The idle close must NOT emit an orphan
	// "ended" for a segment Fleet never saw start.
	// (The settings layer flips the lane switch together with consent.)
	r.consent.set(ConsentFull)
	r.pipeline.SetLogLaneEnabled(true)
	clock.advance(2 * time.Minute)
	tr.Sweep(ctx)
	r.flush()
	for _, e := range decodeLogs(t, r.fleet.byPath("/otlp/v1/logs")) {
		if e.kind == "harness.conversation_ended" {
			t.Fatalf("orphan conversation_ended reached Fleet: %v", e.body)
		}
	}

	// The next turn, with export on, opens a properly reported segment.
	tr.TurnStarted(ctx, localSessionID, "openai")
	tr.EndAll(ctx)
	r.flush()
	var started, ended int
	for _, e := range decodeLogs(t, r.fleet.byPath("/otlp/v1/logs")) {
		switch e.kind {
		case "harness.conversation_started":
			started++
		case "harness.conversation_ended":
			ended++
		}
	}
	if started != 1 || ended != 1 {
		t.Errorf("after consent came on: %d started / %d ended, want 1 / 1", started, ended)
	}
}

func TestTracker_UnreportedSegmentReopensOnceExportIsOn(t *testing.T) {
	// Activation arrives AFTER the first turn (the boot race: the user
	// prompts before enroll completes).
	f := newFakeFleet(t)
	p := NewFleetOTLPPipeline(nil)
	p.SetExportCadence(time.Hour, time.Hour)
	p.SetTelemetryOptIns(optIns(usageClasses...))
	p.SetLogLaneEnabled(true)
	e := NewUsageEmitter(p, &staticConsent{level: ConsentFull})
	tr := NewConversationTracker(e)
	ctx := context.Background()

	tr.TurnStarted(ctx, localSessionID, "openai") // pipeline inactive → unreported

	tokens := &tokenBox{tok: fakeJWT(identityA.UserID)}
	if err := p.Activate(ctx, f.base(), nil, identityA, tokens.provider(), nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	defer p.Deactivate(ctx)

	tr.TurnStarted(ctx, localSessionID, "openai") // now reported
	tr.EndAll(ctx)
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p.Flush(fctx)

	var started, ended int
	for _, ev := range decodeLogs(t, f.byPath("/otlp/v1/logs")) {
		switch ev.kind {
		case "harness.conversation_started":
			started++
		case "harness.conversation_ended":
			ended++
		}
	}
	if started != 1 || ended != 1 {
		t.Errorf("%d started / %d ended, want the post-activation segment reported exactly once", started, ended)
	}
}

func TestTracker_DropAll_ReportsNothing(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	tr := NewConversationTracker(r.emitter)
	ctx := context.Background()

	tr.TurnStarted(ctx, localSessionID, "openai")
	tr.LLMResponse(ctx, localSessionID, 999, 99, 9)
	r.flush()
	before := len(decodeLogs(t, r.fleet.byPath("/otlp/v1/logs")))

	tr.DropAll() // sign-out
	r.flush()
	if tr.OpenSegments() != 0 {
		t.Error("DropAll left segments open")
	}
	if after := len(decodeLogs(t, r.fleet.byPath("/otlp/v1/logs"))); after != before {
		t.Errorf("DropAll emitted %d record(s); sign-out must not report totals", after-before)
	}
}

func TestTracker_AggregateLifecycleBecomesCounters(t *testing.T) {
	r := newActiveRig(t, ConsentAggregate, identityA, optIns(usageClasses...))
	tr := NewConversationTracker(r.emitter)
	ctx := context.Background()

	for _, sid := range []string{"s1", "s2"} {
		tr.TurnStarted(ctx, sid, "openai")
		tr.LLMResponse(ctx, sid, 100, 10, 0)
		tr.ToolInvoked(ctx, sid, "kenaz__bash", time.Millisecond, true)
	}
	tr.TurnFailed(ctx, "s1", ErrorCategoryTransient, true)
	tr.EndAll(ctx)
	r.flush()

	if n := len(r.fleet.byPath("/otlp/v1/logs")); n != 0 {
		t.Fatalf("aggregate lifecycle produced %d log request(s)", n)
	}
	got := sumByName(decodeMetrics(t, r.fleet.byPath("/otlp/v1/metrics")))
	want := map[string]int64{
		"harness.conversations.started": 2,
		"harness.conversations.ended":   2,
		"harness.tool.invocations":      2,
		"harness.errors":                1,
		"harness.tokens.input":          200,
		"harness.tokens.output":         20,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all %v)", k, got[k], v, got)
		}
	}
}

func TestTracker_NilIsANoOp(t *testing.T) {
	var tr *ConversationTracker
	ctx := context.Background()
	tr.Start(ctx)
	tr.TurnStarted(ctx, "s", "p")
	tr.LLMResponse(ctx, "s", 1, 1, 1)
	tr.ToolInvoked(ctx, "s", "t", 0, true)
	tr.TurnFailed(ctx, "s", ErrorCategoryUnknown, false)
	tr.Sweep(ctx)
	tr.EndAll(ctx)
	tr.DropAll()
	tr.Close()
	if NewConversationTracker(nil) != nil {
		t.Error("a tracker over a nil emitter must be nil (fleet disabled)")
	}
}

func TestTracker_SubagentWorkCountsTowardTheParentConversation(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	tr := NewConversationTracker(r.emitter)
	ctx := context.Background()

	tr.TurnStarted(ctx, "parent", "anthropic")
	tr.AttributeTo("child", "parent")
	tr.AttributeTo("grandchild", "child")
	tr.TurnStarted(ctx, "child", "anthropic")
	tr.LLMResponse(ctx, "child", 100, 10, 0)
	tr.TurnStarted(ctx, "grandchild", "anthropic")
	tr.LLMResponse(ctx, "grandchild", 1000, 100, 0)
	tr.ToolInvoked(ctx, "grandchild", "kenaz__bash", time.Millisecond, true)
	if tr.OpenSegments() != 1 {
		t.Fatalf("open segments = %d, want 1 — sub-agents must not open conversations", tr.OpenSegments())
	}
	tr.EndAll(ctx)
	r.flush()

	var started int
	for _, e := range decodeLogs(t, r.fleet.byPath("/otlp/v1/logs")) {
		switch e.kind {
		case "harness.conversation_started":
			started++
		case "harness.conversation_ended":
			if e.body["token_in"] != int64(1100) || e.body["token_out"] != int64(110) {
				t.Errorf("ended totals = %v, want delegated tokens rolled into the parent", e.body)
			}
		}
	}
	if started != 1 {
		t.Errorf("conversation_started = %d, want 1", started)
	}
}
