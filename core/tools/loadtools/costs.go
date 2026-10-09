package loadtools

import (
	"context"
	"fmt"
	"sort"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// TierMixed is a ServerCost tier when the server's tools resolve to
// different tiers.
const TierMixed = "mixed"

// ToolCost is one tool's schema cost and resolved tier.
type ToolCost struct {
	// Name is the bare tool name (no "<server>__" prefix), the key a
	// layer's per-tool setting uses.
	Name      string             `json:"name"`
	TokenEst  int                `json:"tokenEst"`
	Tier      toolexposure.Tier  `json:"tier"`
	Source    toolexposure.Level `json:"source"`
	Activated bool               `json:"activated"`
	Sendable  bool               `json:"sendable"`
	// Hot is true for the built-in hot set (full by harness default); a
	// server-wide built-in tier applies to these too unless a per-tool
	// entry keeps them full.
	Hot bool `json:"hot"`
}

// ServerCost is one server's schema cost and resolved tier in one scope
// (a session, a project, or the user's default).
type ServerCost struct {
	Server  string `json:"server"`
	State   string `json:"state"`
	Running bool   `json:"running"`
	// ToolCount and TokenEst cover the server's listed tools; both are 0
	// for a server that is not running (its tools cannot be listed).
	ToolCount int `json:"toolCount"`
	TokenEst  int `json:"tokenEst"`
	// Tier is the tools' common tier, or TierMixed.
	Tier string `json:"tier"`
	// Source is the layer that decided Tier; empty when the tools'
	// tiers came from different layers.
	Source toolexposure.Level `json:"source"`
	// Pinned is true when an organisation pin decided any tool's tier.
	Pinned bool `json:"pinned"`
	// SendableTokenEst is the cost of the tools a call in this scope
	// sends: full tools plus, in a session, activated summary tools.
	SendableTokenEst int        `json:"sendableTokenEst"`
	Tools            []ToolCost `json:"tools"`
}

// SchemaCosts reports every server's schema cost and resolved tier for
// one scope. With sessionID set it resolves that session (its override
// and activated set included); otherwise it resolves a session with no
// override and no activations in projectID, or in no project when
// projectID is empty (the user's default).
func (s *Service) SchemaCosts(ctx context.Context, sessionID, projectID string) ([]ServerCost, error) {
	entries, err := s.d.Catalog.Catalog(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("loadtools: catalog: %w", err)
	}
	servers := s.servers(ctx)
	resolver := s.d.Resolver
	if sessionID == "" {
		w, err := s.storedLayer(ctx, projectID)
		if err != nil {
			return nil, err
		}
		resolver, err = toolexposure.NewResolver(resolver.Deps().WithLayer(w))
		if err != nil {
			return nil, err
		}
	}
	rc, err := resolver.Resolve(ctx, sessionID, CatalogWithProbes(entries, servers))
	if err != nil {
		return nil, err
	}
	tokens := make(map[string]int, len(entries))
	for _, e := range entries {
		tokens[e.Name] = e.TokenEst
	}
	return serverCosts(rc, servers, tokens), nil
}

// storedLayer is the stored project layer (or the stored user layer when
// projectID is empty) as a LayerWrite, so WithLayer resolves the scope
// with no session.
func (s *Service) storedLayer(ctx context.Context, projectID string) (toolexposure.LayerWrite, error) {
	deps := s.d.Resolver.Deps()
	if projectID != "" {
		e, err := deps.Projects.ProjectToolExposure(ctx, projectID)
		if err != nil {
			return toolexposure.LayerWrite{}, fmt.Errorf("loadtools: project %s: %w", projectID, err)
		}
		return toolexposure.LayerWrite{Level: toolexposure.LevelProject, ProjectID: projectID, Exposure: e}, nil
	}
	st, err := deps.Settings.GetToolExposure(ctx)
	if err != nil {
		return toolexposure.LayerWrite{}, fmt.Errorf("loadtools: settings: %w", err)
	}
	return toolexposure.LayerWrite{Level: toolexposure.LevelUser, Exposure: st.Exposure}, nil
}

func serverCosts(rc toolexposure.ResolvedCatalog, servers map[string]ServerInfo, tokens map[string]int) []ServerCost {
	byServer := map[string]*ServerCost{}
	var order []string
	get := func(name string) *ServerCost {
		if sc, ok := byServer[name]; ok {
			return sc
		}
		sc := &ServerCost{Server: name, Tools: []ToolCost{}}
		if si, ok := servers[name]; ok {
			sc.State, sc.Running = si.State, si.Running
		}
		byServer[name] = sc
		order = append(order, name)
		return sc
	}
	tiers := map[string]map[toolexposure.Tier]bool{}
	sources := map[string]map[toolexposure.Level]bool{}
	for _, t := range rc.Tools {
		sc := get(t.Server)
		if tiers[t.Server] == nil {
			tiers[t.Server] = map[toolexposure.Tier]bool{}
			sources[t.Server] = map[toolexposure.Level]bool{}
		}
		tiers[t.Server][t.Tier] = true
		sources[t.Server][t.Source] = true
		if t.Source == toolexposure.LevelOrgPin {
			sc.Pinned = true
		}
		if t.Probe {
			continue
		}
		if t.Running {
			sc.Running = true
		}
		_, activated := rc.Activated(t.Name)
		sendable := rc.Sendable(t.Name)
		est := tokens[t.Name]
		sc.ToolCount++
		sc.TokenEst += est
		if sendable {
			sc.SendableTokenEst += est
		}
		sc.Tools = append(sc.Tools, ToolCost{
			Name:      bareToolName(t),
			TokenEst:  est,
			Tier:      t.Tier,
			Source:    t.Source,
			Activated: activated,
			Sendable:  sendable,
			Hot:       t.Server == toolexposure.BuiltinServer && toolexposure.InHotSet(t.Name),
		})
	}
	out := make([]ServerCost, 0, len(order))
	for _, name := range order {
		sc := byServer[name]
		sc.Tier = TierMixed
		if len(tiers[name]) == 1 {
			for t := range tiers[name] {
				sc.Tier = string(t)
			}
		}
		if len(sources[name]) == 1 {
			for l := range sources[name] {
				sc.Source = l
			}
		}
		sort.Slice(sc.Tools, func(i, j int) bool { return sc.Tools[i].Name < sc.Tools[j].Name })
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

func bareToolName(t toolexposure.ResolvedTool) string {
	p := t.Server + toolexposure.NameSeparator
	if len(t.Name) > len(p) && t.Name[:len(p)] == p {
		return t.Name[len(p):]
	}
	return t.Name
}
