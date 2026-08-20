package openaiwire

// structured-output-is-reachable-01PMZE14 WP07 — knobcoverage sees
// llm.RequestKnobs (spec §7 G-2). Mirrors
// core/agentgraph/knob_coverage_guard_test.go's pattern for ModelAttrs:
// registrations live in init() next to the consumer (knob_coverage.go),
// this file is only the CI-enforced assertion. Once this lands,
// base.go's docstring can no longer drift from the code again without
// scripts/ci/check-knob-coverage.sh noticing.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

func TestKnobCoverage_RequestKnobs(t *testing.T) {
	uncovered := knobcoverage.Uncovered[llm.RequestKnobs]()
	if len(uncovered) > 0 {
		t.Fatalf("RequestKnobs fields with no registered runtime consumer and no deferral: %v — "+
			"either wire a consumer and call knobcoverage.Register from knob_coverage.go's init(), "+
			"or call knobcoverage.RegisterDeferred with blocker+owner+date "+
			"(CLAUDE.md: \"'We'll get to it' is not a reason\")", uncovered)
	}
}
