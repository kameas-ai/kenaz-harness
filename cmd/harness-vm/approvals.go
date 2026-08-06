// approvals.go — the :7881 approval surface (spec 074 Phase 4, tasks 4.C1 +
// 4.C2; normative sources: .specify/decisions/ADR-approval-broker.md and
// specs/074-kenaz-ios-remote/contracts/approval-events.md).
//
// WHAT THIS IS NOT: a second approval engine. `cedar.Registry` is the harness's
// existing gate — it already owns the pending map, the 5-minute fail-closed
// timer, the crypto-random approval id, and the first-decision-wins
// serialization (`pendingEntry.resolved sync.Once`). This file adds ONE more
// listener to that gate and ONE more way to resolve it. There is no second
// gate, no second timer, and no second serializer; if you find yourself adding
// any of the three, the design has gone wrong.
//
// Three additive kinds, all inert unless negotiated:
//
//	S→C: task.approval_requested   (0..N per task)
//	C→S: task.approval_decision    (0..N)
//	S→C: task.approval_resolved    (exactly one per approval_id)
//
// NEGOTIATION IS THE MECHANISM, NOT THE VERSION NUMBER. The handshake gains one
// optional key each way; both sides omit it when unimplemented, so absent
// negotiation the wire is byte-for-byte what it was before this file existed.
// The harness MUST NOT emit an approval kind unless `approval` was granted in
// auth.ok — the fail-safe direction is silence, not speculation. Not emitting
// is safe because the gate still resolves at the harness's own served (:7880)
// permission modal, which is unconditionally present in every workbench:
// :7881 externalization ADDS a decision surface, it never removes one.
//
// PRIVACY. `summary` is CONTENT (a path, a command line, a provider) and rides
// this wire deliberately — a phone that shows `tool::filesystem::write_file`
// with no path cannot make a decision. `action_kind` is its structural
// counterpart and is what the HOST writes to its ledger, so it carries no path,
// argument, URL, or credential name (see cedar.PromptSurface.ActionKind).
// Nothing in the other direction carries a device identity: `source` is a
// CLASS (`host` / `remote`), because a sandboxed agent runtime has no business
// learning the operator's device inventory.
package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// capabilityApproval is the negotiated capability token for the three
// approval kinds. `capabilities` is a set of OPAQUE strings: unknown entries
// are ignored on both sides, so the same key carries future negotiations
// without another handshake change.
const capabilityApproval = "approval"

// supportedCapabilities is what this build can grant. The granted set sent
// back in auth.ok is the intersection of this with what the client asked for
// AND what this process can actually honour — see negotiateCapabilities.
var supportedCapabilities = []string{capabilityApproval}

// approvalTimestampFormat is the RFC3339 rendering used for requested_at /
// deadline_at / resolved_at. Millisecond precision, UTC — matches the served
// surface's flatPermissionRequest so a host correlating the two sees identical
// strings for the same approval.
const approvalTimestampFormat = "2006-01-02T15:04:05.000Z07:00"

// approvalRegistry is the slice of *cedar.Registry this file needs. Declared
// as an interface so the wiring is explicit about using exactly three methods
// of the existing gate — attach a request listener, attach a resolution
// listener, and inject a decision — and nothing that could constitute owning
// approval state.
type approvalRegistry interface {
	AddDispatcher(d cedar.PromptDispatcher) (remove func())
	AddResolutionObserver(o cedar.ResolutionObserver) (remove func())
	ResolveFrom(requestID string, decision cedar.PromptDecision, source cedar.ResolutionSource) error
}

// negotiateCapabilities computes the GRANTED capability set from the client's
// requested list and what this process can honour.
//
// requested is the raw `capabilities` value off the auth frame — any JSON
// shape, since it comes from an untyped map. A missing key, a non-array, or an
// array of non-strings all yield an empty grant, which the caller renders as
// an OMITTED auth.ok key: an old client that never sends the field gets a
// byte-identical auth.ok.
//
// available gates each capability on whether this process can actually deliver
// it. Granting `approval` with no gate to broker would be a lie the host could
// not detect, and the host's "approvals not brokered" UI state exists precisely
// so the truthful answer is renderable.
func negotiateCapabilities(requested any, available map[string]bool) []string {
	list, ok := requested.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	asked := make(map[string]bool, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok && s != "" {
			asked[s] = true
		}
	}
	// Iterate supportedCapabilities (not the client's list) so the granted
	// set is deterministically ordered and can never echo an unknown token.
	var granted []string
	for _, c := range supportedCapabilities {
		if asked[c] && available[c] {
			granted = append(granted, c)
		}
	}
	return granted
}

// hasCapability reports whether name is in the granted set.
func hasCapability(granted []string, name string) bool {
	for _, c := range granted {
		if c == name {
			return true
		}
	}
	return false
}

// approvalBridge is the per-connection adapter between cedar.Registry and the
// :7881 wire. One bridge per connection; it registers itself against the
// process's single registry for the life of that connection.
//
// TASK CORRELATION. cedar keys approvals by PromptSurface.SessionID; :7881
// speaks task_id, and no correlation exists in the gate. The binding is
// unambiguous only because cmd/harness-vm enforces one task per connection via
// its busy flag, so at most one task is in flight per bridge and every approval
// raised while that task runs belongs to it. IF THE ONE-TASK-PER-CONNECTION
// LIMIT IS EVER LIFTED, THIS BINDING MUST BE REVISITED IN THE SAME CHANGE — it
// is load-bearing, not incidental.
//
// A corollary the host must know about: a bridge forwards approvals only while
// its connection has a task in flight. An approval raised in this process with
// no task bound cannot be attributed to a task_id, so it is not forwarded and
// resolves at :7880 as it always did. Speculating a task_id would be worse than
// silence — it would attribute an action to the wrong run.
type approvalBridge struct {
	w   *connWriter
	log *slog.Logger

	mu sync.Mutex
	// taskID is the in-flight task, or "" when the connection is idle.
	taskID string
	// pending maps approval_id → the task_id it was dispatched under, so a
	// resolution arriving after the task unbound still names the right run.
	pending map[string]string
}

// newApprovalBridge builds a bridge writing to w. Timestamps on this wire are
// the REGISTRY's clock readings (carried on PendingRequest and ResolvedEvent),
// never a second clock read here — two clocks would let requested_at and
// resolved_at disagree about an interval the host reports as latency.
func newApprovalBridge(w *connWriter, log *slog.Logger) *approvalBridge {
	return &approvalBridge{w: w, log: log, pending: map[string]string{}}
}

// Dispatch implements cedar.PromptDispatcher: it is the SECOND fan-out target
// on the existing gate, running alongside whatever the served surface
// registered. topic is ignored — the family already rides the payload, and
// :7881 has no topic concept.
func (b *approvalBridge) Dispatch(_ context.Context, _ string, payload cedar.PendingRequest) {
	if b == nil {
		return
	}
	b.mu.Lock()
	taskID := b.taskID
	if taskID == "" {
		// No task in flight on this connection — see the correlation note on
		// approvalBridge. Silence, not a speculated task_id.
		b.mu.Unlock()
		return
	}
	b.pending[payload.RequestID] = taskID
	b.mu.Unlock()

	timeoutS := int(payload.DeadlineAt.Sub(payload.IssuedAt).Round(time.Second) / time.Second)
	if timeoutS < 0 {
		timeoutS = 0
	}
	if err := b.w.send(msg{
		"kind":        "task.approval_requested",
		"task_id":     taskID,
		"approval_id": payload.RequestID,
		"family":      string(payload.Family),
		"action_kind": payload.Surface.ActionKind(),
		"summary":     payload.Summary(),
		"dangerous":   payload.Project().Dangerous,
		"requested_at": payload.IssuedAt.UTC().
			Format(approvalTimestampFormat),
		// deadline_at is ABSOLUTE and is the only correct countdown source.
		// timeout_s rides alongside for display; relay latency, push delay,
		// and a phone that was asleep make a relative budget wrong by an
		// unknown amount at the point of rendering.
		"deadline_at": payload.DeadlineAt.UTC().Format(approvalTimestampFormat),
		"timeout_s":   timeoutS,
	}); err != nil {
		b.log.Warn("kenaz-harness-vm: write task.approval_requested failed", "err", err)
	}
}

// Resolved implements cedar.ResolutionObserver: exactly one emission per
// approval_id, in every interleaving, because the registry notifies from
// inside the pendingEntry.resolved sync.Once that already guards
// decision-vs-timeout-vs-cancel.
func (b *approvalBridge) Resolved(ev cedar.ResolvedEvent) {
	if b == nil {
		return
	}
	b.mu.Lock()
	taskID, known := b.pending[ev.Request.RequestID]
	delete(b.pending, ev.Request.RequestID)
	if !known {
		// Queue overflow is the legitimate case: cedar denies at the cap with
		// no dispatch at all, so we never recorded the id. Attribute it to the
		// in-flight task so the denial is legible ("denied — too many pending
		// approvals") rather than surfacing as an unexplained tool failure.
		taskID = b.taskID
	}
	b.mu.Unlock()
	if taskID == "" {
		return
	}

	if err := b.w.send(msg{
		"kind":        "task.approval_resolved",
		"task_id":     taskID,
		"approval_id": ev.Request.RequestID,
		"decision":    string(ev.Decision),
		"source":      string(ev.Source),
		"resolved_at": ev.ResolvedAt.UTC().Format(approvalTimestampFormat),
		"latency_ms":  ev.Latency.Milliseconds(),
	}); err != nil {
		b.log.Warn("kenaz-harness-vm: write task.approval_resolved failed", "err", err)
	}
}

// pendingCount reports how many dispatched approvals are unresolved on this
// bridge. Test helper for the leak assertion — the map must drain in every
// interleaving.
func (b *approvalBridge) pendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// handleApprovalDecision processes an inbound task.approval_decision.
//
// WIRE IDEMPOTENCY. A decision for an already-resolved or unknown approval_id
// is ACKED AND DROPPED: no task.error, no second task.approval_resolved, no
// state change. cedar's registry is at-most-once, not idempotent — Resolve
// deletes the pending entry under the mutex and a second call is
// indistinguishable from a timed-out one — so idempotency is implemented HERE,
// at the adapter, which is the only place it can be. That is what makes an
// at-least-once event stream safe to retry.
//
// A MALFORMED frame is a different thing from a late one and still earns a
// bad_request, matching every other kind on this wire.
func (b *approvalBridge) handleApprovalDecision(reg approvalRegistry, m msg) (reply msg, hasReply bool) {
	approvalID, _ := m["approval_id"].(string)
	if approvalID == "" {
		return msg{
			"kind":              "task.error",
			"code":              "bad_request",
			"message_truncated": "approval_id required",
		}, true
	}
	taskID, _ := m["task_id"].(string)

	rawDecision, _ := m["decision"].(string)
	decision := cedar.PromptDecision(rawDecision)
	switch decision {
	case cedar.DecisionAllowOnce, cedar.DecisionAllowAlways, cedar.DecisionDeny:
	default:
		return msg{
			"kind":              "task.error",
			"task_id":           taskID,
			"code":              "bad_request",
			"message_truncated": truncate("invalid decision: "+rawDecision, maxMessageLen),
		}, true
	}

	// `source` is a CLASS. Only host and remote are acceptable inbound —
	// guest is the served modal's own class and the timeout / cancelled /
	// overflow classes are registry-synthesised, so accepting either from the
	// wire would let a host forge a resolution provenance the ledger then
	// records as fact. Absent source defaults to host (the desktop panel).
	rawSource, _ := m["source"].(string)
	source := cedar.ResolutionSource(rawSource)
	switch source {
	case cedar.SourceHost, cedar.SourceRemote:
	case "":
		source = cedar.SourceHost
	default:
		return msg{
			"kind":              "task.error",
			"task_id":           taskID,
			"code":              "bad_request",
			"message_truncated": truncate("invalid source: "+rawSource, maxMessageLen),
		}, true
	}

	if reg == nil {
		return nil, false
	}
	if err := reg.ResolveFrom(approvalID, decision, source); err != nil {
		// Already resolved, or never existed. Both are ordinary traffic on an
		// at-least-once stream: the operator did a reasonable thing slightly
		// late, or the host retried. Drop it. Structural log only — the
		// approval id is opaque, the decision is a closed enum, and neither
		// carries operator content.
		b.log.Debug("kenaz-harness-vm: approval decision dropped",
			"approval_id", approvalID, "reason", err)
	}
	return nil, false
}
