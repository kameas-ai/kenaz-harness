package advice

import (
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
	// RungLocalLaya: no usable explicit setting; the managed local ML
	// sidecar (kenaz-ml, laya-advisors-01LAYA001 WP12) is installed and
	// healthy per a SidecarProbe.
	//
	// WP12 AMENDMENT (tasks.md's DESIGN-LOCKED REVISION, 2026-09-29):
	// this rung used to mean "a configured 'ollama'-kind provider
	// profile serves a model matching a placeholder laya-family name
	// table" — a stand-in built for the since-VOIDED LLM bootstrap path
	// (spec §2e-0). That scan (isLayaFamilyModel / layaModelMatchTable)
	// is deleted: the kenaz-ml integration design (research/kenaz-ml-
	// integration-design.md §9 Phase 0) makes the managed sidecar the
	// real rung-2 answer, and there is no rung "2b" (user-provided local
	// laya runtime) in scope yet — see model_resolve_test.go's rung-2
	// cases, now built against a fake SidecarProbe instead of ollama
	// profile fixtures.
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

// SidecarProbe is the rung-2 seam the WP12 amendment wires in place of
// the deleted placeholder ollama-profile scan. Implemented in production
// by *core/mlsidecar.Manager (see that package's probe.go); tests inject
// a fake so this package never gains a hard import on the lifecycle
// manager (core/advice predates core/mlsidecar and is the lighter-weight
// package — the dependency direction stays advice -> (interface) rather
// than advice -> mlsidecar).
//
// Healthy/Identity are both cheap, non-blocking reads of ALREADY-CACHED
// state. Neither may perform a network call or trigger a spawn: this
// ladder is resolved at process boot (core/rpc/api.go's unconditional
// "advice.laya_ladder.boot_resolve" call site) as well as per advice
// call, and design item 5 requires "lazy start on first advisor demand,
// not app boot" — a probe that spawned on every boot-time resolve would
// violate that the instant the ladder ran once.
type SidecarProbe interface {
	// Healthy reports whether the managed ML sidecar is currently
	// installed and healthy, independent of any specific advice kind —
	// v1 has no graduated laya checkpoint for any kind (spec §2e Phase
	// B), so kind-level gating ("a healthy sidecar with no graduated
	// checkpoint for the kind falls through to the LLM backend",
	// tasks.md WP12) is Phase-3 work. Rung 2 in this release is "the
	// sidecar exists and answers health checks", full stop.
	Healthy() bool
	// Identity returns the sidecar's self-reported version identity,
	// used as the resolved "model" id for RungLocalLaya (e.g.
	// "kenaz-ml-sidecar@1.2.3"). Called only when Healthy() is true.
	Identity() string
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
//  2. RungLocalLaya: sidecar != nil && sidecar.Healthy() — the managed
//     ML sidecar (WP12 amendment; see SidecarProbe's doc comment for why
//     this replaced a placeholder ollama-profile scan). Independent of
//     `profiles` entirely: the sidecar is not an LLM provider profile.
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
//
// A nil sidecar is a valid input (e.g. a boot path before core/rpc/api.go
// wires a *mlsidecar.Manager) — it is treated exactly like an unhealthy
// one: rung 2 is simply unavailable, never a panic.
func ResolveAdvisorModel(setting AdvisorModelSetting, profiles []corellm.ProviderProfile, sidecar SidecarProbe) (profileID, model string, rung ModelRung, unbenchmarked bool, ok bool) {
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

	// Rung 2: the managed ML sidecar (WP12 amendment). No profile
	// backs this rung — the sidecar is reached over its own HTTP
	// contract, not through corellm.ProviderProfile — so profileID is
	// deliberately empty and Model carries the sidecar's own identity
	// string.
	if sidecar != nil && sidecar.Healthy() {
		id := sidecar.Identity()
		logging.L().Info("advice.model_resolve",
			"rung", string(RungLocalLaya),
			"model", id,
			"unbenchmarked", true)
		return "", id, RungLocalLaya, true, true
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
		"reason", "no explicit setting, sidecar unavailable, fleet rung disabled")
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
