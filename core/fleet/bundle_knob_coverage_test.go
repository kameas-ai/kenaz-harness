package fleet

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// TestKnobCoverage_Bundle is picked up automatically by
// scripts/ci/check-knob-coverage.sh (module-wide "TestKnobCoverage*"
// scan). Fails when a new Bundle field is added with neither a Register
// nor a RegisterDeferred entry in bundle_knob_coverage.go — the
// authoring-time tripwire spec §7 G-3 asks for, so a third silently-inert
// bundle section is caught before merge instead of by hand a release
// later (spec §1.1/§1.2).
func TestKnobCoverage_Bundle(t *testing.T) {
	uncovered := knobcoverage.Uncovered[Bundle]()
	if len(uncovered) != 0 {
		t.Fatalf("fleet.Bundle has uncovered fields: %v — register with knobcoverage.Register/RegisterDeferred in bundle_knob_coverage.go", uncovered)
	}
}
