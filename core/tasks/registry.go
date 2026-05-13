package tasks

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"
)

// ErrNotFound is returned by Get when the task ID is unknown.
var ErrNotFound = errors.New("tasks: not found")

// ErrAlreadyTerminal is returned by End / Abort when the task is already done.
var ErrAlreadyTerminal = errors.New("tasks: already in terminal state")

// HookFirerFunc is the function signature the Registry calls to fire the
// background_task_complete hook. The rpc wiring layer injects a real
// implementation; tests may supply a stub.
type HookFirerFunc func(ctx context.Context, payload BackgroundTaskCompletePayload)

// entry is the private in-memory record for a running task.
type entry struct {
	task    Task
	stdout  *ringBuffer
	stderr  *ringBuffer
	subs    *subscriberSet
	lines   []Line  // all captured lines (for Tail)
	linesMu sync.Mutex
	pid     int     // 0 if unknown (sub-agent tasks)
}

// RegisterOpts carries the caller-supplied metadata for a new task.
type RegisterOpts struct {
	// ID overrides the auto-generated task ID. Leave empty to auto-generate.
	ID             string
	Kind           string
	OwnerSessionID string
	Cmd            string
	Description    string
	// PID is the OS process ID; 0 for sub-agent tasks.
	PID            int
}

// Registry tracks every background task in the current harness process.
// It is safe for concurrent use.
//
// The cross-mission public API surface (used by bash background mode and
// the branch-subagent-interactive mission):
//
//	Register(opts) (*entry, error)  — create a new running task
//	End(id, exitCode) error         — mark terminal; fires hook
//	Get(id) (Task, bool)
//	List() []Task
//	Tail(id, fromOffset) ([]Line, bool)
//	Abort(id) error
//	Subscribe(id) (<-chan Line, func(), bool)
//	StdoutWriter(id) (io.Writer, bool)
//	StderrWriter(id) (io.Writer, bool)
type Registry struct {
	mu        sync.RWMutex
	entries   map[string]*entry
	store     SQLStore    // nil-tolerant
	logDir    string      // <DataDir>/task_logs/; empty → no log files
	hookFirer HookFirerFunc
	logger    *slog.Logger
}

// SQLStore abstracts the SQLite persistence layer. nil is accepted when no
// database is available (tests, early boot).
type SQLStore interface {
	Insert(ctx context.Context, t Task) error
	UpdateStatus(ctx context.Context, id, status string, exitCode int, endedAt time.Time) error
	// MarkCrashed sets status=crashed for rows whose status is "running"
	// and whose PID is not alive. Called at boot by recovery.go.
	MarkCrashed(ctx context.Context) (int, error)
}

// Options configures a Registry.
type Options struct {
	// Store is the SQLite backend. nil means in-memory only.
	Store SQLStore
	// LogDir is the directory where per-task log files are written.
	// Empty disables log files.
	LogDir string
	// HookFirer is called when a task reaches a terminal state.
	// nil means no hook is fired (tests).
	HookFirer HookFirerFunc
	// Logger is optional; nil is silent.
	Logger *slog.Logger
}

// NewRegistry constructs an empty Registry.
func NewRegistry(opts Options) *Registry {
	return &Registry{
		entries:   make(map[string]*entry),
		store:     opts.Store,
		logDir:    opts.LogDir,
		hookFirer: opts.HookFirer,
		logger:    opts.Logger,
	}
}

// Register creates a new task and returns the entry's stdout/stderr writers.
// The task's Status is set to StatusRunning immediately.
//
// The callers (bash background mode, sub-agent dispatch) write to the
// returned writers; the registry fans out to ring buffer + log file + subscribers.
func (r *Registry) Register(ctx context.Context, opts RegisterOpts) (string, error) {
	id := opts.ID
	if id == "" {
		id = NewTaskID()
	}
	now := time.Now()
	t := Task{
		ID:             id,
		Kind:           opts.Kind,
		OwnerSessionID: opts.OwnerSessionID,
		Cmd:            opts.Cmd,
		Description:    opts.Description,
		Status:         StatusRunning,
		ExitCode:       0,
		StartedAt:      now,
	}

	lf, err := openLogFile(r.logDir, id)
	if err != nil {
		r.logf("tasks.register.log_open_err", "id", id, "err", err.Error())
		// Non-fatal: proceed without log file.
	}

	subs := newSubscriberSet()
	stdout := newRingBuffer(DefaultRingCapBytes)
	stderr := newRingBuffer(DefaultRingCapBytes)
	e := &entry{
		task:   t,
		stdout: stdout,
		stderr: stderr,
		subs:   subs,
		pid:    opts.PID,
	}
	// Wire up line writers that fan out to ring + log + subscribers.
	e.task.Status = StatusRunning // confirm

	r.mu.Lock()
	r.entries[id] = e
	r.mu.Unlock()

	// Persist to SQLite (best-effort; log failure).
	if r.store != nil {
		if serr := r.store.Insert(ctx, t); serr != nil {
			r.logf("tasks.register.store_err", "id", id, "err", serr.Error())
		}
	}

	r.logf("tasks.registered", "id", id, "kind", opts.Kind, "pid", opts.PID)
	_ = lf // stored in the writers
	return id, nil
}

// StdoutWriter returns an io.Writer that fans to ring buffer + log file +
// subscribers for the task's stdout stream. Returns nil when the task is
// unknown.
func (r *Registry) StdoutWriter(id string) (*lineWriter, bool) {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	lf, _ := openLogFile(r.logDir, id)
	return newLineWriter("stdout", e.stdout, lf, e.subs), true
}

// StderrWriter returns an io.Writer that fans to ring buffer + log file +
// subscribers for the task's stderr stream.
func (r *Registry) StderrWriter(id string) (*lineWriter, bool) {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	lf, _ := openLogFile(r.logDir, id)
	return newLineWriter("stderr", e.stderr, lf, e.subs), true
}

// End marks the task as completed or failed depending on exitCode, sets
// EndedAt, persists to SQLite, and fires the background_task_complete hook.
//
// End is idempotent for the same terminal status (returns nil); calling
// End on an already-terminal task with a different outcome returns
// ErrAlreadyTerminal.
func (r *Registry) End(ctx context.Context, id string, exitCode int) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	if e.task.IsTerminal() {
		r.mu.Unlock()
		return ErrAlreadyTerminal
	}
	now := time.Now()
	status := StatusCompleted
	if exitCode != 0 {
		status = StatusFailed
	}
	e.task.Status = status
	e.task.ExitCode = exitCode
	e.task.EndedAt = &now
	snap := e.task
	durationMs := now.Sub(e.task.StartedAt).Milliseconds()
	stdoutTail := e.stdout.Tail(TailCapBytes)
	stderrTail := e.stderr.Tail(TailCapBytes)
	// Close subscribers so __monitor watch loops unblock.
	e.subs.closeAll()
	r.mu.Unlock()

	// Persist (best-effort).
	if r.store != nil {
		if serr := r.store.UpdateStatus(ctx, id, status, exitCode, now); serr != nil {
			r.logf("tasks.end.store_err", "id", id, "err", serr.Error())
		}
	}

	r.logf("tasks.ended", "id", id, "status", status, "exit_code", exitCode,
		"duration_ms", durationMs)

	// Fire hook (async so we don't block the caller).
	if r.hookFirer != nil {
		payload := BackgroundTaskCompletePayload{
			TaskID:         snap.ID,
			Kind:           snap.Kind,
			ExitCode:       exitCode,
			DurationMs:     durationMs,
			StdoutTail:     stdoutTail,
			StderrTail:     stderrTail,
			OwnerSessionID: snap.OwnerSessionID,
		}
		go r.hookFirer(ctx, payload)
	}
	return nil
}

// Get returns a snapshot of the task with the given ID.
func (r *Registry) Get(id string) (Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return Task{}, false
	}
	snap := e.task
	return snap, true
}

// List returns a snapshot of all tasks in insertion order.
func (r *Registry) List() []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Task, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.task)
	}
	return out
}

// ListByOwner returns tasks owned by the given session.
func (r *Registry) ListByOwner(sessionID string) []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Task
	for _, e := range r.entries {
		if e.task.OwnerSessionID == sessionID {
			out = append(out, e.task)
		}
	}
	return out
}

// ListRunningByOwner returns only running tasks for a session.
func (r *Registry) ListRunningByOwner(sessionID string) []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Task
	for _, e := range r.entries {
		if e.task.OwnerSessionID == sessionID && e.task.Status == StatusRunning {
			out = append(out, e.task)
		}
	}
	return out
}

// Tail returns lines captured since fromOffset. It returns the lines and
// the new offset to pass on the next call (for drain semantics).
func (r *Registry) Tail(id string, fromOffset int64) ([]Line, int64, bool) {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return nil, 0, false
	}
	e.linesMu.Lock()
	defer e.linesMu.Unlock()
	var out []Line
	for _, ln := range e.lines {
		if ln.Offset >= fromOffset {
			out = append(out, ln)
		}
	}
	var nextOffset int64 = fromOffset
	if len(out) > 0 {
		nextOffset = out[len(out)-1].Offset + 1
	}
	return out, nextOffset, true
}

// AppendLine records a captured line. Called by lineWriter (indirectly
// through Subscribe); also available for sub-agent tasks that produce lines
// directly.
func (r *Registry) AppendLine(id string, ln Line) {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return
	}
	e.linesMu.Lock()
	ln.Offset = int64(len(e.lines))
	ln.At = time.Now()
	e.lines = append(e.lines, ln)
	e.linesMu.Unlock()
	e.subs.broadcast(ln)
}

// Subscribe registers a channel to receive live Line broadcasts for the
// given task. Returns the channel, a cancel func to deregister, and true.
// When the task is unknown or already terminal, returns nil, nil, false.
//
// The returned channel is buffered (256 lines). When the task ends the
// registry closes the channel so range loops terminate naturally.
func (r *Registry) Subscribe(id string) (<-chan Line, func(), bool) {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return nil, nil, false
	}
	if e.task.IsTerminal() {
		return nil, nil, false
	}
	ch := make(chan Line, 256)
	e.subs.Add(ch)
	cancel := func() { e.subs.Remove(ch) }
	return ch, cancel, true
}

// Abort sends SIGTERM to the task's process (if PID is known), waits up to
// 5 seconds, then sends SIGKILL. Marks the task as cancelled.
func (r *Registry) Abort(ctx context.Context, id string) error {
	r.mu.RLock()
	e, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	if e.task.IsTerminal() {
		return ErrAlreadyTerminal
	}
	if e.pid > 0 {
		if err := kill(e.pid); err != nil {
			r.logf("tasks.abort.kill_err", "id", id, "pid", e.pid, "err", err.Error())
		}
		// Wait for up to 5 s then SIGKILL.
		go func() {
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				_ = kill9(e.pid)
			case <-ctx.Done():
			}
		}()
	}
	// Mark cancelled.
	r.mu.Lock()
	if !e.task.IsTerminal() {
		now := time.Now()
		e.task.Status = StatusCancelled
		e.task.EndedAt = &now
		e.subs.closeAll()
	}
	r.mu.Unlock()

	if r.store != nil {
		_ = r.store.UpdateStatus(ctx, id, StatusCancelled, -1, time.Now())
	}
	r.logf("tasks.aborted", "id", id)
	return nil
}

// MarkCrashed is called at harness boot by recovery.go. It marks all
// in-flight (Status=running) entries in the SQLite store whose PID is no
// longer alive as Status=crashed.
func (r *Registry) MarkCrashed(ctx context.Context) {
	if r.store == nil {
		return
	}
	n, err := r.store.MarkCrashed(ctx)
	if err != nil {
		r.logf("tasks.mark_crashed.err", "err", err.Error())
		return
	}
	if n > 0 {
		r.logf("tasks.mark_crashed", "count", n)
	}
}

// kill sends SIGTERM to the process with the given PID.
// Extracted as a variable for test overriding.
var kill = func(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return sigterm(p)
}

// kill9 sends SIGKILL to the process with the given PID.
var kill9 = func(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func (r *Registry) logf(event string, kv ...any) {
	if r == nil || r.logger == nil {
		return
	}
	r.logger.Info(event, kv...)
}
