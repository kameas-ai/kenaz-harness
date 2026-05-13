package autonomy

// Source identifies where a resolved knob value originated.
type Source string

const (
	SourceSession     Source = "session"
	SourceProject     Source = "project"
	SourceGlobal      Source = "global"
	SourceTierDefault Source = "tier-default"
	// SourcePostureMode indicates the knob value was locked by a named
	// posture mode (e.g. "plan_mode") that short-circuits normal resolution.
	SourcePostureMode Source = "posture-mode"
)

// ResolvedKnobs is the per-turn output of Resolve: every knob has a concrete
// value plus a SourceTrace stamping which layer (or the tier-default fallback)
// the value came from.
//
// When PostureMode is non-empty it records the active named posture mode
// (e.g. PostureModePlanMode). The toolloop and Cedar wiring consult this
// field to apply posture-level enforcement orthogonal to the knob values.
type ResolvedKnobs struct {
	MaxIterations            int
	AskOnAmbiguity           AskMode
	AutoApproveFamilies      FamilySet
	TokenCeilingPerTurn      int
	RecapStyle               RecapMode
	ContinueOnError          ErrorMode
	DestructiveActionPosture DestructivePosture
	SourceTrace              map[Knob]Source
	// PostureMode is the active named posture mode, or "" when none.
	PostureMode string
}

// allKnobs is the canonical iteration order for resolution. Stable so the
// SourceTrace map and any future deterministic logging is reproducible.
var allKnobs = []Knob{
	KnobMaxIterations,
	KnobAskOnAmbiguity,
	KnobAutoApproveFamilies,
	KnobTokenCeilingPerTurn,
	KnobRecapStyle,
	KnobContinueOnError,
	KnobDestructiveActionPosture,
}

// Resolve implements the §Resolution semantics §Key invariant from plan.md:
// Level and Overrides resolve INDEPENDENTLY per knob. For each knob, the
// priority is a two-pass walk:
//
//  1. session.Overrides[k]   → session
//  2. project.Overrides[k]   → project
//  3. global.Overrides[k]    → global
//  4. session.Level preset   → session
//  5. project.Level preset   → project
//  6. global.Level preset    → global
//  7. tier-default (TierDefault preset)
//
// This is the only interpretation that satisfies plan.md's worked example:
// `global=Default+TokenCeiling=1M, project=Bold (no overrides), session=
// Strict (no overrides)` resolves TokenCeiling to 1M (global override) while
// MaxIterations comes from session.Level=Strict. A project-level Tier=Bold
// does NOT clear the global override for any knob; it only kicks in for knobs
// no upstream layer has overridden.
//
// Mission directive's literal "walk session.Overrides → session.Level →
// project.Overrides → ..." slipped — the explicit narrative assertions in
// plan.md and the mission's own expected test assertions only work under the
// two-pass interpretation. Resolved in favor of the §Key invariant + the
// expected assertion (TokenCeiling=1M from global).
//
// PostureMode short-circuit: when any layer's PostureMode is non-nil,
// the named posture's locked preset is returned and all knob-level
// resolution is skipped. Session PostureMode wins over project/global.
func Resolve(global, project, session Layer) ResolvedKnobs {
	// Check PostureMode in session → project → global order. The first
	// non-nil PostureMode wins and short-circuits knob resolution.
	activePosture := ""
	if session.PostureMode != nil {
		activePosture = *session.PostureMode
	} else if project.PostureMode != nil {
		activePosture = *project.PostureMode
	} else if global.PostureMode != nil {
		activePosture = *global.PostureMode
	}

	if activePosture != "" {
		preset := PresetForPostureMode(activePosture)
		if preset != nil {
			out := ResolvedKnobs{
				SourceTrace: make(map[Knob]Source, len(allKnobs)),
				PostureMode: activePosture,
			}
			for _, k := range allKnobs {
				assignKnob(&out, k, clonePresetValue(preset[k]))
				out.SourceTrace[k] = SourcePostureMode
			}
			return out
		}
		// Unknown posture mode — fall through to normal resolution so
		// an unrecognized value never silently locks the session.
	}

	out := ResolvedKnobs{
		SourceTrace: make(map[Knob]Source, len(allKnobs)),
	}
	for _, k := range allKnobs {
		val, src := resolveKnob(k, global, project, session)
		assignKnob(&out, k, val)
		out.SourceTrace[k] = src
	}
	return out
}

// resolveKnob walks the seven priority slots for a single knob: Overrides
// first across all layers (session→project→global), then Levels across all
// layers (session→project→global), then tier-default.
func resolveKnob(k Knob, global, project, session Layer) (any, Source) {
	// Pass 1: explicit overrides, downstream-first.
	if v, ok := session.Overrides[k]; ok {
		return cloneKnobValue(v), SourceSession
	}
	if v, ok := project.Overrides[k]; ok {
		return cloneKnobValue(v), SourceProject
	}
	if v, ok := global.Overrides[k]; ok {
		return cloneKnobValue(v), SourceGlobal
	}
	// Pass 2: tier-level presets, downstream-first.
	if session.Level != nil {
		return presetValue(*session.Level, k), SourceSession
	}
	if project.Level != nil {
		return presetValue(*project.Level, k), SourceProject
	}
	if global.Level != nil {
		return presetValue(*global.Level, k), SourceGlobal
	}
	// Pass 3: tier-default fallback.
	return presetValue(TierDefault, k), SourceTierDefault
}

// presetValue looks up a single (tier, knob) cell from the preset table,
// returning a defensive copy of FamilySet values.
func presetValue(t Tier, k Knob) any {
	row, ok := presetTable[t]
	if !ok {
		row = presetTable[TierDefault]
	}
	return cloneKnobValue(row[k])
}

// cloneKnobValue returns a value-equal copy of v. Only FamilySet (a map type)
// needs deep copying; the rest of the knob value-types are scalars or
// string-newtypes and copy by value.
func cloneKnobValue(v any) any {
	if fs, ok := v.(FamilySet); ok {
		return fs.Clone()
	}
	return v
}

// assignKnob writes a typed value into the matching field on ResolvedKnobs.
// It is intentionally strict: a malformed override (wrong concrete type) will
// leave the field zero-valued. Validation upstream of Resolve is expected to
// catch type mismatches before they get this far; the JSON unmarshaller in
// layer.go already coerces to the correct types.
func assignKnob(r *ResolvedKnobs, k Knob, v any) {
	switch k {
	case KnobMaxIterations:
		if n, ok := v.(int); ok {
			r.MaxIterations = n
		}
	case KnobAskOnAmbiguity:
		if m, ok := v.(AskMode); ok {
			r.AskOnAmbiguity = m
		}
	case KnobAutoApproveFamilies:
		if fs, ok := v.(FamilySet); ok {
			r.AutoApproveFamilies = fs
		}
	case KnobTokenCeilingPerTurn:
		if n, ok := v.(int); ok {
			r.TokenCeilingPerTurn = n
		}
	case KnobRecapStyle:
		if m, ok := v.(RecapMode); ok {
			r.RecapStyle = m
		}
	case KnobContinueOnError:
		if m, ok := v.(ErrorMode); ok {
			r.ContinueOnError = m
		}
	case KnobDestructiveActionPosture:
		if m, ok := v.(DestructivePosture); ok {
			r.DestructiveActionPosture = m
		}
	}
}
