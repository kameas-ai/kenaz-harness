package mlproducer

// background.go — the exit of a background kenaz__bash job
// (ml-producer-01MLPRD01 WP07 item 2; spec §12 A-15).
//
// A background spawn ships its `terminal` at spawn with no exit_code
// (A-5). When the job ends, bash calls BackgroundEndFunc(ctx, taskID,
// exitCode); Recorder.BackgroundEnded turns that into a follow-up
// `terminal` {task, cmd, exit_code} for the same call, and a non-zero exit
// of a test command adds one to test_fails (test_runs was counted at
// spawn). An exit-0 `git commit` adds commit_count and a `commit` event,
// exactly as a foreground one would.
//
// Everything the follow-up needs is decided AT SPAWN and remembered,
// keyed by the background task id the spawn result carries:
//   - the task id (the ROOT session's task, attributed with the spawn's
//     attendedness — the exit ctx is detached and unattended and is never
//     consulted);
//   - the minimised cmd and the test / commit classification (the full
//     command line is not kept);
//   - the exclusion verdict: an excluded spawn leaves an entry that
//     swallows the exit and ships nothing.
//
// The exit can arrive before the spawn's own observer call has been
// handled (a job shorter than the dispatch return path), so an exit with
// no pending spawn is parked and matched when the spawn lands. Both maps
// are bounded: entries older than bgMaxAge are dropped and each map is
// capped (oldest dropped first). Purge clears both.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
)

const (
	// bgMaxAge drops pending spawns (and parked exits) older than this.
	bgMaxAge = 24 * time.Hour
	// bgMaxPending caps the pending-spawn map.
	bgMaxPending = 1024
	// bgMaxParked caps the parked-exit map.
	bgMaxParked = 256
)

// bgPending is what a background spawn leaves for its exit.
type bgPending struct {
	at       int64 // ms, spawn time
	excluded bool
	taskID   string
	cmd      string // minimised (two tokens)
	isTest   bool
	isCommit bool
}

// bgParked is an exit that arrived before its spawn was handled.
type bgParked struct {
	at       int64
	exitCode int
}

// bashBackgroundTaskID parses kenaz__bash's background spawn result
// ({"task_id":"…","status":"running"}).
func bashBackgroundTaskID(result string) string {
	var r struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(result), &r); err != nil {
		return ""
	}
	return r.TaskID
}

// BackgroundEnded is the recorder's half of bash.BackgroundEndFunc
// (core/rpc wires it next to the task registry's End). The ctx is not
// consulted: the spawn decided attribution.
func (r *Recorder) BackgroundEnded(_ context.Context, bgTaskID string, exitCode int) {
	if r == nil || bgTaskID == "" {
		return
	}
	at := r.nowMS()
	r.enqueue(func() { r.handleBackgroundEnd(bgTaskID, exitCode, at) })
}

// rememberSpawn stores p for bgTaskID and, when its exit was already
// parked, ships the follow-up now. Caller holds r.mu.
func (r *Recorder) rememberSpawn(ctx context.Context, bgTaskID string, p bgPending) {
	if bgTaskID == "" {
		return
	}
	if parked, ok := r.bgParked[bgTaskID]; ok {
		delete(r.bgParked, bgTaskID)
		r.shipBackgroundExit(ctx, p, parked.exitCode, max(parked.at, p.at))
		return
	}
	pruneByAge(r.bgPending, p.at, func(e bgPending) int64 { return e.at }, bgMaxPending)
	r.bgPending[bgTaskID] = p
}

func (r *Recorder) handleBackgroundEnd(bgTaskID string, exitCode int, at int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.bgPending[bgTaskID]
	if !ok {
		pruneByAge(r.bgParked, at, func(e bgParked) int64 { return e.at }, bgMaxParked)
		r.bgParked[bgTaskID] = bgParked{at: at, exitCode: exitCode}
		return
	}
	delete(r.bgPending, bgTaskID)
	r.shipBackgroundExit(context.Background(), p, exitCode, at)
}

// shipBackgroundExit writes the follow-up terminal (+ counters). Caller
// holds r.mu.
func (r *Recorder) shipBackgroundExit(ctx context.Context, p bgPending, exitCode int, at int64) {
	if p.excluded || !r.recording() {
		return
	}
	var events []mlstore.EventDraft
	add := func(kind string, payload map[string]any) {
		events = append(events, mlstore.EventDraft{CreatedAt: at, Body: func(seq int64) ([]byte, error) {
			return encodeEvent(seq, kind, at, payload)
		}})
	}
	add(KindTerminal, map[string]any{"task": p.taskID, "cmd": p.cmd, "exit_code": exitCode})
	prev := r.loadTask(ctx, p.taskID)
	if prev == nil || prev.CompletedAt != 0 {
		// The task is gone or completed (session deleted, idle-swept):
		// the exit still ships, its counters do not reopen the task.
		if _, err := r.cfg.Store.Commit(ctx, mlstore.Write{Events: events}); err != nil {
			logging.L().Warn("mlproducer.recorder.commit_failed", "err", err.Error())
		}
		return
	}
	task := cloneTask(*prev)
	if p.isTest && exitCode != 0 {
		task.TestFails++
	}
	if p.isCommit && exitCode == 0 {
		task.CommitCount++
		add(KindCommit, map[string]any{"task": p.taskID})
	}
	// last_active stays the last tool call / turn end (spec §3.1): a job
	// finishing is not the agent acting. The counters reach the wire on
	// the next upsert (throttled, or the trailing flush).
	r.commit(ctx, &task, events, at-task.LastUpsertAt >= r.cfg.UpsertEvery.Milliseconds())
}

// pruneByAge drops entries older than bgMaxAge relative to now, then the
// oldest ones until there is room for one more under limit.
func pruneByAge[V any](m map[string]V, now int64, age func(V) int64, limit int) {
	cutoff := now - bgMaxAge.Milliseconds()
	for k, v := range m {
		if age(v) < cutoff {
			delete(m, k)
		}
	}
	for len(m) >= limit {
		oldestK, oldest := "", int64(0)
		first := true
		for k, v := range m {
			if a := age(v); first || a < oldest || (a == oldest && k < oldestK) {
				oldestK, oldest, first = k, a, false
			}
		}
		delete(m, oldestK)
	}
}
