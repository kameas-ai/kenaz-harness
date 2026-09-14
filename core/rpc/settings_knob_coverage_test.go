package rpc

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// TestKnobCoverage_Settings is the real (non-mechanism-self-test) guard
// scripts/ci/check-knob-coverage.sh looks for.
// (controls-and-readouts-that-tell-the-truth-01PMZ808 WP22, spec §5 G-1.)
//
// It asserts every exported field of settings.Settings — 82 as of this
// commit — has either a knobcoverage.Register (a real consumer) or a
// knobcoverage.RegisterDeferred (an explicit, dated reason) entry. The
// registrations live in settings_knob_coverage.go, in this same package,
// plus one pre-existing registration in
// harness_self_mcp_disabled_knob_coverage.go (also package rpc) for
// HarnessSelfMCPDisabled — both files' init() functions run before this
// test regardless of source-file order, since Go runs every init() in a
// package before any of its tests.
//
// This is a bookkeeping forcing function, not a wiring proof:
// knobcoverage.Register accepts any non-empty description and verifies
// nothing about the named consumer. A field added to settings.Settings
// without a matching Register/RegisterDeferred call fails this test
// loudly instead of silently escaping every existing sweep, which is
// the gap this WP closes.
func TestKnobCoverage_Settings(t *testing.T) {
	uncovered := knobcoverage.Uncovered[settings.Settings]()
	if len(uncovered) > 0 {
		t.Fatalf("settings.Settings fields with no knobcoverage.Register or "+
			"RegisterDeferred entry: %v — either wire a real consumer and "+
			"call knobcoverage.Register[settings.Settings] from an init() "+
			"next to it (see settings_knob_coverage.go), or call "+
			"knobcoverage.RegisterDeferred[settings.Settings] with a dated "+
			"reason if the field is a deliberate, documented gap "+
			"(controls-and-readouts-that-tell-the-truth-01PMZ808 spec §5 G-1)",
			uncovered)
	}
}
