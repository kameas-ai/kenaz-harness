package memory

// memory-sync-01MEMSY01 WP04 (contract H2): a re-summarize mints a new id
// (content is immutable on Fleet) and must forget the old one — on BOTH
// paths: the inline fallback here and the narrative promoter's synthesised
// write. Every assertion reads back real persistence: the memory.gob store
// and the memory_outbox.json forget queue, reopened from disk.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
	"github.com/kameas-ai/kenaz-harness/core/memory/narrative"
)

type syncFixture struct {
	dir    string
	store  corememory.Store
	outbox *corememory.ForgetOutbox
	api    *API
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	dir := t.TempDir()
	store, err := corememory.NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	clock, err := corememory.NewHLC("devA", corememory.HLCStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	store.(corememory.ClockSetter).SetClock(clock)
	ob, err := corememory.OpenForgetOutbox(corememory.ForgetOutboxPath(dir), clock)
	if err != nil {
		t.Fatal(err)
	}
	api := New(Config{Store: store, Embedder: &fakeEmbedder{dim: 2}, Forgets: ob})
	return &syncFixture{dir: dir, store: store, outbox: ob, api: api}
}

// reopenedForgets reads the outbox back from disk.
func (f *syncFixture) reopenedForgets(t *testing.T) []string {
	t.Helper()
	ob, err := corememory.OpenForgetOutbox(corememory.ForgetOutboxPath(f.dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, op := range ob.Pending() {
		ids = append(ids, op.ID)
	}
	return ids
}

func (f *syncFixture) reopenedChunks(t *testing.T) map[string]corememory.Chunk {
	t.Helper()
	st, err := corememory.NewChromemStore(filepath.Join(f.dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	all, _ := st.List(context.Background())
	out := map[string]corememory.Chunk{}
	for _, c := range all {
		out[c.ID] = c
	}
	return out
}

func TestResummarize_InlinePath_NewIDAndForgetOld(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	// A global chunk Fleet already holds (it was synced).
	old := corememory.Chunk{ID: "mem-old", ScopeKind: corememory.ScopeKindGlobal, TurnID: "turn-7",
		Content: "A long raw transcript of the turn that needs a summary.", Embedding: []float32{1, 0},
		CreatedAt: time.Now().UTC(), SyncedAt: time.Now().UTC(), RecallOwn: 3, RecallOthers: 4}
	if err := f.store.Add(ctx, old); err != nil {
		t.Fatal(err)
	}
	got, err := f.api.ResummarizeChunk(ctx, "mem-old")
	if err != nil {
		t.Fatalf("ResummarizeChunk: %v", err)
	}
	if got.ID == "mem-old" || got.TurnID != "turn-7" {
		t.Fatalf("new record = id %q turn %q; want a new id carrying turn-7", got.ID, got.TurnID)
	}
	chunks := f.reopenedChunks(t)
	if _, still := chunks["mem-old"]; still {
		t.Fatal("old id must be gone locally")
	}
	nc, ok := chunks[got.ID]
	if !ok {
		t.Fatal("new id not persisted")
	}
	if nc.FleetMayKnow() || nc.CreatedHLC == "" || !nc.SyncDirty || nc.ScopeKind != corememory.ScopeKindGlobal {
		t.Fatalf("new record must be a fresh, stamped, dirty global record: %+v", nc)
	}
	// Own recalls travel; other devices' recalls of the OLD id become
	// display-only (never re-pushed under the new id).
	if nc.RecallOwn != 3 || nc.RecallOthers != 0 || nc.RecallFolded != 4 || nc.RecallCount != 7 {
		t.Fatalf("recall rebase = own %d others %d folded %d total %d", nc.RecallOwn, nc.RecallOthers, nc.RecallFolded, nc.RecallCount)
	}
	if forgets := f.reopenedForgets(t); len(forgets) != 1 || forgets[0] != "mem-old" {
		t.Fatalf("persisted forgets = %v, want [mem-old]", forgets)
	}
}

// TestResummarize_InlinePath_UnsyncedCoalesces: the old record was never
// sent, so Fleet cannot know it — the delete coalesces with the unpushed
// create and NO forget is queued (fleet-live-notes 2026-10-07).
func TestResummarize_InlinePath_UnsyncedCoalesces(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := f.store.Add(ctx, corememory.Chunk{ID: "mem-local", ScopeKind: corememory.ScopeKindGlobal,
		Content: "never pushed", Embedding: []float32{1, 0}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.ResummarizeChunk(ctx, "mem-local"); err != nil {
		t.Fatal(err)
	}
	if forgets := f.reopenedForgets(t); len(forgets) != 0 {
		t.Fatalf("an id Fleet never saw must not be forgotten: %v", forgets)
	}
}

// storeNarrativeWriter is the composition the NarrativeWriter contract
// requires of any production implementation (promoter.go doc): replace
// through ReplaceForSync, delete fallbacks through RemoveForSync.
type storeNarrativeWriter struct {
	mu    sync.Mutex
	store corememory.Store
	rec   corememory.ForgetRecorder
	n     int
}

func (w *storeNarrativeWriter) turnChunks(ctx context.Context, sessionID, turnID string, kinds ...string) []string {
	all, _ := w.store.List(ctx)
	var ids []string
	for _, c := range all {
		if c.SessionID != sessionID || c.TurnID != turnID {
			continue
		}
		for _, k := range kinds {
			if c.Kind == k {
				ids = append(ids, c.ID)
			}
		}
	}
	return ids
}

func (w *storeNarrativeWriter) WriteNarrative(ctx context.Context, req narrative.NarrativeWriteReq) (string, error) {
	w.mu.Lock()
	w.n++
	id := "mem-narr-" + req.TurnID + "-" + string(rune('a'+w.n))
	w.mu.Unlock()
	next := corememory.Chunk{ID: id, SessionID: req.SessionID, TurnID: req.TurnID,
		ScopeKind: corememory.ScopeKindGlobal, Content: req.Content, Kind: string(req.Kind),
		RetrievalWeight: req.RetrievalWeight, Source: req.Source, Embedding: []float32{0, 1},
		CreatedAt: time.Now().UTC()}
	var replaced []string
	if req.Kind == narrative.ChunkKindNarrativeSynthesised {
		replaced = w.turnChunks(ctx, req.SessionID, req.TurnID, string(narrative.ChunkKindNarrativeSynthesised))
	}
	return id, corememory.ReplaceForSync(ctx, w.store, w.rec, next, replaced...)
}

func (w *storeNarrativeWriter) DeleteByTurnFallback(ctx context.Context, sessionID, turnID string) error {
	for _, id := range w.turnChunks(ctx, sessionID, turnID, string(narrative.ChunkKindNarrativeExtractiveFallback)) {
		if err := corememory.RemoveForSync(ctx, w.store, w.rec, id); err != nil {
			return err
		}
	}
	return nil
}

type okCaller struct{}

func (okCaller) Complete(context.Context, string, string) (string, error) {
	return `{"request":"r","investigated":"i","learned":"l","outcome":"o","next_steps":"n"}`, nil
}

// TestResummarize_PromoterPath_ForgetsReplacedID: a synced extractive
// fallback replaced by the promoter's synthesised write ends with exactly
// one live record for the turn and a forget of the fallback's id (AC-2).
func TestResummarize_PromoterPath_ForgetsReplacedID(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fallback := corememory.Chunk{ID: "mem-fb", SessionID: "s1", TurnID: "turn-9",
		ScopeKind: corememory.ScopeKindGlobal, Kind: string(narrative.ChunkKindNarrativeExtractiveFallback),
		Content: "fallback summary", Embedding: []float32{1, 0}, CreatedAt: time.Now().UTC(),
		SyncedAt: time.Now().UTC()}
	if err := f.store.Add(ctx, fallback); err != nil {
		t.Fatal(err)
	}
	w := &storeNarrativeWriter{store: f.store, rec: f.outbox}
	q := narrative.NewMemJobQueue()
	p := narrative.NewPromoter(narrative.PromoterConfig{Parallelism: 1, PollInterval: 10 * time.Millisecond},
		q, w, narrative.NewSyntheticBuilder(okCaller{}))
	if err := q.Enqueue(ctx, narrative.Job{ID: "j", SessionID: "s1", TurnID: "turn-9",
		Status: narrative.JobStatusPending, RetryAt: time.Now().Add(-time.Second),
		Payload: narrative.JobPayload{LastUserMsg: "u", LastAssistantMsg: "a"}}); err != nil {
		t.Fatal(err)
	}
	p.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(f.outbox.Pending()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	chunks := f.reopenedChunks(t)
	var live []corememory.Chunk
	for _, c := range chunks {
		if c.TurnID == "turn-9" {
			live = append(live, c)
		}
	}
	if len(live) != 1 || live[0].ID == "mem-fb" || live[0].Kind != string(narrative.ChunkKindNarrativeSynthesised) {
		t.Fatalf("turn-9 live records = %+v; want exactly the new synthesised one", live)
	}
	if forgets := f.reopenedForgets(t); len(forgets) != 1 || forgets[0] != "mem-fb" {
		t.Fatalf("persisted forgets = %v, want [mem-fb]", forgets)
	}
}

// TestResummarize_PendingChunkOnNoopDevice (WP06): a chunk pulled from Fleet
// with no vector, on a device with no real embedder, still re-summarizes —
// the new record inherits EmbedPending instead of failing the add.
func TestResummarize_PendingChunkOnNoopDevice(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	f.api.embedder = corememory.NoopEmbedder{}
	ctx := context.Background()
	if err := f.store.Add(ctx, corememory.Chunk{ID: "mem-pulled", ScopeKind: corememory.ScopeKindGlobal,
		Content: "pulled content without a vector", EmbedPending: true, CreatedAt: time.Now().UTC(),
		SyncedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	got, err := f.api.ResummarizeChunk(ctx, "mem-pulled")
	if err != nil {
		t.Fatalf("ResummarizeChunk: %v", err)
	}
	if c := f.reopenedChunks(t)[got.ID]; !c.EmbedPending || len(c.Embedding) != 0 {
		t.Fatalf("new record must stay EmbedPending: %+v", c)
	}
}

// TestForget_QueuesFleetForgetOnlyForKnownIDs (WP07, H7): the user Forget
// RPC queues a Fleet forget for a chunk Fleet may know and coalesces one
// it cannot (never sent) — read back from the persisted outbox.
func TestForget_QueuesFleetForgetOnlyForKnownIDs(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, c := range []corememory.Chunk{
		{ID: "mem-synced", ScopeKind: corememory.ScopeKindGlobal, Content: "a", Embedding: []float32{1, 0}, CreatedAt: now, SyncedAt: now},
		{ID: "mem-local", ScopeKind: corememory.ScopeKindGlobal, Content: "b", Embedding: []float32{0, 1}, CreatedAt: now},
	} {
		if err := f.store.Add(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.api.Forget(ctx, "mem-synced"); err != nil {
		t.Fatal(err)
	}
	if err := f.api.Forget(ctx, "mem-local"); err != nil {
		t.Fatal(err)
	}
	if got := f.reopenedForgets(t); len(got) != 1 || got[0] != "mem-synced" {
		t.Fatalf("persisted forgets = %v, want [mem-synced]", got)
	}
	if len(f.reopenedChunks(t)) != 0 {
		t.Fatal("both chunks must be gone locally")
	}
}

// TestForget_DropsNarrativeMetrics (WP09, H10): a removed chunk's metrics
// row does not outlive it.
func TestForget_DropsNarrativeMetrics(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	metrics := narrative.NewMemMetricsStore()
	f.api.narrativeMetrics = metrics
	ctx := context.Background()
	if err := f.store.Add(ctx, corememory.Chunk{ID: "mem-m", ScopeKind: corememory.ScopeKindGlobal, Content: "m",
		Embedding: []float32{1, 0}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = metrics.IncrementRetrievals(ctx, "mem-m", time.Now())
	_ = metrics.SetUserPins(ctx, "mem-m", true)
	if err := f.api.Forget(ctx, "mem-m"); err != nil {
		t.Fatal(err)
	}
	if m, _ := metrics.Get(ctx, "mem-m"); m.Retrievals != 0 || m.UserPins != 0 {
		t.Fatalf("metrics outlived their chunk: %+v", m)
	}
}
