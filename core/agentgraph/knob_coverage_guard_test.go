package agentgraph

// structured-output-is-reachable-01PMZE14 WP03 — knobcoverage sees
// agentgraph.ModelAttrs.
//
// The class this gate closes: a codegen'd manifest attr, declared and
// authored, with no reader. ModelAttrs.JsonSchema (spec §1.2) and, before
// it, ModelAttrs.StreamToChat (docs/unwired-ledger.md:1674) are two
// instances of exactly this class in one struct, both found by hand —
// no existing gate sees node attrs at all: check-output-ports.sh sees
// only Outputs[...] writes, check-agentgraph-convergence.sh is
// kind-level, and check-knob-coverage.sh (before this WP) registered
// only autonomy.ResolvedKnobs. Mirrors the pattern in
// core/rpc/views/agentgraph/chat/knob_coverage_guard_test.go —
// registrations live in init() next to the consumer (exec_compute.go),
// this file is only the CI-enforced assertion.
//
// scripts/ci/check-knob-coverage.sh runs `go test ./core/... -run
// 'TestKnobCoverage'` with no per-mission script change required.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

func TestKnobCoverage_ModelAttrs(t *testing.T) {
	uncovered := knobcoverage.Uncovered[ModelAttrs]()
	if len(uncovered) > 0 {
		t.Fatalf("ModelAttrs fields with no registered runtime consumer: %v — "+
			"either wire a consumer and call knobcoverage.Register from "+
			"exec_compute.go's init(), or knobcoverage.RegisterDeferred "+
			"with blocker+owner+date if a real consumer does not exist yet "+
			"(CLAUDE.md: \"'We'll get to it' is not a reason\")", uncovered)
	}
}
