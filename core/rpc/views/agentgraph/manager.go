package agentgraph

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

	// Library: bundled graphs the loader splices in (read-only).
	bundled map[string]bundledGraph

	// In-memory run registry keyed by run id.
	runs map[string]*runEntry

	// asks routes AskNode pending → answers per run + node.
	asks *askRouter

	// nowFn allows tests to pin timestamps.
	nowFn func() time.Time
}

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
}

// NewManager constructs a Manager populated with the bundled library
// (toolloop_default.yaml) plus the bundled activity catalog.
func NewManager(opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		bundled: map[string]bundledGraph{},
		runs:    map[string]*runEntry{},
		asks:    newAskRouter(),
		nowFn:   func() time.Time { return time.Now().UTC() },
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
// the AskNode pending question; on completion it records counters.
func (m *Manager) runWorker(ctx context.Context, entry *runEntry) {
	err := m.kernel.Run(ctx, entry.env)
	entry.mu.Lock()
	entry.endedAt = m.nowFn()
	entry.updatedAt = entry.endedAt
	switch {
	case errors.Is(err, coreag.ErrPaused):
		entry.state = RunStatePaused
		// Find the pending ask, if any.
		entry.pendingAsk = m.asks.snapshot(entry.id, entry.env.Graph)
	case err == nil:
		entry.state = RunStateCompleted
	default:
		entry.state = RunStateFailed
		entry.err = err
	}
	entry.mu.Unlock()
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
	switch {
	case errors.Is(err, coreag.ErrPaused):
		entry.state = RunStatePaused
		entry.pendingAsk = m.asks.snapshot(entry.id, entry.env.Graph)
	case err == nil:
		entry.state = RunStateCompleted
	default:
		entry.state = RunStateFailed
		entry.err = err
	}
	entry.mu.Unlock()
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

// userLibraryDir returns the on-disk graph library path or "" when
// no DataDir was configured.
func (m *Manager) userLibraryDir() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "agent_graph", "library")
}

// askRouter is a multi-run AskBus dispatcher. It bridges per-run
// memAskBus instances onto the kernel's seam without exporting the
// kernel's unexported memAskBus struct.
type askRouter struct {
	mu    sync.Mutex
	buses map[string]*runAskBus
}

func newAskRouter() *askRouter {
	return &askRouter{buses: map[string]*runAskBus{}}
}

func (r *askRouter) bus(runID string) *runAskBus {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buses[runID]
	if !ok {
		b = newRunAskBus(runID)
		r.buses[runID] = b
	}
	return b
}

func (r *askRouter) answer(runID, nodeID, ans string) {
	r.mu.Lock()
	b, ok := r.buses[runID]
	r.mu.Unlock()
	if !ok {
		return
	}
	b.answer(nodeID, ans)
}

// snapshot returns a PendingAsk that corresponds to the run's first
// pending question (the kernel currently parks on at most one Ask per
// pause cycle). Returns nil if no question is pending.
func (r *askRouter) snapshot(runID string, _ *coreag.Graph) *PendingAsk {
	r.mu.Lock()
	b, ok := r.buses[runID]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	nodeID, q, ok := b.firstPending()
	if !ok {
		return nil
	}
	return &PendingAsk{NodeID: nodeID, Question: q}
}

// runAskBus satisfies coreag.AskBus for a single run.
type runAskBus struct {
	runID string

	mu      sync.Mutex
	pending map[string]string // nodeID -> question
	answers map[string]string // nodeID -> answer
}

func newRunAskBus(runID string) *runAskBus {
	return &runAskBus{
		runID:   runID,
		pending: map[string]string{},
		answers: map[string]string{},
	}
}

// Pending records that an Ask is waiting; satisfies coreag.AskBus.
func (b *runAskBus) Pending(_ context.Context, _, nodeID, question string) error {
	b.mu.Lock()
	b.pending[nodeID] = question
	b.mu.Unlock()
	return nil
}

// LookupAnswer reads the recorded answer; satisfies coreag.AskBus.
func (b *runAskBus) LookupAnswer(_ context.Context, _, nodeID string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.answers[nodeID]
	return a, ok
}

// answer injects a user answer into the bus.
func (b *runAskBus) answer(nodeID, ans string) {
	b.mu.Lock()
	b.answers[nodeID] = ans
	delete(b.pending, nodeID)
	b.mu.Unlock()
}

// firstPending returns one pending (nodeID, question) tuple. The map
// iteration order is non-deterministic; callers wanting stable output
// should drive a single-Ask graph.
func (b *runAskBus) firstPending() (string, string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for nodeID, q := range b.pending {
		return nodeID, q, true
	}
	return "", "", false
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
