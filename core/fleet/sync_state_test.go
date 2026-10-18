package fleet

// sync_state_test.go — enabling (or confirming) pulls before it pushes, and
// each category's LWW stamp survives a restart through a real file.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingApplier records every payload applied.
type recordingApplier struct {
	mu   sync.Mutex
	got  []string
	seen atomic.Int32
}

func (r *recordingApplier) apply(_ context.Context, raw json.RawMessage) error {
	r.mu.Lock()
	r.got = append(r.got, string(raw))
	r.mu.Unlock()
	r.seen.Add(1)
	return nil
}

func (r *recordingApplier) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

// newStateSyncer builds a Syncer over the fake with model_prefs registered,
// the upload gate wired to open, and (when dir != "") a FileSyncStateStore.
func newStateSyncer(t *testing.T, baseURL, dir string, open *atomic.Bool, local string, app *recordingApplier) *Syncer {
	t.Helper()
	s := NewSyncer(makeTestClient(t, baseURL))
	t.Cleanup(s.Stop)
	if dir != "" {
		if err := s.SetStateStore(NewFileSyncStateStore(dir)); err != nil {
			t.Fatalf("SetStateStore: %v", err)
		}
	}
	s.RegisterCategory(SyncCategoryModelPrefs, CategoryConfig{
		Collector: func(context.Context) (json.RawMessage, error) { return json.RawMessage(local), nil },
		Applier:   app.apply,
	})
	s.SetUploadGate(open.Load)
	return s
}

func startFake(t *testing.T) (*fakeFleetSync, string) {
	t.Helper()
	fake := newFakeFleetSync(t)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-state", RefreshToken: "rt-state", ExpiresAt: time.Now().Add(time.Hour)})
	return fake, srv.URL
}

// TestSyncEnable_PullsBeforePushing: turning a category on on a device that
// has never synced adopts what the other devices agreed on instead of
// overwriting it. Before the fix SetEnabled pushed first: one PUT, Fleet's
// row replaced by this device's defaults, nothing applied.
func TestSyncEnable_PullsBeforePushing(t *testing.T) {
	fake, base := startFake(t)
	other := `{"maxAgentTurns":42}`
	fake.put(SyncCategoryModelPrefs, time.Now().Add(-time.Minute), other)

	var open atomic.Bool
	open.Store(true)
	app := &recordingApplier{}
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, app)

	if err := s.SetEnabled(context.Background(), SyncCategoryModelPrefs, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("enable sent %d PUTs; it must pull first and adopt Fleet's newer state", n)
	}
	if got := app.snapshot(); len(got) != 1 || got[0] != other {
		t.Fatalf("applied %v, want the other device's %s", got, other)
	}
	if row, _ := fake.get(SyncCategoryModelPrefs); string(row.Payload) != other {
		t.Fatalf("Fleet row = %s, want it untouched (%s)", row.Payload, other)
	}
}

// TestSyncEnable_PushesWhenFleetHasNothing: with nothing stored, enabling
// still uploads this device's state (FR-102).
func TestSyncEnable_PushesWhenFleetHasNothing(t *testing.T) {
	fake, base := startFake(t)
	var open atomic.Bool
	open.Store(true)
	app := &recordingApplier{}
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, app)
	if err := s.SetEnabled(context.Background(), SyncCategoryModelPrefs, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 1 {
		t.Fatalf("PUTs = %d, want 1", n)
	}
	if app.seen.Load() != 0 {
		t.Error("the zero-time absent row must not be applied")
	}
}

// TestSyncPushHeld_PullsBeforePushing: a category enabled before the
// confirmation, while another device uploads, must not overwrite that
// upload when the user finally confirms. Before the fix PushHeld pushed
// straight away with a fresh stamp.
func TestSyncPushHeld_PullsBeforePushing(t *testing.T) {
	fake, base := startFake(t)
	var open atomic.Bool // closed: not confirmed yet
	app := &recordingApplier{}
	s := newStateSyncer(t, base, "", &open, `{"maxAgentTurns":7}`, app)
	if err := s.SetEnabled(context.Background(), SyncCategoryModelPrefs, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	other := `{"maxAgentTurns":42}`
	fake.put(SyncCategoryModelPrefs, time.Now(), other) // another device, later

	open.Store(true)
	if err := s.PushHeld(context.Background()); err != nil {
		t.Fatalf("PushHeld: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("PushHeld sent %d PUTs over the other device's newer upload", n)
	}
	if got := app.snapshot(); len(got) != 1 || got[0] != other {
		t.Fatalf("applied %v, want %s", got, other)
	}
}

// TestSyncState_LWWSurvivesRestart: the stamp this device last agreed with
// Fleet on is written to a real file and read back by a new Syncer, which
// then pulls with ?since= and stamps its next push above it. Before the fix
// a restarted Syncer started from 0: no ?since=, no floor.
func TestSyncState_LWWSurvivesRestart(t *testing.T) {
	fake, base := startFake(t)
	dir := t.TempDir()
	var open atomic.Bool
	open.Store(true)

	s1 := newStateSyncer(t, base, dir, &open, `{"maxAgentTurns":7}`, &recordingApplier{})
	if err := s1.SetEnabled(context.Background(), SyncCategoryModelPrefs, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	row, ok := fake.get(SyncCategoryModelPrefs)
	if !ok {
		t.Fatal("first launch did not upload")
	}
	s1.Stop()

	path := filepath.Join(dir, syncStateFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode = %o, want 600", perm)
	}

	// Restart: a new Syncer over the same directory.
	s2 := newStateSyncer(t, base, dir, &open, `{"maxAgentTurns":7}`, &recordingApplier{})
	var got int64
	for _, st := range s2.Status() {
		if st.Category == SyncCategoryModelPrefs {
			got = st.LWWTS
		}
	}
	if got != row.LWWTs.UnixNano() {
		t.Fatalf("restored lww_ts = %d, want Fleet's stored %d", got, row.LWWTs.UnixNano())
	}
	if err := s2.RestoreEnabled(SyncCategoryModelPrefs, true); err != nil {
		t.Fatal(err)
	}
	if err := s2.Pull(context.Background(), SyncCategoryModelPrefs); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	reqs := fake.snapshot()
	last := reqs[len(reqs)-1]
	q, _ := url.ParseQuery(last.Query)
	if since := q.Get("since"); since != formatLWW(row.LWWTs.UnixNano()) {
		t.Fatalf("pull after restart sent since=%q, want %q", since, formatLWW(row.LWWTs.UnixNano()))
	}
}

// TestSyncState_LateCategoryKeptAndApplied: an entry for a category that
// registers after SetStateStore (slash_commands registers late) is applied
// when it registers, and not dropped from the file meanwhile.
func TestSyncState_LateCategoryKeptAndApplied(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSyncStateStore(dir)
	ts := time.Date(2026, 10, 15, 12, 0, 0, 123456000, time.UTC).UnixNano()
	late := SyncCategory("late_kind")
	if err := store.SaveSyncState(map[SyncCategory]PersistedSyncState{late: {LWWTs: formatLWW(ts)}}); err != nil {
		t.Fatal(err)
	}
	s := NewSyncer(nil)
	t.Cleanup(s.Stop)
	if err := s.SetStateStore(store); err != nil {
		t.Fatal(err)
	}
	s.persist() // a save before the late category registers
	if got, _ := store.LoadSyncState(); got[late].LWWTs != formatLWW(ts) {
		t.Fatalf("late entry dropped before registration: %+v", got)
	}
	s.RegisterCategory(late, CategoryConfig{})
	for _, st := range s.Status() {
		if st.Category == late && st.LWWTS != ts {
			t.Fatalf("late category lww_ts = %d, want %d", st.LWWTS, ts)
		}
	}
}
