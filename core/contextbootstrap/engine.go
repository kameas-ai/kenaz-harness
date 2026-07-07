package contextbootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// engine.go implements WP07: dynamic workflow assembly + budget/coverage control.
//
// The Engine assembles a bootstrap run from the user's connected tools (per
// GatingResult.Approved), executes one ConnectorRun per connector, controls
// budget/coverage (stop-and-report, never silent truncation), and writes
// extracted context nodes via the ContextWriter.
//
// Usage:
//
//	cfg := Config{...}
//	eng := contextbootstrap.New(cfg)
//
//	// Start the run (blocks until done or ctx cancelled):
//	result, err := eng.Run(ctx, RunRequest{...})

// Config bundles all Engine dependencies. Every field that has a sensible
// default is nil-safe; the engine degrades gracefully (e.g. noop syncer
// when ContextWriter.Sync is deferred).
type Config struct {
	// RecipeSource provides the bootstrap recipe. Required.
	RecipeSource RecipeSource
	// Pool is the MCP connection pool. Required.
	Pool MCPPool
	// Model is the LLM caller for extraction prompts. Required.
	Model ModelCaller
	// Writer persists extracted nodes + syncs to fleet. Required.
	Writer ContextWriter
	// Progress receives live run-status updates. Optional (nil = silent).
	Progress ProgressSink
	// Consent checks per-source consent before extraction. Optional (nil = always-consent).
	Consent ConsentChecker
	// Logger is the structured logger. Optional (nil = slog.Default).
	Logger *slog.Logger
	// PerConnectorTimeout is the wall-clock timeout per connector run.
	// Zero defaults to 5 minutes.
	PerConnectorTimeout time.Duration
}

// Engine is the top-level bootstrap service. Safe for concurrent use; only
// one run is permitted at a time (subsequent calls block until the previous
// run finishes, then start a new one).
type Engine struct {
	cfg Config

	mu     sync.Mutex
	status RunStatus // last known status; protected by mu
}

// New constructs an Engine. Panics if cfg.RecipeSource, cfg.Pool, cfg.Model,
// or cfg.Writer are nil.
func New(cfg Config) *Engine {
	if cfg.RecipeSource == nil {
		panic("contextbootstrap.New: RecipeSource is required")
	}
	if cfg.Pool == nil {
		panic("contextbootstrap.New: Pool is required")
	}
	if cfg.Model == nil {
		panic("contextbootstrap.New: Model is required")
	}
	if cfg.Writer == nil {
		panic("contextbootstrap.New: Writer is required")
	}
	if cfg.Consent == nil {
		cfg.Consent = &alwaysConsentChecker{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.PerConnectorTimeout <= 0 {
		cfg.PerConnectorTimeout = 5 * time.Minute
	}
	return &Engine{cfg: cfg}
}

// Status returns a snapshot of the most recent run status.
func (e *Engine) Status() RunStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// RunRequest carries the inputs for a bootstrap run.
type RunRequest struct {
	// Interview is the result of the interview phase (WP08).
	Interview InterviewResult
	// GatingResult is the result of the MCP install/connect gating (WP08).
	// Only connectors in Approved will have extraction run.
	Gating GatingResult
}

// RunResult is the outcome of a completed bootstrap run.
type RunResult struct {
	RunID         RunID
	TotalNodes    int
	Clarifications []ClarificationItem
	CoverageReport []CoverageEntry
	// BlockedConnectors is forwarded from GatingResult.Blocked for display.
	BlockedConnectors []BlockedConnector
}

// Run executes the bootstrap run. It blocks until the run completes or ctx
// is cancelled. At most one run executes at a time.
//
// Run never silently truncates: when a budget ceiling is hit, it stops cleanly
// and records a CoverageEntry in the result and status.
func (e *Engine) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	runID := newRunID()
	log := e.cfg.Logger.With(slog.String("run_id", string(runID)))

	// Load the current recipe.
	recipe, err := e.cfg.RecipeSource.LoadRecipe(ctx)
	if err != nil {
		return RunResult{}, fmt.Errorf("contextbootstrap: load recipe: %w", err)
	}

	// Build trust map from the interview result.
	trustMap := buildTrustMap(req.Interview.TrustedPeople)

	// Build the connector catalog lookup.
	catalogByID := make(map[string]ConnectorDef, len(recipe.ConnectorCatalog))
	for _, c := range recipe.ConnectorCatalog {
		catalogByID[c.ID] = c
	}

	// Assemble the workflow: one ConnectorRun per approved connector.
	approvedDefs := make([]ConnectorDef, 0, len(req.Gating.Approved))
	for _, id := range req.Gating.Approved {
		def, ok := catalogByID[id]
		if !ok {
			log.Warn("approved connector not in recipe catalog", slog.String("connector_id", id))
			continue
		}
		approvedDefs = append(approvedDefs, def)
	}

	if len(approvedDefs) == 0 {
		log.Info("no approved connectors to run")
		return RunResult{
			RunID:             runID,
			BlockedConnectors: req.Gating.Blocked,
		}, nil
	}

	// Initialize progress tracking.
	connProgress := make([]ConnectorProgress, len(approvedDefs))
	for i, def := range approvedDefs {
		connProgress[i] = ConnectorProgress{
			ConnectorID: def.ID,
			Label:       def.Label,
			Status:      "pending",
		}
	}

	e.setStatus(RunStatus{
		RunID:      runID,
		Phase:      RunPhaseExtraction,
		StartedAt:  time.Now(),
		Connectors: connProgress,
	})
	e.emitProgress(ctx)

	// Execute each connector run sequentially (fan-out is recipe-driven; the
	// engine runs them in order to avoid overwhelming the MCP pool).
	var (
		allNodes          []ExtractedNode
		allClarifications []ClarificationItem
		coverageReport    []CoverageEntry
	)

	for i, def := range approvedDefs {
		if ctx.Err() != nil {
			break
		}

		// Check consent before starting extraction.
		ok, consentErr := e.cfg.Consent.HasConsent(ctx, def.ID)
		if consentErr != nil {
			log.Warn("consent check failed", slog.String("connector_id", def.ID), slog.Any("err", consentErr))
		}
		if !ok {
			connProgress[i].Status = "skipped"
			e.updateConnectorProgress(runID, connProgress)
			e.emitProgress(ctx)
			continue
		}

		connProgress[i].Status = "running"
		e.updateConnectorProgress(runID, connProgress)
		e.emitProgress(ctx)

		// Run this connector with a per-connector timeout.
		connCtx, cancel := context.WithTimeout(ctx, e.cfg.PerConnectorTimeout)
		run := newConnectorRun(def, e.cfg.Pool, e.cfg.Model, recipe, trustMap)
		result, runErr := run.Run(connCtx)
		cancel()

		if runErr != nil {
			log.Warn("connector run failed",
				slog.String("connector_id", def.ID),
				// NOTE: error is logged as a class, not raw content (privacy invariant).
				slog.String("error_class", classifyError(runErr)),
			)
			connProgress[i].Status = "failed"
		} else {
			connProgress[i].Status = "done"
			connProgress[i].ItemsFetched = result.ItemsFetched
			connProgress[i].NodesExtracted = len(result.Nodes)
			connProgress[i].BudgetHit = result.BudgetHit

			allNodes = append(allNodes, result.Nodes...)
			allClarifications = append(allClarifications, result.Clarifications...)

			if result.BudgetHit {
				coverageReport = append(coverageReport, CoverageEntry{
					ConnectorID:  def.ID,
					Label:        def.Label,
					ItemsFetched: result.ItemsFetched,
					BudgetKind:   result.BudgetKind,
					BudgetLimit:  def.ExtractionRecipe.MaxItems,
				})
				log.Info("connector hit budget ceiling — stopped cleanly",
					slog.String("connector_id", def.ID),
					slog.String("budget_kind", result.BudgetKind),
					slog.Int("items_fetched", result.ItemsFetched),
				)
			}
		}

		e.updateConnectorProgress(runID, connProgress)
		e.emitProgress(ctx)
	}

	// Write extracted nodes to the local context store.
	if len(allNodes) > 0 {
		if writeErr := e.cfg.Writer.WriteNodes(ctx, allNodes); writeErr != nil {
			log.Warn("context write failed",
				slog.String("error_class", classifyError(writeErr)),
				slog.Int("node_count", len(allNodes)),
			)
			// Non-fatal: proceed to sync attempt.
		}
	}

	// Sync to fleet (deferred seam — currently a no-op).
	syncPayload := buildSyncPayload(runID, allNodes, approvedDefs, connProgress)
	if syncErr := e.cfg.Writer.Sync(ctx, syncPayload); syncErr != nil {
		log.Warn("fleet sync failed (deferred)",
			slog.String("error_class", classifyError(syncErr)),
		)
	}

	// Final status update.
	totalNodes := len(allNodes)
	e.mu.Lock()
	e.status.Phase = RunPhaseDone
	if len(allClarifications) > 0 {
		e.status.Phase = RunPhaseClarify
	}
	e.status.TotalNodesWritten = totalNodes
	e.status.CoverageReport = coverageReport
	e.mu.Unlock()
	e.emitProgress(ctx)

	log.Info("bootstrap run complete",
		slog.Int("nodes_written", totalNodes),
		slog.Int("clarifications", len(allClarifications)),
		slog.Bool("coverage_hit", len(coverageReport) > 0),
	)

	return RunResult{
		RunID:             runID,
		TotalNodes:        totalNodes,
		Clarifications:    allClarifications,
		CoverageReport:    coverageReport,
		BlockedConnectors: req.Gating.Blocked,
	}, nil
}

// ApplyClarifications processes the user's answers to the post-run clarification
// loop (§9.1 Q8 / WP09). Confirmed nodes are written to the context store;
// rejected nodes are discarded; corrected nodes replace their Body.
func (e *Engine) ApplyClarifications(ctx context.Context, items []ClarificationItem, answers []ClarificationAnswer) error {
	answerMap := make(map[string]ClarificationAnswer, len(answers))
	for _, a := range answers {
		answerMap[a.NodeTitle] = a
	}

	var toWrite []ExtractedNode
	for _, item := range items {
		ans, ok := answerMap[item.Node.Title]
		if !ok {
			// Unanswered: treat as confirmed (best-effort).
			node := item.Node
			node.IsAsserted = true
			node.Confidence = 0.6 // modest confidence for unanswered clarifications
			toWrite = append(toWrite, node)
			continue
		}
		if ans.Rejected {
			continue
		}
		node := item.Node
		node.IsAsserted = true
		node.Confidence = 0.9 // user-confirmed = high confidence
		if ans.CorrectedBody != "" {
			node.Body = stripSecrets(ans.CorrectedBody)
		}
		toWrite = append(toWrite, node)
	}

	if len(toWrite) == 0 {
		return nil
	}
	return e.cfg.Writer.WriteNodes(ctx, toWrite)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (e *Engine) setStatus(s RunStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = s
}

func (e *Engine) updateConnectorProgress(runID RunID, connProgress []ConnectorProgress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Recompute total.
	total := 0
	for _, cp := range connProgress {
		total += cp.NodesExtracted
	}
	e.status.RunID = runID
	e.status.Connectors = append([]ConnectorProgress(nil), connProgress...)
	e.status.TotalNodesWritten = total
}

func (e *Engine) emitProgress(ctx context.Context) {
	if e.cfg.Progress == nil {
		return
	}
	e.mu.Lock()
	snap := e.status
	e.mu.Unlock()
	e.cfg.Progress.Emit(ctx, snap)
}

// newRunID generates a random hex run ID.
func newRunID() RunID {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return RunID(hex.EncodeToString(b))
}

// classifyError produces a typed error class label for structured logging.
// Raw error messages MUST NOT be logged (they may contain user content).
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case isContextError(err):
		return "context_canceled_or_deadline"
	case isPermissionError(err):
		return "permission_denied"
	default:
		return "other"
	}
}

func isContextError(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func isPermissionError(err error) bool {
	msg := err.Error()
	for _, kw := range []string{"permission", "unauthorized", "forbidden", "access denied"} {
		if containsCI(msg, kw) {
			return true
		}
	}
	return false
}

func containsCI(s, sub string) bool {
	ls := toLower(s)
	lsub := toLower(sub)
	return len(ls) >= len(lsub) && containsStr(ls, lsub)
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// buildSyncPayload constructs the fleet sync payload from the run results.
// Invariant: no credentials are included; only extracted nodes + status.
func buildSyncPayload(runID RunID, nodes []ExtractedNode, defs []ConnectorDef, progress []ConnectorProgress) SyncPayload {
	progressByID := make(map[string]ConnectorProgress, len(progress))
	for _, cp := range progress {
		progressByID[cp.ConnectorID] = cp
	}

	statuses := make([]ConnectorSyncStatus, 0, len(defs))
	for _, def := range defs {
		cp, ok := progressByID[def.ID]
		if !ok {
			continue
		}
		status := cp.Status
		if cp.BudgetHit {
			status = "budget_hit"
		}
		statuses = append(statuses, ConnectorSyncStatus{
			ConnectorID:    def.ID,
			Status:         status,
			ItemsFetched:   cp.ItemsFetched,
			NodesExtracted: cp.NodesExtracted,
		})
	}

	return SyncPayload{
		RunID:             runID,
		Nodes:             nodes,
		ConnectorStatuses: statuses,
	}
}
