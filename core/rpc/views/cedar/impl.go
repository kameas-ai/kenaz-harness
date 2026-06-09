package cedar

import (
	"context"

	contextaudit "github.com/sigil-tech/kaneaz-harness/core/context/audit"
	corefleet "github.com/sigil-tech/kaneaz-harness/core/fleet"
)

// auditEmitter is the minimal interface the cedar view needs for audit.
type auditEmitter interface {
	EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error
}

// API implements CedarAPI.
type API struct {
	client      *corefleet.Client
	identity    func() (string, error) // returns current user email/id for audit
	emitter     auditEmitter
}

var _ CedarAPI = (*API)(nil)

// NewAPI constructs a CedarAPI. identity is called lazily to get the current
// user's email for audit; pass nil if unavailable.
func NewAPI(client *corefleet.Client, identity func() (string, error), emitter auditEmitter) *API {
	return &API{client: client, identity: identity, emitter: emitter}
}

// Cedar_PublishToTeam implements CedarAPI.
func (a *API) Cedar_PublishToTeam(ctx context.Context, ruleID, ruleSource string) error {
	if a.client == nil {
		return corefleet.ErrFleetDisabled
	}
	author := ""
	if a.identity != nil {
		if id, err := a.identity(); err == nil {
			author = id
		}
	}
	// emitter is wired into the fleet.PublishCedarRule call via the
	// AuditEmitter interface.
	var ae corefleet.AuditEmitter
	if a.emitter != nil {
		ae = &auditEmitterBridge{a.emitter}
	}
	return a.client.PublishCedarRule(ctx, ruleID, ruleSource, author, ae)
}

// auditEmitterBridge adapts the cedar view's auditEmitter to the fleet
// package's AuditEmitter interface.
type auditEmitterBridge struct{ em auditEmitter }

func (b *auditEmitterBridge) EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error {
	return b.em.EmitFleetEvent(ctx, kind, payload)
}
