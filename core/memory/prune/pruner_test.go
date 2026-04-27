package prune

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/memory"
)

// fixedClock returns a clock that always returns t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// mkChunk is a test helper producing a Chunk with sane defaults.
func mkChunk(id, scope string, created, accessed time.Time, recall int, pinned bool) memory.Chunk {
	return memory.Chunk{
		ID:           id,
		ScopeKind:    scope,
		ScopeID:      scope + "-id",
		Content:      "content-" + id,
		ContentHash:  "hash-" + id,
		CreatedAt:    created,
		LastAccessed: accessed,
		RecallCount:  recall,
		Pinned:       pinned,
		Embedding:    []float32{1, 0, 0},
	}
}

func TestPlan_PinnedAlwaysKept(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(-2, 0, 0) // 2 years stale
	chunks := []memory.Chunk{
		mkChunk("pinned-old", memory.ScopeKindGlobal, old, old, 0, true),
		mkChunk("free-old", memory.ScopeKindGlobal, old, old, 0, false),
	}
	rules := DefaultRules()
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	if dec.Pinned != 1 {
		t.Fatalf("Pinned count = %d, want 1", dec.Pinned)
	}
	if !contains(dec.Kept, "pinned-old") {
		t.Fatalf("pinned chunk dropped: %+v", dec)
	}
	if !contains(dec.Dropped, "free-old") {
		t.Fatalf("free old chunk should be dropped: %+v", dec)
	}
}

func TestPlan_StaleSignal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{
		StaleAfter:     30 * 24 * time.Hour,
		HardStaleAfter: 90 * 24 * time.Hour,
		KeepThreshold:  0.5,
		ScopeWeights: map[string]float64{
			memory.ScopeKindSession: 1.0,
		},
	}
	chunks := []memory.Chunk{
		mkChunk("fresh", memory.ScopeKindSession, now, now.Add(-1*24*time.Hour), 1, false),
		mkChunk("aging", memory.ScopeKindSession, now, now.Add(-45*24*time.Hour), 1, false),
		mkChunk("dead", memory.ScopeKindSession, now, now.Add(-200*24*time.Hour), 1, false),
	}
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	if !contains(dec.Kept, "fresh") {
		t.Fatalf("fresh dropped: %+v", dec)
	}
	if !contains(dec.Dropped, "dead") {
		t.Fatalf("dead kept: %+v", dec)
	}
}

func TestPlan_AgeSignal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{MaxAge: 365 * 24 * time.Hour, KeepThreshold: 0.5}
	chunks := []memory.Chunk{
		mkChunk("young", memory.ScopeKindGlobal, now.AddDate(0, -3, 0), now, 1, false),
		mkChunk("old", memory.ScopeKindGlobal, now.AddDate(-2, 0, 0), now, 1, false),
	}
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	if !contains(dec.Kept, "young") {
		t.Fatalf("young dropped: %+v", dec)
	}
	if !contains(dec.Dropped, "old") {
		t.Fatalf("aged-out kept: %+v", dec)
	}
}

func TestPlan_RecallPercentile(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{
		RecallPercentileFloor: 0.20,
		KeepThreshold:         0.5,
	}
	chunks := make([]memory.Chunk, 0, 10)
	for i := 0; i < 10; i++ {
		chunks = append(chunks, mkChunk(
			"c"+string(rune('0'+i)),
			memory.ScopeKindGlobal,
			now, now, i, false,
		))
	}
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	// Bottom 20% are recall counts 0 and 1 — those should drop.
	if !contains(dec.Dropped, "c0") {
		t.Fatalf("bottom-percentile c0 should drop: %+v", dec)
	}
	if !contains(dec.Kept, "c9") {
		t.Fatalf("top-percentile c9 should keep: %+v", dec)
	}
}

func TestPlan_ClusterCollapse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{CollapseCosine: 0.97, KeepThreshold: 0.5}
	// Three near-duplicates in scope A; one outsider in scope B.
	chunks := []memory.Chunk{
		{ID: "a1", ScopeKind: memory.ScopeKindGlobal, ScopeID: "g",
			Embedding: []float32{1, 0, 0.01}, RecallCount: 5, LastAccessed: now, CreatedAt: now},
		{ID: "a2", ScopeKind: memory.ScopeKindGlobal, ScopeID: "g",
			Embedding: []float32{1, 0.001, 0.01}, RecallCount: 1, LastAccessed: now, CreatedAt: now},
		{ID: "a3", ScopeKind: memory.ScopeKindGlobal, ScopeID: "g",
			Embedding: []float32{1, 0, 0.02}, RecallCount: 2, LastAccessed: now, CreatedAt: now},
		{ID: "b1", ScopeKind: memory.ScopeKindSession, ScopeID: "s",
			Embedding: []float32{0, 1, 0}, RecallCount: 1, LastAccessed: now, CreatedAt: now},
	}
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	if len(dec.Collapsed) != 2 {
		t.Fatalf("expected 2 collapses, got %d (verdicts=%+v)", len(dec.Collapsed), dec.Verdicts)
	}
	// Survivor must be a1 (highest recall).
	for _, v := range dec.Verdicts {
		if v.Action == "collapse" && v.CollapsedInto != "a1" {
			t.Fatalf("collapsed into wrong representative: %+v", v)
		}
	}
	// b1 must NOT be touched (different scope).
	if !contains(dec.Kept, "b1") {
		t.Fatalf("cross-scope chunk b1 mistakenly affected: %+v", dec)
	}
}

func TestPlan_SizeCap_DropsOldest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{MaxEntries: 3, KeepThreshold: 0.5}
	// Five distinct chunks, varying LastAccessed.
	chunks := []memory.Chunk{
		mkChunk("c1", memory.ScopeKindGlobal, now, now.Add(-5*time.Hour), 1, false),
		mkChunk("c2", memory.ScopeKindGlobal, now, now.Add(-4*time.Hour), 1, false),
		mkChunk("c3", memory.ScopeKindGlobal, now, now.Add(-3*time.Hour), 1, false),
		mkChunk("c4", memory.ScopeKindGlobal, now, now.Add(-2*time.Hour), 1, false),
		mkChunk("c5", memory.ScopeKindGlobal, now, now.Add(-1*time.Hour), 1, false),
	}
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	if len(dec.Dropped) != 2 {
		t.Fatalf("expected 2 dropped (size cap), got %d: %+v", len(dec.Dropped), dec)
	}
	// Oldest (c1, c2) must be dropped; c5 must survive.
	if !contains(dec.Dropped, "c1") || !contains(dec.Dropped, "c2") {
		t.Fatalf("size cap dropped wrong rows: %+v", dec.Dropped)
	}
	if !contains(dec.Kept, "c5") {
		t.Fatalf("size cap dropped newest: %+v", dec.Kept)
	}
}

func TestPlan_Multiplicative_AllSignalsCombine(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{
		StaleAfter:     30 * 24 * time.Hour,
		HardStaleAfter: 90 * 24 * time.Hour,
		MaxAge:         180 * 24 * time.Hour,
		KeepThreshold:  0.5,
	}
	// 60-day-old, idle for 60 days, recall=0 ⇒ score is 0.5*1*1 = 0.5,
	// which is the keep boundary; should be kept.
	c := mkChunk("borderline", memory.ScopeKindGlobal,
		now.Add(-60*24*time.Hour), now.Add(-60*24*time.Hour), 0, false)
	p := New(nil, rules, fixedClock(now))
	dec := p.plan([]memory.Chunk{c})
	if len(dec.Kept) != 1 {
		t.Fatalf("borderline should be kept: %+v", dec)
	}
}

func TestPlan_DefaultsAreConservative(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rules := Rules{} // zero
	chunks := []memory.Chunk{
		mkChunk("c1", memory.ScopeKindGlobal, now.AddDate(-10, 0, 0), now.AddDate(-10, 0, 0), 0, false),
	}
	p := New(nil, rules, fixedClock(now))
	dec := p.plan(chunks)
	if len(dec.Dropped) > 0 {
		t.Fatalf("zero-rules should not prune anything: %+v", dec)
	}
}

func TestApply_DeletesFromStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() + "/memory.gob"
	store, err := memory.NewChromemStore(dir)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(-2, 0, 0)
	ctx := context.Background()
	chunks := []memory.Chunk{
		mkChunk("keep", memory.ScopeKindGlobal, now, now, 5, false),
		mkChunk("drop", memory.ScopeKindGlobal, old, old, 0, false),
	}
	for _, c := range chunks {
		if err := store.Add(ctx, c); err != nil {
			t.Fatalf("Add %s: %v", c.ID, err)
		}
	}
	rules := DefaultRules()
	p := New(store, rules, fixedClock(now))
	dec, err := p.Apply(ctx)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !contains(dec.Dropped, "drop") {
		t.Fatalf("expected drop in Dropped: %+v", dec)
	}
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List after Apply: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "keep" {
		t.Fatalf("post-apply store has wrong rows: %+v", listed)
	}
}

func TestPin_AndMarkAccessed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() + "/memory.gob"
	store, err := memory.NewChromemStore(dir)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	c := mkChunk("c1", memory.ScopeKindGlobal, now, now.Add(-1*time.Hour), 0, false)
	if err := store.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	pruner, ok := store.(memory.PruneCapable)
	if !ok {
		t.Fatalf("store does not implement PruneCapable")
	}
	if err := pruner.SetPinned(ctx, "c1", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if err := pruner.MarkAccessed(ctx, []string{"c1"}, now); err != nil {
		t.Fatalf("MarkAccessed: %v", err)
	}
	listed, _ := store.List(ctx)
	if len(listed) != 1 || !listed[0].Pinned || listed[0].RecallCount != 1 {
		t.Fatalf("pin/access not applied: %+v", listed)
	}
}

func TestRecorder_RingBufferEvictsOldest(t *testing.T) {
	t.Parallel()
	r := NewRecorder(3)
	r.Record(Stats{Kept: 1})
	r.Record(Stats{Kept: 2})
	r.Record(Stats{Kept: 3})
	r.Record(Stats{Kept: 4})
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snap))
	}
	if snap[0].Kept != 2 || snap[2].Kept != 4 {
		t.Fatalf("oldest not evicted: %+v", snap)
	}
	if r.Latest().Kept != 4 {
		t.Fatalf("Latest = %+v", r.Latest())
	}
}

func TestScheduler_RunOnce_FiresCallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() + "/memory.gob"
	store, _ := memory.NewChromemStore(dir)
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	p := New(store, DefaultRules(), fixedClock(now))

	called := false
	s := NewScheduler(p,
		WithInterval(time.Hour),
		WithClock(fixedClock(now)),
		WithOnSweep(func(_ Decision) { called = true }),
	)
	if _, err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !called {
		t.Fatal("onSweep callback not fired")
	}
	if s.LastRun().IsZero() {
		t.Fatal("LastRun not stamped")
	}
}

func TestScheduler_StartStop_NoLeak(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() + "/memory.gob"
	store, _ := memory.NewChromemStore(dir)
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	p := New(store, DefaultRules(), fixedClock(now))
	s := NewScheduler(p, WithInterval(50*time.Millisecond), WithClock(fixedClock(now)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, time.Time{})
	time.Sleep(120 * time.Millisecond)
	s.Stop()
	// Idempotent.
	s.Stop()
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
