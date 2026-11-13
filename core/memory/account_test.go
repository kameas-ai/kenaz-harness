package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory/narrative"
)

func accountStore(t *testing.T, account *string) SyncStore {
	t.Helper()
	st, err := NewChromemStore(filepath.Join(t.TempDir(), "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	st.(AccountStamper).SetAccountSource(func() AccountView { return AccountView{Stamp: *account, Account: *account} })
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
		{"unresolved, nobody", owner, Chunk{Account: AccountUnresolved}, false},
		{"pending, its token", AccountScope{Account: "b", LegacyOwner: "a", TokenKey: "tb"}, Chunk{Account: PendingAccount("tb")}, true},
		{"pending, another token", AccountScope{Account: "a", LegacyOwner: "a", TokenKey: "ta"}, Chunk{Account: PendingAccount("tb")}, false},
		{"pending, not the legacy owner's", owner, Chunk{Account: PendingAccount("tb")}, false},
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
	if err := st.Add(ctx, Chunk{ID: "x", ScopeKind: ScopeKindSession, ScopeID: "s", Content: "two", Account: "z", Embedding: []float32{1}}); err != nil {
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
	if err := ob.RecordDelete(Chunk{ID: "older", SyncSentAt: time.Now()}); err != nil {
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

func TestAccount_OlderAliasFoldsIntoItsOwnersRow(t *testing.T) {
	ctx := context.Background()
	account := "a"
	st := accountStore(t, &account)
	scope := AccountScope{Account: "a", LegacyOwner: "a"}
	if err := st.Add(ctx, Chunk{ID: "alias", Account: AccountUnknown, ScopeKind: ScopeKindLongTerm, Content: "same", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(ctx, Chunk{ID: "canon", ScopeKind: ScopeKindLongTerm, Content: "same too", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	out := []SyncOutcome{{ID: "alias", Kind: OutcomeRekey, CanonicalID: "canon"}}
	if err := st.ApplySyncOutcomes(ctx, out, time.Now(), AccountScope{Account: "b", LegacyOwner: "a"}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListAll(ctx)
	if len(all) != 2 {
		t.Fatalf("another account's rekey folded rows: %d left", len(all))
	}
	if err := st.ApplySyncOutcomes(ctx, out, time.Now(), scope); err != nil {
		t.Fatal(err)
	}
	all, _ = st.ListAll(ctx)
	if len(all) != 1 || all[0].ID != "canon" {
		t.Fatalf("the owner's older alias must fold: %+v", all)
	}
}

func TestAccount_ClaimPendingGivesNotesAndForgetsToTheConfirmedAccount(t *testing.T) {
	ctx := context.Background()
	account := PendingAccount("tb")
	st := accountStore(t, &account)
	if err := st.Add(ctx, Chunk{ID: "mine", ScopeKind: ScopeKindLongTerm, Content: "x", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	account = PendingAccount("tc")
	if err := st.Add(ctx, Chunk{ID: "theirs", ScopeKind: ScopeKindLongTerm, Content: "y", Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.ClaimPendingAccount(ctx, "tb", "b"); err != nil || n != 1 {
		t.Fatalf("claim: n=%d err=%v", n, err)
	}
	if got := get(t, st, "mine").Account; got != "b" {
		t.Fatalf("mine = %q", got)
	}
	if got := get(t, st, "theirs").Account; got != PendingAccount("tc") {
		t.Fatalf("another token's note = %q", got)
	}
	clock, _ := NewHLC("dev", "")
	ob, err := OpenForgetOutbox("", clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.RecordDelete(Chunk{ID: "gone", Account: PendingAccount("tb"), SyncSentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := ob.ClaimPendingAccount("tb", "b"); err != nil {
		t.Fatal(err)
	}
	if got := ob.PendingFor(AccountScope{Account: "b"}); len(got) != 1 {
		t.Fatalf("forget wasn't claimed: %+v", got)
	}
}

func TestAccountView_Sees(t *testing.T) {
	owner := AccountView{Account: "a", TokenKey: "ta", LegacyOwner: "a"}
	other := AccountView{Account: "b", TokenKey: "tb", LegacyOwner: "a"}
	out := AccountView{LegacyOwner: "a"}
	none := AccountView{}
	cases := []struct {
		name string
		v    AccountView
		c    Chunk
		want bool
	}{
		{"own note", other, Chunk{Account: "b"}, true},
		{"another account's note", other, Chunk{Account: "a"}, false},
		{"another account's note, signed out", out, Chunk{Account: "a"}, false},
		{"older note, recorded owner", owner, Chunk{}, true},
		{"older note, other account", other, Chunk{}, false},
		{"older note, signed out with an owner recorded", out, Chunk{}, false},
		{"older note, no owner recorded", none, Chunk{}, true},
		{"rewritten older note, other account", other, Chunk{Account: AccountUnknown}, false},
		{"signed-out note, any account", other, Chunk{Account: AccountSignedOut}, true},
		{"signed-out note, signed out", out, Chunk{Account: AccountSignedOut}, true},
		{"unresolved, recorded owner", owner, Chunk{Account: AccountUnresolved}, true},
		{"unresolved, other account", other, Chunk{Account: AccountUnresolved}, false},
		{"pending, its token", AccountView{TokenKey: "tb", LegacyOwner: "a"}, Chunk{Account: PendingAccount("tb")}, true},
		{"pending, another token", owner, Chunk{Account: PendingAccount("tb")}, false},
	}
	for _, tc := range cases {
		if got := tc.v.Sees(tc.c); got != tc.want {
			t.Errorf("%s: Sees = %v", tc.name, got)
		}
	}
}

// B can't read or touch A's notes: recalls, pins and deletes would carry over
// to what A later pushes (legal, Option A).
func TestAccount_AnotherAccountCantReadOrTouchANote(t *testing.T) {
	ctx := context.Background()
	view := AccountView{Stamp: "a", Account: "a", LegacyOwner: "a"}
	st0, err := NewChromemStore(filepath.Join(t.TempDir(), "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	st0.(AccountStamper).SetAccountSource(func() AccountView { return view })
	st := st0.(SyncStore)
	vec := []float32{1, 0}
	if err := st.Add(ctx, Chunk{ID: "a1", ScopeKind: ScopeKindLongTerm, Content: "A's", Embedding: vec}); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(ctx, Chunk{ID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s", Content: "A's session", Embedding: vec}); err != nil {
		t.Fatal(err)
	}
	before, _ := st.ListAll(ctx)
	hlcBefore := before[0].SyncGen

	view = AccountView{Stamp: "b", Account: "b", LegacyOwner: "a"}
	if got, _ := st.List(ctx); len(got) != 0 {
		t.Fatalf("B can list A's notes: %+v", got)
	}
	if got, _ := st.Query(ctx, vec, 5); len(got) != 0 {
		t.Fatalf("B can recall A's notes: %+v", got)
	}
	if h, err := st0.(interface {
		SnapshotHealth(context.Context) (HealthSnapshot, error)
	}).SnapshotHealth(ctx); err != nil || h.Counts != (ChunkCounts{}) {
		t.Fatalf("B's health counts A's notes: %+v, %v", h, err)
	}
	if err := st.(PruneCapable).SetPinned(ctx, "a1", true); err == nil {
		t.Fatal("B pinned A's note")
	}
	if _, err := st.Remove(ctx, "a1"); err == nil {
		t.Fatal("B removed A's note")
	}
	if err := st.Delete(ctx, "a1"); err == nil {
		t.Fatal("B deleted A's note")
	}
	if err := st.(PruneCapable).MarkAccessed(ctx, []string{"a1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	gone, err := st.(ScopeDeleter).DeleteScope(ctx, ScopeFilter{Kind: ScopeKindSession, ID: "s"})
	if err != nil || len(gone) != 0 {
		t.Fatalf("B's session delete took A's note: %+v, %v", gone, err)
	}
	// B's identical text isn't a duplicate of a note B can't see.
	if err := st.Add(ctx, Chunk{ID: "b1", ScopeKind: ScopeKindLongTerm, Content: "A's", Embedding: vec}); err != nil {
		t.Fatalf("B's own note: %v", err)
	}
	if err := st.Add(ctx, Chunk{ID: "a1", ScopeKind: ScopeKindLongTerm, Content: "overwrite", Embedding: vec}); err == nil {
		t.Fatal("B overwrote A's note by id")
	}

	view = AccountView{Stamp: "a", Account: "a", LegacyOwner: "a"}
	c := get(t, st, "a1")
	if c.Pinned || c.RecallOwn != 0 || c.Content != "A's" || c.SyncGen != hlcBefore {
		t.Fatalf("A's note was changed: %+v", c)
	}
	get(t, st, "s1")
}

func TestAccount_ForgetAllErasesTheAccountsNotesAndOlderOnes(t *testing.T) {
	ctx := context.Background()
	view := AccountView{Stamp: "a", Account: "a", LegacyOwner: "x"}
	st0, err := NewChromemStore(filepath.Join(t.TempDir(), "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	st0.(AccountStamper).SetAccountSource(func() AccountView { return view })
	st := st0.(SyncStore)
	vec := []float32{1}
	for _, c := range []Chunk{
		{ID: "own-long", ScopeKind: ScopeKindLongTerm},
		{ID: "own-session", ScopeKind: ScopeKindSession, ScopeID: "s"},
		{ID: "pending", Account: PendingAccount("ta"), ScopeKind: ScopeKindLongTerm},
		{ID: "other", Account: "b", ScopeKind: ScopeKindLongTerm},
		{ID: "older", Account: AccountUnknown, ScopeKind: ScopeKindSession, ScopeID: "s"},
		{ID: "offline", Account: AccountSignedOut, ScopeKind: ScopeKindLongTerm},
		{ID: "unresolved", Account: AccountUnresolved, ScopeKind: ScopeKindLongTerm},
	} {
		c.Content, c.Embedding, c.CreatedHLC = c.ID, vec, "1"
		if err := st.Add(ctx, c); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
	}
	scope := AccountScope{Account: "a", LegacyOwner: "x", TokenKey: "ta"}
	n, err := st.DeleteCreatedBefore(ctx, "9", time.Now().Add(time.Hour), scope)
	if err != nil || n != 4 {
		t.Fatalf("erased %d, err %v; want own-long, own-session, pending, older", n, err)
	}
	all, _ := st.ListAll(ctx)
	left := map[string]bool{}
	for _, c := range all {
		left[c.ID] = true
	}
	if !left["other"] || !left["offline"] || !left["unresolved"] || len(left) != 3 {
		t.Fatalf("left = %v", left)
	}
}

func TestNarrative_NoteKeepsTheAccountItsJobWasQueuedUnder(t *testing.T) {
	ctx := context.Background()
	account := "b" // signed in by the time the job runs
	st := accountStore(t, &account)
	w := NewNarrativeStoreWriter(st, nil, nil)
	id, err := w.WriteNarrative(ctx, narrative.NarrativeWriteReq{SessionID: "s", TurnID: "t", Account: "a",
		Content: "from A's turn", Kind: narrative.ChunkKindNarrativeExtractiveFallback, RetrievalWeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, st, id).Account; got != "a" {
		t.Fatalf("stamped %q", got)
	}
}

// Not gone until it's on disk: a failed save leaves the rows, so the retry can't
// report success with nothing done.
func TestAccount_FailedEraseSaveKeepsTheRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st0, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	st := st0.(SyncStore)
	if err := st.Add(ctx, Chunk{ID: "x", ScopeKind: ScopeKindLongTerm, Content: "x", Embedding: []float32{1}, CreatedHLC: "1"}); err != nil {
		t.Fatal(err)
	}
	sent, err := st.MarkSyncSent(ctx, []string{"x"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApplySyncOutcomes(ctx, []SyncOutcome{{ID: "x", Gen: sent["x"].SyncGen, Kind: OutcomeSynced}}, time.Now(), AccountScope{Account: "a", LegacyOwner: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	scope := AccountScope{Account: "a", LegacyOwner: "a"}
	if _, err := st.DeleteCreatedBefore(ctx, "9", time.Now().Add(time.Hour), scope); err == nil {
		t.Skip("this filesystem lets the save through")
	}
	if all, _ := st.ListAll(ctx); len(all) != 1 {
		t.Fatalf("erase dropped rows in memory: %+v", all)
	}
	if _, err := st.DeleteSyncedBefore(ctx, time.Now().Add(time.Hour), scope); err == nil {
		t.Fatal("the reset sweep's failed save must be reported")
	}
	if all, _ := st.ListAll(ctx); len(all) != 1 {
		t.Fatalf("sweep dropped rows in memory: %+v", all)
	}
}

// A rewrite keeps CreatedAt, so a D157 created_at refusal must survive
// FreshIdentity; other blocks (content-based) are cleared.
func TestFreshIdentity_KeepsCreatedAtBlocks(t *testing.T) {
	for code, keep := range map[string]bool{
		BlockErasedBeforeCutoff: true,
		BlockCreatedAtInFuture:  true,
		"secret_detected":       false,
	} {
		out := FreshIdentity(Chunk{ID: "old", SyncBlocked: code}, "new")
		if got := out.SyncBlocked != ""; got != keep {
			t.Fatalf("%s: kept=%v want %v", code, got, keep)
		}
	}
}
