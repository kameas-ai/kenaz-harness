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
