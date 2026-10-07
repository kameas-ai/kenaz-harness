package memory

// Regressions for the memory-sync review (F1, F8, F9). The HLC test is the
// reviewer's probe (zz_hlc_probe_test.go) made into assertions.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Reviewer probe: same-ms burst across the counter rollover, a 6-hour NTP
// step back, observing a far-behind and a max-counter remote, restart, and
// corrupt / out-of-range / empty state files.
func TestHLC_ReviewProbe_BurstRolloverStepBackRestart(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fleet", "memory_hlc.json")
	h, err := NewHLC("devA", p)
	if err != nil {
		t.Fatal(err)
	}
	base := time.UnixMilli(1759784645123)
	now := base
	h.SetNow(func() time.Time { return now })
	prev := ""
	check := func(s string) {
		t.Helper()
		if !(s > prev) {
			t.Fatalf("not monotone: %q <= %q", s, prev)
		}
		if _, _, _, ok := ParseHLC(s); !ok {
			t.Fatalf("bad format %q", s)
		}
		prev = s
	}
	// Same-ms burst across the rollover. The review ran all 10^6 ticks;
	// with fsync-per-tick persistence that is minutes, so the burst starts
	// just below the 6-digit limit — same rollover path, same assertions.
	check(h.Tick())
	h.mu.Lock()
	h.counter = HLCMaxCounter - 3
	h.mu.Unlock()
	for i := 0; i < 10; i++ {
		check(h.Tick())
	}
	if w, _ := h.Snapshot(); w != base.UnixMilli()+1 {
		t.Fatalf("rollover must advance the wall by exactly 1 ms, wall=%d", w-base.UnixMilli())
	}
	now = base.Add(-6 * time.Hour)
	for i := 0; i < 10; i++ {
		check(h.Tick())
	}
	h.Observe(FormatHLC(base.Add(-24*time.Hour).UnixMilli(), 5, "devB"))
	check(h.Tick())
	r := FormatHLC(base.Add(time.Minute).UnixMilli(), HLCMaxCounter, "devB")
	h.Observe(r)
	s := h.Tick()
	if !(s > r) {
		t.Fatalf("tick %q not after observed %q", s, r)
	}
	check(s)
	h2, err := NewHLC("devA", p)
	if err != nil {
		t.Fatal(err)
	}
	h2.SetNow(func() time.Time { return now })
	check(h2.Tick())
	for name, body := range map[string]string{
		"corrupt":      "{garbage",
		"out-of-range": `{"wall_ms":1,"counter":1000000}`,
		"empty":        "",
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewHLC("devA", p); err == nil {
			t.Fatalf("%s state accepted as a fresh clock", name)
		}
	}
}

// F9c: an unreadable clock state is rebuilt from the highest stamped HLC
// (memory.gob + outbox), reports unhealthy until its first clean save, and
// never hands out a stamp that sorts before one already issued.
func TestHLC_RecoverFromStamps(t *testing.T) {
	dir := t.TempDir()
	path := HLCStatePath(dir)
	high := FormatHLC(time.Now().Add(time.Hour).UnixMilli(), 41, "devA")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil { // empty: crash mid-write
		t.Fatal(err)
	}
	h, err := RecoverHLC("devA", path, []string{"", "garbage", FormatHLC(1, 0, "x"), high})
	if err != nil {
		t.Fatal(err)
	}
	if h.HealthErr() == nil {
		t.Fatal("a rebuilt clock must report unhealthy until a clean save")
	}
	if next := h.Tick(); !(next > high) {
		t.Fatalf("rebuilt clock tick %q must sort after the highest stamp %q", next, high)
	}
	if err := h.HealthErr(); err != nil {
		t.Fatalf("after a clean save: %v", err)
	}
	if _, err := NewHLC("devA", path); err != nil {
		t.Fatalf("state file not repaired: %v", err)
	}
}

// F1 (store level): the erase replay never deletes a row the post-erase
// snapshot touched, even though a pulled chunk's CreatedHLC is "".
func TestDeleteCreatedBefore_SparesPostEraseSnapshotRows(t *testing.T) {
	st, err := NewChromemStore(filepath.Join(t.TempDir(), "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	epoch := time.Now().UTC()
	erasedBefore := FormatHLC(epoch.UnixMilli(), 0, "fleet")
	if err := st.(SyncStore).ApplyRemote(ctx, []RemoteRecord{{ID: "pulled-post", Chunk: Chunk{ScopeKind: ScopeKindGlobal, Content: "p"}}}, epoch); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(ctx, Chunk{ID: "local-pre", ScopeKind: ScopeKindGlobal, Content: "l", Embedding: []float32{1},
		CreatedHLC: FormatHLC(epoch.Add(-time.Minute).UnixMilli(), 0, "dev"), CreatedAt: epoch}); err != nil {
		t.Fatal(err)
	}
	n, err := st.(SyncStore).DeleteCreatedBefore(ctx, erasedBefore, epoch)
	if err != nil || n != 1 {
		t.Fatalf("DeleteCreatedBefore = %d, %v", n, err)
	}
	if _, err := st.(ChunkRemover).Remove(ctx, "pulled-post"); err != nil {
		t.Fatal("the post-erase snapshot row was deleted by the erase replay")
	}
}

// F8: deleting a session removes a synced chunk that was demoted into it —
// and, because Fleet may still hold it live, queues its forget.
func TestDeleteSessionMemory_ForgetsDemotedSyncedChunk(t *testing.T) {
	st, err := NewChromemStore(filepath.Join(t.TempDir(), "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	for _, c := range []Chunk{
		{ID: "demoted", ScopeKind: ScopeKindSession, ScopeID: "s1", Content: "d", Embedding: []float32{1}, CreatedAt: now, SyncedAt: now},
		{ID: "plain", ScopeKind: ScopeKindSession, ScopeID: "s1", Content: "p", Embedding: []float32{1}, CreatedAt: now},
	} {
		if err := st.Add(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	ob, _ := OpenForgetOutbox("", nil)
	gone, err := DeleteSessionMemory(ctx, st, ob, "s1")
	if err != nil || len(gone) != 2 {
		t.Fatalf("DeleteSessionMemory = %v, %v", gone, err)
	}
	if !ob.Has("demoted") || ob.Has("plain") {
		t.Fatalf("forgets = %+v; want only the demoted synced chunk", ob.Pending())
	}
}
