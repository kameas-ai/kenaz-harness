package knobcoverage_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// syntheticKnobs is a stand-in for a resolved-config struct like
// autonomy.ResolvedKnobs. Declared once at package scope (not inside a
// test func) because knobcoverage's registry is keyed by
// reflect.TypeFor[T](), and TestKnobCoverage_Mechanism below registers
// against this exact type — the point of this test is to prove the
// GENERAL mechanism works against an arbitrary struct, independent of
// autonomy.ResolvedKnobs (which this package deliberately does not
// import; see the package doc's "Contract between this package and its
// callers").
type syntheticKnobs struct {
	Wired    string
	Deferred int
	Missing  bool
	unexported string //nolint:unused // deliberately present: must NOT show up in Uncovered/Reports
}

func init() {
	knobcoverage.Register[syntheticKnobs]("Wired", "consumed by some_package.someFunc")
	knobcoverage.RegisterDeferred[syntheticKnobs]("Deferred", "needs some-future-mission WPxx")
	// "Missing" and "unexported" are deliberately left unregistered:
	// Missing to prove Uncovered reports it, unexported to prove
	// unexported fields are excluded from consideration entirely (a
	// struct's private bookkeeping fields, like ResolvedKnobs.SourceTrace
	// would be if it were unexported, aren't "knobs" a consumer needs).
}

// TestKnobCoverage_Mechanism is the generalised mechanism's own proof
// (wiring-integrity-01PMAG04 WP07): registering one field as covered
// and one as deferred leaves exactly the unregistered exported field
// reported by Uncovered, and Reports() surfaces the full picture
// (including the deferred reason) for anything that wants to print a
// table. Named TestKnobCoverage* so scripts/ci/check-knob-coverage.sh's
// `go test ./core/... -run 'TestKnobCoverage'` picks it up alongside
// whatever mission-specific test autonomy-knobs-live-01PMAG02's WP07
// adds for the real autonomy.ResolvedKnobs struct.
func TestKnobCoverage_Mechanism(t *testing.T) {
	got := knobcoverage.Uncovered[syntheticKnobs]()
	want := []string{"Missing"}
	if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
		t.Fatalf("Uncovered() = %v, want %v", got, want)
	}

	reports := knobcoverage.Reports[syntheticKnobs]()
	byField := map[string]knobcoverage.Report{}
	for _, r := range reports {
		byField[r.Field] = r
	}
	if len(byField) != 3 {
		t.Fatalf("Reports() returned %d fields, want 3 (exported only): %+v", len(byField), reports)
	}
	if !byField["Wired"].Covered || byField["Wired"].Description == "" {
		t.Errorf("Wired: want covered with a description, got %+v", byField["Wired"])
	}
	if !byField["Deferred"].Deferred || byField["Deferred"].DeferReason == "" {
		t.Errorf("Deferred: want deferred with a reason, got %+v", byField["Deferred"])
	}
	if byField["Missing"].Covered || byField["Missing"].Deferred {
		t.Errorf("Missing: want neither covered nor deferred, got %+v", byField["Missing"])
	}
}

// --- panic-path coverage (each in its own subtest so a synthetic type
// declared inline doesn't collide with syntheticKnobs' registry entry
// above) ---

func TestKnobCoverage_PanicsOnUntrackedType(t *testing.T) {
	type untracked struct{ X int }
	defer func() {
		if recover() == nil {
			t.Fatal("expected Uncovered on an untracked type to panic")
		}
	}()
	knobcoverage.Uncovered[untracked]()
}

func TestKnobCoverage_PanicsOnUnknownField(t *testing.T) {
	type registerUnknownField struct{ X int }
	defer func() {
		if recover() == nil {
			t.Fatal("expected Register with a nonexistent field name to panic")
		}
	}()
	knobcoverage.Register[registerUnknownField]("DoesNotExist", "desc")
}

func TestKnobCoverage_PanicsOnDuplicateRegistration(t *testing.T) {
	type dup struct{ X int }
	knobcoverage.Register[dup]("X", "first registration")
	defer func() {
		if recover() == nil {
			t.Fatal("expected a second Register/RegisterDeferred for the same field to panic")
		}
	}()
	knobcoverage.RegisterDeferred[dup]("X", "second registration should panic")
}

func TestKnobCoverage_PanicsOnEmptyDescription(t *testing.T) {
	type emptyDesc struct{ X int }
	defer func() {
		if recover() == nil {
			t.Fatal("expected Register with an empty description to panic")
		}
	}()
	knobcoverage.Register[emptyDesc]("X", "")
}

func TestKnobCoverage_PanicsOnNonStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected Register against a non-struct type parameter to panic")
		}
	}()
	knobcoverage.Register[int]("X", "desc")
}
