package mlproducer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
)

// Gate is the recording half of the consent gate (spec §4). Recording()
// must be cheap and non-blocking — it is consulted on the tool-dispatch
// path; WP03's implementation answers from its ≤60 s cache. false means
// nothing is written. A nil Gate is closed.
type Gate interface {
	Recording() bool
}

// GateFunc adapts a func to Gate.
type GateFunc func() bool

// Recording implements Gate.
func (f GateFunc) Recording() bool { return f() }

// ParentLinker registers subagent child sessions against the session
// that spawned them (spec §12 A-3). core/rpc's subagent spawner calls
// LinkChild only when the spawn is attended — or when the parent is
// itself linked (a nested child), so chains always end at an attended
// root. *Recorder implements it.
type ParentLinker interface {
	LinkChild(childSessionID, parentSessionID string)
	IsLinked(sessionID string) bool
}

// Defaults.
const (
	DefaultQueueSize     = 1024
	DefaultUpsertEvery   = 60 * time.Second
	DefaultIdleAfter     = 7 * 24 * time.Hour
	DefaultSweepInterval = time.Hour
	maxLinkDepth         = 32
)

// Config wires a Recorder.
type Config struct {
	// Store is the durable outbox + task store. Required.
	Store *mlstore.Store
	// Hasher computes h(x). Required; a key error records nothing.
	Hasher *Hasher
	// Gate decides whether anything is written. nil = closed.
	Gate Gate
	// Servers classifies custom MCP servers. nil = none custom.
	Servers ServerClassifier
	// Workspace returns the process workspace root (Core.WorkspaceDir);
	// repo_root = h(it) at a task's first tool call (spec §12 A-1), and
	// relative tool paths resolve against it.
	Workspace func() string
	// Now is the clock (tests). nil = time.Now.
	Now func() time.Time
	// QueueSize bounds the in-memory work queue; a full queue drops (the
	// observer never blocks dispatch). 0 = DefaultQueueSize.
	QueueSize int
	// UpsertEvery is the task-upsert throttle. 0 = DefaultUpsertEvery.
	UpsertEvery time.Duration
	// IdleAfter completes an open task with no activity. 0 = DefaultIdleAfter.
	IdleAfter time.Duration
	// SweepInterval is how often the idle sweep runs. 0 =
	// DefaultSweepInterval; negative disables the ticker (SweepIdle can
	// still be called).
	SweepInterval time.Duration
	// GitBranch reads the workspace's current git branch name; only its
	// rune length ships (tasks.branch = "x"×len; WP07). nil =
	// ReadGitBranch.
	GitBranch func(workspace string) string
}

// Recorder turns agent tool calls, turn ends and session deletes into
// minimised outbox records (KindTable). It implements agentgraph.ToolCallObserver,
// the TurnEnded half of chat.TurnUsageObserver (TurnStarted / TurnFailed
// are no-ops so it satisfies that interface structurally), a
// session.DeleteObserver-shaped SessionDeleted, and ParentLinker.
//
// All writes happen on one worker goroutine; observer methods only
// enqueue. Close drains it.
type Recorder struct {
	cfg Config
	min minimiser

	queue chan func()
	done  chan struct{}
	stop  chan struct{}

	closeMu sync.RWMutex
	closed  bool

	// mu serialises the worker's store writes with Purge and guards tasks
	// and dirty.
	mu    sync.Mutex
	tasks map[string]*mlstore.TaskRow
	// dirty: cached tasks whose state moved since their last upsert (the
	// trailing-upsert candidates flushStale writes). The store's
	// last_active > last_upsert_at test covers tasks not in the cache
	// after a restart; this set covers same-millisecond activity.
	dirty map[string]bool

	linkMu sync.RWMutex
	parent map[string]string // child session -> parent session

	dropped atomic.Int64

	// excl is the newest org exclusion set (SetExclusions; WP05).
	excl atomic.Pointer[ExclusionSet]

	// WP07, guarded by mu (worker only): background bash spawns awaiting
	// their exit, exits that arrived before their spawn, and the
	// throttled branch read (background.go, branch.go).
	bgPending map[string]bgPending
	bgParked  map[string]bgParked
	branch    branchCache
}

var _ coreag.ToolCallObserver = (*Recorder)(nil)
var _ ParentLinker = (*Recorder)(nil)

// NewRecorder starts the recorder's worker. Call Close on shutdown.
func NewRecorder(cfg Config) *Recorder {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.UpsertEvery <= 0 {
		cfg.UpsertEvery = DefaultUpsertEvery
	}
	if cfg.IdleAfter <= 0 {
		cfg.IdleAfter = DefaultIdleAfter
	}
	if cfg.SweepInterval == 0 {
		cfg.SweepInterval = DefaultSweepInterval
	}
	r := &Recorder{
		cfg:    cfg,
		min:    minimiser{hasher: cfg.Hasher, servers: cfg.Servers},
		queue:  make(chan func(), cfg.QueueSize),
		done:   make(chan struct{}),
		stop:   make(chan struct{}),
		tasks:  map[string]*mlstore.TaskRow{},
		dirty:  map[string]bool{},
		parent: map[string]string{},

		bgPending: map[string]bgPending{},
		bgParked:  map[string]bgParked{},
	}
	go r.run()
	return r
}

func (r *Recorder) run() {
	defer close(r.done)
	var tick, upsertTick <-chan time.Time
	if r.cfg.SweepInterval > 0 {
		t := time.NewTicker(r.cfg.SweepInterval)
		defer t.Stop()
		tick = t.C
		// Trailing upserts ride the same switch: negative SweepInterval
		// disables both background tickers (tests drive them directly).
		u := time.NewTicker(r.cfg.UpsertEvery)
		defer u.Stop()
		upsertTick = u.C
	}
	for {
		select {
		case fn, ok := <-r.queue:
			if !ok {
				return
			}
			r.safe(fn)
		case <-tick:
			r.safe(func() { r.sweepIdle(context.Background()) })
		case <-upsertTick:
			r.safe(func() { r.flushStale(context.Background(), false) })
		}
	}
}

// safe runs one work item; a recorder bug is logged (no record content),
// never propagated.
func (r *Recorder) safe(fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			logging.L().Error("mlproducer.recorder.panic", "panic", fmt.Sprintf("%v", rec))
		}
	}()
	fn()
}

// enqueue never blocks: a full queue or a closed recorder drops.
func (r *Recorder) enqueue(fn func()) bool {
	r.closeMu.RLock()
	defer r.closeMu.RUnlock()
	if r.closed {
		return false
	}
	select {
	case r.queue <- fn:
		return true
	default:
		r.dropped.Add(1)
		return false
	}
}

// Dropped is how many work items a full queue has dropped.
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

// Flush blocks until every item enqueued before it has been processed
// (or ctx ends). Used by tests and WP03's drain.
func (r *Recorder) Flush(ctx context.Context) error {
	ack := make(chan struct{})
	r.closeMu.RLock()
	if r.closed {
		r.closeMu.RUnlock()
		return nil
	}
	select {
	case r.queue <- func() { close(ack) }:
	case <-ctx.Done():
		r.closeMu.RUnlock()
		return ctx.Err()
	}
	r.closeMu.RUnlock()
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting work, drains the queue and stops the worker.
func (r *Recorder) Close() {
	r.closeMu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.closeMu.Unlock()
	<-r.done
}

func (r *Recorder) recording() bool {
	return r.cfg.Gate != nil && r.cfg.Gate.Recording() && r.cfg.Store != nil && r.cfg.Hasher != nil
}

func (r *Recorder) nowMS() int64 { return r.cfg.Now().UnixMilli() }

// ---- ParentLinker ----

// LinkChild records child -> parent. See ParentLinker for when core/rpc
// may call it.
func (r *Recorder) LinkChild(childSessionID, parentSessionID string) {
	if r == nil || childSessionID == "" || parentSessionID == "" || childSessionID == parentSessionID {
		return
	}
	r.linkMu.Lock()
	r.parent[childSessionID] = parentSessionID
	r.linkMu.Unlock()
}

// IsLinked reports whether sessionID is a registered child.
func (r *Recorder) IsLinked(sessionID string) bool {
	if r == nil {
		return false
	}
	r.linkMu.RLock()
	defer r.linkMu.RUnlock()
	_, ok := r.parent[sessionID]
	return ok
}

// rootOf follows the parent chain to its top. linked is false when
// sessionID is not a registered child.
func (r *Recorder) rootOf(sessionID string) (root string, linked bool) {
	r.linkMu.RLock()
	defer r.linkMu.RUnlock()
	cur := sessionID
	for i := 0; i < maxLinkDepth; i++ {
		p, ok := r.parent[cur]
		if !ok {
			break
		}
		cur, linked = p, true
	}
	return cur, linked
}

// attributeTo decides whose task a call or turn counts toward (spec §12
// A-3): an attended session is its own root; an unattended one counts
// only when it is a linked subagent child, toward the chain's root.
func (r *Recorder) attributeTo(sessionID string, unattended bool) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	if !unattended {
		return sessionID, true
	}
	root, linked := r.rootOf(sessionID)
	if !linked {
		return "", false
	}
	return root, true
}

// ---- process markers ----

// ProcessEnv returns the markers every process the agent spawns carries
// (spec §1 rule 3, §12 A-4): KENAZ_ACTOR=agent always, plus
// KENAZ_SESSION=h(root session) when sessionID is known and the key
// loads. A subagent child's root is its attended root.
func (r *Recorder) ProcessEnv(sessionID string) []string {
	env := []string{"KENAZ_ACTOR=agent"}
	if r == nil || sessionID == "" || r.cfg.Hasher == nil {
		return env
	}
	root, _ := r.rootOf(sessionID)
	if hv, err := r.cfg.Hasher.H(root); err == nil {
		env = append(env, "KENAZ_SESSION="+hv)
	}
	return env
}

// EnvProvider adapts ProcessEnv to bash.Options.EnvProvider, given how to
// read the session id from a tool-dispatch ctx (toolloop's accessor).
func (r *Recorder) EnvProvider(sessionFromCtx func(context.Context) string) func(context.Context) []string {
	return func(ctx context.Context) []string {
		sid := ""
		if sessionFromCtx != nil && ctx != nil {
			sid = sessionFromCtx(ctx)
		}
		return r.ProcessEnv(sid)
	}
}

// ---- org exclusions (WP05) ----

// SetExclusions installs the org's newest exclusions (the gate calls it
// after every /me/ml read: GateConfig.OnExclusions). Every call observed
// after it is matched against s; see exclusions.go for the semantics.
func (r *Recorder) SetExclusions(s *ExclusionSet) {
	if r == nil {
		return
	}
	r.excl.Store(s)
}

// excludedBy reports whether either set excludes: the set current when the
// call was observed (seen) or the one current when it is handled. Checking
// both means a call is never recorded under looser lists than the ones in
// force at either moment.
func excludedBy(seen, now *ExclusionSet, match func(*ExclusionSet) bool) bool {
	return match(seen) || (now != seen && match(now))
}

// ---- observer entry points (enqueue only) ----

// ToolCallCompleted implements agentgraph.ToolCallObserver.
func (r *Recorder) ToolCallCompleted(rec coreag.ToolCallRecord) {
	if r == nil || !r.recording() {
		return
	}
	unattended := runposture.IsUnattended(rec.Ctx)
	at := r.nowMS()
	seen := r.excl.Load()
	r.enqueue(func() { r.handleTool(rec, unattended, at, seen) })
}

// TurnStarted is a no-op (chat.TurnUsageObserver shape).
func (r *Recorder) TurnStarted(context.Context, string, string) {}

// TurnFailed is a no-op (chat.TurnUsageObserver shape); the failure
// reaches TurnEnded as outcome "failed".
func (r *Recorder) TurnFailed(context.Context, string, string, bool) {}

// TurnEnded records agent.turn (chat.TurnUsageObserver.TurnEnded).
func (r *Recorder) TurnEnded(ctx context.Context, sessionID, outcome string, modelCalls, toolCalls int, dur time.Duration) {
	if r == nil || !r.recording() {
		return
	}
	unattended := runposture.IsUnattended(ctx)
	at := r.nowMS()
	r.enqueue(func() { r.handleTurn(sessionID, unattended, outcome, modelCalls, toolCalls, dur, at) })
}

// SessionDeleted completes the session's task (session.DeleteObserver
// shape). Never fails the delete.
func (r *Recorder) SessionDeleted(_ context.Context, sessionID string) error {
	if r == nil {
		return nil
	}
	at := r.nowMS()
	r.enqueue(func() { r.handleDelete(sessionID, at) })
	return nil
}

// SweepIdle runs the 7-day idle sweep now and waits for it.
func (r *Recorder) SweepIdle(ctx context.Context) error {
	ack := make(chan struct{})
	if !r.enqueue(func() { r.sweepIdle(ctx); close(ack) }) {
		return fmt.Errorf("mlproducer: recorder closed or queue full")
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FlushTasks writes a trailing task upsert now, ignoring the 60 s
// throttle, for every task whose newest state has not been upserted, and
// waits. The shutdown drain calls it so a session's final counters reach
// the outbox before the last ship.
func (r *Recorder) FlushTasks(ctx context.Context) error {
	if r == nil {
		return nil
	}
	ack := make(chan struct{})
	if !r.enqueue(func() { r.flushStale(ctx, true); close(ack) }) {
		return fmt.Errorf("mlproducer: recorder closed or queue full")
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flushStale is the trailing half of the upsert throttle (spec §3.1:
// "at most once per 60 s while active"): the per-call throttle only
// upserts when a call lands ≥ 60 s after the last upsert, so a session
// that went quiet inside that window would otherwise leave its final
// counters unshipped until completion (delete or the 7-day sweep). Run on
// a UpsertEvery ticker, it upserts every task whose state moved since its
// last upsert and whose last upsert is at least UpsertEvery old; force
// ignores the age.
func (r *Recorder) flushStale(ctx context.Context, force bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording() {
		return
	}
	cutoff := r.nowMS() - r.cfg.UpsertEvery.Milliseconds()
	if force {
		cutoff = math.MaxInt64
	}
	rows, err := r.cfg.Store.StaleTasks(ctx, cutoff)
	if err != nil {
		logging.L().Warn("mlproducer.recorder.stale_tasks_failed", "err", err.Error())
		return
	}
	due := map[string]mlstore.TaskRow{}
	for _, t := range rows {
		due[t.TaskID] = t
	}
	for id := range r.dirty {
		if t, ok := r.tasks[id]; ok && t.LastUpsertAt <= cutoff {
			due[id] = *t
		}
	}
	ids := make([]string, 0, len(due))
	for id := range due {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		task := cloneTask(due[id])
		r.commit(ctx, &task, nil, true)
	}
}

// Purge wipes the outbox and the task table and forgets cached task state
// (spec §4: on effective true→false). Seqs are never reused afterwards.
func (r *Recorder) Purge(ctx context.Context) error {
	if r == nil || r.cfg.Store == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = map[string]*mlstore.TaskRow{}
	r.dirty = map[string]bool{}
	// A background exit never ships across a purge.
	r.bgPending = map[string]bgPending{}
	r.bgParked = map[string]bgParked{}
	return r.cfg.Store.Purge(ctx)
}

// ---- worker ----

func (r *Recorder) taskID(root string) (string, string, bool) {
	sh := r.min.h(root)
	if sh == "" {
		return "", "", false
	}
	return TaskIDPrefix + sh, sh, true
}

// loadTask returns the cached / persisted task for root, or nil.
func (r *Recorder) loadTask(ctx context.Context, id string) *mlstore.TaskRow {
	if t, ok := r.tasks[id]; ok {
		return t
	}
	t, ok, err := r.cfg.Store.LoadTask(ctx, id)
	if err != nil {
		logging.L().Warn("mlproducer.recorder.load_task_failed", "err", err.Error())
		return nil
	}
	if !ok {
		return nil
	}
	r.tasks[id] = &t
	return &t
}

func (r *Recorder) handleTool(rec coreag.ToolCallRecord, unattended bool, at int64, seen *ExclusionSet) {
	root, ok := r.attributeTo(rec.SessionID, unattended)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording() {
		return
	}
	ctx := context.Background()
	id, sessionHash, ok := r.taskID(root)
	if !ok {
		return
	}
	workspace := ""
	if r.cfg.Workspace != nil {
		workspace = r.cfg.Workspace()
	}
	prev := r.loadTask(ctx, id)
	var task mlstore.TaskRow
	if prev != nil {
		task = cloneTask(*prev)
	} else {
		task = mlstore.TaskRow{
			TaskID: id, SessionHash: sessionHash, Phase: string(PhaseIdle),
			Files: map[string]int{}, StartedAt: at,
		}
		if workspace != "" {
			task.RepoRootHash = r.min.h(workspace)
		}
	}
	task.CompletedAt = 0 // activity reopens an idle-swept task

	outcome := rec.Outcome
	bashDidNotRun := false
	if outcome == "" {
		outcome = coreag.ToolOutcomeError
	}
	if outcome == coreag.ToolOutcomeOK && isFSBuiltin(rec.ToolName) && resultFlagsError(rec.ResultContent) {
		outcome = coreag.ToolOutcomeError
	}
	if outcome == coreag.ToolOutcomeOK && rec.ToolName == toolBash {
		// bash's own gate refusals and its pre-run failures come back as a
		// successful dispatch with exit_code -1; the explicit markers say
		// no process ran, so they are not `terminal` events (spec §12
		// A-10) and count toward nothing.
		switch refused, notRun := bashNotRun(rec.ResultContent); {
		case refused:
			outcome = coreag.ToolOutcomeDenied
		case notRun:
			outcome = coreag.ToolOutcomeError
			bashDidNotRun = true
		}
	}
	ran := (outcome == coreag.ToolOutcomeOK || outcome == coreag.ToolOutcomeError) && !bashDidNotRun

	args := parseArgs(rec.RawArgs)
	type draft struct {
		kind    string
		payload map[string]any
	}
	var drafts []draft

	// Org exclusions (WP05; spec §12 A-11): matched on the ABSOLUTE path
	// before it is hashed and on the FULL command line before it is
	// truncated. A match records the call only as agent.tool (tool,
	// outcome, dur_ms): no file / terminal / commit / phase_change event,
	// no files key, no test or commit counters.
	now := r.excl.Load()
	excluded := false
	rawPath, abs := "", ""
	switch {
	case isFileWrite(rec.ToolName) && outcome == coreag.ToolOutcomeOK:
		rawPath = stringArg(args, "path")
		abs = absPath(rawPath, workspace)
		excluded = excludedBy(seen, now, func(s *ExclusionSet) bool {
			return s.MatchPath(abs) || s.MatchPath(rawPath)
		})
	case rec.ToolName == toolBash && ran:
		full := stringArg(args, "command")
		// The cwd relative path tokens resolve against: the call's
		// working_dir (itself relative to the workspace), else the
		// workspace.
		cwd := absPath(stringArg(args, "working_dir"), workspace)
		if cwd == "" {
			cwd = workspace
		}
		excluded = excludedBy(seen, now, func(s *ExclusionSet) bool {
			return s.MatchCommand(full) || s.MatchCommandPaths(full, cwd)
		})
	}

	// Spec §12 A-10: the call's own event is file (a write that
	// succeeded), terminal (bash that ran), or agent.tool (everything
	// else, including denied / cancelled writes and bash, and every
	// excluded call).
	cmd := ""
	exitCode, exitKnown := 0, false
	var bgSpawn *bgPending
	bgSpawnID := ""
	if excluded && rec.ToolName == toolBash {
		// An excluded background spawn leaves an entry that swallows its
		// exit: no follow-up terminal (WP07).
		if bg, _ := args["run_in_background"].(bool); bg {
			if tid := bashBackgroundTaskID(rec.ResultContent); tid != "" {
				bgSpawn, bgSpawnID = &bgPending{at: at, excluded: true}, tid
			}
		}
	}
	switch {
	case excluded:
		drafts = append(drafts, draft{KindTool, map[string]any{
			"task":    id,
			"tool":    r.min.toolName(rec.ToolName),
			"outcome": string(outcome),
			"dur_ms":  rec.Duration.Milliseconds(),
		}})
	case isFileWrite(rec.ToolName) && outcome == coreag.ToolOutcomeOK:
		payload := map[string]any{"task": id}
		if abs != "" {
			tok := r.min.pathToken(abs)
			payload["path"] = tok
			payload["file"] = tok
			task.Files[tok]++
		}
		drafts = append(drafts, draft{KindFile, payload})
	case rec.ToolName == toolBash && ran:
		cmd = stringArg(args, "command")
		payload := map[string]any{"task": id, "cmd": r.min.cmdPrefix(cmd)}
		background, _ := args["run_in_background"].(bool)
		bgTaskID := ""
		if background {
			bgTaskID = bashBackgroundTaskID(rec.ResultContent)
		}
		if bgTaskID == "" {
			// Foreground, or a background job that exited before the spawn
			// returned (bash then reports its exit_code inline, no task id).
			exitCode, exitKnown = bashExitCode(rec.ResultContent)
			if exitKnown {
				payload["exit_code"] = exitCode
			}
		} else {
			// Background bash records at spawn with no exit_code (spec §12
			// A-5); its exit arrives later via BackgroundEnded, which ships
			// the follow-up terminal from what is remembered here (WP07).
			bgSpawn = &bgPending{at: at, taskID: id, cmd: r.min.cmdPrefix(cmd),
				isTest: isTestCommand(cmd), isCommit: gitSubcommand(cmd) == "commit"}
			bgSpawnID = bgTaskID
		}
		if outcome == coreag.ToolOutcomeOK && isTestCommand(cmd) {
			task.TestRuns++
			if exitKnown && exitCode != 0 {
				task.TestFails++
			}
		}
		drafts = append(drafts, draft{KindTerminal, payload})
	default:
		drafts = append(drafts, draft{KindTool, map[string]any{
			"task":    id,
			"tool":    r.min.toolName(rec.ToolName),
			"outcome": string(outcome),
			"dur_ms":  rec.Duration.Milliseconds(),
		}})
	}

	// WP07 item 1: a successful read ships one `file` event per file it
	// touched (reads.go), in addition to its agent.tool row above. Each
	// path is matched against the org exclusions before it is hashed.
	// Reads never touch task.Files (writes only).
	if outcome == coreag.ToolOutcomeOK && isReadTool(rec.ToolName) {
		for _, c := range readPaths(rec.ToolName, args, rec.ResultContent, workspace) {
			c := c
			if excludedBy(seen, now, func(s *ExclusionSet) bool { return s.MatchPath(c.abs) || s.MatchPath(c.raw) }) {
				continue
			}
			tok := r.min.pathToken(c.abs)
			drafts = append(drafts, draft{KindFile, map[string]any{"task": id, "path": tok, "file": tok}})
		}
	}

	if ran && !excluded {
		if p, ok := inferPhase(rec.ToolName, cmd); ok && string(p) != task.Phase {
			task.Phase = string(p)
			drafts = append(drafts, draft{KindPhaseChange, map[string]any{"task": id, "phase": string(p)}})
		}
	}
	if !excluded && outcome == coreag.ToolOutcomeOK && exitKnown && exitCode == 0 && gitSubcommand(cmd) == "commit" {
		task.CommitCount++
		drafts = append(drafts, draft{KindCommit, map[string]any{"task": id}})
	}
	task.LastActive = at

	events := make([]mlstore.EventDraft, 0, len(drafts))
	for _, d := range drafts {
		d := d
		events = append(events, mlstore.EventDraft{CreatedAt: at, Body: func(seq int64) ([]byte, error) {
			return encodeEvent(seq, d.kind, at, d.payload)
		}})
	}
	r.commit(ctx, &task, events, prev == nil || at-task.LastUpsertAt >= r.cfg.UpsertEvery.Milliseconds())
	if bgSpawn != nil {
		r.rememberSpawn(ctx, bgSpawnID, *bgSpawn)
	}
}

func (r *Recorder) handleTurn(sessionID string, unattended bool, outcome string, modelCalls, toolCalls int, dur time.Duration, at int64) {
	root, ok := r.attributeTo(sessionID, unattended)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording() {
		return
	}
	ctx := context.Background()
	id, _, ok := r.taskID(root)
	if !ok {
		return
	}
	prev := r.loadTask(ctx, id)
	if prev == nil {
		// A task exists only once the agent made a tool call (spec §12
		// A-1); a chat turn that never called a tool records nothing.
		return
	}
	task := cloneTask(*prev)
	task.CompletedAt = 0
	switch outcome {
	case "completed", "stopped", "failed":
	default:
		outcome = "failed"
	}
	var events []mlstore.EventDraft
	add := func(kind string, payload map[string]any) {
		events = append(events, mlstore.EventDraft{CreatedAt: at, Body: func(seq int64) ([]byte, error) {
			return encodeEvent(seq, kind, at, payload)
		}})
	}
	// A root turn with no tool call is idle (§3.3). A child's turn does
	// not idle the root: the parent may still be working.
	if toolCalls == 0 && sessionID == root && task.Phase != string(PhaseIdle) {
		task.Phase = string(PhaseIdle)
		add(KindPhaseChange, map[string]any{"task": id, "phase": string(PhaseIdle)})
	}
	add(KindTurn, map[string]any{
		"task":        id,
		"dur_ms":      dur.Milliseconds(),
		"model_calls": modelCalls,
		"tool_calls":  toolCalls,
		"outcome":     outcome,
	})
	task.LastActive = at
	r.commit(ctx, &task, events, at-task.LastUpsertAt >= r.cfg.UpsertEvery.Milliseconds())
}

func (r *Recorder) handleDelete(sessionID string, at int64) {
	r.linkMu.Lock()
	delete(r.parent, sessionID)
	r.linkMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording() {
		return
	}
	ctx := context.Background()
	id, _, ok := r.taskID(sessionID)
	if !ok {
		return
	}
	prev := r.loadTask(ctx, id)
	if prev == nil || prev.CompletedAt != 0 {
		return
	}
	task := cloneTask(*prev)
	task.CompletedAt = at
	if task.LastActive > at {
		task.CompletedAt = task.LastActive
	}
	r.commit(ctx, &task, nil, true)
	delete(r.tasks, id)
}

func (r *Recorder) sweepIdle(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording() {
		return
	}
	cutoff := r.nowMS() - r.cfg.IdleAfter.Milliseconds()
	rows, err := r.cfg.Store.OpenTasksIdleSince(ctx, cutoff)
	if err != nil {
		logging.L().Warn("mlproducer.recorder.sweep_failed", "err", err.Error())
		return
	}
	for _, t := range rows {
		task := cloneTask(t)
		// The task ended when the agent last did something, not when the
		// sweep noticed: completed_at = last_active keeps `duration` true.
		task.CompletedAt = task.LastActive
		r.commit(ctx, &task, nil, true)
		delete(r.tasks, task.TaskID)
	}
}

// commit writes events + the task (+ an upsert record when due) in one
// transaction and updates the cache. Caller holds r.mu.
func (r *Recorder) commit(ctx context.Context, task *mlstore.TaskRow, events []mlstore.EventDraft, upsert bool) {
	w := mlstore.Write{Events: events, Task: task}
	if upsert {
		task.LastUpsertAt = task.LastActive
		body, err := json.Marshal(taskWireBody(*task, r.currentBranch()))
		if err != nil {
			logging.L().Warn("mlproducer.recorder.task_encode_failed", "err", err.Error())
			return
		}
		w.TaskUpsert = body
	}
	if _, err := r.cfg.Store.Commit(ctx, w); err != nil {
		logging.L().Warn("mlproducer.recorder.commit_failed", "err", err.Error())
		return
	}
	cp := cloneTask(*task)
	r.tasks[task.TaskID] = &cp
	if upsert {
		delete(r.dirty, task.TaskID)
	} else {
		r.dirty[task.TaskID] = true
	}
}

// currentBranch is the length-only placeholder of the workspace's current
// branch (WP07; branch.go), read at most once per branchEvery. Caller
// holds r.mu.
func (r *Recorder) currentBranch() string {
	workspace := ""
	if r.cfg.Workspace != nil {
		workspace = r.cfg.Workspace()
	}
	return r.branchFor(workspace)
}

// taskWireBody encodes a task upsert; branch is already the placeholder.
func taskWireBody(t mlstore.TaskRow, branch string) taskBody {
	files := t.Files
	if files == nil {
		files = map[string]int{}
	}
	b := taskBody{
		ID: t.TaskID, RepoRoot: t.RepoRootHash, Branch: branch, Phase: t.Phase, Files: files,
		StartedAt: t.StartedAt, LastActive: t.LastActive,
		CommitCount: t.CommitCount, TestRuns: t.TestRuns, TestFails: t.TestFails,
	}
	if t.CompletedAt != 0 {
		c := t.CompletedAt
		b.CompletedAt = &c
	}
	return b
}

func cloneTask(t mlstore.TaskRow) mlstore.TaskRow {
	files := make(map[string]int, len(t.Files))
	for k, v := range t.Files {
		files[k] = v
	}
	t.Files = files
	return t
}
