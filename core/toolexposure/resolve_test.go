package toolexposure

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeCatalog []CatalogTool

func (f fakeCatalog) Catalog(context.Context, string) ([]CatalogTool, error) {
	return append([]CatalogTool(nil), f...), nil
}

type fakeSettings struct {
	s   Settings
	err error
}

func (f fakeSettings) GetToolExposure(context.Context) (Settings, error) { return f.s, f.err }

type fakeSessions struct{ st SessionState }

func (f fakeSessions) SessionToolExposure(context.Context, string) (SessionState, error) {
	return f.st, nil
}

type fakeProjects struct {
	byID map[string]Exposure
	seen *[]string
}

func (f fakeProjects) ProjectToolExposure(_ context.Context, id string) (Exposure, error) {
	if f.seen != nil {
		*f.seen = append(*f.seen, id)
	}
	return f.byID[id], nil
}

type fakePins struct{ e Exposure }

func (f fakePins) ToolExposurePins(context.Context) (Exposure, error) { return f.e, nil }

func srv(tier Tier) Exposure {
	return Exposure{Servers: map[string]ServerExposure{"outlook": {Tier: tier}}}
}

func toolLevel(tier Tier) Exposure {
	return Exposure{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{"send-mail": tier}}}}
}

var sendMail = CatalogTool{Name: "outlook__send-mail", Server: "outlook", Running: true}

// layerSet places an Exposure at each named level; LevelDefault is
// never set (it is what remains when nothing else has an opinion).
type layerSet map[Level]Exposure

func depsFor(cat []CatalogTool, ls layerSet) Deps {
	return Deps{
		Catalog:  fakeCatalog(cat),
		Settings: fakeSettings{s: Settings{Exposure: ls[LevelUser]}},
		Sessions: fakeSessions{st: SessionState{ProjectID: "p1", Override: ls[LevelSession]}},
		Projects: fakeProjects{byID: map[string]Exposure{"p1": ls[LevelProject]}},
		Pins:     fakePins{e: ls[LevelOrgPin]},
	}
}

func resolveOne(t *testing.T, cat []CatalogTool, ls layerSet, name string) ResolvedTool {
	t.Helper()
	rc, err := Resolve(context.Background(), depsFor(cat, ls), "s1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rt, ok := rc.Tool(name)
	if !ok {
		t.Fatalf("%s missing from resolved catalog %+v", name, rc.Tools)
	}
	return rt
}

// TestResolve_PrecedenceEveryPair covers every pair of §2.1 levels: the
// higher level is set to one tier, the lower to a different one, and
// the higher must win and be named as the source. Each pair is run with
// server-wide and tool-level entries at both ends, so "first layer
// wins" is proven independent of how specific each layer's entry is.
func TestResolve_PrecedenceEveryPair(t *testing.T) {
	order := []Level{LevelOrgPin, LevelSession, LevelProject, LevelUser, LevelDefault}
	shapes := []struct {
		name   string
		hi, lo func(Tier) Exposure
	}{
		{"server/server", srv, srv},
		{"tool/tool", toolLevel, toolLevel},
		{"server-over-tool", srv, toolLevel},
		{"tool-over-server", toolLevel, srv},
	}
	pairs := 0
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			hi, lo := order[i], order[j]
			pairs++
			for _, sh := range shapes {
				// The default for an MCP tool is summary, so the higher
				// level picks a tier that differs from whatever the lower
				// level yields.
				loTier, hiTier := TierOff, TierFull
				if lo == LevelDefault {
					loTier = DefaultTier(sendMail)
				}
				t.Run(fmt.Sprintf("%s>%s/%s", hi, lo, sh.name), func(t *testing.T) {
					ls := layerSet{hi: sh.hi(hiTier)}
					if lo != LevelDefault {
						ls[lo] = sh.lo(loTier)
					}
					got := resolveOne(t, []CatalogTool{sendMail}, ls, sendMail.Name)
					if got.Tier != hiTier || got.Source != hi {
						t.Fatalf("got tier %q from %q, want %q from %q (lower level %q had %q)",
							got.Tier, got.Source, hiTier, hi, lo, loTier)
					}
				})
			}
		}
	}
	if pairs != 10 {
		t.Fatalf("covered %d level pairs, want all 10", pairs)
	}
}

// Inside one layer a tool entry beats its server's tier; a layer with
// only another tool's entry has no opinion about this one.
func TestResolve_ToolEntryBeatsServerTierWithinLayer(t *testing.T) {
	ls := layerSet{LevelUser: {Servers: map[string]ServerExposure{"outlook": {
		Tier:  TierOff,
		Tools: map[string]Tier{"send-mail": TierFull},
	}}}}
	other := CatalogTool{Name: "outlook__list-messages", Server: "outlook", Running: true}
	rc, err := Resolve(context.Background(), depsFor([]CatalogTool{sendMail, other}, ls), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := rc.Tool(sendMail.Name); got.Tier != TierFull || got.Source != LevelUser {
		t.Fatalf("send-mail = %+v, want full from user", got)
	}
	if got, _ := rc.Tool(other.Name); got.Tier != TierOff || got.Source != LevelUser {
		t.Fatalf("list-messages = %+v, want off from user (server tier)", got)
	}

	onlyOther := layerSet{LevelSession: toolLevel(TierOff), LevelUser: srv(TierFull)}
	if got := resolveOne(t, []CatalogTool{other}, onlyOther, other.Name); got.Tier != TierFull || got.Source != LevelUser {
		t.Fatalf("list-messages = %+v, want full from user: the session entry names a different tool", got)
	}
}

func TestResolve_HarnessDefaults(t *testing.T) {
	cat := []CatalogTool{
		{Name: "kenaz__read_file", Server: BuiltinServer, Running: true},
		{Name: "kenaz__bash", Server: BuiltinServer, Running: true},
		{Name: "kenaz__save_document", Server: BuiltinServer, Running: true},
		{Name: "harness-self__list_sessions", Server: "harness-self", Running: true},
		{Name: "fetch__fetch", Server: "fetch", Running: false},
		// A non-built-in that happens to share a hot-set bare name is
		// not hot: the hot set is the kenaz server's.
		{Name: "filesystem__read_file", Server: "filesystem", Running: true},
	}
	rc, err := Resolve(context.Background(), depsFor(cat, layerSet{}), "s1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Tier{
		"kenaz__read_file":            TierFull,
		"kenaz__bash":                 TierFull,
		"kenaz__save_document":        TierSummary,
		"harness-self__list_sessions": TierSummary,
		"fetch__fetch":                TierSummary,
		"filesystem__read_file":       TierSummary,
	}
	for _, rt := range rc.Tools {
		if rt.Tier != want[rt.Name] || rt.Source != LevelDefault {
			t.Errorf("%s = %q from %q, want %q from default", rt.Name, rt.Tier, rt.Source, want[rt.Name])
		}
		if rt.Tier == TierOff {
			t.Errorf("%s is off by default; nothing is off by default", rt.Name)
		}
	}
	if f, _ := rc.Tool("fetch__fetch"); f.Running {
		t.Error("fetch__fetch: Running must carry through from the catalog")
	}
	if rc.SchemaBudgetTokens != DefaultSchemaBudgetTokens || rc.ActivationTTLTurns != DefaultActivationTTLTurns {
		t.Errorf("budget/ttl = %d/%d, want defaults %d/%d", rc.SchemaBudgetTokens, rc.ActivationTTLTurns,
			DefaultSchemaBudgetTokens, DefaultActivationTTLTurns)
	}
}

func TestHotSet_MatchesSpec(t *testing.T) {
	want := []string{
		"kenaz__ask_user_question", "kenaz__bash", "kenaz__edit_file", "kenaz__fork_conversation",
		"kenaz__glob", "kenaz__grep", "kenaz__list_dir", "kenaz__load_tools", "kenaz__read_file",
		"kenaz__save_artifact", "kenaz__skill", "kenaz__todo_write", "kenaz__web_fetch",
		"kenaz__web_search", "kenaz__write_file",
	}
	got := HotSet()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("HotSet() = %v\nwant       %v", got, want)
	}
}

// TestResolve_LoadToolsInvariant: kenaz__load_tools is full whenever any
// tool is summary — even when the user, the project or an org pin says
// off — and is left to its layers when nothing is summary.
func TestResolve_LoadToolsInvariant(t *testing.T) {
	loadTools := CatalogTool{Name: LoadToolsName, Server: BuiltinServer, Running: true}
	offLoad := func() Exposure {
		return Exposure{Servers: map[string]ServerExposure{BuiltinServer: {Tools: map[string]Tier{"load_tools": TierOff}}}}
	}
	for _, lvl := range []Level{LevelOrgPin, LevelSession, LevelProject, LevelUser} {
		t.Run(string(lvl)+"/summary-present", func(t *testing.T) {
			got := resolveOne(t, []CatalogTool{loadTools, sendMail}, layerSet{lvl: offLoad()}, LoadToolsName)
			if got.Tier != TierFull || got.Source != LevelInvariant {
				t.Fatalf("load_tools = %+v, want full from invariant while send-mail is summary", got)
			}
		})
		t.Run(string(lvl)+"/no-summary", func(t *testing.T) {
			ls := layerSet{lvl: offLoad()}
			ls[LevelUser] = mergeServers(ls[LevelUser], srv(TierFull))
			got := resolveOne(t, []CatalogTool{loadTools, sendMail}, ls, LoadToolsName)
			if got.Tier != TierOff || got.Source != lvl {
				t.Fatalf("load_tools = %+v, want off from %s when no tool is summary", got, lvl)
			}
		})
	}
	// Default posture: load_tools is hot, so full from the default.
	got := resolveOne(t, []CatalogTool{loadTools, sendMail}, layerSet{}, LoadToolsName)
	if got.Tier != TierFull || got.Source != LevelDefault {
		t.Fatalf("load_tools = %+v, want full from default", got)
	}
}

func mergeServers(a, b Exposure) Exposure {
	out := a.Clone()
	if out.Servers == nil {
		out.Servers = map[string]ServerExposure{}
	}
	for k, v := range b.Servers {
		out.Servers[k] = v
	}
	return out
}

func TestResolve_ProjectLayerOnlyForProjectSessions(t *testing.T) {
	var seen []string
	deps := Deps{
		Catalog:  fakeCatalog{sendMail},
		Settings: fakeSettings{},
		Sessions: fakeSessions{st: SessionState{}},
		Projects: fakeProjects{byID: map[string]Exposure{"": srv(TierOff)}, seen: &seen},
	}
	rc, err := Resolve(context.Background(), deps, "loose")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("project layer read for a loose session: %v", seen)
	}
	if got, _ := rc.Tool(sendMail.Name); got.Tier != TierSummary {
		t.Fatalf("send-mail = %+v, want summary", got)
	}
}

func TestResolve_CarriesActivationsBudgetAndTTL(t *testing.T) {
	acts := []Activation{{Name: "outlook__send-mail", Server: "outlook", LastUsedTurn: 3, Sticky: true}}
	deps := Deps{
		Catalog:  fakeCatalog{sendMail},
		Settings: fakeSettings{s: Settings{SchemaBudgetTokens: 9000, ActivationTTLTurns: 2}},
		Sessions: fakeSessions{st: SessionState{Activations: acts}},
		Projects: fakeProjects{},
	}
	rc, err := Resolve(context.Background(), deps, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if rc.SchemaBudgetTokens != 9000 || rc.ActivationTTLTurns != 2 {
		t.Fatalf("budget/ttl = %d/%d, want 9000/2", rc.SchemaBudgetTokens, rc.ActivationTTLTurns)
	}
	if len(rc.Activations) != 1 || rc.Activations[0] != acts[0] {
		t.Fatalf("activations = %+v, want %+v", rc.Activations, acts)
	}
}

func TestResolve_ErrorsAreNotSwallowed(t *testing.T) {
	if _, err := Resolve(context.Background(), Deps{}, "s1"); !errors.Is(err, ErrMissingDep) {
		t.Fatalf("empty deps: err = %v, want ErrMissingDep", err)
	}
	boom := errors.New("boom")
	deps := Deps{Catalog: fakeCatalog{}, Settings: fakeSettings{err: boom}, Sessions: fakeSessions{}, Projects: fakeProjects{}}
	if _, err := Resolve(context.Background(), deps, "s1"); !errors.Is(err, boom) {
		t.Fatalf("settings error: err = %v, want it wrapped", err)
	}
}

func TestValidate(t *testing.T) {
	bad := []Exposure{
		{Servers: map[string]ServerExposure{"outlook": {Tier: "hidden"}}},
		{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{"send-mail": ""}}}},
		{Servers: map[string]ServerExposure{"": {Tier: TierOff}}},
		{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{" ": TierOff}}}},
	}
	for i, e := range bad {
		if err := e.Validate(); err == nil {
			t.Errorf("bad[%d] %+v validated", i, e)
		}
	}
	if err := (Exposure{}).Validate(); err != nil {
		t.Errorf("zero exposure: %v", err)
	}
	if err := (Settings{SchemaBudgetTokens: -1}).Validate(); err == nil {
		t.Error("negative budget validated")
	}
	if err := (Settings{ActivationTTLTurns: MaxActivationTTLTurns + 1}).Validate(); err == nil {
		t.Error("over-max TTL validated")
	}
	if err := ValidateActivations([]Activation{{Name: "a__b", Server: "a"}, {Name: "a__b", Server: "a"}}); err == nil {
		t.Error("duplicate activation validated")
	}
	if err := ValidateActivations([]Activation{{Name: "a__b"}}); err == nil {
		t.Error("activation without a server validated")
	}
}

func TestExposure_CloneDoesNotAlias(t *testing.T) {
	orig := toolLevel(TierOff)
	c := orig.Clone()
	c.Servers["outlook"].Tools["send-mail"] = TierFull
	if orig.Servers["outlook"].Tools["send-mail"] != TierOff {
		t.Fatal("Clone aliased the tools map")
	}
	if !(Exposure{}).IsZero() || orig.IsZero() {
		t.Fatal("IsZero wrong")
	}
}
