package sessions

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
)

// AutonomyContextProvider resolves the global + project layers needed
// to fold the session-level Layer into a ResolvedAutonomy. The chassis
// wires this with a closure over the settings store + project manager
// (see core/rpc/api.go). Tests pass a recording fake.
//
// (autonomy-dial-01KR3M2A WP03)
type AutonomyContextProvider interface {
	// LoadGlobal returns the persisted global autonomy.Layer.
	LoadGlobal(ctx context.Context) (autonomy.Layer, error)
	// LoadProjectForSession returns the per-project Layer for the session's
	// owning project. Returns the empty Layer when the session is loose.
	LoadProjectForSession(ctx context.Context, sessionID string) (autonomy.Layer, error)
}

// AutonomyContextFunc adapts two functions into the interface.
type AutonomyContextFunc struct {
	Global            func(ctx context.Context) (autonomy.Layer, error)
	ProjectForSession func(ctx context.Context, sessionID string) (autonomy.Layer, error)
}

// LoadGlobal satisfies AutonomyContextProvider.
func (f AutonomyContextFunc) LoadGlobal(ctx context.Context) (autonomy.Layer, error) {
	if f.Global == nil {
		return autonomy.Layer{}, nil
	}
	return f.Global(ctx)
}

// LoadProjectForSession satisfies AutonomyContextProvider.
func (f AutonomyContextFunc) LoadProjectForSession(ctx context.Context, sessionID string) (autonomy.Layer, error) {
	if f.ProjectForSession == nil {
		return autonomy.Layer{}, nil
	}
	return f.ProjectForSession(ctx, sessionID)
}

// WithAutonomyContext wires an AutonomyContextProvider into the
// SessionsAPI so ResolveAutonomy can fold global → project → session.
// Returns the input api unchanged when not a *managerAPI.
func WithAutonomyContext(api SessionsAPI, p AutonomyContextProvider) SessionsAPI {
	if m, ok := api.(*managerAPI); ok {
		m.autonomyCtx = p
	}
	return api
}

// ErrAutonomyContextNotConfigured is returned by ResolveAutonomy when
// the chassis has not wired an AutonomyContextProvider. The frontend
// renders this as a fallback "tier-default" chip.
var ErrAutonomyContextNotConfigured = errors.New("rpc/sessions: autonomy context not configured")

// LoadAutonomyProfile implements SessionsAPI.
func (a *managerAPI) LoadAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error) {
	return a.mgr.GetAutonomyProfile(ctx, id)
}

// SaveAutonomyProfile implements SessionsAPI.
func (a *managerAPI) SaveAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error {
	return a.mgr.SetAutonomyProfile(ctx, id, layer)
}

// ResolveAutonomy implements SessionsAPI by folding global → project →
// session layers through autonomy.Resolve. Returns the resolved knobs
// plus the three input layers. Falls back to empty global/project when
// no AutonomyContextProvider was wired (still a valid result — the
// session layer + tier-default chain).
func (a *managerAPI) ResolveAutonomy(ctx context.Context, id string) (ResolvedAutonomy, error) {
	var global, project autonomy.Layer
	if a.autonomyCtx != nil {
		g, err := a.autonomyCtx.LoadGlobal(ctx)
		if err != nil {
			return ResolvedAutonomy{}, err
		}
		global = g
		p, err := a.autonomyCtx.LoadProjectForSession(ctx, id)
		if err != nil {
			return ResolvedAutonomy{}, err
		}
		project = p
	}
	session, err := a.mgr.GetAutonomyProfile(ctx, id)
	if err != nil {
		return ResolvedAutonomy{}, err
	}
	resolved := autonomy.Resolve(global, project, session)
	out := ResolvedAutonomy{
		Resolved: toAutonomyKnobValues(resolved, global, project, session),
		Global:   global,
		Project:  project,
		Session:  session,
	}
	return out, nil
}

// toAutonomyKnobValues marshals a ResolvedKnobs into the wire shape +
// computes a stable "effective tier" label using the highest-priority
// layer that contributed a Level (session > project > global > default).
func toAutonomyKnobValues(r autonomy.ResolvedKnobs, global, project, session autonomy.Layer) AutonomyKnobValues {
	families := []string{}
	if r.AutoApproveFamilies != nil {
		families = r.AutoApproveFamilies.Sorted()
	}
	trace := make(map[string]string, len(r.SourceTrace))
	for k, v := range r.SourceTrace {
		trace[string(k)] = string(v)
	}
	tier := "default"
	switch {
	case session.Level != nil:
		tier = session.Level.String()
	case project.Level != nil:
		tier = project.Level.String()
	case global.Level != nil:
		tier = global.Level.String()
	}
	return AutonomyKnobValues{
		MaxIterations:            r.MaxIterations,
		AskOnAmbiguity:           string(r.AskOnAmbiguity),
		AutoApproveFamilies:      families,
		TokenCeilingPerTurn:      r.TokenCeilingPerTurn,
		RecapStyle:               string(r.RecapStyle),
		ContinueOnError:          string(r.ContinueOnError),
		DestructiveActionPosture: string(r.DestructiveActionPosture),
		SourceTrace:              trace,
		Tier:                     tier,
	}
}
