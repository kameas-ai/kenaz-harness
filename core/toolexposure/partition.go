package toolexposure

import (
	"context"
	"errors"
	"sort"
)

// Activated reports whether the session's activated set holds name.
func (c ResolvedCatalog) Activated(name string) (Activation, bool) {
	for _, a := range c.Activations {
		if a.Name == name {
			return a, true
		}
	}
	return Activation{}, false
}

// Sendable reports whether a tool's schema may be sent on a call (spec
// FR-E1): its server is running and its tier is full, or it is summary
// and in the activated set. An off tool is never sendable, activated or
// not, and a name the catalog does not hold is not sendable.
func (c ResolvedCatalog) Sendable(name string) bool {
	t, ok := c.Tool(name)
	if !ok || !t.Running || t.Probe {
		return false
	}
	switch t.Tier {
	case TierFull:
		return true
	case TierSummary:
		_, act := c.Activated(name)
		return act
	}
	return false
}

// Partition is a resolved catalog split into the segments one call's
// tools array is built from (spec §2.3), each in its send order.
//
//   - Hot: full by the harness default, the organisation's
//     hot_set_extra or the load_tools invariant, alphabetical.
//   - Pinned: full because a layer (org pin, session, project, user, org
//     default) says so, plus summary tools with a sticky activation;
//     alphabetical.
//   - Activated: summary tools with a non-sticky activation, most
//     recently used first, then by name.
//   - Digest: summary tools that are not activated — what
//     kenaz__load_tools' description lists.
//   - Stopped: entries whose server is not running and whose tier is not
//     off, alphabetical. They are never sent (FR-H1); the digest marks
//     their servers stopped.
//
// Off tools are in no segment. Hot, Pinned and Activated are exactly the
// tools Sendable reports true for.
//
// A catalog may carry, for a server that is installed but not running,
// one ServerProbe entry: it resolves to the server-wide tier and lands
// in Stopped unless that tier is off.
type Partition struct {
	Hot       []ResolvedTool
	Pinned    []ResolvedTool
	Activated []ResolvedTool
	Digest    []ResolvedTool
	Stopped   []ResolvedTool
}

// SendNames returns the names of every tool the call carries, in
// order: Hot, then Pinned, then Activated.
func (p Partition) SendNames() []string {
	out := make([]string, 0, len(p.Hot)+len(p.Pinned)+len(p.Activated))
	for _, seg := range [][]ResolvedTool{p.Hot, p.Pinned, p.Activated} {
		for _, t := range seg {
			out = append(out, t.Name)
		}
	}
	return out
}

// Partition splits the catalog per spec §2.3.
func (c ResolvedCatalog) Partition() Partition {
	var p Partition
	lastUsed := map[string]int{}
	for _, t := range c.Tools {
		if !t.Running || t.Probe {
			if t.Tier != TierOff {
				p.Stopped = append(p.Stopped, t)
			}
			continue
		}
		switch t.Tier {
		case TierFull:
			if t.Source == LevelDefault || t.Source == LevelInvariant || t.Source == LevelOrgHotSet {
				p.Hot = append(p.Hot, t)
			} else {
				p.Pinned = append(p.Pinned, t)
			}
		case TierSummary:
			a, ok := c.Activated(t.Name)
			switch {
			case !ok:
				p.Digest = append(p.Digest, t)
			case a.Sticky:
				p.Pinned = append(p.Pinned, t)
			default:
				lastUsed[t.Name] = a.LastUsedTurn
				p.Activated = append(p.Activated, t)
			}
		}
	}
	byName := func(s []ResolvedTool) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Name < s[j].Name })
	}
	byName(p.Hot)
	byName(p.Pinned)
	byName(p.Digest)
	byName(p.Stopped)
	sort.SliceStable(p.Activated, func(i, j int) bool {
		li, lj := lastUsed[p.Activated[i].Name], lastUsed[p.Activated[j].Name]
		if li != lj {
			return li > lj
		}
		return p.Activated[i].Name < p.Activated[j].Name
	})
	return p
}

// fixedSettings is a SettingsSource that returns one value.
type fixedSettings struct{ s Settings }

func (f fixedSettings) GetToolExposure(context.Context) (Settings, error) { return f.s, nil }

// ForTurn reads the user's settings once and returns a resolver that
// reuses that read, so the several model calls of one turn resolve
// against one settings snapshot. Session and project layers are still
// read on every Resolve: they change inside a turn (activations).
func (r *Resolver) ForTurn(ctx context.Context) (*Resolver, error) {
	s, err := r.deps.Settings.GetToolExposure(ctx)
	if err != nil {
		return nil, err
	}
	d := r.deps
	d.Settings = fixedSettings{s: s}
	return &Resolver{deps: d}, nil
}

// LayerWrite is a proposed write of one override layer.
type LayerWrite struct {
	// Level is LevelUser, LevelProject or LevelSession.
	Level Level
	// ProjectID is set for LevelProject.
	ProjectID string
	// SessionID is set for LevelSession.
	SessionID string
	// Exposure is the layer as it would be stored.
	Exposure Exposure
}

// WriteGuard vets a layer write against the live catalog before it is
// stored. Writers call it after Validate; a nil guard checks nothing.
type WriteGuard interface {
	CheckLayerWrite(ctx context.Context, w LayerWrite) error
}

// ErrLoadToolsRequired is wrapped by a WriteGuard refusal of a layer
// that would turn LoadToolsName off (or to summary) while other tools
// resolve summary: those tools would have no way to be loaded.
var ErrLoadToolsRequired = errors.New("toolexposure: kenaz__load_tools is required while tools are in the summary tier")

// WithLayer returns deps whose source for w.Level returns w.Exposure in
// place of the stored layer, for resolving a proposed write before it is
// made. A user or project write has no session: the returned deps
// resolve any session id as a session with no override and no
// activations, in project w.ProjectID (empty for a user write).
func (d Deps) WithLayer(w LayerWrite) Deps {
	switch w.Level {
	case LevelUser:
		d.Settings = overrideSettings{inner: d.Settings, e: w.Exposure}
		d.Sessions = bareSession{}
	case LevelProject:
		d.Projects = overrideProject{inner: d.Projects, id: w.ProjectID, e: w.Exposure}
		d.Sessions = bareSession{projectID: w.ProjectID}
	case LevelSession:
		d.Sessions = overrideSession{inner: d.Sessions, id: w.SessionID, e: w.Exposure}
	}
	return d
}

type bareSession struct{ projectID string }

func (b bareSession) SessionToolExposure(context.Context, string) (SessionState, error) {
	return SessionState{ProjectID: b.projectID}, nil
}

// Deps returns the resolver's sources.
func (r *Resolver) Deps() Deps { return r.deps }

type overrideSettings struct {
	inner SettingsSource
	e     Exposure
}

func (o overrideSettings) GetToolExposure(ctx context.Context) (Settings, error) {
	s, err := o.inner.GetToolExposure(ctx)
	if err != nil {
		return Settings{}, err
	}
	s.Exposure = o.e
	return s, nil
}

type overrideProject struct {
	inner ProjectSource
	id    string
	e     Exposure
}

func (o overrideProject) ProjectToolExposure(ctx context.Context, id string) (Exposure, error) {
	if id == o.id {
		return o.e, nil
	}
	return o.inner.ProjectToolExposure(ctx, id)
}

type overrideSession struct {
	inner SessionSource
	id    string
	e     Exposure
}

func (o overrideSession) SessionToolExposure(ctx context.Context, id string) (SessionState, error) {
	st, err := o.inner.SessionToolExposure(ctx, id)
	if err != nil {
		return SessionState{}, err
	}
	if id == o.id {
		st.Override = o.e
	}
	return st, nil
}

// ServerProbe is the catalog entry standing for server, installed but
// not running. Its name is the server prefix alone ("outlook__"), which
// no tool can have; the Probe flag, not the name, is what marks it.
func ServerProbe(server string) CatalogTool {
	return CatalogTool{Name: server + NameSeparator, Server: server, Probe: true}
}
