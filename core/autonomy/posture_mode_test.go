package autonomy

import "testing"

// TestResolvePlanModeShortCircuits verifies that session.PostureMode="plan_mode"
// dominates all Level/Override values on every layer.
func TestResolvePlanModeShortCircuits(t *testing.T) {
	// Build layers with every conceivable override to prove PostureMode wins.
	planMode := PostureModePlanMode
	global := Layer{
		Level: ptrTier(TierAutonomous),
		Overrides: map[Knob]any{
			KnobMaxIterations: 9999,
		},
	}
	project := Layer{Level: ptrTier(TierBold)}
	session := Layer{
		Level: ptrTier(TierStrict),
		Overrides: map[Knob]any{
			KnobMaxIterations: 1234,
		},
		PostureMode: &planMode,
	}

	r := Resolve(global, project, session)
	if r.PostureMode != PostureModePlanMode {
		t.Errorf("PostureMode = %q, want %q", r.PostureMode, PostureModePlanMode)
	}

	preset := PresetForPostureMode(PostureModePlanMode)

	for _, k := range allKnobs {
		if r.SourceTrace[k] != SourcePostureMode {
			t.Errorf("SourceTrace[%v] = %v, want %v", k, r.SourceTrace[k], SourcePostureMode)
		}
	}

	// Plan mode preset values: 50 iterations, AskAlways, empty AutoApprove.
	if r.MaxIterations != preset[KnobMaxIterations].(int) {
		t.Errorf("MaxIterations = %d, want plan_mode preset %d",
			r.MaxIterations, preset[KnobMaxIterations].(int))
	}
	if r.AskOnAmbiguity != AskAlways {
		t.Errorf("AskOnAmbiguity = %v, want %v", r.AskOnAmbiguity, AskAlways)
	}
	if r.AutoApproveFamilies.Has(FamilyWrite) {
		t.Error("plan_mode must not auto-approve write family")
	}
	if r.ContinueOnError != ErrorStop {
		t.Errorf("ContinueOnError = %v, want %v", r.ContinueOnError, ErrorStop)
	}
}

// TestResolvePlanModeProjectLayer verifies project.PostureMode is honoured
// when session doesn't override it.
func TestResolvePlanModeProjectLayer(t *testing.T) {
	planMode := PostureModePlanMode
	project := Layer{
		Level:       ptrTier(TierBold),
		PostureMode: &planMode,
	}
	r := Resolve(Layer{}, project, Layer{})
	if r.PostureMode != PostureModePlanMode {
		t.Errorf("PostureMode = %q, want %q", r.PostureMode, PostureModePlanMode)
	}
}

// TestResolvePlanModeSessionBeatsProject verifies that when session AND
// project both carry PostureMode, session wins.
func TestResolvePlanModeSessionBeatsProject(t *testing.T) {
	planMode := PostureModePlanMode
	otherMode := "other_mode" // unknown; falls through to normal resolution
	project := Layer{PostureMode: &planMode}
	session := Layer{PostureMode: &otherMode}

	// "other_mode" is not in PresetForPostureMode → falls through.
	r := Resolve(Layer{}, project, session)
	// Session's unknown mode causes fallthrough, so project's plan_mode is
	// NOT applied — session wins the precedence race even if unknown.
	if r.PostureMode == PostureModePlanMode {
		t.Error("session PostureMode=unknown should take precedence over project plan_mode")
	}
}

// TestResolvePlanModeDoesNotAffectNormalLayers verifies that a nil
// PostureMode on every layer produces normal resolution (no PostureMode field
// set on output).
func TestResolvePlanModeDoesNotAffectNormalLayers(t *testing.T) {
	r := Resolve(layerLevel(TierBold), layerLevel(TierStrict), Layer{})
	if r.PostureMode != "" {
		t.Errorf("PostureMode = %q, want empty (normal resolution)", r.PostureMode)
	}
}

// TestPresetForPostureMode_PlanMode validates every cell of the plan_mode preset.
func TestPresetForPostureMode_PlanMode(t *testing.T) {
	preset := PresetForPostureMode(PostureModePlanMode)
	if preset == nil {
		t.Fatal("PresetForPostureMode(plan_mode) = nil")
	}
	cases := []struct {
		knob Knob
		want any
	}{
		{KnobMaxIterations, 50},
		{KnobAskOnAmbiguity, AskAlways},
		{KnobAutoApproveFamilies, NewFamilySet()},
		{KnobTokenCeilingPerTurn, 131_072},
		{KnobRecapStyle, RecapFull},
		{KnobContinueOnError, ErrorStop},
		{KnobDestructiveActionPosture, DestructiveConfirm},
	}
	for _, c := range cases {
		got := preset[c.knob]
		if !knobValueEqual(got, c.want) {
			t.Errorf("planModePreset[%v] = %v, want %v", c.knob, got, c.want)
		}
	}
}

// TestPresetForPostureMode_Unknown verifies nil is returned for unknown postures.
func TestPresetForPostureMode_Unknown(t *testing.T) {
	if got := PresetForPostureMode("unknown_mode"); got != nil {
		t.Errorf("PresetForPostureMode(unknown) = %v, want nil", got)
	}
}

// TestPresetForPostureMode_IsDefensiveCopy verifies callers cannot mutate the
// underlying preset table via the returned map.
func TestPresetForPostureMode_IsDefensiveCopy(t *testing.T) {
	p := PresetForPostureMode(PostureModePlanMode)
	p[KnobMaxIterations] = 9999
	fs := p[KnobAutoApproveFamilies].(FamilySet)
	fs.Add(FamilyWrite)

	fresh := PresetForPostureMode(PostureModePlanMode)
	if fresh[KnobMaxIterations].(int) != 50 {
		t.Error("mutation leaked: plan_mode MaxIterations should remain 50")
	}
	freshFS := fresh[KnobAutoApproveFamilies].(FamilySet)
	if freshFS.Has(FamilyWrite) {
		t.Error("mutation leaked: plan_mode AutoApprove should not include write")
	}
}
