// Package fleet — context_bootstrap.go
//
// BootstrapClient is the fleet client for the context-bootstrap API
// (context-bootstrap-harness-integration WP01):
//
//	GET   /api/v1/context/bootstrap/recipe        — versioned recipe pull
//	POST  /api/v1/context/bootstrap               — start a run
//	GET   /api/v1/context/bootstrap/{run_id}      — read run status
//	PATCH /api/v1/context/bootstrap/{run_id}      — advance-only progress update
//	POST  /api/v1/context/bootstrap/{run_id}/resume — resume a paused run
//	GET   /api/v1/me/context/health               — context-graph health rollup
//
// Capability gating (CapContextBootstrap): every method requires the
// capability and a signed-in client. When the capability is absent (OSS tier,
// offline, signed-out) methods return ErrCapabilityNotInTier / ErrFleetDisabled
// so the RPC layer can fall back to the fully-local bootstrap path.
//
// Privacy invariant: NONE of these request/response shapes carry third-party
// credentials. The recipe carries prompts + connector catalog; run status
// carries counts + connector states; the push path (context/push, existing
// endpoint) carries already-quarantined nodes. No provider tokens are ever
// serialised here.
//
// OSS-first: this file lives in core/fleet/ (the fleet-touching layer). The
// contextbootstrap engine (core/contextbootstrap/) never imports core/fleet;
// the adapters that bridge the two live in core/rpc/contextbootstrap_wiring.go.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ── Wire shapes ───────────────────────────────────────────────────────────────
//
// These mirror the fleet contract documented in the harness integration spec.
// json.RawMessage is avoided here (the recipe + status are structured), but the
// shapes intentionally stay flat so they can be mapped into the engine's
// contextbootstrap types by the RPC adapter without leaking fleet types into
// the engine.

// BootstrapRecipeWire is the response of GET /api/v1/context/bootstrap/recipe.
type BootstrapRecipeWire struct {
	Version           string                       `json:"version"`
	InterviewSchema   json.RawMessage              `json:"interview_schema,omitempty"`
	ConnectorCatalog  []BootstrapConnectorWire     `json:"connector_catalog"`
	ExtractionPrompts BootstrapExtractionPrompts   `json:"extraction_prompts"`
	ContextTaxonomy   BootstrapTaxonomyWire        `json:"context_taxonomy"`
	ConfidenceRules   BootstrapConfidenceRulesWire `json:"confidence_rules"`
}

// BootstrapConnectorWire is one connector-catalog entry in the recipe.
type BootstrapConnectorWire struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Description      string   `json:"description,omitempty"`
	MCPRecipeID      string   `json:"mcp_recipe_id"`
	ReadOnlyTools    []string `json:"read_only_tools"`
	FetchStrategy    string   `json:"fetch_strategy,omitempty"`
	ExtractionPrompt string   `json:"extraction_prompt,omitempty"`
	MaxItems         int      `json:"max_items,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
}

// BootstrapExtractionPrompts carries the shared extraction prompt bank.
type BootstrapExtractionPrompts struct {
	PatternExtraction  string `json:"pattern_extraction,omitempty"`
	PeopleTrustMap     string `json:"people_trust_map,omitempty"`
	CorroborationPass  string `json:"corroboration_pass,omitempty"`
	ClarificationItems string `json:"clarification_items,omitempty"`
}

// BootstrapTaxonomyWire declares the node/edge kinds the extraction produces.
type BootstrapTaxonomyWire struct {
	NodeKinds []string `json:"node_kinds"`
	EdgeKinds []string `json:"edge_kinds"`
}

// BootstrapConfidenceRulesWire is the assert/tentative policy shipped by fleet.
type BootstrapConfidenceRulesWire struct {
	AssertThreshold     float64 `json:"assert_threshold"`
	TentativeThreshold  float64 `json:"tentative_threshold"`
	TrustedPersonWeight int     `json:"trusted_person_weight"`
	MinCorroborations   int     `json:"min_corroborations"`
}

// StartBootstrapRequest is the body for POST /api/v1/context/bootstrap.
type StartBootstrapRequest struct {
	ConsentedSources []string `json:"consented_sources"`
	RecipeVersion    string   `json:"recipe_version"`
}

// StartBootstrapResponse is the response of POST /api/v1/context/bootstrap.
// Returned with 201 for a new run, 200 when an active run already exists.
type StartBootstrapResponse struct {
	RunID         string `json:"run_id"`
	Status        string `json:"status"`
	RecipeVersion string `json:"recipe_version"`
}

// BootstrapConnectorStatus is one per-connector status line in a run.
type BootstrapConnectorStatus struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	ItemsProcessed int    `json:"items_processed"`
	NodesCreated   int    `json:"nodes_created"`
	Error          string `json:"error,omitempty"`
}

// BootstrapRunStatus is the response of GET /api/v1/context/bootstrap/{run_id}.
type BootstrapRunStatus struct {
	RunID            string                     `json:"run_id"`
	Status           string                     `json:"status"`
	Phase            string                     `json:"phase"`
	ConsentedSources []string                   `json:"consented_sources"`
	Connectors       []BootstrapConnectorStatus `json:"connectors"`
	ItemsProcessed   int                        `json:"items_processed"`
	NodesCreated     int                        `json:"nodes_created"`
	Errors           []string                   `json:"errors,omitempty"`
	RecipeVersion    string                     `json:"recipe_version"`
	StartedAt        string                     `json:"started_at,omitempty"`
	UpdatedAt        string                     `json:"updated_at,omitempty"`
	FinishedAt       string                     `json:"finished_at,omitempty"`
}

// BootstrapRunPatch is the (advance-only) body for
// PATCH /api/v1/context/bootstrap/{run_id}. All fields are omitempty so a
// partial update only carries the changed fields. PATCH on a completed/failed
// run returns 409 run_finalized.
type BootstrapRunPatch struct {
	Phase          string                     `json:"phase,omitempty"`
	Connectors     []BootstrapConnectorStatus `json:"connectors,omitempty"`
	ItemsProcessed int                        `json:"items_processed,omitempty"`
	NodesCreated   int                        `json:"nodes_created,omitempty"`
	Status         string                     `json:"status,omitempty"`
}

// BootstrapLatestRun is the compact latest-run summary in the health rollup.
type BootstrapLatestRun struct {
	RunID      string `json:"run_id"`
	Status     string `json:"status"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// ContextHealth is the response of GET /api/v1/me/context/health.
type ContextHealth struct {
	TotalNodes        int                 `json:"total_nodes"`
	NodesBySourceKind map[string]int      `json:"nodes_by_source_kind"`
	LastSync          string              `json:"last_sync,omitempty"`
	ConnectedSources  []string            `json:"connected_sources"`
	LatestRun         *BootstrapLatestRun `json:"latest_run,omitempty"`
}

// ── BootstrapClient ────────────────────────────────────────────────────────────

// BootstrapClient wraps a fleet Client + CapabilityPoller for the
// context-bootstrap API. Mirrors the ContextGraphSyncer construction shape:
// the client is the transport, the poller is the capability gate.
//
// A nil BootstrapClient (or a nil/nop underlying client) makes every method
// return a graceful error (ErrFleetDisabled), so the RPC layer can detect
// "fleet path unavailable" and fall back to the local recipe + noop sync.
type BootstrapClient struct {
	client *Client
	caps   *CapabilityPoller
}

// NewBootstrapClient constructs a BootstrapClient. Both arguments may be nil;
// the resulting client's methods then degrade to ErrFleetDisabled /
// ErrCapabilityNotInTier without panicking.
func NewBootstrapClient(client *Client, caps *CapabilityPoller) *BootstrapClient {
	return &BootstrapClient{client: client, caps: caps}
}

// Enabled reports whether the fleet bootstrap path is usable right now:
// the client is live (signed-in-capable) and the CapContextBootstrap
// capability is present. The RPC layer uses this to choose fleet vs local.
func (b *BootstrapClient) Enabled() bool {
	if b == nil || b.client == nil || b.client.isNop {
		return false
	}
	if b.caps == nil {
		return false
	}
	cur := b.caps.Current()
	return cur.Has(CapContextBootstrap)
}

// requireCap returns nil when the client is live AND CapContextBootstrap is
// present, else a graceful error (ErrFleetDisabled or ErrCapabilityNotInTier).
func (b *BootstrapClient) requireCap() error {
	if b == nil || b.client == nil || b.client.isNop {
		return ErrFleetDisabled
	}
	if b.caps == nil {
		return ErrCapabilityNotInTier
	}
	cur := b.caps.Current()
	return cur.Require(CapContextBootstrap)
}

// FetchBootstrapRecipe fetches the current versioned recipe from fleet.
func (b *BootstrapClient) FetchBootstrapRecipe(ctx context.Context) (*BootstrapRecipeWire, error) {
	if err := b.requireCap(); err != nil {
		return nil, err
	}
	resp, err := b.client.Get(ctx, "/api/v1/context/bootstrap/recipe")
	if err != nil {
		return nil, fmt.Errorf("fleet: bootstrap recipe: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: recipe pull", ErrCapabilityNotInTier)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: bootstrap recipe status %d: %s", resp.StatusCode, body)
	}
	var out BootstrapRecipeWire
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: bootstrap recipe decode: %w", err)
	}
	return &out, nil
}

// StartBootstrapRun starts a run. Returns the fleet-assigned run id + status.
// 201 = a fresh run, 200 = an active run already exists (both non-error).
func (b *BootstrapClient) StartBootstrapRun(ctx context.Context, req StartBootstrapRequest) (*StartBootstrapResponse, error) {
	if err := b.requireCap(); err != nil {
		return nil, err
	}
	resp, err := b.client.PostJSON(ctx, "/api/v1/context/bootstrap", req)
	if err != nil {
		return nil, fmt.Errorf("fleet: bootstrap start: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: bootstrap start", ErrCapabilityNotInTier)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: bootstrap start status %d: %s", resp.StatusCode, body)
	}
	var out StartBootstrapResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: bootstrap start decode: %w", err)
	}
	return &out, nil
}

// GetBootstrapRun reads the status of a run by id.
func (b *BootstrapClient) GetBootstrapRun(ctx context.Context, runID string) (*BootstrapRunStatus, error) {
	if err := b.requireCap(); err != nil {
		return nil, err
	}
	resp, err := b.client.Get(ctx, "/api/v1/context/bootstrap/"+runID)
	if err != nil {
		return nil, fmt.Errorf("fleet: bootstrap get: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("fleet: bootstrap run %q not found", runID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: bootstrap get status %d: %s", resp.StatusCode, body)
	}
	var out BootstrapRunStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: bootstrap get decode: %w", err)
	}
	return &out, nil
}

// PatchBootstrapRun advances a run's progress. Advance-only: a PATCH against a
// completed/failed run returns 409, surfaced as ErrBootstrapRunFinalized.
func (b *BootstrapClient) PatchBootstrapRun(ctx context.Context, runID string, patch BootstrapRunPatch) error {
	if err := b.requireCap(); err != nil {
		return err
	}
	resp, err := b.client.PatchJSON(ctx, "/api/v1/context/bootstrap/"+runID, patch)
	if err != nil {
		return fmt.Errorf("fleet: bootstrap patch: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusConflict {
		return ErrBootstrapRunFinalized
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet: bootstrap patch status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ResumeBootstrapRun sets a paused run back to running. 409 when the run is
// already completed / already running.
func (b *BootstrapClient) ResumeBootstrapRun(ctx context.Context, runID string) (*BootstrapRunStatus, error) {
	if err := b.requireCap(); err != nil {
		return nil, err
	}
	resp, err := b.client.PostJSON(ctx, "/api/v1/context/bootstrap/"+runID+"/resume", struct{}{})
	if err != nil {
		return nil, fmt.Errorf("fleet: bootstrap resume: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusConflict {
		return nil, ErrBootstrapRunFinalized
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: bootstrap resume status %d: %s", resp.StatusCode, body)
	}
	var out BootstrapRunStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		// Some servers reply 200 with no body on resume — tolerate that.
		return &BootstrapRunStatus{RunID: runID, Status: "running"}, nil
	}
	return &out, nil
}

// GetContextHealth fetches the context-graph health rollup for the caller.
func (b *BootstrapClient) GetContextHealth(ctx context.Context) (*ContextHealth, error) {
	if err := b.requireCap(); err != nil {
		return nil, err
	}
	resp, err := b.client.Get(ctx, "/api/v1/me/context/health")
	if err != nil {
		return nil, fmt.Errorf("fleet: context health: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: context health", ErrCapabilityNotInTier)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: context health status %d: %s", resp.StatusCode, body)
	}
	var out ContextHealth
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: context health decode: %w", err)
	}
	if out.NodesBySourceKind == nil {
		out.NodesBySourceKind = map[string]int{}
	}
	return &out, nil
}

// drainClose drains and closes an HTTP response body (keep-alive reuse + no leak).
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
