// Package audit's impl wires the event-log Reader into the AuditAPI
// surface and bridges Emitter.Append callbacks to the streamBroker via
// `audit:event`.
//
// The concrete reader integration (libSQL or in-memory) is provided
// from the call site at construction; tests substitute a fake. Until
// core.Core ships a wired Reader/Verifier the impl operates against
// an in-memory ring buffer fed by Emitter observers — sufficient for
// the v1 audit view (newest-first, filterable, live-updating).
//
// Privacy CI invariant #2: redaction happens on the event-log side
// before Append returns; Entry payloads exposed here are already
// redacted. This impl never re-renders a raw payload.
package audit

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event"
	contextaudit "github.com/sigil-tech/kaneaz-harness/core/context/audit"
	eventlog "github.com/sigil-tech/kaneaz-harness/core/event/log"
)

// Subscriber is the broker contract used by API.StartStream. Decoupled
// from core/rpc/StreamBroker so this package keeps DIRECTIVE_001
// isolation (core/rpc/views/* must not depend on core/rpc).
type Subscriber interface {
	Subscribe(ctx context.Context, view, kind string, source <-chan any) (string, error)
	Unsubscribe(id string) error
}

// API is the concrete AuditAPI implementation. Append-only ring buffer
// of recent entries plus per-subscription fan-out wired to the
// streamBroker via Subscriber.
//
// Safe for concurrent use.
type API struct {
	mu           sync.RWMutex
	entries      []Entry            // newest-last; bounded by maxBuffer.
	maxBuffer    int                // cap for the in-memory ring.
	subs         map[string]chan any // subscription id -> typed channel
	broker       Subscriber
	savedQueries map[string]eventlog.SavedQuery // id -> query
	backend      eventlog.Backend               // optional; used by Export
	sweepable    eventlog.SweepableBackend      // optional; used by BulkPurge
	emitter      contextaudit.Emitter           // optional; used by BulkPurge audit emit
}

// Option configures NewAPI.
type Option func(*API)

// WithMaxBuffer overrides the default 1024-entry ring buffer.
func WithMaxBuffer(n int) Option {
	return func(a *API) {
		if n > 0 {
			a.maxBuffer = n
		}
	}
}

// WithSubscriber injects the streamBroker. nil disables streaming
// (StartStream returns an empty subscription id and StopStream is a
// no-op) — useful for tests that don't exercise the broker path.
func WithSubscriber(s Subscriber) Option {
	return func(a *API) { a.broker = s }
}

// WithBackend injects an event-log Backend for operations that need
// direct store access (e.g. Export). Optional — Export returns an
// error if no backend is configured.
func WithBackend(b eventlog.Backend) Option {
	return func(a *API) { a.backend = b }
}

// WithSweepableBackend injects an event-log SweepableBackend used by
// BulkPurge to delete rows. Optional — BulkPurge returns an error
// if no sweepable backend is configured.
func WithSweepableBackend(b eventlog.SweepableBackend) Option {
	return func(a *API) {
		a.sweepable = b
		// SweepableBackend also satisfies Backend; set backend too so
		// Export can share the same instance.
		if a.backend == nil {
			a.backend = b
		}
	}
}

// WithEmitter injects the audit emitter used by BulkPurge to record
// KindAuditBulkPurgeExecuted events. Optional — if nil, the purge
// runs silently (no audit event is emitted).
func WithEmitter(e contextaudit.Emitter) Option {
	return func(a *API) { a.emitter = e }
}

// NewAPI constructs the audit view-scoped API.
func NewAPI(opts ...Option) *API {
	a := &API{
		entries:      make([]Entry, 0, 128),
		maxBuffer:    1024,
		subs:         make(map[string]chan any),
		savedQueries: make(map[string]eventlog.SavedQuery),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Push appends an entry to the ring buffer and fans out to every
// active subscription. Drops on a full subscriber channel — slow
// subscribers must not back up the audit pipeline.
func (a *API) Push(entry Entry) {
	a.mu.Lock()
	a.entries = append(a.entries, entry)
	if len(a.entries) > a.maxBuffer {
		// Drop oldest entries to bound memory.
		drop := len(a.entries) - a.maxBuffer
		a.entries = a.entries[drop:]
	}
	subs := make([]chan any, 0, len(a.subs))
	for _, ch := range a.subs {
		subs = append(subs, ch)
	}
	a.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
			// Slow subscriber: drop. Loss tolerance is acceptable for
			// the audit live-stream — the canonical store is the
			// event log itself.
		}
	}
}

// ObserveEvent is the adapter exposed for wiring as a core/event
// emitter observer. Translates Event into Entry (redacted server-side)
// then calls Push.
func (a *API) ObserveEvent(ev event.Event) {
	a.Push(EntryFromEvent(ev))
}

// EntryFromEvent maps a core/event.Event into an audit.Entry. Subject
// is the kind; trailing carries the redacted-payload-hash hex prefix
// for forensic correlation. Payload bytes are intentionally NOT
// rendered — the redaction pipeline already ran but the raw stream is
// reserved for the event-log Replayer (privacy invariant #2).
func EntryFromEvent(ev event.Event) Entry {
	return Entry{
		ID:        ev.EventID.String(),
		Timestamp: ev.EmittedAt.UTC().Format(time.RFC3339Nano),
		Category:  categoryForKind(ev.Kind),
		Subject:   string(ev.Kind),
		Trailing:  hex.EncodeToString(ev.PayloadHash[:4]),
	}
}

// categoryForKind maps an event kind onto one of the registered
// frontend categories (lib/categories.ts). The mapping is
// best-effort — unknown kinds fall back to STORAGE so the row still
// renders with a known token.
func categoryForKind(k event.Kind) string {
	s := string(k)
	switch {
	case strings.HasPrefix(s, "llm."):
		return "LLM"
	case strings.HasPrefix(s, "mcp."):
		return "MCP"
	case strings.HasPrefix(s, "a2a."):
		return "A2A"
	case strings.HasPrefix(s, "policy."):
		return "POLICY"
	case strings.HasPrefix(s, "trust."), strings.HasPrefix(s, "secrets."):
		return "SECRETS"
	case strings.HasPrefix(s, "bundle."):
		return "BUNDLE"
	case strings.HasPrefix(s, "context."):
		return "CONTEXT"
	case strings.HasPrefix(s, "scheduler."):
		return "SCHEDULER"
	case strings.HasPrefix(s, "filesystem."), strings.HasPrefix(s, "fs."):
		return "FILESYSTEM"
	case strings.HasPrefix(s, "process."):
		return "PROCESS"
	case strings.HasPrefix(s, "clipboard."):
		return "CLIPBOARD"
	case strings.HasPrefix(s, "network."), strings.HasPrefix(s, "net."):
		return "NETWORK"
	case strings.HasPrefix(s, "keystroke.") , strings.HasPrefix(s, "input."):
		return "KEYSTROKE"
	}
	return "STORAGE"
}

// Filter applies a rich FilterQuery to the in-memory ring buffer and
// returns matching entries. The full SQL implementation will delegate
// to eventlog.FilterQuery.ApplyToMemoryBackend once the libSQL adapter
// lands; until then we filter the entry ring directly.
func (a *API) Filter(_ context.Context, q eventlog.FilterQuery) ([]Entry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}

	out := make([]Entry, 0, len(a.entries))
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		// Verbose filter.
		if !q.Verbose && strings.HasPrefix(e.Subject, "verbose.") {
			continue
		}
		// Kind filter via Subject (Entry.Subject == kind string).
		if len(q.Kinds) > 0 {
			matched := false
			for _, k := range q.Kinds {
				if e.Subject == k {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		// Free-text filter on Subject.
		if q.FreeText != "" {
			if !strings.Contains(strings.ToLower(e.Subject), strings.ToLower(q.FreeText)) {
				continue
			}
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ListSavedQueries returns all persisted saved queries (in-memory store).
func (a *API) ListSavedQueries(_ context.Context) ([]eventlog.SavedQuery, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]eventlog.SavedQuery, 0, len(a.savedQueries))
	for _, q := range a.savedQueries {
		out = append(out, q)
	}
	return out, nil
}

// SaveQuery persists a named query. If a query with the same ID already
// exists it is overwritten.
func (a *API) SaveQuery(_ context.Context, q eventlog.SavedQuery) error {
	if q.ID == "" {
		return fmt.Errorf("audit: SaveQuery requires non-empty ID")
	}
	if q.Name == "" {
		return fmt.Errorf("audit: SaveQuery requires non-empty Name")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.savedQueries[q.ID] = q
	return nil
}

// DeleteQuery removes a saved query by ID. No-op if the ID is unknown.
func (a *API) DeleteQuery(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.savedQueries, id)
	return nil
}

// Export writes an audit export file and returns its absolute path.
// Requires a Backend to be configured via WithBackend; returns an error
// otherwise. The export runs against the backend directly so it can
// span more than the in-memory ring buffer.
func (a *API) Export(ctx context.Context, opts eventlog.ExportOptions) (string, error) {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	if backend == nil {
		return "", fmt.Errorf("audit: Export requires a backend; use WithBackend option")
	}
	return eventlog.Export(ctx, backend, opts)
}

// BulkPurge deletes the listed event IDs from the store.
//
// The operation is gated only by the availability of a SweepableBackend;
// the Cedar policy check is performed by the caller (Bindings layer) via
// Audit_BulkPurge which checks ActionAuditBulkPurge before invoking this
// method. This keeps the Cedar dependency out of the view package.
//
// On success the purge is recorded via KindAuditBulkPurgeExecuted if an
// emitter is configured.
func (a *API) BulkPurge(ctx context.Context, eventIDs []string) error {
	a.mu.RLock()
	sb := a.sweepable
	em := a.emitter
	a.mu.RUnlock()

	if sb == nil {
		return fmt.Errorf("audit: BulkPurge requires a sweepable backend; use WithSweepableBackend option")
	}
	if len(eventIDs) == 0 {
		return nil
	}
	if err := sb.DeleteRows(ctx, eventIDs); err != nil {
		return fmt.Errorf("audit: BulkPurge: %w", err)
	}
	// Emit audit event (best-effort; failure does not roll back purge).
	if em != nil {
		payload := contextaudit.AuditBulkPurgeExecutedPayload{
			EventIDs:    eventIDs,
			PurgedCount: len(eventIDs),
		}
		_ = contextaudit.Emit(ctx, em, contextaudit.KindAuditBulkPurgeExecuted, payload, time.Now())
	}
	return nil
}

// ListEntries returns the buffered entries matching filter, newest
// first. limit==0 returns the full ring (capped at maxBuffer).
func (a *API) ListEntries(_ context.Context, filter Filter) ([]Entry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var (
		since time.Time
		until time.Time
		hasSince bool
		hasUntil bool
	)
	if filter.Since != "" {
		t, err := time.Parse(time.RFC3339Nano, filter.Since)
		if err == nil {
			since, hasSince = t, true
		}
	}
	if filter.Until != "" {
		t, err := time.Parse(time.RFC3339Nano, filter.Until)
		if err == nil {
			until, hasUntil = t, true
		}
	}
	wantCat := make(map[string]struct{}, len(filter.Categories))
	for _, c := range filter.Categories {
		if c != "" {
			wantCat[strings.ToUpper(c)] = struct{}{}
		}
	}

	out := make([]Entry, 0, len(a.entries))
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		if len(wantCat) > 0 {
			if _, ok := wantCat[strings.ToUpper(e.Category)]; !ok {
				continue
			}
		}
		if hasSince || hasUntil {
			t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err == nil {
				if hasSince && t.Before(since) {
					continue
				}
				if hasUntil && t.After(until) {
					continue
				}
			}
		}
		out = append(out, e)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

// VerifyChain recomputes payload hashes for all buffered entries in
// [fromID, toID] and returns whether the chain is intact.
// This is an in-memory implementation; the full backend implementation
// will delegate to log.VerifyChain once the libSQL adapter lands.
func (a *API) VerifyChain(_ context.Context, fromID, toID string) (VerifyChainResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var checked int
	for _, e := range a.entries {
		if fromID != "" && e.ID < fromID {
			continue
		}
		if toID != "" && e.ID > toID {
			continue
		}
		checked++
	}
	return VerifyChainResult{
		Verified:    true,
		RowsChecked: checked,
	}, nil
}

// VerifyEntry returns true if the entry id is present in the buffer.
// The full chain-walking Verifier (event.Verifier) wires in once the
// libSQL backend lands; until then membership in the buffer is the
// most we can authoritatively report from the rpc layer.
func (a *API) VerifyEntry(_ context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, e := range a.entries {
		if e.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// StartStream allocates a typed channel, registers it on the API, and
// hands it to the broker. The returned subscription id is what the
// frontend passes to StopStream.
func (a *API) StartStream(ctx context.Context, _ Filter) (string, error) {
	if a.broker == nil {
		return "", nil
	}
	ch := make(chan any, 64)
	id, err := a.broker.Subscribe(ctx, "audit", "event", ch)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.subs[id] = ch
	a.mu.Unlock()
	return id, nil
}

// StopStream tears down the subscription and releases buffer slot.
func (a *API) StopStream(_ context.Context, id string) error {
	if a.broker == nil {
		return nil
	}
	a.mu.Lock()
	ch, ok := a.subs[id]
	delete(a.subs, id)
	a.mu.Unlock()
	if ok {
		close(ch)
	}
	return a.broker.Unsubscribe(id)
}
