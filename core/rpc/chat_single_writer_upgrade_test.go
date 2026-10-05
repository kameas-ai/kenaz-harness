package rpc

// chat-single-writer-01DOGF0G WP-PI (AC-PI-1): the single-writer turn on
// a database a PREVIOUS RELEASE produced, not a fresh one.
//
// TestChatTurn_UserMessageStoredOnce_AppendThenStartStream starts from an
// empty database, where the session holds nothing but the new turn. The
// production failure lived in sessions that already had history — the
// newest-user-row lookup in LLM.StartStream, the span lookup, and the
// "is this row the session tail" announce rule all read pre-existing rows.
// So this materialises testdata/upgrade/v0.85.2 (the newest committed
// snapshot), boots it through production storagesqlite.Open (which also
// runs sessions/0341 over it), and drives the same append -> StartStream
// chain against the snapshot's own seed-session-1, whose history ends in
// an UNANSWERED user row ("Great, thanks.") — the case where "newest user
// row" and "the row just appended" must not be confused.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/attachments"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func TestChatTurn_SingleWriterOnUpgradedSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dump, err := os.ReadFile(filepath.Join("..", "storage", "sqlite", "testdata", "upgrade", "v0.85.2", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.85.2 dump: %v", err)
	}
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(dir, "data.db"))+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dump)); err != nil {
		t.Fatalf("materialise v0.85.2: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open on the v0.85.2 snapshot: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	attMgr := attachments.NewManager(attachments.NewSQLStore(db))
	h := newSingleWriterHarnessOn(t, sessMgr, attMgr)

	const sid = "seed-session-1"
	before, err := sessMgr.ListMessages(ctx, sid)
	if err != nil || len(before) == 0 {
		t.Fatalf("seed-session-1 history: %d rows, err=%v — the snapshot must carry prior history", len(before), err)
	}
	if before[len(before)-1].Role != session.RoleUser {
		t.Fatalf("precondition: seed-session-1 should end in an unanswered user row, got %s", before[len(before)-1].Role)
	}

	const text = "and one more question about upgrades"
	appended, err := h.sessAPI.AppendMessage(ctx, sid, "user", text)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := h.llmAPI.StartStream(ctx, "test-profile", sid, ""); err != nil {
		t.Fatalf("LLM StartStream: %v", err)
	}
	waitForClosedWithin(t, h.broker, 10*time.Second)

	after, err := sessMgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	// The snapshot's own rows are untouched, in order.
	for i, m := range before {
		if after[i].ID != m.ID || after[i].Content != m.Content {
			t.Errorf("pre-existing row %d changed: %s %q -> %s %q", i, m.ID, m.Content, after[i].ID, after[i].Content)
		}
	}
	var newUsers []session.Message
	for _, m := range after[len(before):] {
		if m.Role == session.RoleUser {
			newUsers = append(newUsers, m)
		}
		if span := m.TurnSpanID(); span != "" && span != appended.ID {
			t.Errorf("row %s (%s) spans %q, want the appended row %q", m.ID, m.MoveKind(), span, appended.ID)
		}
	}
	if len(newUsers) != 1 || newUsers[0].ID != appended.ID {
		t.Errorf("new user rows for one send = %d, want exactly the appended row %s", len(newUsers), appended.ID)
	}

	calls := h.registry.snapshot()
	if len(calls) == 0 {
		t.Fatal("provider never called")
	}
	seen := 0
	for _, m := range calls[0].Messages {
		if m.Role == corellm.RoleUser && strings.Contains(m.Text(), text) {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("provider saw the new user text %d times, want 1", seen)
	}

	var userEvents []map[string]string
	for _, ev := range h.sync.snapshot() {
		if ev["role"] == "user" {
			userEvents = append(userEvents, ev)
		}
	}
	if len(userEvents) != 1 || userEvents[0]["id"] != appended.ID {
		t.Errorf("context-sync user events = %v, want exactly one naming %s", userEvents, appended.ID)
	}
}
