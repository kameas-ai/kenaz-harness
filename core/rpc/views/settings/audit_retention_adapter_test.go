package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// readAuditArchiveDir returns the concatenated content of every file
// under <dataDir>/audit-archive/ (RetentionSweep's / ArchiveAndDelete's
// JSONL archive location).
func readAuditArchiveDir(t *testing.T, dataDir string) ([]string, error) {
	t.Helper()
	dir := filepath.Join(dataDir, "audit-archive")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	return out, nil
}

func containsSubstring(entries []string, sub string) bool {
	for _, e := range entries {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func openAdapterTestBackend(t *testing.T) (*eventlog.SQLBackend, storage.DB, string) {
	t.Helper()
	dataDir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dataDir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return eventlog.NewSQLBackend(db), db, dataDir
}

// TestFleetAuditRetentionBackend_SelectBefore_Converts proves the
// []eventlog.RetentionRow -> []fleet.AuditRetentionRow conversion the
// package doc comment describes, over real sqlite.
func TestFleetAuditRetentionBackend_SelectBefore_Converts(t *testing.T) {
	backend, db, dataDir := openAdapterTestBackend(t)
	store := eventlog.NewStore(backend)
	ctx := context.Background()

	old := eventlog.Row{EventID: "fleet-adapter-old", Kind: "k",
		EmittedAt: time.Now().Add(-200 * 24 * time.Hour), Payload: []byte(`{}`), RedactionSummary: "none"}
	if err := store.AppendComputed(ctx, old); err != nil {
		t.Fatalf("append: %v", err)
	}

	adapter := NewFleetAuditRetentionBackend(backend, dataDir)
	rows, err := adapter.SelectBefore(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("SelectBefore: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("SelectBefore returned %d rows, want 1", len(rows))
	}
	if rows[0].EventID != "fleet-adapter-old" {
		t.Errorf("EventID = %q, want %q", rows[0].EventID, "fleet-adapter-old")
	}
	// emitted_at round-trips through a unix-millisecond column, so
	// compare at millisecond precision (the same precision
	// SQLBackend.SelectBefore stores/reads).
	if rows[0].EmittedAt.UnixMilli() != old.EmittedAt.UnixMilli() {
		t.Errorf("EmittedAt = %v, want %v", rows[0].EmittedAt, old.EmittedAt.UTC())
	}
	_ = db
}

// TestFleetAuditRetentionBackend_DeleteRows_ArchivesFirst is E-007's
// concrete proof: the fleet-sweeper adapter's DeleteRows does not issue
// a bare delete — it archives the row (same JSONL location the local
// sweeper's archive_after_window strategy uses) before removing it.
func TestFleetAuditRetentionBackend_DeleteRows_ArchivesFirst(t *testing.T) {
	backend, _, dataDir := openAdapterTestBackend(t)
	store := eventlog.NewStore(backend)
	ctx := context.Background()

	row := eventlog.Row{EventID: "fleet-adapter-del-1", Kind: "k",
		EmittedAt: time.Now(), Payload: []byte(`{"marker":"FLEETADAPTERZORK"}`), RedactionSummary: "none"}
	if err := store.AppendComputed(ctx, row); err != nil {
		t.Fatalf("append: %v", err)
	}

	adapter := NewFleetAuditRetentionBackend(backend, dataDir)
	if err := adapter.DeleteRows(ctx, []string{"fleet-adapter-del-1"}); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}

	// (a) Row is gone from events.
	if _, err := backend.GetRow(ctx, "fleet-adapter-del-1"); err == nil {
		t.Error("fleet-adapter-del-1 still present after DeleteRows")
	}

	// (b) It was archived — same convention RetentionSweep's
	// archive_after_window path uses: a JSONL file under
	// <dataDir>/audit-archive/ containing the marker.
	entries, err := readAuditArchiveDir(t, dataDir)
	if err != nil {
		t.Fatalf("read audit-archive dir: %v", err)
	}
	if !containsSubstring(entries, "FLEETADAPTERZORK") {
		t.Errorf("archived content does not contain the row's marker — DeleteRows did not archive before deleting.\nEntries: %v", entries)
	}
}

// TestFleetAuditRetentionBackend_DeleteRows_MissingRowIsNotAnError
// proves the race-tolerance the doc comment claims: if a row was
// already deleted by the time the fleet adapter's DeleteRows runs
// (e.g. the local sweeper got there first in the same pass), that id
// is skipped, not an error.
func TestFleetAuditRetentionBackend_DeleteRows_MissingRowIsNotAnError(t *testing.T) {
	backend, _, dataDir := openAdapterTestBackend(t)
	adapter := NewFleetAuditRetentionBackend(backend, dataDir)
	if err := adapter.DeleteRows(context.Background(), []string{"never-existed"}); err != nil {
		t.Errorf("DeleteRows on a missing id returned an error, want nil: %v", err)
	}
}
