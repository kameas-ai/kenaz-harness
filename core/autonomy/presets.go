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

// BudgetCeiling is the tier-scaled ceiling for the two call-volume
// agentgraph.Budget fields (MaxLLMCallsPerRun, MaxToolCallsPerRun) that
// KnobTokenCeilingPerTurn does not already govern.
//
// Filed live (owner directive 2026-09-09) after ErrBudgetExceeded fired
// mid-session against chat_default_classic.yaml's flat budget: block
// (max_llm_calls_per_run: 5000, max_tool_calls_per_run: 10000) --
// constants that do not vary by autonomy tier at all today. The owner's
// ruling: "we should be using our built in autonomy dial."
//
// This is deliberately NOT a fully independently-tunable Knob (no
// per-layer Overrides slot, no Settings surface): it is a direct
// function of the tier ladder, the same shape KnobMaxIterations already
// uses (5/15/40/100/unbounded). See chat.applyBudgetTierDial for the
// consumer -- it applies the ceiling the same way
// chat.applyTokenCeilingKnob applies KnobTokenCeilingPerTurn: the dial
// may only LOWER the graph's declared budget, never raise it, and
// TierAutonomous's ceiling equals the graph's own declared cap rather
// than "unbounded" -- the owner asked for the dial to govern this cap,
// not for the cap to stop existing. It exists to stop runaway spend.
type BudgetCeiling struct {
	MaxLLMCallsPerRun  int
	MaxToolCallsPerRun int
}

// budgetCeilingTable mirrors presetTable's five-tier shape. TierAutonomous
// intentionally matches chat_default_classic.yaml's declared budget: block
// (5000/10000) -- the most permissive tier gets the graph author's full
// declared ceiling, not a value beyond it.
var budgetCeilingTable = map[Tier]BudgetCeiling{
	TierStrict:     {MaxLLMCallsPerRun: 150, MaxToolCallsPerRun: 300},
	TierCautious:   {MaxLLMCallsPerRun: 500, MaxToolCallsPerRun: 1000},
	TierDefault:    {MaxLLMCallsPerRun: 1500, MaxToolCallsPerRun: 3000},
	TierBold:       {MaxLLMCallsPerRun: 3000, MaxToolCallsPerRun: 6000},
	TierAutonomous: {MaxLLMCallsPerRun: 5000, MaxToolCallsPerRun: 10000},
}

// BudgetCeilingForTier returns the tier's call-volume budget ceiling.
// Unknown tiers fall back to TierDefault's, mirroring presetValue's
// fallback for the seven-knob preset table.
func BudgetCeilingForTier(t Tier) BudgetCeiling {
	if c, ok := budgetCeilingTable[t]; ok {
		return c
	}
	return budgetCeilingTable[TierDefault]
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
