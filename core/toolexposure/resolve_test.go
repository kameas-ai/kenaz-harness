package toolexposure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

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

type fakePins struct{ p OrgPolicy }

func (f fakePins) ToolExposurePolicy(context.Context) (OrgPolicy, error) { return f.p, nil }

func srv(tier Tier) Exposure {
	return Exposure{Servers: map[string]ServerExposure{"outlook": {Tier: tier}}}
}

func toolLevel(tier Tier) Exposure {
	return Exposure{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{"send-mail": tier}}}}
}

var sendMail = CatalogTool{Name: "outlook__send-mail", Server: "outlook", Running: true}

var allTiers = []Tier{TierFull, TierSummary, TierOff}

// layerSet places an Exposure at each named level; LevelDefault is
// never set (it is what remains when nothing else has an opinion).
type layerSet map[Level]Exposure

func depsFor(ls layerSet) Deps {
	return Deps{
		Settings: fakeSettings{s: Settings{Exposure: ls[LevelUser]}},
		Sessions: fakeSessions{st: SessionState{ProjectID: "p1", Override: ls[LevelSession]}},
		Projects: fakeProjects{byID: map[string]Exposure{"p1": ls[LevelProject]}},
		Pins:     fakePins{p: OrgPolicy{Pins: ls[LevelOrgPin], Defaults: ls[LevelOrgDefault]}},
	}
}

func resolveOne(t *testing.T, cat []CatalogTool, ls layerSet, name string) ResolvedTool {
	t.Helper()
	rc, err := Resolve(context.Background(), depsFor(ls), "s1", cat)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rt, ok := rc.Tool(name)
	if !ok {
		t.Fatalf("%s missing from resolved catalog %+v", name, rc.Tools)
	}
	return rt
}

// TestResolve_PrecedenceEveryPair covers every pair of §2.1 levels and,
// for each, every ordered pair of distinct tiers: the higher level must
// win whichever tier it holds, so no ordering of the tiers themselves
// (e.g. "most permissive wins") can pass. When the lower level is the
// harness default (summary for an MCP tool) the higher level takes each
// tier that differs from it. Each case runs with server-wide and
// tool-level entries at both ends. The organisation appears at both ends
// of the order: a pin (pinned:true) beats every layer, an org default
// (pinned:false) loses to session, project and user and beats only the
// harness default.
func TestResolve_PrecedenceEveryPair(t *testing.T) {
	order := []Level{LevelOrgPin, LevelSession, LevelProject, LevelUser, LevelOrgDefault, LevelDefault}
	shapes := []struct {
		name   string
		hi, lo func(Tier) Exposure
	}{
		{"server/server", srv, srv},
		{"tool/tool", toolLevel, toolLevel},
		{"server-over-tool", srv, toolLevel},
		{"tool-over-server", toolLevel, srv},
	}
	pairs, cases := 0, 0
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			hi, lo := order[i], order[j]
			pairs++
			for _, hiTier := range allTiers {
				for _, loTier := range allTiers {
					if lo == LevelDefault {
						loTier = DefaultTier(sendMail)
					}
					if hiTier == loTier {
						continue
					}
					for _, sh := range shapes {
						cases++
						t.Run(fmt.Sprintf("%s=%s>%s=%s/%s", hi, hiTier, lo, loTier, sh.name), func(t *testing.T) {
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
					if lo == LevelDefault {
						break
					}
				}
			}
		}
	}
	if pairs != 15 {
		t.Fatalf("covered %d level pairs, want all 15", pairs)
	}
	// 10 level pairs among the five set levels × 6 ordered tier pairs, plus
	// 5 pairs against the default × 2 tiers, each × 4 shapes.
	if want := (10*6 + 5*2) * 4; cases != want {
		t.Fatalf("ran %d cases, want %d", cases, want)
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
	rc, err := Resolve(context.Background(), depsFor(ls), "s1", []CatalogTool{sendMail, other})
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

// A tier value that is not one of the three (a hand-edited settings
// file, a row written before validation) is no opinion: resolution
// falls through to the next layer and never yields a fourth tier.
func TestResolve_InvalidStoredTierIsNoOpinion(t *testing.T) {
	ls := layerSet{
		LevelSession: {Servers: map[string]ServerExposure{"outlook": {Tier: "loud", Tools: map[string]Tier{"send-mail": "hidden"}}}},
		LevelUser:    srv(TierOff),
	}
	if got := resolveOne(t, []CatalogTool{sendMail}, ls, sendMail.Name); got.Tier != TierOff || got.Source != LevelUser {
		t.Fatalf("send-mail = %+v, want off from user past the invalid session entries", got)
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
	rc, err := Resolve(context.Background(), depsFor(layerSet{}), "s1", cat)
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
	if len(rc.Tools) != len(cat) {
		t.Fatalf("resolved %d tools, want %d", len(rc.Tools), len(cat))
	}
	for i, rt := range rc.Tools {
		if rt.Name != cat[i].Name {
			t.Errorf("Tools[%d] = %s, want catalog order (%s)", i, rt.Name, cat[i].Name)
		}
		if rt.Tier != want[rt.Name] || rt.Source != LevelDefault {
			t.Errorf("%s = %q from %q, want %q from default", rt.Name, rt.Tier, rt.Source, want[rt.Name])
		}
	}
	if f, _ := rc.Tool("fetch__fetch"); f.Running {
		t.Error("fetch__fetch: Running must carry through from the catalog")
	}
	if _, ok := rc.Tool("outlook__nope"); ok {
		t.Error("Tool found a name not in the catalog")
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
	if got := HotSet(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("HotSet() = %v\nwant       %v", got, want)
	}
}

// TestResolve_LoadToolsInvariant: kenaz__load_tools is full whenever any
// other tool is summary — even when the user, the project or an org pin
// says off — and is left to its layers when nothing else is summary. Its
// own tier never counts as "a summary tool exists".
func TestResolve_LoadToolsInvariant(t *testing.T) {
	loadTools := CatalogTool{Name: LoadToolsName, Server: BuiltinServer, Running: true}
	loadAt := func(tier Tier) Exposure {
		return Exposure{Servers: map[string]ServerExposure{BuiltinServer: {Tools: map[string]Tier{"load_tools": tier}}}}
	}
	for _, lvl := range []Level{LevelOrgPin, LevelSession, LevelProject, LevelUser} {
		t.Run(string(lvl)+"/summary-present", func(t *testing.T) {
			got := resolveOne(t, []CatalogTool{loadTools, sendMail}, layerSet{lvl: loadAt(TierOff)}, LoadToolsName)
			if got.Tier != TierFull || got.Source != LevelInvariant {
				t.Fatalf("load_tools = %+v, want full from invariant while send-mail is summary", got)
			}
		})
		t.Run(string(lvl)+"/no-summary", func(t *testing.T) {
			ls := layerSet{lvl: loadAt(TierOff)}
			ls[LevelUser] = mergeServers(ls[LevelUser], srv(TierFull))
			got := resolveOne(t, []CatalogTool{loadTools, sendMail}, ls, LoadToolsName)
			if got.Tier != TierOff || got.Source != lvl {
				t.Fatalf("load_tools = %+v, want off from %s when no tool is summary", got, lvl)
			}
		})
	}
	// load_tools itself at summary, nothing else summary: it stays summary.
	ls := layerSet{LevelUser: mergeServers(loadAt(TierSummary), srv(TierFull))}
	if got := resolveOne(t, []CatalogTool{loadTools, sendMail}, ls, LoadToolsName); got.Tier != TierSummary || got.Source != LevelUser {
		t.Fatalf("load_tools = %+v, want summary from user: its own tier must not trip the invariant", got)
	}
	// Default posture: load_tools is hot, so full from the default.
	if got := resolveOne(t, []CatalogTool{loadTools, sendMail}, layerSet{}, LoadToolsName); got.Tier != TierFull || got.Source != LevelDefault {
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
		Settings: fakeSettings{},
		Sessions: fakeSessions{st: SessionState{}},
		Projects: fakeProjects{byID: map[string]Exposure{"": srv(TierOff)}, seen: &seen},
	}
	rc, err := Resolve(context.Background(), deps, "loose", []CatalogTool{sendMail})
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
	r, err := NewResolver(Deps{
		Settings: fakeSettings{s: Settings{SchemaBudgetTokens: 9000, ActivationTTLTurns: 2}},
		Sessions: fakeSessions{st: SessionState{Activations: acts}},
		Projects: fakeProjects{},
	})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := r.Resolve(context.Background(), "s1", []CatalogTool{sendMail})
	if err != nil {
		t.Fatal(err)
	}
	if rc.SchemaBudgetTokens != 9000 || rc.ActivationTTLTurns != 2 {
		t.Fatalf("budget/ttl = %d/%d, want 9000/2", rc.SchemaBudgetTokens, rc.ActivationTTLTurns)
	}
	if !reflect.DeepEqual(rc.Activations, acts) {
		t.Fatalf("activations = %+v, want %+v", rc.Activations, acts)
	}
}

func TestResolve_ErrorsAreNotSwallowed(t *testing.T) {
	if _, err := NewResolver(Deps{Settings: fakeSettings{}, Sessions: fakeSessions{}}); !errors.Is(err, ErrMissingDep) {
		t.Fatalf("NewResolver without Projects: err = %v, want ErrMissingDep", err)
	}
	if _, err := Resolve(context.Background(), Deps{}, "s1", nil); !errors.Is(err, ErrMissingDep) {
		t.Fatalf("empty deps: err = %v, want ErrMissingDep", err)
	}
	boom := errors.New("boom")
	deps := Deps{Settings: fakeSettings{err: boom}, Sessions: fakeSessions{}, Projects: fakeProjects{}}
	if _, err := Resolve(context.Background(), deps, "s1", nil); !errors.Is(err, boom) {
		t.Fatalf("settings error: err = %v, want it wrapped", err)
	}
}

func TestValidate(t *testing.T) {
	bad := []Exposure{
		{Servers: map[string]ServerExposure{"outlook": {Tier: "hidden"}}},
		{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{"send-mail": ""}}}},
		{Servers: map[string]ServerExposure{"": {Tier: TierOff}}},
		{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{" ": TierOff}}}},
		{Servers: map[string]ServerExposure{"outlook": {Tools: map[string]Tier{"outlook__send-mail": TierOff}}}},
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
	if err := (Settings{EffectiveSchemaBudgetTokens: -7}).Validate(); err != nil {
		t.Errorf("read-only effective field validated as input: %v", err)
	}
}

func TestValidateActivations(t *testing.T) {
	good := []Activation{{Name: "outlook__send-mail", Server: "outlook"}, {Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 4, Sticky: true}}
	if err := ValidateActivations(good); err != nil {
		t.Fatalf("good activations: %v", err)
	}
	bad := [][]Activation{
		{{Name: "a__b", Server: "a"}, {Name: "a__b", Server: "a"}},
		{{Name: "a__b"}},
		{{Name: "send-mail", Server: "outlook"}},
		{{Name: "fetch__fetch", Server: "outlook"}},
		{{Name: "outlook__", Server: "outlook"}},
		{{Name: "outlook__x", Server: "outlook", LastUsedTurn: -1}},
	}
	for i, as := range bad {
		if err := ValidateActivations(as); err == nil {
			t.Errorf("bad[%d] %+v validated", i, as)
		}
	}
}

func TestColumnCodec(t *testing.T) {
	if v, err := MarshalExposureColumn(Exposure{}); v != nil || err != nil {
		t.Fatalf("zero layer = %v, %v; want NULL", v, err)
	}
	if v, err := MarshalActivationsColumn(nil); v != nil || err != nil {
		t.Fatalf("empty activations = %v, %v; want NULL", v, err)
	}
	e := toolLevel(TierOff)
	v, err := MarshalExposureColumn(e)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseExposureColumn(sql.NullString{String: v.(string), Valid: true})
	if err != nil || !reflect.DeepEqual(back, e) {
		t.Fatalf("exposure round trip = %+v (%v), want %+v", back, err, e)
	}
	acts := []Activation{{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 2, Sticky: true}}
	v, err = MarshalActivationsColumn(acts)
	if err != nil {
		t.Fatal(err)
	}
	if v.(string) != `[{"name":"fetch__fetch","server":"fetch","lastUsedTurn":2,"sticky":true}]` {
		t.Fatalf("activations wire = %s", v)
	}
	ab, err := ParseActivationsColumn(sql.NullString{String: v.(string), Valid: true})
	if err != nil || !reflect.DeepEqual(ab, acts) {
		t.Fatalf("activations round trip = %+v (%v)", ab, err)
	}
	if z, err := ParseExposureColumn(sql.NullString{}); err != nil || !z.IsZero() {
		t.Fatalf("NULL exposure = %+v, %v", z, err)
	}
	if _, err := ParseExposureColumn(sql.NullString{String: "{", Valid: true}); err == nil {
		t.Fatal("corrupt column decoded")
	}
}

func TestSettings_WithEffective(t *testing.T) {
	got := Settings{}.WithEffective()
	if got.SchemaBudgetTokens != 0 || got.EffectiveSchemaBudgetTokens != DefaultSchemaBudgetTokens ||
		got.ActivationTTLTurns != 0 || got.EffectiveActivationTTLTurns != DefaultActivationTTLTurns {
		t.Fatalf("WithEffective on unset = %+v", got)
	}
	got = Settings{SchemaBudgetTokens: 500, ActivationTTLTurns: 9}.WithEffective()
	if got.EffectiveSchemaBudgetTokens != 500 || got.EffectiveActivationTTLTurns != 9 {
		t.Fatalf("WithEffective on set = %+v", got)
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
