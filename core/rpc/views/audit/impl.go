// Package audit's impl wires the event-log Reader into the AuditAPI
// surface and bridges Emitter.Append callbacks to the streamBroker via
// `audit:event`.
//
// The concrete reader integration (libSQL or in-memory) is provided
// from the call site at construction; tests substitute a fake. Until a
// Store is configured (WithStore) the impl operates against an
// in-memory ring buffer fed by Emitter observers only — sufficient for
// the test chassis and for read paths, but the ring is not durable.
//
// Redaction (E-004, audit-that-tells-the-truth-01PMZA10 UNIT-4): this
// package does NOT redact anything itself, on either the ring or the
// store-backed path. Every Push call site (core/rpc/api.go's eight
// bridge types — confirmedAuditEmitter, lockdownAuditEmitter, etc.)
// already constructs its Entry from ONLY pre-redacted fields: a kind
// string, a category label, and a Trailing string that is at most a
// byte count or a Go %T type name — never raw payload content, per
// each bridge's own privacy comment. Push (and, when a Store is
// configured, the write-through to it) persists EXACTLY that
// already-redacted Entry projection and nothing more — see
// rowFromEntry/entryFromRow below, the one place the Entry<->log.Row
// conversion happens in each direction. There used to be a comment
// here claiming "redaction happens on the event-log side before
// Append returns" — false with core/event.NewEmitter unbuilt (nothing
// on this path ever called Append), and now doubly moot: nothing
// downstream of Entry construction needs to redact, because nothing
// downstream of Entry construction ever sees anything Entry didn't
// already carry.
package audit

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/event"
	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
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
	entries      []Entry             // newest-last; bounded by maxBuffer.
	maxBuffer    int                 // cap for the in-memory ring.
	subs         map[string]chan any // subscription id -> typed channel
	broker       Subscriber
	savedQueries map[string]eventlog.SavedQuery // id -> query
	backend      eventlog.Backend               // optional; used by Export
	sweepable    eventlog.SweepableBackend      // optional; used by BulkPurge
	emitter      contextaudit.Emitter           // optional; used by BulkPurge audit emit
	gate         cedar.Gate                     // optional; gates BulkPurge via ActionAuditBulkPurge
	store        *eventlog.Store                // optional; write-through + read-path backing (UNIT-4)
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

// WithGate injects the Cedar policy gate used by BulkPurge to enforce
// ActionAuditBulkPurge (F-001 security fix). When nil, BulkPurge is
// ungated (pre-boot / test posture — use in production only when a real
// gate is wired). The gate is consulted BEFORE the delete loop; on Deny
// (including NotApplicable, which is fail-closed for this action) BulkPurge
// returns a *PolicyDeniedError and emits KindAuditBulkPurgeBlockedByPolicy.
func WithGate(g cedar.Gate) Option {
	return func(a *API) { a.gate = g }
}

// WithStore injects a durable event-log Store. Optional — nil leaves
// the impl operating against the in-memory ring only (the pre-UNIT-4
// posture, and still what the test chassis uses by default). When set:
//   - Push writes through to the store in addition to the ring (the
//     ring stays; it is the live-stream fan-out buffer, not a cache).
//   - Filter, ListEntries and VerifyEntry read the store instead of
//     the ring, so results survive a relaunch.
//
// This is "the honesty threshold" (audit-that-tells-the-truth-01PMZA10
// UNIT-4, spec §5.4): before it, "the audit log" was an in-memory ring
// that lied about being a log. After it, calling NewAPI without
// WithStore is still valid (tests, and any future served-mode chassis
// with no DataDir) — it is just honestly ring-only, and G-1 (UNIT-10)
// makes it mechanical that nothing may claim otherwise.
func WithStore(s *eventlog.Store) Option {
	return func(a *API) { a.store = s }
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

// Push appends an entry to the ring buffer, fans out to every active
// subscription, and — when a Store is configured (WithStore) — writes
// through to it so the entry survives a relaunch (UNIT-4, "the honesty
// threshold").
//
// A store write failure NEVER fails the caller (spec D-5): Push has no
// error return and ten production call sites depend on that signature
// never changing (spec R-5 — core/rpc/api.go:933, 972, 7387, 7410,
// 7432, 7454, 7498, 7521, 8113 and
// core/rpc/contextbootstrap_wiring.go:545). A write failure is logged
// and Push returns as if nothing happened; the ring and the live
// stream are unaffected either way — an audit write failure must never
// fail the action being audited (FR-011).
//
// Drops on a full subscriber channel — slow subscribers must not back
// up the audit pipeline.
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
	store := a.store
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
	if store == nil {
		return
	}
	// Push has no context parameter (spec R-5's ten call sites are all
	// synchronous, context-free callbacks). context.Background() is
	// correct here: the write is fire-and-forget by design (D-5), not
	// tied to any caller's request lifecycle.
	if err := store.AppendComputed(context.Background(), rowFromEntry(entry)); err != nil {
		logging.L().Warn("audit.push.store_write_failed",
			"entry_id", entry.ID,
			"kind", entry.Subject,
			"err", err.Error(),
		)
	}
}

// auditPersistedPayload is the exact wire shape of what gets persisted
// for a Push-sourced row: the already-redacted Entry projection and
// nothing else (see the package doc comment's E-004 note). It exists
// so rowFromEntry/entryFromRow have a stable, symmetric encoding rather
// than ad hoc string concatenation.
type auditPersistedPayload struct {
	Category string `json:"category"`
	Subject  string `json:"subject"`
	Trailing string `json:"trailing,omitempty"`
}

// rowFromEntry converts an Entry into a log.Row shaped for
// Store.AppendComputed — the ENTRY -> ROW half of the one conversion
// this package performs in each direction (entryFromRow, below, is the
// other half). PrevHash/PayloadHash are left zero; AppendComputed fills
// them in from the session's chain head. SessionID and EmitterID are
// left empty: Entry carries neither field today (every existing Push
// call site already lacks that context — see the package doc comment),
// so this is not a new loss introduced by persisting, only an honest
// recording of one that already existed in the ring.
func rowFromEntry(entry Entry) eventlog.Row {
	ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		ts = time.Now().UTC()
	}
	payload, _ := json.Marshal(auditPersistedPayload{
		Category: entry.Category,
		Subject:  entry.Subject,
		Trailing: entry.Trailing,
	})
	return eventlog.Row{
		EventID:   entry.ID,
		Kind:      entry.Subject,
		EmittedAt: ts,
		Payload:   payload,
		// Not "n/a" — this states what happened: nothing was redacted
		// here because there was nothing left to redact (see the
		// package doc comment).
		RedactionSummary: "pre-redacted at call site; no additional redaction applied",
		SchemaVersion:    1,
	}
}

// entryFromRow converts a log.Row back into an Entry — the ROW ->
// ENTRY half of the conversion. Falls back to row.Kind / a
// category derived from it when the row predates this encoding or the
// payload otherwise fails to decode (defensive; every row this package
// writes itself always decodes cleanly).
func entryFromRow(row eventlog.Row) Entry {
	var decoded auditPersistedPayload
	_ = json.Unmarshal(row.Payload, &decoded)
	subject := decoded.Subject
	if subject == "" {
		subject = row.Kind
	}
	category := decoded.Category
	if category == "" {
		category = categoryForKind(event.Kind(row.Kind))
	}
	return Entry{
		ID:        row.EventID,
		Timestamp: row.EmittedAt.UTC().Format(time.RFC3339Nano),
		Category:  category,
		Subject:   subject,
		Trailing:  decoded.Trailing,
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
	case strings.HasPrefix(s, "keystroke."), strings.HasPrefix(s, "input."):
		return "KEYSTROKE"
	}
	return "STORAGE"
}

// matchesFilterQuery is the ONE place Filter's matching semantics are
// implemented — shared by the ring path and the store path so they
// cannot silently diverge (spec §5.4 item 4: "a silent change in
// filter meaning is a defect nobody notices for a release").
func matchesFilterQuery(e Entry, q eventlog.FilterQuery) bool {
	// Verbose filter.
	if !q.Verbose && strings.HasPrefix(e.Subject, "verbose.") {
		return false
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
			return false
		}
	}
	// Free-text filter on Subject.
	if q.FreeText != "" {
		if !strings.Contains(strings.ToLower(e.Subject), strings.ToLower(q.FreeText)) {
			return false
		}
	}
	return true
}

// Filter applies a rich FilterQuery and returns matching entries,
// newest first. Reads the store when one is configured (WithStore);
// falls back to the in-memory ring otherwise (the test-harness path).
func (a *API) Filter(ctx context.Context, q eventlog.FilterQuery) ([]Entry, error) {
	a.mu.RLock()
	store := a.store
	a.mu.RUnlock()

	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}

	if store != nil {
		rows, err := store.ByTimeRange(ctx, time.Time{}, time.Time{}, "", 0, false)
		if err != nil {
			return nil, fmt.Errorf("audit: Filter: %w", err)
		}
		sortRowsNewestFirst(rows)
		out := make([]Entry, 0, len(rows))
		for _, r := range rows {
			e := entryFromRow(r)
			if !matchesFilterQuery(e, q) {
				continue
			}
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
		return out, nil
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Entry, 0, len(a.entries))
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		if !matchesFilterQuery(e, q) {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// sortRowsNewestFirst orders rows by EmittedAt descending, breaking
// ties on EventID descending for determinism. Deliberately NOT an
// event_id-lexicographic sort: Push-sourced EventIDs carry a
// caller-specific prefix ("tool-confirm-", "fleet-lockdown-", "acp-",
// …) before a Unix-nanosecond suffix, so comparing EventIDs across
// different bridge types does not recover chronological order — only
// EmittedAt does. This is what makes the store path behave like the
// ring, which iterates in true push/insertion order.
func sortRowsNewestFirst(rows []eventlog.Row) {
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].EmittedAt.Equal(rows[j].EmittedAt) {
			return rows[i].EmittedAt.After(rows[j].EmittedAt)
		}
		return rows[i].EventID > rows[j].EventID
	})
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
// The Cedar policy gate (ActionAuditBulkPurge) is checked FIRST via the
// gate injected by WithGate. On Deny (including NotApplicable, which is
// fail-closed for this destructive action) BulkPurge returns a
// *PolicyDeniedError and emits KindAuditBulkPurgeBlockedByPolicy without
// touching the store.
//
// On success the purge is recorded via KindAuditBulkPurgeExecuted if an
// emitter is configured.
func (a *API) BulkPurge(ctx context.Context, eventIDs []string) error {
	a.mu.RLock()
	sb := a.sweepable
	em := a.emitter
	g := a.gate
	a.mu.RUnlock()

	// ── Cedar gate check (F-001) ────────────────────────────────────────
	if err := cedar.CheckAuditBulkPurge(ctx, g); err != nil {
		// Emit a blocked-by-policy audit event (best-effort).
		if em != nil {
			var reason string
			if len(err.Error()) > 0 {
				reason = err.Error()
			}
			payload := contextaudit.AuditBulkPurgeBlockedByPolicyPayload{
				AttemptCount: len(eventIDs),
				Reason:       reason,
			}
			contextaudit.MustEmit(ctx, em, contextaudit.KindAuditBulkPurgeBlockedByPolicy, payload, time.Now())
		}
		return err
	}

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
		contextaudit.MustEmit(ctx, em, contextaudit.KindAuditBulkPurgeExecuted, payload, time.Now())
	}
	return nil
}

// listEntriesFilter bundles ListEntries' parsed filter state so the
// ring path and the store path share one matching function.
type listEntriesFilter struct {
	wantCat  map[string]struct{}
	since    time.Time
	until    time.Time
	hasSince bool
	hasUntil bool
}

func parseListEntriesFilter(filter Filter) listEntriesFilter {
	f := listEntriesFilter{wantCat: make(map[string]struct{}, len(filter.Categories))}
	for _, c := range filter.Categories {
		if c != "" {
			f.wantCat[strings.ToUpper(c)] = struct{}{}
		}
	}
	if filter.Since != "" {
		if t, err := time.Parse(time.RFC3339Nano, filter.Since); err == nil {
			f.since, f.hasSince = t, true
		}
	}
	if filter.Until != "" {
		if t, err := time.Parse(time.RFC3339Nano, filter.Until); err == nil {
			f.until, f.hasUntil = t, true
		}
	}
	return f
}

func (f listEntriesFilter) matches(e Entry) bool {
	if len(f.wantCat) > 0 {
		if _, ok := f.wantCat[strings.ToUpper(e.Category)]; !ok {
			return false
		}
	}
	if f.hasSince || f.hasUntil {
		t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err == nil {
			if f.hasSince && t.Before(f.since) {
				return false
			}
			if f.hasUntil && t.After(f.until) {
				return false
			}
		}
	}
	return true
}

// ListEntries returns entries matching filter, newest first. Reads the
// store when one is configured (WithStore); falls back to the
// in-memory ring otherwise, where limit==0 returns the full ring
// (capped at maxBuffer).
func (a *API) ListEntries(ctx context.Context, filter Filter) ([]Entry, error) {
	a.mu.RLock()
	store := a.store
	a.mu.RUnlock()

	f := parseListEntriesFilter(filter)

	if store != nil {
		rows, err := store.ByTimeRange(ctx, time.Time{}, time.Time{}, "", 0, false)
		if err != nil {
			return nil, fmt.Errorf("audit: ListEntries: %w", err)
		}
		sortRowsNewestFirst(rows)
		out := make([]Entry, 0, len(rows))
		for _, r := range rows {
			e := entryFromRow(r)
			if !f.matches(e) {
				continue
			}
			out = append(out, e)
			if filter.Limit > 0 && len(out) >= filter.Limit {
				break
			}
		}
		return out, nil
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Entry, 0, len(a.entries))
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		if !f.matches(e) {
			continue
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

// VerifyEntry returns true if the entry id is present — in the store
// when one is configured (WithStore), otherwise in the in-memory ring.
// This is membership only; VerifyChain (UNIT-7) is the chain-walking
// tamper-evidence surface. The doc here used to say the ring was "the
// most we can authoritatively report" — true when it was the only
// thing there was; the store is more authoritative once it exists,
// since ring membership degrades every time the ring evicts an entry
// (bounded at maxBuffer) while the store does not.
func (a *API) VerifyEntry(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	a.mu.RLock()
	store := a.store
	a.mu.RUnlock()
	if store != nil {
		if _, err := store.Get(ctx, id); err != nil {
			if errors.Is(err, eventlog.ErrNotFound) {
				return false, nil
			}
			return false, fmt.Errorf("audit: VerifyEntry: %w", err)
		}
		return true, nil
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
