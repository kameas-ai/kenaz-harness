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
	"sync"
	"time"

	corememory "github.com/sigil-tech/kaneaz-harness/core/memory"
	"github.com/sigil-tech/kaneaz-harness/core/memory/prune"
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

// API is the concrete MemoryAPI.
type API struct {
	store    corememory.Store
	embedder corememory.Embedder
	reader   MessageReader
	rules    prune.Rules
	journal  JournalSource

	mu     sync.Mutex
	stats  []PruneStats
}

// Config bundles dependencies for New. Embedder + Reader are required
// for the explicit RememberMessage path; ListChunks / Forget continue
// working when only Store is wired (the rpc layer's degraded mode).
//
// PruneRules + Journal are Bundle E WP15/WP16 additions; both are
// optional. When PruneRules is the zero value the prune surface uses
// prune.DefaultRules(). When Journal is nil, JournalTail returns an
// empty slice.
type Config struct {
	Store      corememory.Store
	Embedder   corememory.Embedder
	Reader     MessageReader
	PruneRules prune.Rules
	Journal    JournalSource
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
		store:    cfg.Store,
		embedder: cfg.Embedder,
		reader:   cfg.Reader,
		rules:    rules,
		journal:  cfg.Journal,
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
		ID:            c.ID,
		SessionID:     c.SessionID,
		ProjectID:     c.ProjectID,
		ScopeKind:     c.ScopeKind,
		ScopeID:       c.ScopeID,
		SourceTurn:    c.SourceTurn,
		Content:       c.Content,
		ContentHash:   c.ContentHash,
		ToolName:      c.ToolName,
		FilesRead:     c.FilesRead,
		FilesModified: c.FilesModified,
		Title:         c.Title,
		CreatedAt:     c.CreatedAt,
		Pinned:        c.Pinned,
		RecallCount:   c.RecallCount,
		LastAccessed:  c.LastAccessed,
		Source:        c.Source,
	}
}

// Compile-time witness.
var _ MemoryAPI = (*API)(nil)
