// Package memory defines the MemoryAPI view-scoped accessor that
// surfaces the long-term-memory store to the frontend. The concrete
// implementation lives in impl.go and wraps core/memory.Store +
// core/memory.Embedder so the rpc layer doesn't import the storage
// internals directly (DIRECTIVE_001).
package memory

import (
	"context"
	"time"
)

// Chunk is the wire shape returned to the management UI. Mirrors
// core/memory.Chunk minus the embedding column — the frontend never
// needs the vectors and they should never leave the harness.
type Chunk struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"sessionId,omitempty"`
	ProjectID     string    `json:"projectId,omitempty"`
	ScopeKind     string    `json:"scopeKind"`
	ScopeID       string    `json:"scopeId"`
	SourceTurn    string    `json:"sourceTurn,omitempty"`
	Content       string    `json:"content"`
	ContentHash   string    `json:"contentHash"`
	ToolName      string    `json:"toolName,omitempty"`
	FilesRead     []string  `json:"filesRead,omitempty"`
	FilesModified []string  `json:"filesModified,omitempty"`
	Title         string    `json:"title,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	// Bundle E WP15 — greedy memory metadata.
	Pinned       bool      `json:"pinned,omitempty"`
	RecallCount  int       `json:"recallCount,omitempty"`
	LastAccessed time.Time `json:"lastAccessed,omitempty"`
	Source       string    `json:"source,omitempty"`
	// Narrative layer fields (memory-narrative-layer-01KQ8TD1 WP01).
	Kind            string  `json:"kind,omitempty"`
	RetrievalWeight float32 `json:"retrievalWeight,omitempty"`
	TurnID          string  `json:"turnId,omitempty"`
}

// NarrativeJobStatus is the wire shape for a failed narrative synthesis job
// (WP07). Surfaces in the Memory view "N narratives unrecoverable" list.
type NarrativeJobStatus struct {
	ID        string    `json:"id"`
	TurnID    string    `json:"turnId"`
	SessionID string    `json:"sessionId"`
	Attempt   int       `json:"attempt"`
	LastError string    `json:"lastError"`
	CreatedAt time.Time `json:"createdAt"`
}

// NarrativeMetrics surfaces the promotion-score counters for one chunk
// (WP07). Shown in the per-chunk score column in the Memory view.
type NarrativeMetrics struct {
	ChunkID         string    `json:"chunkId"`
	Retrievals      int64     `json:"retrievals"`
	Citations       int64     `json:"citations"`
	UserPins        int64     `json:"userPins"`
	Score           float64   `json:"score"`
	LastRetrievedAt time.Time `json:"lastRetrievedAt,omitempty"`
	LastCitedAt     time.Time `json:"lastCitedAt,omitempty"`
}

// ListFilter narrows the chunks returned by ListChunks. Each non-empty
// field acts as a conjunction: ScopeKind="project" AND ScopeID=p1
// returns only chunks pinned to that project. Empty filter returns
// every chunk.
type ListFilter struct {
	ScopeKind string `json:"scopeKind,omitempty"`
	ScopeID   string `json:"scopeId,omitempty"`
}

// JournalEntry is the wire shape for one memory hook journal row
// (Bundle E WP16). Surfaces what the greedy-memory hooks captured so
// the user can audit write-time activity.
type JournalEntry struct {
	Seq         int64     `json:"seq"`
	Boundary    string    `json:"boundary"`
	Scope       string    `json:"scope"`
	Title       string    `json:"title,omitempty"`
	Source      string    `json:"source,omitempty"`
	Written     bool      `json:"written"`
	Deduped     bool      `json:"deduped"`
	Skipped     bool      `json:"skipped"`
	SkipReason  string    `json:"skipReason,omitempty"`
	ChunkID     string    `json:"chunkId,omitempty"`
	ContentHash string    `json:"contentHash,omitempty"`
	At          time.Time `json:"at"`
}

// PruneStats is the wire shape for one prune-sweep summary
// (Bundle E WP15).
type PruneStats struct {
	StartedAt time.Time `json:"startedAt"`
	DurationMs int64    `json:"durationMs"`
	Kept      int       `json:"kept"`
	Dropped   int       `json:"dropped"`
	Collapsed int       `json:"collapsed"`
	Pinned    int       `json:"pinned"`
}

// PruneVerdict is the wire shape for one chunk's prune-sweep verdict.
type PruneVerdict struct {
	ID            string  `json:"id"`
	Action        string  `json:"action"`
	Reason        string  `json:"reason,omitempty"`
	KeepScore     float64 `json:"keepScore"`
	CollapsedInto string  `json:"collapsedInto,omitempty"`
}

// PrunePreview is the dry-run output. The frontend renders the
// would-prune set so the user can review before clicking "Apply".
type PrunePreview struct {
	Verdicts []PruneVerdict `json:"verdicts"`
	Stats    PruneStats     `json:"stats"`
	// Rows is the subset of Verdicts whose Action is "drop" or "collapse",
	// enriched with a human-readable snippet. The UI renders this in the
	// confirmation modal (§2.5). Populated alongside Verdicts.
	Rows []PruneRow `json:"rows"`
}

// PruneRow is one row in the §2.5 prune-preview confirmation modal.
// It is a projection of the corresponding PruneVerdict plus the
// short content snippet the user can review before confirming.
type PruneRow struct {
	ID      string `json:"id"`
	Snippet string `json:"snippet"`
	Reason  string `json:"reason"`
	Action  string `json:"action"` // "drop" | "collapse"
}

// HealthCounts holds the per-kind breakdown surfaced in the §2.4 health
// panel. All values come from indexed passes (no full-table scan).
type HealthCounts struct {
	Total            int `json:"total"`
	Raw              int `json:"raw"`              // ScopeKind=="session"
	Narrative        int `json:"narrative"`        // reserved; always 0 until narrative-layer ships
	LongTermPromoted int `json:"longTermPromoted"` // ScopeKind=="global" | "long_term"
	Embedded         int `json:"embedded"`         // len(Embedding) > 0
	Unembedded       int `json:"unembedded"`       // len(Embedding) == 0
}

// HealthActivity summarises the last-7-day window shown in the §2.4 panel.
type HealthActivity struct {
	Captured int `json:"captured"` // chunks created in the last 7d
	Pruned   int `json:"pruned"`   // always 0 (no audit journal yet)
	Promoted int `json:"promoted"` // chunks promoted to global in the last 7d
}

// HealthEmbedder surfaces static embedder properties (no network call).
type HealthEmbedder struct {
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// HealthSnapshot is the wire shape returned by Memory_HealthSnapshot.
// All counts come from a single O(n) pass over the in-RAM store.
type HealthSnapshot struct {
	Counts     HealthCounts   `json:"counts"`
	Activity   HealthActivity `json:"activity"`
	Embedder   HealthEmbedder `json:"embedder"`
	CapturedAt string         `json:"capturedAt"` // RFC3339 UTC
}

// EmbedderEligibility is the wire shape returned by EmbedderEligibility().
// It summarises which provider profiles are capable of supplying embeddings
// so the frontend can surface a contextual "no memory provider" banner when
// the user has only Anthropic-direct or Bedrock profiles configured.
type EmbedderEligibility struct {
	// HasEligible is true when at least one configured profile can supply
	// embeddings (openai / openrouter / custom_openai_compatible / azure).
	HasEligible bool `json:"hasEligible"`
	// AllProfiles is the total number of profiles that were examined.
	AllProfiles int `json:"allProfiles"`
	// EligibleProfiles is the count of profiles that are eligible.
	EligibleProfiles int `json:"eligibleProfiles"`
	// SkippedKinds holds the unique provider kinds that are present but
	// cannot supply embeddings by design (e.g. "anthropic", "bedrock").
	// The frontend renders a per-provider explanation in the banner.
	SkippedKinds []string `json:"skippedKinds"`
}

// MemoryAPI is the view-scoped accessor exposed via HarnessAPI.
//
// The hooks-driven architecture handles automatic persistence (via the
// memory.persist post_send builtin), but the chat surface still ships
// an explicit "📌 remember this" button so users can capture short
// turns the auto-persist length thresholds skip. RememberMessage is
// the binding behind that button.
type MemoryAPI interface {
	// ListChunks returns persisted memories that match the filter,
	// newest first. Empty filter returns every chunk.
	ListChunks(ctx context.Context, filter ListFilter) ([]Chunk, error)
	// RememberMessage embeds the message at messageID inside sessionID
	// and persists it as a new memory chunk under scope. scope must be
	// one of "global", "project", "session"; defaults to "session" when
	// empty. Returns the new chunk's ID.
	RememberMessage(ctx context.Context, sessionID, messageID, scope string) (string, error)
	// PromoteScope moves the chunk with chunkID to (newScopeKind,
	// newScopeID). Move semantics: the original row is deleted, a new
	// row is inserted with a new ID. Returns the new chunk's ID.
	PromoteScope(ctx context.Context, chunkID, newScopeKind, newScopeID string) (string, error)
	// Forget deletes the chunk with the given id.
	Forget(ctx context.Context, id string) error
	// Pin marks a chunk as immune to the prune sweep (Bundle E WP16).
	// pinned=false unpins. Returns ErrStoreUnavailable when the wired
	// store doesn't support pinning.
	Pin(ctx context.Context, id string, pinned bool) error
	// JournalTail returns the most recent N memory hook journal
	// entries. Used by HookJournalView to surface what greedy memory
	// is capturing.
	JournalTail(ctx context.Context, scope string, sinceSeq int64, limit int) ([]JournalEntry, error)
	// PrunePreview computes the prune verdict without mutating the
	// store. The user reviews the result before triggering Apply.
	PrunePreview(ctx context.Context, scope string) (PrunePreview, error)
	// RunPruneNow applies the prune sweep immediately and returns the
	// resulting stats. Bypasses the scheduler's cadence.
	RunPruneNow(ctx context.Context, scope string) (PruneStats, error)
	// HealthSnapshot returns an at-a-glance health snapshot for the §2.4
	// memory health dashboard. All counts are computed from indexed
	// passes (no full-table scan). The embedder argument is threaded
	// through the impl so the UI can display provider/model/dimensions
	// without a separate round-trip.
	HealthSnapshot(ctx context.Context) (HealthSnapshot, error)
	// TestEmbedder probes the wired embedder against the fixed string
	// "hello world" and returns the resulting vector dimensions (or an
	// error). Used by the [Test embedder] button in the §2.4 panel.
	TestEmbedder(ctx context.Context) (int, error)
	// CaptureRate returns a snapshot of the current memory capture
	// velocity and embedder health for the §2.7 LegendBar pill.
	CaptureRate(ctx context.Context) (CaptureRateSnapshot, error)
	// EmbedderEligibility inspects the configured provider profiles and
	// returns eligibility metadata — READ ONLY, no embedder is constructed.
	// Used by the Settings → Memory banner to tell users when none of their
	// profiles support embeddings (e.g. Anthropic-only setup).
	EmbedderEligibility(ctx context.Context) (EmbedderEligibility, error)

	// ── Narrative layer (memory-narrative-layer-01KQ8TD1) ──────────────

	// MarkImportant increments (important=true) or clears (false) the
	// user_pins signal for chunkID, contributing to the long-term
	// promotion score. This is NOT a "pin to chat top" affordance;
	// the UX label reads "Mark important" (FR-008b).
	MarkImportant(ctx context.Context, chunkID string, important bool) error
	// NarrativeFailedCount returns the count of synthesis jobs with
	// status=failed. Used by the "N narratives unrecoverable" banner.
	NarrativeFailedCount(ctx context.Context) (int, error)
	// NarrativeFailedList returns all failed synthesis jobs for the
	// manual-retry list view.
	NarrativeFailedList(ctx context.Context) ([]NarrativeJobStatus, error)
	// RetryFailedNarrative resets the job with the given ID so the
	// Promoter picks it up on the next scan. Caller should pass the
	// job's ID (not TurnID).
	RetryFailedNarrative(ctx context.Context, jobID string) error
	// NarrativeMetricsForChunk returns the promotion-score counters for
	// chunkID. Returns zero-value metrics when no signals have been
	// recorded.
	NarrativeMetricsForChunk(ctx context.Context, chunkID string) (NarrativeMetrics, error)

	// ── Capstone (memory-inspection-ui-01KX5R8E) ───────────────────────

	// LastRetrieval returns the most recent retrieval report for the
	// given session. Returns an empty report (no error) when no
	// retrieval has occurred for that session in the current process
	// lifetime.
	LastRetrieval(ctx context.Context, sessionID string) (RetrievalReport, error)
	// EmbeddingProbe embeds query and returns up to limit scored chunks
	// ranked by cosine similarity. limit is capped at 50 server-side.
	// Returns ErrEmbedderUnavailable when no embedder is wired.
	EmbeddingProbe(ctx context.Context, query string, limit int) ([]ScoredChunk, error)
	// ResummarizeChunk re-runs narrative synthesis on a single chunk.
	// For narrative chunks with TurnID: re-enqueues to the Promoter
	// (async; returns current Chunk immediately). For raw/legacy chunks:
	// runs extractive fallback inline and returns the updated Chunk.
	// Returns ErrResummarizeRateLimited when called within 60s of a
	// previous call for the same chunk.
	ResummarizeChunk(ctx context.Context, chunkID string) (Chunk, error)
	// GetChunkProvenance returns the full audit chain for a chunk:
	// source turn, hook boundary, retrieval counts, citation counts,
	// promotion score, scope path, embedding metadata.
	GetChunkProvenance(ctx context.Context, chunkID string) (ChunkProvenance, error)
}

// CaptureRateSnapshot is the wire shape returned by Memory_CaptureRate
// (§2.7 — LegendBar capture-rate widget).
type CaptureRateSnapshot struct {
	ChunksPerMinute  float64    `json:"chunksPerMinute"`  // writes in the last 60s
	EmbedderHealth   string     `json:"embedderHealth"`   // "ok" | "slow" | "error"
	LastErrorAt      *time.Time `json:"lastErrorAt"`      // nil when no recent error
	RecentErrorCount int        `json:"recentErrorCount"` // errors in the last 5 min
}

// ── Capstone types (memory-inspection-ui-01KX5R8E) ───────────────────────────

// ScoredChunk pairs a Chunk with its cosine similarity score against
// a query. Used by EmbeddingProbe and LastRetrieval.
type ScoredChunk struct {
	Chunk      Chunk   `json:"chunk"`
	Similarity float32 `json:"similarity"`
	// Injected is true when similarity >= the retriever's configured
	// threshold (i.e. the chunk was included in the LLM context).
	Injected bool `json:"injected"`
}

// RetrievalReport is the wire shape returned by Memory_LastRetrieval.
// It represents the most recent retrieval call for a session.
type RetrievalReport struct {
	SessionID string        `json:"sessionId"`
	Query     string        `json:"query"`
	Results   []ScoredChunk `json:"results"`
	Threshold float32       `json:"threshold"`
	At        time.Time     `json:"at"`
}

// ChunkProvenance is the full audit chain for one chunk, returned by
// Memory_GetChunkProvenance (§2.6).
type ChunkProvenance struct {
	ChunkID        string    `json:"chunkId"`
	SourceTurn     string    `json:"sourceTurn,omitempty"`
	HookBoundary   string    `json:"hookBoundary,omitempty"` // e.g. "post-llm"
	Kind           string    `json:"kind,omitempty"`
	ScopePath      string    `json:"scopePath"`    // e.g. "session → project"
	Pinned         bool      `json:"pinned"`
	RetrievalCount int       `json:"retrievalCount"` // RecallCount from store
	CitationCount  int64     `json:"citationCount"`  // from NarrativeMetrics
	PromotionScore float64   `json:"promotionScore"` // from NarrativeMetrics
	EmbedderKind   string    `json:"embedderKind,omitempty"`
	EmbedDimensions int      `json:"embedDimensions,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}
