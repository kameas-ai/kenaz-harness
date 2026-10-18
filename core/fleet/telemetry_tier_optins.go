// Package fleet — telemetry_tier_optins.go
//
// Bridges the 3-tier consent radio (TelemetryConsent, telemetry_consent.go)
// to the per-class opt-in store (telemetry_optins.go) that actually gates
// the OTLP log lane's admission decision (LogKindsAdmittedBy,
// log_event_kind.go).
//
// # The bug this closes
//
// FleetTelemetryPanel.vue persists a consent LEVEL (none/aggregate/full) via
// SetFleetTelemetryConsent -> TelemetryConsent.SetLevel, which is LOCAL
// dataDir-only persistence. Separately, LogKindsAdmittedBy admits a log kind
// only if its class is opted in via the FLEET-authoritative per-class store
// (GetTelemetryOptIns / PutTelemetryOptIns) — an empty snapshot admits
// nothing (fail-closed, deliberate, see log_event_kind.go). Before this
// file, nothing wired the two together: the only writer of per-class
// opt-ins was the single-class toggle FleetSetTelemetryOptIn, which no .vue
// calls. Choosing "Full" previewed "Log records ... will be sent" while the
// opt-in snapshot stayed empty forever, so LogKindsAdmittedBy admitted
// nothing regardless of the chosen tier.
//
// # The mapping
//
// Picking a tier writes the FULL per-class opt-in vector implied by that tier
// — the whole vector, not a diff — so downgrading (e.g. full -> none)
// actively clears stale `true` rows instead of stranding them opted in forever
// with no future write to turn them back off.
//
//   - ConsentNone:      every ceiling class -> false.
//   - ConsentAggregate: the three COUNT classes -> true
//     (harness.usage_counts, harness.tool_calls, harness.errors);
//     every other ceiling class -> false.
//   - ConsentFull:      every ceiling class -> true.
//
// # AMENDMENT 2026-09-16 (supersedes "option B of fleet brief #94" for the
// aggregate row; needs owner ratification — see telemetry-plan.md §2.3)
//
// Option B mapped aggregate to all-false, reasoning that every ceiling class
// exists only to gate LOG-record admission and aggregate promises "no log
// records". The first half of that stopped being true, and the conclusion was
// already wrong end to end:
//
//   - Fleet's receiver gates the METRICS lane on these same classes
//     (kenaz-fleet service/telemetry/receiver.go HandleMetrics: a metric is
//     dropped unless its class is opted in). With aggregate all-false, every
//     counter the aggregate tier is *for* was discarded server-side, traces
//     need harness.diagnostics (Team+), and logs are off — so choosing
//     Aggregate produced nothing anywhere. The tier was a no-op that reported
//     itself as on: exactly the "lie" the unwired sweep exists to end.
//   - The classes are, by Fleet's own definition, count classes:
//     harness.usage_counts "carries aggregated harness session / conversation
//     counts", harness.tool_calls "counts and latencies (no payloads)",
//     harness.errors "error rate and categories". Aggregate's promise —
//     "Counts + durations only" — is these classes.
//
// What keeps "no log records under Aggregate" true is no longer an empty
// opt-in vector; it is an explicit switch. FleetOTLPPipeline.SetLogLaneEnabled
// closes the event lane unless EFFECTIVE consent is full, and UsageEmitter
// routes an aggregate occurrence to a label-less counter, never to a record.
// Both are pinned on wire bytes by
// TestUsageLane_AggregateConsent_SendsLabellessDeltaCountersAndNoLogRecords
// and TestAggregateTierVector_OpensCountersButNeverTheLogLane.
//
// sigil.* stays false under aggregate: those kinds have no counter form in the
// declared metric budget, so opting them in would gate nothing.
//
// The mapping is scanned over CeilingClasses() (the classes the compiled
// ceiling actually names) rather than fleet's full KnownTelemetryClasses
// list (telemetry_optins.go) — see CeilingClasses' doc comment for why
// those two lists differ and which one this mapping deliberately follows.
package fleet

// TierOptInUpdates returns the complete per-class opt-in vector implied by
// level, ready to hand to Client.PutTelemetryOptIns. It always returns one
// TelemetryOptInItem per CeilingClasses() entry, in that (sorted) order,
// with OptedIn set per the mapping documented above the package. OptedAt and
// Source are left zero-valued: the server forces source=user_self and
// opted_at=NOW() on every PUT (telemetry_optins.go), which is semantically
// correct here — a tier change is direct, first-party user consent.
func TierOptInUpdates(level ConsentLevel) []TelemetryOptInItem {
	classes := CeilingClasses()
	out := make([]TelemetryOptInItem, 0, len(classes))
	for _, class := range classes {
		out = append(out, TelemetryOptInItem{Class: class, OptedIn: tierOptsIn(level, class)})
	}
	return out
}

// aggregateCountClasses are the classes the Aggregate tier opts in: exactly
// the classes that gate a declared label-less counter (usageCounterClass).
// TestAggregateCountClasses_MatchTheCounterBudget keeps the two in step.
var aggregateCountClasses = map[string]bool{
	"harness.usage_counts": true,
	"harness.tool_calls":   true,
	"harness.errors":       true,
}

func tierOptsIn(level ConsentLevel, class string) bool {
	switch level {
	case ConsentFull:
		return true
	case ConsentAggregate:
		return aggregateCountClasses[class]
	default:
		return false
	}
}
