package sessions

// impl_last_usage_read_test.go — chat-turn-integrity-01PMZ606 WP11: the
// token/cost footer and context-window meter must survive a session
// reload, not just a live session.usage.updated event.
//
// Per spec.md §5.7 / C-5, session.Manager.GetLastUsage already existed and
// already read the persisted sessions.last_usage_json column, but nothing
// on the rpc read path called it: `grep -c "astUsage"
// core/rpc/views/sessions/api.go` returned 0 before this WP. Get() handed
// back a Session with no usage data at all, so every session reopen (and
// every app restart) showed 0 tok · $0.0000 until the next live turn
// completed — even though the real number was sitting in the database the
// whole time.
//
// Per spec.md §8 rule 1 / CLAUDE.md blind spot #2, this drives REAL sqlite
// (storagesqlite.Open + session.NewSQLStore), not session.NewMemoryStore,
// and reopens the database from scratch (fresh *storage.DB, fresh
// *session.Manager, fresh SessionsAPI) to model an actual session reload /
// app restart rather than an in-process cache hit.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// openRealSessionAPI opens a real sqlite database rooted at dir and wraps
// it in a fresh Manager + SessionsAPI. Called twice in the reload test
// against the SAME dir to model "close the app, reopen the session."
func openRealSessionAPI(t *testing.T, dir string) (SessionsAPI, *session.Manager, func()) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	api := NewManagerAPI(mgr)
	closeFn := func() { _ = db.Close(context.Background()) }
	return api, mgr, closeFn
}

// TestGet_LastUsage_SurvivesReload is AC-014's Go-side half: a session
// with a persisted last_usage_json snapshot returns it on Get, after a
// genuine close-and-reopen of the underlying database — not merely a
// second call against the same in-process Manager.
func TestGet_LastUsage_SurvivesReload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	api1, mgr1, close1 := openRealSessionAPI(t, dir)
	rec, err := api1.Create(ctx, "reload-me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := session.LastUsage{
		PromptTokens:     4096,
		CompletionTokens: 512,
		TotalTokens:      4608,
		CostUSD:          0.0234,
		CostSource:       "provider",
	}
	if err := mgr1.SetLastUsage(ctx, rec.ID, want); err != nil {
		t.Fatalf("SetLastUsage: %v", err)
	}
	close1()

	// Reopen from the SAME on-disk database with a brand new Manager +
	// SessionsAPI — this is what a session reopen / app restart does.
	api2, _, close2 := openRealSessionAPI(t, dir)
	defer close2()

	got, err := api2.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}

	if got.LastUsage == nil {
		t.Fatal("Get after reload: LastUsage is nil; want the persisted snapshot. " +
			"This is the CHAT-turn-integrity WP11 defect: the composer footer and " +
			"context-window meter read 0 tok · $0.0000 on every session reopen even " +
			"though the real number is already in sessions.last_usage_json.")
	}
	got2 := *got.LastUsage
	if got2.PromptTokens != want.PromptTokens ||
		got2.CompletionTokens != want.CompletionTokens ||
		got2.TotalTokens != want.TotalTokens ||
		got2.CostUSD != want.CostUSD ||
		got2.CostSource != want.CostSource {
		t.Errorf("Get after reload LastUsage = %+v, want %+v", got2, want)
	}
}

// TestGet_LastUsage_NilWhenNoTurnCompleted pins the "absent, not zero"
// contract: a session that never had SetLastUsage called returns a nil
// LastUsage pointer, not a non-nil struct with zero fields. This is what
// lets the frontend distinguish "no data yet" from "a real turn that
// happened to cost $0" — matching GetLastUsage's own "zero value, not an
// error" doc contract at the store layer.
func TestGet_LastUsage_NilWhenNoTurnCompleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	api, _, closeFn := openRealSessionAPI(t, dir)
	defer closeFn()

	rec, err := api.Create(ctx, "never-turned")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := api.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastUsage != nil {
		t.Errorf("LastUsage = %+v, want nil for a session with no completed turn", *got.LastUsage)
	}
}
