package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	coreconv "github.com/kameas-ai/kenaz-harness/core/conversation"
	llmcap "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// newConversationManager constructs the conversation.Manager backing
// the Branches view. Returns nil when c (or its storage) is nil — the
// view surface falls back to ErrManagerUnavailable so the frontend
// renders an empty state instead of a confusing "not wired" error.
func newConversationManager(c *core.Core) *coreconv.Manager {
	if c == nil {
		return nil
	}
	s := c.Storage()
	if s == nil {
		return nil
	}
	store := coreconv.NewSQLStore(coreconv.NewStorageDB(s))
	return coreconv.NewManager(store, c.SessionManager())
}

// sessionManagerOrNil exposes c.SessionManager() guarded against a nil
// receiver. Mirrors the pattern in newSessionsAPI / newProjectsAPI.
func sessionManagerOrNil(c *core.Core) *session.Manager {
	if c == nil {
		return nil
	}
	return c.SessionManager()
}

// tierSourceAdapter wraps *llmcap.Catalog to satisfy agentgraph.TierSource
// (versioned-model-profile-01PMDL04 WP04): the recommender asks a data
// question through this seam instead of string-matching model family
// names itself. A nil cat (capabilities data failed to load) makes every
// lookup report "no opinion" so callers fall through to their own default.
type tierSourceAdapter struct {
	cat *llmcap.Catalog
}

func (a *tierSourceAdapter) Tier(providerKind, modelID string) (agentgraph.ModelTier, bool) {
	if a == nil || a.cat == nil {
		return "", false
	}
	t, ok := a.cat.Tier(providerKind, modelID)
	if !ok {
		return "", false
	}
	return agentgraph.ModelTier(t), true
}

// knownModelProviders lists the (providerID, providerKind) pairs the v1
// recommender enumerates concrete candidates for. providerID and
// providerKind coincide for the two directly-configured providers
// covered here; a real multi-connection setup would key providerID off
// the user's configured connection name instead — tracked below.
var knownModelProviders = []string{"anthropic", "openai"}

// newBranchRecommender returns the v1 recommender pre-loaded with a
// known-model table sourced from the LLM capabilities registry
// (versioned-model-profile-01PMDL04 WP04) instead of the hand-maintained
// literal list this replaces — that table duplicated model-family names
// already tracked in core/llm/capabilities/data/*.yaml (tiers:), which is
// exactly the frozen-core violation WP04 closes. cat may be nil (e.g. the
// embedded YAML failed to load); the recommender degrades to its
// medium-tier default in that case rather than panicking.
//
// Production wiring (a follow-up patch) should still hydrate provider
// IDs from the LLM connector view's ListProviders so the recommendation
// chip surfaces the user's actually-configured providers/models, not
// just the two enumerated here.
func newBranchRecommender(cat *llmcap.Catalog) *agentgraph.BranchRecommender {
	var models []agentgraph.ModelInfo
	if cat != nil {
		for _, providerID := range knownModelProviders {
			for _, km := range cat.KnownModels(providerID) {
				models = append(models, agentgraph.ModelInfo{
					ProviderID:   providerID,
					ProviderKind: providerID,
					ModelID:      km.ModelID,
					Tier:         agentgraph.ModelTier(km.Tier),
				})
			}
		}
	}
	return agentgraph.NewBranchRecommenderWithTierSource(models, &tierSourceAdapter{cat: cat})
}

// ContextWindow satisfies agentgraph.ContextWindowSource, letting the
// compaction pipeline evaluate its pre-call threshold against the real
// model context window instead of firing on every turn. Returns 0 when
// the catalog has no entry, which the pipeline reads as "unknown" and
// skips on.
func (a *tierSourceAdapter) ContextWindow(providerKind, modelID string) int {
	if a == nil || a.cat == nil {
		return 0
	}
	return a.cat.ContextWindow(providerKind, modelID)
}
