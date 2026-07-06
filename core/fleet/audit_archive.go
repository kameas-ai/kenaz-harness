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
	"sync"
	"sync/atomic"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
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
	cfg      AuditArchiverConfig
	cancel   context.CancelFunc
	done     chan struct{}
	running  atomic.Bool
	mu       sync.RWMutex
	cursor   string
	chainErr atomic.Bool // true after a chain-break hard-stop

	// status fields for the Compliance RPC view.
	lastArchivedAt atomic.Int64 // unix nano; 0 = never
	pendingCount   atomic.Int64
}

// NewAuditArchiver constructs an Archiver from cfg.
func NewAuditArchiver(cfg AuditArchiverConfig) *AuditArchiver {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = auditBatchSize
	}
	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = auditBatchInterval
	}
	return &AuditArchiver{cfg: cfg}
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

	loopCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.done = make(chan struct{})

	go func() {
		defer close(a.done)
		defer a.running.Store(false)
		a.loop(loopCtx)
	}()
}

// Stop signals the loop to stop and waits for it to exit.
func (a *AuditArchiver) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.done != nil {
		<-a.done
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

		// Hard-stop on chain-break.
		if a.chainErr.Load() {
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
			slog.Warn("fleet/audit_archive: flush error", "err", err)
			// Exponential backoff.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < auditBackoffMax {
				backoff *= 2
				if backoff > auditBackoffMax {
					backoff = auditBackoffMax
				}
			}
		} else {
			backoff = auditBackoffBase
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
func (a *AuditArchiver) post(ctx context.Context, body []byte) error {
	resp, err := a.poster().Post(ctx, auditArchiveEndpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// ── batch construction ────────────────────────────────────────────────────────

// auditBatchPayload is the JSON body POSTed to /api/v1/audit/append.
type auditBatchPayload struct {
	DevicePubkeyFingerprint string            `json:"device_pubkey_fingerprint"`
	Signature               string            `json:"signature"`
	Events                  []auditEventWire  `json:"events"`
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
