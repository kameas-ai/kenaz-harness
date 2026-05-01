package autonomy

import (
	"testing"
)

// helpers ----------------------------------------------------------------

func ptrTier(t Tier) *Tier { return &t }

func layerLevel(t Tier) Layer {
	return Layer{Level: ptrTier(t), Overrides: map[Knob]any{}}
}

// expectKnob asserts a single knob value + source on a ResolvedKnobs.
func expectKnob(t *testing.T, r ResolvedKnobs, k Knob, wantVal any, wantSrc Source) {
	t.Helper()
	if got, want := r.SourceTrace[k], wantSrc; got != want {
		t.Errorf("knob %s: SourceTrace = %q, want %q", k, got, want)
	}
	var got any
	switch k {
	case KnobMaxIterations:
		got = r.MaxIterations
	case KnobAskOnAmbiguity:
		got = r.AskOnAmbiguity
	case KnobAutoApproveFamilies:
		got = r.AutoApproveFamilies
	case KnobTokenCeilingPerTurn:
		got = r.TokenCeilingPerTurn
	case KnobRecapStyle:
		got = r.RecapStyle
	case KnobContinueOnError:
		got = r.ContinueOnError
	case KnobDestructiveActionPosture:
		got = r.DestructiveActionPosture
	}
	if !knobValueEqual(got, wantVal) {
		t.Errorf("knob %s: value = %v, want %v", k, got, wantVal)
	}
}

// tests ------------------------------------------------------------------

// TestResolveAllDefault: empty layers everywhere → tier-default for all knobs.
func TestResolveAllDefault(t *testing.T) {
	r := Resolve(Layer{}, Layer{}, Layer{})
	preset := PresetForTier(TierDefault)
	for _, k := range allKnobs {
		expectKnob(t, r, k, preset[k], SourceTierDefault)
	}
}

// TestResolveSingleLayerGlobalLevel: only global has a Level set.
func TestResolveSingleLayerGlobalLevel(t *testing.T) {
	r := Resolve(layerLevel(TierStrict), Layer{}, Layer{})
	preset := PresetForTier(TierStrict)
	for _, k := range allKnobs {
		expectKnob(t, r, k, preset[k], SourceGlobal)
	}
}

// TestResolveSingleLayerGlobalOverride: global has only an override on one knob;
// every other knob falls through to tier-default.
func TestResolveSingleLayerGlobalOverride(t *testing.T) {
	g := Layer{Overrides: map[Knob]any{KnobMaxIterations: 999}}
	r := Resolve(g, Layer{}, Layer{})
	expectKnob(t, r, KnobMaxIterations, 999, SourceGlobal)

	defPreset := PresetForTier(TierDefault)
	for _, k := range allKnobs {
		if k == KnobMaxIterations {
			continue
		}
		expectKnob(t, r, k, defPreset[k], SourceTierDefault)
	}
}

func TestResolveSingleLayerProjectOnly(t *testing.T) {
	r := Resolve(Layer{}, layerLevel(TierBold), Layer{})
	preset := PresetForTier(TierBold)
	for _, k := range allKnobs {
		expectKnob(t, r, k, preset[k], SourceProject)
	}
}

func TestResolveSingleLayerSessionOnly(t *testing.T) {
	r := Resolve(Layer{}, Layer{}, layerLevel(TierAutonomous))
	preset := PresetForTier(TierAutonomous)
	for _, k := range allKnobs {
		expectKnob(t, r, k, preset[k], SourceSession)
	}
}

// TestResolveOverrideBeatsLevelInLayer: a session-level override wins over
// session-level Level for that knob, but other knobs come from session.Level.
func TestResolveOverrideBeatsLevelInLayer(t *testing.T) {
	session := Layer{
		Level: ptrTier(TierStrict),
		Overrides: map[Knob]any{
			KnobMaxIterations: 12345,
		},
	}
	r := Resolve(Layer{}, Layer{}, session)
	expectKnob(t, r, KnobMaxIterations, 12345, SourceSession)
	expectKnob(t, r, KnobAskOnAmbiguity, AskAlways, SourceSession) // strict preset
}

// TestResolveSessionBeatsProjectBeatsGlobal: every layer sets the same knob in
// override form; session must win, then project, then global.
func TestResolveSessionBeatsProjectBeatsGlobal(t *testing.T) {
	g := Layer{Overrides: map[Knob]any{KnobMaxIterations: 1}}
	p := Layer{Overrides: map[Knob]any{KnobMaxIterations: 2}}
	s := Layer{Overrides: map[Knob]any{KnobMaxIterations: 3}}
	if got := Resolve(g, p, s).MaxIterations; got != 3 {
		t.Errorf("session override should win: got %d, want 3", got)
	}
	if got := Resolve(g, p, Layer{}).MaxIterations; got != 2 {
		t.Errorf("project override should win when session empty: got %d, want 2", got)
	}
	if got := Resolve(g, Layer{}, Layer{}).MaxIterations; got != 1 {
		t.Errorf("global override should win when project+session empty: got %d, want 1", got)
	}
}

// TestResolvePlanFixturePerKnobIndependence: the §Resolution semantics §Key
// invariant — project Level=Bold and session Level=Strict do NOT clear the
// global override on TokenCeilingPerTurn.
//
// 3-layer fixture from plan.md §Resolution semantics:
//
//	global   = Default + override TokenCeilingPerTurn = 1_000_000
//	project  = Bold (no overrides)
//	session  = Strict (no overrides)
//
// Expected: MaxIterations = Strict's preset (from session.Level),
// AskOnAmbiguity = Strict's, TokenCeiling = 1_000_000 from global Override
// (no downstream layer touched it). SourceTrace records session for the
// Level-supplied knobs and global for TokenCeiling.
//
// This pins the §Key invariant: Overrides win over Levels regardless of
// which layer holds them. Resolver does a two-pass walk — Overrides first
// across session→project→global, then Levels across session→project→global,
// then tier-default. See resolve.go for the spec-ambiguity note.
func TestResolvePlanFixturePerKnobIndependence(t *testing.T) {
	global := Layer{
		Level: ptrTier(TierDefault),
		Overrides: map[Knob]any{
			KnobTokenCeilingPerTurn: 1_000_000,
		},
	}
	project := layerLevel(TierBold)
	session := layerLevel(TierStrict)

	r := Resolve(global, project, session)

	strict := PresetForTier(TierStrict)
	// All knobs except TokenCeiling come from session.Level=Strict.
	expectKnob(t, r, KnobMaxIterations, strict[KnobMaxIterations], SourceSession)
	expectKnob(t, r, KnobAskOnAmbiguity, strict[KnobAskOnAmbiguity], SourceSession)
	expectKnob(t, r, KnobAutoApproveFamilies, strict[KnobAutoApproveFamilies], SourceSession)
	expectKnob(t, r, KnobRecapStyle, strict[KnobRecapStyle], SourceSession)
	expectKnob(t, r, KnobContinueOnError, strict[KnobContinueOnError], SourceSession)
	expectKnob(t, r, KnobDestructiveActionPosture, strict[KnobDestructiveActionPosture], SourceSession)

	// TokenCeiling: global override shines through — neither project nor
	// session has an override for this knob, so the §Key invariant says the
	// global override wins over the downstream Levels.
	expectKnob(t, r, KnobTokenCeilingPerTurn, 1_000_000, SourceGlobal)
}

// TestResolveGlobalOverrideShinesThroughEmptyDownstream: when downstream
// layers have nil Level AND no override for a knob, a global override
// shines through.
func TestResolveGlobalOverrideShinesThrough(t *testing.T) {
	global := Layer{
		Level: ptrTier(TierDefault),
		Overrides: map[Knob]any{
			KnobTokenCeilingPerTurn: 1_000_000,
		},
	}
	project := Layer{} // empty
	session := Layer{} // empty
	r := Resolve(global, project, session)
	expectKnob(t, r, KnobTokenCeilingPerTurn, 1_000_000, SourceGlobal)
}

// TestResolveSourceTraceCoverage: the SourceTrace map must contain an entry
// for every knob (no missing keys).
func TestResolveSourceTraceCoverage(t *testing.T) {
	r := Resolve(Layer{}, Layer{}, Layer{})
	if len(r.SourceTrace) != len(allKnobs) {
		t.Errorf("SourceTrace len = %d, want %d", len(r.SourceTrace), len(allKnobs))
	}
	for _, k := range allKnobs {
		if _, ok := r.SourceTrace[k]; !ok {
			t.Errorf("SourceTrace missing knob %s", k)
		}
	}
}

// TestResolveDefensiveCopy: mutating the FamilySet returned by Resolve must
// not affect the preset table.
func TestResolveDefensiveCopy(t *testing.T) {
	r := Resolve(Layer{}, Layer{}, Layer{})
	r.AutoApproveFamilies.Add(FamilyNetwork)

	fresh := PresetForTier(TierDefault)
	freshFS := fresh[KnobAutoApproveFamilies].(FamilySet)
	if freshFS.Has(FamilyNetwork) {
		t.Error("Resolve returned a live reference into the preset table")
	}
}

// TestResolveOverrideValueIsCopied: a FamilySet supplied as an override must
// be cloned before being stamped onto ResolvedKnobs (so mutating it through
// the resolved value doesn't leak back into the layer).
func TestResolveOverrideFamilySetCopied(t *testing.T) {
	src := NewFamilySet(FamilyRead)
	session := Layer{Overrides: map[Knob]any{KnobAutoApproveFamilies: src}}
	r := Resolve(Layer{}, Layer{}, session)
	r.AutoApproveFamilies.Add(FamilyNetwork)
	if src.Has(FamilyNetwork) {
		t.Error("mutation on Resolve output leaked into the source FamilySet")
	}
}
