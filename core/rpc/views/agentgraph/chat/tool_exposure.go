package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// ToolExposure is the source of per-call tool exposure decisions (spec
// tool-context-budget-01TCBUD01 §2.1–2.3): which discovered tools a
// model call carries in full, and what kenaz__load_tools' description
// lists. *loadtools.Service implements it.
type ToolExposure interface {
	// Resolver resolves a session's catalog to one tier per tool.
	Resolver() *toolexposure.Resolver
	// Servers reports installed servers, their live state and purpose.
	Servers(ctx context.Context) map[string]loadtools.ServerInfo
	// AutoActivate activates one summary tool the model called by exact
	// name; it reports whether the tool is activated now.
	AutoActivate(ctx context.Context, sessionID, name string) (bool, error)
}

var _ ToolExposure = (*loadtools.Service)(nil)

// exposureTurn is one turn's view of tool exposure. It holds the turn's
// discovered catalog and a resolver whose user-settings read is taken
// once, at turn start; every model call re-resolves against it, which
// re-reads the session's activated set, so a tool kenaz__load_tools
// activates on call N is sent on call N+1 of the same turn (FR-E4).
type exposureTurn struct {
	src       ToolExposure
	resolver  *toolexposure.Resolver
	sessionID string
	entries   []loadtools.CatalogEntry
	byName    map[string]corellm.ToolSpec

	mu            sync.Mutex
	autoTried     map[string]bool
	autoActivated int
}

// newExposureTurn snapshots the user settings for the turn. A failed
// settings read leaves the turn without a resolver: every call then
// sends the harness-default full set only (selectTools' fallback).
func newExposureTurn(ctx context.Context, src ToolExposure, sessionID string, catalog []corellm.ToolSpec) *exposureTurn {
	t := &exposureTurn{
		src:       src,
		sessionID: sessionID,
		byName:    make(map[string]corellm.ToolSpec, len(catalog)),
		autoTried: map[string]bool{},
	}
	for _, spec := range catalog {
		t.byName[spec.Name] = spec
		t.entries = append(t.entries, loadtools.CatalogEntry{Name: spec.Name, Server: spec.Server})
	}
	if base := src.Resolver(); base != nil {
		r, err := base.ForTurn(ctx)
		if err != nil {
			logging.L().Warn("chat.tool_exposure.settings_read_failed",
				"session_id", sessionID, "err", err.Error())
		} else {
			t.resolver = r
		}
	}
	return t
}

// toolSelection is what one model call carries.
type toolSelection struct {
	tools []corellm.ToolSpec
	// stable is how many leading tools form the call-to-call stable
	// prefix (hot + pinned); see assembleRequestTools.
	stable int
	// summary is the number of catalog tools listed only in the digest.
	summary int
	// digest is the per-call "Available but not loaded" system section
	// (loadtools.RenderDigest); "" when nothing is unloaded. It changes
	// with every activation, so it travels after the cacheable prefix
	// (appendDigest), never in a tool description.
	digest string
	// autoActivated is the turn's auto-activation count so far.
	autoActivated int
}

// resolve resolves the turn's catalog for this call.
func (t *exposureTurn) resolve(ctx context.Context) (toolexposure.ResolvedCatalog, map[string]loadtools.ServerInfo, error) {
	servers := t.src.Servers(ctx)
	if t.resolver == nil {
		return toolexposure.ResolvedCatalog{}, servers, fmt.Errorf("chat: tool exposure: no settings snapshot for this turn")
	}
	rc, err := loadtools.ResolveCatalog(ctx, t.resolver, t.sessionID, t.entries, servers)
	return rc, servers, err
}

// selectTools builds one model call's tools array — the partition's hot,
// pinned and activated segments in that order — and the digest of
// everything else. Only tools toolexposure.ResolvedCatalog.Sendable
// admits are sent (FR-E1). Every tool keeps its own static description,
// so the hot + pinned prefix is byte-identical from call to call.
//
// If resolution fails the call carries the catalog's harness-default
// full tools only (toolexposure.DefaultTier): never a summary tool, so
// the failure costs reach, not budget. Its digest lists the default
// summary tools under a note that settings could not be read.
func (t *exposureTurn) selectTools(ctx context.Context) toolSelection {
	t.mu.Lock()
	auto := t.autoActivated
	t.mu.Unlock()

	rc, servers, err := t.resolve(ctx)
	if err != nil {
		logging.L().Warn("chat.tool_exposure.resolve_failed",
			"session_id", t.sessionID, "err", err.Error())
		var hot []corellm.ToolSpec
		for _, e := range t.entries {
			if toolexposure.DefaultTier(toolexposure.CatalogTool{Name: e.Name, Server: e.Server}) == toolexposure.TierFull {
				hot = append(hot, t.byName[e.Name])
			}
		}
		sort.SliceStable(hot, func(i, j int) bool { return hot[i].Name < hot[j].Name })
		tools, stable := assembleRequestTools(hot, nil, nil)
		digestServers := loadtools.DefaultDigest(t.entries, loadtools.Purposes(servers))
		summary := 0
		for _, d := range digestServers {
			summary += d.Count
		}
		return toolSelection{
			tools: tools, stable: stable, summary: summary, autoActivated: auto,
			digest: loadtools.RenderDigest(digestServers, loadtools.NoteDefaultsApply),
		}
	}

	p := rc.Partition()
	specs := func(seg []toolexposure.ResolvedTool) []corellm.ToolSpec {
		out := make([]corellm.ToolSpec, 0, len(seg))
		for _, rt := range seg {
			if spec, ok := t.byName[rt.Name]; ok {
				out = append(out, spec)
			}
		}
		return out
	}
	tools, stable := assembleRequestTools(specs(p.Hot), specs(p.Pinned), specs(p.Activated))
	return toolSelection{
		tools: tools, stable: stable, summary: len(p.Digest), autoActivated: auto,
		digest: loadtools.RenderDigest(loadtools.BuildDigest(p, loadtools.Purposes(servers)), ""),
	}
}

// appendDigest adds the per-call digest section after everything else
// in the system prompt, so the stable system text before it stays a
// cacheable prefix. "" leaves system unchanged.
func appendDigest(system, digest string) string {
	if digest == "" {
		return system
	}
	if system == "" {
		return digest
	}
	return system + "\n\n" + digest
}

// willSend reports whether this turn's next model call carries name's
// schema: the tool is in the turn's catalog and, resolved against the
// turn's settings snapshot and the session's current activations, is
// sendable. A loaded tool outside the turn's catalog (its server started
// mid-turn, or the turn withholds it) is sent from the next turn.
func (t *exposureTurn) willSend(ctx context.Context, name string) bool {
	if _, ok := t.byName[name]; !ok {
		return false
	}
	rc, _, err := t.resolve(ctx)
	return err == nil && rc.Sendable(name)
}

// assembleRequestTools lays out one call's tools array from its three
// segments (spec §2.3): hot then pinned, each sorted by name, then
// activated in the order given (most recently used first). stable is the
// count of leading tools that stay byte-identical from call to call
// (hot + pinned) — the cacheable prefix; activated tools follow it.
// Callers pass hot and pinned already sorted by name.
func assembleRequestTools(hot, pinned, activated []corellm.ToolSpec) (tools []corellm.ToolSpec, stable int) {
	tools = make([]corellm.ToolSpec, 0, len(hot)+len(pinned)+len(activated))
	tools = append(tools, hot...)
	tools = append(tools, pinned...)
	tools = append(tools, activated...)
	return tools, len(hot) + len(pinned)
}

// notLoadedResult is the structured tool result for a call to a tool
// whose schema the model was not sent.
type notLoadedResult struct {
	Error   string `json:"error"`
	Tool    string `json:"tool"`
	Loaded  bool   `json:"loaded_now"`
	Message string `json:"message"`
}

// gateCall stops a call to a catalog tool the session may not be sent:
// an off tool is refused with the setting that turned it off; a summary
// tool that is not activated is activated once per turn (the model's
// next call then carries its schema) and the call is answered with a
// structured "not loaded" error so the model calls it again with the
// definition in view. It reports false — dispatch proceeds — for a
// sendable tool, a name outside the catalog, or when resolution fails
// (the permission gates still decide the call).
func (t *exposureTurn) gateCall(ctx context.Context, name string) (coreag.ToolResult, bool) {
	if _, inCatalog := t.byName[name]; !inCatalog {
		return coreag.ToolResult{}, false
	}
	rc, _, err := t.resolve(ctx)
	if err != nil || rc.Sendable(name) {
		return coreag.ToolResult{}, false
	}
	rt, ok := rc.Tool(name)
	if !ok {
		return coreag.ToolResult{}, false
	}
	res := notLoadedResult{Error: "not_loaded", Tool: name}
	switch rt.Tier {
	case toolexposure.TierOff:
		res.Error = "not_available"
		res.Message = fmt.Sprintf("tool %s is not available: %s", name, loadtools.OffReason(rt.Source))
	default:
		t.mu.Lock()
		first := !t.autoTried[name]
		t.autoTried[name] = true
		t.mu.Unlock()
		if first {
			activated, aerr := t.src.AutoActivate(ctx, t.sessionID, name)
			if aerr != nil {
				logging.L().Warn("chat.tool_exposure.auto_activate_failed",
					"session_id", t.sessionID, "tool", name, "err", aerr.Error())
			}
			if activated {
				t.mu.Lock()
				t.autoActivated++
				t.mu.Unlock()
				res.Loaded = true
			}
		}
		if res.Loaded {
			res.Message = fmt.Sprintf("tool %s was not loaded, so this call did not run. It is loaded now: its definition is in your next request — call it again.", name)
		} else {
			res.Message = fmt.Sprintf("tool %s is not loaded — call %s with {\"tools\": [%q]} first.", name, loadtools.Name, name)
		}
	}
	body, _ := json.Marshal(res)
	return coreag.ToolResult{Content: string(body), IsError: true}, true
}
