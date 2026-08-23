// sync_mcp.go — installed-MCP sync category.
//
// Implements the collector (reads MCP registry → redacts secrets) and
// applier (writes registry + queues secret-prompt) for SyncCategoryInstalledMCP.
//
// HARD RULE: credstore bytes (MCP server secrets, API keys) NEVER appear in
// the synced payload. The Redactor enforces this by stripping all EnvKey
// values from EnvOverrides. On the receiving end, the Applier detects MCPs
// with RequiresSecretKeys and enqueues a "needs credentials" notification.
package fleet

import (
	"context"
	"encoding/json"
	"sync"
)

// InstalledMCP is one entry in the installed-MCP sync payload.
// Only non-secret fields are included; credstore bytes are never serialized.
type InstalledMCP struct {
	// ID is the install-unique identifier (e.g. a UUID assigned at install time).
	ID string `json:"id"`
	// RecipeID is the recipe this MCP was installed from.
	RecipeID string `json:"recipe_id"`
	// RecipeVersion is the recipe version installed. Empty if not tracked.
	RecipeVersion string `json:"recipe_version,omitempty"`
	// EnabledState reflects whether the MCP is enabled on the source device.
	EnabledState bool `json:"enabled"`
	// EnvOverrides are non-secret per-install env var overrides (e.g. a
	// custom URL override). Any key whose name matches a secret key in the
	// source recipe's EnvKeys is stripped by the Redactor before sync.
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
	// RequiresSecretKeys lists EnvKey names that require secret values on the
	// receiving device. The receiving device must prompt the user to provide
	// these before the MCP can start.
	RequiresSecretKeys []string `json:"requires_secret_keys,omitempty"`
}

// InstalledMCPPayload is the top-level shape synced for SyncCategoryInstalledMCP.
type InstalledMCPPayload struct {
	Items []InstalledMCP `json:"items"`
}

// MCPRegistryReader is implemented by callers that can enumerate the locally
// installed MCP set. The harness wires a concrete reader from the stdio pool +
// recipe store in the RPC boot sequence.
type MCPRegistryReader interface {
	// ListInstalled returns the current installed MCP set. The returned
	// InstalledMCP values should already have RequiresSecretKeys populated by
	// the caller (based on the source recipe's EnvKeys).
	ListInstalled() ([]InstalledMCP, error)
}

// MCPRegistryWriter is implemented by callers that can apply an incoming
// installed-MCP payload. Implementations must be idempotent (dedupe by
// RecipeID + ID).
type MCPRegistryWriter interface {
	// ApplyInstalled merges the incoming installed set into local state.
	// For each item in incoming with non-empty RequiresSecretKeys that are not
	// already in the local credstore, the applier must call
	// EnqueueSecretPrompt.
	ApplyInstalled(incoming []InstalledMCP) error
}

// SecretPromptQueue holds MCPs that arrived via sync but need credentials
// before they can start. Thread-safe.
type SecretPromptQueue struct {
	mu    sync.Mutex
	items []InstalledMCP
}

// Enqueue adds an MCP to the pending-credentials queue.
func (q *SecretPromptQueue) Enqueue(mcp InstalledMCP) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// Dedupe by ID.
	for _, existing := range q.items {
		if existing.ID == mcp.ID {
			return
		}
	}
	q.items = append(q.items, mcp)
}

// Snapshot returns a copy of the pending queue.
func (q *SecretPromptQueue) Snapshot() []InstalledMCP {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]InstalledMCP, len(q.items))
	copy(out, q.items)
	return out
}

// Remove removes the MCP with the given ID from the queue (called after
// the user has provided the required credentials).
func (q *SecretPromptQueue) Remove(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.items[:0]
	for _, it := range q.items {
		if it.ID != id {
			out = append(out, it)
		}
	}
	q.items = out
}

// MCPSyncCategory implements CategoryCollector and CategoryApplier for
// SyncCategoryInstalledMCP. It is registered with a Syncer via
// RegisterCategory.
type MCPSyncCategory struct {
	Reader     MCPRegistryReader
	Writer     MCPRegistryWriter
	SecretKeys map[string]bool   // set of env key names flagged as secrets in loaded recipes
	Pending    *SecretPromptQueue // shared with Sync RPC view for banner
}

// localOnlyConfigKeys names config keys that resolveConfig
// (core/rpc/views/tools/impl.go) injects into a recipe's persisted
// EnabledRecipe.Config for LOCAL template substitution
// (${DATA_DIR} in args_template) — never for cross-device sync.
// "data_dir" is not a recipe-declared EnvKey, so it survives the
// SecretKeys redaction below untouched; it carries the user's
// absolute profile path (and therefore OS username) and MUST be
// stripped here regardless of whether the caller's Reader ever
// copies EnabledRecipe.Config verbatim into InstalledMCP.EnvOverrides.
// See docs/unwired-ledger.md, H2, 2026-08-23.
var localOnlyConfigKeys = map[string]bool{
	"data_dir": true,
}

// Collect implements CategoryCollector. Reads local MCP registry, strips
// secret env override values, and returns the redacted JSON payload.
//
// HARD RULE: EnvOverrides values for any key listed in SecretKeys, or
// in localOnlyConfigKeys, are silently dropped (never included in the
// returned payload).
func (m *MCPSyncCategory) Collect(ctx context.Context) (json.RawMessage, error) {
	if m.Reader == nil {
		// No reader wired; return empty payload rather than blocking sync.
		p := InstalledMCPPayload{Items: []InstalledMCP{}}
		return json.Marshal(p)
	}
	items, err := m.Reader.ListInstalled()
	if err != nil {
		return nil, err
	}
	// Redact: strip secret env override values and local-only
	// infrastructure keys (e.g. "data_dir") that must never leave
	// the device.
	redacted := make([]InstalledMCP, len(items))
	for i, it := range items {
		r := it
		if len(it.EnvOverrides) > 0 {
			clean := make(map[string]string, len(it.EnvOverrides))
			for k, v := range it.EnvOverrides {
				if localOnlyConfigKeys[k] {
					continue
				}
				if len(m.SecretKeys) > 0 && m.SecretKeys[k] {
					continue
				}
				clean[k] = v
				// Secret / local-only keys are dropped — audit block is
				// best-effort here; a full audit emit would require
				// threading the audit emitter through MCPSyncCategory;
				// deferred to follow-up.
			}
			r.EnvOverrides = clean
		}
		redacted[i] = r
	}
	p := InstalledMCPPayload{Items: redacted}
	return json.Marshal(p)
}

// Apply implements CategoryApplier. Receives the remote installed-MCP payload
// and writes it to local state. MCPs with RequiresSecretKeys are queued in
// Pending (visible via Sync_PendingMCPSecrets RPC).
func (m *MCPSyncCategory) Apply(ctx context.Context, raw json.RawMessage) error {
	var p InstalledMCPPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	// Enqueue MCPs needing secrets before applying, so the UI can show the
	// banner even if apply fails.
	if m.Pending != nil {
		for _, it := range p.Items {
			if len(it.RequiresSecretKeys) > 0 {
				m.Pending.Enqueue(it)
			}
		}
	}
	if m.Writer != nil {
		return m.Writer.ApplyInstalled(p.Items)
	}
	return nil
}

// NewMCPSyncCategory constructs a wired MCPSyncCategory. reader and writer
// may be nil (best-effort: sync still runs, just with empty collect / no apply).
func NewMCPSyncCategory(reader MCPRegistryReader, writer MCPRegistryWriter, secretKeys map[string]bool, pending *SecretPromptQueue) *MCPSyncCategory {
	if pending == nil {
		pending = &SecretPromptQueue{}
	}
	return &MCPSyncCategory{
		Reader:     reader,
		Writer:     writer,
		SecretKeys: secretKeys,
		Pending:    pending,
	}
}

// CategoryConfigForMCP converts an MCPSyncCategory into a CategoryConfig
// suitable for Syncer.RegisterCategory.
func CategoryConfigForMCP(m *MCPSyncCategory) CategoryConfig {
	return CategoryConfig{
		Collector: m.Collect,
		Applier:   m.Apply,
	}
}
