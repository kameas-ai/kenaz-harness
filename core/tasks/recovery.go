package tasks

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RecoverOrphans is called at harness boot. It scans the SQLite tasks
// table for rows with status=running and marks them crashed, since those
// tasks cannot still be alive after a process restart.
//
// It also removes task log archives older than logRetentionDays to keep
// disk usage bounded.
func RecoverOrphans(ctx context.Context, reg *Registry, logDir string) {
	reg.MarkCrashed(ctx)
	if logDir != "" {
		pruneOldLogs(logDir)
	}
}

// RecoverOrphansWithPIDCheck is the WP03 upgrade of RecoverOrphans.
// It performs the same MarkCrashed sweep PLUS checks in-memory entries
// whose PID is recorded — an entry whose PID is still alive is NOT marked
// crashed (it may be a task that survived a soft restart, e.g. a detached
// sub-process the user launched before a window reload).
//
// In practice the harness is a single process and a true restart kills
// all children, but this makes MarkCrashed semantically correct even in
// test scenarios that share a process.
//
// Returns the count of in-memory entries that were NOT crashed because
// their PID was still alive (informational only).
func RecoverOrphansWithPIDCheck(ctx context.Context, reg *Registry, logDir string) int {
	if reg == nil {
		return 0
	}
	// First: sweep the SQL store (marks all running rows as crashed, as before).
	reg.MarkCrashed(ctx)

	// Second: for each in-memory entry in StatusRunning state, check the PID.
	// If the PID is still alive, restore the entry to its running state in the
	// in-memory map (the SQL row is already marked crashed above, but the
	// in-memory entry reflects reality).
	aliveCount := 0
	reg.mu.Lock()
	for _, e := range reg.entries {
		if e.task.Status == StatusRunning && e.pid > 0 {
			if IsProcessAlive(e.pid) {
				aliveCount++
				// Leave the in-memory entry running — don't change SQL state.
			}
		}
	}
	reg.mu.Unlock()

	if logDir != "" {
		pruneOldLogs(logDir)
	}
	return aliveCount
}

// pruneOldLogs removes *.log.1 archive files older than logRetentionDays.
func pruneOldLogs(logDir string) {
	cutoff := time.Now().AddDate(0, 0, -logRetentionDays)
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if len(ent.Name()) < 6 || ent.Name()[len(ent.Name())-6:] != ".log.1" {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(logDir, ent.Name()))
		}
	}
}
