package chat

// Schema budget, eviction, activation TTL and mid-turn immunity through
// the REAL LLMProviderAdapter and *loadtools.Service
// (tool-context-budget-01TCBUD01 §2.3, FR-B1, FR-B2). Sizes are planted
// on ToolSpec.TokenEst so the eviction arithmetic is exact.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// kindAudit records audit kinds, race-safe.
type kindAudit struct {
	mu    sync.Mutex
	kinds []audit.Kind
}

func (k *kindAudit) Emit(_ context.Context, ev audit.Event) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.kinds = append(k.kinds, ev.Kind)
	return nil
}

func (k *kindAudit) count(kind audit.Kind) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, x := range k.kinds {
		if x == kind {
			n++
		}
	}
	return n
}

// fixedTurns is a settable turn counter.
type fixedTurns struct{ n atomic.Int32 }

func (f *fixedTurns) TurnOrdinal(context.Context, string) (int, error) { return int(f.n.Load()), nil }

// activationSource is what the budget tests read and plant activations
// through: the in-memory fake or a real *session.Manager.
type activationSource interface {
	toolexposure.SessionSource
	SetToolActivations(ctx context.Context, sessionID string, as []toolexposure.Activation) error
}

func sized(server, tool string, tokens int) corellm.ToolSpec {
	s := spec(server, tool)
	s.TokenEst = tokens
	return s
}

// budgetCatalog: hot load_tools and read_file (100), sticky-pinned
// fetch__fetch (1000), four Outlook tools (1000 each).
func budgetCatalog() []corellm.ToolSpec {
	return []corellm.ToolSpec{
		spec("kenaz", "load_tools"),
		sized("kenaz", "read_file", 100),
		sized("fetch", "fetch", 1000),
		sized("outlook", "a", 1000),
		sized("outlook", "b", 1000),
		sized("outlook", "c", 1000),
		sized("outlook", "d", 1000),
	}
}

// budgetActivations: fetch sticky; Outlook a..d last used on turns 4..1,
// so d is the activated tail.
func budgetActivations() []toolexposure.Activation {
	return []toolexposure.Activation{
		{Name: "fetch__fetch", Server: "fetch", Sticky: true},
		{Name: "outlook__a", Server: "outlook", LastUsedTurn: 4},
		{Name: "outlook__b", Server: "outlook", LastUsedTurn: 3},
		{Name: "outlook__c", Server: "outlook", LastUsedTurn: 2},
		{Name: "outlook__d", Server: "outlook", LastUsedTurn: 1},
	}
}

func newBudgetService(t *testing.T, specs []corellm.ToolSpec, st toolexposure.Settings, sess activationSource, turns loadtools.TurnCounter) (*loadtools.Service, *kindAudit) {
	t.Helper()
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: exposureSettings{s: st},
		Sessions: sess,
		Projects: exposureProjects{},
	})
	if err != nil {
		t.Fatal(err)
	}
	au := &kindAudit{}
	svc, err := loadtools.NewService(loadtools.Deps{
		Catalog:     specCatalog{specs: specs},
		Resolver:    resolver,
		Servers:     noServers{},
		Activations: sess,
		Turns:       turns,
		Audit:       au,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, au
}

// profileRegistry answers Profile with a kind, so the adapter can look
// up the model's window.
type profileRegistry struct {
	*recordingScriptedRegistry
	kind   string
	model  string
	models []string
}

func (p profileRegistry) Profile(string) (corellm.ProviderProfile, error) {
	model := p.model
	if model == "" {
		model = "m"
	}
	return corellm.ProviderProfile{ID: "p", Kind: p.kind, Model: model, Models: p.models}, nil
}

func compositionLines(t *testing.T, logs, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func lastComposition(a *LLMProviderAdapter) corellm.PromptComposition {
	a.lastRespMu.Lock()
	defer a.lastRespMu.Unlock()
	if a.lastResp.Composition == nil {
		return corellm.PromptComposition{}
	}
	return *a.lastResp.Composition
}

// TestBudget_EvictsActivatedTailThenStableAcrossCalls is FR-B2's first
// half and the ordering contract: over budget, the least recently used
// activated tools are left out (never hot, never pinned while activated
// tools remain), their server is back in the digest section of the
// system prompt, the eviction is logged once and audited once per
// session (across turns too), the composition log carries budget and
// evicted, and two calls send byte-identical tools.
//
// Mutation: evict from the head of the activated segment -> outlook__a
// is evicted instead of outlook__d and this fails.
func TestBudget_EvictsActivatedTailThenStableAcrossCalls(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: budgetActivations()}
	// Sent unfitted: 100 + load_tools (small) + 1000 pinned + 4000
	// activated. Over by 2500..3000 at 2600: d, c and b go, a stays.
	svc, au := newBudgetService(t, specs, toolexposure.Settings{SchemaBudgetTokens: 2600}, sess, nil)
	ctx := context.Background()

	reg := &recordingScriptedRegistry{}
	reg.push(textTurn("one"))
	reg.push(textTurn("two"))
	adapter := NewLLMProviderAdapter(reg, "p", "m", specs, nil).
		withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
		WithSessionID("s1")
	req := coreag.LLMRequest{SystemPrompt: "base", Messages: []coreag.Message{{Role: "user", Content: "hi"}}}

	logs := captureChatLog(t, func() {
		for i := 0; i < 2; i++ {
			if _, err := adapter.Generate(ctx, req); err != nil {
				t.Fatalf("Generate %d: %v", i+1, err)
			}
		}
	})
	reqs := reg.requests()
	want := "kenaz__load_tools,kenaz__read_file,fetch__fetch,outlook__a"
	if got := strings.Join(toolNamesOf(reqs[0]), ","); got != want {
		t.Fatalf("request 1 tools = %s, want %s", got, want)
	}
	if loadSize := corellm.EstimateToolSpecTokens(specs[0]); loadSize >= 500 {
		t.Fatalf("load_tools is %d tokens; the planted arithmetic assumes < 500", loadSize)
	}
	if !strings.Contains(reqs[0].System, "outlook (3 tools) — e.g. b, c, d") {
		t.Errorf("evicted Outlook tools are not back in the digest:\n%s", reqs[0].System)
	}
	b1, _ := json.Marshal(reqs[0].Tools)
	b2, _ := json.Marshal(reqs[1].Tools)
	if string(b1) != string(b2) {
		t.Errorf("two calls with no change sent different tools bytes:\n%s\n%s", b1, b2)
	}

	evLines := compositionLines(t, logs, "tools.evicted")
	if len(evLines) != 1 {
		t.Fatalf("tools.evicted lines = %d, want 1 (same eviction on both calls logs once)", len(evLines))
	}
	if got, _ := json.Marshal(evLines[0]["evicted"]); string(got) != `["outlook__d","outlook__c","outlook__b"]` {
		t.Errorf("tools.evicted evicted = %s", got)
	}
	if ob, _ := evLines[0]["over_by"].(float64); ob < 2500 || ob > 3000 {
		t.Errorf("tools.evicted over_by = %v, want 2500..3000", evLines[0]["over_by"])
	}
	if n := au.count(audit.KindToolsEvicted); n != 1 {
		t.Errorf("tools.evicted audit rows = %d, want 1", n)
	}
	comps := compositionLines(t, logs, "llm.request.composition")
	if len(comps) != 2 {
		t.Fatalf("composition lines = %d, want 2", len(comps))
	}
	for i, c := range comps {
		if c["budget"] != float64(2600) || c["evicted"] != float64(3) || c["pinned_over_budget_by"] != float64(0) {
			t.Errorf("composition %d: budget=%v evicted=%v pinned_over_budget_by=%v, want 2600, 3, 0",
				i+1, c["budget"], c["evicted"], c["pinned_over_budget_by"])
		}
	}
	if c := lastComposition(adapter); c.Budget != 2600 || c.Evicted != 3 {
		t.Errorf("response composition budget=%d evicted=%d, want 2600 and 3", c.Budget, c.Evicted)
	}

	// The next turn evicts the same set: no second audit row.
	reg.push(textTurn("three"))
	next := NewLLMProviderAdapter(reg, "p", "m", specs, nil).
		withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
		WithSessionID("s1")
	if _, err := next.Generate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if n := au.count(audit.KindToolsEvicted); n != 1 {
		t.Errorf("tools.evicted audit rows after a second turn with the same overage = %d, want 1", n)
	}
}

// TestBudget_PinnedEvictedWithWarning: when evicting every activated tool
// is not enough, pinned tools go next and the composition carries how far
// pinned tools exceed the budget; the hot set always stays.
func TestBudget_PinnedEvictedWithWarning(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: budgetActivations()}
	svc, _ := newBudgetService(t, specs, toolexposure.Settings{SchemaBudgetTokens: 600}, sess, nil)
	ctx := context.Background()
	reg := &recordingScriptedRegistry{}
	reg.push(textTurn("one"))
	adapter := NewLLMProviderAdapter(reg, "p", "m", specs, nil).
		withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
		WithSessionID("s1")
	logs := captureChatLog(t, func() {
		if _, err := adapter.Generate(ctx, coreag.LLMRequest{Messages: []coreag.Message{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatal(err)
		}
	})
	if got := strings.Join(toolNamesOf(reg.requests()[0]), ","); got != "kenaz__load_tools,kenaz__read_file" {
		t.Fatalf("tools = %s, want the hot set only", got)
	}
	c := lastComposition(adapter)
	if c.Evicted != 5 || c.PinnedOverBudgetBy <= 0 {
		t.Fatalf("composition evicted=%d pinnedOverBudgetBy=%d, want 5 and > 0", c.Evicted, c.PinnedOverBudgetBy)
	}
	ev := compositionLines(t, logs, "tools.evicted")
	if len(ev) != 1 || ev[0]["pinned_evicted"] != float64(1) {
		t.Fatalf("tools.evicted = %v, want one line with pinned_evicted 1", ev)
	}
	// hot (100 + load_tools) + pinned 1000 exceed 600; the hot set fits,
	// so the whole overage is the pinned segment's.
	want := 100 + corellm.EstimateToolSpecTokens(specs[0]) + 1000 - 600
	if c.PinnedOverBudgetBy != want || c.HotOverBudgetBy != 0 {
		t.Errorf("PinnedOverBudgetBy = %d, HotOverBudgetBy = %d, want %d and 0", c.PinnedOverBudgetBy, c.HotOverBudgetBy, want)
	}
}

// TestBudget_HotOverBudgetReported: a budget the always-sent core tools
// alone exceed sends them anyway and reports by how much, apart from
// the pinned overage.
func TestBudget_HotOverBudgetReported(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: budgetActivations()}
	svc, _ := newBudgetService(t, specs, toolexposure.Settings{SchemaBudgetTokens: 50}, sess, nil)
	ctx := context.Background()
	reg := &recordingScriptedRegistry{}
	reg.push(textTurn("one"))
	adapter := NewLLMProviderAdapter(reg, "p", "m", specs, nil).
		withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
		WithSessionID("s1")
	logs := captureChatLog(t, func() {
		if _, err := adapter.Generate(ctx, coreag.LLMRequest{Messages: []coreag.Message{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatal(err)
		}
	})
	if got := strings.Join(toolNamesOf(reg.requests()[0]), ","); got != "kenaz__load_tools,kenaz__read_file" {
		t.Fatalf("tools = %s, want the hot set (never evicted)", got)
	}
	hot := 100 + corellm.EstimateToolSpecTokens(specs[0])
	c := lastComposition(adapter)
	if c.HotOverBudgetBy != hot-50 || c.PinnedOverBudgetBy != 1000 {
		t.Fatalf("HotOverBudgetBy = %d, PinnedOverBudgetBy = %d, want %d and 1000 (the pinned segment's own size)", c.HotOverBudgetBy, c.PinnedOverBudgetBy, hot-50)
	}
	comps := compositionLines(t, logs, "llm.request.composition")
	if len(comps) != 1 || comps[0]["hot_over_budget_by"] != float64(hot-50) {
		t.Fatalf("composition hot_over_budget_by = %v", comps)
	}
}

// TestBudget_CappedByModelWindow is FR-B1 through the adapter: the
// budget is 15 % of the window the lookup returns for the adapter's
// provider kind and model, and the setting when the window is unknown.
func TestBudget_CappedByModelWindow(t *testing.T) {
	specs := budgetCatalog()
	for _, c := range []struct {
		name   string
		window int
		want   int
	}{
		{"window caps the setting", 20000, 3000},
		{"window above the setting", 400000, 24000},
		{"unknown window", 0, 24000},
	} {
		t.Run(c.name, func(t *testing.T) {
			sess := &exposureSessions{acts: budgetActivations()}
			svc, _ := newBudgetService(t, specs, toolexposure.Settings{SchemaBudgetTokens: 24000}, sess, nil)
			ctx := context.Background()
			inner := &recordingScriptedRegistry{}
			inner.push(textTurn("one"))
			var asked []string
			var mu sync.Mutex
			adapter := NewLLMProviderAdapter(profileRegistry{recordingScriptedRegistry: inner, kind: "anthropic"}, "p", "model-x", specs, nil).
				withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
				withModelWindow(func(kind, model string) int {
					mu.Lock()
					asked = append(asked, kind+"/"+model)
					mu.Unlock()
					return c.window
				}).
				WithSessionID("s1")
			if _, err := adapter.Generate(ctx, coreag.LLMRequest{Messages: []coreag.Message{{Role: "user", Content: "hi"}}}); err != nil {
				t.Fatal(err)
			}
			if got := lastComposition(adapter).Budget; got != c.want {
				t.Errorf("budget = %d, want %d", got, c.want)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(asked) == 0 || asked[0] != "anthropic/model-x" {
				t.Errorf("window lookup asked %v, want anthropic/model-x", asked)
			}
		})
	}
}

// TestBudget_WindowIsTheDispatchedModels: with no model override the
// call goes to the profile's dispatch model, and the budget is capped by
// that model's window — not by the first entry of the profile's model
// list, which this call never runs.
//
// Mutation: resolve the window model with ActiveModelID() -> the 8k
// model's cap (1200) applies and this fails.
func TestBudget_WindowIsTheDispatchedModels(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: budgetActivations()}
	svc, _ := newBudgetService(t, specs, toolexposure.Settings{SchemaBudgetTokens: 24000}, sess, nil)
	ctx := context.Background()
	inner := &recordingScriptedRegistry{}
	inner.push(textTurn("one"))
	reg := profileRegistry{recordingScriptedRegistry: inner, kind: "anthropic", model: "big", models: []string{"small", "big"}}
	windows := map[string]int{"anthropic/big": 200000, "anthropic/small": 8000}
	adapter := NewLLMProviderAdapter(reg, "p", "", specs, nil).
		withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
		withModelWindow(func(kind, model string) int { return windows[kind+"/"+model] }).
		WithSessionID("s1")
	if _, err := adapter.Generate(ctx, coreag.LLMRequest{Messages: []coreag.Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	if got := lastComposition(adapter).Budget; got != 24000 {
		t.Fatalf("budget = %d, want 24000 (min(24000, 15 %% of big's 200000))", got)
	}
}

// noMarkExposure is the load core with MarkUsed disabled, so the only
// thing keeping a called tool in the request is the turn's immunity.
type noMarkExposure struct{ *loadtools.Service }

func (noMarkExposure) MarkUsed(context.Context, string, string, int) (bool, error) { return false, nil }

// TestBudget_ToolCalledThisTurnIsNeverEvicted: the activated tail is
// immune once the model has called it this turn; eviction takes the next
// tool instead.
//
// Mutation: pass a nil immune func to FitBudget -> outlook__d is evicted
// and this fails.
func TestBudget_ToolCalledThisTurnIsNeverEvicted(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: budgetActivations()}
	// Over by 500..1000 at 4600: exactly one activated tool goes.
	svc, _ := newBudgetService(t, specs, toolexposure.Settings{SchemaBudgetTokens: 4600}, sess, nil)
	ctx := context.Background()
	turn := newExposureTurn(ctx, noMarkExposure{svc}, "s1", specs)

	before := turn.selectTools(ctx, 0)
	if names := toolNamesOf(corellm.GenerationRequest{Tools: before.tools}); hasName(names, "outlook__d") || before.evicted != 1 {
		t.Fatalf("before the call: tools %v evicted %d, want outlook__d evicted alone", names, before.evicted)
	}
	turn.markCalled(ctx, "outlook__d")
	after := turn.selectTools(ctx, 0)
	names := toolNamesOf(corellm.GenerationRequest{Tools: after.tools})
	if !hasName(names, "outlook__d") || hasName(names, "outlook__c") || after.evicted != 1 {
		t.Fatalf("after calling outlook__d: tools %v evicted %d, want outlook__d kept and outlook__c evicted", names, after.evicted)
	}
}

func hasName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

// failingTurns is a turn counter whose read fails.
type failingTurns struct{}

func (failingTurns) TurnOrdinal(context.Context, string) (int, error) {
	return 0, errors.New("turn runs unreadable")
}

// TestTTL_UnknownTurnKeepsStamps: when the turn ordinal cannot be read,
// neither a call nor a re-load re-stamps an activation (a stamp of 0
// would expire it at the next turn), and nothing expires.
func TestTTL_UnknownTurnKeepsStamps(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: []toolexposure.Activation{
		{Name: "outlook__a", Server: "outlook", LastUsedTurn: 5},
		{Name: "outlook__d", Server: "outlook", LastUsedTurn: 5},
	}}
	svc, _ := newBudgetService(t, specs, toolexposure.Settings{}, sess, failingTurns{})
	ctx := context.Background()
	turn := newExposureTurn(ctx, svc, "s1", specs)
	turn.selectTools(ctx, 0)
	turn.markCalled(ctx, "outlook__d")
	if _, err := svc.Load(ctx, "s1", loadtools.Request{Tools: []string{"outlook__a"}}, audit.ToolsActivatedByUser); err != nil {
		t.Fatal(err)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.acts) != 2 {
		t.Fatalf("activations = %+v, want both kept", sess.acts)
	}
	for _, a := range sess.acts {
		if a.LastUsedTurn != 5 {
			t.Errorf("%s LastUsedTurn = %d, want 5 kept", a.Name, a.LastUsedTurn)
		}
	}
}

// TestTTL_CalledToolRefreshesLastUsedTurn: a call to an activated tool
// stamps the current turn on its activation; a call to a full-tier tool
// writes nothing.
func TestTTL_CalledToolRefreshesLastUsedTurn(t *testing.T) {
	specs := budgetCatalog()
	sess := &exposureSessions{acts: budgetActivations()}
	turns := &fixedTurns{}
	turns.n.Store(5)
	svc, _ := newBudgetService(t, specs, toolexposure.Settings{}, sess, turns)
	ctx := context.Background()
	turn := newExposureTurn(ctx, svc, "s1", specs)
	turn.markCalled(ctx, "outlook__d")
	turn.markCalled(ctx, "kenaz__read_file")
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for _, a := range sess.acts {
		if a.Name == "outlook__d" && a.LastUsedTurn != 5 {
			t.Fatalf("outlook__d LastUsedTurn = %d, want 5", a.LastUsedTurn)
		}
		if a.Name == "kenaz__read_file" {
			t.Fatalf("a call to a full-tier tool created an activation: %+v", a)
		}
	}
}

func openExposureSQLite(t *testing.T, dir string) (storage.DB, *session.Manager) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	return db, session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
}

// TestTTL_ExpiresAcrossTurnsThroughRealSQLite: activations persisted on
// turn 1 survive a close/reopen; at a later turn's first call the
// non-sticky ones unused for more than the TTL are dropped from the
// stored set and from the request, their server returns to the digest,
// and the sticky one stays. Within that turn nothing further expires.
func TestTTL_ExpiresAcrossTurnsThroughRealSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	specs := budgetCatalog()
	settingsTTL := toolexposure.Settings{ActivationTTLTurns: 2}

	db, mgr := openExposureSQLite(t, dir)
	rec, err := mgr.Create(ctx, "ttl")
	if err != nil {
		t.Fatal(err)
	}
	turns := &fixedTurns{}
	turns.n.Store(1)
	svc, _ := newBudgetService(t, specs, settingsTTL, mgr, turns)
	if _, err := svc.Load(ctx, rec.ID, loadtools.Request{Tools: []string{"outlook__a", "outlook__b"}}, audit.ToolsActivatedByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Load(ctx, rec.ID, loadtools.Request{Tools: []string{"fetch__fetch"}, Sticky: true}, audit.ToolsActivatedByUser); err != nil {
		t.Fatal(err)
	}
	turns.n.Store(3)
	if _, err := svc.MarkUsed(ctx, rec.ID, "outlook__b", 3); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}

	db, mgr = openExposureSQLite(t, dir)
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	turns.n.Store(4) // a: 4-1 = 3 > 2 expires; b: 4-3 = 1 stays; fetch sticky.
	svc, _ = newBudgetService(t, specs, settingsTTL, mgr, turns)
	turn := newExposureTurn(ctx, svc, rec.ID, specs)
	sel := turn.selectTools(ctx, 0)
	names := toolNamesOf(corellm.GenerationRequest{Tools: sel.tools})
	if hasName(names, "outlook__a") || !hasName(names, "outlook__b") || !hasName(names, "fetch__fetch") {
		t.Fatalf("turn 4 tools = %v, want outlook__a expired, outlook__b and fetch__fetch kept", names)
	}
	if !strings.Contains(sel.digest, "outlook (3 tools)") {
		t.Errorf("expired outlook__a is not back in the digest:\n%s", sel.digest)
	}
	st, err := mgr.SessionToolExposure(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	for _, a := range st.Activations {
		stored = append(stored, a.Name)
	}
	if strings.Join(stored, ",") != "outlook__b,fetch__fetch" {
		t.Fatalf("stored activations after expiry = %v, want [outlook__b fetch__fetch]", stored)
	}

	// Mid-turn: the turn counter moving on does not expire anything until
	// the next turn's view is built.
	turns.n.Store(40)
	again := turn.selectTools(ctx, 0)
	if !hasName(toolNamesOf(corellm.GenerationRequest{Tools: again.tools}), "outlook__b") {
		t.Fatal("outlook__b expired mid-turn")
	}
	next := newExposureTurn(ctx, svc, rec.ID, specs)
	if hasName(toolNamesOf(corellm.GenerationRequest{Tools: next.selectTools(ctx, 0).tools}), "outlook__b") {
		t.Fatal("outlook__b survived to turn 40 with a TTL of 2")
	}
}

// TestLoad_IntoExhaustedBudget is the load's honesty about the budget: a
// tool loaded this turn goes ahead of older activations; when even that
// does not fit, the load reports it not loaded with the overage (and a
// pinned full-tier tool as pinned but over budget) instead of "loaded",
// and the next request does not carry it.
//
// Mutation: drop the TurnBudget hook from load -> both report "loaded"
// and this fails.
func TestLoad_IntoExhaustedBudget(t *testing.T) {
	specs := []corellm.ToolSpec{
		spec("kenaz", "load_tools"),
		sized("kenaz", "read_file", 100),
		sized("git", "status", 1000),
		sized("outlook", "a", 1000),
		sized("outlook", "b", 1000),
	}
	hot := 100 + corellm.EstimateToolSpecTokens(specs[0])
	userFullGit := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"git": {Tier: toolexposure.TierFull}}}
	ctx := context.Background()

	t.Run("loaded this turn goes ahead of an older activation", func(t *testing.T) {
		sess := &exposureSessions{acts: []toolexposure.Activation{{Name: "outlook__a", Server: "outlook", LastUsedTurn: 9}}}
		svc, _ := newBudgetService(t, specs, toolexposure.Settings{Exposure: userFullGit, SchemaBudgetTokens: hot + 2000}, sess, nil)
		turn := newExposureTurn(ctx, svc, "s1", specs)
		turn.selectTools(ctx, 0)
		res, err := svc.Load(loadtools.WithTurnBudget(ctx, turn.fitLoaded), "s1", loadtools.Request{Tools: []string{"outlook__b"}}, audit.ToolsActivatedByModel)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.NotLoaded) != 0 || !hasName(res.Loaded, "outlook__b") {
			t.Fatalf("result = %+v, want outlook__b loaded", res)
		}
		names := toolNamesOf(corellm.GenerationRequest{Tools: turn.selectTools(ctx, 0).tools})
		if !hasName(names, "outlook__b") || hasName(names, "outlook__a") {
			t.Fatalf("next call tools = %v, want outlook__b sent and the older outlook__a evicted", names)
		}
	})

	t.Run("nothing left to evict", func(t *testing.T) {
		sess := &exposureSessions{acts: []toolexposure.Activation{{Name: "outlook__a", Server: "outlook", LastUsedTurn: 9}}}
		svc, _ := newBudgetService(t, specs, toolexposure.Settings{Exposure: userFullGit, SchemaBudgetTokens: hot + 500}, sess, nil)
		turn := newExposureTurn(ctx, svc, "s1", specs)
		turn.selectTools(ctx, 0)
		res, err := svc.Load(loadtools.WithTurnBudget(ctx, turn.fitLoaded), "s1",
			loadtools.Request{Tools: []string{"outlook__b", "git__status"}}, audit.ToolsActivatedByModel)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Loaded) != 0 {
			t.Fatalf("Loaded = %v, want none (neither fits)", res.Loaded)
		}
		reasons := map[string]string{}
		for _, nl := range res.NotLoaded {
			reasons[nl.Name] = nl.Reason
		}
		if r := reasons["outlook__b"]; !strings.HasPrefix(r, loadtools.ReasonOverBudget+" by ") {
			t.Errorf("outlook__b reason = %q, want %q…", r, loadtools.ReasonOverBudget)
		}
		if r := reasons["git__status"]; !strings.HasPrefix(r, loadtools.ReasonPinnedOverBudget+" by ") {
			t.Errorf("git__status reason = %q, want %q…", r, loadtools.ReasonPinnedOverBudget)
		}
		if !strings.Contains(res.Summary, "2 loaded but over the schema budget — unload others or pick a larger model") {
			t.Errorf("summary = %q", res.Summary)
		}
		names := toolNamesOf(corellm.GenerationRequest{Tools: turn.selectTools(ctx, 0).tools})
		if strings.Join(names, ",") != "kenaz__load_tools,kenaz__read_file" {
			t.Fatalf("next call tools = %v, want the hot set only", names)
		}
	})
}
