// Package modelswitch implements the "model.switched" event
// (laya-advisors-01LAYA001 WP08, design §5.1/§5.2): a user-initiated
// model move recorded as an audit + event-log kind, per
// core/event/branch's identical shape for branch.created. This is the
// missing behavioral-label source design §5.1's table names — before
// this WP, no capture point existed for "the user just changed which
// model serves this session."
package modelswitch

import (
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/event/kind"
)

// KindModelSwitched is the kind.Kind value registered in the event-log
// kind registry for model-switch audit events. Same string as
// audit.KindModelSwitched — this package registers it into the
// event-log's own open registry (core/event/kind), exactly as
// core/event/branch registers "branch.created" alongside
// audit.KindBranchCreated.
const KindModelSwitched kind.Kind = "model.switched"

func init() {
	if err := kind.Register(KindModelSwitched); err != nil {
		panic("modelswitch: kind.Register(KindModelSwitched): " + err.Error())
	}
}

// SwitchedEvent is the event shape for KindModelSwitched. Mirrors
// audit.ModelSwitchedPayload but also carries the timestamp for callers
// that prefer a self-contained value rather than unpacking an
// audit.Event.
type SwitchedEvent struct {
	SessionID     string
	FromProfileID string
	FromModelID   string
	ToProfileID   string
	ToModelID     string
	Timestamp     time.Time
}

// NewSwitchedEvent constructs a SwitchedEvent and its corresponding
// audit.Event in one call — mirrors core/event/branch.NewCreatedEvent.
// Callers pass the returned audit.Event to an audit.Emitter; they use
// the SwitchedEvent for any local in-process signalling they need.
func NewSwitchedEvent(
	sessionID string,
	fromProfileID, fromModelID string,
	toProfileID, toModelID string,
	now time.Time,
) (SwitchedEvent, audit.Event, error) {
	ev := SwitchedEvent{
		SessionID:     sessionID,
		FromProfileID: fromProfileID,
		FromModelID:   fromModelID,
		ToProfileID:   toProfileID,
		ToModelID:     toModelID,
		Timestamp:     now,
	}
	auditEvent, err := audit.Marshal(audit.KindModelSwitched, audit.ModelSwitchedPayload{
		SessionID:     sessionID,
		FromProfileID: fromProfileID,
		FromModelID:   fromModelID,
		ToProfileID:   toProfileID,
		ToModelID:     toModelID,
	}, now)
	if err != nil {
		return SwitchedEvent{}, audit.Event{}, err
	}
	return ev, auditEvent, nil
}
