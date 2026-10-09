package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// ToolExposure is the source of per-call tool exposure decisions (spec
// tool-context-budget-01TCBUD01 §2.1–2.3): which discovered tools a
// model call carries in full, within the schema budget, and what the
// call's digest lists. *loadtools.Service implements it.
type ToolExposure interface {
	// Resolver resolves a session's catalog to one tier per tool.
	Resolver() *toolexposure.Resolver
	// Servers reports installed servers, their live state and purpose.
	Servers(ctx context.Context) map[string]loadtools.ServerInfo
	// AutoActivate activates one summary tool the model called by exact
	// name; it reports whether the tool is activated now.
	AutoActivate(ctx context.Context, sessionID, name string) (bool, error)
	// TurnOrdinal returns the session's current turn ordinal.
	TurnOrdinal(ctx context.Context, sessionID string) (int, error)
	// ExpireActivations drops the session's non-sticky activations past
	// their TTL as of turn and returns them.
	ExpireActivations(ctx context.Context, sessionID string, turn, ttl int) ([]toolexposure.Activation, error)
	// MarkUsed stamps an activated tool the model called with turn.
	MarkUsed(ctx context.Context, sessionID, name string, turn int) (bool, error)
	// RecordEviction audits one call's budget eviction.
	RecordEviction(ctx context.Context, sessionID string, fit toolexposure.BudgetFit)
}

var _ ToolExposure = (*loadtools.Service)(nil)

// ModelWindowFunc returns the context window, in tokens, of a model on a
// provider kind; 0 when unknown.
type ModelWindowFunc func(providerKind, modelID string) int

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

	// start runs beginTurn once, on the turn's first model call: the
	// turn ordinal is only final after the runner records the turn run,
	// which happens after this view is built.
	start sync.Once
	// turn is the session's turn ordinal and turnKnown whether reading
	// it succeeded; both are set by beginTurn.
	turn      int
	turnKnown bool

	mu            sync.Mutex
	autoTried     map[string]bool
	autoActivated int
	// called holds the tools the model called this turn: never evicted
	// for the rest of the turn.
	called map[string]bool
	// loaded holds the tools loaded this turn (kenaz__load_tools or an
	// auto-activation): evicted only after every older activated and
	// pinned tool, and reported by the load when they still do not fit.
	loaded map[string]bool
	// window is the context window of the turn's last model call, which
	// a load fits against.
	window int
	// lastEvicted is the evicted set of the turn's previous call, so an
	// eviction that persists across calls is logged once.
	lastEvicted string
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
		called:    map[string]bool{},
		loaded:    map[string]bool{},
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
	// budget is the call's effective schema budget (0 when no budget
	// was applied: resolution failed).
	budget int
	// evicted is the number of loaded tools left out to fit budget.
	evicted int
	// pinnedOverBy is the part of the overage pinned tools account for;
	// > 0 is the composer's "Pinned tools exceed the schema budget"
	// warning.
	pinnedOverBy int
	// hotOverBy is how far the always-sent core tools alone exceed
	// budget.
	hotOverBy int
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
func (t *exposureTurn) selectTools(ctx context.Context, window int) toolSelection {
	t.beginTurn(ctx)
	t.mu.Lock()
	auto := t.autoActivated
	t.window = window
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

	budget := toolexposure.EffectiveBudget(rc.SchemaBudgetTokens, window)
	fit := t.fit(rc.Partition(), budget)
	t.reportEviction(ctx, fit)
	p := fit.Partition
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
		digest:       loadtools.RenderDigest(loadtools.BuildDigest(p, loadtools.Purposes(servers)), ""),
		budget:       budget,
		evicted:      len(fit.Evicted),
		pinnedOverBy: fit.PinnedOverBy,
		hotOverBy:    fit.HotOverBy,
	}
}

// fit fits p to budget. The budget covers tool definitions only: each
// tool counts its own TokenEst (kenaz__load_tools' static description
// included); the digest section in the system prompt does not count.
// Tools the model called this turn are immune; tools loaded this turn go
// only after every other activated and pinned tool.
func (t *exposureTurn) fit(p toolexposure.Partition, budget int) toolexposure.BudgetFit {
	return p.FitBudget(budget, t.fitSize, t.isCalled, t.isLoaded)
}

func (t *exposureTurn) isCalled(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.called[name]
}

func (t *exposureTurn) isLoaded(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.loaded[name]
}

// reportEviction logs tools.evicted when a call's evicted set differs
// from the previous call's in this turn, and hands every fit to the
// audit, which dedupes per session.
func (t *exposureTurn) reportEviction(ctx context.Context, fit toolexposure.BudgetFit) {
	names := make([]string, 0, len(fit.Evicted))
	for _, e := range fit.Evicted {
		names = append(names, e.Name)
	}
	key := strings.Join(names, ",")
	t.mu.Lock()
	changed := key != t.lastEvicted
	t.lastEvicted = key
	t.mu.Unlock()
	t.src.RecordEviction(ctx, t.sessionID, fit)
	if !changed || len(names) == 0 {
		return
	}
	logging.L().Info("tools.evicted",
		"session_id", t.sessionID,
		"evicted", names,
		"over_by", fit.OverBy,
		"budget", fit.Budget,
		"pinned_evicted", fit.PinnedEvicted,
		"pinned_over_by", fit.PinnedOverBy,
		"hot_over_by", fit.HotOverBy,
		"remaining_over_by", fit.Remaining,
	)
}

// beginTurn reads the turn ordinal and drops activations past their TTL
// before the turn's first call is built, so an expired server is back in
// the digest from that call on. Expiry happens only here: an activation
// cannot expire mid-turn. When the ordinal cannot be read, nothing
// expires and no activation is re-stamped this turn.
func (t *exposureTurn) beginTurn(ctx context.Context) {
	t.start.Do(func() {
		turn, err := t.src.TurnOrdinal(ctx, t.sessionID)
		if err != nil {
			logging.L().Warn("chat.tool_exposure.turn_read_failed",
				"session_id", t.sessionID, "err", err.Error())
			return
		}
		t.turn, t.turnKnown = turn, true
		if t.resolver == nil {
			return
		}
		s, err := t.resolver.Deps().Settings.GetToolExposure(ctx)
		if err != nil {
			return
		}
		ttl := s.WithEffective().EffectiveActivationTTLTurns
		expired, err := t.src.ExpireActivations(ctx, t.sessionID, turn, ttl)
		if err != nil {
			logging.L().Warn("chat.tool_exposure.expire_failed",
				"session_id", t.sessionID, "err", err.Error())
			return
		}
		if len(expired) > 0 {
			names := make([]string, 0, len(expired))
			for _, a := range expired {
				names = append(names, a.Name)
			}
			logging.L().Info("tools.expired",
				"session_id", t.sessionID, "turn", turn, "ttl_turns", ttl, "expired", names)
		}
	})
}

// markCalled records that the model's call to name was permitted to run
// this turn: the tool is immune from eviction for the rest of the turn,
// and an activation of it has its LastUsedTurn refreshed, which restarts
// its TTL. With the turn ordinal unknown the stamp is left as it is.
func (t *exposureTurn) markCalled(ctx context.Context, name string) {
	if _, inCatalog := t.byName[name]; !inCatalog {
		return
	}
	t.beginTurn(ctx)
	t.mu.Lock()
	first := !t.called[name]
	t.called[name] = true
	t.mu.Unlock()
	if !first || !t.turnKnown {
		return
	}
	if _, err := t.src.MarkUsed(ctx, t.sessionID, name, t.turn); err != nil {
		logging.L().Warn("chat.tool_exposure.mark_used_failed",
			"session_id", t.sessionID, "tool", name, "err", err.Error())
	}
}

// fitLoaded is the turn's loadtools.TurnBudget: it keeps names ahead of
// older activations for the rest of the turn, fits the session's current
// catalog against the window of the turn's last call, and returns why
// each of names still cannot be carried.
func (t *exposureTurn) fitLoaded(ctx context.Context, names []string) map[string]string {
	t.mu.Lock()
	for _, n := range names {
		t.loaded[n] = true
	}
	window := t.window
	t.mu.Unlock()
	rc, _, err := t.resolve(ctx)
	if err != nil {
		return nil
	}
	fit := t.fit(rc.Partition(), toolexposure.EffectiveBudget(rc.SchemaBudgetTokens, window))
	if len(fit.Evicted) == 0 {
		return nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := map[string]string{}
	for _, e := range fit.Evicted {
		if !want[e.Name] {
			continue
		}
		reason := loadtools.ReasonOverBudget
		if e.Tier == toolexposure.TierFull {
			reason = loadtools.ReasonPinnedOverBudget
		}
		out[e.Name] = fmt.Sprintf("%s by %d tokens", reason, fit.Remaining+t.fitSize(e))
	}
	return out
}

func (t *exposureTurn) fitSize(rt toolexposure.ResolvedTool) int {
	spec := t.byName[rt.Name]
	if spec.TokenEst > 0 {
		return spec.TokenEst
	}
	return corellm.EstimateToolSpecTokens(spec)
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
				t.loaded[name] = true
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
