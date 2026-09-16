package fleet

import (
	"reflect"
	"testing"
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

func TestTierOptInUpdates_Aggregate_AllFalse(t *testing.T) {
	// Aggregate's own preview text (FleetTelemetryPanel.vue) promises "No
	// log records". Every ceiling class exists only to gate log-record
	// admission (LogKindsAdmittedBy), so aggregate's vector must be
	// all-false — identical to none's — until a class governing some other
	// signal type is added to the ceiling.
	assertAllOptedIn(t, TierOptInUpdates(ConsentAggregate), false)
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
// derived opt-in vector: nothing for none/aggregate, every compiled kind for
// full.
func TestTierOptInUpdates_GateAdmission(t *testing.T) {
	if admitted := LogKindsAdmittedBy(TierOptInUpdates(ConsentNone)); len(admitted) != 0 {
		t.Errorf("none: admitted %d kinds, want 0", len(admitted))
	}
	if admitted := LogKindsAdmittedBy(TierOptInUpdates(ConsentAggregate)); len(admitted) != 0 {
		t.Errorf("aggregate: admitted %d kinds, want 0 (no log records)", len(admitted))
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
