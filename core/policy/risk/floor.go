package risk

import "github.com/kameas-ai/kenaz-harness/core/toolloop"

// familyFloorScore is the minimum score WP06 enforces for
// toolloop.FamilyDestructive and toolloop.FamilyUnknown, regardless of
// what a RiskRater returns. It is chosen strictly ABOVE the autonomy
// tier ladder's most permissive threshold (core/autonomy's
// KnobRiskThreshold top tier is 80 — see core/autonomy/presets.go) so
// that even at the most permissive autonomy tier, a destructive or
// unclassifiable-family call can never resolve to Allow via
// score < threshold: 81 < 80 is false at every tier, so the call always
// falls through to the confirm prompt. 81 stays inside
// BandCritical (80-100) — "destructive, exfiltrating, or irreversible" —
// which is the correct band for both families by construction: a
// destructive call already reads as critical, and an unclassifiable
// tool (FamilyUnknown) is exactly the case where the harness has no
// positive signal the call is SAFE, so treating "unknown" as
// "assume worst case" is the conservative direction (matching
// toolloop.ClassifyToolFamily's own doc comment: "A tool the classifier
// cannot place is FamilyUnknown and never skipped").
const familyFloorScore = 81

// FamilyFloors maps toolloop's canonical family names (by VALUE, not by
// import of a shared constant table — toolloop.FamilyDestructive and
// toolloop.FamilyUnknown are plain string constants "destructive" /
// "unknown") to the minimum score ApplyFamilyFloor enforces for that
// family. Families with no entry are unfloored: the rater's raw score
// stands.
var FamilyFloors = map[string]int{
	toolloop.FamilyDestructive: familyFloorScore,
	toolloop.FamilyUnknown:     familyFloorScore,
}

// ApplyFamilyFloor raises score to at least family's configured floor
// (FamilyFloors) and returns score unchanged otherwise. It NEVER lowers
// score — a rater that (legitimately, via its own judgment) rates a
// destructive call at 95 keeps 95; only a score BELOW the floor is
// raised. This is WP06's whole security property: a successful
// prompt-injection attack that gets the rating model to emit a
// low score for a destructive or unclassifiable call still cannot
// escape the floor, because the floor is enforced here, in Go, after
// the model's output has already been parsed and validated — the model
// has no way to reach or influence this function's inputs beyond the
// Score integer itself.
//
// Mutation-test property (WP06's proof requirement): deleting this
// function's floor-raising behaviour (e.g. by degenerating it to
// `return score`) must make the injection-fixture test in
// llmrater_injection_test.go fail — a destructive call rated 0 by an
// injected prompt would then resolve to Allow below any positive
// threshold, exactly the defect this function exists to prevent.
func ApplyFamilyFloor(family string, score int) int {
	if floor, ok := FamilyFloors[family]; ok && score < floor {
		return floor
	}
	return score
}
