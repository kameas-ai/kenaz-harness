// Package fleet — audit_cursor.go
//
// Atomic cursor persistence for the fleet audit archiver.
// The cursor is the event_id of the last ACK'd event. On startup the
// archiver reads it and resumes from cursor+1.
//
// Atomicity: writes use a tmp file + rename to avoid partial writes.
// (fleet-audit-archival-01NDFSEX13 WP02 — FR-003, NFR-003)
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// auditCursorFile is the filename within <DataDir>/fleet/ where the
// last-ACK'd event_id is persisted.
const auditCursorFile = "audit_cursor.txt"

// auditCursorDir is the subdirectory under DataDir used for fleet cursor
// and archival-related files.
const auditCursorDir = "fleet"

// readAuditCursor reads the persisted cursor from disk.
// Returns ("", nil) when the file does not exist (fresh start).
func readAuditCursor(dataDir string) (string, error) {
	path := auditCursorPath(dataDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("fleet/audit_cursor: read: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// saveAuditCursor atomically writes the cursor to disk.
// An empty cursorID is a no-op (avoids overwriting a real cursor with nothing).
func saveAuditCursor(dataDir, cursorID string) error {
	if cursorID == "" {
		return nil
	}
	dir := filepath.Join(dataDir, auditCursorDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet/audit_cursor: mkdir: %w", err)
	}
	path := auditCursorPath(dataDir)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(cursorID), 0o600); err != nil {
		return fmt.Errorf("fleet/audit_cursor: write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fleet/audit_cursor: rename: %w", err)
	}
	return nil
}

func auditCursorPath(dataDir string) string {
	return filepath.Join(dataDir, auditCursorDir, auditCursorFile)
}
