package config

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/bundle"
)

type Document struct {
	Revision    int64               `json:"revision"`
	UpdatedAt   int64               `json:"updated_at"`
	BundleSet   []bundle.Dependency `json:"bundle_set"`
	Overrides   []bundle.Override   `json:"overrides,omitempty"`
	LLM         LLMConfig           `json:"llm"`
	MCP         map[string]bool     `json:"mcp"`
	UI          map[string]any      `json:"ui,omitempty"`
	// FeatureFlags holds runtime toggles. The harness reads each flag at
	// startup and on config reload. Unknown flags are silently ignored.
	FeatureFlags map[string]bool `json:"feature_flags,omitempty"`
}

// FlagHooksV2Enabled is the feature flag that enables v2 lifecycle hooks
// (hooks-event-surface-expansion-01KZNP3A). Default: true.
// Set to false to revert to v1 chat-pipeline hooks only.
const FlagHooksV2Enabled = "hooks_v2_enabled"

// IsHooksV2Enabled returns the value of hooks_v2_enabled, defaulting to true
// when the flag is absent (or the Document is nil).
func IsHooksV2Enabled(doc *Document) bool {
	if doc == nil || doc.FeatureFlags == nil {
		return true // default on
	}
	val, ok := doc.FeatureFlags[FlagHooksV2Enabled]
	if !ok {
		return true // default on
	}
	return val
}

type LLMConfig struct {
	DefaultModel  string         `json:"default_model"`
	DefaultParams map[string]any `json:"default_params,omitempty"`
	CredentialIDs map[string]string `json:"credential_ids,omitempty"`
}

type Store interface {
	Current(ctx context.Context) (*Document, error)
	Put(ctx context.Context, d *Document) (*Document, error)
	History(ctx context.Context, limit int) ([]Document, error)
	GetRevision(ctx context.Context, rev int64) (*Document, error)
}
