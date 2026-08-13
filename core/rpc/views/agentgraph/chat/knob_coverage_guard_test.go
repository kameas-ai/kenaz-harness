package chat

// autonomy-knobs-live-01PMAG02 WP07 — knob-coverage guard.
//
// The mission started from a single finding: six of the seven autonomy
// knobs resolved through the three-layer chain and were read by
// nothing (spec §1). Nothing caught that. This test is the tripwire:
// core/autonomy.MissingConsumers() reflects over ResolvedKnobs and
// reports any field with zero registered consumers. WP01/WP02/WP03/
// WP04/WP05/WP06 each declare themselves via autonomy.RegisterConsumer
// in an init() co-located with their wiring site (see the init()
// functions in chat_runner.go, kernel_tool_adapter.go, and
// llm_provider_adapter.go in this package).
//
// This package is the right place for the CI-enforced gate: it
// transitively contains every one of the seven knobs' wiring sites
// today, so by the time `go test` runs this file, every init() in the
// package has already fired (Go runs all of a package's init()
// functions before any of its tests). A guard test living inside
// core/autonomy itself would not see this — nothing in that package
// imports its own consumers (and must not, to avoid a cycle back into
// core/rpc). If a future knob's wiring site lands in a different
// package, either that package must also import this one (so its
// init() fires alongside these), or the guard moves to wherever
// imports the full consumer set — see wiring-integrity-01PMAG04 for
// the more general mechanism this is meant to be absorbed into.
//
// # Migration to core/wiring/knobcoverage (wiring-integrity-01PMAG04 WP07)
//
// That package generalises exactly this mechanism to any resolved-
// config struct, and is where this guard is meant to end up:
// core/autonomy's RegisterConsumer/Consumers/MissingConsumers get
// replaced by knobcoverage.Register[autonomy.ResolvedKnobs](field,
// desc) at each of the same call sites, and this test's body becomes
// `if got := knobcoverage.Uncovered[autonomy.ResolvedKnobs](); len(got)
// > 0 { t.Fatalf(...) }`. SourceTrace and PostureMode (resolver
// bookkeeping, not tunable knobs — see core/autonomy/registry.go's
// knobFieldSkip) need an explicit
// knobcoverage.RegisterDeferred[autonomy.ResolvedKnobs]("SourceTrace",
// "resolver bookkeeping, not a tunable knob") pair, since Uncovered
// there reflects over ALL exported fields rather than skipping a
// hardcoded list the way MissingConsumers does.
//
// core/wiring/knobcoverage does not exist on this branch's base
// (b4b45961) — it ships via a sibling mission (wiring-integrity-
// 01PMAG04) that has landed on release/v0.60.0 but not here yet.
// Importing it now would break every package that transitively depends
// on this one (core/rpc/api.go, core/serve, cmd/harness-served,
// cmd/harness-vm, main.go — essentially the whole binary), so the swap
// stays core/autonomy-backed on this branch; the integrator applies the
// migration described above once this branch is merged where
// knobcoverage exists. core/autonomy/registry.go stays in place as the
// working mechanism until then — it is not superseded by anything on
// this branch, only slated to be once merged.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// WP07 migration (integration 2026-08-12): the guard rides the general
// core/wiring/knobcoverage mechanism from wiring-integrity-01PMAG04.
// Registrations live in init() blocks co-located with each consumer
// (chat_runner.go, kernel_tool_adapter.go, llm_provider_adapter.go);
// SourceTrace/PostureMode are RegisterDeferred as resolver bookkeeping.
// knobcoverage's own TestKnobCoverage_Mechanism proves the mechanism
// can fail, replacing the old DetectsARemovedConsumer tautology check
// (autonomy.ClearConsumersForTest had no knobcoverage equivalent by
// design — its registry is append-only at init).
func TestKnobCoverageGuard_EveryKnobHasARegisteredConsumer(t *testing.T) {
	uncovered := knobcoverage.Uncovered[autonomy.ResolvedKnobs]()
	if len(uncovered) > 0 {
		t.Fatalf("knobs with no registered runtime consumer: %v — "+
			"either wire a consumer and call knobcoverage.Register "+
			"from its package's init(), or the knob should be deleted "+
			"from autonomy.ResolvedKnobs (spec FR-001)", uncovered)
	}
}
