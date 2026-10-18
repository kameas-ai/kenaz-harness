package memory

import (
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWall is a settable wall clock for HLC tests.
type fakeWall struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeWall) now() time.Time  { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeWall) set(t time.Time) { f.mu.Lock(); f.t = t; f.mu.Unlock() }

func newTestHLC(t *testing.T, node, path string, w *fakeWall) *HLC {
	t.Helper()
	h, err := NewHLC(node, path)
	if err != nil {
		t.Fatalf("NewHLC: %v", err)
	}
	h.SetNow(w.now)
	return h
}

func TestHLC_WireFormatMatchesFleetGrammar(t *testing.T) {
	w := &fakeWall{t: time.UnixMilli(1759784645123)}
	h := newTestHLC(t, "01HZNODE7F3C", "", w)
	got := h.Tick()
	if got != "0001759784645123:000000:01HZNODE7F3C" {
		t.Fatalf("Tick = %q", got)
	}
	if _, _, node, ok := ParseHLC(got); !ok || node != "01HZNODE7F3C" {
		t.Fatalf("ParseHLC(%q) failed", got)
	}
	if _, err := NewHLC("bad node!", ""); err == nil {
		t.Fatal("node id outside [A-Za-z0-9._-]{1,64} must be refused")
	}
}

func TestHLC_TickMonotone_SameMsAndWallRegression(t *testing.T) {
	w := &fakeWall{t: time.UnixMilli(5_000)}
	h := newTestHLC(t, "dev", "", w)
	a := h.Tick()
	b := h.Tick()                // same ms ⇒ counter++
	w.set(time.UnixMilli(1_000)) // NTP stepped the wall clock back
	c := h.Tick()
	w.set(time.UnixMilli(9_000))
	d := h.Tick()
	if !(a < b && b < c && c < d) {
		t.Fatalf("not strictly monotone: %s %s %s %s", a, b, c, d)
	}
	if !strings.HasPrefix(c, "0000000000005000:000002:") {
		t.Fatalf("regressed wall must keep last wall and bump counter, got %s", c)
	}
	if !strings.HasPrefix(d, "0000000000009000:000000:") {
		t.Fatalf("advanced wall resets counter, got %s", d)
	}
}

func TestHLC_CounterOverflowAdvancesWall(t *testing.T) {
	w := &fakeWall{t: time.UnixMilli(7_000)}
	h := newTestHLC(t, "dev", "", w)
	h.wallMS, h.counter = 7_000, HLCMaxCounter
	got := h.Tick()
	if got != FormatHLC(7_001, 0, "dev") {
		t.Fatalf("overflow tick = %s", got)
	}
}

// TestHLC_ObserveReceiveRule: after observing a remote stamp the next local
// tick sorts after it, even when the local wall clock is behind.
func TestHLC_ObserveReceiveRule(t *testing.T) {
	w := &fakeWall{t: time.UnixMilli(1_000)}
	h := newTestHLC(t, "devA", "", w)
	_ = h.Tick()
	remote := FormatHLC(60_000, 41, "devB") // B's clock is a minute ahead
	h.Observe(remote)
	next := h.Tick()
	if !(next > remote) {
		t.Fatalf("local tick %s must sort after observed %s", next, remote)
	}
	// Equal wall: counter = max(local, remote)+1.
	h2 := newTestHLC(t, "devA", "", &fakeWall{t: time.UnixMilli(500)})
	h2.wallMS, h2.counter = 60_000, 3
	h2.Observe(FormatHLC(60_000, 9, "devB"))
	if wm, c := h2.Snapshot(); wm != 60_000 || c != 10 {
		t.Fatalf("equal-wall receive = (%d,%d), want (60000,10)", wm, c)
	}
	// Garbage is ignored.
	before, bc := h2.Snapshot()
	h2.Observe("not-an-hlc")
	h2.Observe("")
	if a, ac := h2.Snapshot(); a != before || ac != bc {
		t.Fatal("malformed remote must not move the clock")
	}
}

// TestHLC_OfflineEditOrdering (AC-1 at the clock level): an edit made
// earlier on an offline device loses to a later edit on an online device
// regardless of which one is pushed first — the LWW comparison is by HLC,
// not by arrival.
func TestHLC_OfflineEditOrdering(t *testing.T) {
	w := &fakeWall{t: time.UnixMilli(100_000)}
	a := newTestHLC(t, "devA", "", w)
	b := newTestHLC(t, "devB", "", w)
	offlineEdit := a.Tick() // A edits at t=100s, then goes offline
	w.set(time.UnixMilli(200_000))
	onlineEdit := b.Tick() // B edits later and pushes first
	if !(onlineEdit > offlineEdit) {
		t.Fatalf("later edit %s must win over earlier %s", onlineEdit, offlineEdit)
	}
}

// TestHLC_PersistsAcrossRestart drives the real state file.
func TestHLC_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := HLCStatePath(dir)
	w := &fakeWall{t: time.UnixMilli(50_000)}
	h := newTestHLC(t, "dev", path, w)
	_ = h.Tick()
	last := h.Tick()
	if err := h.SaveErr(); err != nil {
		t.Fatalf("save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
	// Restart with the wall clock set BEFORE the persisted wall: the
	// reopened clock must still sort after everything it handed out.
	w2 := &fakeWall{t: time.UnixMilli(10_000)}
	h2 := newTestHLC(t, "dev", path, w2)
	if next := h2.Tick(); !(next > last) {
		t.Fatalf("after restart %s must sort after %s", next, last)
	}
	// A corrupt state file is an error, never a silent reset to zero.
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHLC("dev", path); err == nil {
		t.Fatal("corrupt hlc state must surface as an error")
	}
}

// TestHLC_FastClockSurface: a device >5 min fast reports how far ahead it
// is relative to the server's time, which the sync lane names as the cause
// of clock_in_future rejections.
func TestHLC_FastClockSurface(t *testing.T) {
	server := time.UnixMilli(1_000_000)
	w := &fakeWall{t: server.Add(7 * time.Minute)}
	h := newTestHLC(t, "dev", "", w)
	_ = h.Tick()
	if ahead := h.AheadOf(server); ahead <= HLCMaxSkew {
		t.Fatalf("AheadOf = %v, want > %v", ahead, HLCMaxSkew)
	}
	w.set(server)
	h2 := newTestHLC(t, "dev", "", w)
	_ = h2.Tick()
	if ahead := h2.AheadOf(server); ahead != 0 {
		t.Fatalf("in-sync clock AheadOf = %v", ahead)
	}
}

// TestStore_StampsEveryLocalMutation drives the real gob store: create,
// pin and promote each stamp their field's HLC; a reopen reads the stamps
// back from disk.
func TestStore_StampsEveryLocalMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.gob")
	st, err := NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	w := &fakeWall{t: time.UnixMilli(1_000_000)}
	clock := newTestHLC(t, "dev", HLCStatePath(dir), w)
	st.(ClockSetter).SetClock(clock)
	ctx := context.Background()
	if err := st.Add(ctx, Chunk{ID: "m1", Content: "x", Title: "t", ScopeKind: ScopeKindGlobal,
		Embedding: []float32{1}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	c := get(t, st, "m1")
	if c.CreatedHLC == "" || c.ScopeHLC != c.CreatedHLC || c.TitleHLC != c.CreatedHLC {
		t.Fatalf("create stamps = %+v", c)
	}
	if !c.SyncDirty || c.FleetMayKnow() {
		t.Fatalf("new chunk: dirty=%v fleetMayKnow=%v", c.SyncDirty, c.FleetMayKnow())
	}
	if err := st.(PruneCapable).SetPinned(ctx, "m1", true); err != nil {
		t.Fatal(err)
	}
	c2 := get(t, st, "m1")
	if !(c2.PinnedHLC > c.CreatedHLC) {
		t.Fatalf("pin HLC %q must sort after create %q", c2.PinnedHLC, c.CreatedHLC)
	}
	// Reopen: stamps persisted in the gob.
	st2, err := NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c3 := get(t, st2, "m1")
	if c3.PinnedHLC != c2.PinnedHLC || c3.CreatedHLC != c.CreatedHLC || c3.TitleHLC != c.TitleHLC {
		t.Fatalf("stamps lost on reopen: %+v", c3)
	}
}

// legacyChunkV0 is the chunk shape as of v0.91.0 (before memory sync):
// none of the HLC / recall-split / sync fields exist.
type legacyChunkV0 struct {
	ID              string
	SessionID       string
	ScopeKind       string
	ScopeID         string
	Content         string
	ContentHash     string
	Title           string
	Embedding       []float32
	CreatedAt       time.Time
	Pinned          bool
	RecallCount     int
	LastAccessed    time.Time
	Kind            string
	RetrievalWeight float32
}

// TestStore_LegacyGobBackfill: a pre-mission memory.gob (no sync fields)
// loads, migrates RecallCount → RecallOwn, reads as never-synced and
// unstamped, and survives a write + reopen with the migration intact.
func TestStore_LegacyGobBackfill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.gob")
	created := time.Now().Add(-time.Hour).UTC()
	legacy := []legacyChunkV0{{ID: "mem-old", ScopeKind: ScopeKindGlobal, Content: "c", Title: "t",
		Embedding: []float32{1, 0}, CreatedAt: created, RecallCount: 7, Kind: "raw", RetrievalWeight: 1}}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(legacy); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	st, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("legacy gob must load: %v", err)
	}
	c := get(t, st, "mem-old")
	if c.RecallOwn != 7 || c.RecallCount != 7 || c.RecallOthers != 0 {
		t.Fatalf("recall backfill = own %d total %d others %d", c.RecallOwn, c.RecallCount, c.RecallOthers)
	}
	if c.CreatedHLC != "" || c.TitleHLC != "" || c.FleetMayKnow() || c.SyncBlocked != "" || c.EmbedPending {
		t.Fatalf("legacy chunk must read as unstamped + never synced: %+v", c)
	}
	// A recall after migration adds to the migrated counter, not over it.
	if err := st.(PruneCapable).MarkAccessed(context.Background(), []string{"mem-old"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st2, err := NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c2 := get(t, st2, "mem-old")
	if c2.RecallOwn != 8 || c2.RecallCount != 8 {
		t.Fatalf("after recall + reopen: own %d total %d, want 8/8", c2.RecallOwn, c2.RecallCount)
	}
}

func get(t *testing.T, st Store, id string) Chunk {
	t.Helper()
	all, err := st.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("chunk %s not found", id)
	return Chunk{}
}
