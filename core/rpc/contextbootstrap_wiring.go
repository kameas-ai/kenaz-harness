// contextbootstrap_wiring.go wires the fleet-free context-bootstrap engine
// (core/contextbootstrap) to the harness's concrete subsystems: the fleet
// bootstrap client, the local Context Library, the MCP pool, the configured
// LLM, and the Wails event broker.
//
// OSS-first boundary: core/contextbootstrap MUST NOT import core/fleet. All
// fleet-backed adapters live HERE, in core/rpc (an allowlisted fleet importer
// per scripts/ci/check-no-fleet-imports.sh). The engine sees only its own
// seam interfaces (RecipeSource, ContextWriter, ProgressSink, MCPPool,
// ModelCaller); this file supplies the implementations.
//
// Every adapter is nil-safe and degrades gracefully when fleet is disabled:
//   - RecipeSource: fleet recipe when signed-in + CapContextBootstrap, else
//     the embedded LocalRecipeSource.
//   - ContextWriter: always writes nodes to the local Library; the fleet
//     /context/push + run-PATCH legs no-op when the fleet client is unavailable.
//   - ProgressSink: always emits to the Wails broker; the fleet run-PATCH leg
//     no-ops when fleet is unavailable.
//
// Privacy invariants (do NOT weaken):
//   - No third-party credentials ever reach a fleet payload.
//   - Node bodies / titles are NEVER logged (only counts + classes).
//
// Mission: context-bootstrap-harness-integration (WP02–WP04).
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/contextbootstrap"
	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	stdio "github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	auditview "github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
)

// ─── WP02: RecipeSource adapter ───────────────────────────────────────────────

// bootstrapRecipeSource loads the bootstrap recipe from fleet when the fleet
// path is enabled (signed-in + CapContextBootstrap), and otherwise falls back
// to the embedded LocalRecipeSource. It re-checks Enabled() on every LoadRecipe
// call so a mid-session sign-in / capability change is picked up without a
// harness restart.
type bootstrapRecipeSource struct {
	fleet *corefleet.BootstrapClient
	local *contextbootstrap.LocalRecipeSource
}

func newBootstrapRecipeSource(fleet *corefleet.BootstrapClient) *bootstrapRecipeSource {
	return &bootstrapRecipeSource{
		fleet: fleet,
		local: contextbootstrap.NewLocalRecipeSource(),
	}
}

// LoadRecipe implements contextbootstrap.RecipeSource.
func (s *bootstrapRecipeSource) LoadRecipe(ctx context.Context) (*contextbootstrap.BootstrapRecipe, error) {
	if s.fleet != nil && s.fleet.Enabled() {
		wire, err := s.fleet.FetchBootstrapRecipe(ctx)
		if err == nil && wire != nil {
			return mapFleetRecipe(wire), nil
		}
		// Fleet reachable-but-failing (network / decode) → fall back to local
		// so onboarding never hard-blocks. Log the class only.
		if err != nil {
			logging.L().Warn("contextbootstrap.recipe.fleet_fallback_local", "err_class", classifyWiringErr(err))
		}
	}
	return s.local.LoadRecipe(ctx)
}

// mapFleetRecipe converts the fleet wire recipe → the engine's BootstrapRecipe.
func mapFleetRecipe(w *corefleet.BootstrapRecipeWire) *contextbootstrap.BootstrapRecipe {
	r := &contextbootstrap.BootstrapRecipe{
		Version: w.Version,
		ConfidenceRules: contextbootstrap.ConfidenceRules{
			AssertMinCorroborations: w.ConfidenceRules.MinCorroborations,
			TrustedPersonWeight:     w.ConfidenceRules.TrustedPersonWeight,
			// NOTE: assert/tentative float thresholds are advisory-only — the engine
			// uses a count-based corroboration model (min_corroborations), so these
			// are intentionally not mapped yet.
		},
	}
	for _, c := range w.ConnectorCatalog {
		extraction := c.ExtractionPrompt
		if extraction == "" {
			extraction = w.ExtractionPrompts.PatternExtraction
		}
		r.ConnectorCatalog = append(r.ConnectorCatalog, contextbootstrap.ConnectorDef{
			ID:            c.ID,
			Label:         c.Label,
			MCPRecipeID:   c.MCPRecipeID,
			ReadOnlyTools: append([]string(nil), c.ReadOnlyTools...),
			ExtractionRecipe: contextbootstrap.ConnectorExtractionRecipe{
				FetchStrategy:    c.FetchStrategy,
				ExtractionPrompt: extraction,
				MaxItems:         c.MaxItems,
				MaxTokens:        c.MaxTokens,
			},
		})
		r.InterviewSchema.ToolChoices = append(r.InterviewSchema.ToolChoices, contextbootstrap.ToolChoice{
			ID:          c.ID,
			Label:       c.Label,
			Description: c.Description,
			MCPRecipeID: c.MCPRecipeID,
		})
	}
	for _, k := range w.ContextTaxonomy.NodeKinds {
		r.Taxonomy = append(r.Taxonomy, contextbootstrap.TaxonomyEntry{Kind: k})
	}
	return r
}

// ─── WP03: ContextWriter adapter ──────────────────────────────────────────────

// bootstrapContextWriter persists extracted nodes to the LOCAL Context Library
// AND (best-effort) pushes them to fleet's /context/push with
// source_kind + provenance + confidence. Sync() PATCHes run progress.
//
// The active fleet run id is set by the RPC layer before each run via
// SetRunID so the Sync leg PATCHes the right run.
type bootstrapContextWriter struct {
	lib          *corecontexts.Library
	fleetClient  *corefleet.Client
	fleetBoot    *corefleet.BootstrapClient
	auditEmitter contextaudit.Emitter

	mu    sync.RWMutex // guards runID; write on SetRunID, read during dispatch/Sync
	runID string       // current fleet run id; empty when fleet path is disabled

	// contextSyncedOnce guards the one-time PATCH /me/onboarding {context_synced:true}
	// fired after the first successful fleet push (mirrors the ContextGraphSyncer
	// first-push hook, which does not fire for bootstrap's direct /context/push).
	contextSyncedOnce sync.Once
}

func newBootstrapContextWriter(lib *corecontexts.Library, fleetClient *corefleet.Client, fleetBoot *corefleet.BootstrapClient, auditEmitter contextaudit.Emitter) *bootstrapContextWriter {
	return &bootstrapContextWriter{lib: lib, fleetClient: fleetClient, fleetBoot: fleetBoot, auditEmitter: auditEmitter}
}

// SetRunID records the fleet run id for subsequent Sync PATCH calls.
func (w *bootstrapContextWriter) SetRunID(runID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runID = runID
}

// WriteNodes implements contextbootstrap.ContextWriter. It writes each node to
// the local Context Library as a markdown file with YAML frontmatter carrying
// provenance (source_kind, source_ref, confidence, corroborations), then
// best-effort pushes each node to fleet /context/push.
//
// Privacy: node bodies/titles are never logged; only counts.
func (w *bootstrapContextWriter) WriteNodes(ctx context.Context, nodes []contextbootstrap.ExtractedNode) error {
	if len(nodes) == 0 {
		return nil
	}
	localWritten := 0
	pushed := 0
	for _, n := range nodes {
		if w.lib != nil {
			if err := w.lib.Save(bootstrapNodePath(n), renderNodeMarkdown(n)); err != nil {
				// Non-fatal per node; keep going. Log class, never the body/title.
				logging.L().Warn("contextbootstrap.write.local_failed", "err_class", classifyWiringErr(err), "kind", n.Kind)
			} else {
				localWritten++
			}
		}
		// Fleet push leg — best-effort, gated on the bootstrap capability.
		if w.pushNodeToFleet(ctx, n) {
			pushed++
		}
	}
	logging.L().Info("contextbootstrap.write.done", "local_written", localWritten, "fleet_pushed", pushed, "total", len(nodes))
	return nil
}

// pushNodeToFleet pushes one node to fleet /context/push with source_kind +
// provenance + confidence in metadata. Returns true on a successful push.
// Personal-classification is used (bootstrap-extracted context is personal by
// default — FR-008). Never includes credentials.
func (w *bootstrapContextWriter) pushNodeToFleet(ctx context.Context, n contextbootstrap.ExtractedNode) bool {
	if w.fleetBoot == nil || !w.fleetBoot.Enabled() || w.fleetClient == nil || w.fleetClient.IsNop() {
		return false
	}
	meta := bootstrapNodeMetadata(n)
	body := struct {
		Nodes []bootstrapPushNode `json:"nodes"`
	}{
		Nodes: []bootstrapPushNode{{
			ID:             bootstrapNodeID(n),
			Kind:           n.Kind,
			Title:          n.Title,
			Body:           n.Body,
			Metadata:       meta,
			Classification: "personal",
			SourceKind:     n.SourceKind,
			Provenance:     n.ConnectorID,
			Confidence:     n.Confidence,
			Version:        1,
		}},
	}
	resp, err := w.fleetClient.PostJSON(ctx, "/api/v1/context/push", body)
	if err != nil {
		logging.L().Warn("contextbootstrap.push.failed", "err_class", classifyWiringErr(err))
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		logging.L().Warn("contextbootstrap.push.non2xx", "status", resp.StatusCode)
		return false
	}
	// Audit: reuse KindFleetContextPublished (no title/body — only id + class).
	contextaudit.MustEmit(ctx, w.auditEmitter, contextaudit.KindFleetContextPublished,
		contextaudit.FleetContextPublishedPayload{
			NodeID:         bootstrapNodeID(n),
			Classification: "personal",
			Version:        1,
		}, time.Now())

	// After the FIRST successful push, PATCH /me/onboarding {context_synced:true}
	// exactly once. The ContextGraphSyncer first-push hook does not fire for the
	// bootstrap engine's direct /context/push calls, so we own the signal here.
	//
	// NOTE: a second PATCH /me/onboarding {context_synced:true} may also fire
	// from ContextGraphSyncer.SetFirstPushHook (api.go ~2043) when the regular
	// context-graph sync path runs its first push. Fleet treats context_synced as
	// advance-only/idempotent, so the double-PATCH is harmless.
	w.contextSyncedOnce.Do(func() {
		client := w.fleetClient
		go func() {
			if client == nil || client.IsNop() {
				return
			}
			if perr := client.PatchOnboardingState(context.Background(),
				corefleet.OnboardingStateWire{Schema: 1, ContextSynced: true}); perr != nil {
				logging.L().Warn("contextbootstrap.context_synced.patch_failed", "err_class", classifyWiringErr(perr))
			} else {
				logging.L().Info("contextbootstrap.context_synced.patch_ok")
			}
		}()
	})
	return true
}

// bootstrapPushNode is the /context/push node shape with the migration-0094
// provenance fields (source_kind, provenance, confidence). Kept local so the
// engine never sees a fleet type.
type bootstrapPushNode struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Classification string          `json:"classification"`
	SourceKind     string          `json:"source_kind,omitempty"`
	Provenance     string          `json:"provenance,omitempty"`
	Confidence     float64         `json:"confidence,omitempty"`
	Version        int             `json:"version"`
}

// Sync implements contextbootstrap.ContextWriter. It PATCHes the fleet run's
// aggregate progress (connector statuses + counts). Best-effort: a nil/nop
// fleet client or missing run id makes this a no-op (the local write already
// happened in WriteNodes).
func (w *bootstrapContextWriter) Sync(ctx context.Context, payload contextbootstrap.SyncPayload) error {
	w.mu.RLock()
	runID := w.runID
	w.mu.RUnlock()
	if w.fleetBoot == nil || !w.fleetBoot.Enabled() || runID == "" {
		return nil
	}
	patch := corefleet.BootstrapRunPatch{
		NodesCreated: len(payload.Nodes),
	}
	for _, cs := range payload.ConnectorStatuses {
		patch.Connectors = append(patch.Connectors, corefleet.BootstrapConnectorStatus{
			Name:           cs.ConnectorID,
			Status:         cs.Status,
			ItemsProcessed: cs.ItemsFetched,
			NodesCreated:   cs.NodesExtracted,
		})
	}
	if err := w.fleetBoot.PatchBootstrapRun(ctx, runID, patch); err != nil {
		logging.L().Warn("contextbootstrap.sync.patch_failed", "err_class", classifyWiringErr(err))
	}
	return nil
}

// ─── WP04: ProgressSink adapter ───────────────────────────────────────────────

// TopicContextBootstrapProgress is the Wails event topic the frontend
// subscribes to for live run progress.
const TopicContextBootstrapProgress = "contextbootstrap:progress"

// bootstrapProgressSink broadcasts run status to the frontend via the Wails
// broker AND PATCHes fleet run progress (phase + counts). Non-blocking.
type bootstrapProgressSink struct {
	broker    *StreamBroker
	fleetBoot *corefleet.BootstrapClient

	mu    sync.RWMutex // guards runID; write on SetRunID, read in Emit
	runID string
}

func newBootstrapProgressSink(broker *StreamBroker, fleetBoot *corefleet.BootstrapClient) *bootstrapProgressSink {
	return &bootstrapProgressSink{broker: broker, fleetBoot: fleetBoot}
}

// SetRunID records the fleet run id for progress PATCH calls.
func (p *bootstrapProgressSink) SetRunID(runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runID = runID
}

// Emit implements contextbootstrap.ProgressSink. It must not block: the broker
// emit is a fire-and-forget, and the fleet PATCH runs in a goroutine.
func (p *bootstrapProgressSink) Emit(ctx context.Context, status contextbootstrap.RunStatus) {
	if p.broker != nil {
		p.broker.emitter.Emit(p.broker.EmitCtx(), TopicContextBootstrapProgress, status)
	}
	p.mu.RLock()
	runID := p.runID
	p.mu.RUnlock()
	if p.fleetBoot != nil && p.fleetBoot.Enabled() && runID != "" {
		patch := corefleet.BootstrapRunPatch{
			Phase:        string(status.Phase),
			NodesCreated: status.TotalNodesWritten,
		}
		for _, c := range status.Connectors {
			patch.Connectors = append(patch.Connectors, corefleet.BootstrapConnectorStatus{
				Name:           c.ConnectorID,
				Status:         c.Status,
				ItemsProcessed: c.ItemsFetched,
				NodesCreated:   c.NodesExtracted,
			})
		}
		go func() {
			if err := p.fleetBoot.PatchBootstrapRun(context.Background(), runID, patch); err != nil {
				logging.L().Debug("contextbootstrap.progress.patch_failed", "err_class", classifyWiringErr(err))
			}
		}()
	}
}

// ─── WP04: ModelCaller adapter ────────────────────────────────────────────────

// bootstrapModelCompleter is the narrow slice of the autotitle LLM caller the
// engine needs: send a prompt, get text. Satisfied by
// *autotitle/wiring.LLMCaller (its Call signature is a superset).
type bootstrapModelCompleter interface {
	Call(ctx context.Context, systemPrompt, userPrompt string) (text string, inputTokens, outputTokens int, err error)
}

// bootstrapModelCaller adapts the harness's configured-model caller onto the
// engine's ModelCaller.Complete(ctx, prompt) seam.
type bootstrapModelCaller struct {
	caller bootstrapModelCompleter
}

func newBootstrapModelCaller(caller bootstrapModelCompleter) *bootstrapModelCaller {
	return &bootstrapModelCaller{caller: caller}
}

// Complete implements contextbootstrap.ModelCaller. Extraction prompts are
// fully self-contained (they frame source content as DATA), so the system
// prompt is empty and the whole instruction goes in the user turn.
func (m *bootstrapModelCaller) Complete(ctx context.Context, prompt string) (string, error) {
	if m == nil || m.caller == nil {
		return "", fmt.Errorf("contextbootstrap: no model configured")
	}
	text, _, _, err := m.caller.Call(ctx, "", prompt)
	if err != nil {
		return "", err
	}
	return text, nil
}

// ─── WP04: MCPPool adapter ────────────────────────────────────────────────────

// bootstrapMCPPool adapts the harness's *stdio.Pool onto the engine's MCPPool
// seam (Tools / Call / IsRunning).
type bootstrapMCPPool struct {
	pool *stdio.Pool
}

func newBootstrapMCPPool(pool *stdio.Pool) *bootstrapMCPPool {
	return &bootstrapMCPPool{pool: pool}
}

// Tools implements contextbootstrap.MCPPool. Maps coremcp.Tool → the engine's
// locally-declared MCPTool (avoids the engine importing core/mcp).
func (p *bootstrapMCPPool) Tools(ctx context.Context) ([]contextbootstrap.MCPTool, error) {
	if p.pool == nil {
		return nil, nil
	}
	tools, err := p.pool.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contextbootstrap.MCPTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, contextbootstrap.MCPTool{
			Server:      t.Server,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return out, nil
}

// Call implements contextbootstrap.MCPPool.
func (p *bootstrapMCPPool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("contextbootstrap: mcp pool unavailable")
	}
	return p.pool.Call(ctx, server, tool, args)
}

// IsRunning implements contextbootstrap.MCPPool. A recipe is "running" when the
// pool holds a live ServerInstance for that recipe id.
func (p *bootstrapMCPPool) IsRunning(ctx context.Context, recipeID string) bool {
	if p.pool == nil {
		return false
	}
	return p.pool.Server(recipeID) != nil
}

// ─── WP05: audit bridge ───────────────────────────────────────────────────────

// bootstrapAuditBridge routes contextbootstrap audit events into the in-process
// audit ring. Privacy: only opaque ids + counts appear; no node content.
type bootstrapAuditBridge struct {
	impl *auditview.API
}

func (e *bootstrapAuditBridge) Emit(_ context.Context, ev contextaudit.Event) error {
	if e == nil || e.impl == nil {
		return nil
	}
	e.impl.Push(auditview.Entry{
		ID:        fmt.Sprintf("ctx-bootstrap-%d", ev.TS.UnixNano()),
		Timestamp: ev.TS.UTC().Format(time.RFC3339Nano),
		Category:  "CONTEXT_BOOTSTRAP",
		Subject:   string(ev.Kind),
		Trailing:  fmt.Sprintf("payload_type=%T", ev.Payload),
	})
	return nil
}

// ─── Construction ─────────────────────────────────────────────────────────────

// contextBootstrapDeps bundles everything newContextBootstrapAPI needs. All
// fields are nil-tolerant; a nil model/pool/lib simply degrades that leg.
type contextBootstrapDeps struct {
	lib         *corecontexts.Library
	pool        *stdio.Pool
	model       bootstrapModelCompleter
	broker      *StreamBroker
	fleetClient *corefleet.Client
	caps        *corefleet.CapabilityPoller
	cedar       cedarpolicy.Gate
	audit       contextaudit.Emitter
}

// newContextBootstrapAPI assembles the engine + all seam adapters and returns
// the RPC-facing orchestration impl. Returns nil when the engine's required
// seams cannot be satisfied (no model configured) — callers fall back to the
// null accessor so the RPC surface degrades gracefully.
func newContextBootstrapAPI(d contextBootstrapDeps) *contextBootstrapImpl {
	// The engine requires a non-nil Model + Pool + Writer + RecipeSource.
	// When no model is configured we cannot build the engine; return nil and
	// let the null accessor answer the RPC surface with "not available".
	if d.model == nil {
		logging.L().Info("contextbootstrap.engine.skipped", "reason", "no_model_configured")
		return nil
	}

	fleetBoot := corefleet.NewBootstrapClient(d.fleetClient, d.caps)
	recipe := newBootstrapRecipeSource(fleetBoot)
	writer := newBootstrapContextWriter(d.lib, d.fleetClient, fleetBoot, d.audit)
	progress := newBootstrapProgressSink(d.broker, fleetBoot)
	pool := newBootstrapMCPPool(d.pool)
	model := newBootstrapModelCaller(d.model)

	eng := contextbootstrap.New(contextbootstrap.Config{
		RecipeSource: recipe,
		Pool:         pool,
		Model:        model,
		Writer:       writer,
		Progress:     progress,
	})

	return &contextBootstrapImpl{
		engine:    eng,
		recipe:    recipe,
		writer:    writer,
		progress:  progress,
		pool:      pool,
		fleetBoot: fleetBoot,
		cedar:     d.cedar,
		audit:     d.audit,
	}
}

// nullContextBootstrapAPI is the graceful "engine unavailable" implementation
// returned when no model is configured. Every method returns an empty result
// (or a descriptive error for run-dispatch) without panicking.
type nullContextBootstrapAPI struct{}

func (nullContextBootstrapAPI) StartRun(context.Context, StartBootstrapRunRequest) (StartBootstrapRunResult, error) {
	return StartBootstrapRunResult{}, fmt.Errorf("contextbootstrap: unavailable (no model configured)")
}
func (nullContextBootstrapAPI) Resume(context.Context, string) (StartBootstrapRunResult, error) {
	return StartBootstrapRunResult{}, fmt.Errorf("contextbootstrap: unavailable (no model configured)")
}
func (nullContextBootstrapAPI) Status(context.Context) (contextbootstrap.RunStatus, error) {
	return contextbootstrap.RunStatus{Phase: contextbootstrap.RunPhaseIdle}, nil
}
func (nullContextBootstrapAPI) Health(context.Context) (corefleet.ContextHealth, error) {
	return corefleet.ContextHealth{NodesBySourceKind: map[string]int{}, ConnectedSources: []string{}}, nil
}

// ─── Orchestration API (WP04/WP05) ────────────────────────────────────────────

// ContextBootstrapAPI is the RPC-facing surface for the context-bootstrap
// engine. It orchestrates: POST /bootstrap (fleet run id) → engine.Run →
// PATCH progress → PATCH completed/failed, and exposes status + health reads.
//
// All methods are safe to call when fleet is disabled: the run executes
// locally (LocalRecipeSource, local Library writes) and the fleet legs no-op.
type ContextBootstrapAPI interface {
	// StartRun kicks a bootstrap run over the given consented connector ids.
	// Returns the run id (fleet-assigned when the fleet path is live, else a
	// local engine run id) plus the recipe version used.
	StartRun(ctx context.Context, req StartBootstrapRunRequest) (StartBootstrapRunResult, error)
	// Status returns the latest run status snapshot from the engine.
	Status(ctx context.Context) (contextbootstrap.RunStatus, error)
	// Resume resumes a paused/interrupted run (fleet resume + re-dispatch).
	Resume(ctx context.Context, runID string) (StartBootstrapRunResult, error)
	// Health returns the context-graph health rollup (fleet when live, else a
	// local zero-value rollup so the card renders without a fleet round-trip).
	Health(ctx context.Context) (corefleet.ContextHealth, error)
}

// StartBootstrapRunRequest is the RPC request to start a run.
type StartBootstrapRunRequest struct {
	// ConsentedSources is the list of connector ids the user consented to.
	ConsentedSources []string `json:"consented_sources"`
	// TrustedPeople is the user-declared trust map (optional).
	TrustedPeople []contextbootstrap.TrustedPerson `json:"trusted_people,omitempty"`
}

// StartBootstrapRunResult is the RPC response for StartRun / Resume.
type StartBootstrapRunResult struct {
	RunID         string `json:"run_id"`
	RecipeVersion string `json:"recipe_version"`
	Status        string `json:"status"`
	// FleetBacked is true when the run is tracked by a fleet run id.
	FleetBacked bool `json:"fleet_backed"`
}

// contextBootstrapImpl is the concrete ContextBootstrapAPI. It holds the engine
// plus the run-scoped adapters (writer + progress sink) so it can thread the
// fleet run id into them before each run.
type contextBootstrapImpl struct {
	engine    *contextbootstrap.Engine
	recipe    *bootstrapRecipeSource
	writer    *bootstrapContextWriter
	progress  *bootstrapProgressSink
	pool      contextbootstrap.MCPPool
	fleetBoot *corefleet.BootstrapClient
	cedar     cedarpolicy.Gate
	audit     contextaudit.Emitter
}

// StartRun implements ContextBootstrapAPI. It runs the Cedar gate before any
// dispatch (a forbid rule disables the bootstrap engine).
func (a *contextBootstrapImpl) StartRun(ctx context.Context, req StartBootstrapRunRequest) (StartBootstrapRunResult, error) {
	if err := cedarpolicy.CheckContextBootstrapRun(ctx, a.cedar); err != nil {
		return StartBootstrapRunResult{}, err
	}
	return a.dispatch(ctx, "", req.ConsentedSources, req.TrustedPeople)
}

// Resume implements ContextBootstrapAPI. It first asks fleet to resume the run
// (advance-only), then re-dispatches the engine over the run's consented
// sources. When fleet is disabled, resume is a fresh local dispatch.
func (a *contextBootstrapImpl) Resume(ctx context.Context, runID string) (StartBootstrapRunResult, error) {
	if err := cedarpolicy.CheckContextBootstrapRun(ctx, a.cedar); err != nil {
		return StartBootstrapRunResult{}, err
	}
	var sources []string
	if a.fleetBoot != nil && a.fleetBoot.Enabled() && runID != "" {
		if st, err := a.fleetBoot.ResumeBootstrapRun(ctx, runID); err == nil && st != nil {
			sources = st.ConsentedSources
		} else if err != nil {
			logging.L().Warn("contextbootstrap.resume.fleet_failed", "err_class", classifyWiringErr(err))
		}
	}
	return a.dispatch(ctx, runID, sources, nil)
}

// dispatch is the shared start/resume path. It runs the Cedar gate, opens (or
// reuses) a fleet run id, threads it into the run-scoped adapters, runs the
// engine, and finalises the fleet run.
func (a *contextBootstrapImpl) dispatch(ctx context.Context, existingRunID string, sources []string, trusted []contextbootstrap.TrustedPerson) (StartBootstrapRunResult, error) {
	if a == nil || a.engine == nil {
		return StartBootstrapRunResult{}, fmt.Errorf("contextbootstrap: engine not wired")
	}

	// Load the recipe first so we know the recipe version to report to fleet.
	rec, err := a.recipe.LoadRecipe(ctx)
	if err != nil {
		return StartBootstrapRunResult{}, fmt.Errorf("contextbootstrap: load recipe: %w", err)
	}

	// Open a fleet run when the fleet path is live and we don't already have one.
	fleetRunID := existingRunID
	fleetBacked := false
	if a.fleetBoot != nil && a.fleetBoot.Enabled() {
		if fleetRunID == "" {
			if resp, serr := a.fleetBoot.StartBootstrapRun(ctx, corefleet.StartBootstrapRequest{
				ConsentedSources: sources,
				RecipeVersion:    rec.Version,
			}); serr == nil && resp != nil {
				fleetRunID = resp.RunID
				fleetBacked = true
			} else if serr != nil {
				logging.L().Warn("contextbootstrap.start.fleet_failed", "err_class", classifyWiringErr(serr))
			}
		} else {
			fleetBacked = true
		}
	}

	// Thread the fleet run id into the run-scoped adapters.
	if a.writer != nil {
		a.writer.SetRunID(fleetRunID)
	}
	if a.progress != nil {
		a.progress.SetRunID(fleetRunID)
	}

	// Build the gating result: only connectors whose MCP is running are
	// approved. Missing MCPs are reported as blocked (the frontend guides the
	// user to install/connect them).
	gating := a.buildGating(ctx, rec, sources)

	interview := contextbootstrap.InterviewResult{
		SelectedConnectorIDs: sources,
		TrustedPeople:        trusted,
	}

	// Audit: run started.
	contextaudit.MustEmit(ctx, a.audit, contextaudit.KindContextBootstrapRun,
		contextaudit.ContextBootstrapRunPayload{
			RunID:          fleetRunID,
			Phase:          string(contextbootstrap.RunPhaseExtraction),
			Outcome:        "started",
			ConnectorCount: len(gating.Approved),
		}, time.Now())

	// Run the engine (blocking).
	result, runErr := a.engine.Run(ctx, contextbootstrap.RunRequest{
		Interview: interview,
		Gating:    gating,
	})

	// Finalise the fleet run + emit the terminal audit event.
	finalStatus := "completed"
	outcome := "completed"
	errClass := ""
	if runErr != nil {
		finalStatus = "failed"
		outcome = "failed"
		errClass = classifyWiringErr(runErr)
	}
	if a.fleetBoot != nil && a.fleetBoot.Enabled() && fleetRunID != "" {
		if perr := a.fleetBoot.PatchBootstrapRun(ctx, fleetRunID, corefleet.BootstrapRunPatch{
			Status:       finalStatus,
			NodesCreated: result.TotalNodes,
		}); perr != nil {
			logging.L().Warn("contextbootstrap.finalize.patch_failed", "err_class", classifyWiringErr(perr))
		}
	}
	contextaudit.MustEmit(ctx, a.audit, contextaudit.KindContextBootstrapRun,
		contextaudit.ContextBootstrapRunPayload{
			RunID:          fleetRunID,
			Phase:          finalStatus,
			Outcome:        outcome,
			ConnectorCount: len(gating.Approved),
			NodesWritten:   result.TotalNodes,
			CoverageHit:    len(result.CoverageReport) > 0,
			ErrorClass:     errClass,
		}, time.Now())

	if runErr != nil {
		return StartBootstrapRunResult{}, fmt.Errorf("contextbootstrap: run: %w", runErr)
	}

	rid := fleetRunID
	if rid == "" {
		rid = string(result.RunID)
	}
	return StartBootstrapRunResult{
		RunID:         rid,
		RecipeVersion: rec.Version,
		Status:        finalStatus,
		FleetBacked:   fleetBacked,
	}, nil
}

// buildGating partitions requested sources into approved (MCP running) and
// blocked (MCP not installed/connected) using the recipe's connector catalog.
func (a *contextBootstrapImpl) buildGating(ctx context.Context, rec *contextbootstrap.BootstrapRecipe, sources []string) contextbootstrap.GatingResult {
	catalog := make(map[string]contextbootstrap.ConnectorDef, len(rec.ConnectorCatalog))
	for _, c := range rec.ConnectorCatalog {
		catalog[c.ID] = c
	}
	var gating contextbootstrap.GatingResult
	pool := a.enginePool()
	for _, id := range sources {
		def, ok := catalog[id]
		if !ok {
			gating.Blocked = append(gating.Blocked, contextbootstrap.BlockedConnector{
				ConnectorID: id, Reason: "not_found",
			})
			continue
		}
		if pool != nil && def.MCPRecipeID != "" && !pool.IsRunning(ctx, def.MCPRecipeID) {
			gating.Blocked = append(gating.Blocked, contextbootstrap.BlockedConnector{
				ConnectorID: id, Label: def.Label, Reason: "not_installed",
			})
			continue
		}
		gating.Approved = append(gating.Approved, id)
	}
	return gating
}

// enginePool exposes the MCP pool the engine was constructed with, for gating.
// Set at construction; may be nil in tests.
func (a *contextBootstrapImpl) enginePool() contextbootstrap.MCPPool { return a.pool }

// Status implements ContextBootstrapAPI.
func (a *contextBootstrapImpl) Status(_ context.Context) (contextbootstrap.RunStatus, error) {
	if a == nil || a.engine == nil {
		return contextbootstrap.RunStatus{Phase: contextbootstrap.RunPhaseIdle}, nil
	}
	return a.engine.Status(), nil
}

// Health implements ContextBootstrapAPI. Falls back to a local zero rollup
// when the fleet path is unavailable so the frontend card always renders.
func (a *contextBootstrapImpl) Health(ctx context.Context) (corefleet.ContextHealth, error) {
	if a != nil && a.fleetBoot != nil && a.fleetBoot.Enabled() {
		if h, err := a.fleetBoot.GetContextHealth(ctx); err == nil && h != nil {
			return *h, nil
		} else if err != nil {
			logging.L().Debug("contextbootstrap.health.fleet_failed", "err_class", classifyWiringErr(err))
		}
	}
	return corefleet.ContextHealth{NodesBySourceKind: map[string]int{}, ConnectedSources: []string{}}, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// bootstrapNodeID derives a stable-ish id for an extracted node from its
// connector + source-ref + title. Never a credential.
func bootstrapNodeID(n contextbootstrap.ExtractedNode) string {
	ref := n.SourceRef
	if ref == "" {
		ref = n.Title
	}
	return "ctxb-" + sanitizeSlug(n.ConnectorID) + "-" + sanitizeSlug(ref)
}

// bootstrapNodePath is the local Library path for a node: bootstrap/<kind>/<slug>.md.
func bootstrapNodePath(n contextbootstrap.ExtractedNode) string {
	kind := sanitizeSlug(n.Kind)
	if kind == "" {
		kind = "misc"
	}
	slug := sanitizeSlug(n.Title)
	if slug == "" {
		slug = sanitizeSlug(n.SourceRef)
	}
	if slug == "" {
		slug = "node"
	}
	return "bootstrap/" + kind + "/" + slug + ".md"
}

// renderNodeMarkdown renders an ExtractedNode as a markdown context entry with
// YAML frontmatter carrying provenance. Body is already secret-stripped by the
// engine's quarantine; we do not add @secret: tokens.
func renderNodeMarkdown(n contextbootstrap.ExtractedNode) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "kind: %s\n", yamlScalar(n.Kind))
	fmt.Fprintf(&b, "source_kind: %s\n", yamlScalar(n.SourceKind))
	fmt.Fprintf(&b, "connector: %s\n", yamlScalar(n.ConnectorID))
	if n.SourceRef != "" {
		fmt.Fprintf(&b, "source_ref: %s\n", yamlScalar(n.SourceRef))
	}
	fmt.Fprintf(&b, "confidence: %.3f\n", n.Confidence)
	fmt.Fprintf(&b, "corroborations: %d\n", n.Corroborations)
	fmt.Fprintf(&b, "asserted: %t\n", n.IsAsserted)
	fmt.Fprintf(&b, "extracted_by: context_bootstrap\n")
	b.WriteString("---\n\n")
	if n.Title != "" {
		fmt.Fprintf(&b, "# %s\n\n", n.Title)
	}
	b.WriteString(n.Body)
	b.WriteString("\n")
	return b.String()
}

// bootstrapNodeMetadata builds the /context/push metadata blob (provenance).
func bootstrapNodeMetadata(n contextbootstrap.ExtractedNode) json.RawMessage {
	m := map[string]any{
		"source_kind":    n.SourceKind,
		"connector":      n.ConnectorID,
		"confidence":     n.Confidence,
		"corroborations": n.Corroborations,
		"asserted":       n.IsAsserted,
		"extracted_by":   "context_bootstrap",
	}
	if n.SourceRef != "" {
		m["source_ref"] = n.SourceRef
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return raw
}

// yamlScalar quotes a YAML scalar defensively (single-line values only).
func yamlScalar(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "\"\""
	}
	if strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`") {
		return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
	}
	return s
}

// sanitizeSlug lowercases and replaces non-alphanumeric runs with a single dash.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// classifyWiringErr returns a coarse error class for logging (never the raw
// message, which may carry user content or URLs).
func classifyWiringErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context canceled") || strings.Contains(msg, "deadline"):
		return "context_canceled_or_deadline"
	case strings.Contains(msg, "capability"):
		return "capability_absent"
	case strings.Contains(msg, "disabled"):
		return "fleet_disabled"
	case strings.Contains(msg, "not signed in") || strings.Contains(msg, "unauthorized"):
		return "not_signed_in"
	default:
		return "other"
	}
}
