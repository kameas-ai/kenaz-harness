// The chassis-side implementation of core/tools/fs.BlockedRequestSink
// (model-scheduled-jobs-01PMSJ01 WP06, FR-004). Lives here (not in
// core/policy/blockedrequests) because it needs BOTH the durable store
// AND the audit ring buffer — core/tools/fs must not import either
// core/rpc or the audit ring, so the concrete implementation goes on the
// chassis side of the seam, the same split LiveChatRunDispatcher already
// uses for core/scheduler.ChatRunDispatcher.
package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// blockedRequestSink implements corefs.BlockedRequestSink by writing a
// durable row (via blockedrequests.Store) AND emitting an audit record.
// Both happen on every call — spec.md §5.5 requires the row (a lifecycle
// a surfacing UI can query) and the audit trail (docs/unwired-ledger.md:
// "memory-write, model-select, workflow and scheduled-chat denials are
// recorded into stores nothing reads" — the Cedar decision store is
// explicitly NOT a substitute for either).
type blockedRequestSink struct {
	store blockedrequests.Store
	audit contextaudit.Emitter
	now   func() time.Time
	// notify publishes the refreshed pending list (model-scheduled-jobs-
	// 01PMSJ01 WP07) after a successful row write, so a pending-
	// permissions panel already open updates live instead of only ever
	// seeing new denials on the next app launch's boot query. nil is a
	// no-op — RecordBlocked's row write still succeeds either way.
	notify func(ctx context.Context)
}

// newBlockedRequestSink constructs a blockedRequestSink. store, audit and
// notify are all nil-tolerant at the call site (a nil store skips the row
// write; a nil audit emitter is itself nil-tolerant via MustEmit's
// underlying Emit, which core/rpc/api.go's acpAuditBridge already
// handles for a nil a.auditImpl) — mirrors LiveChatRunDispatcher's own
// nil-tolerant field docs.
func newBlockedRequestSink(store blockedrequests.Store, audit contextaudit.Emitter, notify func(ctx context.Context)) *blockedRequestSink {
	return &blockedRequestSink{store: store, audit: audit, now: time.Now, notify: notify}
}

// RecordBlocked implements corefs.BlockedRequestSink.
func (s *blockedRequestSink) RecordBlocked(ctx context.Context, req corefs.BlockedRequest) error {
	now := s.now().UTC()

	// Audit trail first — best-effort per MustEmit's own contract, and
	// deliberately not gated on the row write succeeding: an audit
	// record with no matching row is still useful evidence; the reverse
	// (a row with no audit trail) is the shape this WP exists to end.
	if s.audit != nil {
		contextaudit.MustEmit(ctx, s.audit, contextaudit.KindBlockedPermissionRequest,
			contextaudit.BlockedPermissionRequestPayload{
				Origin:    req.Origin,
				OriginID:  req.OriginID,
				SessionID: req.SessionID,
				Family:    req.Family,
				Action:    req.Action,
				Resource:  req.Resource,
				Reason:    req.Reason,
			}, now)
	}

	if s.store == nil {
		return nil
	}
	if err := s.store.Create(ctx, blockedrequests.Record{
		ID:        newBlockedRequestID(),
		Origin:    req.Origin,
		OriginID:  req.OriginID,
		SessionID: req.SessionID,
		Family:    req.Family,
		Action:    req.Action,
		Resource:  req.Resource,
		Reason:    req.Reason,
		Status:    blockedrequests.StatusPending,
		CreatedAt: now,
	}); err != nil {
		return err
	}
	if s.notify != nil {
		s.notify(ctx)
	}
	return nil
}

func newBlockedRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ corefs.BlockedRequestSink = (*blockedRequestSink)(nil)

// publishPendingBlockedRequests re-reads the pending set through
// a.blockedRequestsAPI and publishes it on TopicBlockedPermissionRequestPending
// (model-scheduled-jobs-01PMSJ01 WP07). Called live, every time
// blockedRequestSink records a new denial (the notify closure in New()).
// A nil blockedRequestsAPI or broker is a silent no-op: the underlying
// row write already succeeded either way, this only affects whether an
// already-open panel refreshes without a manual reload.
//
// NOT called unconditionally at boot — see publishPendingBlockedRequestsAtBoot,
// which only emits when the pending set is non-empty. AC-013 (WP12,
// FR-008) requires a zero-schedule, zero-pending-request build to start
// "no new broker emissions"; this function always publishes (even an
// empty slice), which is correct for the live-refresh case (the caller
// just wrote a row, so the set is never empty) but would violate FR-008
// if also used verbatim for the boot path.
func (a *API) publishPendingBlockedRequests(ctx context.Context) {
	if a.blockedRequestsAPI == nil || a.broker == nil {
		return
	}
	pending, err := a.blockedRequestsAPI.ListPending(ctx)
	if err != nil {
		logging.L().Warn("rpc.blocked_requests.list_pending_failed", "err", err.Error())
		return
	}
	a.broker.Publish(TopicBlockedPermissionRequestPending, pending)
}

// publishPendingBlockedRequestsAtBoot is SetContext's boot-time call
// (owner decision 2: "surfaced to the user next time they open the
// app"). Unlike publishPendingBlockedRequests, it is silent when there
// is nothing pending — an install with zero blocked_permission_requests
// rows (which includes every install with zero scheduled_chat_runs rows,
// since that is currently the only producer) must be byte-identical at
// boot per FR-008/AC-013, and "publishes an empty slice on every launch
// forever" is a permanent, avoidable broker emission a schedule-less user
// would carry for the lifetime of the install.
func (a *API) publishPendingBlockedRequestsAtBoot(ctx context.Context) {
	if a.blockedRequestsAPI == nil || a.broker == nil {
		return
	}
	pending, err := a.blockedRequestsAPI.ListPending(ctx)
	if err != nil {
		logging.L().Warn("rpc.blocked_requests.list_pending_failed", "err", err.Error())
		return
	}
	if len(pending) == 0 {
		return
	}
	a.broker.Publish(TopicBlockedPermissionRequestPending, pending)
}
