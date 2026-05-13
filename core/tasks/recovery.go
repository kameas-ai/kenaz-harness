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
