package fleet

// Memory sync client tests (memory-sync-01MEMSY01 WP07). Two simulated
// devices — each with its own real data dir (memory.gob, HLC state, forget
// outbox, sync state) — talk to one fakeMemoryFleet over HTTP through the
// production Client. Every local assertion reads the real gob store.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

type testWall struct {
	mu sync.Mutex
	t  time.Time
}

func (w *testWall) now() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.t }
func (w *testWall) advance(d time.Duration) {
	w.mu.Lock()
	w.t = w.t.Add(d)
	w.mu.Unlock()
}

type memDevice struct {
	t      *testing.T
	dir    string
	store  memory.SyncStore
	clock  *memory.HLC
	outbox *memory.ForgetOutbox
	ms     *MemorySync
	lanes  *SyncLanes
	caps   *Capabilities
}

type memWorld struct {
	t     *testing.T
	wall  *testWall
	fleet *fakeMemoryFleet
	srv   *httptest.Server
}

func newMemWorld(t *testing.T) *memWorld {
	t.Helper()
	wall := &testWall{t: time.Now().UTC().Truncate(time.Millisecond)}
	f := newFakeMemoryFleet(wall.now)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if err := SaveTokens(TokenSet{AccessToken: "at-mem", RefreshToken: "rt-mem", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	return &memWorld{t: t, wall: wall, fleet: f, srv: srv}
}

// device builds a device; skew offsets its OS wall clock.
func (w *memWorld) device(node string, skew time.Duration) *memDevice {
	t := w.t
	t.Helper()
	dir := t.TempDir()
	return w.deviceAt(node, dir, skew)
}

func (w *memWorld) deviceAt(node, dir string, skew time.Duration) *memDevice {
	t := w.t
	t.Helper()
	st, err := memory.NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	clock, err := memory.NewHLC(node, memory.HLCStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	clock.SetNow(func() time.Time { return w.wall.now().Add(skew) })
	st.(memory.ClockSetter).SetClock(clock)
	ob, err := memory.OpenForgetOutbox(memory.ForgetOutboxPath(dir), clock)
	if err != nil {
		t.Fatal(err)
	}
	d := &memDevice{t: t, dir: dir, store: st.(memory.SyncStore), clock: clock, outbox: ob, lanes: NewSyncLanes(),
		caps: &Capabilities{Enabled: map[Capability]bool{CapMemorySync: true}, FetchedAt: time.Now()}}
	ms, err := NewMemorySync(MemorySyncConfig{
		Client: newTestClient(t, w.srv), Store: d.store, Clock: clock, Outbox: ob, DataDir: dir,
		Caps: func() *Capabilities { return d.caps }, Lanes: d.lanes, Now: w.wall.now, HomeDir: "/Users/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	d.ms = ms
	return d
}

func (d *memDevice) enable() {
	d.t.Helper()
	if _, err := d.ms.Enable(context.Background(), []string{"long_term", "global"}, MemoryConsentVersion); err != nil {
		d.t.Fatalf("Enable: %v", err)
	}
}

// sync runs one cycle and requires the lane to be healthy.
func (d *memDevice) sync() {
	d.t.Helper()
	d.ms.RunOnce(context.Background())
	if s := d.lanes.Snapshot(LaneMemorySync); s.Status != LaneOK {
		d.t.Fatalf("lane after sync = %+v", s)
	}
}

func (d *memDevice) add(id, scope, content string) {
	d.t.Helper()
	if err := d.store.Add(context.Background(), memory.Chunk{ID: id, ScopeKind: scope, Content: content,
		Title: "t-" + id, Embedding: []float32{1, 0}, CreatedAt: time.Now().UTC()}); err != nil {
		d.t.Fatal(err)
	}
}

// chunks reopens the gob from disk.
func (d *memDevice) chunks() map[string]memory.Chunk {
	d.t.Helper()
	st, err := memory.NewChromemStore(filepath.Join(d.dir, "memory.gob"))
	if err != nil {
		d.t.Fatal(err)
	}
	all, _ := st.List(context.Background())
	out := map[string]memory.Chunk{}
	for _, c := range all {
		out[c.ID] = c
	}
	return out
}

func (d *memDevice) forget(id string) {
	d.t.Helper()
	if err := memory.RemoveForSync(context.Background(), d.store, d.outbox, id); err != nil {
		d.t.Fatal(err)
	}
}

// ── AC-9: fixtures from Fleet's real shapes ─────────────────────────────────

// TestMemorySync_FleetFixtures decodes Fleet-shaped bodies (kenaz-fleet main
// @ 7fb62de, docs/contract-harness-memory.md §3–§5, field sets checked
// against service/memory/types.go) with DisallowUnknownFields: a field
// Fleet sends that the harness does not model fails here, not in prod.
func TestMemorySync_FleetFixtures(t *testing.T) {
	decode := func(name string, v any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", "memory", name))
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var push memPushResponse
	decode("push_response.json", &push)
	if len(push.Results) != 7 || push.Results[1].CanonicalID != "mem-9" || push.Results[2].SupersededBy != "mem-4" ||
		push.Results[4].Retry == nil || *push.Results[4].Retry || push.Results[4].Field != "content" || !*push.Results[5].Retry {
		t.Fatalf("push results = %+v", push.Results)
	}
	var pull memPullResponse
	decode("pull_response.json", &pull)
	live := pull.Records[0]
	if live.State != "live" || live.RecallCount != 19 || live.RecallCountOthers != 12 || live.Aliases[0] != "mem-aaaa" ||
		live.ScopeKind != "long_term" || live.PinnedHLC == "" || pull.Records[2].SupersededBy != "mem-d" || pull.Records[3].Reason != "left_sync_scope" {
		t.Fatalf("pull = %+v", pull)
	}
	if _, _, node, ok := memory.ParseHLC(live.TitleHLC); !ok || node != "mach-7f3c" {
		t.Fatalf("fixture HLC must parse with the harness parser: %q", live.TitleHLC)
	}
	var reset memPullResponse
	decode("pull_reset_erased.json", &reset)
	if !reset.Reset || reset.ResetReason != "erased" || reset.ErasedBefore == "" {
		t.Fatalf("reset = %+v", reset)
	}
	var off memPullResponse
	decode("pull_disabled.json", &off)
	if off.Enabled {
		t.Fatal("disabled fixture")
	}
	var set MemorySyncSettings
	decode("settings.json", &set)
	if set.Usage.MaxRecords != 20000 || len(set.Scopes) != 2 {
		t.Fatalf("settings = %+v", set)
	}
	var env struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	decode("error_rate_limited.json", &env)
	if env.Code != "rate_limited" {
		t.Fatal(env)
	}
	// And the request shape we send round-trips the field names Fleet's
	// PushItem decodes (op/id/content/content_hash/kind/fields{v,hlc}/…).
	pt := true
	w := 1.5
	n := int64(3)
	c := "x"
	raw, _ := json.Marshal(memPushItem{Op: "upsert", ID: "mem-1", Content: &c, ContentHash: "h", Kind: "raw", RetrievalWeight: &w,
		Fields: map[string]memFieldValue{"pinned": {V: pt, HLC: "0000000000000001:000000:d"}}, RecallOwn: &n})
	for _, k := range []string{`"op":"upsert"`, `"content_hash":"h"`, `"retrieval_weight":1.5`, `"fields":{"pinned":{"v":true,"hlc":"`, `"recall_own":3`} {
		if !strings.Contains(string(raw), k) {
			t.Fatalf("push item JSON %s lacks %s", raw, k)
		}
	}
}

// ── AC-7: gates and back-off ────────────────────────────────────────────────

func TestMemorySync_NoRequestWithoutCapabilityOrOptIn(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.add("mem-1", "global", "hello")
	// Not opted in: idle.
	a.ms.RunOnce(context.Background())
	if n := w.fleet.requestCount(); n != 0 {
		t.Fatalf("not opted in: %d requests", n)
	}
	if s := a.lanes.Snapshot(LaneMemorySync); s.Status != LaneOff || s.Reason != "not_opted_in" {
		t.Fatalf("lane = %+v", s)
	}
	// Opted in, then the tier loses the capability: idle again.
	a.enable()
	before := w.fleet.requestCount()
	a.caps = &Capabilities{Enabled: map[Capability]bool{}, FetchedAt: time.Now()}
	a.ms.RunOnce(context.Background())
	if n := w.fleet.requestCount(); n != before {
		t.Fatalf("capability absent: %d new requests", n-before)
	}
	if _, err := a.ms.Enable(context.Background(), []string{"global"}, MemoryConsentVersion); err == nil {
		t.Fatal("Enable must refuse without the capability")
	}
}

func TestMemorySync_BacksOffOn429And403(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.enable()
	w.fleet.mu.Lock()
	w.fleet.status429 = 1
	w.fleet.mu.Unlock()
	a.ms.RunOnce(context.Background())
	s := a.lanes.Snapshot(LaneMemorySync)
	if s.Status != LaneDegraded || s.Reason != "rate_limited" || !s.NextRetryAt.Equal(w.wall.now().Add(120*time.Second)) {
		t.Fatalf("after 429 lane = %+v", s)
	}
	n := w.fleet.requestCount()
	a.ms.RunOnce(context.Background()) // inside Retry-After: no request
	if w.fleet.requestCount() != n {
		t.Fatal("hot loop: a request was made inside Retry-After")
	}
	w.wall.advance(121 * time.Second)
	w.fleet.mu.Lock()
	w.fleet.status403 = 1
	w.fleet.mu.Unlock()
	a.ms.RunOnce(context.Background())
	if s := a.lanes.Snapshot(LaneMemorySync); s.Reason != "capability_not_in_tier" || s.NextRetryAt.Sub(w.wall.now()) < time.Hour {
		t.Fatalf("after 403 lane = %+v", s)
	}
	n = w.fleet.requestCount()
	w.wall.advance(30 * time.Minute)
	a.ms.RunOnce(context.Background())
	if w.fleet.requestCount() != n {
		t.Fatal("403 must back off long")
	}
	w.wall.advance(31 * time.Minute)
	a.sync()
}

// ── AC-1: same origin id across devices; LWW by HLC, not by arrival ─────────

func TestMemorySync_TwoDevices_PromotePinAndOfflineOrdering(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-1", "global", "the build uses make")
	a.add("mem-sess", "session", "session-only, never syncs")
	a.sync()
	b.sync()
	got := b.chunks()["mem-1"]
	if got.ID != "mem-1" || got.Content != "the build uses make" || got.Title != "t-mem-1" || !got.EmbedPending || len(got.Embedding) != 0 {
		t.Fatalf("B's copy = %+v", got)
	}
	if _, leaked := b.chunks()["mem-sess"]; leaked {
		t.Fatal("a session chunk reached another device")
	}
	for _, p := range w.fleet.pushLog() {
		for _, it := range p.Items {
			if it.ID == "mem-sess" {
				t.Fatal("a session chunk was pushed")
			}
		}
	}

	// Promote on A keeps the id; B follows.
	if err := a.store.(memory.ScopePromoter).PromoteScope(context.Background(), "mem-1", memory.ScopeKindLongTerm, ""); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	if c := b.chunks()["mem-1"]; c.ScopeKind != memory.ScopeKindLongTerm {
		t.Fatalf("B scope = %q", c.ScopeKind)
	}

	// A pins while offline (earlier HLC); B unpins later and pushes first;
	// A pushes last. The later edit (B's) must win everywhere.
	pin := a.store.(memory.PruneCapable)
	if err := pin.SetPinned(context.Background(), "mem-1", true); err != nil {
		t.Fatal(err)
	}
	w.wall.advance(2 * time.Second)
	if err := b.store.(memory.PruneCapable).SetPinned(context.Background(), "mem-1", true); err != nil {
		t.Fatal(err)
	}
	if err := b.store.(memory.PruneCapable).SetPinned(context.Background(), "mem-1", false); err != nil {
		t.Fatal(err)
	}
	b.sync()
	a.sync() // A's older pin arrives after B's newer unpin
	b.sync()
	if a.chunks()["mem-1"].Pinned || b.chunks()["mem-1"].Pinned || w.fleet.live("mem-1").pinned {
		t.Fatal("the later unpin must win on A, B and Fleet regardless of push order")
	}
}

// ── AC-2: resummarize ⇒ exactly one live record for the turn on B ───────────

func TestMemorySync_ResummarizeSwapReachesOfflineDevice(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	ctx := context.Background()
	if err := a.store.Add(ctx, memory.Chunk{ID: "mem-old", ScopeKind: "global", TurnID: "turn-1", Kind: "narrative_extractive_fallback",
		Content: "fallback", Embedding: []float32{1, 0}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	// B goes offline; A re-summarizes (new id, forget old) and syncs.
	old := a.chunks()["mem-old"]
	next := memory.FreshIdentity(old, "mem-new")
	next.Content, next.ContentHash, next.Kind = "synthesised", "", "narrative_synthesised"
	if err := memory.ReplaceForSync(ctx, a.store, a.outbox, next, "mem-old"); err != nil {
		t.Fatal(err)
	}
	a.sync()
	a.sync()
	b.sync() // B back online
	var turn []string
	for id, c := range b.chunks() {
		if c.TurnID == "turn-1" {
			turn = append(turn, id)
		}
	}
	if len(turn) != 1 || turn[0] != "mem-new" {
		t.Fatalf("B's live records for turn-1 = %v, want [mem-new]", turn)
	}
	if len(a.outbox.Pending()) != 0 {
		t.Fatal("the forget must be acked once Fleet answered")
	}
}

// ── AC-3: recall sums on Fleet; prune collapse does not inflate recall_own ──

func TestMemorySync_RecallGCounterAndPruneCollapse(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-r", "global", "recall me")
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
	a.sync() // idempotent retry must not double count
	if tot := w.fleet.total("mem-r"); tot != 5 {
		t.Fatalf("Fleet total = %d, want 5", tot)
	}
	if c := a.chunks()["mem-r"]; c.RecallOwn != 2 || c.RecallOthers != 3 || c.RecallCount != 5 {
		t.Fatalf("A recall = own %d others %d total %d", c.RecallOwn, c.RecallOthers, c.RecallCount)
	}
	// A display-only fold (prune collapse) never reaches recall_own.
	if err := a.store.(memory.RecallFolder).FoldRecall(ctx, "mem-r", 40, time.Time{}); err != nil {
		t.Fatal(err)
	}
	_ = a.store.(memory.PruneCapable).MarkAccessed(ctx, []string{"mem-r"}, time.Now())
	a.sync()
	if tot := w.fleet.total("mem-r"); tot != 6 {
		t.Fatalf("Fleet total after fold + 1 real recall = %d, want 6", tot)
	}
}

// ── AC-5: forget, forget-all (offline replay), prune is local ───────────────

func TestMemorySync_ForgetForgetAllAndPrune(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-f", "global", "forget me")
	a.add("mem-p", "global", "prune me locally")
	a.sync()
	b.sync()
	// Prune on A is device-local: plain Delete, nothing queued, B keeps it.
	if err := a.store.Delete(context.Background(), "mem-p"); err != nil {
		t.Fatal(err)
	}
	a.forget("mem-f")
	a.sync()
	b.sync()
	bc := b.chunks()
	if _, ok := bc["mem-f"]; ok {
		t.Fatal("forget on A must delete on B")
	}
	if _, ok := bc["mem-p"]; !ok {
		t.Fatal("prune on A must change nothing on B")
	}

	// forget-all from A while B is offline; B also holds an UNSYNCED
	// global chunk created before the erase — it must go too.
	b.add("mem-b-local", "global", "created before the erase, never pushed")
	b.add("mem-b-session", "session", "local session memory is not Fleet's to erase")
	w.wall.advance(time.Second)
	if _, err := a.ms.Disable(context.Background(), true, "nope"); err == nil {
		t.Fatal("forget-all must require the confirmation string")
	}
	if n, err := a.ms.Disable(context.Background(), true, "forget-all"); err != nil || n != 1 {
		t.Fatalf("Disable+forget-all = %d, %v", n, err)
	}
	// Fleet's opt-in is user-level: B must be opted back in to observe the
	// erase replay (in production B learns "disabled" and stays off).
	w.fleet.mu.Lock()
	w.fleet.enabled = true
	w.fleet.mu.Unlock()
	w.wall.advance(time.Second)
	b.sync()
	bc = b.chunks()
	if len(bc) != 1 || bc["mem-b-session"].ID == "" {
		t.Fatalf("B after erased replay = %v; want only its session chunk", keys(bc))
	}
}

// ── AC-6: demotion ⇒ removal elsewhere, kept locally ────────────────────────

func TestMemorySync_DemotionRemovesElsewhereKeepsLocal(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-d", "global", "demote me")
	a.add("mem-dp", "global", "demote me to a project")
	a.sync()
	b.sync()
	ctx := context.Background()
	if err := a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-d", memory.ScopeKindSession, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-dp", memory.ScopeKindProject, "proj-1"); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	a.sync() // A pulls its own left_sync_scope tombstones
	bc, ac := b.chunks(), a.chunks()
	if _, ok := bc["mem-d"]; ok {
		t.Fatal("B must delete a chunk A demoted to session")
	}
	if _, ok := bc["mem-dp"]; ok {
		t.Fatal("B must delete a chunk A demoted to project")
	}
	if ac["mem-d"].ScopeKind != "session" || ac["mem-dp"].ScopeKind != "project" || ac["mem-d"].FleetMayKnow() {
		t.Fatalf("A keeps its demoted chunks, unsynced: %+v / %+v", ac["mem-d"], ac["mem-dp"])
	}
	// Re-promote on A: Fleet revives the record, B gets it back.
	if err := a.store.(memory.ScopePromoter).PromoteScope(ctx, "mem-d", memory.ScopeKindGlobal, ""); err != nil {
		t.Fatal(err)
	}
	a.sync()
	b.sync()
	if _, ok := b.chunks()["mem-d"]; !ok {
		t.Fatal("re-promotion must revive the record on B")
	}
}

// ── outbox coalescing (fleet-live-notes 2026-10-07) ─────────────────────────

func TestMemorySync_DeleteBeforeFirstPushSendsNothing(t *testing.T) {
	w := newMemWorld(t)
	a := w.device("devA", 0)
	a.enable()
	a.add("mem-x", "global", "created and deleted between syncs")
	a.forget("mem-x")
	a.sync()
	for _, p := range w.fleet.pushLog() {
		for _, it := range p.Items {
			if it.ID == "mem-x" {
				t.Fatalf("pushed %s for an id Fleet never saw", it.Op)
			}
		}
	}
	if len(a.outbox.Pending()) != 0 {
		t.Fatal("no forget may be queued for a never-pushed create")
	}
}

// ── per-result handling ─────────────────────────────────────────────────────

func TestMemorySync_SecretBlockedMergedAliasAndClockInFuture(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-secret", "global", "token sk-ant-api03-xxxx")
	a.add("mem-dup-a", "global", "same content")
	a.sync()
	b.add("mem-dup-b", "global", "same content")
	b.sync()
	if c := a.chunks()["mem-secret"]; c.SyncBlocked != "secret_detected" || c.SyncDirty {
		t.Fatalf("secret chunk = blocked %q dirty %v", c.SyncBlocked, c.SyncDirty)
	}
	n := len(w.fleet.pushLog())
	a.sync()
	for _, p := range w.fleet.pushLog()[n:] {
		for _, it := range p.Items {
			if it.ID == "mem-secret" {
				t.Fatal("a sync_blocked chunk was retried")
			}
		}
	}
	bc := b.chunks()
	if _, ok := bc["mem-dup-b"]; ok {
		t.Fatal("merged: B's alias must be re-keyed to the canonical id")
	}
	if c, ok := bc["mem-dup-a"]; !ok || len(c.Embedding) != 2 {
		t.Fatalf("canonical on B must keep B's embedding: %+v", c)
	}

	// A device 7 min fast: per-field clock_in_future, lane degraded with
	// the reason, chunk stays dirty (not blocked).
	fast := w.device("devFast", 7*time.Minute)
	fast.enable()
	fast.add("mem-fast", "global", "from the future")
	fast.ms.RunOnce(context.Background())
	s := fast.lanes.Snapshot(LaneMemorySync)
	if s.Status != LaneDegraded || s.Reason != "clock_in_future" || !strings.Contains(s.LastError, "ahead of Fleet") {
		t.Fatalf("fast-clock lane = %+v", s)
	}
	if c := fast.chunks()["mem-fast"]; c.SyncBlocked != "" || !c.SyncDirty {
		t.Fatalf("fast-clock chunk must stay dirty, unblocked: %+v", c)
	}
}

// ── reset protocol: cursor_expired ──────────────────────────────────────────

func TestMemorySync_ResetDropsSyncedChunksAbsentFromSnapshot(t *testing.T) {
	w := newMemWorld(t)
	a, b := w.device("devA", 0), w.device("devB", 0)
	a.enable()
	b.enable()
	a.add("mem-keep", "global", "keep")
	a.add("mem-gone", "global", "gone while B was away")
	a.sync()
	b.sync()
	b.add("mem-b-new", "global", "B's unsynced work survives a cursor reset")
	// Fleet sweeps mem-gone's history past the floor (tombstone expired).
	w.fleet.mu.Lock()
	delete(w.fleet.recs, "mem-gone")
	w.fleet.seq += 10
	w.fleet.floor = w.fleet.seq
	w.fleet.mu.Unlock()
	b.sync()
	bc := b.chunks()
	if _, ok := bc["mem-gone"]; ok {
		t.Fatal("a synced chunk absent from the reset snapshot must be deleted")
	}
	if _, ok := bc["mem-keep"]; !ok {
		t.Fatal("snapshot chunk lost")
	}
	if _, ok := bc["mem-b-new"]; !ok {
		t.Fatal("unsynced local work must survive a cursor_expired reset")
	}
	if w.fleet.live("mem-b-new") == nil {
		t.Fatal("after the reset the device pushes again")
	}
}

// ── two-device property test (mirror of fleet S2) ───────────────────────────

// TestMemorySync_Property_ConvergesUnderPermutedOps: random field edits,
// recalls, promotions and forgets on two devices, interleaved with syncs
// in random order, always converge to the same final state on both
// devices once each has synced twice after the last op.
func TestMemorySync_Property_ConvergesUnderPermutedOps(t *testing.T) {
	for seed := int64(1); seed <= 12; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
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
			for _, d := range devs {
				d.sync()
			}
			for _, d := range devs {
				d.sync()
			}
			ctx := context.Background()
			for step := 0; step < 40; step++ {
				d := devs[rng.Intn(2)]
				id := ids[rng.Intn(len(ids))]
				w.wall.advance(time.Duration(rng.Intn(5)+1) * time.Millisecond)
				if _, ok := d.chunks()[id]; !ok {
					if rng.Intn(3) == 0 {
						d.sync()
					}
					continue
				}
				switch rng.Intn(6) {
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
				default:
					d.sync()
				}
			}
			for round := 0; round < 2; round++ {
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
					out[id] = view{c.Pinned, c.ScopeKind, c.RecallCount}
				}
				return out
			}
			va, vb := snap(devs[0]), snap(devs[1])
			if fmt.Sprint(sortedViews(va)) != fmt.Sprint(sortedViews(vb)) {
				t.Fatalf("diverged:\nA=%v\nB=%v", sortedViews(va), sortedViews(vb))
			}
		})
	}
}

func sortedViews[V any](m map[string]V) []string {
	var out []string
	for k, v := range m {
		out = append(out, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(out)
	return out
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestMemorySync_HomePathNormalized: H11 hygiene — files under $HOME are
// sent as ~/… ; Fleet's caps trim an over-long list rather than reject.
func TestMemorySync_HomePathNormalized(t *testing.T) {
	m := &MemorySync{cfg: MemorySyncConfig{HomeDir: "/Users/test"}}
	got := m.normFiles([]string{"/Users/test/src/a.go", "/Users/testing/b.go", "/etc/x", strings.Repeat("y", 2000)})
	want := []string{"~/src/a.go", "/Users/testing/b.go", "/etc/x"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("normFiles = %v", got)
	}
	many := make([]string, 100)
	for i := range many {
		many[i] = fmt.Sprintf("/f%d", i)
	}
	if n := len(m.normFiles(many)); n != memoryMaxFiles {
		t.Fatalf("files capped to %d, want %d", n, memoryMaxFiles)
	}
}

var _ = http.StatusOK

// TestMemorySync_V091GobFullSyncCycle (WP-PI): a device whose memory.gob
// was written by v0.91.0 (no HLC / recall-split / sync fields) completes a
// full sync cycle — unstamped fields are stamped on first push, the legacy
// recall count is pushed as recall_own, session memory stays home — and a
// second device receives it.
func TestMemorySync_V091GobFullSyncCycle(t *testing.T) {
	w := newMemWorld(t)
	raw, err := os.ReadFile(filepath.Join("..", "memory", "testdata", "upgrade", "v0.91.0", "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	dirA := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "memory.gob"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a := w.deviceAt("devA", dirA, 0)
	b := w.device("devB", 0)
	a.enable()
	b.enable()
	a.sync()
	g := w.fleet.live("mem-v091-global")
	if g == nil || g.recall["devA"] != 3 || !g.pinned || g.pinnedHLC == "" || g.scopeHLC == "" || g.title != "Build uses make" {
		t.Fatalf("Fleet copy of the legacy global chunk = %+v", g)
	}
	if lt := w.fleet.live("mem-v091-longterm"); lt == nil || lt.kind != "narrative_synthesised" || lt.turnID != "turn-42" {
		t.Fatalf("Fleet copy of the legacy long_term chunk = %+v", lt)
	}
	if w.fleet.live("mem-v091-session") != nil {
		t.Fatal("a legacy session chunk was synced")
	}
	ac := a.chunks()["mem-v091-global"]
	if ac.CreatedHLC == "" || ac.SyncedAt.IsZero() || ac.SyncDirty {
		t.Fatalf("A after first cycle = %+v", ac)
	}
	b.sync()
	bc := b.chunks()
	if c, ok := bc["mem-v091-global"]; !ok || !c.Pinned || c.RecallOthers != 3 || !c.EmbedPending {
		t.Fatalf("B's copy = %+v", c)
	}
	if _, ok := bc["mem-v091-longterm"]; !ok {
		t.Fatal("B missing the long_term chunk")
	}
}
