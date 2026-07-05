// Concrete MemoryAPI implementation. Wraps core/memory.Store +
// core/memory.Embedder behind the view-scoped surface; the rpc layer
// constructs exactly one instance per process and shares it with the
// hooks subsystem (memory.retrieve / memory.persist builtins) so the
// auto-persist path and the explicit "📌 remember this" path see the
// same on-disk gob file.
//
// The hooks-driven architecture handles automatic persistence based
// on length thresholds; this surface backs the user-driven pin button
// that captures short turns the auto-path would skip.
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
	"github.com/kameas-ai/kenaz-harness/core/memory/narrative"
	"github.com/kameas-ai/kenaz-harness/core/memory/prune"
)

// MessageReader is the slice of session.Manager the impl needs to
// resolve a (sessionID, messageID) pair to its content. The rpc layer
// adapts session.Manager to it; tests pass fakes.
type MessageReader interface {
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
}

// ProjectResolver is the optional capability the impl uses to map a
// session id to its project id when resolving project scope. The rpc
// layer's session adapter implements this when the project entity
// lands; until then RememberMessage's project scope falls back to
// session scope (mirrors the persist-builtin warning path).
type ProjectResolver interface {
	ProjectIDForSession(ctx context.Context, sessionID string) (string, error)
}

// Message mirrors the role+content+id triple buildMessages cares about;
// kept here so the impl doesn't import core/session directly.
type Message struct {
	ID      string
	Role    string
	Content string
}

// JournalSource is the optional capability the impl uses to surface
// memory hook journal entries (Bundle E WP16). The kernel's
// HookManager satisfies this — the rpc layer reaches into core/agentgraph
// when it has a live kernel to bind, otherwise the JournalTail surface
// returns an empty slice.
type JournalSource interface {
	JournalSnapshot() []JournalEntry
}

// ProfileLister is the optional capability the impl uses to inspect
// configured provider profiles for the EmbedderEligibility surface. The
// rpc layer wires the personal.Store; tests pass a fake or nil (nil
// returns an empty profile list, which yields HasEligible=false).
type ProfileLister interface {
	// ListProfiles returns the full list of provider profiles and their
	// kind/endpoint/azure metadata needed to compute eligibility.
	ListProfiles() []corememory.ProfileEligibilityInput
}

// API is the concrete MemoryAPI.
type API struct {
	store    corememory.Store
	embedder corememory.Embedder
	reader   MessageReader
	rules    prune.Rules
	journal  JournalSource
	profiles ProfileLister
	// Narrative layer (memory-narrative-layer-01KQ8TD1 WP07). Both are
	// optional; when nil the narrative API methods return a stub/no-op.
	narrativeMetrics narrative.MetricsStore
	narrativeJobs    narrative.JobQueue

	mu    sync.Mutex
	stats []PruneStats

	// Capstone (memory-inspection-ui-01KX5R8E).
	// resummaryMu guards resummaryAt; separate from mu to avoid holding
	// the prune-stats lock during extractive summary computation.
	resummaryMu sync.Mutex
	// resummaryAt tracks when each chunkID was last re-summarised so we
	// can enforce the 1-per-60s rate limit (WP04).
	resummaryAt map[string]time.Time
}

// Config bundles dependencies for New. Embedder + Reader are required
// for the explicit RememberMessage path; ListChunks / Forget continue
// working when only Store is wired (the rpc layer's degraded mode).
//
// PruneRules + Journal are Bundle E WP15/WP16 additions; both are
// optional. When PruneRules is the zero value the prune surface uses
// prune.DefaultRules(). When Journal is nil, JournalTail returns an
// empty slice.
//
// Profiles is optional. When set, EmbedderEligibility inspects the
// profile list to surface which provider kinds are present but cannot
// supply embeddings. When nil, EmbedderEligibility returns all-zero
// (HasEligible=false, AllProfiles=0) — the frontend renders the banner.
//
// NarrativeMetrics and NarrativeJobs are narrative-layer additions
// (memory-narrative-layer-01KQ8TD1 WP07). Both are optional; when nil
// the narrative API methods return stub/no-op results.
//
// NarrativeJobs is also used by ResummarizeChunk (capstone WP04) to
// re-enqueue narrative chunks. When nil, ResummarizeChunk falls through
// to the extractive fallback for all chunks.
type Config struct {
	Store            corememory.Store
	Embedder         corememory.Embedder
	Reader           MessageReader
	PruneRules       prune.Rules
	Journal          JournalSource
	Profiles         ProfileLister
	NarrativeMetrics narrative.MetricsStore
	NarrativeJobs    narrative.JobQueue
}

// New constructs a MemoryAPI.
func New(cfg Config) *API {
	rules := cfg.PruneRules
	// Detect zero ruleset: switch to defaults so a freshly-wired
	// API has sensible behavior.
	if rules.MaxAge == 0 && rules.StaleAfter == 0 && rules.MaxEntries == 0 &&
		rules.CollapseCosine == 0 && rules.MinRecallCount == 0 &&
		rules.RecallPercentileFloor == 0 {
		rules = prune.DefaultRules()
	}
	return &API{
		store:            cfg.Store,
		embedder:         cfg.Embedder,
		reader:           cfg.Reader,
		rules:            rules,
		journal:          cfg.Journal,
		profiles:         cfg.Profiles,
		narrativeMetrics: cfg.NarrativeMetrics,
		narrativeJobs:    cfg.NarrativeJobs,
		resummaryAt:      make(map[string]time.Time),
	}
}

// ErrStoreUnavailable surfaces when the harness booted without a
// working vector store. The chassis still runs (chat works) but the
// memory surface returns this so the UI can render an actionable
// empty state.
var ErrStoreUnavailable = errors.New("memory: store unavailable")

// ErrEmbedderUnavailable mirrors the lower-level error so the rpc
// surface can match without importing core/memory.
var ErrEmbedderUnavailable = corememory.ErrEmbedderUnavailable

// ErrInvalidScope is returned when RememberMessage receives a scope
// that is not one of "global" / "project" / "session".
var ErrInvalidScope = errors.New("memory: invalid scope")

// ListChunks returns matching chunks newest-first. A nil store yields
// an empty slice so the UI's empty state is the observable behaviour.
func (a *API) ListChunks(ctx context.Context, filter ListFilter) ([]Chunk, error) {
	if a == nil || a.store == nil {
		return []Chunk{}, nil
	}
	var scopes []corememory.ScopeFilter
	if filter.ScopeKind != "" {
		scopes = append(scopes, corememory.ScopeFilter{
			Kind: filter.ScopeKind,
			ID:   filter.ScopeID,
		})
	}
	stored, err := a.store.List(ctx, scopes...)
	if err != nil {
		return nil, err
	}
	out := make([]Chunk, 0, len(stored))
	for _, c := range stored {
		out = append(out, toViewChunk(c))
	}
	return out, nil
}

// RememberMessage persists the message at (sessionID, messageID) as a
// new memory chunk under scope. Privacy: content stays on disk under
// the harness's data dir; the only network call is the embeddings
// request to the configured OpenAI provider.
func (a *API) RememberMessage(ctx context.Context, sessionID, messageID, scope string) (string, error) {
	if a == nil || a.store == nil {
		return "", ErrStoreUnavailable
	}
	if a.embedder == nil {
		return "", ErrEmbedderUnavailable
	}
	if _, ok := a.embedder.(corememory.NoopEmbedder); ok {
		return "", ErrEmbedderUnavailable
	}
	if a.reader == nil {
		return "", errors.New("memory: session reader unwired")
	}
	if scope == "" {
		scope = corememory.ScopeKindSession
	}
	switch scope {
	case corememory.ScopeKindGlobal, corememory.ScopeKindProject, corememory.ScopeKindSession:
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	msgs, err := a.reader.ListMessages(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("memory: load session %s: %w", sessionID, err)
	}
	var target *Message
	for i := range msgs {
		if msgs[i].ID == messageID {
			target = &msgs[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("memory: message %s not in session %s", messageID, sessionID)
	}
	if target.Content == "" {
		return "", errors.New("memory: cannot remember empty content")
	}

	projectID := ""
	if pr, ok := a.reader.(ProjectResolver); ok {
		pid, perr := pr.ProjectIDForSession(ctx, sessionID)
		if perr == nil {
			projectID = pid
		}
	}
	scopeKind, scopeID := scope, ""
	switch scope {
	case corememory.ScopeKindGlobal:
		scopeID = ""
	case corememory.ScopeKindProject:
		if projectID == "" {
			scopeKind = corememory.ScopeKindSession
			scopeID = sessionID
		} else {
			scopeID = projectID
		}
	case corememory.ScopeKindSession:
		scopeID = sessionID
	}

	vecs, err := a.embedder.Embed(ctx, []string{target.Content})
	if err != nil {
		return "", fmt.Errorf("memory: embed: %w", err)
	}
	if len(vecs) == 0 {
		return "", errors.New("memory: embedder returned no vectors")
	}
	id, err := newChunkID()
	if err != nil {
		return "", err
	}
	chunk := corememory.Chunk{
		ID:          id,
		SessionID:   sessionID,
		ProjectID:   projectID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		SourceTurn:  target.Role,
		Content:     target.Content,
		ContentHash: corememory.HashContent(target.Content),
		Embedding:   vecs[0],
		CreatedAt:   time.Now().UTC(),
	}
	if err := a.store.Add(ctx, chunk); err != nil {
		return "", err
	}
	return id, nil
}

// PromoteScope moves a chunk to a new scope. It deletes the original
// row and inserts a new chunk with a new ID, the same content +
// embedding, and the new (kind, id) scope. Atomic under the store's
// mutex: callers see either the old chunk or the new one, never both.
func (a *API) PromoteScope(ctx context.Context, chunkID, newScopeKind, newScopeID string) (string, error) {
	if a == nil || a.store == nil {
		return "", ErrStoreUnavailable
	}
	switch newScopeKind {
	case corememory.ScopeKindGlobal, corememory.ScopeKindProject, corememory.ScopeKindSession:
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidScope, newScopeKind)
	}
	if newScopeKind == corememory.ScopeKindGlobal {
		newScopeID = ""
	}
	mover, ok := a.store.(corememory.ScopePromoter)
	if !ok {
		return "", errors.New("memory: store does not support scope promotion")
	}
	newID, err := newChunkID()
	if err != nil {
		return "", err
	}
	if err := mover.PromoteScope(ctx, chunkID, newID, newScopeKind, newScopeID); err != nil {
		return "", err
	}
	return newID, nil
}

// Forget removes the chunk with id from the store. Bare wrapper around
// Store.Delete so the bindings layer doesn't import core/memory.
func (a *API) Forget(ctx context.Context, id string) error {
	if a == nil || a.store == nil {
		return ErrStoreUnavailable
	}
	return a.store.Delete(ctx, id)
}

// Pin sets / clears the do-not-prune flag on a chunk (Bundle E WP16).
// Returns ErrPinUnsupported when the wired store does not implement
// PruneCapable — older stores don't carry the flag.
func (a *API) Pin(ctx context.Context, id string, pinned bool) error {
	if a == nil || a.store == nil {
		return ErrStoreUnavailable
	}
	pruner, ok := a.store.(corememory.PruneCapable)
	if !ok {
		return ErrPinUnsupported
	}
	return pruner.SetPinned(ctx, id, pinned)
}

// JournalTail returns the most recent N memory hook journal entries.
// Empty journal source ⇒ empty slice. The scope filter is exact-match
// against the recorded JournalEntry.Scope; an empty scope returns all.
func (a *API) JournalTail(_ context.Context, scope string, sinceSeq int64, limit int) ([]JournalEntry, error) {
	if a == nil || a.journal == nil {
		return []JournalEntry{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	all := a.journal.JournalSnapshot()
	out := make([]JournalEntry, 0, len(all))
	for _, e := range all {
		if e.Seq <= sinceSeq {
			continue
		}
		if scope != "" && e.Scope != scope {
			continue
		}
		out = append(out, e)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// PrunePreview computes the would-prune verdict without mutating the
// store. The frontend renders the verdict list before letting the user
// confirm with RunPruneNow.
//
// §2.5 extension: the returned PrunePreview now includes a Rows field
// containing the drop/collapse verdicts enriched with a human-readable
// content snippet so the confirmation modal can display them. The prune
// ALGORITHM is unchanged — only the wire shape is extended.
func (a *API) PrunePreview(ctx context.Context, scope string) (PrunePreview, error) {
	if a == nil || a.store == nil {
		return PrunePreview{}, ErrStoreUnavailable
	}
	p := prune.New(a.store, a.rules, nil)
	scopes := buildScopeFilter(scope)
	started := time.Now().UTC()
	dec, err := p.Plan(ctx, scopes...)
	if err != nil {
		return PrunePreview{}, err
	}
	dur := time.Since(started)

	// Build a snippet index from the store so Rows can include content
	// previews without an extra List call (we already hold the plan result).
	chunks, _ := a.store.List(ctx, scopes...)
	snippetByID := make(map[string]string, len(chunks))
	for _, c := range chunks {
		snippetByID[c.ID] = pruneSnippet(c.Content)
	}

	out := PrunePreview{
		Stats: PruneStats{
			StartedAt:  started,
			DurationMs: dur.Milliseconds(),
			Kept:       len(dec.Kept),
			Dropped:    len(dec.Dropped),
			Collapsed:  len(dec.Collapsed),
			Pinned:     dec.Pinned,
		},
		Verdicts: make([]PruneVerdict, 0, len(dec.Verdicts)),
	}
	for _, v := range dec.Verdicts {
		out.Verdicts = append(out.Verdicts, PruneVerdict{
			ID:            v.ID,
			Action:        v.Action,
			Reason:        v.Reason,
			KeepScore:     v.KeepScore,
			CollapsedInto: v.CollapsedInto,
		})
		if v.Action == "drop" || v.Action == "collapse" {
			reason := v.Reason
			if reason == "" {
				reason = v.Action
			}
			out.Rows = append(out.Rows, PruneRow{
				ID:      v.ID,
				Snippet: snippetByID[v.ID],
				Reason:  reason,
				Action:  v.Action,
			})
		}
	}
	return out, nil
}

// pruneSnippet returns a short human-readable excerpt of content for
// display in the §2.5 prune-preview modal. Limited to 120 runes.
func pruneSnippet(content string) string {
	const maxRunes = 120
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes]) + "…"
}

// HealthSnapshot returns an at-a-glance health snapshot (§2.4). It
// delegates to the store's HealthSnapshotter capability when available;
// otherwise it builds the snapshot from a List call (same semantics,
// slightly more allocations).
func (a *API) HealthSnapshot(ctx context.Context) (HealthSnapshot, error) {
	if a == nil || a.store == nil {
		return HealthSnapshot{}, ErrStoreUnavailable
	}

	var coresnap corememory.HealthSnapshot
	if hs, ok := a.store.(corememory.HealthSnapshotter); ok {
		var err error
		coresnap, err = hs.SnapshotHealth(ctx)
		if err != nil {
			return HealthSnapshot{}, err
		}
		// Inject embedder info that the store doesn't have access to.
		if a.embedder != nil {
			coresnap.Embedder = corememory.EmbedderInfo{
				Kind:       a.embedder.Kind(),
				Dimensions: a.embedder.Dimensions(),
			}
			if namer, ok := a.embedder.(interface{ Model() string }); ok {
				coresnap.Embedder.Model = namer.Model()
			}
		}
	} else {
		// Fallback: build snapshot from List (identical result, more allocs).
		chunks, err := a.store.List(ctx)
		if err != nil {
			return HealthSnapshot{}, err
		}
		var embedder corememory.Embedder
		if a.embedder != nil {
			embedder = a.embedder
		}
		coresnap = corememory.SnapshotHealth(chunks, embedder, time.Now().UTC())
	}

	out := HealthSnapshot{
		Counts: HealthCounts{
			Total:            coresnap.Counts.Total,
			Raw:              coresnap.Counts.Raw,
			Narrative:        coresnap.Counts.Narrative,
			LongTermPromoted: coresnap.Counts.LongTermPromoted,
			Embedded:         coresnap.Counts.Embedded,
			Unembedded:       coresnap.Counts.Unembedded,
		},
		Activity: HealthActivity{
			Captured: coresnap.Activity.Captured,
			Pruned:   coresnap.Activity.Pruned,
			Promoted: coresnap.Activity.Promoted,
		},
		Embedder: HealthEmbedder{
			Kind:       coresnap.Embedder.Kind,
			Model:      coresnap.Embedder.Model,
			Dimensions: coresnap.Embedder.Dimensions,
		},
		CapturedAt: coresnap.CapturedAt.UTC().Format(time.RFC3339),
	}
	return out, nil
}

// TestEmbedder probes the wired embedder against the fixed string
// "hello world" and returns the resulting vector dimensions on success.
// The §2.4 "Test embedder" button calls this RPC.
func (a *API) TestEmbedder(ctx context.Context) (int, error) {
	if a == nil || a.embedder == nil {
		return 0, ErrEmbedderUnavailable
	}
	if _, ok := a.embedder.(corememory.NoopEmbedder); ok {
		return 0, ErrEmbedderUnavailable
	}
	vecs, err := a.embedder.Embed(ctx, []string{"hello world"})
	if err != nil {
		return 0, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return 0, errors.New("memory: embedder returned empty vector")
	}
	return len(vecs[0]), nil
}

// RunPruneNow applies the prune sweep immediately. Returns aggregated
// stats; the per-id verdict list is omitted from the wire response —
// callers that want details should call PrunePreview first.
func (a *API) RunPruneNow(ctx context.Context, scope string) (PruneStats, error) {
	if a == nil || a.store == nil {
		return PruneStats{}, ErrStoreUnavailable
	}
	p := prune.New(a.store, a.rules, nil)
	scopes := buildScopeFilter(scope)
	started := time.Now().UTC()
	dec, err := p.Apply(ctx, scopes...)
	if err != nil {
		return PruneStats{}, err
	}
	dur := time.Since(started)
	stats := PruneStats{
		StartedAt:  started,
		DurationMs: dur.Milliseconds(),
		Kept:       len(dec.Kept),
		Dropped:    len(dec.Dropped),
		Collapsed:  len(dec.Collapsed),
		Pinned:     dec.Pinned,
	}
	a.mu.Lock()
	a.stats = append(a.stats, stats)
	if len(a.stats) > 30 {
		a.stats = a.stats[len(a.stats)-30:]
	}
	a.mu.Unlock()
	return stats, nil
}

// CaptureRate returns the live capture-rate snapshot for the §2.7
// LegendBar pill. The data comes from the process-scoped
// corememory.GlobalCaptureTracker() — no store access needed.
func (a *API) CaptureRate(_ context.Context) (CaptureRateSnapshot, error) {
	snap := corememory.GlobalCaptureTracker().Snapshot(time.Now().UTC())
	return CaptureRateSnapshot{
		ChunksPerMinute:  snap.ChunksPerMinute,
		EmbedderHealth:   snap.EmbedderHealth,
		LastErrorAt:      snap.LastErrorAt,
		RecentErrorCount: snap.RecentErrorCount,
	}, nil
}

// ErrPinUnsupported is returned by Pin when the wired store predates
// the PruneCapable interface (in-memory test stubs etc.).
var ErrPinUnsupported = errors.New("memory: store does not support pinning")

// buildScopeFilter expands a single scope string ("global" / "project"
// / "session" / "") into the core/memory ScopeFilter slice. Empty ⇒
// nil filter (match everywhere).
func buildScopeFilter(scope string) []corememory.ScopeFilter {
	switch scope {
	case "":
		return nil
	case corememory.ScopeKindGlobal, corememory.ScopeKindProject, corememory.ScopeKindSession:
		return []corememory.ScopeFilter{{Kind: scope}}
	default:
		return nil
	}
}

// newChunkID returns a 16-byte hex-encoded random id. crypto/rand so
// concurrent Remembers cannot collide and the value stays opaque to the
// frontend.
func newChunkID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("memory: random id: %w", err)
	}
	return "mem-" + hex.EncodeToString(b), nil
}

func toViewChunk(c corememory.Chunk) Chunk {
	return Chunk{
		ID:              c.ID,
		SessionID:       c.SessionID,
		ProjectID:       c.ProjectID,
		ScopeKind:       c.ScopeKind,
		ScopeID:         c.ScopeID,
		SourceTurn:      c.SourceTurn,
		Content:         c.Content,
		ContentHash:     c.ContentHash,
		ToolName:        c.ToolName,
		FilesRead:       c.FilesRead,
		FilesModified:   c.FilesModified,
		Title:           c.Title,
		CreatedAt:       c.CreatedAt,
		Pinned:          c.Pinned,
		RecallCount:     c.RecallCount,
		LastAccessed:    c.LastAccessed,
		Source:          c.Source,
		Kind:            c.Kind,
		RetrievalWeight: c.RetrievalWeight,
		TurnID:          c.TurnID,
	}
}

// EmbedderEligibility inspects the configured provider profiles and returns
// eligibility metadata without constructing an Embedder. The implementation
// delegates to core/memory.CheckEligibility so the selection logic is
// tested independently of the rpc layer.
//
// When no ProfileLister is wired (nil), the result has HasEligible=false and
// AllProfiles=0, which causes the frontend to display the "no memory provider"
// banner — a safe default for test environments and minimal deployments.
func (a *API) EmbedderEligibility(_ context.Context) (EmbedderEligibility, error) {
	var inputs []corememory.ProfileEligibilityInput
	if a != nil && a.profiles != nil {
		inputs = a.profiles.ListProfiles()
	}
	result := corememory.CheckEligibility(inputs)
	return EmbedderEligibility{
		HasEligible:      result.HasEligible,
		AllProfiles:      result.AllProfiles,
		EligibleProfiles: result.EligibleProfiles,
		SkippedKinds:     result.SkippedKinds,
	}, nil
}

// ── Narrative layer methods (memory-narrative-layer-01KQ8TD1 WP07) ──────────

// MarkImportant increments or clears the user_pins signal for chunkID.
// This is NOT a "pin to chat top" affordance; UX label reads "Mark important"
// (FR-008b). When the narrative metrics store is not wired, returns nil
// (graceful degradation).
func (a *API) MarkImportant(ctx context.Context, chunkID string, important bool) error {
	if a.narrativeMetrics == nil {
		return nil
	}
	return a.narrativeMetrics.SetUserPins(ctx, chunkID, important)
}

// NarrativeFailedCount returns the count of synthesis jobs with status=failed.
// Returns 0 when the job queue is not wired.
func (a *API) NarrativeFailedCount(ctx context.Context) (int, error) {
	if a.narrativeJobs == nil {
		return 0, nil
	}
	return a.narrativeJobs.CountFailed(ctx)
}

// NarrativeFailedList returns all failed synthesis jobs for the manual-retry
// list view. Returns empty slice when the job queue is not wired.
func (a *API) NarrativeFailedList(ctx context.Context) ([]NarrativeJobStatus, error) {
	if a.narrativeJobs == nil {
		return nil, nil
	}
	jobs, err := a.narrativeJobs.ListFailed(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NarrativeJobStatus, len(jobs))
	for i, j := range jobs {
		out[i] = NarrativeJobStatus{
			ID:        j.ID,
			TurnID:    j.TurnID,
			SessionID: j.SessionID,
			Attempt:   j.Attempt,
			LastError: j.LastError,
			CreatedAt: j.CreatedAt,
		}
	}
	return out, nil
}

// RetryFailedNarrative resets a failed job so the Promoter picks it up on
// the next scan (attempt=0, retry_at=now). Returns an error when the job is
// not found or the queue is not wired.
func (a *API) RetryFailedNarrative(ctx context.Context, jobID string) error {
	if a.narrativeJobs == nil {
		return errors.New("memory: narrative job queue not available")
	}
	return a.narrativeJobs.ResetForRetry(ctx, jobID)
}

// NarrativeMetricsForChunk returns promotion-score counters for chunkID.
// Returns zero-value metrics when the metrics store is not wired.
func (a *API) NarrativeMetricsForChunk(ctx context.Context, chunkID string) (NarrativeMetrics, error) {
	if a.narrativeMetrics == nil {
		return NarrativeMetrics{ChunkID: chunkID}, nil
	}
	m, err := a.narrativeMetrics.Get(ctx, chunkID)
	if err != nil {
		return NarrativeMetrics{ChunkID: chunkID}, err
	}
	w := narrative.DefaultPromotionWeights()
	return NarrativeMetrics{
		ChunkID:         m.ChunkID,
		Retrievals:      m.Retrievals,
		Citations:       m.Citations,
		UserPins:        m.UserPins,
		Score:           m.Score(w),
		LastRetrievedAt: m.LastRetrievedAt,
		LastCitedAt:     m.LastCitedAt,
	}, nil
}

// ── Capstone methods (memory-inspection-ui-01KX5R8E) ──────────────────────────

// ErrResummarizeRateLimited is returned by ResummarizeChunk when the same
// chunk was re-summarised within the last 60 seconds.
var ErrResummarizeRateLimited = errors.New("memory: re-summarize rate limit: wait 60s between calls per chunk")

// ErrChunkNotFound is returned by GetChunkProvenance when the chunk ID
// does not exist in the store.
var ErrChunkNotFound = errors.New("memory: chunk not found")

// LastRetrieval returns the most recent retrieval report for the given
// session from the process-scoped GlobalRetrievalHistory ring buffer.
// Returns an empty report (no error) when no retrieval has occurred
// for that session in the current process lifetime.
func (a *API) LastRetrieval(_ context.Context, sessionID string) (RetrievalReport, error) {
	rec, ok := corememory.GlobalRetrievalHistory().Last(sessionID)
	if !ok {
		return RetrievalReport{SessionID: sessionID}, nil
	}
	out := RetrievalReport{
		SessionID: rec.SessionID,
		Query:     rec.Query,
		Threshold: rec.Threshold,
		At:        rec.At,
		Results:   make([]ScoredChunk, 0, len(rec.Results)),
	}
	for _, r := range rec.Results {
		// Build a minimal Chunk for the wire shape — content is present;
		// other fields default to zero/empty since the history only stores
		// what the retriever had at call time.
		c := Chunk{
			ID:      r.ChunkID,
			Content: r.Content,
			Kind:    r.Kind,
			Pinned:  r.Pinned,
		}
		out.Results = append(out.Results, ScoredChunk{
			Chunk:      c,
			Similarity: r.Similarity,
			Injected:   r.Injected,
		})
	}
	return out, nil
}

// EmbeddingProbe embeds query against the wired embedder and returns up
// to limit scored chunks ranked by cosine similarity descending.
// limit is capped at 50. Returns ErrEmbedderUnavailable when no real
// embedder is wired.
func (a *API) EmbeddingProbe(ctx context.Context, query string, limit int) ([]ScoredChunk, error) {
	if a == nil || a.store == nil {
		return nil, ErrStoreUnavailable
	}
	if a.embedder == nil {
		return nil, ErrEmbedderUnavailable
	}
	if _, ok := a.embedder.(corememory.NoopEmbedder); ok {
		return nil, ErrEmbedderUnavailable
	}
	if query == "" {
		return nil, nil
	}
	const maxLimit = 50
	if limit <= 0 {
		limit = 10
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	vecs, err := a.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("memory: embed probe query: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, errors.New("memory: embedder returned no vectors")
	}
	results, err := a.store.Query(ctx, vecs[0], limit)
	if err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})
	out := make([]ScoredChunk, 0, len(results))
	for _, r := range results {
		out = append(out, ScoredChunk{
			Chunk:      toViewChunk(r.Chunk),
			Similarity: r.Similarity,
		})
	}
	return out, nil
}

// ResummarizeChunk re-runs narrative synthesis on a single chunk.
//
// Rate limit: at most one call per chunkID per 60 seconds.
//
// For chunks with TurnID set (narrative chunks): re-enqueues to the
// Promoter's job queue (async) and returns the current Chunk unchanged.
// The Promoter will update the chunk content asynchronously.
//
// For raw/legacy chunks (TurnID empty): runs ExtractiveBuilder
// inline and replaces the chunk in the store. Returns the updated Chunk.
func (a *API) ResummarizeChunk(ctx context.Context, chunkID string) (Chunk, error) {
	if a == nil || a.store == nil {
		return Chunk{}, ErrStoreUnavailable
	}

	// Rate-limit check.
	a.resummaryMu.Lock()
	last, seen := a.resummaryAt[chunkID]
	if seen && time.Since(last) < 60*time.Second {
		a.resummaryMu.Unlock()
		return Chunk{}, ErrResummarizeRateLimited
	}
	a.resummaryAt[chunkID] = time.Now().UTC()
	a.resummaryMu.Unlock()

	// Load the chunk.
	chunks, err := a.store.List(ctx)
	if err != nil {
		return Chunk{}, err
	}
	var found *corememory.Chunk
	for i := range chunks {
		if chunks[i].ID == chunkID {
			found = &chunks[i]
			break
		}
	}
	if found == nil {
		return Chunk{}, ErrChunkNotFound
	}

	// Narrative chunk with TurnID: re-enqueue to Promoter if available.
	if found.TurnID != "" && a.narrativeJobs != nil {
		job := narrative.Job{
			ID:        "resummary-" + found.TurnID,
			TurnID:    found.TurnID,
			SessionID: found.SessionID,
			RetryAt:   time.Now().UTC(),
		}
		// (FR-006) WARN-log on enqueue failure so a "re-summarize did nothing"
		// scenario is diagnosable. The view chunk is still returned (user-facing
		// re-summarize reports success on enqueue, not on completion).
		if err := a.narrativeJobs.Enqueue(ctx, job); err != nil {
			slog.WarnContext(ctx, "memory: resummarize enqueue failed",
				"turn_id",    found.TurnID,
				"session_id", found.SessionID,
				"error",      err.Error(),
			)
			// Surfaced to caller as an explicit error so the frontend can inform
			// the user the re-summarize was not enqueued (FR-007).
			return Chunk{}, fmt.Errorf("memory: resummarize not enqueued: %w", err)
		}
		return toViewChunk(*found), nil
	}

	// Raw/legacy chunk: run extractive fallback inline.
	eb := narrative.NewExtractiveBuilder()
	fallback := eb.BuildTurnFallback(found.Content, "", nil)
	newContent := fallback.String()

	// Replace the chunk atomically (Delete + Add).
	newID, err := newChunkID()
	if err != nil {
		return Chunk{}, err
	}
	updated := *found
	updated.ID = newID
	updated.Content = newContent
	updated.ContentHash = corememory.HashContent(newContent)
	updated.Kind = "narrative_extractive_fallback"

	// Re-embed the new content if an embedder is available.
	if a.embedder != nil {
		if _, ok := a.embedder.(corememory.NoopEmbedder); !ok {
			vecs, embedErr := a.embedder.Embed(ctx, []string{newContent})
			if embedErr == nil && len(vecs) > 0 {
				updated.Embedding = vecs[0]
			}
		}
	}

	if err := a.store.Delete(ctx, chunkID); err != nil {
		return Chunk{}, fmt.Errorf("memory: delete old chunk: %w", err)
	}
	if err := a.store.Add(ctx, updated); err != nil {
		return Chunk{}, fmt.Errorf("memory: add summarised chunk: %w", err)
	}
	return toViewChunk(updated), nil
}

// GetChunkProvenance returns the full audit chain for a chunk.
// Aggregates data from the store + narrative metrics + embedder info.
// No new SQL tables are required.
func (a *API) GetChunkProvenance(ctx context.Context, chunkID string) (ChunkProvenance, error) {
	if a == nil || a.store == nil {
		return ChunkProvenance{}, ErrStoreUnavailable
	}
	chunks, err := a.store.List(ctx)
	if err != nil {
		return ChunkProvenance{}, err
	}
	var found *corememory.Chunk
	for i := range chunks {
		if chunks[i].ID == chunkID {
			found = &chunks[i]
			break
		}
	}
	if found == nil {
		return ChunkProvenance{}, ErrChunkNotFound
	}

	prov := ChunkProvenance{
		ChunkID:        found.ID,
		SourceTurn:     found.SourceTurn,
		HookBoundary:   found.Source,
		Kind:           found.Kind,
		ScopePath:      buildScopePath(found.ScopeKind),
		Pinned:         found.Pinned,
		RetrievalCount: found.RecallCount,
		CreatedAt:      found.CreatedAt,
	}

	// Embedder metadata (read from the wired instance — no network call).
	if a.embedder != nil {
		if _, ok := a.embedder.(corememory.NoopEmbedder); !ok {
			prov.EmbedderKind = a.embedder.Kind()
			prov.EmbedDimensions = a.embedder.Dimensions()
		}
	}

	// Narrative metrics (optional — zero-value safe).
	if a.narrativeMetrics != nil {
		m, merr := a.narrativeMetrics.Get(ctx, chunkID)
		if merr == nil {
			prov.CitationCount = m.Citations
			w := narrative.DefaultPromotionWeights()
			prov.PromotionScore = m.Score(w)
		}
	}

	return prov, nil
}

// buildScopePath converts a ScopeKind constant to a human-readable path
// string for the provenance drawer. The path shows the progression from
// narrowest to widest scope that a chunk could be promoted through.
func buildScopePath(scopeKind string) string {
	switch scopeKind {
	case corememory.ScopeKindSession:
		return "session"
	case corememory.ScopeKindProject:
		return strings.Join([]string{"session", "project"}, " → ")
	case corememory.ScopeKindGlobal:
		return strings.Join([]string{"session", "project", "global"}, " → ")
	case corememory.ScopeKindLongTerm:
		return strings.Join([]string{"session", "project", "global", "long_term"}, " → ")
	default:
		return scopeKind
	}
}

// Compile-time witness.
var _ MemoryAPI = (*API)(nil)
