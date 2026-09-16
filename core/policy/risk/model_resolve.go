package risk

import (
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// RaterModelRung names which rung of ResolveRaterModel's three-rung
// ladder (owner ruling 3, 2026-09-15, mission risk-rated-autonomy-
// 01PMRA01: "per-provider defaults + default to whatever is available")
// produced a (profileID, model) pair. Every call to ResolveRaterModel
// logs its rung with distinct slog fields — "log which rung resolved ...
// so 'running on fallback model X' is visible, not inferred" is the
// owner's exact requirement — rather than folding the outcome into one
// undifferentiated "resolved" line a reader would have to reverse-
// engineer.
type RaterModelRung string

const (
	// RungExplicitSetting: the user's Settings.RiskRaterModel is set and
	// that (provider, model) pair is available on the matching profile.
	RungExplicitSetting RaterModelRung = "explicit_setting"
	// RungProviderDefault: no usable explicit setting; a configured
	// profile's provider-kind has a benchmarked (or conservative,
	// unbenchmarked) default model, and that model is available on it.
	RungProviderDefault RaterModelRung = "provider_default"
	// RungAnyAvailable: neither of the above resolved to an available
	// model. Falls back to ANY available model on ANY configured
	// profile — the pre-01PMRA01 profiles[0].Model behaviour, kept as
	// the last resort so the rater always constructs.
	RungAnyAvailable RaterModelRung = "any_available"
	// RungNone: no configured profile has any available model at all.
	// The rater cannot construct; the caller's existing nil-rater /
	// no-route failure semantics apply (which map to Confirm — safe).
	RungNone RaterModelRung = "none"
)

// RaterModelSetting mirrors settings.ProviderProfileRef without this
// package importing the settings view package (core/rpc/views/settings
// already depends on things that would make the reverse import a cycle;
// core/rpc translates between the two at the wiring site, same pattern
// CompactionModel / compaction.ProviderProfileRef already established).
type RaterModelSetting struct {
	ProviderID string
	ModelID    string
}

// IsZero reports whether no explicit rating-model setting is configured.
func (s RaterModelSetting) IsZero() bool {
	return s.ProviderID == "" && s.ModelID == ""
}

// ProviderDefaultModel returns the conservative small-model default for a
// profile of the given (kind, templateID), per owner ruling 3
// (2026-09-15). unbenchmarked reports whether this id has no measured
// row in the vendored benchmark data (core/policy/risk/data/
// rater_benchmark.json) — it is a defensible small-model guess for a
// provider kind nobody measured, not a claim of measured quality.
//
// The benchmark of record (task #109, 234 calls / 9 models) is why
// openrouter's default is qwen/qwen-2.5-7b-instruct rather than
// deepseek/deepseek-v4-flash: the owner weighed the two, and the data
// showed identical quality (4/5 exact band agreement) at 2.2x qwen's
// speed (467.9ms vs 1020.1ms median) — qwen won on latency with no
// quality cost.
func ProviderDefaultModel(kind, templateID string) (model string, unbenchmarked bool) {
	switch kind {
	case "openrouter":
		return "qwen/qwen-2.5-7b-instruct", false
	case "openai":
		return "gpt-4o-mini", false // model-lit-allow: benchmarked default-model id, not classification logic (task #109)
	case "anthropic":
		return "claude-haiku-4.5", false // model-lit-allow: benchmarked default-model id, not classification logic (task #109)
	case "custom-openai":
		// deepseek is a custom-openai TEMPLATE (core/llm/custom/
		// templates.yaml), not its own provider Kind — every other
		// custom-openai template (vllm, groq, fireworks, anyscale,
		// mistral, llamacpp, litellm, together, ...) was never
		// benchmarked, so only the deepseek template gets a measured
		// default; everything else gets a conservative, explicitly
		// unbenchmarked guess.
		if templateID == "deepseek" {
			return "deepseek-chat", false
		}
		return "gpt-4o-mini", true // model-lit-allow: conservative default-model id, not classification logic
	case "bedrock":
		return "anthropic.claude-3-5-haiku-20241022-v1:0", true // model-lit-allow: conservative default-model id, not classification logic
	case "gemini": // model-lit-allow: provider Kind constant, not a model-family classification
		return "gemini-1.5-flash", true // model-lit-allow: conservative default-model id, not classification logic
	case "ollama":
		return "qwen2.5:7b-instruct", true
	default:
		// No conservative id known for this kind at all — rung b simply
		// does not fire for it; rung c (any available) decides.
		return "", true
	}
}

// ResolveRaterModel implements the three-rung resolution ladder (owner
// ruling 3, 2026-09-15):
//
//   - Rung a (RungExplicitSetting): setting is non-zero AND its
//     (ProviderID, ModelID) pair is available (present in
//     AvailableModels()) on the matching configured profile.
//   - Rung b (RungProviderDefault): the first configured profile whose
//     provider-kind default (ProviderDefaultModel) is available on it.
//   - Rung c (RungAnyAvailable): the first configured profile with ANY
//     available model at all — the pre-01PMRA01 profiles[0].Model
//     fallback, kept as the last resort so the rater always constructs.
//
// "Available" reads AvailableModels(), which itself falls back to
// [Model] when Models is unset (llm.ProviderProfile.AvailableModels's own
// documented behaviour) — so a provider with no enumerable model list
// still treats its configured Model as available, and a default that
// happens to be wrong for that profile is left to fail into the rater's
// own failure semantics (every Rate() error maps to Confirm — safe),
// exactly as this ladder's contract promises.
//
// Returns ok=false only when profiles is empty or not one of them has
// any available model at all (RungNone) — there is truly no model to
// call. Every resolution (and the RungNone miss) is logged at
// construction-relevant granularity with the rung as a distinct field,
// per the owner's "log which rung resolved" requirement.
func ResolveRaterModel(setting RaterModelSetting, profiles []corellm.ProviderProfile) (profileID, model string, rung RaterModelRung, ok bool) {
	if len(profiles) == 0 {
		logging.L().Warn("risk.rater.model_resolve",
			"rung", string(RungNone),
			"reason", "no configured profiles")
		return "", "", RungNone, false
	}

	// Rung a: explicit setting, if set and available on its profile.
	if !setting.IsZero() {
		for _, p := range profiles {
			if p.ID != setting.ProviderID {
				continue
			}
			if modelAvailable(p, setting.ModelID) {
				logging.L().Debug("risk.rater.model_resolve",
					"rung", string(RungExplicitSetting),
					"profile_id", p.ID,
					"model", setting.ModelID)
				return p.ID, setting.ModelID, RungExplicitSetting, true
			}
			logging.L().Warn("risk.rater.model_resolve",
				"reason", "explicit rating-model setting is unavailable on its profile, falling through",
				"profile_id", p.ID,
				"model", setting.ModelID)
			break
		}
	}

	// Rung b: per-provider default, if available.
	for _, p := range profiles {
		def, _ := ProviderDefaultModel(p.Kind, p.TemplateID)
		if def == "" {
			continue
		}
		if modelAvailable(p, def) {
			logging.L().Info("risk.rater.model_resolve",
				"rung", string(RungProviderDefault),
				"profile_id", p.ID,
				"model", def,
				"provider_kind", p.Kind)
			return p.ID, def, RungProviderDefault, true
		}
	}

	// Rung c: any available model on any configured profile — the
	// pre-01PMRA01 fallback, kept as the LAST resort.
	for _, p := range profiles {
		models := p.AvailableModels()
		if len(models) == 0 {
			continue
		}
		logging.L().Warn("risk.rater.model_resolve",
			"rung", string(RungAnyAvailable),
			"profile_id", p.ID,
			"model", models[0],
			"reason", "no explicit setting or provider default available on any configured profile")
		return p.ID, models[0], RungAnyAvailable, true
	}

	logging.L().Warn("risk.rater.model_resolve",
		"rung", string(RungNone),
		"reason", "no configured profile has any available model")
	return "", "", RungNone, false
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
