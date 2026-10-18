package fleet

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestCeilingClasses_PinnedClassSet pins the exact class set the tier→
// opt-ins mapping (TierOptInUpdates) is derived from. If a future kind
// vendor bump (schema/kinds.json) adds a kind under a NEW class, this test
// fails until a human updates the pin here AND decides — in the
// TestTierOptInUpdates_* tests below — whether that new class should be
// true under aggregate and/or full. See telemetry_tier_optins.go's package
// doc for why this friction is deliberate.
func TestCeilingClasses_PinnedClassSet(t *testing.T) {
	want := []string{
		"harness.errors",
		"harness.tool_calls",
		"harness.usage_counts",
		"sigil.predictions",
		"sigil.suggestions",
	}
	got := CeilingClasses()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CeilingClasses() = %v, want %v\n"+
			"(schema/kinds.json's class set changed — update this pin AND "+
			"telemetry_tier_optins.go's TierOptInUpdates mapping)", got, want)
	}
}

func TestTierOptInUpdates_None_AllFalse(t *testing.T) {
	assertAllOptedIn(t, TierOptInUpdates(ConsentNone), false)
}

func TestTierOptInUpdates_Aggregate_OptsInExactlyTheCountClasses(t *testing.T) {
	// AMENDMENT 2026-09-16 (telemetry_tier_optins.go): aggregate used to be
	// all-false, which made the tier a no-op end to end — Fleet gates the
	// METRICS lane on these classes too. "No log records under Aggregate" is
	// now enforced by the lane switch, asserted on wire bytes below.
	want := map[string]bool{
		"harness.errors":       true,
		"harness.tool_calls":   true,
		"harness.usage_counts": true,
		"sigil.predictions":    false,
		"sigil.suggestions":    false,
	}
	got := map[string]bool{}
	for _, u := range TierOptInUpdates(ConsentAggregate) {
		got[u.Class] = u.OptedIn
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregate vector = %v, want %v", got, want)
	}
}

// TestAggregateCountClasses_MatchTheCounterBudget keeps the tier mapping and
// the declared counter set in step: a class opted in under aggregate that
// gates no counter would be an opt-in that does nothing, and a counter whose
// class aggregate leaves off could never be sent.
func TestAggregateCountClasses_MatchTheCounterBudget(t *testing.T) {
	fromCounters := map[string]bool{}
	for _, c := range UsageCounters() {
		class, ok := UsageCounterClass(c)
		if !ok {
			t.Fatalf("counter %s has no class", c)
		}
		fromCounters[class] = true
	}
	if !reflect.DeepEqual(fromCounters, aggregateCountClasses) {
		t.Fatalf("aggregate opts in %v but the counter budget is gated by %v", aggregateCountClasses, fromCounters)
	}
}

func TestTierOptInUpdates_Full_AllTrue(t *testing.T) {
	assertAllOptedIn(t, TierOptInUpdates(ConsentFull), true)
}

func TestTierOptInUpdates_CoversEveryCeilingClass(t *testing.T) {
	updates := TierOptInUpdates(ConsentFull)
	classes := CeilingClasses()
	if len(updates) != len(classes) {
		t.Fatalf("TierOptInUpdates returned %d classes, want %d (one per CeilingClasses entry)",
			len(updates), len(classes))
	}
	got := make([]string, len(updates))
	for i, u := range updates {
		got[i] = u.Class
	}
	if !reflect.DeepEqual(got, classes) {
		t.Fatalf("TierOptInUpdates classes = %v, want %v (CeilingClasses order)", got, classes)
	}
}

func assertAllOptedIn(t *testing.T, updates []TelemetryOptInItem, want bool) {
	t.Helper()
	if len(updates) == 0 {
		t.Fatal("TierOptInUpdates returned no updates")
	}
	for _, u := range updates {
		if u.OptedIn != want {
			t.Errorf("class %q: OptedIn = %v, want %v", u.Class, u.OptedIn, want)
		}
	}
}

// TestTierOptInUpdates_GateAdmission asserts the egress gate
// (LogKindsAdmittedBy) admits exactly the expected kinds from each tier's
// derived opt-in vector: nothing for none, every compiled kind for full.
// Aggregate's vector is asserted through the real gate, lane switch included,
// in TestAggregateTierVector_OpensCountersButNeverTheLogLane.
func TestTierOptInUpdates_GateAdmission(t *testing.T) {
	if admitted := LogKindsAdmittedBy(TierOptInUpdates(ConsentNone)); len(admitted) != 0 {
		t.Errorf("none: admitted %d kinds, want 0", len(admitted))
	}

	full := LogKindsAdmittedBy(TierOptInUpdates(ConsentFull))
	wantKinds := CeilingLogEventKinds()
	if len(full) != len(wantKinds) {
		t.Fatalf("full: admitted %d kinds, want %d (every compiled kind)", len(full), len(wantKinds))
	}
	for _, kind := range wantKinds {
		if _, ok := full[LogEventKind(kind)]; !ok {
			t.Errorf("full: kind %q not admitted", kind)
		}
	}
}

// TestAggregateTierVector_OpensCountersButNeverTheLogLane drives the REAL
// aggregate opt-in vector through the real pipeline and asserts on wire
// bytes: counters arrive, and not one log record does — even when a caller
// bypasses UsageEmitter's routing and asks the pipeline for an event directly.
func TestAggregateTierVector_OpensCountersButNeverTheLogLane(t *testing.T) {
	r := newActiveRig(t, ConsentAggregate, identityA, TierOptInUpdates(ConsentAggregate))
	ctx := context.Background()

	r.emitter.ConversationStarted(ctx, "x", "openai")
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.emitter.Error(ctx, ErrorCategoryAuth, false)

	// Belt and braces: the class IS opted in, so only the lane switch stands
	// between this call and the network.
	if r.pipeline.EmitEvent(ctx, LogKindHarnessConversationStarted, nil) {
		t.Error("the pipeline accepted an event record while the log lane is closed (aggregate)")
	}
	r.flush()

	if n := len(r.fleet.byPath("/otlp/v1/logs")); n != 0 {
		t.Fatalf("aggregate's opt-in vector let %d log request(s) out", n)
	}
	got := sumByName(decodeMetrics(t, r.fleet.byPath("/otlp/v1/metrics")))
	for _, name := range []string{"harness.conversations.started", "harness.tool.invocations", "harness.errors"} {
		if got[name] != 1 {
			t.Errorf("%s = %d, want 1 — aggregate must yield counts (all: %v)", name, got[name], got)
		}
	}
}

// TestNoneTierVector_SendsNothing: downgrading to none clears every class, so
// even an ACTIVE pipeline with consent still mistakenly reading "full" would
// have nothing admitted.
func TestNoneTierVector_SendsNothing(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, TierOptInUpdates(ConsentNone))
	ctx := context.Background()
	r.emitter.ConversationStarted(ctx, "11111111-2222-4333-8444-555555555555", "openai")
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.flush()
	if reqs := r.fleet.snapshot(); len(reqs) != 0 {
		t.Fatalf("none's vector admitted traffic: %v", pathsOf(reqs))
	}
}
