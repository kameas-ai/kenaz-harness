// Long-term memory types. Chunk is the unit the user pins via the chat
// surface's "remember this" button; the embedding stays out of the
// JSON wire shape because the frontend never reads it (it is consumed
// only by the local k-NN search inside core/memory.Store).
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Scope kinds. Mirrors claude-mem's scope dimension: a chunk is either
// global (visible across every session), project-scoped (visible to
// every session inside a Project), or session-scoped (the default —
// visible only to the originating session).
const (
	ScopeKindGlobal  = "global"
	ScopeKindProject = "project"
	ScopeKindSession = "session"
	// ScopeKindLongTerm is the long-term scope tier added by the narrative
	// layer (memory-narrative-layer-01KQ8TD1 WP09). Chunks promoted to
	// long_term resist the prune sweep and are loaded into the system-prompt
	// prelude at session start. One-way promotion: demotion requires an
	// explicit pruner verdict, not a score drop.
	ScopeKindLongTerm = "long_term"
)

// Chunk is one stored memory: an opt-in snippet the user explicitly
// asked the harness to keep across sessions.
//
// Greedy-memory addendum (Bundle E WP15): the Pinned, RecallCount, and
// LastAccessed fields drive the background prune sweep. They default to
// the "fresh chunk" values when an old gob is read back (Pinned=false,
// RecallCount=0, LastAccessed=CreatedAt) so existing on-disk stores
// keep working without a migration.
//
// Narrative-layer addendum (memory-narrative-layer-01KQ8TD1 WP01): Kind
// and RetrievalWeight were added without a gob schema migration. Existing
// on-disk stores read back with empty Kind and zero RetrievalWeight;
// backfillChunkDefaults in store.go sets them to "raw" and 1.0 so the
// retriever's score multiplier is transparent for legacy chunks.
type Chunk struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id,omitempty"`
	ProjectID     string    `json:"project_id,omitempty"`
	ScopeKind     string    `json:"scope_kind"`
	ScopeID       string    `json:"scope_id"`
	SourceTurn    string    `json:"source_turn,omitempty"`
	Content       string    `json:"content"`
	ContentHash   string    `json:"content_hash"`
	ToolName      string    `json:"tool_name,omitempty"`
	FilesRead     []string  `json:"files_read,omitempty"`
	FilesModified []string  `json:"files_modified,omitempty"`
	Title         string    `json:"title,omitempty"`
	Embedding     []float32 `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	// Pinned chunks are immune to the prune sweep (FR-028).
	Pinned bool `json:"pinned,omitempty"`
	// RecallCount is the number of times this chunk has been retrieved
	// by the kernel's MemoryNode / retriever. Updated lazily — the
	// production store is the canonical recorder. Used by the
	// recall-frequency prune signal. Since memory-sync-01MEMSY01 WP05 it
	// is DERIVED: RecallOwn + RecallOthers + RecallFolded (see below);
	// mutate the components, never this field.
	RecallCount int `json:"recall_count,omitempty"`
	// LastAccessed is the last time this chunk was read out of the
	// store. Defaults to CreatedAt when zero. Used by the staleness
	// prune signal.
	LastAccessed time.Time `json:"last_accessed,omitempty"`
	// Source records the originating hook boundary ("post-llm" etc.)
	// so the inspector can show users why a chunk was captured.
	Source string `json:"source,omitempty"`
	// Kind classifies the chunk in the narrative layer
	// (memory-narrative-layer-01KQ8TD1 WP01). One of: "raw",
	// "narrative_extractive", "narrative_synthesised",
	// "narrative_extractive_fallback". Empty values are backfilled to
	// "raw" on load so legacy gobs are transparent.
	Kind string `json:"kind,omitempty"`
	// RetrievalWeight is the score multiplier applied during similarity
	// search (WP01). Default 1.0 (no boost). Narrative chunks default
	// to 1.5 (set by the narrative layer at write time). Zero values
	// are backfilled to 1.0 on load.
	RetrievalWeight float32 `json:"retrieval_weight,omitempty"`
	// TurnID links this chunk to a specific agent turn for narrative
	// keying. Used by the Promoter to correlate synthesised narratives
	// with their extractive fallbacks. Empty for raw chunks.
	TurnID string `json:"turn_id,omitempty"`

	// ── Memory sync (memory-sync-01MEMSY01) ─────────────────────────────
	//
	// All fields below were added gob-additively: a pre-mission memory.gob
	// reads back with every one at its zero value, and the zero value is
	// the correct "never synced, unstamped" state. backfillChunkDefaults
	// migrates the recall counter; nothing else needs a migration.

	// Per-field HLCs (WP02, contract §2). Empty = "unstamped" (legacy
	// gob); the sync client stamps unstamped fields with a fresh tick on
	// first push — Fleet keeps the first-seen value and any later real
	// edit wins by normal ordering. CreatedHLC is set once, at creation,
	// and drives the forget-all `erased_before` replay.
	TitleHLC   string `json:"title_hlc,omitempty"`
	PinnedHLC  string `json:"pinned_hlc,omitempty"`
	ScopeHLC   string `json:"scope_hlc,omitempty"`
	CreatedHLC string `json:"created_hlc,omitempty"`

	// Recall G-counter split (WP05, H4). RecallOwn is this device's
	// monotone counter (the value pushed as `recall_own`); RecallOthers is
	// pulled `recall_count_others`; RecallFolded is display/score-only
	// recall inherited from chunks a prune collapse folded into this one —
	// it is NEVER pushed (folding it into RecallOwn would double-report
	// recalls another device already pushed). RecallCount above stays the
	// sum of the three so every existing reader (prune signals, UI) keeps
	// one number.
	RecallOwn    int `json:"recall_own,omitempty"`
	RecallOthers int `json:"recall_others,omitempty"`
	RecallFolded int `json:"recall_folded,omitempty"`

	// EmbedPending marks a chunk stored without an embedding (pulled from
	// Fleet; embeddings never travel). The background re-embed drains it
	// when a real embedder exists (WP06, H5).
	EmbedPending bool `json:"embed_pending,omitempty"`

	// Sync bookkeeping (WP07). SyncDirty: a local mutation not yet
	// accepted by Fleet. SyncSentAt: first time the chunk rode a push
	// request — from then on Fleet MAY know the id, so a local delete must
	// send a forget (before it, a delete coalesces with the unpushed create
	// and nothing is sent). SyncedAt: last time Fleet accepted the chunk or
	// it arrived by pull. SyncBlocked: a retry:false rejection code
	// (e.g. secret_detected) — the chunk stays local and is surfaced in
	// the UI. SyncGen: bumped on every local mutation so a push result only
	// clears SyncDirty when nothing changed while the request was in flight.
	SyncDirty   bool      `json:"-"`
	SyncSentAt  time.Time `json:"-"`
	SyncedAt    time.Time `json:"synced_at,omitempty"`
	SyncBlocked string    `json:"sync_blocked,omitempty"`
	SyncGen     int64     `json:"-"`
}

// FleetMayKnow reports whether Fleet may hold a record for this chunk's id:
// it was pulled, accepted, or at least sent. A delete of a chunk Fleet
// cannot know is coalesced with its unpushed create (no forget is sent —
// Fleet records no forgets for ids it has never seen, fleet-live-notes
// 2026-10-07).
func (c Chunk) FleetMayKnow() bool {
	return !c.SyncedAt.IsZero() || !c.SyncSentAt.IsZero()
}

// normalizeRecall migrates a legacy single counter into the G-counter split
// and re-derives RecallCount as the sum. A chunk whose components are all
// zero but whose RecallCount is not is legacy (pre-WP05 gob, or a caller
// that set only RecallCount): its count becomes RecallOwn — this device
// produced every recall it ever recorded.
// recomputeRecall re-derives RecallCount from the components. Every
// mutation of a component uses this — NOT normalizeRecall, whose legacy
// rule would misread "others just dropped to 0 with a stale total" as a
// pre-split chunk and mint phantom own recalls (found by the review's
// demotion property test).
func recomputeRecall(c *Chunk) {
	c.RecallCount = c.RecallOwn + c.RecallOthers + c.RecallFolded
}

func normalizeRecall(c *Chunk) {
	if c.RecallOwn == 0 && c.RecallOthers == 0 && c.RecallFolded == 0 && c.RecallCount > 0 {
		c.RecallOwn = c.RecallCount
	}
	c.RecallCount = c.RecallOwn + c.RecallOthers + c.RecallFolded
}

// Result pairs a Chunk with its similarity score against a query
// embedding. Similarity is cosine; values close to 1 mean "highly
// related", values near 0 mean "unrelated".
type Result struct {
	Chunk      Chunk
	Similarity float32
}

// HashContent returns the hex-encoded sha256 of content. Used by the
// store to compute ContentHash on Add when the caller leaves it empty
// and to backfill old gobs that predate the column.
func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ScopeFilter targets chunks by (kind, id) for List / Query / Delete.
// An empty ID matches every chunk of the given kind (used for global,
// where ScopeID is always empty).
type ScopeFilter struct {
	Kind string
	ID   string
}

// matchesScope returns true when c falls under any filter in filters.
// An empty filter slice matches every chunk (no filtering).
func matchesScope(c Chunk, filters []ScopeFilter) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if f.Kind != c.ScopeKind {
			continue
		}
		if f.ID == "" || f.ID == c.ScopeID {
			return true
		}
	}
	return false
}
