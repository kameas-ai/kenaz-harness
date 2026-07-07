package contextbootstrap

import (
	"context"
	"encoding/json"
)

// ─── Fleet integration seams (DEFERRED) ──────────────────────────────────────
//
// All interfaces below are fleet-integration points. The harness implements
// the engine against these interfaces; the real fleet implementations land
// when fleet WP01–WP06 merge. Until then, LocalRecipeSource and NoopSyncer
// satisfy the seams.

// RecipeSource provides the bootstrap recipe. In production it is backed
// by the fleet config-bundle/catalog pipe (fleet WP01). Until fleet lands,
// LocalRecipeSource (backed by embedded fixture files) satisfies this seam.
//
// DEFERRED: wire to the fleet recipe push when fleet WP06 merges.
type RecipeSource interface {
	// LoadRecipe returns the current bootstrap recipe. May be called on each
	// run to pick up recipe updates without a harness restart.
	LoadRecipe(ctx context.Context) (*BootstrapRecipe, error)
}

// ContextWriter persists extracted nodes to the local context store and
// optionally syncs them to fleet. The harness side of this is a local
// context-graph write; the fleet sync is deferred.
//
// DEFERRED: sync side is wired when fleet WP03 ("context storage integration")
// and WP02 ("run lifecycle + status API") merge.
type ContextWriter interface {
	// WriteNodes persists extracted nodes to the local context store with
	// provenance, source_kind, confidence, and personal classification (FR-008).
	// Nodes that already exist are deduped (confidence raised on repeat).
	WriteNodes(ctx context.Context, nodes []ExtractedNode) error

	// Sync sends the run summary and extracted nodes to fleet. This is a
	// best-effort operation; failures are logged but do not abort the run.
	// DEFERRED: fleet endpoint defined in fleet WP02/WP03.
	// The implementation MUST NOT include any third-party credentials in the
	// payload — only extracted nodes + connection status.
	Sync(ctx context.Context, payload SyncPayload) error
}

// ProgressSink receives live run-status updates. The RPC layer wires this
// to a Wails EventsEmit broadcast so the frontend can show live progress.
// A nil ProgressSink is safe; progress is silently dropped.
type ProgressSink interface {
	// Emit sends a progress snapshot to the frontend. Non-blocking; the
	// implementation MUST NOT block the caller.
	Emit(ctx context.Context, status RunStatus)
}

// MCPPool is the harness's MCP connection pool. The engine uses this to
// check liveness, list tools, and call read-only tools for extraction.
// Satisfied by core/mcp.Pool in production.
type MCPPool interface {
	// Tools returns the tools currently available from all running servers.
	Tools(ctx context.Context) ([]MCPTool, error)
	// Call invokes one tool on one server with the given arguments.
	// The engine MUST only call tools named in ConnectorDef.ReadOnlyTools (WP09).
	Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error)
	// IsRunning reports whether the MCP server for the given recipe ID is
	// currently live (started + passed init).
	IsRunning(ctx context.Context, recipeID string) bool
}

// MCPTool mirrors core/mcp.Tool but is declared locally to avoid a
// cross-package import loop. The engine uses only Name and Server.
type MCPTool struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ModelCaller invokes the user's configured LLM for extraction prompts.
// The engine uses this to run recipe extraction prompts over quarantined
// source content. Satisfied by a thin wrapper over core/llm in production.
//
// Privacy invariant: the engine NEVER passes raw source content to
// ModelCaller without quarantine() first.
type ModelCaller interface {
	// Complete sends prompt to the model and returns the text completion.
	// The call is budget-limited by the engine's token ceilings.
	Complete(ctx context.Context, prompt string) (string, error)
}

// ConsentChecker verifies per-source consent before extraction begins (FR-013).
// DEFERRED: consent records are managed by fleet WP04. Until that lands,
// LocalConsentChecker returns true for all sources (consent assumed in the
// harness-only flow).
//
// DEFERRED: wire to fleet consent store when fleet WP04 merges.
type ConsentChecker interface {
	// HasConsent returns true when the user has given consent to read the
	// given connector's source data. Blocks extraction when false.
	HasConsent(ctx context.Context, connectorID string) (bool, error)
}

// ─── Noop / local implementations for deferred fleet seams ──────────────────

// NoopSyncer is the ContextWriter.Sync implementation used until fleet WP03
// lands. It logs a structured message but does NOT send anything to fleet.
// DEFERRED: replaced by the fleet HTTP client when fleet WP03 merges.
type noopSyncer struct{}

func (n *noopSyncer) Sync(_ context.Context, _ SyncPayload) error {
	// DEFERRED — fleet WP03 not yet merged. Sync is a no-op.
	// When fleet WP03 lands, replace this with a POST to
	// /api/v1/context/bootstrap/{run_id}/sync (fleet WP02 endpoint).
	return nil
}

// alwaysConsentChecker is the ConsentChecker used until fleet WP04 lands.
// DEFERRED: replaced by the fleet consent-record lookup when fleet WP04 merges.
type alwaysConsentChecker struct{}

func (a *alwaysConsentChecker) HasConsent(_ context.Context, _ string) (bool, error) {
	// DEFERRED — fleet WP04 not yet merged. Assume consent in harness-only flow.
	// When fleet WP04 lands, replace this with a check against the consent store.
	return true, nil
}
