// Package fleet — audit_archive.go
//
// Archiver streams local hash-chained audit events to the fleet
// POST /api/v1/audit/append endpoint. Batches by 100 events or 10s
// (whichever comes first). Each batch is signed with the device ed25519
// key and delivered with a cursor that advances on ACK.
//
// Key behaviours:
//   - Capability-gated: CapAuditLogImmuDB must be true.
//   - Hash-chain verification before each send (WP03 hooks in via chainVerifier).
//   - Cursor persistence: <DataDir>/fleet/audit_cursor.txt (atomic rename).
//   - Backoff on transient errors; hard-stop on chain-break (FR-005).
//   - Rate limit: max 1 batch per 10s (NFR-001).
//   - Payload cap: 10 MB per batch (NFR-002); oversized events are split.
//
// (fleet-audit-archival-01NDFSEX13 WP02)
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

const (
	// auditBatchSize is the maximum number of events per batch.
	auditBatchSize = 100
	// auditBatchInterval is the maximum wait time before flushing a partial batch.
	auditBatchInterval = 10 * time.Second
	// auditPayloadMax is the maximum JSON payload size per batch (10 MiB).
	auditPayloadMax = 10 * 1024 * 1024
	// auditArchiveEndpoint is the fleet endpoint for audit batch upload.
	auditArchiveEndpoint = "/api/v1/audit/append"
	// auditBackoffBase is the initial backoff on transient errors.
	auditBackoffBase = 30 * time.Second
	// auditBackoffMax is the maximum backoff on persistent errors.
	auditBackoffMax = 15 * time.Minute
)

// AuditChainVerifier verifies the hash chain of a candidate batch before
// sending. Returns (true, "") when the chain is intact. Returns
// (false, brokenAtID) on first break; the archiver halts on false.
type AuditChainVerifier interface {
	VerifyBatch(events []contextaudit.TailEvent) (ok bool, brokenAtID string)
}

// AuditHTTPPoster is a narrow interface for posting a batch to the fleet endpoint.
// In production it is backed by *Client; in tests it can be a stub.
type AuditHTTPPoster interface {
	Post(ctx context.Context, path, contentType string, body io.Reader) (*http.Response, error)
}

// AuditArchiverConfig holds the knobs for Archiver construction.
type AuditArchiverConfig struct {
	// Client is the fleet HTTP client. Archiver is a no-op when nil.
	Client *Client
	// Poster overrides Client for posting batches. When set, Client is not
	// used for HTTP (Client is still checked for isNop / nil guard). If nil,
	// Client.Post is used. This field is intended for tests.
	Poster AuditHTTPPoster
	// DataDir is the harness data dir for cursor persistence.
	DataDir string
	// Tail is the cursor-based reader for local audit events.
	Tail contextaudit.TailReader
	// Signer signs each batch payload with the device ed25519 key.
	Signer Signer
	// Verifier performs hash-chain verification on each candidate batch.
	// When nil, chain verification is skipped (tests).
	Verifier AuditChainVerifier
	// Emitter is used to emit audit events (chain-break, archived).
	Emitter contextaudit.Emitter
	// CapCheck is called before the main loop; archiver stays dormant
	// when the function returns false (CapAuditLogImmuDB not enabled).
	// When nil, the archiver runs unconditionally (tests/dev).
	CapCheck func() bool
	// BatchSize overrides auditBatchSize (0 = default).
	BatchSize int
	// BatchInterval overrides auditBatchInterval (0 = default).
	BatchInterval time.Duration
}

// AuditArchiver streams local audit events to the fleet endpoint.
// Constructed via NewAuditArchiver; started with Start.
type AuditArchiver struct {
	cfg AuditArchiverConfig
	// lifeMu guards cancel / done / parentCtx / stopped: Start can now run
	// again from the fleet session-reset hook (ResetUnsupported) while Stop
	// runs at shutdown.
	lifeMu    sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	parentCtx context.Context
	stopped   bool
	running   atomic.Bool
	mu        sync.RWMutex
	cursor    string
	chainErr  atomic.Bool // true after a chain-break hard-stop

	// status fields for the Compliance RPC view.
	lastArchivedAt atomic.Int64 // unix nano; 0 = never
	pendingCount   atomic.Int64

	// unsupported latches when POST /api/v1/audit/append answers a plain
	// (non-JSON) 404: kenaz-fleet registers no such route (verified
	// 2026-10-05). The loop then exits — IsRunning() reports false and
	// Unsupported() true, so the Compliance panel says "not supported by
	// this fleet server" — and nothing is re-posted until ResetUnsupported
	// (fleet sign-in / sign-out) restarts it; events stay in the local
	// hash-chained log (unsupported_endpoint.go).
	unsupported atomic.Bool

	// tooLarge latches when fleet answers 413 to a batch (fleet contract
	// 2026-10-06: 413 is PERMANENT — the same batch can never be accepted;
	// content problems come back as 200 + a per-event report instead).
	// The loop idles without posting (events stay in the local log) until
	// a fleet session reset clears it (ResetUnsupported).
	tooLarge atomic.Bool

	// rejectedEvents counts events fleet refused per-event in a 200
	// response (rejected_events[], e.g. payload_too_large). The cursor
	// advances past them — retrying cannot change the answer — but they are
	// logged and counted, never silent (review F10).
	rejectedEvents atomic.Int64

	// orgPaused is true while the last flush was refused 403 org_paused
	// (kenaz-fleet #206 staff pause). TRANSIENT: the loop keeps its normal
	// backoff, nothing latches, events stay in the local log; the
	// OnOrgUnpaused fan-out (ResumeAfterOrgUnpause) clears it and wakes the
	// loop so archival resumes without waiting out the backoff.
	orgPaused atomic.Bool
	// wake cuts a backoff wait short (buffered 1; never blocks a sender).
	wake chan struct{}
}

// OrgPaused reports whether archival is currently held by an org pause.
func (a *AuditArchiver) OrgPaused() bool { return a.orgPaused.Load() }

// ResumeAfterOrgUnpause is the OnOrgUnpaused hook: clears the org-pause
// hold and wakes the loop out of its backoff.
func (a *AuditArchiver) ResumeAfterOrgUnpause() {
	if a == nil || !a.orgPaused.CompareAndSwap(true, false) {
		return
	}
	logging.L().Info("fleet.audit_archive.org_unpaused_resume")
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// RejectedEvents is the cumulative count of events fleet refused per-event.
func (a *AuditArchiver) RejectedEvents() int64 {
	return a.rejectedEvents.Load()
}

// auditAppendResponse mirrors the fields of kenaz-fleet
// service/handlers_audit_append.go AuditAppendResponse the archiver reads.
type auditAppendResponse struct {
	Accepted       int                  `json:"accepted"`
	Rejected       int                  `json:"rejected"`
	RejectedEvents []auditRejectedEvent `json:"rejected_events,omitempty"`
}

// auditRejectedEvent mirrors fleet AuditRejectedEvent.
type auditRejectedEvent struct {
	Index  int    `json:"index"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

// auditRetryAfterError is a 429 carrying fleet's Retry-After.
type auditRetryAfterError struct {
	After time.Duration
}

func (e *auditRetryAfterError) Error() string {
	return fmt.Sprintf("status 429 (retry after %s)", e.After)
}

// parseRetryAfter reads a Retry-After header (delta-seconds or HTTP date).
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// retryWait is how long the loop waits after err: a 429's Retry-After,
// capped at the NEXT backoff tier (review F10 — a quota reset hours away
// must not park the archiver past its normal retry ladder); otherwise the
// current backoff.
func retryWait(err error, backoff time.Duration) time.Duration {
	var ra *auditRetryAfterError
	if errors.As(err, &ra) {
		next := backoff * 2
		if next > auditBackoffMax {
			next = auditBackoffMax
		}
		if ra.After < next {
			return ra.After
		}
		return next
	}
	return backoff
}

// ErrAuditBatchTooLarge is fleet's 413 for an audit batch: permanent for that
// batch, so the archiver stops retrying it.
var ErrAuditBatchTooLarge = errors.New("fleet/audit_archive: fleet refused the batch as too large (413); archival paused")

// TooLarge reports whether archival is paused on a 413.
func (a *AuditArchiver) TooLarge() bool {
	return a.tooLarge.Load()
}

// Unsupported reports whether the loop stopped because the connected fleet
// server has no audit-append route (a plain 404 latched it).
func (a *AuditArchiver) Unsupported() bool {
	return a.unsupported.Load()
}

// ResetUnsupported clears the unsupported latch and, if the loop exited
// because of it, restarts the loop under the context the last Start was
// given. Wired to the settings view's fleet session reset (sign-in /
// sign-out) so a fleet that ships the endpoint, or a fixed host, resumes
// archival without an app restart. No-op after Stop or before any Start.
func (a *AuditArchiver) ResetUnsupported() {
	a.tooLarge.Store(false) // a new session may carry a different server cap
	if !a.unsupported.CompareAndSwap(true, false) {
		return
	}
	a.lifeMu.Lock()
	parent, stopped := a.parentCtx, a.stopped
	a.lifeMu.Unlock()
	if stopped || parent == nil || parent.Err() != nil {
		return
	}
	logging.L().Info("fleet.audit_archive.restart_after_unsupported_reset")
	a.Start(parent)
}

// NewAuditArchiver constructs an Archiver from cfg.
func NewAuditArchiver(cfg AuditArchiverConfig) *AuditArchiver {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = auditBatchSize
	}
	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = auditBatchInterval
	}
	return &AuditArchiver{cfg: cfg, wake: make(chan struct{}, 1)}
}

// Start launches the archive background loop. Idempotent: second Start
// returns immediately. The loop runs until Stop is called or the context
// is cancelled.
func (a *AuditArchiver) Start(ctx context.Context) {
	if !a.running.CompareAndSwap(false, true) {
		return
	}
	if a.cfg.Poster == nil && (a.cfg.Client == nil || a.cfg.Client.isNop) {
		slog.Info("fleet/audit_archive: no fleet client; archive loop not started")
		a.running.Store(false)
		return
	}

	// Load persisted cursor.
	cur, err := readAuditCursor(a.cfg.DataDir)
	if err != nil {
		slog.Warn("fleet/audit_archive: cursor read failed; starting from zero",
			"err", err)
	}
	a.mu.Lock()
	a.cursor = cur
	a.mu.Unlock()

	a.lifeMu.Lock()
	if a.stopped {
		a.lifeMu.Unlock()
		a.running.Store(false)
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.cancel = cancel
	a.done = done
	a.parentCtx = ctx
	a.lifeMu.Unlock()

	go func() {
		defer close(done)
		defer a.running.Store(false)
		a.loop(loopCtx)
	}()
}

// Stop signals the loop to stop and waits for it to exit. A stopped
// archiver is never restarted by ResetUnsupported.
func (a *AuditArchiver) Stop() {
	a.lifeMu.Lock()
	a.stopped = true
	cancel, done := a.cancel, a.done
	a.lifeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// IsRunning reports whether the archive background loop is currently
// running. A constructed-but-not-started archiver (or one that has
// stopped) returns false. Used by the compliance status view so a
// constructed-but-stopped archiver does not report running=true.
func (a *AuditArchiver) IsRunning() bool {
	return a.running.Load()
}

// ChainBreakDetected reports whether a hash-chain break was detected
// and the archiver has halted.
func (a *AuditArchiver) ChainBreakDetected() bool {
	return a.chainErr.Load()
}

// LastArchivedAt returns the time of the last successful ACK, or zero.
func (a *AuditArchiver) LastArchivedAt() time.Time {
	ns := a.lastArchivedAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// PendingCount returns the approximate number of locally-unarchived events.
func (a *AuditArchiver) PendingCount() int64 {
	return a.pendingCount.Load()
}

// ArchiveNow triggers an immediate archive flush (triggered by the
// "Archive now" button in the Compliance panel). Non-blocking; the
// normal loop handles the actual POST.
func (a *AuditArchiver) ArchiveNow(ctx context.Context) error {
	if a.chainErr.Load() {
		return errors.New("fleet/audit_archive: archive halted due to chain-break; operator action required")
	}
	if a.isUnsupported() {
		return &UnsupportedEndpointError{Feature: FeatureAuditAppend, Endpoint: auditArchiveEndpoint}
	}
	if a.tooLarge.Load() {
		return ErrAuditBatchTooLarge
	}
	if !a.running.Load() {
		return errors.New("fleet/audit_archive: archiver not running")
	}
	// Signal the loop by draining the interval timer in the goroutine.
	// Since we can't reach the timer from here, we just flush once directly.
	return a.flushOnce(ctx)
}

// CurrentCursor returns the current cursor value (the last ACK'd event_id).
func (a *AuditArchiver) CurrentCursor() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cursor
}

// SkipToID manually advances the cursor to id, emitting
// KindFleetAuditChainSkipped. This is the operator recovery action
// after a chain-break.
func (a *AuditArchiver) SkipToID(ctx context.Context, toID string) error {
	a.mu.Lock()
	fromID := a.cursor
	a.cursor = toID
	a.mu.Unlock()

	if err := saveAuditCursor(a.cfg.DataDir, toID); err != nil {
		return fmt.Errorf("fleet/audit_archive: SkipToID persist: %w", err)
	}
	a.chainErr.Store(false) // clear the hard-stop

	_ = emitAudit(ctx, a.cfg.Emitter, contextaudit.KindFleetAuditChainSkipped,
		contextaudit.FleetAuditChainSkippedPayload{
			FromID: fromID,
			ToID:   toID,
		})
	slog.Info("fleet/audit_archive: cursor skipped",
		"from", fromID, "to", toID)
	return nil
}

// loop is the main background goroutine.
func (a *AuditArchiver) loop(ctx context.Context) {
	backoff := auditBackoffBase
	ticker := time.NewTicker(a.cfg.BatchInterval)
	defer ticker.Stop()

	for {
		// Capability gate check.
		if a.cfg.CapCheck != nil && !a.cfg.CapCheck() {
			// Not enabled in this tier; idle, poll again after a longer wait.
			select {
			case <-ctx.Done():
				return
			case <-time.After(60 * time.Second):
				continue
			}
		}

		// The server has no audit-append route: stop for the archiver's
		// lifetime — no posts, no backoff churn, no WARNs.
		if a.unsupported.Load() {
			return
		}

		// Hard-stop on chain-break, or on a permanent 413.
		if a.chainErr.Load() || a.tooLarge.Load() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(60 * time.Second):
				// Re-check; operator may have called SkipToID.
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if err := a.flushOnce(ctx); err != nil {
			if errors.Is(err, ErrEndpointUnsupported) {
				return // latched: no route, nothing to retry
			}
			if IsOrgPaused(err) {
				// A staff pause hold: transient, back off on the normal
				// tiers, log the transition once (not every cycle).
				if a.orgPaused.CompareAndSwap(false, true) {
					logging.L().Info("fleet.audit_archive.org_paused",
						"reason", ReasonOrgPaused, "paused_category", OrgPausedCategoryOf(err))
				}
			} else {
				slog.Warn("fleet/audit_archive: flush error", "err", err)
			}
			// Exponential backoff (a 429 waits its Retry-After, capped).
			// An org unpause wakes the loop early.
			select {
			case <-ctx.Done():
				return
			case <-a.wake:
				backoff = auditBackoffBase
				continue
			case <-time.After(retryWait(err, backoff)):
			}
			if backoff < auditBackoffMax {
				backoff *= 2
				if backoff > auditBackoffMax {
					backoff = auditBackoffMax
				}
			}
		} else {
			backoff = auditBackoffBase
			a.orgPaused.Store(false)
		}
	}
}

// flushOnce reads one batch, verifies the chain, signs, POSTs, and on
// ACK advances the cursor. Returns nil when there is nothing to flush.
func (a *AuditArchiver) flushOnce(ctx context.Context) error {
	a.mu.RLock()
	cursor := a.cursor
	a.mu.RUnlock()

	events, err := a.cfg.Tail.Since(ctx, cursor, a.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("fleet/audit_archive: tail.Since: %w", err)
	}
	if len(events) == 0 {
		a.updatePending(ctx, cursor)
		return nil
	}

	// Update pending count.
	a.updatePending(ctx, cursor)

	// Hash-chain verification before send.
	if a.cfg.Verifier != nil {
		ok, brokenAt := a.cfg.Verifier.VerifyBatch(events)
		if !ok {
			a.chainErr.Store(true)
			_ = emitAudit(ctx, a.cfg.Emitter, contextaudit.KindFleetAuditChainBreak,
				contextaudit.FleetAuditChainBreakPayload{
					BrokenAtID:   brokenAt,
					BatchStartID: events[0].ID,
					ErrorClass:   "payload_hash_mismatch",
				})
			slog.Error("fleet/audit_archive: chain-break detected; archival halted",
				"broken_at", brokenAt)
			return fmt.Errorf("fleet/audit_archive: chain-break at %s", brokenAt)
		}
	}

	// Trim to payload size limit.
	events = trimToBudget(events)

	fromID := events[0].ID
	toID := events[len(events)-1].ID

	body, err := buildAuditBatch(events, a.cfg.Signer)
	if err != nil {
		return fmt.Errorf("fleet/audit_archive: build batch: %w", err)
	}

	if err := a.post(ctx, body); err != nil {
		return fmt.Errorf("fleet/audit_archive: POST: %w", err)
	}

	// ACK received — advance cursor.
	a.mu.Lock()
	a.cursor = toID
	a.mu.Unlock()

	if err := saveAuditCursor(a.cfg.DataDir, toID); err != nil {
		slog.Warn("fleet/audit_archive: cursor save failed", "err", err)
		// Non-fatal; cursor will be re-applied on next run.
	}

	a.lastArchivedAt.Store(time.Now().UnixNano())

	// Advance the predecessor hash in the BatchChainVerifier.
	if bv, ok := a.cfg.Verifier.(*BatchChainVerifier); ok {
		bv.AdvancePredecessor(events)
	}

	_ = emitAudit(ctx, a.cfg.Emitter, contextaudit.KindFleetAuditArchived,
		contextaudit.FleetAuditArchivedPayload{
			BatchSize: len(events),
			FromID:    fromID,
			ToID:      toID,
		})

	slog.Info("fleet/audit_archive: batch archived",
		"count", len(events), "from", fromID, "to", toID)
	return nil
}

// updatePending queries the HighWater and updates the pendingCount field.
// Non-blocking best-effort; errors are swallowed.
func (a *AuditArchiver) updatePending(ctx context.Context, cursor string) {
	hw, err := a.cfg.Tail.HighWater(ctx)
	if err != nil || hw == "" || hw <= cursor {
		a.pendingCount.Store(0)
		return
	}
	// Estimate pending as the number of events Since cursor. Cap at one
	// page to avoid a full scan just for the status readout.
	events, err := a.cfg.Tail.Since(ctx, cursor, 10*auditBatchSize)
	if err != nil {
		return
	}
	a.pendingCount.Store(int64(len(events)))
}

// poster returns the AuditHTTPPoster to use for outbound POSTs.
// Prefers the explicitly-injected Poster (tests), falls back to Client.
func (a *AuditArchiver) poster() AuditHTTPPoster {
	if a.cfg.Poster != nil {
		return a.cfg.Poster
	}
	return a.cfg.Client
}

// post signs and POSTs the batch JSON body to the fleet endpoint.
// isUnsupported reports the audit_append latch: the archiver's own flag OR
// the Client's resettable feature latch (WP04 — so a sign-in reset that
// clears the client latch is honoured by a new archiver run too).
func (a *AuditArchiver) isUnsupported() bool {
	if a.unsupported.Load() {
		return true
	}
	return a.cfg.Client != nil && a.cfg.Client.endpointUnsupported(FeatureAuditAppend) != nil
}

func (a *AuditArchiver) post(ctx context.Context, body []byte) error {
	if a.isUnsupported() {
		return &UnsupportedEndpointError{Feature: FeatureAuditAppend, Endpoint: auditArchiveEndpoint}
	}
	resp, err := a.poster().Post(ctx, auditArchiveEndpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		peek, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if isPlainNotFound(resp.StatusCode, resp.Header.Get("Content-Type"), peek) {
			if a.unsupported.CompareAndSwap(false, true) && a.cfg.Client == nil {
				logging.L().Info("fleet.endpoint.unsupported",
					"feature", FeatureAuditAppend,
					"endpoint", auditArchiveEndpoint,
					"action", "stop_retrying_keep_local")
			}
			if a.cfg.Client != nil {
				// Latch on the Client too (logs once there), so every
				// audit_append caller shares one resettable latch.
				return a.cfg.Client.markEndpointUnsupported(FeatureAuditAppend, auditArchiveEndpoint)
			}
			return &UnsupportedEndpointError{Feature: FeatureAuditAppend, Endpoint: auditArchiveEndpoint}
		}
	}
	if resp.StatusCode == http.StatusForbidden {
		// Client.do already converts org_paused to an error; an injected
		// Poster (tests, alternative transports) may still hand back the
		// raw 403, so classify it here too.
		peek, _ := io.ReadAll(io.LimitReader(resp.Body, orgPausedPeekLimit))
		if pe := ParseOrgPaused(resp.StatusCode, peek); pe != nil {
			if a.cfg.Client != nil {
				a.cfg.Client.observeOrgPaused(pe.PausedCategory)
			}
			return pe
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		if a.tooLarge.CompareAndSwap(false, true) {
			logging.L().Warn("fleet.audit_archive.batch_too_large",
				"endpoint", auditArchiveEndpoint, "action", "pause_archival_keep_local")
		}
		return ErrAuditBatchTooLarge
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return &auditRetryAfterError{After: d}
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	// 200 / 201 / 204 are all success (fleet contract 2026-10-06).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	// Content problems come back as 200 + a per-event report. The batch is
	// accepted (the cursor advances — retrying cannot change the answer),
	// but each refused event is logged (id + reason, never the payload) and
	// counted (review F10).
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var ar auditAppendResponse
	if len(raw) > 0 && json.Unmarshal(raw, &ar) == nil && (ar.Rejected > 0 || len(ar.RejectedEvents) > 0) {
		n := ar.Rejected
		if len(ar.RejectedEvents) > n {
			n = len(ar.RejectedEvents)
		}
		a.rejectedEvents.Add(int64(n))
		for _, re := range ar.RejectedEvents {
			logging.L().Warn("fleet.audit_archive.event_rejected",
				"event_id", re.ID, "index", re.Index, "reason", re.Reason)
		}
		if len(ar.RejectedEvents) == 0 {
			logging.L().Warn("fleet.audit_archive.events_rejected", "count", n)
		}
	}
	return nil
}

// ── batch construction ────────────────────────────────────────────────────────

// auditBatchPayload is the JSON body POSTed to /api/v1/audit/append.
type auditBatchPayload struct {
	DevicePubkeyFingerprint string           `json:"device_pubkey_fingerprint"`
	Signature               string           `json:"signature"`
	Events                  []auditEventWire `json:"events"`
}

// auditEventWire is the wire shape for a single audit event in the batch.
type auditEventWire struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	EmittedAtNS int64  `json:"emitted_at_ns"`
	Payload     []byte `json:"payload"`
	PayloadHash string `json:"payload_hash"`
	PrevHash    string `json:"prev_hash"`
	SessionID   string `json:"session_id,omitempty"`
}

// buildAuditBatch serialises events into the wire body, signs the canonical
// serialisation, and returns the JSON-encoded batch.
func buildAuditBatch(events []contextaudit.TailEvent, signer Signer) ([]byte, error) {
	wires := make([]auditEventWire, len(events))
	for i, e := range events {
		wires[i] = auditEventWire{
			ID:          e.ID,
			Kind:        e.Kind,
			EmittedAtNS: e.EmittedAt.UnixNano(),
			Payload:     e.Payload,
			PayloadHash: fmt.Sprintf("%x", e.PayloadHash),
			PrevHash:    fmt.Sprintf("%x", e.PrevHash),
			SessionID:   e.SessionID,
		}
	}

	// Canonical payload for signing: JSON-encode the events only.
	canonical, err := json.Marshal(wires)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical: %w", err)
	}

	batch := auditBatchPayload{Events: wires}
	if signer != nil {
		sig, fp, err := signer.Sign(canonical)
		if err != nil {
			return nil, fmt.Errorf("sign: %w", err)
		}
		batch.Signature = sig
		batch.DevicePubkeyFingerprint = fp
	}

	return json.Marshal(batch)
}

// trimToBudget trims the event slice so the combined payload bytes stay
// under auditPayloadMax. The first event is always included even if
// its payload alone exceeds the budget.
func trimToBudget(events []contextaudit.TailEvent) []contextaudit.TailEvent {
	var total int
	for i, e := range events {
		total += len(e.Payload)
		if i > 0 && total > auditPayloadMax {
			return events[:i]
		}
	}
	return events
}

// emitAudit fires an audit event; ignores nil emitters and errors so the
// archival path never fails due to audit-pipeline issues.
func emitAudit(ctx context.Context, emitter contextaudit.Emitter, kind contextaudit.Kind, payload any) error {
	if emitter == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return emitter.Emit(ctx, contextaudit.Event{Kind: kind, TS: time.Now(), Payload: raw})
}
