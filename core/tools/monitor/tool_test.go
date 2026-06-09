package monitor_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/tools/monitor"
)

// fakeRegistry implements monitor.RegistryIface with an in-memory backend.
type fakeRegistry struct {
	mu      sync.Mutex
	tasks   map[string]*fakeTaskEntry
}

type fakeTaskEntry struct {
	task  tasks.Task
	lines []tasks.Line
	subs  []chan tasks.Line
	mu    sync.Mutex
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{tasks: make(map[string]*fakeTaskEntry)}
}

func (r *fakeRegistry) addTask(id, kind, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[id] = &fakeTaskEntry{
		task: tasks.Task{ID: id, Kind: kind, Status: status, StartedAt: time.Now()},
	}
}

func (r *fakeRegistry) appendLine(id string, stream, text string) {
	r.mu.Lock()
	e, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	e.mu.Lock()
	ln := tasks.Line{
		Stream: stream,
		Text:   text,
		Offset: int64(len(e.lines)),
		At:     time.Now(),
	}
	e.lines = append(e.lines, ln)
	subs := make([]chan tasks.Line, len(e.subs))
	copy(subs, e.subs)
	e.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ln:
		default:
		}
	}
}

func (r *fakeRegistry) endTask(id string, exitCode int) {
	r.mu.Lock()
	e, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	e.mu.Lock()
	status := tasks.StatusCompleted
	if exitCode != 0 {
		status = tasks.StatusFailed
	}
	now := time.Now()
	e.task.Status = status
	e.task.ExitCode = exitCode
	e.task.EndedAt = &now
	subs := e.subs
	e.subs = nil
	e.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

// RegistryIface implementation.

func (r *fakeRegistry) Get(id string) (tasks.Task, bool) {
	r.mu.Lock()
	e, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return tasks.Task{}, false
	}
	e.mu.Lock()
	t := e.task
	e.mu.Unlock()
	return t, true
}

func (r *fakeRegistry) Tail(id string, fromOffset int64) ([]tasks.Line, int64, bool) {
	r.mu.Lock()
	e, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return nil, 0, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []tasks.Line
	for _, ln := range e.lines {
		if ln.Offset >= fromOffset {
			out = append(out, ln)
		}
	}
	nextOffset := fromOffset
	if len(out) > 0 {
		nextOffset = out[len(out)-1].Offset + 1
	}
	return out, nextOffset, true
}

func (r *fakeRegistry) Subscribe(id string) (<-chan tasks.Line, func(), bool) {
	r.mu.Lock()
	e, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	e.mu.Lock()
	if e.task.IsTerminal() {
		e.mu.Unlock()
		return nil, nil, false
	}
	ch := make(chan tasks.Line, 256)
	e.subs = append(e.subs, ch)
	e.mu.Unlock()
	cancel := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		for i, s := range e.subs {
			if s == ch {
				e.subs = append(e.subs[:i], e.subs[i+1:]...)
				return
			}
		}
	}
	return ch, cancel, true
}

// ── helpers ──────────────────────────────────────────────────────────────────

func newTool(reg *fakeRegistry) *monitor.Tool {
	return monitor.New(monitor.Options{
		Registry: reg,
	})
}

func callTool(t *testing.T, tool *monitor.Tool, args map[string]any) map[string]any {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("tool.Call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return out
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestDrain_ReturnsBufferedLines(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-1", tasks.KindBash, tasks.StatusRunning)
	reg.appendLine("task-1", "stdout", "line A")
	reg.appendLine("task-1", "stdout", "line B")

	tool := newTool(reg)

	out := callTool(t, tool, map[string]any{
		"task_id": "task-1",
		"mode":    "drain",
	})

	lines, _ := out["lines"].([]any)
	if len(lines) != 2 {
		t.Errorf("drain returned %d lines; want 2", len(lines))
	}
	if out["matched"] != "drained" {
		t.Errorf("matched = %v; want drained", out["matched"])
	}
}

func TestDrain_UpdatesOffset(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-2", tasks.KindBash, tasks.StatusRunning)
	reg.appendLine("task-2", "stdout", "first")
	reg.appendLine("task-2", "stdout", "second")

	offsets := monitor.NewInMemoryOffsetStore()
	tool := monitor.New(monitor.Options{Registry: reg, Offsets: offsets})

	argsJSON, _ := json.Marshal(map[string]any{"task_id": "task-2", "mode": "drain"})

	// First drain: returns 2 lines.
	r1, _ := tool.Call(context.Background(), argsJSON)
	var out1 map[string]any
	_ = json.Unmarshal(r1, &out1)
	if len(out1["lines"].([]any)) != 2 {
		t.Errorf("first drain: %d lines; want 2", len(out1["lines"].([]any)))
	}

	// Append more lines.
	reg.appendLine("task-2", "stdout", "third")

	// Second drain: returns only new line.
	r2, _ := tool.Call(context.Background(), argsJSON)
	var out2 map[string]any
	_ = json.Unmarshal(r2, &out2)
	if len(out2["lines"].([]any)) != 1 {
		t.Errorf("second drain: %d lines; want 1", len(out2["lines"].([]any)))
	}
}

func TestWatch_RegexMatch(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-3", tasks.KindBash, tasks.StatusRunning)

	tool := newTool(reg)

	// Produce a line asynchronously.
	go func() {
		time.Sleep(50 * time.Millisecond)
		reg.appendLine("task-3", "stdout", "compiled successfully in 1.2s")
	}()

	out := callTool(t, tool, map[string]any{
		"task_id": "task-3",
		"mode":    "watch",
		"until": map[string]any{
			"regex":     "compiled successfully",
			"timeout_s": 5,
		},
	})

	if out["matched"] != "regex" {
		t.Errorf("matched = %v; want regex", out["matched"])
	}
	lines, _ := out["lines"].([]any)
	if len(lines) == 0 {
		t.Error("no lines returned on regex match")
	}
}

func TestWatch_Exit(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-4", tasks.KindBash, tasks.StatusRunning)

	tool := newTool(reg)

	// End the task asynchronously.
	go func() {
		time.Sleep(50 * time.Millisecond)
		reg.appendLine("task-4", "stdout", "build done")
		reg.endTask("task-4", 0)
	}()

	out := callTool(t, tool, map[string]any{
		"task_id": "task-4",
		"mode":    "watch",
		"until": map[string]any{
			"exit":      true,
			"timeout_s": 5,
		},
	})

	if out["matched"] != "exit" {
		t.Errorf("matched = %v; want exit", out["matched"])
	}
	if out["exit_code"] == nil {
		t.Error("exit_code should be set after exit match")
	}
}

func TestWatch_Timeout(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-5", tasks.KindBash, tasks.StatusRunning)

	tool := newTool(reg)

	out := callTool(t, tool, map[string]any{
		"task_id": "task-5",
		"mode":    "watch",
		"until": map[string]any{
			"regex":     "will_never_match_xyz",
			"exit":      false,
			"timeout_s": 1,
		},
	})

	if out["matched"] != "timeout" {
		t.Errorf("matched = %v; want timeout", out["matched"])
	}
}

func TestWatch_AlreadyTerminalTask(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-6", tasks.KindBash, tasks.StatusCompleted)

	tool := newTool(reg)

	out := callTool(t, tool, map[string]any{
		"task_id": "task-6",
		"mode":    "watch",
		"until": map[string]any{
			"exit": true,
		},
	})

	if out["matched"] != "exit" {
		t.Errorf("matched = %v; want exit (task already terminal)", out["matched"])
	}
}

func TestWatch_TaskNotFound(t *testing.T) {
	reg := newFakeRegistry()
	tool := newTool(reg)

	out := callTool(t, tool, map[string]any{
		"task_id": "does-not-exist",
		"mode":    "drain",
	})

	if out["error"] == nil {
		t.Error("expected error result for unknown task")
	}
}

func TestDrain_InvalidMode(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-7", tasks.KindBash, tasks.StatusRunning)
	tool := newTool(reg)

	out := callTool(t, tool, map[string]any{
		"task_id": "task-7",
		"mode":    "invalid_mode",
	})
	if out["error"] == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestWatch_RegexInvalidPattern(t *testing.T) {
	reg := newFakeRegistry()
	reg.addTask("task-8", tasks.KindBash, tasks.StatusRunning)
	tool := newTool(reg)

	out := callTool(t, tool, map[string]any{
		"task_id": "task-8",
		"mode":    "watch",
		"until": map[string]any{
			"regex":     "[invalid regex(",
			"timeout_s": 1,
		},
	})
	if out["error"] == nil {
		t.Error("expected error for invalid regex")
	}
}
