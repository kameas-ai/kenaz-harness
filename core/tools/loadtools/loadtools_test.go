package loadtools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// ── fakes (race-safe) ──────────────────────────────────────────────────

type fakeCatalog struct{ entries []CatalogEntry }

func (f fakeCatalog) Catalog(context.Context, string) ([]CatalogEntry, error) {
	return append([]CatalogEntry(nil), f.entries...), nil
}

type fakeServers struct{ list []ServerInfo }

func (f fakeServers) Servers(context.Context) []ServerInfo {
	return append([]ServerInfo(nil), f.list...)
}

type fakeSettings struct{ s toolexposure.Settings }

func (f fakeSettings) GetToolExposure(context.Context) (toolexposure.Settings, error) {
	return f.s, nil
}

type fakeProjects map[string]toolexposure.Exposure

func (f fakeProjects) ProjectToolExposure(_ context.Context, id string) (toolexposure.Exposure, error) {
	return f[id], nil
}

// fakeSessions is the session store: exposure state for the resolver and
// the activation writer the service persists through. In-memory on
// purpose: these tests assert the load decisions; the sqlite round trip
// is core/rpc TestToolExposureWiring_LoadToolsAndGuardThroughNew.
type fakeSessions struct {
	mu       sync.Mutex
	override toolexposure.Exposure
	project  string
	acts     []toolexposure.Activation
	writes   int
}

func (f *fakeSessions) SessionToolExposure(context.Context, string) (toolexposure.SessionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return toolexposure.SessionState{ProjectID: f.project, Override: f.override.Clone(),
		Activations: append([]toolexposure.Activation(nil), f.acts...)}, nil
}

func (f *fakeSessions) SetToolActivations(_ context.Context, _ string, as []toolexposure.Activation) error {
	if err := toolexposure.ValidateActivations(as); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acts = append([]toolexposure.Activation(nil), as...)
	f.writes++
	return nil
}

func (f *fakeSessions) snapshot() []toolexposure.Activation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolexposure.Activation(nil), f.acts...)
}

type fakeTurns struct{ n int }

func (f fakeTurns) TurnOrdinal(context.Context, string) (int, error) { return f.n, nil }

type recordingAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAudit) Emit(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingAudit) snapshot() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]audit.Event(nil), r.events...)
}

// ── fixture ───────────────────────────────────────────────────────────

type fixture struct {
	svc      *Service
	sessions *fakeSessions
	audit    *recordingAudit
}

func catalogFixture() []CatalogEntry {
	return []CatalogEntry{
		{Name: Name, Server: toolexposure.BuiltinServer},
		{Name: "kenaz__read_file", Server: toolexposure.BuiltinServer},
		{Name: "kenaz__monitor", Server: toolexposure.BuiltinServer},
		{Name: "outlook__send-mail", Server: "outlook"},
		{Name: "outlook__list-messages", Server: "outlook"},
		{Name: "outlook__list-events", Server: "outlook"},
		{Name: "outlook__create-event", Server: "outlook"},
		{Name: "outlook__search-contacts", Server: "outlook"},
		{Name: "outlook__delete-mail", Server: "outlook"},
		{Name: "fetch__fetch", Server: "fetch"},
		{Name: "secret__dump", Server: "secret"},
		{Name: "mixed__keep", Server: "mixed"},
		{Name: "mixed__drop", Server: "mixed"},
	}
}

func serversFixture() []ServerInfo {
	return []ServerInfo{
		{Name: "outlook", Purpose: "Read, search, send and organise Microsoft 365 mail. Also calendars.", State: "running", Running: true},
		{Name: "fetch", Purpose: "Fetch a URL and read it.", State: "running", Running: true},
		{Name: "secret", Purpose: "Should never be listed.", State: "running", Running: true},
		{Name: "mixed", State: "running", Running: true},
		{Name: "github", Purpose: "GitHub issues and pull requests.", State: "failed", Running: false},
	}
}

func newFixture(t *testing.T, user toolexposure.Exposure) fixture {
	t.Helper()
	sess := &fakeSessions{}
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: fakeSettings{s: toolexposure.Settings{Exposure: user}},
		Sessions: sess,
		Projects: fakeProjects{},
	})
	if err != nil {
		t.Fatal(err)
	}
	au := &recordingAudit{}
	svc, err := NewService(Deps{
		Catalog:     fakeCatalog{entries: catalogFixture()},
		Resolver:    resolver,
		Servers:     fakeServers{list: serversFixture()},
		Activations: sess,
		Turns:       fakeTurns{n: 3},
		Audit:       au,
		Now:         func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{svc: svc, sessions: sess, audit: au}
}

func userOffSecretAndMixedDrop() toolexposure.Exposure {
	return toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"secret": {Tier: toolexposure.TierOff},
		"mixed":  {Tools: map[string]toolexposure.Tier{"drop": toolexposure.TierOff}},
	}}
}

// ── digest ─────────────────────────────────────────────────────────────

// TestDigest_RendersSummaryServersAndStoppedMarker covers FR-E2 and
// FR-H1: every summary server is listed with count, purpose and at most
// five examples; off servers are not listed; a server that is installed
// but not running is marked "(stopped)" from the server directory, not
// from the catalog (which omits it).
func TestDigest_RendersSummaryServersAndStoppedMarker(t *testing.T) {
	f := newFixture(t, userOffSecretAndMixedDrop())
	ctx := context.Background()
	rc, servers, err := f.svc.resolve(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	got := RenderDigest(BuildDigest(rc.Partition(), Purposes(servers)), "")

	want := "## Available but not loaded\n" + introLine + "\n" +
		"- fetch (1 tool): Fetch a URL and read it — e.g. fetch\n" +
		"- github (stopped): GitHub issues and pull requests — not running; its tools cannot be loaded until it is started\n" +
		"- kenaz (1 tool) — e.g. monitor\n" +
		"- mixed (1 tool) — e.g. keep\n" +
		"- outlook (6 tools): Read, search, send and organise Microsoft 365 mail — e.g. create-event, delete-mail, list-events, list-messages, search-contacts, …"
	if got != want {
		t.Fatalf("digest mismatch\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "drop") {
		t.Fatalf("digest lists an off server or tool:\n%s", got)
	}
	// Deterministic: a second render of an unchanged catalog is
	// byte-identical.
	rc2, servers2, _ := f.svc.resolve(ctx, "s1")
	if again := RenderDigest(BuildDigest(rc2.Partition(), Purposes(servers2)), ""); again != got {
		t.Fatal("digest is not byte-identical across two renders of the same catalog")
	}
}

func TestDigest_EverythingLoadedRendersNothing(t *testing.T) {
	if got := RenderDigest(nil, ""); got != "" {
		t.Fatalf("empty digest = %q, want no section", got)
	}
	if got := RenderDigest(nil, NoteDefaultsApply); got != "## Available but not loaded\n"+NoteDefaultsApply {
		t.Fatalf("note-only digest = %q", got)
	}
}

// TestDigest_DefaultDigestListsDefaultSummaryTools: with tiers
// unresolvable, every non-hot entry is listed under its server.
func TestDigest_DefaultDigestListsDefaultSummaryTools(t *testing.T) {
	got := RenderDigest(DefaultDigest(catalogFixture(), Purposes(map[string]ServerInfo{
		"fetch": {Name: "fetch", Purpose: "Fetch a URL and read it."},
	})), NoteDefaultsApply)
	for _, want := range []string{NoteDefaultsApply, "- fetch (1 tool): Fetch a URL and read it", "- outlook (6 tools)", "- secret (1 tool)", "- kenaz (1 tool) — e.g. monitor"} {
		if !strings.Contains(got, want) {
			t.Errorf("default digest lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "read_file") {
		t.Errorf("default digest lists a hot tool:\n%s", got)
	}
}

// TestTool_DescriptionIsStatic: the description never carries the
// digest, so the tool stays inside the cacheable prefix.
func TestTool_DescriptionIsStatic(t *testing.T) {
	d := New(nil).Description()
	if !strings.Contains(d, "'Available but not loaded'") || strings.Contains(d, "outlook") {
		t.Fatalf("description = %q", d)
	}
}

// ── Load ───────────────────────────────────────────────────────────────

// TestLoad_ServersToolsAndGlobsWithHonestRefusals covers FR-H2: every
// name that is not loaded is reported with why — the setting for off,
// the live state for a stopped server, "unknown" otherwise.
func TestLoad_ServersToolsAndGlobsWithHonestRefusals(t *testing.T) {
	f := newFixture(t, userOffSecretAndMixedDrop())
	res, err := f.svc.Load(context.Background(), "s1", Request{
		Servers: []string{"fetch", "secret", "github", "nosuch", "mixed"},
		Tools:   []string{"outlook__list*", "outlook__send-mail", "kenaz__read_file", "github__create-issue", "outlook__nope", "zzz__*"},
	}, audit.ToolsActivatedByModel)
	if err != nil {
		t.Fatal(err)
	}
	wantLoaded := []string{"fetch__fetch", "kenaz__read_file", "mixed__keep", "outlook__list-events", "outlook__list-messages", "outlook__send-mail"}
	if !reflect.DeepEqual(res.Loaded, wantLoaded) {
		t.Errorf("Loaded = %v, want %v", res.Loaded, wantLoaded)
	}
	if want := map[string]int{"fetch": 1, "kenaz": 1, "mixed": 1, "outlook": 3}; !reflect.DeepEqual(res.LoadedByServer, want) {
		t.Errorf("LoadedByServer = %v, want %v", res.LoadedByServer, want)
	}
	wantNL := map[string]string{
		"secret":               "off — turned off in Settings → Capabilities",
		"github":               "server github is not running (state: failed)",
		"nosuch":               ReasonUnknown,
		"mixed__drop":          "off — turned off in Settings → Capabilities",
		"github__create-issue": "server github is not running (state: failed)",
		"outlook__nope":        ReasonUnknown,
		"zzz__*":               ReasonInvalidGlob,
	}
	gotNL := map[string]string{}
	for _, nl := range res.NotLoaded {
		gotNL[nl.Name] = nl.Reason
	}
	if !reflect.DeepEqual(gotNL, wantNL) {
		t.Errorf("NotLoaded = %v\nwant       %v", gotNL, wantNL)
	}

	// Activations: summary tools only (kenaz__read_file is already full),
	// at the current turn, not sticky.
	acts := f.sessions.snapshot()
	gotActs := map[string]toolexposure.Activation{}
	for _, a := range acts {
		gotActs[a.Name] = a
	}
	for _, n := range []string{"fetch__fetch", "mixed__keep", "outlook__list-events", "outlook__list-messages", "outlook__send-mail"} {
		a, ok := gotActs[n]
		if !ok || a.LastUsedTurn != 3 || a.Sticky {
			t.Errorf("activation %q = %+v (present=%v), want turn 3 non-sticky", n, a, ok)
		}
	}
	if _, ok := gotActs["kenaz__read_file"]; ok || len(acts) != 5 {
		t.Errorf("activations = %+v, want exactly the 5 summary tools", acts)
	}

	// One values-free audit row.
	evs := f.audit.snapshot()
	if len(evs) != 1 || evs[0].Kind != audit.KindToolsActivated {
		t.Fatalf("audit events = %+v, want one %s", evs, audit.KindToolsActivated)
	}
	var p audit.ToolsActivatedPayload
	if err := json.Unmarshal(evs[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	want := audit.ToolsActivatedPayload{SessionID: "s1", Servers: []string{"fetch", "mixed", "outlook"}, ToolCount: 5, Sticky: false, By: "model"}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("audit payload = %+v, want %+v", p, want)
	}
	if strings.Contains(string(evs[0].Payload), "send-mail") {
		t.Errorf("audit payload carries a tool name: %s", evs[0].Payload)
	}
}

// TestLoad_StickyUpgradesAndNeverDowngrades: a sticky load pins an
// existing activation; a later non-sticky load refreshes its turn but
// keeps it sticky, and re-loading changes nothing auditable.
func TestLoad_StickyUpgradesAndNeverDowngrades(t *testing.T) {
	f := newFixture(t, toolexposure.Exposure{})
	ctx := context.Background()
	if _, err := f.svc.Load(ctx, "s1", Request{Tools: []string{"fetch__fetch"}}, audit.ToolsActivatedByModel); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Load(ctx, "s1", Request{Tools: []string{"fetch__fetch"}, Sticky: true}, audit.ToolsActivatedByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Load(ctx, "s1", Request{Servers: []string{"fetch"}}, audit.ToolsActivatedByModel); err != nil {
		t.Fatal(err)
	}
	acts := f.sessions.snapshot()
	if len(acts) != 1 || !acts[0].Sticky {
		t.Fatalf("activations = %+v, want fetch__fetch sticky", acts)
	}
	evs := f.audit.snapshot()
	if len(evs) != 2 {
		t.Fatalf("audit events = %d, want 2 (activate, make sticky; the re-load changes nothing)", len(evs))
	}
}

func TestLoad_EmptyRequestRefused(t *testing.T) {
	f := newFixture(t, toolexposure.Exposure{})
	if _, err := f.svc.Load(context.Background(), "s1", Request{}, audit.ToolsActivatedByModel); !errors.Is(err, ErrEmptyRequest) {
		t.Fatalf("err = %v, want ErrEmptyRequest", err)
	}
}

func TestAutoActivate_OnlySummaryToolsNotYetActivated(t *testing.T) {
	f := newFixture(t, userOffSecretAndMixedDrop())
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"outlook__send-mail", true},
		{"outlook__send-mail", false}, // already activated
		{"kenaz__read_file", false},   // full
		{"secret__dump", false},       // off
		{"nope__x", false},            // unknown
	} {
		got, err := f.svc.AutoActivate(ctx, "s1", tc.name)
		if err != nil || got != tc.want {
			t.Errorf("AutoActivate(%q) = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	evs := f.audit.snapshot()
	var p audit.ToolsActivatedPayload
	if len(evs) != 1 || json.Unmarshal(evs[0].Payload, &p) != nil || p.By != audit.ToolsActivatedByAuto {
		t.Fatalf("audit = %+v, want one row by %q", evs, audit.ToolsActivatedByAuto)
	}
}

// ── FR-E3 write guard ──────────────────────────────────────────────────

func loadToolsAt(tier toolexposure.Tier) toolexposure.Exposure {
	return toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		toolexposure.BuiltinServer: {Tools: map[string]toolexposure.Tier{"load_tools": tier}},
	}}
}

// TestCheckLayerWrite_RefusesLoadToolsOffWhileSummaryExists: at every
// level a layer turning kenaz__load_tools off (or summary) is refused
// with the reason while summary tools exist, and allowed once nothing
// would be summary.
func TestCheckLayerWrite_RefusesLoadToolsOffWhileSummaryExists(t *testing.T) {
	f := newFixture(t, toolexposure.Exposure{})
	ctx := context.Background()
	for _, lvl := range []toolexposure.Level{toolexposure.LevelUser, toolexposure.LevelProject, toolexposure.LevelSession} {
		for _, tier := range []toolexposure.Tier{toolexposure.TierOff, toolexposure.TierSummary} {
			w := toolexposure.LayerWrite{Level: lvl, ProjectID: "p1", SessionID: "s1", Exposure: loadToolsAt(tier)}
			err := f.svc.CheckLayerWrite(ctx, w)
			if !errors.Is(err, toolexposure.ErrLoadToolsRequired) {
				t.Fatalf("%s/%s: err = %v, want ErrLoadToolsRequired", lvl, tier, err)
			}
			if !strings.Contains(err.Error(), "summary tier") || !strings.Contains(err.Error(), "github (stopped)") {
				t.Errorf("%s/%s: refusal does not name the reason: %v", lvl, tier, err)
			}
		}
		if err := f.svc.CheckLayerWrite(ctx, toolexposure.LayerWrite{Level: lvl, ProjectID: "p1", SessionID: "s1", Exposure: loadToolsAt(toolexposure.TierFull)}); err != nil {
			t.Errorf("%s: full load_tools refused: %v", lvl, err)
		}
	}

	// Every other server full or off in the same layer: nothing would be
	// summary, so turning load_tools off is allowed.
	all := loadToolsAt(toolexposure.TierOff)
	all.Servers[toolexposure.BuiltinServer] = toolexposure.ServerExposure{Tier: toolexposure.TierFull, Tools: map[string]toolexposure.Tier{"load_tools": toolexposure.TierOff}}
	for _, s := range []string{"outlook", "fetch", "secret", "mixed", "github"} {
		all.Servers[s] = toolexposure.ServerExposure{Tier: toolexposure.TierOff}
	}
	if err := f.svc.CheckLayerWrite(ctx, toolexposure.LayerWrite{Level: toolexposure.LevelUser, Exposure: all}); err != nil {
		t.Fatalf("load_tools off with no summary tools refused: %v", err)
	}
}

// ── the tool ───────────────────────────────────────────────────────────

func TestTool_CallLoadsForTheContextSession(t *testing.T) {
	f := newFixture(t, toolexposure.Exposure{})
	tool := New(f.svc)
	if tool.Name() != "kenaz__load_tools" {
		t.Fatalf("Name = %q", tool.Name())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := toolloop.WithSessionID(context.Background(), "s1")
	out, err := tool.Call(ctx, json.RawMessage(`{"servers":["fetch"],"sticky":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Loaded, []string{"fetch__fetch"}) || len(res.NotLoaded) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if acts := f.sessions.snapshot(); len(acts) != 1 || !acts[0].Sticky {
		t.Fatalf("activations = %+v", acts)
	}
	if _, err := tool.Call(ctx, json.RawMessage(`{"server":["fetch"]}`)); err == nil {
		t.Fatal("unknown argument accepted")
	}
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"servers":["fetch"]}`)); err == nil {
		t.Fatal("call without a session accepted")
	}
}

// TestLoad_GlobsAreServerScopedAndHonest: a glob loads within one known
// server only, refuses each off tool it matched by name (FR-H2), and is
// refused outright when bare, cross-server or over an unknown server.
func TestLoad_GlobsAreServerScopedAndHonest(t *testing.T) {
	f := newFixture(t, userOffSecretAndMixedDrop())
	res, err := f.svc.Load(context.Background(), "s1", Request{
		Tools: []string{"mixed__*", "*", "outlook*", "nosuch__*", "github__*", "outlook__zz*", "secret__*"},
	}, audit.ToolsActivatedByModel)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Loaded, []string{"mixed__keep"}) {
		t.Errorf("Loaded = %v, want [mixed__keep]", res.Loaded)
	}
	want := map[string]string{
		"mixed__drop":  "off — turned off in Settings → Capabilities",
		"*":            ReasonInvalidGlob,
		"outlook*":     ReasonInvalidGlob,
		"nosuch__*":    ReasonInvalidGlob,
		"github__*":    "server github is not running (state: failed)",
		"outlook__zz*": ReasonUnknown,
		"secret__*":    "off — turned off in Settings → Capabilities",
	}
	got := map[string]string{}
	for _, nl := range res.NotLoaded {
		got[nl.Name] = nl.Reason
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NotLoaded = %v\nwant       %v", got, want)
	}
}

// TestLoad_ManyToolsAreCountedNotListed: past MaxLoadedNames the result
// counts per server and the summary says so.
func TestLoad_ManyToolsAreCountedNotListed(t *testing.T) {
	f := newFixture(t, userOffSecretAndMixedDrop())
	res, err := f.svc.Load(context.Background(), "s1", Request{Servers: []string{"outlook", "fetch", "mixed", "kenaz"}}, audit.ToolsActivatedByModel)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Loaded) != 0 {
		t.Errorf("Loaded lists %d names past the cap", len(res.Loaded))
	}
	if want := map[string]int{"outlook": 6, "fetch": 1, "mixed": 1, "kenaz": 3}; !reflect.DeepEqual(res.LoadedByServer, want) {
		t.Errorf("LoadedByServer = %v, want %v", res.LoadedByServer, want)
	}
	if !strings.Contains(res.Summary, "6 tools from outlook") {
		t.Errorf("Summary = %q", res.Summary)
	}
}

// TestLoad_ReportsToolsTheTurnWillNotCarry: with a turn view attached,
// a loaded tool the turn's next call will not carry is reported as
// arriving on the next turn.
func TestLoad_ReportsToolsTheTurnWillNotCarry(t *testing.T) {
	f := newFixture(t, toolexposure.Exposure{})
	ctx := WithTurnView(context.Background(), func(_ context.Context, name string) bool { return name != "fetch__fetch" })
	res, err := f.svc.Load(ctx, "s1", Request{Tools: []string{"fetch__fetch", "outlook__send-mail"}}, audit.ToolsActivatedByModel)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.NextTurn, []string{"fetch__fetch"}) || !strings.Contains(res.Summary, "1 available from your next turn") {
		t.Fatalf("NextTurn = %v, Summary = %q", res.NextTurn, res.Summary)
	}
}
