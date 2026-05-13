package autonomy

// PostureModePlanMode is the sixth named posture — a session-scoped mode
// the model enters before non-trivial implementations. While active, every
// write-class Cedar action is denied. The posture is exited only via the
// __exit_plan_mode builtin which presents a plan for user approval.
//
// PostureMode is deliberately distinct from Tier: it short-circuits the
// entire knob-resolution pipeline (Overrides + Level) and locks the session
// to a fixed read-only preset.
const PostureModePlanMode = "plan_mode"

// planModePreset is the locked knob preset applied when
// Layer.PostureMode == PostureModePlanMode. The values are deliberately
// conservative: tight iteration budget, always-ask, no auto-approve of any
// family, halt on error, verbose recap so the model narrates its exploration
// clearly.
var planModePreset = map[Knob]any{
	KnobMaxIterations:            50,
	KnobAskOnAmbiguity:           AskAlways,
	KnobAutoApproveFamilies:      NewFamilySet(), // empty — no write family
	KnobTokenCeilingPerTurn:      131_072,        // same as Default
	KnobRecapStyle:               RecapFull,
	KnobContinueOnError:          ErrorStop,
	KnobDestructiveActionPosture: DestructiveConfirm,
}

// presetTable is the canonical mapping from Tier to the seven knob values, as
// specified in the §Tier preset table of plan.md. Values are typed as the
// concrete knob types (int, AskMode, FamilySet, RecapMode, ErrorMode,
// DestructivePosture) so resolution can return them directly.
//
// The map is package-private and accessed only through PresetForTier, which
// returns a defensive copy.
var presetTable = map[Tier]map[Knob]any{
	TierStrict: {
		KnobMaxIterations:            5,
		KnobAskOnAmbiguity:           AskAlways,
		KnobAutoApproveFamilies:      NewFamilySet(),
		KnobTokenCeilingPerTurn:      8_192,
		KnobRecapStyle:               RecapNone,
		KnobContinueOnError:          ErrorStop,
		KnobDestructiveActionPosture: DestructiveConfirm,
	},
	TierCautious: {
		KnobMaxIterations:            15,
		KnobAskOnAmbiguity:           AskHard,
		KnobAutoApproveFamilies:      NewFamilySet(FamilyRead),
		KnobTokenCeilingPerTurn:      32_768,
		KnobRecapStyle:               RecapBrief,
		KnobContinueOnError:          ErrorStop,
		KnobDestructiveActionPosture: DestructiveConfirm,
	},
	TierDefault: {
		KnobMaxIterations:            40,
		KnobAskOnAmbiguity:           AskMajor,
		KnobAutoApproveFamilies:      NewFamilySet(FamilyRead, FamilyWrite),
		KnobTokenCeilingPerTurn:      131_072,
		KnobRecapStyle:               RecapBrief,
		KnobContinueOnError:          ErrorRetryOnce,
		KnobDestructiveActionPosture: DestructiveConfirm,
	},
	TierBold: {
		KnobMaxIterations:            100,
		KnobAskOnAmbiguity:           AskProceed,
		KnobAutoApproveFamilies:      NewFamilySet(FamilyRead, FamilyWrite, FamilyShellSafe),
		KnobTokenCeilingPerTurn:      524_288,
		KnobRecapStyle:               RecapFull,
		KnobContinueOnError:          ErrorAdapt,
		KnobDestructiveActionPosture: DestructiveCedarOnly,
	},
	TierAutonomous: {
		KnobMaxIterations:            0, // 0 == unbounded
		KnobAskOnAmbiguity:           AskNever,
		KnobAutoApproveFamilies:      NewFamilySet(FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork),
		KnobTokenCeilingPerTurn:      2_097_152,
		KnobRecapStyle:               RecapFull,
		KnobContinueOnError:          ErrorAdapt,
		KnobDestructiveActionPosture: DestructiveCedarOnly,
	},
}

// PresetForTier returns a defensive copy of the preset map for a given tier.
// Mutations on the returned map (or on a FamilySet value within it) do not
// affect the package-level table. Returns nil for an unknown tier.
func PresetForTier(t Tier) map[Knob]any {
	row, ok := presetTable[t]
	if !ok {
		return nil
	}
	out := make(map[Knob]any, len(row))
	for k, v := range row {
		out[k] = clonePresetValue(v)
	}
	return out
}

// clonePresetValue returns a deep copy of a preset value for the value types
// stored in presetTable. Scalar types (int and the string-newtype enums) are
// value-copied automatically; FamilySet is a map and must be cloned.
func clonePresetValue(v any) any {
	if fs, ok := v.(FamilySet); ok {
		return fs.Clone()
	}
	return v
}

// PresetForPostureMode returns a defensive copy of the knob preset for a
// named posture mode. Currently only PostureModePlanMode ("plan_mode") is
// supported; other values return nil.
func PresetForPostureMode(mode string) map[Knob]any {
	var src map[Knob]any
	switch mode {
	case PostureModePlanMode:
		src = planModePreset
	default:
		return nil
	}
	out := make(map[Knob]any, len(src))
	for k, v := range src {
		out[k] = clonePresetValue(v)
	}
	return out
}
