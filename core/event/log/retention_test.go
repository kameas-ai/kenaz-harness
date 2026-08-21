// AC-PI-2 (audit-that-tells-the-truth-01PMZA10 WP-PI): drives
// NewMemoryBackend() / a fake SweepableBackend deliberately — this
// exercises RetentionSweep's own strategy/window logic (keep_forever /
// delete_after_window / archive_after_window selection, archive-before-
// delete ordering), which is backend-agnostic. WP10's AC-011/AC-012/
// AC-013 — a real sqlite database, POPULATED (booted from a committed
// upgrade snapshot, not Open on an empty directory), asserting aged
// rows are gone, SearchFTS on a purged term returns zero rows without
// erroring, and unrelated rows survive — is covered by
// core/storage/sqlite/audit_retention_populated_test.go's
// TestAuditRetention_DeleteAfterWindow_AgainstPopulatedUpgradedDatabase.
package log

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildAgedRows inserts n rows into a MemoryBackend, all older than window.
// Returns the backend and the inserted rows.
func buildAgedRows(t *testing.T, n int, prefix string, age time.Duration) *MemoryBackend {
	t.Helper()
	b := NewMemoryBackend()
	ctx := context.Background()
	cutoff := time.Now().Add(-age)
	for i := 0; i < n; i++ {
		r := Row{
			EventID:   fmt.Sprintf("%s%020d", prefix, i),
			SessionID: fmt.Sprintf("%s-sess", prefix),
			EmitterID: "test/retention",
			Kind:      "test.retain",
			// Place all rows before the cutoff.
			EmittedAt: cutoff.Add(-time.Duration(n-i) * time.Second),
			Payload:   []byte(fmt.Sprintf(`{"seq":%d}`, i)),
			PrevHash:  [32]byte{},
		}
		_ = b.AppendRow(ctx, r, [32]byte{})
	}
	return b
}

func TestRetentionSweep_KeepForever_NoOp(t *testing.T) {
	b := buildAgedRows(t, 5, "01RK", 48*time.Hour)
	res, err := RetentionSweep(context.Background(), b, t.TempDir(), RetentionKeepForever, 24*time.Hour)
	if err != nil {
		t.Fatalf("RetentionSweep keep_forever: %v", err)
	}
	if res.Purged != 0 {
		t.Errorf("keep_forever should purge 0 rows, got %d", res.Purged)
	}
	// Rows should still be present.
	rows, _ := b.SelectByTimeRange(context.Background(), time.Time{}, time.Time{}, "", 0, false)
	if len(rows) != 5 {
		t.Errorf("keep_forever should leave 5 rows, got %d", len(rows))
	}
}

func TestRetentionSweep_DeleteAfterWindow(t *testing.T) {
	b := buildAgedRows(t, 5, "01RD", 48*time.Hour)
	res, err := RetentionSweep(context.Background(), b, t.TempDir(), RetentionDeleteAfterWindow, 24*time.Hour)
	if err != nil {
		t.Fatalf("RetentionSweep delete_after_window: %v", err)
	}
	if res.Purged != 5 {
		t.Errorf("expected 5 rows purged, got %d", res.Purged)
	}
	rows, _ := b.SelectByTimeRange(context.Background(), time.Time{}, time.Time{}, "", 0, false)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestRetentionSweep_ArchiveAfterWindow_WritesJSONL(t *testing.T) {
	b := buildAgedRows(t, 3, "01RA", 48*time.Hour)
	dir := t.TempDir()
	res, err := RetentionSweep(context.Background(), b, dir, RetentionArchiveAfterWindow, 24*time.Hour)
	if err != nil {
		t.Fatalf("RetentionSweep archive_after_window: %v", err)
	}
	if res.Archived != 3 {
		t.Errorf("expected 3 archived, got %d", res.Archived)
	}
	if res.Purged != 3 {
		t.Errorf("expected 3 purged, got %d", res.Purged)
	}
	if res.ArchivePath == "" {
		t.Fatal("expected non-empty ArchivePath")
	}
	// Verify JSONL file exists and has 3 lines.
	data, err := os.ReadFile(res.ArchivePath)
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 JSONL lines, got %d", len(lines))
	}
}

func TestRetentionSweep_ArchivePath_InAuditArchiveDir(t *testing.T) {
	b := buildAgedRows(t, 1, "01RP", 48*time.Hour)
	dir := t.TempDir()
	res, err := RetentionSweep(context.Background(), b, dir, RetentionArchiveAfterWindow, 24*time.Hour)
	if err != nil {
		t.Fatalf("RetentionSweep: %v", err)
	}
	expectedDir := filepath.Join(dir, "audit-archive")
	if filepath.Dir(res.ArchivePath) != expectedDir {
		t.Errorf("archive path %q not in audit-archive dir %q", res.ArchivePath, expectedDir)
	}
}

// TestRetentionSweep_ArchiveAfterWindow_UnwritableArchiveDir_DeletesNothing
// is AC-012: with an unwritable archive directory, archive_after_window
// must delete NOTHING — the archive-before-delete invariant
// (retention.go:64-65 / ArchiveAndDelete) means a failed archive stops
// the delete before it happens, not after.
//
// Mutation-verified manually: swapping the archive+delete order (delete
// first, archive second) in ArchiveAndDelete makes this go red — see
// the mission report for the pasted transcript.
func TestRetentionSweep_ArchiveAfterWindow_UnwritableArchiveDir_DeletesNothing(t *testing.T) {
	b := buildAgedRows(t, 3, "01RU", 48*time.Hour)
	dir := t.TempDir()
	// Create a REGULAR FILE at the exact path archiveRows would
	// os.MkdirAll as a directory — MkdirAll fails with "not a
	// directory" regardless of the test process's uid (portable across
	// CI, unlike a chmod-based unwritable-directory trick, which a
	// root-run CI container can silently bypass).
	archiveDirPath := filepath.Join(dir, "audit-archive")
	if err := os.WriteFile(archiveDirPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}

	_, err := RetentionSweep(context.Background(), b, dir, RetentionArchiveAfterWindow, 24*time.Hour)
	if err == nil {
		t.Fatal("RetentionSweep with an unwritable archive dir returned nil error, want one")
	}

	rows, _ := b.SelectByTimeRange(context.Background(), time.Time{}, time.Time{}, "", 0, false)
	if len(rows) != 3 {
		t.Errorf("rows remaining after a failed archive = %d, want 3 (nothing deleted — archive-before-delete)", len(rows))
	}
}

func TestRetentionSweep_NoOldRows_DoesNothing(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	// Insert a "new" row (won't be old enough).
	r := Row{
		EventID:   "01RN000000000000000000000001",
		SessionID: "sess-new",
		EmitterID: "test/retention",
		Kind:      "test.retain",
		EmittedAt: time.Now(), // fresh
		Payload:   []byte(`{}`),
		PrevHash:  [32]byte{},
	}
	_ = b.AppendRow(ctx, r, [32]byte{})

	res, err := RetentionSweep(ctx, b, t.TempDir(), RetentionDeleteAfterWindow, 24*time.Hour)
	if err != nil {
		t.Fatalf("RetentionSweep no-old: %v", err)
	}
	if res.Purged != 0 {
		t.Errorf("expected 0 purged for fresh row, got %d", res.Purged)
	}
}
