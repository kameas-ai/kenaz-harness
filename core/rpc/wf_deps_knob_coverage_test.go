package rpc

// wf_deps_knob_coverage_test.go — automation-actually-runs-01PMZ404
// UNIT-17, G-1b. scripts/ci/check-knob-coverage.sh runs `go test
// ./core/... -run 'TestKnobCoverage'` — any test whose name starts with
// "TestKnobCoverage" is picked up automatically. See
// wf_deps_knob_coverage.go for the registrations this asserts.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

func TestKnobCoverage_WorkflowDeps(t *testing.T) {
	uncovered := knobcoverage.Uncovered[corewf.Deps]()
	if len(uncovered) > 0 {
		t.Fatalf("corewf.Deps fields with no registered runtime consumer or deferral: %v — "+
			"either wire a consumer and call knobcoverage.Register from wf_deps_knob_coverage.go, "+
			"or knobcoverage.RegisterDeferred with blocker+owner+date if a real consumer does "+
			"not exist yet (CLAUDE.md: \"'We'll get to it' is not a reason\")", uncovered)
	}
}
