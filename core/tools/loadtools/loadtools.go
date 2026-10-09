// Package loadtools implements kenaz__load_tools, the built-in that
// loads the full definitions of summary-tier tools into a session on
// demand (spec tool-context-budget-01TCBUD01 §2.2), and the shared core
// the composer's load action (Sessions_LoadTools) and the request
// builder's auto-activation use.
//
// Loading a tool adds it to the session's activated set
// (sessions.tool_activations); the request builder sends every activated
// summary tool's schema on the session's next model calls. Loading
// changes what the model is shown, never what it may call: an activated
// tool passes every permission gate at call time exactly as before.
//
// Gating: always on at the builtinEnabledPredicate gate, and per call
// through the use_tool resolution every built-in gets (the shipped
// default_tool_policy.cedar permits server "kenaz"). It is read-class for
// plan mode: it changes only which schemas the session is sent.
package loadtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Name is the tool name the model sees.
const Name = toolexposure.LoadToolsName

// CatalogEntry is one servable tool: its namespaced name and server.
// TokenEst is its schema's token estimate; 0 when the source does not
// measure it.
type CatalogEntry struct {
	Name     string
	Server   string
	TokenEst int
}

// CatalogSource lists the tools a session could be sent — every tool of
// every running server plus every enabled built-in the session may call.
type CatalogSource interface {
	Catalog(ctx context.Context, sessionID string) ([]CatalogEntry, error)
}

// ServerInfo is one installed server as the server pool reports it.
type ServerInfo struct {
	Name string
	// Purpose is the one-line description from the server's recipe
	// metadata; empty when there is none.
	Purpose string
	// State is the pool's live state ("running", "stopped", "failed", …).
	State   string
	Running bool
}

// ServerDirectory reports installed servers and their live state. It is
// the source of the digest's "(stopped)" marker and of purposes: the
// catalog lists only servable tools, so a stopped server is absent from
// it.
type ServerDirectory interface {
	Servers(ctx context.Context) []ServerInfo
}

// ActivationWriter replaces a session's activated set.
type ActivationWriter interface {
	SetToolActivations(ctx context.Context, sessionID string, as []toolexposure.Activation) error
}

// TurnCounter returns the session's current turn ordinal (1 for the
// first turn), the value an activation's LastUsedTurn records.
type TurnCounter interface {
	TurnOrdinal(ctx context.Context, sessionID string) (int, error)
}

// Deps are the Service's collaborators. Catalog, Resolver and
// Activations are required; a nil Servers lists no stopped servers and
// no purposes, a nil Turns records turn 0, a nil Audit records nothing.
type Deps struct {
	Catalog     CatalogSource
	Resolver    *toolexposure.Resolver
	Servers     ServerDirectory
	Activations ActivationWriter
	Turns       TurnCounter
	Audit       audit.Emitter
	Now         func() time.Time
}

// ErrMissingDep is returned by NewService when a required dependency is
// nil.
var ErrMissingDep = errors.New("loadtools: required dependency not wired")

// Service is the load core shared by the tool, the composer binding and
// the request builder.
type Service struct {
	d Deps
	// mu serialises the read-modify-write of activated sets, so two
	// loads in one turn (parallel tool calls) cannot drop each other's
	// activations.
	mu sync.Mutex

	// evictMu guards lastEvicted: per session, the evicted set last
	// audited as KindToolsEvicted.
	evictMu     sync.Mutex
	lastEvicted map[string]string
}

// NewService checks the required dependencies once, at wiring time.
func NewService(d Deps) (*Service, error) {
	if d.Catalog == nil || d.Resolver == nil || d.Activations == nil {
		return nil, ErrMissingDep
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}, nil
}

// Request is what to load. Servers are server names; Tools are exact
// namespaced names ("outlook__send-mail") or prefix globs
// ("outlook__list*"). Sticky activations survive TTL expiry.
type Request struct {
	Servers []string `json:"servers,omitempty"`
	Tools   []string `json:"tools,omitempty"`
	Sticky  bool     `json:"sticky,omitempty"`
}

// NotLoaded is one requested name that was not loaded, with why.
type NotLoaded struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Result reports every requested name (FR-H2). LoadedByServer counts
// the loaded tools (newly activated or already full) per server; Loaded
// names them when there are at most MaxLoadedNames, and is empty
// otherwise so a whole-server load stays short. NextTurn names loaded
// tools the calling turn's next model call will not carry (the turn
// started before their server did, or the turn withholds them); they are
// sent from the session's next turn. NotLoaded is every name that could
// not be loaded and why.
type Result struct {
	Loaded         []string       `json:"loaded"`
	LoadedByServer map[string]int `json:"loaded_by_server"`
	NextTurn       []string       `json:"next_turn,omitempty"`
	NotLoaded      []NotLoaded    `json:"not_loaded"`
	Summary        string         `json:"summary"`
}

// MaxLoadedNames is the most tool names Result.Loaded lists.
const MaxLoadedNames = 10

// ReasonInvalidGlob is the NotLoaded reason for a glob that is not
// "<server>__<prefix>*" over a known server: a bare "*" or a prefix
// spanning servers would load every schema at once.
const ReasonInvalidGlob = "invalid glob: use <server>__<prefix>* for a server in the list"

// TurnView reports whether the calling turn's next model call will carry
// a tool's schema. The chat runner attaches its turn's view to a tool
// call's context (WithTurnView) so Load can say when a loaded tool only
// arrives on the next turn.
type TurnView func(ctx context.Context, name string) bool

type turnViewKey struct{}

// WithTurnView attaches v to ctx.
func WithTurnView(ctx context.Context, v TurnView) context.Context {
	return context.WithValue(ctx, turnViewKey{}, v)
}

func turnViewFrom(ctx context.Context) TurnView {
	v, _ := ctx.Value(turnViewKey{}).(TurnView)
	return v
}

// TurnBudget is the calling turn's schema budget. Load calls it with the
// tools it just loaded: the turn keeps them ahead of older activations
// for the rest of the turn, and the returned map gives, for each loaded
// tool the turn's next call still cannot carry within the budget, the
// reason (absent: it fits).
type TurnBudget func(ctx context.Context, loaded []string) map[string]string

type turnBudgetKey struct{}

// WithTurnBudget attaches b to ctx.
func WithTurnBudget(ctx context.Context, b TurnBudget) context.Context {
	return context.WithValue(ctx, turnBudgetKey{}, b)
}

func turnBudgetFrom(ctx context.Context) TurnBudget {
	b, _ := ctx.Value(turnBudgetKey{}).(TurnBudget)
	return b
}

// ReasonOverBudget is the NotLoaded reason prefix for a loaded tool the
// schema budget leaves out of the call; the tool stays activated and is
// sent once there is room.
const ReasonOverBudget = "over the schema budget"

// ReasonPinnedOverBudget is the NotLoaded reason prefix for a full-tier
// (pinned) tool the schema budget leaves out of the call.
const ReasonPinnedOverBudget = "pinned but over the schema budget"

// ReasonUnknown is the NotLoaded reason for a name that matches no
// installed server or tool.
const ReasonUnknown = "unknown"

// ErrEmptyRequest is returned when a request names nothing.
var ErrEmptyRequest = errors.New("loadtools: name at least one server or tool")

// Load resolves the session's catalog, activates every requested summary
// tool, and reports what was and was not loaded. by is one of the
// audit.ToolsActivatedBy* values.
func (s *Service) Load(ctx context.Context, sessionID string, req Request, by string) (Result, error) {
	res, _, err := s.load(ctx, sessionID, req, by)
	return res, err
}

// AutoActivate activates one tool the model called by its exact name
// while it was summary tier and not activated. It reports whether the
// tool is activated now; a tool that is full, off, already activated or
// not in the catalog is left alone and reported false.
//
// Ruling 2026-10-09 (owner alec, WP03 review): the harness does not
// retry the call itself. It activates the tool and answers the call with
// a not_loaded error; the model re-calls with the schema in view. That
// costs one model call and never runs a call the model built without the
// schema. These rows audit as by "auto", an addition to the spec's
// model|user enum.
func (s *Service) AutoActivate(ctx context.Context, sessionID, name string) (bool, error) {
	_, n, err := s.load(ctx, sessionID, Request{Tools: []string{name}}, audit.ToolsActivatedByAuto)
	return n > 0, err
}

func (s *Service) load(ctx context.Context, sessionID string, req Request, by string) (Result, int, error) {
	if len(req.Servers) == 0 && len(req.Tools) == 0 {
		return Result{}, 0, ErrEmptyRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rc, servers, err := s.resolve(ctx, sessionID)
	if err != nil {
		return Result{}, 0, err
	}
	var (
		loaded    = map[string]bool{}
		activate  = map[string]bool{}
		notLoaded []NotLoaded
		seenNL    = map[string]bool{}
	)
	refuse := func(name, reason string) {
		if !seenNL[name] {
			seenNL[name] = true
			notLoaded = append(notLoaded, NotLoaded{Name: name, Reason: reason})
		}
	}
	// take classifies tools matched by one requested name. It returns
	// false when nothing matched was loadable, so the caller can report
	// the name itself.
	take := func(matched []toolexposure.ResolvedTool, perTool bool) (anyLoaded bool, offReason string) {
		for _, t := range matched {
			switch t.Tier {
			case toolexposure.TierFull:
				loaded[t.Name] = true
				anyLoaded = true
			case toolexposure.TierSummary:
				loaded[t.Name] = true
				activate[t.Name] = true
				anyLoaded = true
			case toolexposure.TierOff:
				if offReason == "" {
					offReason = OffReason(t.Source)
				}
				if perTool {
					refuse(t.Name, OffReason(t.Source))
				}
			}
		}
		return anyLoaded, offReason
	}

	// takeAll takes every tool a server name or glob matched: when some
	// are loadable, each off tool is refused by name; when none is, the
	// requested name itself is refused with the off reason.
	takeAll := func(name string, matched []toolexposure.ResolvedTool) bool {
		someLoadable := false
		for _, t := range matched {
			if t.Tier != toolexposure.TierOff {
				someLoadable = true
				break
			}
		}
		if _, off := take(matched, someLoadable); !someLoadable {
			refuse(name, off)
			return false
		}
		return true
	}
	catalogServers := map[string]bool{}
	for _, t := range rc.Tools {
		if t.Running && !t.Probe {
			catalogServers[t.Server] = true
		}
	}

	for _, raw := range req.Servers {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		matched := toolsOf(rc, func(t toolexposure.ResolvedTool) bool { return t.Server == name })
		if len(matched) == 0 {
			refuse(name, notServedReason(name, servers))
			continue
		}
		takeAll(name, matched)
	}
	for _, raw := range req.Tools {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if prefix, ok := strings.CutSuffix(name, "*"); ok {
			server := serverOf(prefix)
			if !strings.Contains(prefix, toolexposure.NameSeparator) || server == "" || strings.Contains(prefix, "*") {
				refuse(name, ReasonInvalidGlob)
				continue
			}
			if !catalogServers[server] {
				if si, known := servers[server]; known && !si.Running {
					refuse(name, notServedReason(server, servers))
				} else {
					refuse(name, ReasonInvalidGlob)
				}
				continue
			}
			matched := toolsOf(rc, func(t toolexposure.ResolvedTool) bool { return strings.HasPrefix(t.Name, prefix) })
			if len(matched) == 0 {
				refuse(name, ReasonUnknown)
				continue
			}
			takeAll(name, matched)
			continue
		}
		t, found := rc.Tool(name)
		if !found || !t.Running || t.Probe {
			refuse(name, notServedReason(serverOf(name), servers))
			continue
		}
		take([]toolexposure.ResolvedTool{t}, true)
	}

	changed, count := s.applyActivations(ctx, sessionID, rc, activate, req.Sticky)
	if changed != nil {
		if err := s.d.Activations.SetToolActivations(ctx, sessionID, changed); err != nil {
			return Result{}, 0, fmt.Errorf("loadtools: save activations: %w", err)
		}
	}
	if count > 0 && s.d.Audit != nil {
		audit.MustEmit(ctx, s.d.Audit, audit.KindToolsActivated, audit.ToolsActivatedPayload{
			SessionID: sessionID,
			Servers:   serversOf(activate, rc),
			ToolCount: count,
			Sticky:    req.Sticky,
			By:        by,
		}, s.d.Now())
	}

	overBudget := 0
	if fit := turnBudgetFrom(ctx); fit != nil && len(loaded) > 0 {
		for name, reason := range fit(ctx, sortedKeys(loaded)) {
			if !loaded[name] {
				continue
			}
			delete(loaded, name)
			refuse(name, reason)
			overBudget++
		}
		sort.SliceStable(notLoaded, func(i, j int) bool { return notLoaded[i].Name < notLoaded[j].Name })
	}

	names := sortedKeys(loaded)
	res := Result{Loaded: []string{}, LoadedByServer: map[string]int{}, NotLoaded: notLoaded}
	for _, n := range names {
		t, _ := rc.Tool(n)
		res.LoadedByServer[t.Server]++
	}
	if len(names) <= MaxLoadedNames {
		res.Loaded = names
	}
	if view := turnViewFrom(ctx); view != nil {
		for _, n := range names {
			if !view(ctx, n) {
				res.NextTurn = append(res.NextTurn, n)
			}
		}
	}
	if res.NotLoaded == nil {
		res.NotLoaded = []NotLoaded{}
	}
	res.Summary = summarise(res, len(names), count)
	if overBudget > 0 {
		res.Summary += fmt.Sprintf("; %d loaded but over the schema budget — unload others or pick a larger model", overBudget)
	}
	return res, count, nil
}

// CheckLayerWrite implements toolexposure.WriteGuard: it refuses a layer
// that sets kenaz__load_tools off or summary while any other tool would
// resolve summary under it (spec FR-E3) — those tools would have no way
// to be loaded. A layer a higher layer overrides to full is allowed: it
// changes nothing.
//
// It checks the layer being written only (2026-10-09, owner alec): a
// stored user-layer load_tools=off followed by a project write that
// makes tools summary is not refused at the project write. The
// resolver's invariant still forces load_tools full in that state, so
// nothing becomes unreachable; only the write-time message is skipped.
func (s *Service) CheckLayerWrite(ctx context.Context, w toolexposure.LayerWrite) error {
	bare := strings.TrimPrefix(Name, toolexposure.BuiltinServer+toolexposure.NameSeparator)
	tier := w.Exposure.TierFor(toolexposure.BuiltinServer, bare)
	if tier != toolexposure.TierOff && tier != toolexposure.TierSummary {
		return nil
	}
	entries, err := s.d.Catalog.Catalog(ctx, w.SessionID)
	if err != nil {
		return fmt.Errorf("loadtools: catalog: %w", err)
	}
	r, err := toolexposure.NewResolver(s.d.Resolver.Deps().WithLayer(w))
	if err != nil {
		return err
	}
	rc, err := ResolveCatalog(ctx, r, w.SessionID, entries, s.servers(ctx))
	if err != nil {
		return err
	}
	if lt, ok := rc.Tool(Name); ok && lt.Source != toolexposure.LevelInvariant {
		return nil
	}
	var summary []string
	for _, t := range rc.Tools {
		if t.Tier != toolexposure.TierSummary || t.Name == Name {
			continue
		}
		if t.Probe {
			summary = append(summary, t.Server+" (stopped)")
			continue
		}
		summary = append(summary, t.Name)
	}
	if len(summary) == 0 {
		return nil
	}
	sort.Strings(summary)
	ex := summary
	if len(ex) > 3 {
		ex = ex[:3]
	}
	return fmt.Errorf("%w: %d tool(s) are in the summary tier (e.g. %s) and could no longer be loaded with %s %s; set them full or off first",
		toolexposure.ErrLoadToolsRequired, len(summary), strings.Join(ex, ", "), Name, tier)
}

var _ toolexposure.WriteGuard = (*Service)(nil)

// applyActivations merges the requested activations into the session's
// set. It returns the new set when anything changed (nil otherwise) and
// how many tools were newly activated or newly made sticky. A tool
// already activated has its LastUsedTurn refreshed; a sticky activation
// is never made non-sticky by a later non-sticky load.
func (s *Service) applyActivations(ctx context.Context, sessionID string, rc toolexposure.ResolvedCatalog, activate map[string]bool, sticky bool) ([]toolexposure.Activation, int) {
	if len(activate) == 0 {
		return nil, 0
	}
	// An unreadable turn count keeps an existing activation's stamp (a
	// stamp of 0 would expire it at the next turn); a new activation is
	// stamped 0.
	turn, turnKnown := 0, true
	if s.d.Turns != nil {
		n, err := s.d.Turns.TurnOrdinal(ctx, sessionID)
		if err != nil {
			turnKnown = false
		} else {
			turn = n
		}
	}
	out := append([]toolexposure.Activation(nil), rc.Activations...)
	idx := make(map[string]int, len(out))
	for i, a := range out {
		idx[a.Name] = i
	}
	count := 0
	for _, name := range sortedKeys(activate) {
		t, _ := rc.Tool(name)
		if i, ok := idx[name]; ok {
			if sticky && !out[i].Sticky {
				out[i].Sticky = true
				count++
			}
			if turnKnown {
				out[i].LastUsedTurn = turn
			}
			continue
		}
		out = append(out, toolexposure.Activation{Name: name, Server: t.Server, LastUsedTurn: turn, Sticky: sticky})
		count++
	}
	return out, count
}

// TurnOrdinal returns the session's current turn ordinal, or 0 when no
// turn counter is wired.
func (s *Service) TurnOrdinal(ctx context.Context, sessionID string) (int, error) {
	if s.d.Turns == nil {
		return 0, nil
	}
	return s.d.Turns.TurnOrdinal(ctx, sessionID)
}

// ExpireActivations removes the session's non-sticky activations that
// have gone more than ttl turns unused as of turn
// (toolexposure.ActivationExpired) and returns them. Nothing is written
// when nothing expired.
func (s *Service) ExpireActivations(ctx context.Context, sessionID string, turn, ttl int) ([]toolexposure.Activation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Resolver.Deps().Sessions.SessionToolExposure(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("loadtools: read activations: %w", err)
	}
	kept, expired := toolexposure.ExpireActivations(st.Activations, turn, ttl)
	if len(expired) == 0 {
		return nil, nil
	}
	if err := s.d.Activations.SetToolActivations(ctx, sessionID, kept); err != nil {
		return nil, fmt.Errorf("loadtools: save activations: %w", err)
	}
	return expired, nil
}

// MarkUsed records that the model called name on turn: an activation of
// that name has its LastUsedTurn set to turn, which restarts its TTL and
// moves it to the head of the activated segment. It reports whether an
// activation was updated; a name with no activation (a full-tier tool)
// writes nothing.
func (s *Service) MarkUsed(ctx context.Context, sessionID, name string, turn int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Resolver.Deps().Sessions.SessionToolExposure(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("loadtools: read activations: %w", err)
	}
	for i, a := range st.Activations {
		if a.Name != name {
			continue
		}
		if a.LastUsedTurn == turn {
			return false, nil
		}
		out := append([]toolexposure.Activation(nil), st.Activations...)
		out[i].LastUsedTurn = turn
		if err := s.d.Activations.SetToolActivations(ctx, sessionID, out); err != nil {
			return false, fmt.Errorf("loadtools: save activations: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// RecordEviction emits KindToolsEvicted for one fitted call when its
// evicted set differs from the last one audited for the session, so an
// overage that persists across calls and turns writes one row. A fit
// that evicted nothing records nothing and clears the session's last
// set, so a later recurrence is audited again.
func (s *Service) RecordEviction(ctx context.Context, sessionID string, fit toolexposure.BudgetFit) {
	names := make([]string, 0, len(fit.Evicted))
	servers := map[string]bool{}
	for _, t := range fit.Evicted {
		names = append(names, t.Name)
		servers[t.Server] = true
	}
	sort.Strings(names)
	key := strings.Join(names, ",")
	s.evictMu.Lock()
	if s.lastEvicted == nil {
		s.lastEvicted = map[string]string{}
	}
	same := s.lastEvicted[sessionID] == key
	if key == "" {
		delete(s.lastEvicted, sessionID)
	} else {
		s.lastEvicted[sessionID] = key
	}
	s.evictMu.Unlock()
	if same || key == "" || s.d.Audit == nil {
		return
	}
	audit.MustEmit(ctx, s.d.Audit, audit.KindToolsEvicted, audit.ToolsEvictedPayload{
		SessionID:   sessionID,
		Servers:     sortedKeys(servers),
		ToolCount:   len(fit.Evicted),
		PinnedCount: fit.PinnedEvicted,
		Budget:      fit.Budget,
		OverBy:      fit.OverBy,
	}, s.d.Now())
}

// resolve lists the session's catalog plus one probe per installed
// server that is not running, and resolves it.
func (s *Service) resolve(ctx context.Context, sessionID string) (toolexposure.ResolvedCatalog, map[string]ServerInfo, error) {
	entries, err := s.d.Catalog.Catalog(ctx, sessionID)
	if err != nil {
		return toolexposure.ResolvedCatalog{}, nil, fmt.Errorf("loadtools: catalog: %w", err)
	}
	servers := s.servers(ctx)
	rc, err := ResolveCatalog(ctx, s.d.Resolver, sessionID, entries, servers)
	if err != nil {
		return toolexposure.ResolvedCatalog{}, nil, err
	}
	return rc, servers, nil
}

// ResolveCatalog resolves a session's catalog entries plus one probe per
// installed server that is not running — the one resolution the load
// core and the request builder share.
func ResolveCatalog(ctx context.Context, r *toolexposure.Resolver, sessionID string, entries []CatalogEntry, servers map[string]ServerInfo) (toolexposure.ResolvedCatalog, error) {
	return r.Resolve(ctx, sessionID, CatalogWithProbes(entries, servers))
}

// servers returns the directory keyed by server name.
func (s *Service) servers(ctx context.Context) map[string]ServerInfo {
	out := map[string]ServerInfo{}
	if s.d.Servers == nil {
		return out
	}
	for _, si := range s.d.Servers.Servers(ctx) {
		out[si.Name] = si
	}
	return out
}

// Servers exposes the directory to the request builder, keyed by name.
func (s *Service) Servers(ctx context.Context) map[string]ServerInfo { return s.servers(ctx) }

// Resolver returns the resolver the service loads against.
func (s *Service) Resolver() *toolexposure.Resolver { return s.d.Resolver }

// CatalogWithProbes converts catalog entries to resolver input (every
// entry running) and appends one toolexposure.ServerProbe entry per
// installed server that is not running and has no catalog entries.
func CatalogWithProbes(entries []CatalogEntry, servers map[string]ServerInfo) []toolexposure.CatalogTool {
	out := make([]toolexposure.CatalogTool, 0, len(entries)+len(servers))
	present := map[string]bool{}
	for _, e := range entries {
		out = append(out, toolexposure.CatalogTool{Name: e.Name, Server: e.Server, Running: true})
		present[e.Server] = true
	}
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if si := servers[n]; !si.Running && !present[n] {
			out = append(out, toolexposure.ServerProbe(n))
		}
	}
	return out
}

// Purposes projects the directory onto server name -> purpose.
func Purposes(servers map[string]ServerInfo) map[string]string {
	out := make(map[string]string, len(servers))
	for n, si := range servers {
		if si.Purpose != "" {
			out[n] = si.Purpose
		}
	}
	return out
}

// OffReason names the setting that turned a tool off.
func OffReason(source toolexposure.Level) string {
	switch source {
	case toolexposure.LevelOrgPin:
		return "off — set by your organisation"
	case toolexposure.LevelSession:
		return "off — turned off for this session"
	case toolexposure.LevelProject:
		return "off — turned off for this project"
	case toolexposure.LevelUser:
		return "off — turned off in Settings → Capabilities"
	}
	return "off"
}

// notServedReason explains why a name the catalog does not hold cannot
// be loaded: its server is installed but not running (with the live
// state), or nothing by that name is installed.
func notServedReason(server string, servers map[string]ServerInfo) string {
	if si, ok := servers[server]; ok && !si.Running {
		state := si.State
		if state == "" {
			state = "stopped"
		}
		return fmt.Sprintf("server %s is not running (state: %s)", server, state)
	}
	return ReasonUnknown
}

func serverOf(name string) string {
	if i := strings.Index(name, toolexposure.NameSeparator); i > 0 {
		return name[:i]
	}
	return name
}

func toolsOf(rc toolexposure.ResolvedCatalog, keep func(toolexposure.ResolvedTool) bool) []toolexposure.ResolvedTool {
	var out []toolexposure.ResolvedTool
	for _, t := range rc.Tools {
		if t.Running && !t.Probe && keep(t) {
			out = append(out, t)
		}
	}
	return out
}

func serversOf(names map[string]bool, rc toolexposure.ResolvedCatalog) []string {
	set := map[string]bool{}
	for n := range names {
		if t, ok := rc.Tool(n); ok {
			set[t.Server] = true
		}
	}
	return sortedKeys(set)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func summarise(r Result, loaded, activated int) string {
	var b strings.Builder
	if loaded == 0 {
		b.WriteString("loaded no tools")
	} else {
		servers := make([]string, 0, len(r.LoadedByServer))
		for sv := range r.LoadedByServer {
			servers = append(servers, sv)
		}
		sort.Strings(servers)
		parts := make([]string, 0, len(servers))
		for _, sv := range servers {
			n := r.LoadedByServer[sv]
			if n == 1 {
				parts = append(parts, "1 tool from "+sv)
			} else {
				parts = append(parts, fmt.Sprintf("%d tools from %s", n, sv))
			}
		}
		b.WriteString("loaded ")
		b.WriteString(strings.Join(parts, ", "))
		if activated == 0 {
			b.WriteString(" (already loaded)")
		}
		switch n := len(r.NextTurn); {
		case n == loaded:
			b.WriteString("; available from your next turn")
		case n > 0:
			fmt.Fprintf(&b, "; definitions are sent on your next call, except %d available from your next turn (see next_turn)", n)
		default:
			b.WriteString("; definitions are sent on your next call")
		}
	}
	if n := len(r.NotLoaded); n > 0 {
		fmt.Fprintf(&b, "; %d could not be loaded (see not_loaded)", n)
	}
	return b.String()
}

// Tool is the kenaz__load_tools built-in.
type Tool struct {
	svc *Service
}

// New returns the tool over svc.
func New(svc *Service) *Tool { return &Tool{svc: svc} }

// Name implements toolloop.BuiltinTool.
func (t *Tool) Name() string { return Name }

// Description implements toolloop.BuiltinTool. It is static: the list
// of unloaded capabilities is the digest, a per-call system section.
func (t *Tool) Description() string { return staticDescription }

var inputSchema = json.RawMessage(`{"type":"object","properties":{` +
	`"servers":{"type":"array","items":{"type":"string"},"description":"Server names from the list in this tool's description; loads every tool of each."},` +
	`"tools":{"type":"array","items":{"type":"string"},"description":"Exact tool names (\"server__tool\") or prefix globs (\"server__prefix*\")."},` +
	`"sticky":{"type":"boolean","description":"Keep these tools loaded for the rest of the session."}` +
	`},"additionalProperties":false}`)

// InputSchema implements toolloop.BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage { return inputSchema }

// Call implements toolloop.BuiltinTool.
func (t *Tool) Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t == nil || t.svc == nil {
		return nil, ErrMissingDep
	}
	sessionID := toolloop.SessionIDFromContext(ctx)
	if sessionID == "" {
		return nil, errors.New("loadtools: no session in context")
	}
	var req Request
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := dec.Decode(&req); err != nil {
			return nil, fmt.Errorf("loadtools: invalid arguments: %w", err)
		}
	}
	res, err := t.svc.Load(ctx, sessionID, req, audit.ToolsActivatedByModel)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}
