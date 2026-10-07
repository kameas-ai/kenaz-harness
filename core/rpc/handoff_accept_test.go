package rpc

// handoff_accept_test.go — device-keys-handoff-01DEVKH01 WP05 + WP-PI
// (AC-6, AC-PI-1/2): an accepted handoff becomes a NEW local session in a
// database a PREVIOUS RELEASE produced (v0.91.0 snapshot, booted through
// production storagesqlite.Open — real SQL encode/decode, not
// NewMemoryStore), deduped by inbox item id through a REAL ledger file,
// surviving a close/reopen of the database.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextsyncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

func shareRecordFor(t *testing.T, mgr *session.Manager, sid, inboxID string) contextsyncview.AcceptedShareRecord {
	t.Helper()
	evs, err := (&handoffSessionLoader{sessions: mgr}).LoadSessionEvents(context.Background(), sid)
	if err != nil || len(evs) == 0 {
		t.Fatalf("load %s: %d events, %v", sid, len(evs), err)
	}
	return contextsyncview.AcceptedShareRecord{InboxItemID: inboxID, SessionID: sid,
		SenderUserID: "u-alice", SenderEmail: "alice@example.com", Events: evs}
}

func TestHandoffAccept_PersistsNewSession_OnUpgradedDB(t *testing.T) {
	ctx := context.Background()
	dbDir, dataDir := t.TempDir(), t.TempDir()
	mgr, closeDB := openHandoffSnapshot(t, dbDir)

	// Source: the snapshot's own session plus a move-bearing turn written
	// through the production transcript seam.
	const src = "seed-session-1"
	user, err := mgr.AppendTranscriptEntry(ctx, src, session.TranscriptEntry{Role: session.RoleUser, Content: "list the files"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []session.TranscriptEntry{
		{Role: session.RoleTool, Content: "list_files(path: string)", Kind: session.MoveKindToolCall, MoveIndex: 0, TurnSpanID: user.ID,
			ToolCalls: []session.ToolCall{{ID: "call-1", Name: "list_files"}}, ModelToolArgs: map[string]string{"call-1": `{"path":"/private"}`}},
		{Role: session.RoleTool, Content: "a.go", Kind: session.MoveKindToolResult, MoveIndex: 1, TurnSpanID: user.ID,
			ToolCalls: []session.ToolCall{{ID: "call-1", Name: "list_files", Result: "a.go"}}},
		{Role: session.RoleAssistant, Content: "One file: a.go", Kind: session.MoveKindFinal, MoveIndex: 2, TurnSpanID: user.ID},
	} {
		if _, err := mgr.AppendTranscriptEntry(ctx, src, e); err != nil {
			t.Fatal(err)
		}
	}
	srcRows, _ := mgr.ListMessages(ctx, src)
	srcRec, _ := mgr.Get(ctx, src)
	before, _ := mgr.List(ctx)

	store := newHandoffAcceptStore(mgr, dataDir)
	rec := shareRecordFor(t, mgr, src, "inbox-item-1")
	view, err := store.Persist(ctx, rec)
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if view.LocalSessionID == "" || view.LocalSessionID == src || view.AlreadyAccepted || view.EventCount != len(srcRows) {
		t.Fatalf("view = %+v", view)
	}
	wantTitle := srcRec.Name + " — shared by alice@example.com"
	if view.Title != wantTitle {
		t.Fatalf("title = %q, want %q", view.Title, wantTitle)
	}

	check := func(m *session.Manager) {
		t.Helper()
		got, err := m.ListMessages(ctx, view.LocalSessionID)
		if err != nil || len(got) != len(srcRows) {
			t.Fatalf("accepted rows = %d (%v), want %d", len(got), err, len(srcRows))
		}
		var newUser string
		for i := range got {
			if got[i].Role != srcRows[i].Role || got[i].Content != srcRows[i].Content || got[i].MoveKind() != srcRows[i].MoveKind() {
				t.Fatalf("row %d: %s/%s %q != %s/%s %q", i, got[i].Role, got[i].MoveKind(), got[i].Content,
					srcRows[i].Role, srcRows[i].MoveKind(), srcRows[i].Content)
			}
			if got[i].ModelLayerToolArgs() != nil {
				t.Fatalf("row %d carries model-layer args into the shared copy", i)
			}
			if got[i].Content == "list the files" {
				newUser = got[i].ID
			}
			if span := got[i].TurnSpanID(); span != "" && span != newUser {
				t.Fatalf("row %d span %q not re-linked to the accepted copy's user row %q", i, span, newUser)
			}
		}
		rec, err := m.Get(ctx, view.LocalSessionID)
		if err != nil || rec.Name != wantTitle {
			t.Fatalf("session row = %+v, %v", rec, err)
		}
	}
	check(mgr)

	// Re-accept is a no-op returning the same local session.
	again, err := store.Persist(ctx, rec)
	if err != nil || !again.AlreadyAccepted || again.LocalSessionID != view.LocalSessionID {
		t.Fatalf("re-persist = %+v, %v", again, err)
	}
	after, _ := mgr.List(ctx)
	if len(after) != len(before)+1 {
		t.Fatalf("sessions %d -> %d, want exactly one new", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(dataDir, "fleet", "handoff_accepted.json")); err != nil {
		t.Fatalf("provenance ledger: %v", err)
	}
	closeDB()

	// Reopen the SAME database file and a fresh store over the SAME data
	// dir: rows, title and the dedupe all survive.
	mgr2, closeDB2 := openHandoffSnapshot(t, dbDir)
	defer closeDB2()
	check(mgr2)
	if v, ok := newHandoffAcceptStore(mgr2, dataDir).Lookup(ctx, "inbox-item-1"); !ok || v.LocalSessionID != view.LocalSessionID {
		t.Fatalf("lookup after reopen = %+v %v", v, ok)
	}
	// Deleting the local copy re-enables accept (fleet may still hold it).
	if err := mgr2.Delete(ctx, view.LocalSessionID); err != nil {
		t.Fatal(err)
	}
	if _, ok := newHandoffAcceptStore(mgr2, dataDir).Lookup(ctx, "inbox-item-1"); ok {
		t.Fatal("lookup must not resurrect a deleted local copy")
	}
}

func TestHandoffAccept_BadPayload_LeavesNoSession(t *testing.T) {
	ctx := context.Background()
	mgr, closeDB := openHandoffSnapshot(t, t.TempDir())
	defer closeDB()
	before, _ := mgr.List(ctx)
	store := newHandoffAcceptStore(mgr, t.TempDir())
	_, err := store.Persist(ctx, contextsyncview.AcceptedShareRecord{InboxItemID: "x",
		Events: []contextsyncview.SessionEventRecord{{Seq: 1, Bytes: []byte(`{"v":9,"role":"user"}`)}}})
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("err = %v", err)
	}
	after, _ := mgr.List(ctx)
	if len(after) != len(before) {
		t.Fatal("a rejected payload must not create a session")
	}
	if title := acceptedTitle("", ""); title != "Shared by a teammate" {
		t.Fatalf("fallback title = %q", title)
	}
}
