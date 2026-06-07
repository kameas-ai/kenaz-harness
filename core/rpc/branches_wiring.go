package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	coreconv "github.com/kameas-ai/kenaz-harness/core/conversation"
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

// newBranchRecommender returns the v1 recommender pre-loaded with a
// small canned model table. Production wiring (a follow-up patch)
// should hydrate this from the LLM connector view's ListProviders so
// the recommendation chip surfaces real models. Until then the v1
// table covers the most common cases (Anthropic + OpenAI tiers).
func newBranchRecommender() *agentgraph.BranchRecommender {
	return agentgraph.NewBranchRecommender([]agentgraph.ModelInfo{
		{ProviderID: "anthropic", ProviderKind: "anthropic", ModelID: "claude-haiku-4", Tier: agentgraph.ModelTierSmall},
		{ProviderID: "anthropic", ProviderKind: "anthropic", ModelID: "claude-sonnet-4", Tier: agentgraph.ModelTierMedium},
		{ProviderID: "anthropic", ProviderKind: "anthropic", ModelID: "claude-opus-4", Tier: agentgraph.ModelTierLarge},
		{ProviderID: "openai", ProviderKind: "openai", ModelID: "gpt-4o-mini", Tier: agentgraph.ModelTierSmall},
		{ProviderID: "openai", ProviderKind: "openai", ModelID: "gpt-4o", Tier: agentgraph.ModelTierMedium},
		{ProviderID: "openai", ProviderKind: "openai", ModelID: "o1-preview", Tier: agentgraph.ModelTierLarge},
	})
}
