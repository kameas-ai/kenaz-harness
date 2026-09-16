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
// # The mapping (owner ruling 2026-09-16, option B of fleet brief #94)
//
// Picking a tier now writes the FULL per-class opt-in vector implied by
// that tier — the whole vector, not a diff — so downgrading (e.g.
// full -> none) actively clears stale `true` rows instead of stranding
// them opted in forever with no future write to turn them back off.
//
//   - ConsentNone:      every ceiling class -> false.
//   - ConsentAggregate: every ceiling class -> false. FleetTelemetryPanel.vue's
//     own preview text for aggregate is explicit: "No log records". Every
//     class in the compiled ceiling (log_event_kind.go) exists *only* to
//     gate log-record admission via LogKindsAdmittedBy — the spans and
//     metrics aggregate DOES send are gated purely by
//     TelemetryConsent.EffectiveLevel inside FleetOTLPPipeline.Activate, not
//     by any per-class opt-in. So aggregate's per-class vector is
//     indistinguishable from none's today, and that is correct, not an
//     oversight: there is currently no telemetry class in the compiled
//     ceiling whose job is "log records for aggregate". If a future kind
//     vendor bump adds one, TestCeilingClasses_PinnedClassSet
//     (telemetry_tier_optins_test.go) fails until a human decides which
//     tier(s) should carry it true.
//   - ConsentFull:      every ceiling class -> true. This is what actually
//     delivers on Full's preview promise of log records.
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
	optedIn := level == ConsentFull
	classes := CeilingClasses()
	out := make([]TelemetryOptInItem, 0, len(classes))
	for _, class := range classes {
		out = append(out, TelemetryOptInItem{Class: class, OptedIn: optedIn})
	}
	return out
}
