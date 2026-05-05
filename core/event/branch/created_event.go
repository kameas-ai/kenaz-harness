// Package branch implements the branch primitive (FR-010): given a
// parent session and an event id E, compute a Replay Snapshot up to E
// and seed a new child session with a single
// event-log.session.branched event referencing
// (parent_session_id, parent_event_id).
package branch

import (
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
	"github.com/sigil-tech/kaneaz-harness/core/event/kind"
)

// KindBranchCreated is the kind.Kind value registered in the kind
// registry for branch-creation audit events
// (branching-ux-polish-01KQ8TD7 WP01).
const KindBranchCreated kind.Kind = "branch.created"

func init() {
	// Register at package-init time so the kind is available regardless
	// of import order. Idempotent — safe to call from multiple init
	// functions.
	if err := kind.Register(KindBranchCreated); err != nil {
		// This should never fail for a well-formed literal constant.
		panic("branch: kind.Register(KindBranchCreated): " + err.Error())
	}
}

// BranchCreatedEvent is the event shape for KindBranchCreated. It
// mirrors audit.BranchCreatedPayload but also carries the timestamp for
// callers that prefer a self-contained value rather than unpacking an
// audit.Event.
type BranchCreatedEvent struct {
	ParentSessionID string
	ParentMessageID string
	BranchSessionID string
	// CreationPath is "explicit" | "edit_resend".
	CreationPath string
	Timestamp    time.Time
}

// NewCreatedEvent constructs a BranchCreatedEvent and its corresponding
// audit.Event in one call. Callers pass the returned audit.Event to an
// audit.Emitter; they use the BranchCreatedEvent for any local
// in-process signalling they need.
func NewCreatedEvent(
	parentSessionID string,
	parentMessageID string,
	branchSessionID string,
	creationPath string,
	now time.Time,
) (BranchCreatedEvent, audit.Event, error) {
	ev := BranchCreatedEvent{
		ParentSessionID: parentSessionID,
		ParentMessageID: parentMessageID,
		BranchSessionID: branchSessionID,
		CreationPath:    creationPath,
		Timestamp:       now,
	}
	auditEvent, err := audit.Marshal(audit.KindBranchCreated, audit.BranchCreatedPayload{
		ParentSessionID: parentSessionID,
		ParentMessageID: parentMessageID,
		BranchSessionID: branchSessionID,
		CreationPath:    creationPath,
	}, now)
	if err != nil {
		return BranchCreatedEvent{}, audit.Event{}, err
	}
	return ev, auditEvent, nil
}
