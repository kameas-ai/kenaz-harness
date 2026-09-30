package advice

import (
	"strings"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ModelRung names which rung of ResolveAdvisorModel's ladder (spec §2:
// "the laya ladder") produced a (profileID, model) pair. Every call logs
// its rung with distinct slog fields, including an `unbenchmarked` bool —
// the rater review's follow-up (risk.RaterModelRung's doc comment)
// applied here from the start rather than bolted on later.
type ModelRung string

const (
	// RungExplicitSetting: Settings.AdvisorModel is set and that
	// (provider, model) pair is available on the matching profile.
	RungExplicitSetting ModelRung = "explicit_setting"
	// RungLocalLaya: no usable explicit setting; a configured local
	// provider profile (kind "ollama" today — OQ-1 may widen "or
	// compatible") serves a model whose id matches the laya family per
	// isLayaFamilyModel's placeholder table.
	RungLocalLaya ModelRung = "local_laya"
	// RungFleetLaya: fleet-hosted laya inference. Compiled but
	// UNREACHABLE in this release — see fleetRungEnabled. Owner: fleet
	// team (OQ-2). Blocks on the advisor_inference capability key +
	// endpoint brokerage; tasks.md WP09 wires this for real.
	RungFleetLaya ModelRung = "fleet_laya"
	// RungNone: no explicit setting, no local laya model, and the fleet
	// rung is disabled (or also unavailable). Advisors are disabled for
	// this resolution; the UI shows why (spec §2 rung 4).
	//
	// Deliberately NOT followed by an "any available model" rung the
	// way risk.RungAnyAvailable is: the pre-01PMRA01 risk-rater defect
	// this mission's owner explicitly cited (silently repurposing the
	// user's big CHAT model, commonly a large reasoning model, for a
	// bounded judgment call — task #109's benchmark measured that class
	// of model at a 7.6s median latency and 38.5% reliability inside a
	// 5s budget) is exactly what an advisor ladder with a generic
	// fallback rung would reintroduce, cheaper and more often (advice
	// calls fire far more frequently than risk-rated dispatches).
	// model_resolve_test.go's profiles[0]-regression case pins this.
	RungNone ModelRung = "none"
)

// fleetRungEnabled gates rung 3 (fleet-hosted laya). OQ-2 (owner: fleet
// team, spec §8): the `advisor_inference` capability key and endpoint
// brokerage contract do not exist yet. Do NOT flip this true until OQ-2
// resolves and tasks.md WP09 lands the real fleet-brokered call —
// inventing a fleet contract here would ship an untested, unreviewed
// egress path for conversation-derived features (spec §5's privacy
// boundary). Dated 2026-09-27 (laya-advisors-01LAYA001 WP02).
const fleetRungEnabled = false

// layaModelMatchTable is a PLACEHOLDER family-detection list for rung 2
// (local laya). OQ-1 (owner: kenaz-ml team, spec §8) blocks the REAL laya
// model naming/size(s) and local runtime target (ollama tag? gguf?) —
// this table exists so rung 2 has something concrete to test against
// (model_resolve_test.go's TestIsLayaFamilyModel_PlaceholderTable pins
// it) and so replacing it with the real name(s) once OQ-1 lands is a
// visible, single-slice diff in this file rather than a silent behaviour
// change discovered in production. Dated 2026-09-27.
var layaModelMatchTable = []string{
	"laya", // model-lit-allow: placeholder family-detection substring, not a shipped model id
}

// isLayaFamilyModel reports whether model's id matches the (placeholder)
// laya family, case-insensitively substring-matched against
// layaModelMatchTable.
func isLayaFamilyModel(model string) bool {
	lower := strings.ToLower(model)
	for _, pat := range layaModelMatchTable {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// AdvisorModelSetting mirrors risk.RaterModelSetting / the settings
// package's ProviderProfileRef, without this package importing the
// settings view package (same reasoning risk.RaterModelSetting's doc
// comment gives — core/rpc translates between the two at the wiring
// site).
type AdvisorModelSetting struct {
	ProviderID string
	ModelID    string
}

// IsZero reports whether no explicit advisor-model setting is configured.
func (s AdvisorModelSetting) IsZero() bool {
	return s.ProviderID == "" && s.ModelID == ""
}

// ResolveAdvisorModel implements the laya ladder (spec §2):
//
//  1. RungExplicitSetting: setting is non-zero AND its (ProviderID,
//     ModelID) pair is available on the matching configured profile.
//  2. RungLocalLaya: the first configured "ollama"-kind profile serving
//     an available model matching isLayaFamilyModel.
//  3. RungFleetLaya: compiled, unreachable while fleetRungEnabled is
//     false (OQ-2).
//  4. RungNone: none of the above resolved. ok=false; there is no model
//     to call for advice. Unlike risk.ResolveRaterModel there is no
//     "any available model" last resort — see RungNone's doc comment.
//
// unbenchmarked is false only for RungExplicitSetting (the operator
// picked deliberately); every other resolved rung is true, since no
// benchmark of record exists yet for any laya model (OQ-3). Every rung
// (including the RungNone miss) logs at construction-relevant
// granularity with the rung and unbenchmarked as distinct fields,
// mirroring risk.ResolveRaterModel's "log which rung decided" contract.
func ResolveAdvisorModel(setting AdvisorModelSetting, profiles []corellm.ProviderProfile) (profileID, model string, rung ModelRung, unbenchmarked bool, ok bool) {
	if len(profiles) == 0 {
		logging.L().Warn("advice.model_resolve",
			"rung", string(RungNone),
			"unbenchmarked", true,
			"reason", "no configured profiles")
		return "", "", RungNone, true, false
	}

	// Rung 1: explicit setting, if set and available on its profile.
	if !setting.IsZero() {
		for _, p := range profiles {
			if p.ID != setting.ProviderID {
				continue
			}
			if modelAvailable(p, setting.ModelID) {
				logging.L().Debug("advice.model_resolve",
					"rung", string(RungExplicitSetting),
					"profile_id", p.ID,
					"model", setting.ModelID,
					"unbenchmarked", false)
				return p.ID, setting.ModelID, RungExplicitSetting, false, true
			}
			logging.L().Warn("advice.model_resolve",
				"reason", "explicit advisor-model setting is unavailable on its profile, falling through",
				"profile_id", p.ID,
				"model", setting.ModelID)
			break
		}
	}

	// Rung 2: local laya — first "ollama"-kind profile with an available
	// model matching the (placeholder) family table.
	for _, p := range profiles {
		if p.Kind != "ollama" {
			continue
		}
		for _, m := range p.AvailableModels() {
			if isLayaFamilyModel(m) {
				logging.L().Info("advice.model_resolve",
					"rung", string(RungLocalLaya),
					"profile_id", p.ID,
					"model", m,
					"unbenchmarked", true)
				return p.ID, m, RungLocalLaya, true, true
			}
		}
	}

	// Rung 3: fleet-hosted laya. Compiled, unreachable pending OQ-2.
	if fleetRungEnabled {
		// Unreachable in this release (fleetRungEnabled is a compile-time
		// false above) — kept as a documented seam for WP09's follow-up,
		// which replaces this whole block with the real fleet-brokered
		// resolution once OQ-2's capability key + endpoint contract land.
		logging.L().Info("advice.model_resolve", "rung", string(RungFleetLaya), "unbenchmarked", true)
	}

	logging.L().Warn("advice.model_resolve",
		"rung", string(RungNone),
		"unbenchmarked", true,
		"reason", "no explicit setting, no local laya model available, fleet rung disabled")
	return "", "", RungNone, true, false
}

func modelAvailable(p corellm.ProviderProfile, model string) bool {
	if model == "" {
		return false
	}
	for _, m := range p.AvailableModels() {
		if m == model {
			return true
		}
	}
	return false
}
