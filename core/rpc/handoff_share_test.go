package rpc

// handoff_share_test.go — device-keys-handoff-01DEVKH01 WP04 (AC-4; closes
// unwired-ledger 2026-10-06 item 1). Handoff_Share's session loader reads
// a session a PREVIOUS RELEASE wrote (the committed v0.91.0 upgrade
// snapshot, booted through production storagesqlite.Open) and produces
// N>0 self-contained events; an empty session never reaches the backend.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	contextsyncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// handoffSnapshotTag is the newest committed upgrade snapshot at the
// mission's base (release/v0.93.0 @ 1867d7ec).
const handoffSnapshotTag = "v0.91.0"

// openHandoffSnapshot materialises the snapshot into dir and boots it
// through production storage, returning a session manager over REAL sqlite.
func openHandoffSnapshot(t *testing.T, dir string) (*session.Manager, func()) {
	t.Helper()
	ctx := context.Background()
	if _, err := os.Stat(filepath.Join(dir, "data.db")); os.IsNotExist(err) {
		dump, err := os.ReadFile(filepath.Join("..", "storage", "sqlite", "testdata", "upgrade", handoffSnapshotTag, "dump.sql"))
		if err != nil {
			t.Fatalf("read %s dump: %v", handoffSnapshotTag, err)
		}
		raw, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(dir, "data.db"))+"?_pragma=foreign_keys(1)")
		if err != nil {
			t.Fatal(err)
		}
		raw.SetMaxOpenConns(1)
		if err := upgradesnap.Materialize(ctx, raw, string(dump)); err != nil {
			t.Fatalf("materialise %s: %v", handoffSnapshotTag, err)
		}
		_ = raw.Close()
	}
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open on the %s snapshot: %v", handoffSnapshotTag, err)
	}
	return session.NewManager(session.NewSQLStore(session.NewStorageDB(db))), func() { _ = db.Close(context.Background()) }
}

type recordingHandoffBackend struct {
	contextsyncview.HandoffBackend
	events []contextsyncview.SessionEventRecord
	calls  int
}

func (r *recordingHandoffBackend) ShareSession(_ context.Context, _, _ string, ev []contextsyncview.SessionEventRecord) error {
	r.calls++
	r.events = ev
	return nil
}

func TestHandoffShare_LoadsRealSessionFromUpgradedDB(t *testing.T) {
	ctx := context.Background()
	mgr, closeDB := openHandoffSnapshot(t, t.TempDir())
	defer closeDB()
	const sid = "seed-session-1"
	rows, err := mgr.ListMessages(ctx, sid)
	if err != nil || len(rows) == 0 {
		t.Fatalf("precondition: snapshot session history = %d rows, %v", len(rows), err)
	}
	rec, _ := mgr.Get(ctx, sid)

	backend := &recordingHandoffBackend{}
	im := &contextsyncview.Impl{Handoff: backend, SessionEvents: &handoffSessionLoader{sessions: mgr}}
	if err := im.Handoff_Share(ctx, sid, "recipient-1"); err != nil {
		t.Fatalf("Handoff_Share: %v", err)
	}
	if len(backend.events) != len(rows) {
		t.Fatalf("events = %d, want one per transcript row (%d)", len(backend.events), len(rows))
	}
	payloads := make([][]byte, len(backend.events))
	for i, e := range backend.events {
		if e.Seq != uint64(i+1) {
			t.Fatalf("event %d seq = %d, want 1..N", i, e.Seq)
		}
		payloads[i] = e.Bytes
	}
	tr, err := session.DecodeHandoffTranscript(payloads)
	if err != nil {
		t.Fatalf("events must be self-contained and decodable: %v", err)
	}
	if tr.Title != rec.Name {
		t.Fatalf("title = %q, want %q", tr.Title, rec.Name)
	}
	for i := range rows {
		if string(tr.Messages[i].Role) != string(rows[i].Role) || tr.Messages[i].Content != rows[i].Content {
			t.Fatalf("row %d: %s %q != %s %q", i, tr.Messages[i].Role, tr.Messages[i].Content, rows[i].Role, rows[i].Content)
		}
	}

	// An empty session errors readably and never reaches the backend.
	empty, err := mgr.Create(ctx, "nothing yet")
	if err != nil {
		t.Fatal(err)
	}
	backend.calls = 0
	if err := im.Handoff_Share(ctx, empty.ID, "recipient-1"); !errors.Is(err, contextsyncview.ErrHandoffNothingToShare) {
		t.Fatalf("empty session: %v", err)
	}
	if backend.calls != 0 {
		t.Fatal("empty session must not reach the backend")
	}
}
