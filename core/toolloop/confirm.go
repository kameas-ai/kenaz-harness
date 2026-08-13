// Package toolloop — confirm-each enforcement seam
// (confirm-each-enforcement-01PMAG05 WP01).
//
// The resolver in perms.go computes a three-valued verdict
// (auto_allow | confirm_each | deny) but the dispatch path historically
// acted on exactly two of the three: a `confirm_each` verdict dispatched
// without asking anyone. ConfirmBus is the narrow seam that makes the
// third value real.
//
// # Shape: a pause, not an await with a deadline
//
// Owner decision (spec §3.1, 2026-08-12): there is **no timeout**. An
// unanswered confirmation parks the run — like an AskNode pause — until
// the user answers, the batch is explicitly cancelled, or the run's
// context is cancelled. There is deliberately no clock in this file that
// converts absence into an outcome; elapsed time resolves to nothing.
// Do not add one.
//
// # Dependency posture
//
// ConfirmBus imports nothing beyond the standard library and the
// harness logger. The broker / Wails emitter is injected as a
// ConfirmPublisher func so the chassis owns the transport and this
// package stays testable without it.
// 01PMGX01 Phase 3 folds this and the AskBus onto one elicitation
// primitive — keep the surface minimal so absorption is a move, not a
// rewrite.
package toolloop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// TopicToolConfirmPending is the broker topic the chassis publishes a
// ConfirmRequest on when a tool call is parked awaiting user
// confirmation.
//
// # Frontend contract (WP02 builds against this)
//
// Topic: "tool:confirm-pending". Payload: one ConfirmRequest per parked
// call, JSON-encoded:
//
//	{
//	  "session_id":   "sess-abc",       // which session is parked
//	  "call_id":      "confirm-1a2b…",  // unique per parked call
//	  "batch_id":     "batch-9f8e…",    // shared by every call in one turn
//	  "server":       "filesystem",     // MCP server (bare name)
//	  "tool":         "write_file",     // bare tool name
//	  "args_summary": "2 arguments: content (string), path (string)",
//	  "reason":       "confirm each use" // resolver reason, may be absent
//	}
//
// N parallel calls from one turn produce N events sharing one batch_id
// so the frontend renders **one** batched modal with per-row
// approve/deny plus an approve-all (owner decision 3), not a modal
// storm. Rows resolve independently: an approved row dispatches the
// moment its answer arrives, a denied row returns a tool error, and the
// remaining rows stay parked.
//
// Redaction contract (mirrors core/rpc/views/elicit/api.go and
// cedar.ToolPromptSurface.ArgsRedacted): args_summary is a **structural**
// description only — argument count, argument names, and JSON kinds. It
// MUST NOT carry raw argument values, secret bytes, or env-derived data.
// SummarizeArgs is the canonical producer; do not hand-roll a summary
// that interpolates values.
//
// The reverse direction (user's answer) is ConfirmBus.Resolve /
// Cancel / CancelBatch, which WP02 wires to a Wails binding.
const TopicToolConfirmPending = "tool:confirm-pending"

// ConfirmRequest is one parked tool call awaiting a user decision. It is
// both the registry entry and the broker payload — see
// TopicToolConfirmPending for the wire contract.
type ConfirmRequest struct {
	// SessionID scopes the pending decision. Confirmations are keyed
	// per (SessionID, CallID); one session's parked call never blocks
	// another session (FR-002).
	SessionID string `json:"session_id"`

	// CallID uniquely identifies this parked call within the session.
	// The frontend round-trips it back in Resolve.
	CallID string `json:"call_id"`

	// BatchID groups every call parked from the same turn so the
	// frontend can render one batched modal (owner decision 3).
	BatchID string `json:"batch_id"`

	// Server is the bare MCP server name (no "__" suffix).
	Server string `json:"server"`

	// Tool is the bare tool name (no server prefix).
	Tool string `json:"tool"`

	// ArgsSummary is a redaction-safe structural description of the
	// call. NEVER raw argument values. See SummarizeArgs.
	ArgsSummary string `json:"args_summary"`

	// Reason is the permission resolver's reason string for the
	// confirm_each verdict, when it supplied one.
	Reason string `json:"reason,omitempty"`
}

// ConfirmDecision is the user's answer to one parked call.
//
// There is no third "timed out" state by design: absence of an answer is
// not an outcome (owner decision 1). Explicit dismissal / window-close
// arrives here as Approved=false (FR-003).
type ConfirmDecision struct {
	// Approved is true when the user allowed the call to dispatch.
	Approved bool `json:"approved"`

	// Reason is an optional human-readable explanation carried into the
	// tool error on denial (e.g. "dismissed", "user denied").
	Reason string `json:"reason,omitempty"`

	// RememberSession asks the dispatcher to record a per-tool,
	// per-session grant so the same (server, tool) does not prompt again
	// for the rest of this session (owner decision 2, WP03). The grant
	// lives in process memory only and is never written to disk.
	//
	// Carried on the decision rather than applied by the RPC view
	// because the dispatcher is the side that knows the (session,
	// server, tool) triple behind a call id — and it is also the side
	// that writes the audit record, so grant and audit stay together.
	//
	// Ignored when Approved is false: "deny, but remember to allow" is
	// not a thing a user can mean.
	RememberSession bool `json:"remember_session,omitempty"`

	// Persist asks the dispatcher to write a DURABLE allow rule for
	// (server, tool) — the visually-separated "always allow" control
	// (owner decision 2, WP03). Survives restarts and is revocable from
	// the Settings grants list. Ignored when Approved is false.
	Persist bool `json:"persist,omitempty"`
}

// ConfirmPublisher is the injected transport for TopicToolConfirmPending.
// Production binds it to the chassis broker's Emit; tests inject a
// recording fake. Nil is tolerated — the bus still parks and resolves,
// it just never announces itself (useful in unit tests).
type ConfirmPublisher func(req ConfirmRequest)

// ErrUnknownConfirmation is returned by Resolve / Cancel when the
// (session, call) pair has already been resolved or was never parked.
var ErrUnknownConfirmation = errors.New("toolloop: unknown or already-resolved confirmation")

// ErrDuplicateConfirmation is returned by Pending when a (session,
// call) pair is already parked. Call IDs are generated per dispatch, so
// this indicates a caller bug rather than a user-reachable condition.
var ErrDuplicateConfirmation = errors.New("toolloop: confirmation already pending for this call")

// confirmEntry is one parked call plus the channel its decision arrives
// on. The channel is buffered (1) so a resolver never blocks even if the
// awaiting goroutine has already gone away via context cancellation.
type confirmEntry struct {
	req ConfirmRequest
	ch  chan ConfirmDecision
}

// ConfirmBus is the per-(session, call-id) pending-decision registry.
//
// Safe for concurrent use: a turn's tool calls fan out across a
// worker pool, so Pending / Resolve / Cancel are all called from
// different goroutines. Every field access is under mu; the blocking
// wait in Pending happens *outside* the lock so one parked call never
// serialises another (FR-002).
type ConfirmBus struct {
	mu      sync.Mutex
	pending map[string]*confirmEntry
	publish ConfirmPublisher
}

// NewConfirmBus returns an empty bus. publish may be nil.
func NewConfirmBus(publish ConfirmPublisher) *ConfirmBus {
	return &ConfirmBus{
		pending: make(map[string]*confirmEntry),
		publish: publish,
	}
}

// confirmKey is the registry key. NUL separator: neither session IDs nor
// call IDs contain NUL.
func confirmKey(sessionID, callID string) string {
	return sessionID + "\x00" + callID
}

// Pending parks req and blocks until a decision arrives or ctx is
// cancelled. It is the register-and-wait half of the Pending / Resolve /
// Cancel trio.
//
// There is no deadline. The only exits are:
//   - Resolve / Cancel / CancelBatch supplies a ConfirmDecision;
//   - ctx is cancelled (run stopped, session torn down) → returns
//     ctx.Err() and the entry is unparked so it cannot leak.
//
// The publish callback fires after the entry is registered and after mu
// is released, so a synchronous publisher may resolve re-entrantly.
func (b *ConfirmBus) Pending(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error) {
	if b == nil {
		return ConfirmDecision{}, errors.New("toolloop: nil ConfirmBus")
	}
	if req.CallID == "" {
		return ConfirmDecision{}, errors.New("toolloop: ConfirmRequest.CallID is required")
	}
	key := confirmKey(req.SessionID, req.CallID)
	entry := &confirmEntry{req: req, ch: make(chan ConfirmDecision, 1)}

	b.mu.Lock()
	if b.pending == nil {
		b.pending = make(map[string]*confirmEntry)
	}
	if _, dup := b.pending[key]; dup {
		b.mu.Unlock()
		return ConfirmDecision{}, ErrDuplicateConfirmation
	}
	b.pending[key] = entry
	publish := b.publish
	b.mu.Unlock()

	if publish != nil {
		publish(req)
	} else {
		// A bus with no publisher parks the call and tells nobody. The
		// run then hangs until ctx is cancelled — fail-closed, which is
		// the right outcome, but silent, which is not: from the outside
		// it is indistinguishable from a slow model. Log it loudly at
		// park time so the misconfiguration is legible in the logs the
		// moment it bites, not after someone bisects a "hung turn".
		//
		// The behaviour is unchanged by this log: the call still parks
		// and still never dispatches without an answer. Callers that
		// legitimately have no prompt channel should not construct a bus
		// at all — kernelToolAdapter treats a nil bus as "headless" and
		// applies the configured headless policy (WP05).
		logging.L().Error("toolloop.confirm.parked_without_publisher",
			"session_id", req.SessionID,
			"call_id", req.CallID,
			"batch_id", req.BatchID,
			"server", req.Server,
			"tool", req.Tool,
			"detail", "ConfirmBus has no publisher: this call is parked and no prompt was announced; it will block until the run context is cancelled")
	}

	select {
	case d := <-entry.ch:
		return d, nil
	case <-ctx.Done():
		b.mu.Lock()
		// Only delete our own entry: a concurrent Resolve may have
		// already removed it and a later call may have reused the key.
		if cur, ok := b.pending[key]; ok && cur == entry {
			delete(b.pending, key)
		}
		b.mu.Unlock()
		return ConfirmDecision{}, ctx.Err()
	}
}

// Resolve delivers the user's decision for one parked call. Returns
// ErrUnknownConfirmation when nothing is parked under (sessionID,
// callID) — an already-answered row, or a stale frontend.
func (b *ConfirmBus) Resolve(sessionID, callID string, d ConfirmDecision) error {
	if b == nil {
		return ErrUnknownConfirmation
	}
	b.mu.Lock()
	key := confirmKey(sessionID, callID)
	entry, ok := b.pending[key]
	if ok {
		delete(b.pending, key)
	}
	b.mu.Unlock()

	if !ok {
		return ErrUnknownConfirmation
	}
	// Buffered send: the awaiting goroutine may already have left via
	// context cancellation, in which case the decision is discarded.
	select {
	case entry.ch <- d:
	default:
	}
	return nil
}

// Cancel resolves one parked call as a denial. This is the path for
// explicit dismissal (FR-003) — the user closed the row or the dialog.
// Elapsed time never calls this.
func (b *ConfirmBus) Cancel(sessionID, callID, reason string) error {
	if reason == "" {
		reason = "cancelled by user"
	}
	return b.Resolve(sessionID, callID, ConfirmDecision{Approved: false, Reason: reason})
}

// CancelBatch resolves every parked call sharing batchID as a denial and
// returns how many rows it cancelled. This is the window-close /
// dismiss-all path: dismissal is deny-all, never allow-all (FR-003).
func (b *ConfirmBus) CancelBatch(batchID, reason string) int {
	if reason == "" {
		reason = "cancelled by user"
	}
	return b.ResolveBatch(batchID, ConfirmDecision{Approved: false, Reason: reason})
}

// ResolveBatch delivers the same decision to every parked call sharing
// batchID and returns how many rows it resolved.
//
// It backs both halves of the batched modal (owner decision 3):
// approve-all sends Approved=true, dismiss / window-close goes through
// CancelBatch. Rows already answered individually are gone from the
// registry and are therefore untouched — an approve-all after two manual
// denies approves only the remaining three.
func (b *ConfirmBus) ResolveBatch(batchID string, d ConfirmDecision) int {
	if b == nil || batchID == "" {
		return 0
	}
	b.mu.Lock()
	var victims []*confirmEntry
	for key, entry := range b.pending {
		if entry.req.BatchID == batchID {
			victims = append(victims, entry)
			delete(b.pending, key)
		}
	}
	b.mu.Unlock()

	for _, entry := range victims {
		select {
		case entry.ch <- d:
		default:
		}
	}
	return len(victims)
}

// HasChannel reports whether a prompt channel is attached — i.e. whether
// parking a call would actually announce itself to anybody. A bus with
// no publisher can park but cannot ask, which is the headless condition
// the WP05 policy governs.
func (b *ConfirmBus) HasChannel() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.publish != nil
}

// List returns a snapshot of every parked call, sorted by (session,
// batch, call) so the output is deterministic. The frontend calls this
// on reconnect to re-render a modal that was open before the connection
// dropped — the pause survives a UI reload (the run is parked in-process,
// not in the dialog).
func (b *ConfirmBus) List() []ConfirmRequest {
	return b.list("")
}

// ListSession returns the parked calls for one session only.
func (b *ConfirmBus) ListSession(sessionID string) []ConfirmRequest {
	return b.list(sessionID)
}

func (b *ConfirmBus) list(sessionID string) []ConfirmRequest {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	out := make([]ConfirmRequest, 0, len(b.pending))
	for _, entry := range b.pending {
		if sessionID != "" && entry.req.SessionID != sessionID {
			continue
		}
		out = append(out, entry.req)
	}
	b.mu.Unlock()

	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		if out[i].BatchID != out[j].BatchID {
			return out[i].BatchID < out[j].BatchID
		}
		return out[i].CallID < out[j].CallID
	})
	return out
}

// PendingCount reports how many calls are currently parked. Test and
// telemetry helper; not part of the frontend contract.
func (b *ConfirmBus) PendingCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// ---- batch grouping ----

type confirmBatchCtxKey struct{}

// WithConfirmBatch stamps a batch ID onto ctx. Every tool call
// dispatched under the resulting context that parks for confirmation
// shares the ID, which is what lets the frontend render one modal per
// turn instead of one per call. Empty id is a no-op.
func WithConfirmBatch(ctx context.Context, batchID string) context.Context {
	if batchID == "" {
		return ctx
	}
	return context.WithValue(ctx, confirmBatchCtxKey{}, batchID)
}

// ConfirmBatchFromContext returns the batch ID stamped by
// WithConfirmBatch, or "" when the caller did not group its calls. A
// caller with no batch context is expected to fall back to a per-call
// batch (one row, one modal).
func ConfirmBatchFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(confirmBatchCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// ---- id generation ----

var confirmIDSeq uint64

// NewConfirmID returns a process-unique identifier suitable for a
// ConfirmRequest CallID or BatchID. Not a security boundary — uniqueness
// within the process lifetime is all that is required.
func NewConfirmID(prefix string) string {
	seq := atomic.AddUint64(&confirmIDSeq, 1)
	return fmt.Sprintf("%s-%x%x", prefix, uint64(time.Now().UnixNano()), seq)
}

// ---- redaction-safe args summary ----

// SummarizeArgs renders a structural, redaction-safe description of a
// tool call's arguments: how many there are, what they are named, and
// what JSON kind each one is. It never renders a value.
//
// Examples:
//
//	map[string]any{}                          → "no arguments"
//	{"path": "/etc/passwd"}                   → "1 argument: path (string)"
//	{"path": "…", "lines": 12, "raw": true}   → "3 arguments: lines (number), path (string), raw (boolean)"
//
// Keys are sorted so the summary is stable across map iteration order.
// This is the ArgsSummary producer referenced by the redaction contract
// on TopicToolConfirmPending — callers must not build their own.
func SummarizeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "no arguments"
	}
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s (%s)", n, jsonKind(args[n])))
	}
	noun := "arguments"
	if len(names) == 1 {
		noun = "argument"
	}
	return fmt.Sprintf("%d %s: %s", len(names), noun, strings.Join(parts, ", "))
}

// jsonKind names the JSON kind of v without revealing its contents.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}
