package fleet

// sync_dirty_test.go — an edit made while Fleet is unreachable is neither
// overwritten by a later pull nor forgotten across a restart. Both tests
// drive a real settings_sync_state.json.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// offlineFake is the strict Fleet fake behind a switch that, when off,
// drops every connection (a transport error, as when the network is down).
type offlineFake struct {
	*fakeFleetSync
	offline atomic.Bool
}

func (o *offlineFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if o.offline.Load() {
		hj, ok := w.(http.Hijacker)
		if !ok {
			panic("offlineFake: no hijacker")
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	o.fakeFleetSync.ServeHTTP(w, r)
}

func startOfflineFake(t *testing.T) (*offlineFake, string) {
	t.Helper()
	o := &offlineFake{fakeFleetSync: newFakeFleetSync(t)}
	srv := httptest.NewServer(o)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-dirty", RefreshToken: "rt-dirty", ExpiresAt: time.Now().Add(time.Hour)})
	return o, srv.URL
}

// localPrefs is a collector whose value the test changes, as a local edit.
type localPrefs struct{ v atomic.Value }

func (l *localPrefs) set(s string) { l.v.Store(s) }
func (l *localPrefs) collect(context.Context) (json.RawMessage, error) {
	return json.RawMessage(l.v.Load().(string)), nil
}

func newDirtySyncer(t *testing.T, base, dir string, open *atomic.Bool, local *localPrefs, app *recordingApplier) *Syncer {
	t.Helper()
	s := NewSyncer(makeTestClient(t, base))
	t.Cleanup(s.Stop)
	if err := s.SetStateStore(NewFileSyncStateStore(dir)); err != nil {
		t.Fatalf("SetStateStore: %v", err)
	}
	s.RegisterCategory(SyncCategoryModelPrefs, CategoryConfig{Collector: local.collect, Applier: app.apply})
	s.SetUploadGate(open.Load)
	if err := s.RestoreEnabled(SyncCategoryModelPrefs, true); err != nil {
		t.Fatal(err)
	}
	return s
}

func modelPrefsStatus(t *testing.T, s *Syncer) SyncStatus {
	t.Helper()
	for _, st := range s.Status() {
		if st.Category == SyncCategoryModelPrefs {
			return st
		}
	}
	t.Fatal("no model_prefs status")
	return SyncStatus{}
}

func persistedModelPrefs(t *testing.T, dir string) PersistedSyncState {
	t.Helper()
	got, err := NewFileSyncStateStore(dir).LoadSyncState()
	if err != nil {
		t.Fatal(err)
	}
	return got[SyncCategoryModelPrefs]
}

// TestSyncDirty_OfflineEditThenRemoteChange_IsConflictNotOverwrite: this
// device edits while offline, the PUT fails, another device writes, and the
// network returns. The next poll must surface a conflict, not apply the
// other device's state over the unsent edit. Before the fix the failed PUT
// only set lastErr, and the poll's pull applied {"maxAgentTurns":99}.
func TestSyncDirty_OfflineEditThenRemoteChange_IsConflictNotOverwrite(t *testing.T) {
	fake, base := startOfflineFake(t)
	dir := t.TempDir()
	var open atomic.Bool
	open.Store(true)
	local := &localPrefs{}
	local.set(`{"maxAgentTurns":7}`)
	app := &recordingApplier{}
	s := newDirtySyncer(t, base, dir, &open, local, app)
	ctx := context.Background()

	if err := s.Push(ctx, SyncCategoryModelPrefs); err != nil { // in sync
		t.Fatalf("first Push: %v", err)
	}

	fake.offline.Store(true)
	local.set(`{"maxAgentTurns":8}`) // the offline edit
	if err := s.Push(ctx, SyncCategoryModelPrefs); err == nil {
		t.Fatal("Push while offline succeeded")
	}
	if !persistedModelPrefs(t, dir).Dirty {
		t.Fatal("a failed PUT must persist the category as dirty")
	}

	other := `{"maxAgentTurns":99}`
	fake.put(SyncCategoryModelPrefs, time.Now(), other) // another device
	fake.offline.Store(false)

	// The poll loop's per-category step (pollLoop calls exactly this).
	if err := s.syncOnce(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("syncOnce: %v", err)
	}
	if got := app.snapshot(); len(got) != 0 {
		t.Fatalf("applied %v over the unsent offline edit; want a conflict", got)
	}
	if st := modelPrefsStatus(t, s); !st.Conflict {
		t.Fatalf("status = %+v, want Conflict", st)
	}
	if row, _ := fake.get(SyncCategoryModelPrefs); string(row.Payload) != other {
		t.Fatalf("Fleet row = %s; the other device's write must not be overwritten either", row.Payload)
	}
	if p := persistedModelPrefs(t, dir); !p.Conflict || !p.Dirty {
		t.Fatalf("persisted = %+v, want conflict and dirty", p)
	}

	// The user keeps this device's edit: it uploads and the marks clear.
	if err := s.ForcePush(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("ForcePush: %v", err)
	}
	if row, _ := fake.get(SyncCategoryModelPrefs); string(row.Payload) != `{"maxAgentTurns":8}` {
		t.Fatalf("Fleet row after ForcePush = %s", row.Payload)
	}
	if p := persistedModelPrefs(t, dir); p.Conflict || p.Dirty {
		t.Fatalf("persisted after ForcePush = %+v, want clean", p)
	}
}

// TestSyncDirty_OfflineEditSurvivesRestartAndUploads: an edit made offline
// (the app quit inside the 5s debounce window) is still pending after a
// restart and uploads once Fleet is reachable, through the consent gate and
// the clock check. Before the fix nothing was persisted or retried: the
// restarted poll pulled (304) and the edit never left the device.
func TestSyncDirty_OfflineEditSurvivesRestartAndUploads(t *testing.T) {
	fake, base := startOfflineFake(t)
	dir := t.TempDir()
	var open atomic.Bool
	open.Store(true)
	local := &localPrefs{}
	local.set(`{"maxAgentTurns":7}`)
	ctx := context.Background()

	s1 := newDirtySyncer(t, base, dir, &open, local, &recordingApplier{})
	if err := s1.Push(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	fake.offline.Store(true)
	local.set(`{"maxAgentTurns":8}`)
	s1.NotifyMutation(SyncCategoryModelPrefs)
	s1.Stop() // quit before the debounced push fires

	raw, err := os.ReadFile(filepath.Join(dir, syncStateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !persistedModelPrefs(t, dir).Dirty {
		t.Fatalf("state file after an offline edit = %s, want dirty", raw)
	}

	// Restart, still offline: the retry fails and the mark stays.
	app := &recordingApplier{}
	s2 := newDirtySyncer(t, base, dir, &open, local, app)
	if err := s2.syncOnce(ctx, SyncCategoryModelPrefs); err == nil {
		t.Fatal("syncOnce while offline reported success")
	}
	putsBefore := fake.countMethod(http.MethodPut)

	// Online, but the clock is 5 minutes off: the retry is refused.
	fake.offline.Store(false)
	fake.mu.Lock()
	fake.now = func() time.Time { return time.Now().Add(-5 * time.Minute) }
	fake.mu.Unlock()
	_ = s2.syncOnce(ctx, SyncCategoryModelPrefs) // refused: ErrSyncClockMismatch
	if n := fake.countMethod(http.MethodPut); n != putsBefore {
		t.Fatalf("skewed clock: %d PUTs sent", n-putsBefore)
	}

	// Clock fixed: the pending edit uploads.
	fake.mu.Lock()
	fake.now = time.Now
	fake.mu.Unlock()
	if err := s2.syncOnce(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("syncOnce online: %v", err)
	}
	if row, _ := fake.get(SyncCategoryModelPrefs); string(row.Payload) != `{"maxAgentTurns":8}` {
		t.Fatalf("Fleet row = %s, want the offline edit", row.Payload)
	}
	if len(app.snapshot()) != 0 {
		t.Error("nothing should have been applied")
	}
	if p := persistedModelPrefs(t, dir); p.Dirty {
		t.Fatalf("persisted after upload = %+v, want dirty cleared", p)
	}
	// And it is not uploaded again.
	puts := fake.countMethod(http.MethodPut)
	if err := s2.syncOnce(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatal(err)
	}
	if n := fake.countMethod(http.MethodPut); n != puts {
		t.Fatalf("a clean category re-uploaded (%d PUTs)", n-puts)
	}
}

// TestSyncDirty_RetryRespectsConsentGate: a pending upload does not leave
// the device while the upload gate is closed.
func TestSyncDirty_RetryRespectsConsentGate(t *testing.T) {
	fake, base := startOfflineFake(t)
	dir := t.TempDir()
	if err := NewFileSyncStateStore(dir).SaveSyncState(map[SyncCategory]PersistedSyncState{
		SyncCategoryModelPrefs: {Dirty: true},
	}); err != nil {
		t.Fatal(err)
	}
	var open atomic.Bool // closed
	local := &localPrefs{}
	local.set(`{"maxAgentTurns":8}`)
	s := newDirtySyncer(t, base, dir, &open, local, &recordingApplier{})
	if err := s.syncOnce(context.Background(), SyncCategoryModelPrefs); err != nil {
		t.Fatalf("syncOnce: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("%d PUTs sent with the upload gate closed", n)
	}
	if !persistedModelPrefs(t, dir).Dirty {
		t.Fatal("the pending mark must survive a held retry")
	}
}

// TestSyncDirty_ContentRefusalIsNotRetried: a PUT Fleet refused for its
// content stays pending but is not resent until the next local change.
func TestSyncDirty_ContentRefusalIsNotRetried(t *testing.T) {
	fake, base := startOfflineFake(t)
	dir := t.TempDir()
	var open atomic.Bool
	open.Store(true)
	local := &localPrefs{}
	local.set(`{"maxAgentTurns":8}`)
	s := newDirtySyncer(t, base, dir, &open, local, &recordingApplier{})
	ctx := context.Background()
	s.markDirty(SyncCategoryModelPrefs)
	fake.mu.Lock()
	fake.putStatusOverride, fake.putCodeOverride = http.StatusUnprocessableEntity, "schema_violation"
	fake.mu.Unlock()
	if err := s.syncOnce(ctx, SyncCategoryModelPrefs); err == nil {
		t.Fatal("want the schema refusal reported")
	}
	puts := fake.countMethod(http.MethodPut)
	if err := s.syncOnce(ctx, SyncCategoryModelPrefs); err != nil {
		t.Fatalf("second syncOnce: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != puts {
		t.Fatalf("a refused payload was resent %d times", n-puts)
	}
	if !isDirty(s.categories[SyncCategoryModelPrefs]) {
		t.Fatal("refused change must stay pending")
	}
}
