package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func accountStore(t *testing.T, account *string) SyncStore {
	t.Helper()
	st, err := NewChromemStore(filepath.Join(t.TempDir(), "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	st.(AccountStamper).SetAccountSource(func() string { return *account })
	return st.(SyncStore)
}

func TestAccountScope_Owns(t *testing.T) {
	owner := AccountScope{Account: "a", LegacyOwner: "a"}
	other := AccountScope{Account: "b", LegacyOwner: "a"}
	cases := []struct {
		name  string
		scope AccountScope
		c     Chunk
		want  bool
	}{
		{"own note", other, Chunk{Account: "b"}, true},
		{"another account's note", other, Chunk{Account: "a"}, false},
		{"unstamped, legacy owner", owner, Chunk{}, true},
		{"unstamped, other account", other, Chunk{}, false},
		{"rewritten unstamped, legacy owner", owner, Chunk{Account: AccountUnknown}, true},
		{"rewritten unstamped, other account", other, Chunk{Account: AccountUnknown}, false},
		{"signed out", owner, Chunk{Account: AccountSignedOut}, false},
		{"zero scope owns nothing", AccountScope{}, Chunk{}, false},
	}
	for _, tc := range cases {
		if got := tc.scope.Owns(tc.c); got != tc.want {
			t.Errorf("%s: Owns = %v", tc.name, got)
		}
	}
}

func TestAccount_StampedOnceAtCreation(t *testing.T) {
	ctx := context.Background()
	account := "a"
	st := accountStore(t, &account)
	if err := st.Add(ctx, Chunk{ID: "x", ScopeKind: ScopeKindSession, ScopeID: "s", Content: "one", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	account = "b"
	if err := st.Add(ctx, Chunk{ID: "x", ScopeKind: ScopeKindSession, ScopeID: "s", Content: "two", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, "x").Account; got != "a" {
		t.Fatalf("a same-id rewrite changed the owner to %q", got)
	}
	account = ""
	if err := st.Add(ctx, Chunk{ID: "y", ScopeKind: ScopeKindLongTerm, Content: "offline", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, "y").Account; got != AccountSignedOut {
		t.Fatalf("signed-out note account = %q", got)
	}
}

func TestAccount_RewriteOfAnUnstampedNoteStaysUnknown(t *testing.T) {
	ctx := context.Background()
	account := "b"
	st := accountStore(t, &account)
	if err := st.Add(ctx, FreshIdentity(Chunk{ID: "old", ScopeKind: ScopeKindLongTerm, Content: "x", Embedding: []float32{1}}, "new")); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, "new").Account; got != AccountUnknown {
		t.Fatalf("rewrite was claimed by the signed-in account: %q", got)
	}
	if err := st.Add(ctx, FreshIdentity(Chunk{ID: "old2", Account: "a", ScopeKind: ScopeKindLongTerm, Content: "y", Embedding: []float32{1}}, "new2")); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, "new2").Account; got != "a" {
		t.Fatalf("rewrite changed the owner to %q", got)
	}
}

func TestAccount_SweepsAndPullsOnlyTouchTheScopesRows(t *testing.T) {
	ctx := context.Background()
	account := "a"
	st := accountStore(t, &account)
	at := time.Now().UTC()
	scopeA := AccountScope{Account: "a", LegacyOwner: "a"}
	scopeB := AccountScope{Account: "b", LegacyOwner: "a"}
	if err := st.ApplyRemote(ctx, []RemoteRecord{{ID: "a1", Chunk: Chunk{ScopeKind: ScopeKindLongTerm, Content: "x"}}}, at, scopeA); err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, "a1").Account; got != "a" {
		t.Fatalf("pulled note account = %q", got)
	}
	later := at.Add(time.Hour)

	if n, err := st.DeleteSyncedBefore(ctx, later, scopeB); err != nil || n != 0 {
		t.Fatalf("B's reset swept A's row: n=%d err=%v", n, err)
	}
	if n, err := st.DeleteCreatedBefore(ctx, "9", later, scopeB); err != nil || n != 0 {
		t.Fatalf("B's erase took A's row: n=%d err=%v", n, err)
	}
	if err := st.ApplyRemote(ctx, []RemoteRecord{{ID: "a1", Tombstone: true, Reason: TombstoneForgotten}}, at, scopeB); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearSyncMarkers(ctx, scopeB); err != nil {
		t.Fatal(err)
	}
	if c := get(t, st, "a1"); c.SyncedAt.IsZero() {
		t.Fatal("B cleared A's synced markers")
	}
	if n, err := st.DeleteSyncedBefore(ctx, later, scopeA); err != nil || n != 1 {
		t.Fatalf("A's own reset: n=%d err=%v", n, err)
	}
}

func TestAccount_ForgetCarriesTheDeletedNotesOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	clock, _ := NewHLC("dev", "")
	ob, err := OpenForgetOutbox(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.RecordDelete(Chunk{ID: "a1", Account: "a", SyncSentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := ob.Enqueue("older"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForgetOutbox(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.PendingFor(AccountScope{Account: "b", LegacyOwner: "a"}); len(got) != 0 {
		t.Fatalf("B may send %+v", got)
	}
	if got := reopened.PendingFor(AccountScope{Account: "a", LegacyOwner: "a"}); len(got) != 2 {
		t.Fatalf("A may send %+v", got)
	}
}
