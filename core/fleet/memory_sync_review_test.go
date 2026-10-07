package fleet

// Regression tests for the memory-sync review findings (F1–F11). Each
// TestMemorySync_F* started life as one of the reviewer's executable
// probes (core/fleet/zz_probe*_test.go in the review copy) and failed
// against the pre-fix branch.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

// F1: a record pushed to Fleet AFTER a forget-all must survive an offline
// device's erase replay. Pulled chunks carry CreatedHLC "" (Fleet does not
// send it), which sorted below every erased_before.
func TestMemorySync_F1_EraseReplayKeepsPostEraseRecords(t *testing.T) {
	w := newMemWorld(t)
	a, b, c := w.device("devA", 0), w.device("devB", 0), w.device("devC", 0)
	a.enable()
	b.enable()
	c.enable()
	a.add("mem-old", "global", "pre-erase")
	a.sync()
	b.sync()
	c.sync()
	w.wall.advance(time.Second)
	if _, err := a.ms.Disable(context.Background(), true, "forget-all"); err != nil {
		t.Fatal(err)
	}
	w.fleet.mu.Lock()
	w.fleet.enabled = true // user re-opts in elsewhere
	w.fleet.mu.Unlock()
	w.wall.advance(time.Second)
	c.add("mem-post", "global", "created after the erase on C")
	c.sync() // C replays the erase, then pushes mem-post
	if w.fleet.live("mem-post") == nil {
		t.Fatal("setup: mem-post not on Fleet")
	}
	w.wall.advance(time.Second)
	b.sync() // B: erased reset → snapshot holds mem-post → erase replay
	b.sync()
	bc := b.chunks()
	if _, ok := bc["mem-post"]; !ok {
		t.Fatalf("B lost mem-post, a record created AFTER the erase: %v", keys(bc))
	}
	if _, ok := bc["mem-old"]; ok {
		t.Fatal("B kept mem-old, which the erase removed")
	}
}

// F2: "delete from Fleet" is one operation, disable-first: no cycle can run
// between the erase and the opt-out and re-upload everything.
func TestMemorySync_F2_DeleteFromFleetNeverReuploads(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.enable()
	a.add("mem-1", "global", "private")
	a.sync()
	if _, err := a.ms.Disable(context.Background(), true, "forget-all"); err != nil {
		t.Fatal(err)
	}
	a.ms.RunOnce(context.Background()) // a tick right after
	if w.fleet.live("mem-1") != nil {
		t.Fatal("a cycle after delete-from-Fleet re-uploaded mem-1")
	}
	if _, ok := a.chunks()["mem-1"]; !ok {
		t.Fatal("delete from Fleet must not delete here")
	}
}

// F2: a failed disable PUT leaves the device OFF (never re-enabled, nothing
// re-uploaded); the lane retries the PUT and the panel cannot flip it back.
func TestMemorySync_F2_FailedDisableStaysOffAndRetries(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.enable()
	a.add("mem-1", "global", "private")
	a.sync()
	w.fleet.mu.Lock()
	w.fleet.failPut = 3 // do() retries 5xx three times
	w.fleet.mu.Unlock()
	if _, err := a.ms.Disable(context.Background(), true, "forget-all"); err == nil {
		t.Fatal("a failed PUT must surface as an error")
	}
	if st := a.ms.state(); st.Enabled || !st.DisablePending {
		t.Fatalf("after failed disable: %+v", st)
	}
	if w.fleet.live("mem-1") == nil {
		t.Fatal("forget-all must not run before Fleet confirms the opt-out")
	}
	if s := a.ms.Status(context.Background()); s.LocalEnabled {
		t.Fatal("opening the panel re-enabled a device whose user turned sync off")
	}
	a.ms.RunOnce(context.Background()) // lane retries the PUT
	w.fleet.mu.Lock()
	on := w.fleet.enabled
	w.fleet.mu.Unlock()
	if on || a.ms.state().DisablePending {
		t.Fatalf("disable retry: fleet enabled=%v pending=%v", on, a.ms.state().DisablePending)
	}
}

func seedTombstones(w *memWorld, n int) {
	w.fleet.mu.Lock()
	defer w.fleet.mu.Unlock()
	for i := 0; i < n; i++ {
		id := "mem-x" + strconv.Itoa(i)
		rec := &fakeMemRec{id: id, state: "tombstone", reason: "forgotten", recall: map[string]int64{}}
		w.fleet.recs[id] = rec
		w.fleet.bump(rec)
	}
}

// F3: a reset snapshot larger than one cycle's pull budget (40 pages)
// completes across cycles; push resumes.
func TestMemorySync_F3_LargeResetCompletesAcrossCycles(t *testing.T) {
	w := newMemWorld(t)
	b := w.device("devB", 0)
	b.enable()
	b.add("mem-b", "global", "B")
	b.sync()
	seedTombstones(w, 20500)
	w.fleet.mu.Lock()
	w.fleet.floor = w.fleet.seq - 5
	w.fleet.mu.Unlock()
	b.add("mem-b2", "global", "B new work")
	for i := 0; i < 3; i++ {
		b.ms.RunOnce(context.Background())
		w.wall.advance(3 * time.Minute)
	}
	st := b.ms.state()
	if st.ResetPending != "" || w.fleet.live("mem-b2") == nil {
		t.Fatalf("reset over >40 pages never finished (pending=%q pushed=%v)", st.ResetPending, w.fleet.live("mem-b2") != nil)
	}
	if _, ok := b.chunks()["mem-b"]; !ok {
		t.Fatal("snapshot row lost by the epoch sweep")
	}
}

// F4: no push while the feed is mid-snapshot; base_cursor is never a
// mid-snapshot cursor.
func TestMemorySync_F4_NoPushMidSnapshot(t *testing.T) {
	w := newMemWorld(t)
	b := w.device("devB", 0)
	seedTombstones(w, 20500)
	b.enable()
	b.add("mem-b", "global", "B")
	b.ms.RunOnce(context.Background())
	for _, p := range w.fleet.pushLog() {
		if len(p.Items) > 0 {
			t.Fatalf("pushed %d items mid-snapshot with base_cursor %q", len(p.Items), p.BaseCursor)
		}
	}
	b.ms.RunOnce(context.Background())
	w.fleet.mu.Lock()
	final := strconv.FormatInt(w.fleet.seq-1, 10) // mem-b's own create bumps seq
	w.fleet.mu.Unlock()
	pushed := false
	for _, p := range w.fleet.pushLog() {
		if len(p.Items) > 0 {
			pushed = true
			if seqLess(p.BaseCursor, final) {
				t.Fatalf("push base_cursor %q is behind the end of the feed %q", p.BaseCursor, final)
			}
		}
	}
	if !pushed || w.fleet.live("mem-b") == nil {
		t.Fatal("push never happened after the snapshot completed")
	}
}

// F5: a queued forget is acked only by a "forgotten" tombstone; a
// left_sync_scope tombstone must not swallow it.
func TestMemorySync_F5_ForgetSurvivesLeftScopeTombstone(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-z", "global", "zzz")
	a.sync()
	b.sync()
	ctx := context.Background()
	if err := a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-z", memory.ScopeKindSession, "s1"); err != nil {
		t.Fatal(err)
	}
	a.sync() // Fleet: left_sync_scope
	b.forget("mem-z")
	b.sync() // pull sees left_sync_scope; the forget must still be sent
	w.wall.advance(time.Second)
	if err := a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-z", memory.ScopeKindGlobal, ""); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	if _, ok := b.chunks()["mem-z"]; ok {
		t.Fatal("B's user forgot mem-z, yet it came back after A re-promoted")
	}
	if w.fleet.live("mem-z") != nil {
		t.Fatal("an explicit forget must be absorbing on Fleet")
	}
}

// Probe P5: an edit made while a push is in flight keeps its dirty flag.
func TestMemorySync_InFlightEditStaysDirty(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.add("mem-e", "global", "edit me")
	ctx := context.Background()
	sent, err := a.store.MarkSyncSent(ctx, []string{"mem-e"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	gen := sent["mem-e"].SyncGen
	_ = a.store.(memory.PruneCapable).SetPinned(ctx, "mem-e", true)
	if err := a.store.ApplySyncOutcomes(ctx, []memory.SyncOutcome{{ID: "mem-e", Gen: gen, Kind: memory.OutcomeSynced}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if c := a.chunks()["mem-e"]; !c.SyncDirty {
		t.Fatal("in-flight edit lost its dirty flag")
	}
}

// F6: recall made under a demoted-then-revived record still converges —
// a device whose own counter exceeds Fleet's marks itself for push.
func TestMemorySync_F6_RecallConvergesAfterDemoteRevive(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-r", "global", "r")
	a.sync()
	b.sync()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = a.store.(memory.PruneCapable).MarkAccessed(ctx, []string{"mem-r"}, time.Now())
	}
	for i := 0; i < 3; i++ {
		_ = b.store.(memory.PruneCapable).MarkAccessed(ctx, []string{"mem-r"}, time.Now())
	}
	a.sync()
	b.sync()
	a.sync()
	_ = a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-r", "session", "s")
	a.sync()
	b.sync()
	w.wall.advance(time.Second)
	_ = a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-r", "global", "")
	for i := 0; i < 3; i++ {
		a.sync()
		b.sync()
	}
	ca, cb := a.chunks()["mem-r"], b.chunks()["mem-r"]
	if ca.RecallCount != cb.RecallCount || int64(ca.RecallCount) != w.fleet.total("mem-r") {
		t.Fatalf("recall diverged: A=%d B=%d Fleet=%d", ca.RecallCount, cb.RecallCount, w.fleet.total("mem-r"))
	}
}

// F6 + the reviewer's extended property test: demotion in the op mix, 60
// steps, and both devices must agree with each other AND with Fleet's live
// set. MEMSYNC_SEEDS raises the seed count (the review ran 500).
func TestMemorySync_Property_WithDemotion(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("MEMSYNC_SEEDS"))
	if n == 0 {
		n = 40
	}
	for seed := int64(1); seed <= int64(n); seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) { runDemotionProperty(t, seed) })
	}
}

func runDemotionProperty(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	w := newMemWorld(t)
	devs := []*memDevice{w.device("devA", 0), w.device("devB", 0)}
	for _, d := range devs {
		d.enable()
	}
	ids := []string{"m0", "m1", "m2", "m3"}
	for i, id := range ids {
		devs[i%2].add(id, "global", "content "+id)
	}
	for r := 0; r < 2; r++ {
		for _, d := range devs {
			d.sync()
		}
	}
	ctx := context.Background()
	for step := 0; step < 60; step++ {
		d := devs[rng.Intn(2)]
		id := ids[rng.Intn(len(ids))]
		w.wall.advance(time.Duration(rng.Intn(5)+1) * time.Millisecond)
		if _, ok := d.chunks()[id]; !ok {
			if rng.Intn(3) == 0 {
				d.sync()
			}
			continue
		}
		switch rng.Intn(7) {
		case 0:
			_ = d.store.(memory.PruneCapable).SetPinned(ctx, id, rng.Intn(2) == 0)
		case 1:
			_ = d.store.(memory.ScopePromoter).PromoteScope(ctx, id, []string{"global", "long_term"}[rng.Intn(2)], "")
		case 2:
			_ = d.store.(memory.PruneCapable).MarkAccessed(ctx, []string{id}, time.Now())
		case 3:
			if rng.Intn(4) == 0 {
				d.forget(id)
			}
		case 6:
			_ = d.store.(memory.ScopePromoter).PromoteScope(ctx, id, "session", "s1")
		default:
			d.sync()
		}
	}
	for round := 0; round < 3; round++ {
		for _, d := range devs {
			d.sync()
		}
	}
	type view struct {
		pinned bool
		scope  string
		recall int
	}
	snap := func(d *memDevice) map[string]view {
		out := map[string]view{}
		for id, c := range d.chunks() {
			if !memory.IsSyncScope(c.ScopeKind) {
				continue // demoted copies are device-local by ruling
			}
			out[id] = view{c.Pinned, c.ScopeKind, c.RecallCount}
		}
		return out
	}
	va, vb := snap(devs[0]), snap(devs[1])
	if fmt.Sprint(sortedViews(va)) != fmt.Sprint(sortedViews(vb)) {
		t.Fatalf("diverged:\nA=%v\nB=%v", sortedViews(va), sortedViews(vb))
	}
	w.fleet.mu.Lock()
	live := map[string]bool{}
	for id, r := range w.fleet.recs {
		if r.state == "live" {
			live[id] = true
		}
	}
	w.fleet.mu.Unlock()
	for id := range va {
		if !live[id] {
			t.Fatalf("A holds %s which is not live on Fleet", id)
		}
	}
	for id := range live {
		if _, ok := va[id]; !ok {
			t.Fatalf("Fleet live %s missing on A", id)
		}
	}
}

// F7: a 413 drops the batch and logs — it does not durably block the
// chunks; the refusal is counted on the lane.
func TestMemorySync_F7_EnvelopeFaultDropsWithoutBlocking(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.enable()
	a.add("mem-big", "global", "too big for this fake")
	w.fleet.mu.Lock()
	w.fleet.status413 = 1 // next item-bearing push answers 413
	w.fleet.mu.Unlock()
	a.ms.RunOnce(context.Background())
	if c := a.chunks()["mem-big"]; c.SyncBlocked != "" {
		t.Fatalf("a dropped batch must not block durably: %+v", c)
	}
	if s := a.lanes.Snapshot(LaneMemorySync); s.Reason != "items_refused" {
		t.Fatalf("lane = %+v, want the refusal counted", s)
	}
	// Changed locally ⇒ resent, and now it goes through.
	_ = a.store.(memory.PruneCapable).SetPinned(context.Background(), "mem-big", true)
	a.sync()
	if w.fleet.live("mem-big") == nil {
		t.Fatal("a changed chunk must be resent after a dropped batch")
	}
}

// F7: a credential-blocked chunk can still leave sync (its demotion push).
func TestMemorySync_F7_SecretBlockedChunkStillDemotes(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-t", "global", "clean content")
	a.sync()
	b.sync()
	// A later edit trips Fleet's credential scan: the record stays live on
	// Fleet while the chunk is durably blocked here.
	cur := a.chunks()["mem-t"]
	if err := a.store.ApplySyncOutcomes(context.Background(), []memory.SyncOutcome{{ID: "mem-t", Gen: cur.SyncGen,
		Kind: memory.OutcomeBlocked, Code: "secret_detected"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if a.chunks()["mem-t"].SyncBlocked != "secret_detected" {
		t.Fatal("setup: not blocked")
	}
	if err := a.store.(memory.ScopePromoter).PromoteScope(context.Background(), "mem-t", memory.ScopeKindSession, "s"); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	if _, ok := b.chunks()["mem-t"]; ok {
		t.Fatal("a secret-blocked chunk's demotion must still reach Fleet")
	}
}

// F9a: a clock that cannot persist turns the lane degraded.
func TestMemorySync_F9_HLCSaveFailureDegradesLane(t *testing.T) {
	w := newMemWorld(t)
	dir := t.TempDir()
	a := w.deviceAt("devA", dir, 0)
	a.enable()
	// Replace the state file with a directory: every save now fails.
	_ = os.Remove(memory.HLCStatePath(dir))
	if err := os.MkdirAll(memory.HLCStatePath(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	a.add("mem-c", "global", "ticks the clock")
	a.ms.RunOnce(context.Background())
	if s := a.lanes.Snapshot(LaneMemorySync); s.Status != LaneDegraded || s.Reason != "hlc_state" {
		t.Fatalf("lane = %+v, want degraded hlc_state", s)
	}
}

// F3 (real Fleet semantics): Fleet answers any non-empty pull cursor below
// the floor with reset:true, so a cursor_expired snapshot with more than one
// page of live rows below the floor could never be paged. The reset is
// taken from the export route instead; 600 old live rows all survive.
func TestMemorySync_F3_ResetWithManyLiveRowsBelowFloor(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	for i := 0; i < 600; i++ {
		a.add(fmt.Sprintf("mem-live-%03d", i), "global", fmt.Sprintf("old live row %d", i))
	}
	a.sync()
	b.sync()
	b.add("mem-b-new", "global", "B's unsynced work")
	seedTombstones(w, 10)
	w.fleet.mu.Lock()
	w.fleet.floor = w.fleet.seq // every live row now sits below the floor
	w.fleet.mu.Unlock()
	b.sync()
	if st := b.ms.state(); st.ResetPending != "" {
		t.Fatalf("reset still pending: %+v", st)
	}
	bc := b.chunks()
	n := 0
	for id := range bc {
		if len(id) > 9 && id[:9] == "mem-live-" {
			n++
		}
	}
	if n != 600 {
		t.Fatalf("B holds %d of 600 live rows after the reset", n)
	}
	if w.fleet.live("mem-b-new") == nil {
		t.Fatal("push did not resume after the reset")
	}
	b.sync() // and the next pull is not another reset
	if st := b.ms.state(); st.ResetPending != "" || seqLess(st.Cursor, strconv.FormatInt(w.fleet.floor, 10)) {
		t.Fatalf("cursor %q left below the floor — reset loop", st.Cursor)
	}
}
