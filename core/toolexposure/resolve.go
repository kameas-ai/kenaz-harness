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
	// one tool resolved summary.
	LevelInvariant Level = "invariant"
)

// CatalogTool is one tool the harness could expose: its namespaced
// name, the server that serves it, and whether that server is running.
type CatalogTool struct {
	Name    string
	Server  string
	Running bool
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
	Tier    Tier
	// Source is the layer that decided Tier.
	Source Level
}

// ResolvedCatalog is every catalog tool's tier for one session, plus
// the session's activated set and the effective budget and TTL.
type ResolvedCatalog struct {
	Tools              []ResolvedTool
	Activations        []Activation
	SchemaBudgetTokens int
	ActivationTTLTurns int
}

// Tool returns the resolved entry for a namespaced name.
func (c ResolvedCatalog) Tool(name string) (ResolvedTool, bool) {
	for _, t := range c.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return ResolvedTool{}, false
}

// CatalogSource lists every tool the session could be shown.
//
// wiring:deferred(the production adapter over the MCP pool + built-in registry lands with tool-context-budget-01TCBUD01 WP03, which first calls Resolve on the request path; dated 2026-10-08, owner: alec)
type CatalogSource interface {
	Catalog(ctx context.Context, sessionID string) ([]CatalogTool, error)
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
	Catalog  CatalogSource
	Settings SettingsSource
	Sessions SessionSource
	Projects ProjectSource
	Pins     PinSource
}

// ErrMissingDep is returned when a required dependency is nil.
var ErrMissingDep = errors.New("toolexposure: resolver dependency not wired")

// Resolve folds org pins, the session override, the project override,
// the user's settings and the harness default into one tier per catalog
// tool (spec §2.1; first layer with an opinion wins, and inside a layer
// a tool entry beats its server's tier). LoadToolsName is then forced
// full whenever any tool resolved summary.
func Resolve(ctx context.Context, deps Deps, sessionID string) (ResolvedCatalog, error) {
	if deps.Catalog == nil || deps.Settings == nil || deps.Sessions == nil || deps.Projects == nil {
		return ResolvedCatalog{}, ErrMissingDep
	}
	tools, err := deps.Catalog.Catalog(ctx, sessionID)
	if err != nil {
		return ResolvedCatalog{}, fmt.Errorf("toolexposure: catalog: %w", err)
	}
	user, err := deps.Settings.GetToolExposure(ctx)
	if err != nil {
		return ResolvedCatalog{}, fmt.Errorf("toolexposure: settings: %w", err)
	}
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
		Tools:              make([]ResolvedTool, 0, len(tools)),
		Activations:        append([]Activation(nil), sess.Activations...),
		SchemaBudgetTokens: user.EffectiveSchemaBudgetTokens(),
		ActivationTTLTurns: user.EffectiveActivationTTLTurns(),
	}
	anySummary := false
	loadTools := -1
	for _, c := range tools {
		rt := ResolvedTool{Name: c.Name, Server: c.Server, Running: c.Running}
		bare := c.bareName()
		for _, l := range layers {
			if t := l.exp.lookup(c.Server, bare); t != "" {
				rt.Tier, rt.Source = t, l.level
				break
			}
		}
		if rt.Tier == "" {
			rt.Tier, rt.Source = DefaultTier(c), LevelDefault
		}
		if rt.Tier == TierSummary {
			anySummary = true
		}
		if c.Name == LoadToolsName {
			loadTools = len(out.Tools)
		}
		out.Tools = append(out.Tools, rt)
	}
	if anySummary && loadTools >= 0 && out.Tools[loadTools].Tier != TierFull {
		out.Tools[loadTools].Tier = TierFull
		out.Tools[loadTools].Source = LevelInvariant
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
