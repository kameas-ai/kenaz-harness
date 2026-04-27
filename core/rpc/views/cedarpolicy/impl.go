package cedarpolicy

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// Engine is the small subset of *cedar.Engine the API needs. Defined
// as an interface so tests can drive the API without constructing a
// real Cedar engine.
type Engine interface {
	ListPolicies() []cedar.PolicyFile
	Reload(ctx context.Context) error
	RecentDecisions(limit int) []cedar.Decision
}

// Compile-time witness: *cedar.Engine satisfies Engine.
var _ Engine = (*cedar.Engine)(nil)

// API is the concrete CedarPolicyAPI implementation.
type API struct {
	engine Engine
}

// NewAPI constructs the view-scoped API. engine MAY be nil — in that
// case every method returns an empty result with no error so the
// frontend renders an empty panel rather than an exception screen
// during boot before the engine is wired.
func NewAPI(engine Engine) *API {
	return &API{engine: engine}
}

// ListPolicies implements CedarPolicyAPI.
func (a *API) ListPolicies(_ context.Context) ([]PolicyFile, error) {
	if a == nil || a.engine == nil {
		return []PolicyFile{}, nil
	}
	return a.engine.ListPolicies(), nil
}

// ReloadPolicies implements CedarPolicyAPI.
func (a *API) ReloadPolicies(ctx context.Context) error {
	if a == nil || a.engine == nil {
		return errors.New("cedarpolicy: engine not wired")
	}
	return a.engine.Reload(ctx)
}

// RecentDecisions implements CedarPolicyAPI.
func (a *API) RecentDecisions(_ context.Context, limit int) ([]Decision, error) {
	if a == nil || a.engine == nil {
		return []Decision{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return a.engine.RecentDecisions(limit), nil
}
