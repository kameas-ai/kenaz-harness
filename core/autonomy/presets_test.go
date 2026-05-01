package autonomy

import "testing"

// TestPresetTableExact pins every (Tier, Knob) cell against the §Tier preset
// table in plan.md. Any drift here is a spec break.
func TestPresetTableExact(t *testing.T) {
	type cell struct {
		tier Tier
		knob Knob
		want any
	}
	cases := []cell{
		// Strict
		{TierStrict, KnobMaxIterations, 5},
		{TierStrict, KnobAskOnAmbiguity, AskAlways},
		{TierStrict, KnobAutoApproveFamilies, NewFamilySet()},
		{TierStrict, KnobTokenCeilingPerTurn, 8_192},
		{TierStrict, KnobRecapStyle, RecapNone},
		{TierStrict, KnobContinueOnError, ErrorStop},
		{TierStrict, KnobDestructiveActionPosture, DestructiveConfirm},
		// Cautious
		{TierCautious, KnobMaxIterations, 15},
		{TierCautious, KnobAskOnAmbiguity, AskHard},
		{TierCautious, KnobAutoApproveFamilies, NewFamilySet(FamilyRead)},
		{TierCautious, KnobTokenCeilingPerTurn, 32_768},
		{TierCautious, KnobRecapStyle, RecapBrief},
		{TierCautious, KnobContinueOnError, ErrorStop},
		{TierCautious, KnobDestructiveActionPosture, DestructiveConfirm},
		// Default
		{TierDefault, KnobMaxIterations, 40},
		{TierDefault, KnobAskOnAmbiguity, AskMajor},
		{TierDefault, KnobAutoApproveFamilies, NewFamilySet(FamilyRead, FamilyWrite)},
		{TierDefault, KnobTokenCeilingPerTurn, 131_072},
		{TierDefault, KnobRecapStyle, RecapBrief},
		{TierDefault, KnobContinueOnError, ErrorRetryOnce},
		{TierDefault, KnobDestructiveActionPosture, DestructiveConfirm},
		// Bold
		{TierBold, KnobMaxIterations, 100},
		{TierBold, KnobAskOnAmbiguity, AskProceed},
		{TierBold, KnobAutoApproveFamilies, NewFamilySet(FamilyRead, FamilyWrite, FamilyShellSafe)},
		{TierBold, KnobTokenCeilingPerTurn, 524_288},
		{TierBold, KnobRecapStyle, RecapFull},
		{TierBold, KnobContinueOnError, ErrorAdapt},
		{TierBold, KnobDestructiveActionPosture, DestructiveCedarOnly},
		// Autonomous
		{TierAutonomous, KnobMaxIterations, 0},
		{TierAutonomous, KnobAskOnAmbiguity, AskNever},
		{TierAutonomous, KnobAutoApproveFamilies, NewFamilySet(FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork)},
		{TierAutonomous, KnobTokenCeilingPerTurn, 2_097_152},
		{TierAutonomous, KnobRecapStyle, RecapFull},
		{TierAutonomous, KnobContinueOnError, ErrorAdapt},
		{TierAutonomous, KnobDestructiveActionPosture, DestructiveCedarOnly},
	}
	for _, c := range cases {
		row := PresetForTier(c.tier)
		if row == nil {
			t.Fatalf("PresetForTier(%v) = nil", c.tier)
		}
		got := row[c.knob]
		if !knobValueEqual(got, c.want) {
			t.Errorf("preset[%v][%v] = %v, want %v", c.tier, c.knob, got, c.want)
		}
	}
}

func TestPresetForTierUnknown(t *testing.T) {
	if PresetForTier(Tier(99)) != nil {
		t.Error("PresetForTier(unknown) should return nil")
	}
}

// TestPresetForTierIsDefensiveCopy proves callers cannot mutate the preset
// table by writing back into the returned map or its FamilySet values.
func TestPresetForTierIsDefensiveCopy(t *testing.T) {
	got := PresetForTier(TierDefault)
	got[KnobMaxIterations] = 999
	fs := got[KnobAutoApproveFamilies].(FamilySet)
	fs.Add(FamilyNetwork)

	// Re-fetch and assert nothing leaked.
	fresh := PresetForTier(TierDefault)
	if fresh[KnobMaxIterations] != 40 {
		t.Errorf("preset table mutation leaked: MaxIterations = %v, want 40",
			fresh[KnobMaxIterations])
	}
	freshFS := fresh[KnobAutoApproveFamilies].(FamilySet)
	if freshFS.Has(FamilyNetwork) {
		t.Error("preset table mutation leaked: Default tier should not auto-approve network")
	}
}

// knobValueEqual compares two preset values, special-casing FamilySet whose
// underlying map type doesn't support `==`.
func knobValueEqual(a, b any) bool {
	if fa, ok := a.(FamilySet); ok {
		fb, ok := b.(FamilySet)
		if !ok {
			return false
		}
		return fa.Equal(fb)
	}
	return a == b
}
