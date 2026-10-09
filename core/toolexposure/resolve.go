package toolexposure

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Level names which layer decided a tool's tier. Surfaces use it to say
// why ("set by your organisation", "pinned for this project") and
// refusals use it to name the setting that turned a tool off.
type Level string

const (
	LevelOrgPin  Level = "org_pin"
	LevelSession Level = "session"
	LevelProject Level = "project"
	LevelUser    Level = "user"
	LevelDefault Level = "default"
	// LevelInvariant marks LoadToolsName forced full because at least
	// one other tool resolved summary.
	LevelInvariant Level = "invariant"
)

// CatalogTool is one tool the harness could expose: its namespaced
// name, the server that serves it, and whether that server is running.
// Probe marks the entry standing for an installed server that is not
// running (see ServerProbe): it is no tool, never sent, and resolves to
// the server-wide tier.
type CatalogTool struct {
	Name    string
	Server  string
	Running bool
	Probe   bool
}

// bareName strips the "<server>__" prefix; a name without it is
// returned unchanged.
func (c CatalogTool) bareName() string {
	if p := c.Server + NameSeparator; strings.HasPrefix(c.Name, p) {
		return c.Name[len(p):]
	}
	return c.Name
}

// ResolvedTool is one catalog tool with its tier for this call. Tier is
// the only place the decision lives: consumers branch on it here rather
// than copying it onto another struct.
type ResolvedTool struct {
	Name    string
	Server  string
	Running bool
	// Probe is copied from CatalogTool.Probe.
	Probe bool
	Tier  Tier
	// Source is the layer that decided Tier.
	Source Level
}

// ResolvedCatalog is every catalog tool's tier for one session, in
// catalog order, plus the session's activated set and the effective
// budget and TTL.
type ResolvedCatalog struct {
	Tools              []ResolvedTool
	Activations        []Activation
	SchemaBudgetTokens int
	ActivationTTLTurns int

	byName map[string]int
}

// Tool returns the resolved entry for a namespaced name.
func (c ResolvedCatalog) Tool(name string) (ResolvedTool, bool) {
	i, ok := c.byName[name]
	if !ok {
		return ResolvedTool{}, false
	}
	return c.Tools[i], true
}

// SettingsSource returns the user-level configuration.
//
// wiring:deferred(settings.API implements this — asserted at compile time in core/rpc/views/settings/impl.go — but checkseams skips that package as a test double because its fleet.go imports "testing"; dated 2026-10-08, owner: alec)
type SettingsSource interface {
	GetToolExposure(ctx context.Context) (Settings, error)
}

// SessionState is what the resolver needs from the session row: its
// project (empty when loose), its override layer and its activations.
type SessionState struct {
	ProjectID   string
	Override    Exposure
	Activations []Activation
}

// SessionSource returns a session's exposure state.
type SessionSource interface {
	SessionToolExposure(ctx context.Context, sessionID string) (SessionState, error)
}

// ProjectSource returns a project's override layer.
type ProjectSource interface {
	ProjectToolExposure(ctx context.Context, projectID string) (Exposure, error)
}

// PinSource returns the organisation's pinned layer from the signed
// bundle; empty when the bundle carries none.
//
// wiring:deferred(the bundle field tool_exposure and its reader land with tool-context-budget-01TCBUD01 WP07, gated on the fleet field; until then Deps.Pins is nil and Resolve's org-pin step sees no opinion; dated 2026-10-08, owner: alec)
type PinSource interface {
	ToolExposurePins(ctx context.Context) (Exposure, error)
}

// Deps are the resolver's inputs. Every source but Pins is required;
// a nil Pins means the organisation pins nothing.
type Deps struct {
	Settings SettingsSource
	Sessions SessionSource
	Projects ProjectSource
	Pins     PinSource
}

// ErrMissingDep is returned when a required dependency is nil.
var ErrMissingDep = errors.New("toolexposure: resolver dependency not wired")

func (d Deps) check() error {
	if d.Settings == nil || d.Sessions == nil || d.Projects == nil {
		return ErrMissingDep
	}
	return nil
}

// Resolver resolves catalogs against a fixed, checked set of sources.
type Resolver struct{ deps Deps }

// NewResolver checks the required sources once, at wiring time.
func NewResolver(deps Deps) (*Resolver, error) {
	if err := deps.check(); err != nil {
		return nil, err
	}
	return &Resolver{deps: deps}, nil
}

// Resolve resolves catalog for sessionID; see the package-level Resolve.
func (r *Resolver) Resolve(ctx context.Context, sessionID string, catalog []CatalogTool) (ResolvedCatalog, error) {
	return Resolve(ctx, r.deps, sessionID, catalog)
}

// Resolve folds org pins, the session override, the project override,
// the user's settings and the harness default into one tier per catalog
// tool (spec §2.1; first layer with an opinion wins, and inside a layer
// a tool entry beats its server's tier). A stored value that is not one
// of the three tiers is no opinion. LoadToolsName is then forced full
// whenever any other tool resolved summary.
func Resolve(ctx context.Context, deps Deps, sessionID string, catalog []CatalogTool) (ResolvedCatalog, error) {
	if err := deps.check(); err != nil {
		return ResolvedCatalog{}, err
	}
	user, err := deps.Settings.GetToolExposure(ctx)
	if err != nil {
		return ResolvedCatalog{}, fmt.Errorf("toolexposure: settings: %w", err)
	}
	user = user.WithEffective()
	sess, err := deps.Sessions.SessionToolExposure(ctx, sessionID)
	if err != nil {
		return ResolvedCatalog{}, fmt.Errorf("toolexposure: session %s: %w", sessionID, err)
	}
	var project Exposure
	if sess.ProjectID != "" {
		project, err = deps.Projects.ProjectToolExposure(ctx, sess.ProjectID)
		if err != nil {
			return ResolvedCatalog{}, fmt.Errorf("toolexposure: project %s: %w", sess.ProjectID, err)
		}
	}
	var pins Exposure
	if deps.Pins != nil {
		pins, err = deps.Pins.ToolExposurePins(ctx)
		if err != nil {
			return ResolvedCatalog{}, fmt.Errorf("toolexposure: org pins: %w", err)
		}
	}
	layers := []struct {
		level Level
		exp   Exposure
	}{
		{LevelOrgPin, pins},
		{LevelSession, sess.Override},
		{LevelProject, project},
		{LevelUser, user.Exposure},
	}

	out := ResolvedCatalog{
		Tools:              make([]ResolvedTool, 0, len(catalog)),
		Activations:        append([]Activation(nil), sess.Activations...),
		SchemaBudgetTokens: user.EffectiveSchemaBudgetTokens,
		ActivationTTLTurns: user.EffectiveActivationTTLTurns,
		byName:             make(map[string]int, len(catalog)),
	}
	anySummary := false
	for _, c := range catalog {
		rt := ResolvedTool{Name: c.Name, Server: c.Server, Running: c.Running, Probe: c.Probe}
		bare := c.bareName()
		if c.Probe {
			bare = ""
		}
		for _, l := range layers {
			if t := l.exp.lookup(c.Server, bare); t != "" {
				rt.Tier, rt.Source = t, l.level
				break
			}
		}
		if rt.Tier == "" {
			rt.Tier, rt.Source = DefaultTier(c), LevelDefault
		}
		if rt.Tier == TierSummary && c.Name != LoadToolsName {
			anySummary = true
		}
		out.byName[c.Name] = len(out.Tools)
		out.Tools = append(out.Tools, rt)
	}
	if i, ok := out.byName[LoadToolsName]; ok && anySummary && out.Tools[i].Tier != TierFull {
		out.Tools[i].Tier = TierFull
		out.Tools[i].Source = LevelInvariant
	}
	return out, nil
}

// DefaultTier is the harness default for a tool no layer has an opinion
// about: the built-in hot set is full, every other tool is summary.
// Nothing is off by default.
func DefaultTier(c CatalogTool) Tier {
	if c.Server == BuiltinServer && InHotSet(c.Name) {
		return TierFull
	}
	return TierSummary
}
