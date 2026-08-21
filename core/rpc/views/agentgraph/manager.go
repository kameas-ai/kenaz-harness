package agentgraph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/activities"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/prompts"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/elicitation"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Manager owns the kernel, the on-disk graph library, the activity
// catalog, and the per-run bookkeeping (counters, ask bus, cancel
// channels) the API needs to satisfy a frontend GraphsView/RunView.
//
// Manager is intentionally local-first: every graph and event lives
// either embedded in the binary (bundled library, bundled activities)
// or under <DataDir>. There is no network surface.
type Manager struct {
	mu sync.RWMutex

	dataDir string

	kernel    *coreag.Kernel
	log       coreag.EventLog
	catalog   *activities.Catalog
	envDeps   EnvDeps

	// routingEnabled reports the agentic-turn-routing launch flag
	// (agentgraph-total-convergence-01PMGX01 WP11b). nil means OFF,
	// which is both the shipped default and the fail-closed reading —
	// a Manager constructed without the settings seam (every test
	// chassis) must not execute the routed topology.
	routingEnabled func() bool

	// Library: bundled graphs the loader splices in (read-only).
	bundled map[string]bundledGraph

	// In-memory run registry keyed by run id.
	runs map[string]*runEntry

	// externalRuns holds the resolved spec of runs this manager did not
	// start — chat turns, which drive the shared kernel directly
	// (WP12). externalRunOrder is the insertion order used for eviction.
	externalRuns     map[string]coreag.Graph
	externalRunOrder []string

	// asks routes AskNode pending → answers per run + node.
	asks *askRouter

	// nowFn allows tests to pin timestamps.
	nowFn func() time.Time

	// cedarGate gates Graph_ResolveApproval (approval-node-01PMZC12
	// UNIT-3). nil ⇒ AllowAll (cedar.CheckApprovalResolve is
	// nil-safe). This gate covers ONLY the human-verb path — the
	// no-watcher / auto-approve-window timeouts resolve without it
	// (spec.md §5.3: a Cedar misconfiguration must not be able to
	// leave a run parked forever when the system itself decided to
	// fail closed).
	cedarGate cedar.Gate

	// auditEmitter records KindApprovalResolved to the Settings audit
	// panel. nil ⇒ no-op (contextaudit.Emit is nil-safe against a nil
	// Emitter only if the caller checks; this Manager checks before
	// calling).
	auditEmitter contextaudit.Emitter

	// approvalTimeout bounds how long an approval node may sit
	// unresolved before the no-watcher fail-closed posture resolves it
	// to rejected (approval-node-01PMZC12 UNIT-4, FR-006, G5). A
	// positive auto_approve_window_seconds on the node overrides this
	// per-node with a shorter auto-APPROVE deadline (UNIT-5, FR-007).
	// Zero disables the fallback entirely — test-only; production
	// always sets defaultApprovalTimeout via NewManager.
	approvalTimeout time.Duration
}

// defaultApprovalTimeout is the production no-watcher fail-closed
// deadline: how long an approval node may sit unresolved, with no
// auto_approve_window_seconds override, before the run resolves itself
// to rejected rather than parking forever (spec.md G5). Generous on
// purpose — this is the backstop for a run genuinely nobody is
// watching, not a UX timeout for an attended one.
const defaultApprovalTimeout = 24 * time.Hour

// maxTrackedExternalRuns bounds the chat-run spec registry (WP12). Big
// enough that "show me the graph of the turn I just ran" always works
// across a working session; small enough that a day of chatting cannot
// pin hundreds of resolved specs in memory.
const maxTrackedExternalRuns = 64

// ManagerOption tunes a Manager at construction.
type ManagerOption func(*Manager)

// WithKernel injects a pre-configured kernel. Useful in tests when
// callers want to pin executor overrides; production wiring constructs
// a kernel here with the supplied EventLog.
func WithKernel(k *coreag.Kernel) ManagerOption {
	return func(m *Manager) { m.kernel = k }
}

// WithEventLog plugs an EventLog into the manager's kernel.
func WithEventLog(l coreag.EventLog) ManagerOption {
	return func(m *Manager) { m.log = l }
}

// WithDataDir sets the base directory for graph library + activity
// overrides. Empty disables disk persistence (the frontend can still
// list the bundled library + run graphs in-memory).
func WithDataDir(dir string) ManagerOption {
	return func(m *Manager) { m.dataDir = dir }
}

// WithCatalog installs an activity catalog. Default is a fresh
// LoadCatalog over the bundled set.
func WithCatalog(cat *activities.Catalog) ManagerOption {
	return func(m *Manager) { m.catalog = cat }
}

// WithClock pins the manager's clock; used in tests for deterministic
// timestamps.
func WithClock(now func() time.Time) ManagerOption {
	return func(m *Manager) { m.nowFn = now }
}

// WithCedarGate gates Graph_ResolveApproval (approval-node-01PMZC12
// UNIT-3). nil is equivalent to omitting the option.
func WithCedarGate(g cedar.Gate) ManagerOption {
	return func(m *Manager) { m.cedarGate = g }
}

// WithAuditEmitter records approval resolutions to the Settings audit
// panel (approval-node-01PMZC12 UNIT-3). nil is equivalent to omitting
// the option.
func WithAuditEmitter(e contextaudit.Emitter) ManagerOption {
	return func(m *Manager) { m.auditEmitter = e }
}

// WithApprovalTimeout overrides the no-watcher fail-closed deadline
// (approval-node-01PMZC12 UNIT-4). Tests use a short duration so AC-05
// resolves within a bounded test budget instead of
// defaultApprovalTimeout's 24h; production wiring does not call this
// and gets the default.
func WithApprovalTimeout(d time.Duration) ManagerOption {
	return func(m *Manager) { m.approvalTimeout = d }
}

// bundledGraph carries the parsed Graph alongside its YAML source so
// LoadGraph can return both the typed metadata + the editable text.
type bundledGraph struct {
	graph coreag.Graph
	yaml  string
}

// runEntry is the per-run bookkeeping the API needs.
type runEntry struct {
	mu sync.Mutex

	id        string
	graphID   string
	sessionID string
	graph     coreag.Graph
	state     string
	startedAt time.Time
	updatedAt time.Time
	endedAt   time.Time
	err       error
	env       *coreag.Env
	cancel    context.CancelFunc

	// pendingAsk is set when an AskNode parks the run.
	pendingAsk *PendingAsk

	// pendingApproval is set when an approval node parks the run.
	// Mutually exclusive with pendingAsk — the kernel parks on at most
	// one node per pause cycle (approval-node-01PMZC12 UNIT-3).
	pendingApproval *PendingApproval

	// approvalTimer fires the no-watcher fail-closed resolution (or the
	// auto_approve_window_seconds auto-approve) if nobody resolves
	// pendingApproval first. Stopped and cleared on any resolution or
	// on cancelRun so it never fires against a run that already moved
	// on (approval-node-01PMZC12 UNIT-4).
	approvalTimer *time.Timer
}

// NewManager constructs a Manager populated with the bundled library
// (toolloop_default.yaml) plus the bundled activity catalog.
func NewManager(opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		bundled:         map[string]bundledGraph{},
		runs:            map[string]*runEntry{},
		asks:            newAskRouter(),
		nowFn:           func() time.Time { return time.Now().UTC() },
		approvalTimeout: defaultApprovalTimeout,
	}
	for _, o := range opts {
		o(m)
	}
	if m.catalog == nil {
		userActivities := ""
		if m.dataDir != "" {
			userActivities = filepath.Join(m.dataDir, "agent_graph", "activities")
		}
		cat, err := activities.LoadCatalog(activities.LoadOptions{UserDir: userActivities})
		if err != nil {
			// Non-fatal; the catalog still contains whatever loaded.
			// (FR-006) WARN-log so a corrupt user-activities directory is
			// diagnosable rather than silently swallowed.
			slog.Warn("agentgraph: activity catalog load error; catalog may be incomplete",
				"user_dir", userActivities,
				"error",    err.Error(),
			)
		}
		if cat == nil {
			cat = activities.NewEmptyCatalog()
		}
		m.catalog = cat
	}
	if m.log == nil {
		m.log = coreag.NewMemoryEventLog()
	}
	if m.kernel == nil {
		m.kernel = coreag.NewKernel(coreag.WithEventLog(m.log))
	}
	if err := loadBundledLibrary(m); err != nil {
		return nil, err
	}
	return m, nil
}

// Catalog exposes the activity catalog so the API can satisfy
// list-activities calls without a separate accessor (kept private to
// the package — the public API surface is the impl's RPC methods).
func (m *Manager) Catalog() *activities.Catalog { return m.catalog }

// EventLog returns the underlying log. Tests rely on this to inspect
// raw events.
func (m *Manager) EventLog() coreag.EventLog { return m.log }

// Kernel returns the underlying *coreag.Kernel. The chat-migration
// ChatRunner shares the manager's kernel so chat runs reuse the same
// EventLog / executors the graph view debugs against. Returns nil
// when the manager was constructed without a kernel (test path).
func (m *Manager) Kernel() *coreag.Kernel { return m.kernel }

// EnvDefaults returns a closure that applies the manager's configured
// production EnvDeps onto an Env. The chat runner threads this onto
// every per-run Env so memory / policy / branch / journal seams are
// wired identically to the graph runner. The returned closure is nil
// when the manager has no EnvDeps (test path); callers must nil-check.
func (m *Manager) EnvDefaults() func(*coreag.Env) {
	deps := m.envDeps
	return func(env *coreag.Env) { deps.applyTo(env) }
}

// LoadGraphSpec returns the parsed graph for the supplied id, applying
// the same user-wins-over-bundled lookup the impl_graphs.LoadGraph
// surface uses. The chat runner consumes this to resolve
// chat_default.yaml on every StartStream so user-saved overrides take
// effect on the next chat turn without a process restart.
func (m *Manager) LoadGraphSpec(id string) (coreag.Graph, error) {
	spec, err := m.loadGraph(id)
	if err != nil {
		return coreag.Graph{}, err
	}
	g, err := coreag.LoadYAML([]byte(spec.YAML))
	if err != nil {
		return coreag.Graph{}, fmt.Errorf("agentgraph: parse %q: %w", id, err)
	}
	// WP05 (system-prompt-grounding): seed the shared base constitution
	// onto every non-chat graph's SystemPrompt at load time — the single
	// source of truth for base grounding, so graph authors never have to
	// paste the constitution text into YAML. chat_default is excluded:
	// the chat graph is grounded via its own dedicated mission (WP02) so
	// seeding it here too would double-compose (or fight) that path.
	if id != "chat_default" {
		g.SystemPrompt = seedBaseConstitution(g.SystemPrompt)
	}
	return g, nil
}

// seedBaseConstitution prepends the shared prompts.DefaultBaseConstitution()
// to an existing graph-level system prompt. Both parts are trimmed of
// surrounding whitespace and empty parts are dropped before joining with a
// blank line, mirroring the kernel's own composePrompt behaviour
// (core/agentgraph/exec_compute.go) without importing it — this package
// sits above core/agentgraph and only needs the two-part join.
func seedBaseConstitution(existing string) string {
	base := strings.TrimSpace(prompts.DefaultBaseConstitution())
	existing = strings.TrimSpace(existing)
	switch {
	case base == "":
		return existing
	case existing == "":
		return base
	default:
		return base + "\n\n" + existing
	}
}

// listLibrary returns the bundled + user library entries.
func (m *Manager) listLibrary(scope GraphScope) []GraphInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []GraphInfo{}
	if scope == "" || scope == "library" {
		for id, b := range m.bundled {
			out = append(out, GraphInfo{
				ID:          id,
				Name:        b.graph.Name,
				Description: b.graph.Description,
				Scope:       "library",
				Source:      "bundled",
			})
		}
	}
	if scope == "" || scope == "user" {
		userDir := m.userLibraryDir()
		if userDir != "" {
			entries, err := os.ReadDir(userDir)
			if err == nil {
				for _, e := range entries {
					name := e.Name()
					if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".yaml") {
						continue
					}
					full := filepath.Join(userDir, name)
					data, err := os.ReadFile(full) //nolint:gosec // user-controlled path under DataDir is intentional
					if err != nil {
						continue
					}
					g, err := coreag.LoadYAML(data)
					if err != nil || g.ID == "" {
						continue
					}
					st, err := os.Stat(full)
					var updated string
					if err == nil {
						updated = st.ModTime().UTC().Format(time.RFC3339Nano)
					}
					out = append(out, GraphInfo{
						ID:          g.ID,
						Name:        g.Name,
						Description: g.Description,
						Scope:       "user",
						Source:      full,
						UpdatedAt:   updated,
					})
				}
			}
		}
	}
	return out
}

// loadGraph returns the YAML payload for a single graph, preferring
// user-saved versions over bundled.
func (m *Manager) loadGraph(id string) (GraphSpec, error) {
	if id == "" {
		return GraphSpec{}, errors.New("agentgraph: id required")
	}
	// User dir wins.
	userDir := m.userLibraryDir()
	if userDir != "" {
		full := filepath.Join(userDir, sanitizeGraphFilename(id))
		if data, err := os.ReadFile(full); err == nil { //nolint:gosec // user-controlled path under DataDir is intentional
			g, err := coreag.LoadYAML(data)
			if err != nil {
				return GraphSpec{}, fmt.Errorf("agentgraph: parse %s: %w", full, err)
			}
			return GraphSpec{ID: g.ID, Name: g.Name, Scope: "user", YAML: string(data)}, nil
		}
	}
	m.mu.RLock()
	b, ok := m.bundled[id]
	m.mu.RUnlock()
	if !ok {
		return GraphSpec{}, fmt.Errorf("agentgraph: graph %q not found", id)
	}
	return GraphSpec{ID: id, Name: b.graph.Name, Scope: "library", YAML: b.yaml}, nil
}

// saveGraph persists user-supplied YAML at <DataDir>/agent_graph/library/<id>.yaml.
// Library ids are reserved; saving over a bundled id is rejected so
// users can't accidentally shadow the default toolloop graph.
func (m *Manager) saveGraph(spec GraphSpec) error {
	if spec.ID == "" {
		return errors.New("agentgraph: id required")
	}
	if spec.YAML == "" {
		return errors.New("agentgraph: yaml is empty")
	}
	g, err := coreag.LoadYAML([]byte(spec.YAML))
	if err != nil {
		return fmt.Errorf("agentgraph: parse: %w", err)
	}
	if g.ID != spec.ID {
		return fmt.Errorf("agentgraph: yaml id %q != requested id %q", g.ID, spec.ID)
	}
	m.mu.RLock()
	_, isBundled := m.bundled[spec.ID]
	m.mu.RUnlock()
	if isBundled {
		return fmt.Errorf("agentgraph: id %q is a bundled library graph; choose a different id", spec.ID)
	}
	dir := m.userLibraryDir()
	if dir == "" {
		return errors.New("agentgraph: data dir not configured; cannot persist")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // application data dir
		return fmt.Errorf("agentgraph: mkdir: %w", err)
	}
	full := filepath.Join(dir, sanitizeGraphFilename(spec.ID))
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, []byte(spec.YAML), 0o644); err != nil { //nolint:gosec // user data
		return fmt.Errorf("agentgraph: write: %w", err)
	}
	return os.Rename(tmp, full)
}

// deleteGraph removes a user graph. Bundled ids are rejected so the
// API surface can't accidentally erase the default library.
func (m *Manager) deleteGraph(id string) error {
	if id == "" {
		return errors.New("agentgraph: id required")
	}
	m.mu.RLock()
	_, isBundled := m.bundled[id]
	m.mu.RUnlock()
	if isBundled {
		return fmt.Errorf("agentgraph: id %q is bundled; cannot delete", id)
	}
	dir := m.userLibraryDir()
	if dir == "" {
		return nil
	}
	full := filepath.Join(dir, sanitizeGraphFilename(id))
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agentgraph: remove: %w", err)
	}
	return nil
}

// validateYAML runs the kernel validator without persisting.
func (m *Manager) validateYAML(yaml string) ValidationResult {
	if strings.TrimSpace(yaml) == "" {
		return ValidationResult{OK: false, Issues: []ValidationIssue{{Rule: "schema", Message: "yaml is empty"}}}
	}
	g, err := coreag.LoadYAML([]byte(yaml))
	if err != nil {
		return ValidationResult{OK: false, Issues: []ValidationIssue{{Rule: "schema", Message: err.Error()}}}
	}
	verr := coreag.Validate(g, coreag.WithActivityRegistry(m.catalog))
	if verr == nil {
		return ValidationResult{OK: true, Issues: []ValidationIssue{}}
	}
	var ve *coreag.ValidationError
	if errors.As(verr, &ve) {
		issues := make([]ValidationIssue, 0, len(ve.Issues))
		for _, msg := range ve.Issues {
			rule, body := splitRule(msg)
			issues = append(issues, ValidationIssue{Rule: rule, Message: body})
		}
		return ValidationResult{OK: false, Issues: issues}
	}
	return ValidationResult{OK: false, Issues: []ValidationIssue{{Rule: "validate", Message: verr.Error()}}}
}

// startRun schedules a kernel run for the requested graph. The run
// executes asynchronously; status + trace are queryable via the API.
func (m *Manager) startRun(req StartRunRequest) (StartRunResponse, error) {
	if req.GraphID == "" {
		return StartRunResponse{}, errors.New("agentgraph: graphId required")
	}
	// LoadGraphSpec seeds the shared base constitution onto non-chat
	// graphs (WP05, system-prompt-grounding) — every graph started here
	// (toolloop_default plus any user-saved library graph) picks up the
	// base grounding without startRun re-implementing the composition.
	g, err := m.LoadGraphSpec(req.GraphID)
	if err != nil {
		return StartRunResponse{}, err
	}
	// The agentic-turn-routing launch gate, applied HERE as well as in
	// the chat chassis's GraphLoader (review finding N5). The Graphs
	// view's Run button executes chat_default like any other library
	// graph, so without this it ran the routed topology while the
	// user's lever said off — a flag with a hole in it is not a flag.
	// nil resolver = off, matching the shipped default and failing
	// closed for any Manager built without the settings seam.
	enabled := m.routingEnabled != nil && m.routingEnabled()
	g = coreag.GateAgenticTurnRouting(g, enabled)
	if err := coreag.Validate(g, coreag.WithActivityRegistry(m.catalog)); err != nil {
		return StartRunResponse{}, fmt.Errorf("agentgraph: validate: %w", err)
	}

	runID := newRunID()
	runCtx, cancel := context.WithCancel(context.Background())
	// Pre-initialise Counters + State so snapshotStatus can read them
	// concurrently with the worker without racing applyEnvDefaults.
	env := &coreag.Env{
		RunID:      runID,
		SessionID:  req.SessionID,
		Graph:      &g,
		Activities: m.catalog,
		Ask:        m.asks.bus(runID),
		Counters:   &coreag.RunCounters{WallclockStart: m.nowFn().UnixNano()},
		State:      coreag.NewRunState(),
	}
	// Production wiring: thread the chassis-supplied EnvDeps into the
	// per-run Env. Each non-nil dep replaces the corresponding nil-stub
	// the kernel's applyEnvDefaults would otherwise install. Tests that
	// don't wire deps fall through to the existing stubs.
	m.envDeps.applyTo(env)
	now := m.nowFn()
	entry := &runEntry{
		id:        runID,
		graphID:   req.GraphID,
		sessionID: req.SessionID,
		graph:     g,
		state:     RunStateRunning,
		startedAt: now,
		updatedAt: now,
		env:       env,
		cancel:    cancel,
	}
	m.mu.Lock()
	m.runs[runID] = entry
	m.mu.Unlock()

	go m.runWorker(runCtx, entry)

	status := m.snapshotStatus(entry)
	return StartRunResponse{RunID: runID, Status: status}, nil
}

// runWorker is the goroutine that calls Kernel.Run. On Pause it surfaces
// the pending ask or approval; on completion it records counters.
func (m *Manager) runWorker(ctx context.Context, entry *runEntry) {
	err := m.kernel.Run(ctx, entry.env)
	entry.mu.Lock()
	entry.endedAt = m.nowFn()
	entry.updatedAt = entry.endedAt
	var startApprovalTimeout bool
	switch {
	case errors.Is(err, coreag.ErrPaused):
		entry.state = RunStatePaused
		// Find the pending decision, if any (approval-node-01PMZC12
		// UNIT-3: ask and approval are mutually exclusive, discriminated
		// by the paused node's kind).
		entry.pendingAsk, entry.pendingApproval = m.asks.snapshotDecision(entry.id, entry.env.Graph)
		startApprovalTimeout = entry.pendingApproval != nil
	case err == nil:
		entry.state = RunStateCompleted
	default:
		entry.state = RunStateFailed
		entry.err = err
	}
	entry.mu.Unlock()
	if startApprovalTimeout {
		m.scheduleApprovalTimeout(entry)
	}
}

// resumeRun records the user's ask response + restarts the kernel.
func (m *Manager) resumeRun(runID, response string) error {
	m.mu.RLock()
	entry, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agentgraph: run %q not found", runID)
	}
	entry.mu.Lock()
	if entry.state != RunStatePaused {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q not paused (state=%s)", runID, entry.state)
	}
	if entry.pendingApproval != nil {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q has a pending approval, not an ask; use Graph_ResolveApproval", runID)
	}
	if entry.pendingAsk == nil {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q has no pending ask", runID)
	}
	nodeID := entry.pendingAsk.NodeID
	entry.pendingAsk = nil
	entry.state = RunStateRunning
	entry.updatedAt = m.nowFn()
	env := entry.env
	if env.State != nil {
		env.State.SetUserAnswer(response)
	}
	entry.mu.Unlock()

	// Inject answer + flip the ask bus state.
	m.asks.answer(runID, nodeID, response)

	runCtx, cancel := context.WithCancel(context.Background())
	entry.mu.Lock()
	entry.cancel = cancel
	entry.mu.Unlock()
	go m.resumeWorker(runCtx, entry)
	return nil
}

// resumeWorker calls Kernel.Resume; mirror of runWorker for the
// post-pause leg.
func (m *Manager) resumeWorker(ctx context.Context, entry *runEntry) {
	err := m.kernel.Resume(ctx, entry.env)
	entry.mu.Lock()
	entry.endedAt = m.nowFn()
	entry.updatedAt = entry.endedAt
	var startApprovalTimeout bool
	switch {
	case errors.Is(err, coreag.ErrPaused):
		entry.state = RunStatePaused
		entry.pendingAsk, entry.pendingApproval = m.asks.snapshotDecision(entry.id, entry.env.Graph)
		startApprovalTimeout = entry.pendingApproval != nil
	case err == nil:
		entry.state = RunStateCompleted
	default:
		entry.state = RunStateFailed
		entry.err = err
	}
	entry.mu.Unlock()
	if startApprovalTimeout {
		m.scheduleApprovalTimeout(entry)
	}
}

// scheduleApprovalTimeout arms the no-watcher fail-closed / auto-
// approve-window timer for entry's CURRENT pendingApproval
// (approval-node-01PMZC12 UNIT-4/UNIT-5). Must be called with
// entry.mu NOT held — it takes the lock itself, both to read the
// pending decision and again to install the timer.
//
// A positive auto_approve_window_seconds on the paused node overrides
// the deadline with a SHORTER auto-APPROVE window (UNIT-5, FR-007);
// otherwise the run falls back to m.approvalTimeout, which auto-
// REJECTS as the no-watcher safety property (UNIT-4, FR-006, G5).
// m.approvalTimeout == 0 disables the fallback (test-only escape
// hatch for isolating the auto_approve_window_seconds=0 "parks
// indefinitely" acceptance criterion, AC-06, from the no-watcher axis).
func (m *Manager) scheduleApprovalTimeout(entry *runEntry) {
	entry.mu.Lock()
	pa := entry.pendingApproval
	if pa == nil {
		entry.mu.Unlock()
		return
	}
	nodeID := pa.NodeID
	graph := entry.env.Graph
	entry.mu.Unlock()

	var windowSeconds int
	if graph != nil {
		for i := range graph.Nodes {
			if graph.Nodes[i].ID != nodeID {
				continue
			}
			if a, ok := graph.Nodes[i].Attrs.(coreag.ApprovalAttrs); ok {
				windowSeconds = a.AutoApproveWindowSeconds
			}
			break
		}
	}

	var (
		d       time.Duration
		verdict coreag.ApprovalVerdict
	)
	if windowSeconds > 0 {
		d = time.Duration(windowSeconds) * time.Second
		verdict = coreag.ApprovalVerdict{
			Approved: true,
			Auto:     true,
			Reason:   fmt.Sprintf("auto-approved after %ds window elapsed", windowSeconds),
		}
	} else {
		if m.approvalTimeout <= 0 {
			return
		}
		d = m.approvalTimeout
		verdict = coreag.ApprovalVerdict{
			Approved: false,
			Auto:     true,
			Reason:   fmt.Sprintf("no watcher: approval timed out after %s without a human resolution", d),
		}
	}

	runID := entry.id
	timer := time.AfterFunc(d, func() { m.timeoutApproval(runID, nodeID, verdict) })

	entry.mu.Lock()
	// Only arm if the same approval is still pending — a resolution
	// that landed between the unlock above and here would otherwise
	// leak a timer racing a decision that already happened.
	if entry.pendingApproval != nil && entry.pendingApproval.NodeID == nodeID {
		entry.approvalTimer = timer
	} else {
		timer.Stop()
	}
	entry.mu.Unlock()
}

// applyApprovalVerdict is the core state mutation for resolving a
// pending approval: injects the verdict onto AskBus, clears the
// pending decision + timer, flips the run back to running, and
// restarts the kernel. Shared by the human resolve-verb path
// (resolveApproval, Cedar-gated) and the no-watcher / auto-approve-
// window timeout path (timeoutApproval, which bypasses the gate —
// spec.md §5.3: a Cedar misconfiguration must not be able to block the
// system's own fail-closed backstop).
func (m *Manager) applyApprovalVerdict(runID, nodeID string, verdict coreag.ApprovalVerdict) error {
	m.mu.RLock()
	entry, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agentgraph: run %q not found", runID)
	}
	entry.mu.Lock()
	if entry.state != RunStatePaused {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q not paused (state=%s)", runID, entry.state)
	}
	if entry.pendingAsk != nil {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q has a pending ask, not an approval; use Graph_Resume", runID)
	}
	if entry.pendingApproval == nil {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q has no pending approval", runID)
	}
	if entry.pendingApproval.NodeID != nodeID {
		entry.mu.Unlock()
		return fmt.Errorf("agentgraph: run %q pending approval is node %q, not %q", runID, entry.pendingApproval.NodeID, nodeID)
	}
	entry.pendingApproval = nil
	if entry.approvalTimer != nil {
		entry.approvalTimer.Stop()
		entry.approvalTimer = nil
	}
	entry.state = RunStateRunning
	entry.updatedAt = m.nowFn()
	entry.mu.Unlock()

	ans, err := coreag.EncodeApprovalAnswer(verdict)
	if err != nil {
		return err
	}
	if err := m.asks.resolveApproval(runID, nodeID, ans); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	entry.mu.Lock()
	entry.cancel = cancel
	entry.mu.Unlock()
	go m.resumeWorker(runCtx, entry)
	return nil
}

// resolveApproval is the human-verb entry point behind
// Graph_ResolveApproval (approval-node-01PMZC12 UNIT-3, FR-003). The
// Cedar gate + audit emission bracket the mutation on the ALLOW side
// only: the gate is checked before anything changes (so a Deny is never
// observed as "already applied"), and the audit record is emitted only
// once the mutation actually landed.
func (m *Manager) resolveApproval(ctx context.Context, runID, nodeID string, approved bool, reason string) error {
	if err := cedar.CheckApprovalResolve(ctx, m.cedarGate, runID, nodeID, approved); err != nil {
		return err
	}
	verdict := coreag.ApprovalVerdict{
		Approved: approved,
		Reason:   reason,
		Auto:     false,
		Approver: "user",
	}
	if err := m.applyApprovalVerdict(runID, nodeID, verdict); err != nil {
		return err
	}
	m.auditApprovalResolved(ctx, runID, nodeID, verdict)
	return nil
}

// timeoutApproval is the no-watcher / auto-approve-window timer
// callback (approval-node-01PMZC12 UNIT-4/UNIT-5). Deliberately
// bypasses cedar.CheckApprovalResolve — see applyApprovalVerdict's doc
// comment — but is still audited.
func (m *Manager) timeoutApproval(runID, nodeID string, verdict coreag.ApprovalVerdict) {
	if err := m.applyApprovalVerdict(runID, nodeID, verdict); err != nil {
		// A human beat the timer, or the run moved on for some other
		// reason (cancelled, process state gone) — a lost race, not a
		// failure. Nothing landed, so nothing to audit.
		return
	}
	m.auditApprovalResolved(context.Background(), runID, nodeID, verdict)
}

// auditApprovalResolved emits audit.KindApprovalResolved. Best-effort:
// called only after applyApprovalVerdict already succeeded, so a
// broken emitter can drop the audit trail but never un-resolve a
// verdict that already landed.
func (m *Manager) auditApprovalResolved(ctx context.Context, runID, nodeID string, verdict coreag.ApprovalVerdict) {
	if m.auditEmitter == nil {
		return
	}
	_ = contextaudit.Emit(ctx, m.auditEmitter, contextaudit.KindApprovalResolved, contextaudit.ApprovalResolvedPayload{
		RunID:    runID,
		NodeID:   nodeID,
		Approved: verdict.Approved,
		Auto:     verdict.Auto,
		Approver: verdict.Approver,
		Reason:   verdict.Reason,
	}, m.nowFn())
}

// cancelRun signals a running run to stop.
func (m *Manager) cancelRun(runID string) error {
	m.mu.RLock()
	entry, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agentgraph: run %q not found", runID)
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.cancel != nil {
		entry.cancel()
	}
	if entry.approvalTimer != nil {
		entry.approvalTimer.Stop()
		entry.approvalTimer = nil
	}
	if entry.state == RunStateRunning || entry.state == RunStatePaused {
		entry.state = RunStateFailed
		entry.err = errors.New("cancelled")
		entry.endedAt = m.nowFn()
		entry.updatedAt = entry.endedAt
	}
	return nil
}

// runStatus snapshots a run's state for the API.
func (m *Manager) runStatus(runID string) (RunStatus, error) {
	m.mu.RLock()
	entry, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return RunStatus{}, fmt.Errorf("agentgraph: run %q not found", runID)
	}
	return m.snapshotStatus(entry), nil
}

func (m *Manager) snapshotStatus(entry *runEntry) RunStatus {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	out := RunStatus{
		RunID:     entry.id,
		GraphID:   entry.graphID,
		SessionID: entry.sessionID,
		State:     entry.state,
		StartedAt: entry.startedAt.Format(time.RFC3339Nano),
		UpdatedAt: entry.updatedAt.Format(time.RFC3339Nano),
	}
	if !entry.endedAt.IsZero() {
		out.CompletedAt = entry.endedAt.Format(time.RFC3339Nano)
	}
	if entry.err != nil {
		out.Error = entry.err.Error()
	}
	if entry.env != nil && entry.env.State != nil {
		out.NodesComplete = len(entry.env.State.AllCompleted())
	}
	if entry.env != nil && entry.env.Counters != nil {
		t, c, tools, cost := entry.env.Counters.Snapshot()
		out.LLMTokens = t
		out.LLMCalls = c
		out.ToolCalls = tools
		out.CostUSD = cost
	}
	if entry.pendingAsk != nil {
		// Defensive copy to avoid the caller mutating shared state.
		copy := *entry.pendingAsk
		out.PendingAsk = &copy
	}
	if entry.pendingApproval != nil {
		copy := *entry.pendingApproval
		out.PendingApproval = &copy
	}
	return out
}

// runTrace replays the EventLog tail for a run.
func (m *Manager) runTrace(runID string, since int64) ([]RunTraceEvent, error) {
	m.mu.RLock()
	_, ok := m.runs[runID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agentgraph: run %q not found", runID)
	}
	out := []RunTraceEvent{}
	err := m.log.Replay(runID, func(ev coreag.Event) error {
		if ev.Seq <= since {
			return nil
		}
		out = append(out, RunTraceEvent{
			Seq:       ev.Seq,
			RunID:     ev.RunID,
			NodeID:    ev.NodeID,
			Kind:      string(ev.Kind),
			Timestamp: ev.Timestamp.Format(time.RFC3339Nano),
			Payload:   string(ev.Payload),
		})
		return nil
	})
	return out, err
}

// TrackExternalRun records the RESOLVED spec a run executed under, for
// runs this Manager did not start itself — today that means every chat
// turn (agentgraph-total-convergence-01PMGX01 WP12).
//
// The chat runner builds its own coreag.Env and calls the shared kernel
// directly, so its events land in this Manager's EventLog while its
// spec does not land in `m.runs`. Materialization needs both: the log
// says what happened, the spec says what each node was configured to
// do, and the spec that matters is the resolved one (post routing-gate
// rewrite, post max-turns dial), not the library file on disk.
//
// The registry is bounded: a desktop session can run thousands of chat
// turns and a resolved chat_default is not small. Oldest entries are
// evicted once the cap is reached, so "materialize the turn I just ran"
// always works and "materialize a turn from two hours ago" degrades to
// the run_start graph_id fallback in materializeRun.
func (m *Manager) TrackExternalRun(runID string, g coreag.Graph) {
	if m == nil || runID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.externalRuns == nil {
		m.externalRuns = map[string]coreag.Graph{}
	}
	if _, exists := m.externalRuns[runID]; !exists {
		m.externalRunOrder = append(m.externalRunOrder, runID)
	}
	m.externalRuns[runID] = g
	for len(m.externalRunOrder) > maxTrackedExternalRuns {
		oldest := m.externalRunOrder[0]
		m.externalRunOrder = m.externalRunOrder[1:]
		delete(m.externalRuns, oldest)
	}
}

// materializeRun projects a finished (or in-flight) run's EventLog into
// a graph spec — the executed conversation, as a graph.
//
// It returns the same GraphSpec shape LoadGraph returns, so the existing
// editor renders it with no new wire type; `Scope` is "materialized",
// which is what puts the editor in read-only mode. A record of what
// happened must not be editable in place: saving it would write a
// synthetic id into the user library and make a claim about a run that
// the run no longer supports.
func (m *Manager) materializeRun(runID string) (GraphSpec, error) {
	if runID == "" {
		return GraphSpec{}, errors.New("agentgraph: run id required")
	}
	if m.log == nil {
		return GraphSpec{}, errors.New("agentgraph: no event log configured")
	}
	src, provenance, err := m.runSpecFor(runID)
	if err != nil {
		return GraphSpec{}, err
	}
	var opts []coreag.MaterializeOption
	if provenance != "" {
		opts = append(opts, coreag.WithSpecProvenance(provenance))
	}
	mg, err := coreag.MaterializeRun(src, runID, m.log, opts...)
	if err != nil {
		return GraphSpec{}, err
	}
	// Validating here rather than inside the projection keeps the
	// guarantee honest at the boundary that hands the spec to the
	// editor: what the frontend receives is a graph the loader accepts.
	if err := coreag.Validate(mg, coreag.WithActivityRegistry(m.catalog)); err != nil {
		return GraphSpec{}, fmt.Errorf("agentgraph: materialized run %q is not a valid graph: %w", runID, err)
	}
	out, err := coreag.DumpYAML(mg)
	if err != nil {
		return GraphSpec{}, fmt.Errorf("agentgraph: encode materialized run %q: %w", runID, err)
	}
	return GraphSpec{ID: mg.ID, Name: mg.Name, Scope: "materialized", YAML: string(out)}, nil
}

// runSpecFor resolves the spec a run executed, in decreasing order of
// fidelity, and REPORTS which tier answered (WP12 review finding F2).
//
//  1. a run this manager started — the exact resolved spec;
//  2. a run the chat runner registered — likewise exact;
//  3. the library graph named by the run's own run_start event.
//
// Tier 3 is a degraded answer, not an equivalent one: it recovers the
// topology after an eviction or a restart, but the routing gate rewrites
// chat_default in place and the max-turns dial overrides the loop cap
// before a run starts, so the library file can describe a DIFFERENT
// graph than the one that executed. Returning it unmarked would let a
// viewer read a routed run as a classic one and never know, which is why
// the second return value flows through to Graph.SpecProvenance and into
// the graph's own description.
func (m *Manager) runSpecFor(runID string) (coreag.Graph, string, error) {
	m.mu.RLock()
	entry, started := m.runs[runID]
	tracked, external := m.externalRuns[runID]
	m.mu.RUnlock()
	if started {
		entry.mu.Lock()
		g := entry.graph
		entry.mu.Unlock()
		if len(g.Nodes) > 0 {
			return g, "", nil
		}
	}
	if external && len(tracked.Nodes) > 0 {
		return tracked, "", nil
	}
	graphID := m.graphIDFromLog(runID)
	if graphID == "" {
		return coreag.Graph{}, "", fmt.Errorf("agentgraph: run %q not found", runID)
	}
	g, err := m.LoadGraphSpec(graphID)
	if err != nil {
		return coreag.Graph{}, "", fmt.Errorf("agentgraph: run %q references graph %q: %w", runID, graphID, err)
	}
	return g, coreag.SpecProvenanceLibraryFallback, nil
}

// graphIDFromLog reads the graph id off the run's run_start event.
func (m *Manager) graphIDFromLog(runID string) string {
	var out string
	_ = m.log.Replay(runID, func(ev coreag.Event) error {
		if ev.Kind != coreag.EventRunStart || out != "" {
			return nil
		}
		var p struct {
			GraphID string `json:"graph_id"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err == nil {
			out = p.GraphID
		}
		return nil
	})
	return out
}

// userLibraryDir returns the on-disk graph library path or "" when
// no DataDir was configured.
func (m *Manager) userLibraryDir() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "agent_graph", "library")
}

// askRouter is a multi-run AskBus dispatcher over ONE
// elicitation.Registry (agentgraph-total-convergence-01PMGX01 WP06).
//
// It used to own a map of per-run in-memory buses, each with its own
// pending / answers maps. There is now a single store — the same type
// the ask node, the kenaz__ask_user_question dialog and deferred asks
// all ride — keyed by elicitation.NodeAskID(runID, nodeID). runAskBus
// is a per-run *view* of that store, nothing more.
//
// Why this router keeps its own Registry instance rather than sharing
// the RPC chassis's elicit one: the two differ in *transport*, not in
// storage. A graph-run ask is answered through Resume(runID, answer),
// which also flips run state and restarts the kernel; a dialog ask is
// answered through Elicit_SubmitAnswer, which resolves a waiting
// goroutine. Pointing the dialog at a parked run would announce a
// question the dialog cannot actually un-park — a half-wired control,
// which this mission exists to delete rather than add. Unifying the
// transport is dynamic materialization's job (spec §4.5), where a tool
// call becomes an `ask` node and there is only one resume leg left.
type askRouter struct {
	registry *elicitation.Registry
}

func newAskRouter() *askRouter {
	return &askRouter{registry: elicitation.NewRegistry(elicitation.Config{})}
}

func (r *askRouter) bus(runID string) *runAskBus {
	return &runAskBus{runID: runID, registry: r.registry}
}

func (r *askRouter) answer(runID, nodeID, ans string) {
	r.bus(runID).answer(nodeID, ans)
}

// resolveApproval injects a structured verdict answer for nodeID
// (approval-node-01PMZC12 UNIT-3/UNIT-4). Mirrors answer() above but
// carries a full elicitation.Answer (JSON-typed Value) rather than
// plain text — a verdict needs more than a string (ApprovalVerdict's
// approved/reason/auto/approver fields).
func (r *askRouter) resolveApproval(runID, nodeID string, ans elicitation.Answer) error {
	id := elicitation.NodeAskID(runID, nodeID)
	if _, ok := r.registry.Get(id); !ok {
		if _, err := r.registry.Register(elicitation.Request{
			ID:     id,
			RunID:  runID,
			NodeID: nodeID,
		}); err != nil {
			return err
		}
	}
	return r.registry.Resolve(id, ans)
}

// snapshotDecision returns the run's pending decision as EXACTLY ONE of
// a *PendingAsk or a *PendingApproval — never both (the kernel parks on
// at most one node per pause cycle). Returns nil, nil if nothing is
// pending.
//
// The kind discriminator is the paused node's Kind in graph, not
// anything stored in the elicitation registry itself — a plain
// elicitation.KindRadio question is not proof of an approval node (a
// user-authored `ask` node can ask a two-option radio question too),
// so kind is read from the graph the run actually executed
// (approval-node-01PMZC12 UNIT-3, spec.md §5.2). graph == nil, or a
// lookup miss, falls back to treating the pause as an ask — the
// pre-UNIT-3 behaviour — rather than guessing.
func (r *askRouter) snapshotDecision(runID string, graph *coreag.Graph) (*PendingAsk, *PendingApproval) {
	pending := r.registry.ListPending(elicitation.Filter{RunID: runID})
	if len(pending) == 0 {
		return nil, nil
	}
	// Deterministic pick: map iteration inside ListPending is random and
	// a run with two parked decisions must not flip which one the UI
	// shows.
	first := pending[0]
	for _, e := range pending[1:] {
		if e.NodeID < first.NodeID {
			first = e
		}
	}

	if graph != nil {
		for i := range graph.Nodes {
			n := &graph.Nodes[i]
			if n.ID != first.NodeID {
				continue
			}
			if n.Kind != coreag.NodeKindApproval {
				break
			}
			a, ok := n.Attrs.(coreag.ApprovalAttrs)
			out := &PendingApproval{NodeID: first.NodeID, Prompt: first.Question.Text}
			if ok && a.ApproverRole != "" {
				out.ApproverRole = a.ApproverRole
			} else {
				out.ApproverRole = "user"
			}
			return nil, out
		}
	}

	out := &PendingAsk{NodeID: first.NodeID, Question: first.Question.Text}
	if first.Question.Kind != elicitation.KindFreeform {
		out.Kind = string(first.Question.Kind)
		out.Spec = &first.Question
	}
	return out, nil
}

// runAskBus satisfies coreag.AskBus for a single run.
type runAskBus struct {
	runID    string
	registry *elicitation.Registry
}

// Pending records that an Ask is waiting; satisfies coreag.AskBus.
func (b *runAskBus) Pending(_ context.Context, runID, nodeID string, q elicitation.Question) error {
	if runID == "" {
		runID = b.runID
	}
	id := elicitation.NodeAskID(runID, nodeID)
	if _, ok := b.registry.Get(id); ok {
		return nil
	}
	_, err := b.registry.Register(elicitation.Request{
		ID:       id,
		RunID:    runID,
		NodeID:   nodeID,
		Question: q,
	})
	return err
}

// LookupAnswer reads the recorded answer; satisfies coreag.AskBus.
func (b *runAskBus) LookupAnswer(_ context.Context, runID, nodeID string) (elicitation.Answer, bool) {
	if runID == "" {
		runID = b.runID
	}
	return b.registry.Answered(elicitation.NodeAskID(runID, nodeID))
}

// answer injects a user answer into the store, registering the ask
// first when the run has not parked one yet (the pre-seeded case).
func (b *runAskBus) answer(nodeID, ans string) {
	id := elicitation.NodeAskID(b.runID, nodeID)
	if _, ok := b.registry.Get(id); !ok {
		_, _ = b.registry.Register(elicitation.Request{
			ID:     id,
			RunID:  b.runID,
			NodeID: nodeID,
		})
	}
	_ = b.registry.Resolve(id, elicitation.TextAnswer(ans))
}

// Compile-time witness.
var _ coreag.AskBus = (*runAskBus)(nil)

// ---- helpers ----

// loadBundledLibrary registers the embedded toolloop_default.yaml as
// the only currently shipped library entry. As more graphs are added
// to the spec, expand this list.
func loadBundledLibrary(m *Manager) error {
	for id, src := range bundledLibrary {
		g, err := coreag.LoadYAML([]byte(src))
		if err != nil {
			return fmt.Errorf("agentgraph: bundled %s: %w", id, err)
		}
		m.bundled[id] = bundledGraph{graph: g, yaml: src}
	}
	return nil
}

// newRunID returns a 16-byte hex run id. Stable across processes (two
// Managers will not collide).
func newRunID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Defensive: fall back to a time-based id; the chance of a
		// collision in a single-user desktop app is negligible.
		t := time.Now().UnixNano()
		return fmt.Sprintf("run-%x", t)
	}
	return "run-" + hex.EncodeToString(b[:])
}

// sanitizeGraphFilename defangs an id for on-disk persistence: the id
// becomes the YAML filename, so we strip directory separators + the
// few characters Windows rejects in filenames.
func sanitizeGraphFilename(id string) string {
	out := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return r
	}, id)
	return out + ".yaml"
}

// splitRule pulls the leading "rule:" prefix the validator emits so
// the wire shape can render rule + message separately.
func splitRule(msg string) (rule, body string) {
	idx := strings.Index(msg, ":")
	if idx <= 0 {
		return "validate", msg
	}
	return msg[:idx], strings.TrimSpace(msg[idx+1:])
}
