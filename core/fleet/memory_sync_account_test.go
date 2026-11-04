package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

var (
	accountA = Identity{OrgID: "org-a", UserID: "user-a", Email: "a@example.test"}
	accountB = Identity{OrgID: "org-b", UserID: "user-b", Email: "b@example.test"}
)

// pushedIDs lists every id a push request has carried, forgets included.
func pushedIDs(f *fakeMemoryFleet) map[string]bool {
	out := map[string]bool{}
	for _, req := range f.pushLog() {
		for _, it := range req.Items {
			out[it.ID] = true
		}
	}
	return out
}

func accountChunk(t *testing.T, d *memDevice, id string) memory.Chunk {
	t.Helper()
	all, err := d.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("chunk %s is not on the device", id)
	return memory.Chunk{}
}

func TestAccountGuard_NotesDoNotCrossAccounts(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-synced", memory.ScopeKindLongTerm, "A's synced note")
	d.sync()
	d.add("a-unsynced", memory.ScopeKindLongTerm, "A's note that never left")

	d.signIn(accountB)
	d.enable()
	d.add("b-note", memory.ScopeKindLongTerm, "B's note")
	d.sync()

	pushed := pushedIDs(w.fleet)
	if pushed["a-unsynced"] {
		t.Fatal("A's unsynced note was uploaded under B")
	}
	if !pushed["b-note"] {
		t.Fatal("B's own note must upload")
	}
	if got := accountChunk(t, d, "a-unsynced").Account; got != AccountKey(accountA) {
		t.Fatalf("a-unsynced account = %q", got)
	}
	if got := accountChunk(t, d, "b-note").Account; got != AccountKey(accountB) {
		t.Fatalf("b-note account = %q", got)
	}
}

func TestAccountGuard_NewAccountStartsWithSyncOffAndAnEmptyCursor(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-synced", memory.ScopeKindLongTerm, "x")
	d.sync()
	d.sync() // the pull after the push moves the cursor
	cursorA := d.ms.state().Cursor
	if cursorA == "" {
		t.Fatal("setup: A must hold a cursor")
	}

	d.signIn(accountB)
	before := w.fleet.requestCount()
	d.ms.RunOnce(context.Background())
	st := d.ms.state()
	if st.Enabled || st.Cursor != "" || st.Account != AccountKey(accountB) {
		t.Fatalf("B's state = %+v", st)
	}
	if n := w.fleet.requestCount(); n != before {
		t.Fatalf("B hasn't opted in here, but the lane made %d requests", n-before)
	}

	// A returns to their own opt-in and cursor.
	d.signIn(accountA)
	d.ms.RunOnce(context.Background())
	st = d.ms.state()
	if !st.Enabled || st.Cursor != cursorA || st.Account != AccountKey(accountA) {
		t.Fatalf("A's restored state = %+v, want cursor %q", st, cursorA)
	}
}

func TestAccountGuard_QueuedForgetWaitsForItsOwnAccount(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-gone", memory.ScopeKindLongTerm, "x")
	d.sync()
	if err := memory.RemoveForSync(context.Background(), d.store, d.outbox, "a-gone"); err != nil {
		t.Fatal(err)
	}

	d.signIn(accountB)
	d.enable()
	mark := len(w.fleet.pushLog())
	d.add("b-note", memory.ScopeKindLongTerm, "b")
	d.sync()
	for _, req := range w.fleet.pushLog()[mark:] {
		for _, it := range req.Items {
			if it.ID == "a-gone" {
				t.Fatal("A's forget was sent under B")
			}
		}
	}
	if !d.outbox.Has("a-gone") {
		t.Fatal("A's forget must stay queued")
	}

	d.signIn(accountA)
	d.sync()
	if d.outbox.Has("a-gone") {
		t.Fatal("A's forget must be sent when A returns")
	}
}

func TestAccountGuard_PendingForgetAllIsNeverSentUnderAnotherAccount(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-note", memory.ScopeKindLongTerm, "x")
	d.sync()
	if err := d.ms.update(func(s *memSyncState) {
		s.Enabled, s.DisablePending, s.ForgetAllPending = false, true, true
	}); err != nil {
		t.Fatal(err)
	}

	d.signIn(accountB)
	before := w.fleet.requestCount()
	d.ms.RunOnce(context.Background())
	if n := w.fleet.requestCount(); n != before {
		t.Fatalf("A's unfinished forget-all made %d requests under B", n-before)
	}
	if w.fleet.live("a-note") == nil {
		t.Fatal("forget-all ran under the wrong account")
	}

	d.signIn(accountA)
	d.ms.RunOnce(context.Background())
	if st := d.ms.state(); st.ForgetAllPending || st.DisablePending {
		t.Fatalf("A's forget-all must finish when A returns: %+v", st)
	}
	if w.fleet.live("a-note") != nil {
		t.Fatal("A's forget-all must erase A's notes")
	}
}

func TestAccountGuard_NoAccountMeansNoRequests(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-note", memory.ScopeKindLongTerm, "x")
	d.sync()
	d.add("a-later", memory.ScopeKindLongTerm, "y")

	d.signIn(Identity{}) // the window between two accounts
	before := w.fleet.requestCount()
	d.ms.RunOnce(context.Background())
	if s := d.lanes.Snapshot(LaneMemorySync); s.Status != LaneOff || s.Reason != "signed_out" {
		t.Fatalf("lane = %+v", s)
	}
	if st := d.ms.Status(context.Background()); st.FleetError != "" || st.Fleet != nil {
		t.Fatalf("status must not read Fleet: %+v", st)
	}
	if _, err := d.ms.Enable(context.Background(), []string{"long_term"}, MemoryConsentVersion); !errors.Is(err, errNoAccount) {
		t.Fatalf("Enable err = %v", err)
	}
	if _, err := d.ms.Disable(context.Background(), true, "forget-all"); !errors.Is(err, errNoAccount) {
		t.Fatalf("Disable err = %v", err)
	}
	if n := w.fleet.requestCount(); n != before {
		t.Fatalf("%d requests with no known account", n-before)
	}
	if d.ms.state().ForgetAllPending {
		t.Fatal("an erase asked for with no known account must not wait to run on A later")
	}
	if _, err := d.ms.Disable(context.Background(), false, ""); err != nil {
		t.Fatalf("a plain opt-out must still be recorded: %v", err)
	}
	if st := d.ms.state(); st.Enabled || !st.DisablePending || st.Account != AccountKey(accountA) {
		t.Fatalf("the opt-out must wait on A's state for A: %+v", st)
	}
}

// A sign-in can land while a cycle is between requests: every request checks.
func TestAccountGuard_RequestFailsClosedWhenAccountChangesMidCycle(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	for _, next := range []Identity{{}, accountB} {
		d.signIn(next)
		before := w.fleet.requestCount()
		if err := d.ms.doJSON(context.Background(), "GET", "/api/v1/memory/settings", nil, nil); !errors.Is(err, errNoAccount) {
			t.Fatalf("doJSON err = %v", err)
		}
		if n := w.fleet.requestCount(); n != before {
			t.Fatal("the request reached Fleet")
		}
	}
}

// The reviewer's reproduction of the hole: identity and state both say A, the
// tokens are B's. A stale enroll or a late refresh can leave this behind, and in
// served mode the host can change the token at any time.
func TestAccountGuard_TokenForAnotherAccountSendsNothing(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-note", memory.ScopeKindLongTerm, "x")
	d.sync()
	d.add("a-later", memory.ScopeKindLongTerm, "y")

	saveTestTokens(t, accountB.UserID) // the identity file still names A
	before := w.fleet.requestCount()
	d.ms.RunOnce(context.Background())
	if n := w.fleet.requestCount(); n != before {
		t.Fatalf("%d requests with A's identity beside B's token", n-before)
	}
	if s := d.lanes.Snapshot(LaneMemorySync); s.Status != LaneOff || s.Reason != "signed_out" {
		t.Fatalf("lane = %+v", s)
	}
	if _, err := d.ms.Disable(context.Background(), true, "forget-all"); !errors.Is(err, errNoAccount) {
		t.Fatalf("forget-all err = %v", err)
	}
	if n := w.fleet.requestCount(); n != before {
		t.Fatal("forget-all reached Fleet under the wrong token")
	}
	if pushedIDs(w.fleet)["a-later"] {
		t.Fatal("A's note uploaded under B's token")
	}
}

// Checked where the token is attached: passing the account check a moment
// before doesn't help if the token has changed by the time it's sent.
func TestAccountGuard_ClientRefusesToSendAnotherAccountsToken(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	want := TokenIdentityKey()
	saveTestTokens(t, accountB.UserID)
	before := w.fleet.requestCount()
	_, err := d.ms.cfg.Client.do(requireTokenKey(context.Background(), want), "GET", "/api/v1/memory/settings", nil)
	if !errors.Is(err, errNoAccount) {
		t.Fatalf("do err = %v", err)
	}
	if n := w.fleet.requestCount(); n != before {
		t.Fatal("the request reached Fleet")
	}
}

// An identity from before TokenKey existed isn't trusted to send until the next
// enroll records it, and a real enroll response then unblocks sync.
func TestAccountGuard_IdentityWithoutATokenKeyWaitsForEnroll(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.enable()
	if err := SaveIdentity(d.dir, Identity{OrgID: "org-test", UserID: "user-test"}); err != nil {
		t.Fatal(err)
	}
	before := w.fleet.requestCount()
	d.ms.RunOnce(context.Background())
	if n := w.fleet.requestCount(); n != before {
		t.Fatal("sent on an identity with no token record")
	}
	d.enrolled(Identity{OrgID: "org-test", UserID: "user-test"})
	d.sync()
}

func TestAccountGuard_NoteStampFollowsTheTokenAndIdentity(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	if got := CurrentAccountKey(d.dir); got != AccountKey(accountA) {
		t.Fatalf("signed in: %q", got)
	}
	saveTestTokens(t, accountB.UserID) // sign-in before its enroll answers
	if got := CurrentAccountKey(d.dir); got != memory.AccountUnknown {
		t.Fatalf("A's identity beside B's token: %q", got)
	}
	d.signIn(Identity{})
	if got := CurrentAccountKey(d.dir); got != memory.AccountUnknown {
		t.Fatalf("tokens with no identity: %q", got)
	}
	if err := ClearTokens(); err != nil {
		t.Fatal(err)
	}
	if got := CurrentAccountKey(d.dir); got != "" {
		t.Fatalf("no tokens: %q", got)
	}
}

func TestAccountGuard_SignedOutNotesStayOnTheDevice(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.sync()
	if err := ClearTokens(); err != nil {
		t.Fatal(err)
	}
	d.signIn(Identity{})
	d.add("offline", memory.ScopeKindLongTerm, "off")
	if got := accountChunk(t, d, "offline").Account; got != memory.AccountSignedOut {
		t.Fatalf("account = %q", got)
	}
	d.signIn(accountA)
	d.sync()
	if pushedIDs(w.fleet)["offline"] {
		t.Fatal("a signed-out note must not upload")
	}
}

func TestAccountGuard_OneAccountResetLeavesAnothersNotes(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-1", 0)
	d.signIn(accountA)
	d.enable()
	d.add("a-synced", memory.ScopeKindLongTerm, "x")
	d.sync()

	d.signIn(accountB)
	d.enable()
	d.sync()
	w.wall.advance(1)
	if err := d.ms.beginReset(context.Background(), "erased", d.clock.Tick()); err != nil {
		t.Fatal(err)
	}
	d.sync()
	accountChunk(t, d, "a-synced") // fails the test if the sweep took it
}

// ── upgrade: a profile the previous release wrote ───────────────────────────

func copyUpgradeProfile(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "memory", "upgrade", "v0.100.2")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "fleet"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory.gob", "fleet/memory_sync.json", "fleet/memory_outbox.json", "fleet/memory_hlc.json"} {
		raw, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestUpgrade_V0100_FirstAccountOwnsOlderNotesAfterTheOneTimeLine(t *testing.T) {
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.deviceAt("node-v0100", copyUpgradeProfile(t), 0)
	if st := d.ms.state(); !st.Enabled || st.Cursor != "42" || st.Account != "" {
		t.Fatalf("v0.100.2 state must load unchanged: %+v", st)
	}
	d.signIn(accountA)
	d.sync()
	if st := d.ms.state(); st.LegacyOwner != AccountKey(accountA) || st.Account != AccountKey(accountA) || !st.Enabled {
		t.Fatalf("state = %+v", st)
	}

	pushed := pushedIDs(w.fleet)
	if pushed["v0100-unsynced"] {
		t.Fatal("older notes must wait for the one-time line")
	}
	if !pushed["v0100-forgotten"] {
		t.Fatal("the owner's older queued forget must still be sent")
	}
	st := d.ms.Status(context.Background())
	if st.OlderNotes != 1 || st.OlderNotesAccount != accountA.Email {
		t.Fatalf("status = %+v, want 1 older note for %s", st, accountA.Email)
	}

	if err := d.ms.ContinueOlderNotes(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.sync()
	if !pushedIDs(w.fleet)["v0100-unsynced"] {
		t.Fatal("older notes must upload once the owner continues")
	}
	if pushedIDs(w.fleet)["v0100-session"] {
		t.Fatal("session notes never upload")
	}
	if got := d.ms.Status(context.Background()).OlderNotes; got != 0 {
		t.Fatalf("OlderNotes after continuing = %d", got)
	}
}

func TestUpgrade_V0100_OlderNotesNeverUploadAfterAnAccountChange(t *testing.T) {
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.deviceAt("node-v0100", copyUpgradeProfile(t), 0)
	d.signIn(accountA)
	d.sync()

	d.signIn(accountB)
	d.enable()
	d.sync()
	if err := d.ms.ContinueOlderNotes(context.Background()); err == nil {
		t.Fatal("B must not be able to claim the older notes")
	}
	d.sync()
	if pushedIDs(w.fleet)["v0100-unsynced"] {
		t.Fatal("older notes uploaded after an account change")
	}
	if st := d.ms.Status(context.Background()); st.OlderNotes != 0 || st.PendingPush != 0 {
		t.Fatalf("B's panel counts notes that aren't B's: %+v", st)
	}
	accountChunk(t, d, "v0100-unsynced") // stays on the device

	// The recorded account comes back and can still answer the line.
	d.signIn(accountA)
	if err := d.ms.ContinueOlderNotes(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.sync()
	if !pushedIDs(w.fleet)["v0100-unsynced"] {
		t.Fatal("older notes must upload when the owner returns and continues")
	}
}

// The count in the one-time line must be what really uploads, and leaving sync
// must never wait on the line.
func TestUpgrade_V0100_OneTimeLineCountsOnlyWhatWouldUpload(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.deviceAt("node-v0100", copyUpgradeProfile(t), 0)
	d.signIn(accountA)
	d.sync()

	// A recall dirties a note already on Fleet. It isn't an older note to upload.
	if err := d.ms.update(func(s *memSyncState) { s.Scopes = []string{"long_term"} }); err != nil {
		t.Fatal(err)
	}
	if got := d.ms.Status(ctx).OlderNotes; got != 1 {
		t.Fatalf("OlderNotes = %d, want only the unsynced long-term note", got)
	}
	if err := d.ms.update(func(s *memSyncState) { s.Scopes = []string{"global"} }); err != nil {
		t.Fatal(err)
	}
	if got := d.ms.Status(ctx).OlderNotes; got != 0 {
		t.Fatalf("OlderNotes = %d, but long-term isn't enabled", got)
	}

	if !mayPush(memory.Chunk{ScopeKind: memory.ScopeKindSession, SyncSentAt: time.Now()}, d.ms.scope(), false) {
		t.Fatal("the owner's demotion must not wait for the line")
	}
	if !mayPush(memory.Chunk{ScopeKind: memory.ScopeKindGlobal, SyncedAt: time.Now()}, d.ms.scope(), false) {
		t.Fatal("changes to a note already on Fleet must not wait for the line")
	}
	if mayPush(memory.Chunk{ScopeKind: memory.ScopeKindLongTerm}, d.ms.scope(), false) {
		t.Fatal("an older note must wait for the line")
	}
	other := memory.AccountScope{Account: AccountKey(accountB), LegacyOwner: AccountKey(accountA)}
	if mayPush(memory.Chunk{ScopeKind: memory.ScopeKindSession, SyncSentAt: time.Now()}, other, true) {
		t.Fatal("another account can't demote an older note")
	}
}

// An erase left unfinished by v0.100.2 can't be tied to whoever signs in first.
func TestUpgrade_V0100_InheritedForgetAllIsNotRunForTheFirstAccount(t *testing.T) {
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.deviceAt("node-v0100", copyUpgradeProfile(t), 0)
	d.add("kept-on-fleet", memory.ScopeKindGlobal, "already on Fleet")
	if err := d.ms.update(func(s *memSyncState) {
		s.Enabled, s.DisablePending, s.ForgetAllPending = false, true, true
		s.Account, s.LegacyOwner = "", ""
	}); err != nil {
		t.Fatal(err)
	}
	d.signIn(accountB)
	d.ms.RunOnce(context.Background())
	if st := d.ms.state(); st.ForgetAllPending || st.Enabled || st.DisablePending {
		t.Fatalf("state = %+v, want the opt-out finished and the erase dropped", st)
	}
	if w.fleet.forgetAllCount() != 0 {
		t.Fatal("the inherited erase ran under the first account")
	}
}
