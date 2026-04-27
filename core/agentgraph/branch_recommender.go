package agentgraph

import (
	"strings"
)

// ModelTier is the size bucket the recommender works in. v1 is
// intentionally a 3-tier table; spec FR-036 lists it as dial-tunable.
type ModelTier string

const (
	// ModelTierSmall — cheap / fast / contained-task tier (Haiku, GPT-4o-mini, etc.)
	ModelTierSmall ModelTier = "small"
	// ModelTierMedium — default tier (Sonnet, GPT-4o, etc.)
	ModelTierMedium ModelTier = "medium"
	// ModelTierLarge — heavy reasoning tier (Opus, GPT-4-turbo, o1, etc.)
	ModelTierLarge ModelTier = "large"
)

// ModelInfo is the minimal projection of a configured provider+model
// the recommender consumes. The rpc layer translates from its richer
// llm.Provider shape into a slice of these so the recommender stays
// import-cycle-clean (charter DIRECTIVE_001).
type ModelInfo struct {
	ProviderID string
	ProviderKind string // "anthropic" | "openai" | "bedrock" | "openrouter" | "personal"
	ModelID    string
	Tier       ModelTier
}

// RecommendationReason is a short user-facing explanation. Stable
// strings so the frontend can highlight specific reasons.
type RecommendationReason string

const (
	ReasonContainedTask RecommendationReason = "contained_task_smaller_model"
	ReasonDeepDesign    RecommendationReason = "deep_design_larger_model"
	ReasonSkillTag      RecommendationReason = "skill_tag_match"
	ReasonUserOverride  RecommendationReason = "user_override"
	ReasonDefault       RecommendationReason = "default"
)

// ForkPreference is the user's stated preference at fork time. Maps
// onto the dropdown the frontend renders ("smaller", "larger", "exact").
type ForkPreference string

const (
	ForkPrefSmaller ForkPreference = "smaller"
	ForkPrefLarger  ForkPreference = "larger"
	ForkPrefSame    ForkPreference = "same"
	ForkPrefExact   ForkPreference = "exact"
)

// Recommendation is the recommender's verdict. Fields:
//   - Provider/Model: which (providerID, modelID) the kernel should
//     spawn the child run on.
//   - Tier: the size bucket the model maps into.
//   - Reason: a stable short string the frontend can localize.
//   - Notes: a longer human-friendly explanation.
type Recommendation struct {
	ProviderID string
	ModelID    string
	Tier       ModelTier
	Reason     RecommendationReason
	Notes      string
}

// BranchRecommender picks a (provider, model) pair for a forked child
// session. v1 is a small heuristic table — spec §4.6 / FR-036 explicitly
// notes that this is dial-tunable and OK to start crude.
type BranchRecommender struct {
	models []ModelInfo
}

// NewBranchRecommender constructs a recommender from the available
// model list. The slice is copied so subsequent caller mutations don't
// surprise the recommender.
func NewBranchRecommender(models []ModelInfo) *BranchRecommender {
	cp := make([]ModelInfo, len(models))
	copy(cp, models)
	return &BranchRecommender{models: cp}
}

// Recommend takes the parent's (providerID, modelID) and a free-text
// task hint and returns a Recommendation. The heuristic:
//
//  1. If the hint contains keywords mapped to "small" (e.g. lookup,
//     check version, latest, what's, format), recommend smaller tier.
//  2. If the hint contains keywords mapped to "large" (e.g. design,
//     architect, plan, deep dive, refactor strategy), recommend larger.
//  3. Otherwise pin the parent's tier.
//
// Falls back to the parent's exact (provider, model) when no candidate
// is configured at the recommended tier.
func (r *BranchRecommender) Recommend(parentProviderID, parentModelID, taskHint string, pref ForkPreference) Recommendation {
	parentTier := r.tierOf(parentProviderID, parentModelID)
	target := parentTier
	reason := RecommendationReason(ReasonDefault)
	notes := "Same tier as parent"

	lower := strings.ToLower(taskHint)
	switch pref {
	case ForkPrefSmaller:
		target = stepDown(parentTier)
		reason = ReasonUserOverride
		notes = "User asked for a smaller model"
	case ForkPrefLarger:
		target = stepUp(parentTier)
		reason = ReasonUserOverride
		notes = "User asked for a larger model"
	case ForkPrefSame, ForkPrefExact:
		target = parentTier
	default:
		// Inspect the task hint.
		if matchesAny(lower, smallKeywords) {
			target = stepDown(parentTier)
			reason = ReasonContainedTask
			notes = "Task looks contained — recommending a smaller, faster model"
		} else if matchesAny(lower, largeKeywords) {
			target = stepUp(parentTier)
			reason = ReasonDeepDesign
			notes = "Task looks like a deep-design problem — recommending a larger model"
		}
	}

	rec := r.pickAtTier(target, parentProviderID)
	if rec.ModelID == "" {
		// Fallback: parent's exact pair.
		return Recommendation{
			ProviderID: parentProviderID,
			ModelID:    parentModelID,
			Tier:       parentTier,
			Reason:     ReasonDefault,
			Notes:      "No model configured at the recommended tier; falling back to parent",
		}
	}
	rec.Reason = reason
	rec.Notes = notes
	return rec
}

// tierOf finds the tier of a (provider, model) pair, defaulting to medium.
func (r *BranchRecommender) tierOf(providerID, modelID string) ModelTier {
	for _, m := range r.models {
		if m.ProviderID == providerID && m.ModelID == modelID {
			return m.Tier
		}
	}
	// Heuristic on the model id itself.
	return tierFromModelID(modelID)
}

// pickAtTier returns the first configured model at the given tier,
// preferring the parent's provider when one is configured at the tier.
func (r *BranchRecommender) pickAtTier(tier ModelTier, preferProvider string) Recommendation {
	var best Recommendation
	for _, m := range r.models {
		if m.Tier != tier {
			continue
		}
		if best.ModelID == "" {
			best = Recommendation{ProviderID: m.ProviderID, ModelID: m.ModelID, Tier: m.Tier}
		}
		if m.ProviderID == preferProvider {
			return Recommendation{ProviderID: m.ProviderID, ModelID: m.ModelID, Tier: m.Tier}
		}
	}
	return best
}

func stepDown(t ModelTier) ModelTier {
	switch t {
	case ModelTierLarge:
		return ModelTierMedium
	case ModelTierMedium:
		return ModelTierSmall
	default:
		return ModelTierSmall
	}
}

func stepUp(t ModelTier) ModelTier {
	switch t {
	case ModelTierSmall:
		return ModelTierMedium
	case ModelTierMedium:
		return ModelTierLarge
	default:
		return ModelTierLarge
	}
}

// tierFromModelID falls back to a name-based guess when the recommender
// has no metadata for the parent model. Matches stable family names so
// it doesn't flip every time a provider ships a new revision.
func tierFromModelID(id string) ModelTier {
	lower := strings.ToLower(id)
	switch {
	case strings.Contains(lower, "haiku"),
		strings.Contains(lower, "mini"),
		strings.Contains(lower, "nano"),
		strings.Contains(lower, "small"),
		strings.Contains(lower, "instant"):
		return ModelTierSmall
	case strings.Contains(lower, "opus"),
		strings.Contains(lower, "ultra"),
		strings.Contains(lower, "o1"),
		strings.Contains(lower, "large"):
		return ModelTierLarge
	default:
		return ModelTierMedium
	}
}

// smallKeywords/largeKeywords are the v1 keyword tables. They live in
// the package so the rpc layer can pull them too if it wants to render
// the rationale chip in the frontend.
var (
	smallKeywords = []string{
		"lookup", "look up", "what's the", "what is the", "version of",
		"latest version", "format", "fix typo", "rename", "syntax",
		"convert", "summarize",
	}
	largeKeywords = []string{
		"design", "architect", "plan", "strategy", "deep dive",
		"refactor", "redesign", "trade-off", "tradeoff", "blueprint",
		"brainstorm", "rfc",
	}
)

func matchesAny(s string, kws []string) bool {
	for _, k := range kws {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
