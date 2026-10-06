package scheduler

// Per-run tool containment for fired scheduled chat runs
// (model-harness-toolset-01MHTS001 WP02, finding H-1).
//
// Owner ruling B-3 (docs/escalation-register-2026-08-19.md:1314): a
// model-created schedule may run unattended "ONLY WITHIN A TOOL
// ALLOWLIST ... Containment is now the only boundary". Before this file
// the allowlist was persisted (migration sessions/0340) and reduced to a
// has-an-allowlist boolean for the Cedar execute gate — the fired run
// itself could call any tool. ResolveRunContainment is the single place
// that decides what boundary a run executes under; core/rpc's
// LiveChatRunDispatcher applies it to the run's session, and the merged
// tool-permission resolver enforces it per call.

// RunContainment is the tool boundary one fired chat run executes under.
type RunContainment struct {
	// CreatedBy is the effective provenance: ScheduledRunCreatedByModel
	// when EITHER the gate-time spec or the re-read row says model.
	CreatedBy string
	// Refuse, when non-empty, means the run must not start at all — the
	// B-3 F1/F2/F3 "DOES NOT RUN" outcome. It is the reason recorded on
	// the failed history row.
	Refuse string
	// Contained is true when only the tools in Allow may be called. A
	// Contained run with an empty Allow denies every tool call.
	Contained bool
	// Allow is the effective allowlist (published tool names, e.g.
	// "kenaz__web_fetch"). Meaningful only when Contained.
	Allow []string
}

// ResolveRunContainment combines the gate-time spec (nil for a caller
// that built none) with the row re-read at dispatch time. It never
// widens past either input:
//
//   - model-created (either side): an absent, empty or unresolvable
//     allowlist refuses the run; otherwise the run is contained to the
//     intersection of the spec's list (when the spec carries one) and
//     the row's list, and an empty intersection refuses the run.
//   - user-created with an unresolvable allowlist (a declared list that
//     no longer decodes): contained with nothing allowed — a corrupt
//     allowlist never reads as "unrestricted".
//   - user-created with a declared allowlist on either side: contained
//     to the intersection of the non-empty lists.
//   - user-created with no allowlist anywhere: not contained — exactly
//     the behaviour every pre-0340 row shipped with.
func ResolveRunContainment(spec *ChatRunSpec, rec ChatRunRecord) RunContainment {
	createdBy := rec.CreatedBy
	if createdBy == "" {
		createdBy = ScheduledRunCreatedByUser
	}
	var gated []string
	if spec != nil {
		if spec.CreatedBy == ScheduledRunCreatedByModel {
			createdBy = ScheduledRunCreatedByModel
		}
		gated = spec.ToolAllowlist
	}
	out := RunContainment{CreatedBy: createdBy}

	if createdBy == ScheduledRunCreatedByModel {
		switch {
		case rec.ToolAllowlistUnresolvable:
			out.Refuse = "model-created schedule's tool allowlist cannot be read; it does not run (owner ruling B-3)"
			return out
		case len(rec.ToolAllowlist) == 0:
			out.Refuse = "model-created schedule has no tool allowlist; it does not run (owner ruling B-3)"
			return out
		case spec != nil && len(gated) == 0:
			out.Refuse = "model-created schedule was dispatched without the allowlist its execute gate evaluated; it does not run (owner ruling B-3)"
			return out
		}
		allow := rec.ToolAllowlist
		if len(gated) > 0 {
			allow = intersectToolNames(gated, rec.ToolAllowlist)
		}
		if len(allow) == 0 {
			out.Refuse = "model-created schedule's tool allowlist changed between its execute gate and dispatch and no tool remains allowed; it does not run (owner ruling B-3)"
			return out
		}
		out.Contained = true
		out.Allow = allow
		return out
	}

	if rec.ToolAllowlistUnresolvable {
		out.Contained = true
		out.Allow = nil
		return out
	}
	switch {
	case len(gated) > 0 && len(rec.ToolAllowlist) > 0:
		out.Contained = true
		out.Allow = intersectToolNames(gated, rec.ToolAllowlist)
	case len(gated) > 0:
		out.Contained = true
		out.Allow = append([]string(nil), gated...)
	case len(rec.ToolAllowlist) > 0:
		out.Contained = true
		out.Allow = append([]string(nil), rec.ToolAllowlist...)
	}
	return out
}

// intersectToolNames returns the names present in both a and b, in a's
// order, without duplicates.
func intersectToolNames(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, n := range b {
		inB[n] = struct{}{}
	}
	seen := make(map[string]struct{}, len(a))
	var out []string
	for _, n := range a {
		if _, ok := inB[n]; !ok {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
