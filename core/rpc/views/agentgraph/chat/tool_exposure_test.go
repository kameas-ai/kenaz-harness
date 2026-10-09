package chat

// On-demand tool exposure through the REAL LLMProviderAdapter and
// kernelToolAdapter (tool-context-budget-01TCBUD01 FR-E1, FR-E4, the
// unloaded-tool auto-activation). The load core is the real
// *loadtools.Service over in-memory sources; only the session store and
// the catalog are fakes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// ── fakes ──────────────────────────────────────────────────────────────

// syncBuffer is a log sink safe to read while the run goroutine is still
// writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureChatLogSync is captureChatLog for runs that log from their own
// goroutine.
func captureChatLogSync(t *testing.T, f func()) string {
	t.Helper()
	buf := &syncBuffer{}
	logging.Replace(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { logging.Replace(logging.FileHandler()) })
	f()
	return buf.String()
}

type exposureSettings struct{ s toolexposure.Settings }

func (e exposureSettings) GetToolExposure(context.Context) (toolexposure.Settings, error) {
	return e.s, nil
}

type exposureProjects struct{}

func (exposureProjects) ProjectToolExposure(context.Context, string) (toolexposure.Exposure, error) {
	return toolexposure.Exposure{}, nil
}

// exposureSessions is a race-safe session store: the resolver reads the
// activated set through it on every call and the load core writes it.
// In-memory on purpose: these tests assert what each request carries,
// not persistence; the sqlite round trip is
// core/rpc TestToolExposureWiring_LoadToolsAndGuardThroughNew.
type exposureSessions struct {
	mu   sync.Mutex
	acts []toolexposure.Activation
}

func (s *exposureSessions) SessionToolExposure(context.Context, string) (toolexposure.SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return toolexposure.SessionState{Activations: append([]toolexposure.Activation(nil), s.acts...)}, nil
}

func (s *exposureSessions) SetToolActivations(_ context.Context, _ string, as []toolexposure.Activation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acts = append([]toolexposure.Activation(nil), as...)
	return nil
}

type specCatalog struct{ specs []corellm.ToolSpec }

func (c specCatalog) Catalog(context.Context, string) ([]loadtools.CatalogEntry, error) {
	out := make([]loadtools.CatalogEntry, 0, len(c.specs))
	for _, s := range c.specs {
		out = append(out, loadtools.CatalogEntry{Name: s.Name, Server: s.Server})
	}
	return out, nil
}

type noServers struct{}

func (noServers) Servers(context.Context) []loadtools.ServerInfo { return nil }

type countingAudit struct {
	mu sync.Mutex
	n  int
}

func (c *countingAudit) Emit(context.Context, audit.Event) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return nil
}

func (c *countingAudit) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func spec(server, tool string) corellm.ToolSpec {
	return corellm.ToolSpec{
		Name:        server + "__" + tool,
		Server:      server,
		Description: tool + " does a thing",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"}}}`),
	}
}

// exposureCatalog: two hot built-ins (load_tools, read_file), one
// non-hot built-in, two Outlook tools and one tool the user turned off.
func exposureCatalog() []corellm.ToolSpec {
	return []corellm.ToolSpec{
		spec("kenaz", "load_tools"),
		spec("kenaz", "read_file"),
		spec("kenaz", "monitor"),
		spec("outlook", "send-mail"),
		spec("outlook", "list-messages"),
		spec("secret", "dump"),
	}
}

func newExposureService(t *testing.T, specs []corellm.ToolSpec) (*loadtools.Service, *exposureSessions, *countingAudit) {
	t.Helper()
	sess := &exposureSessions{}
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: exposureSettings{s: toolexposure.Settings{Exposure: toolexposure.Exposure{
			Servers: map[string]toolexposure.ServerExposure{"secret": {Tier: toolexposure.TierOff}},
		}}},
		Sessions: sess,
		Projects: exposureProjects{},
	})
	if err != nil {
		t.Fatal(err)
	}
	au := &countingAudit{}
	svc, err := loadtools.NewService(loadtools.Deps{
		Catalog:     specCatalog{specs: specs},
		Resolver:    resolver,
		Servers:     noServers{},
		Activations: sess,
		Audit:       au,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, sess, au
}

// recordingScriptedRegistry hands out one scriptedTurn per Stream call
// and records every request's tools and messages.
type recordingScriptedRegistry struct {
	stubRegistry
	mu    sync.Mutex
	turns []scriptedTurn
	reqs  []corellm.GenerationRequest
}

func (r *recordingScriptedRegistry) push(t scriptedTurn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns = append(r.turns, t)
}

func (r *recordingScriptedRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	if len(r.turns) == 0 {
		r.mu.Unlock()
		return nil, fmt.Errorf("recordingScriptedRegistry: out of turns")
	}
	t := r.turns[0]
	r.turns = r.turns[1:]
	r.mu.Unlock()
	ch := make(chan corellm.StreamEvent, len(t.deltas))
	for _, d := range t.deltas {
		ch <- d
	}
	close(ch)
	return &scriptedStream{events: ch, resp: t.resp}, nil
}

func (r *recordingScriptedRegistry) requests() []corellm.GenerationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]corellm.GenerationRequest(nil), r.reqs...)
}

func toolNamesOf(req corellm.GenerationRequest) []string {
	out := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		out = append(out, t.Name)
	}
	return out
}

func hasTool(req corellm.GenerationRequest, name string) bool {
	for _, t := range req.Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// assertOnlySendable is the FR-E1 invariant, checked independently of
// the builder: every tool a request carried is full tier or activated at
// the time of the request.
func assertOnlySendable(t *testing.T, svc *loadtools.Service, specs []corellm.ToolSpec, req corellm.GenerationRequest) {
	t.Helper()
	entries, _ := specCatalog{specs: specs}.Catalog(context.Background(), "s1")
	rc, err := svc.Resolver().Resolve(context.Background(), "s1", loadtools.CatalogWithProbes(entries, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range toolNamesOf(req) {
		if !rc.Sendable(n) {
			rt, _ := rc.Tool(n)
			t.Errorf("FR-E1 violated: request carried %q (tier %s, not activated)", n, rt.Tier)
		}
	}
}

// TestRequestBuilder_NeverSendsSummaryToolUnlessActivated is the FR-E1
// gate seed: across calls, a request carries hot and activated tools
// only — never a summary tool that is not activated, never an off tool —
// and kenaz__load_tools' description is the digest of what was left out.
//
// Mutation: make selectTools return every catalog spec -> the first
// request carries outlook__send-mail and kenaz__monitor and this fails.
func TestRequestBuilder_NeverSendsSummaryToolUnlessActivated(t *testing.T) {
	specs := exposureCatalog()
	svc, _, _ := newExposureService(t, specs)
	ctx := context.Background()

	reg := &recordingScriptedRegistry{}
	reg.push(textTurn("one"))
	reg.push(textTurn("two"))
	reg.push(textTurn("three"))
	adapter := NewLLMProviderAdapter(reg, "p", "m", specs, nil).
		withToolExposure(newExposureTurn(ctx, svc, "s1", specs)).
		WithSessionID("s1")
	req := coreag.LLMRequest{SystemPrompt: "base", Messages: []coreag.Message{{Role: "user", Content: "hi"}}}

	var logs string
	logs = captureChatLog(t, func() {
		for i := 0; i < 2; i++ {
			if _, err := adapter.Generate(ctx, req); err != nil {
				t.Fatalf("Generate %d: %v", i+1, err)
			}
		}
	})
	r1, r2 := reg.requests()[0], reg.requests()[1]
	if got := strings.Join(toolNamesOf(r1), ","); got != "kenaz__load_tools,kenaz__read_file" {
		t.Fatalf("request 1 tools = %s, want the hot set only", got)
	}
	assertOnlySendable(t, svc, specs, r1)

	// The digest is per-call system material — in SystemVolatile, after
	// the cacheable prefix, never in System and never a tool description:
	// kenaz__load_tools is sent exactly as the catalog lists it.
	if r1.Tools[0].Description != specs[0].Description {
		t.Errorf("load_tools description changed per call: %q", r1.Tools[0].Description)
	}
	if strings.Contains(r1.System, loadtools.DigestHeading) {
		t.Errorf("digest is in the cacheable system prefix:\n%s", r1.System)
	}
	i := strings.Index(r1.SystemVolatile, "## "+loadtools.DigestHeading)
	if i < 0 {
		t.Fatalf("per-call system segment has no digest section:\n%s", r1.SystemVolatile)
	}
	digest := r1.SystemVolatile[i:]
	for _, want := range []string{"- kenaz (1 tool) — e.g. monitor", "- outlook (2 tools) — e.g. list-messages, send-mail"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
	if strings.Contains(digest, "secret") {
		t.Errorf("digest lists an off server:\n%s", digest)
	}
	// Empty activated set, non-empty hot set: the whole list is the
	// stable prefix, counted explicitly (len(hot)+len(pinned)) rather
	// than as 0 = "all", and the cache marker lands on its last tool.
	if r1.CacheStableTools != 2 || corellm.CacheMarkerToolIndex(r1) != 1 {
		t.Errorf("request 1 CacheStableTools = %d (marker %d), want 2 (marker on tool 1)", r1.CacheStableTools, corellm.CacheMarkerToolIndex(r1))
	}
	if !strings.Contains(logs, `"tools_summary":3`) || !strings.Contains(logs, `"tools_full":2`) || !strings.Contains(logs, `"tools_stable":2`) {
		t.Errorf("composition log does not count 2 full (both stable) / 3 summary:\n%s", logs)
	}

	// Nothing changed between calls 1 and 2: tools and system are
	// byte-identical.
	b1, _ := json.Marshal(r1.Tools)
	b2, _ := json.Marshal(r2.Tools)
	if string(b1) != string(b2) || r1.System != r2.System {
		t.Fatal("two calls with nothing changed differ in tools or system bytes")
	}

	// Activate one Outlook tool between calls: the next call carries it,
	// after the unchanged stable prefix, and still nothing else.
	if _, err := svc.Load(ctx, "s1", loadtools.Request{Tools: []string{"outlook__send-mail"}}, audit.ToolsActivatedByUser); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Generate(ctx, req); err != nil {
		t.Fatalf("Generate 3: %v", err)
	}
	r3 := reg.requests()[2]
	if got := strings.Join(toolNamesOf(r3), ","); got != "kenaz__load_tools,kenaz__read_file,outlook__send-mail" {
		t.Fatalf("request 3 tools = %s", got)
	}
	b3, _ := json.Marshal(r3.Tools[:2])
	b1p, _ := json.Marshal(r1.Tools[:2])
	if string(b3) != string(b1p) {
		t.Fatal("activation changed the stable prefix bytes")
	}
	// The activation changed the digest, which lives in SystemVolatile:
	// the cacheable System is still byte-identical, and the marker still
	// ends the hot segment rather than moving onto the activated tool.
	if r3.System != r1.System {
		t.Errorf("activation changed the cacheable system prefix:\n%s\n---\n%s", r1.System, r3.System)
	}
	if r3.SystemVolatile == r1.SystemVolatile {
		t.Error("activation did not change the per-call digest")
	}
	if r3.CacheStableTools != 2 || corellm.CacheMarkerToolIndex(r3) != 1 {
		t.Errorf("request 3 CacheStableTools = %d (marker %d), want 2 (marker on tool 1)", r3.CacheStableTools, corellm.CacheMarkerToolIndex(r3))
	}
	assertOnlySendable(t, svc, specs, r3)
}

// TestRequestBuilder_FallbackListsDefaultsWithNote is defect 1 of the
// WP03 review: when tool settings cannot be read, the call carries the
// hot set only and the digest lists the default summary tools under a
// note, instead of claiming everything is loaded.
func TestRequestBuilder_FallbackListsDefaultsWithNote(t *testing.T) {
	specs := exposureCatalog()
	src := brokenSettingsExposure(t, specs)
	sel := newExposureTurn(context.Background(), src, "s1", specs).selectTools(context.Background(), 0)
	var names []string
	for _, tl := range sel.tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "kenaz__load_tools,kenaz__read_file" || sel.stable != 2 {
		t.Fatalf("fallback tools = %v (stable %d)", names, sel.stable)
	}
	for _, want := range []string{loadtools.NoteDefaultsApply, "- outlook (2 tools)", "- secret (1 tool)", "- kenaz (1 tool)"} {
		if !strings.Contains(sel.digest, want) {
			t.Errorf("fallback digest lacks %q:\n%s", want, sel.digest)
		}
	}
	if sel.summary != 4 {
		t.Errorf("fallback summary = %d, want 4", sel.summary)
	}
}

type brokenSettingsSource struct{}

func (brokenSettingsSource) GetToolExposure(context.Context) (toolexposure.Settings, error) {
	return toolexposure.Settings{}, fmt.Errorf("settings.json unreadable")
}

func brokenSettingsExposure(t *testing.T, specs []corellm.ToolSpec) *loadtools.Service {
	t.Helper()
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: brokenSettingsSource{}, Sessions: &exposureSessions{}, Projects: exposureProjects{},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := loadtools.NewService(loadtools.Deps{
		Catalog: specCatalog{specs: specs}, Resolver: resolver, Servers: noServers{}, Activations: &exposureSessions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// TestToolAdapter_BareNameCallFacesTheExposureGate is defect 3: a call
// by bare name ("send-mail") that the adapter resolves to
// outlook__send-mail is gated like the namespaced call.
func TestToolAdapter_BareNameCallFacesTheExposureGate(t *testing.T) {
	specs := exposureCatalog()
	svc, _, _ := newExposureService(t, specs)
	pool := &barePool{entries: []ToolEntry{{Server: "outlook", Name: "send-mail"}}}
	ad := newKernelToolAdapter(pool, nil, "s1").withToolExposure(newExposureTurn(context.Background(), svc, "s1", specs))
	res, err := ad.Call(context.Background(), coreag.ToolCall{Name: "send-mail", Args: map[string]any{"to": "bob"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not_loaded") {
		t.Fatalf("bare-name call result = %+v, want not_loaded", res)
	}
	if n := pool.callCount(); n != 0 {
		t.Fatalf("bare-name call reached the pool %d time(s)", n)
	}
}

type barePool struct {
	entries []ToolEntry
	mu      sync.Mutex
	calls   int
}

func (p *barePool) Tools(context.Context) ([]ToolEntry, error) { return p.entries, nil }

func (p *barePool) Call(context.Context, string, string, []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return []byte(`{"ok":true}`), nil
}

func (p *barePool) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestAssembleRequestTools_SegmentOrderAndStablePrefix: hot, then
// pinned, then activated in the order given (most recently used first);
// stable counts the hot + pinned prefix. A sticky activation joins the
// stable prefix; a non-sticky one follows it.
func TestAssembleRequestTools_SegmentOrderAndStablePrefix(t *testing.T) {
	specs := exposureCatalog()
	specs = append(specs, spec("fetch", "fetch"))
	svc, _, _ := newExposureService(t, specs)
	ctx := context.Background()
	for _, ld := range []loadtools.Request{
		{Tools: []string{"outlook__send-mail"}},
		{Tools: []string{"fetch__fetch"}, Sticky: true},
	} {
		if _, err := svc.Load(ctx, "s1", ld, audit.ToolsActivatedByUser); err != nil {
			t.Fatal(err)
		}
	}
	sel := newExposureTurn(ctx, svc, "s1", specs).selectTools(ctx, 0)
	var got []string
	for _, tl := range sel.tools {
		got = append(got, tl.Name)
	}
	want := "kenaz__load_tools,kenaz__read_file,fetch__fetch,outlook__send-mail"
	if strings.Join(got, ",") != want || sel.stable != 3 {
		t.Fatalf("tools = %v (stable %d), want %s with stable 3", got, sel.stable, want)
	}
}

// exposurePool dispatches kenaz__load_tools to the real tool and records
// every other call.
type exposurePool struct {
	tool  *loadtools.Tool
	mu    sync.Mutex
	calls []string
}

func (p *exposurePool) Tools(context.Context) ([]ToolEntry, error) { return nil, nil }

func (p *exposurePool) Call(ctx context.Context, server, tool string, args []byte) ([]byte, error) {
	if server+"__"+tool == loadtools.Name {
		return p.tool.Call(ctx, args)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, server+"__"+tool)
	return []byte(`{"ok":true}`), nil
}

func (p *exposurePool) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func runExposureTurn(t *testing.T, reg *recordingScriptedRegistry, svc *loadtools.Service, specs []corellm.ToolSpec) (*exposurePool, StreamClosedPayload, string) {
	t.Helper()
	return runExposureTurnPerms(t, reg, svc, specs, nil)
}

// runExposureTurnPerms is runExposureTurn with a permission resolver on
// the tool path.
func runExposureTurnPerms(t *testing.T, reg *recordingScriptedRegistry, svc *loadtools.Service, specs []corellm.ToolSpec, perms ToolPermissionResolver) (*exposurePool, StreamClosedPayload, string) {
	t.Helper()
	pool := &exposurePool{tool: loadtools.New(svc)}
	broker := &recordingBroker{}
	graph := loadProductionChatGraph(t)
	runner, err := New(Config{
		Kernel:         coreag.NewKernel(),
		Registry:       reg,
		Pool:           pool,
		Broker:         broker,
		HistoryWriter:  &recordingHistoryWriter{},
		History:        staticHistoryReader{},
		GraphLoader:    func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:       func() int { return 25 },
		ToolDiscoverer: fakeToolDiscoverer{specs: specs},
		ToolExposure:   svc,
		Perms:          perms,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var closed StreamClosedPayload
	logs := captureChatLogSync(t, func() {
		if _, err := runner.StartStream(context.Background(), "profile-1", "s1", "", testTurn("mail bob")); err != nil {
			t.Fatalf("StartStream: %v", err)
		}
		closed = waitForClosed(t, broker)
	})
	if closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}
	return pool, closed, logs
}

// TestToolLoop_LoadToolsReachesTheNextCall is FR-E4: the model's
// kenaz__load_tools call on call 1 puts the loaded schemas into call 2 of
// the same turn — the loop re-reads the activated set between
// iterations.
//
// Mutation: compute the selection once at StartStream (a.tools) instead
// of per Generate -> call 2 lacks the Outlook tools and this fails.
func TestToolLoop_LoadToolsReachesTheNextCall(t *testing.T) {
	specs := exposureCatalog()
	svc, sess, au := newExposureService(t, specs)
	reg := &recordingScriptedRegistry{}
	reg.push(toolTurn("loading outlook", "tu-1", loadtools.Name, `{"servers":["outlook"]}`))
	reg.push(textTurn("ready"))

	runExposureTurn(t, reg, svc, specs)

	reqs := reg.requests()
	if len(reqs) < 2 {
		t.Fatalf("model calls = %d, want at least 2", len(reqs))
	}
	if hasTool(reqs[0], "outlook__send-mail") {
		t.Fatalf("call 1 already carried outlook__send-mail: %v", toolNamesOf(reqs[0]))
	}
	for _, n := range []string{"outlook__send-mail", "outlook__list-messages"} {
		if !hasTool(reqs[1], n) {
			t.Errorf("call 2 lacks %s after kenaz__load_tools loaded outlook: %v", n, toolNamesOf(reqs[1]))
		}
	}
	for _, r := range reqs {
		assertOnlySendable(t, svc, specs, r)
	}
	sess.mu.Lock()
	n := len(sess.acts)
	sess.mu.Unlock()
	if n != 2 || au.count() != 1 {
		t.Fatalf("activations = %d, audit rows = %d; want 2 and 1", n, au.count())
	}
}

// TestToolLoop_UnloadedToolCallAutoActivatesOnce: a call by exact name
// to a summary tool that is not loaded does not run; it returns a
// structured not_loaded error, the tool is activated, the next call
// carries its schema, the model's retry runs, and the composition log
// counts the auto-activation. An off tool is refused with its setting
// and never activated.
func TestToolLoop_UnloadedToolCallAutoActivatesOnce(t *testing.T) {
	specs := exposureCatalog()
	svc, _, _ := newExposureService(t, specs)
	reg := &recordingScriptedRegistry{}
	reg.push(toolTurn("sending", "tu-1", "outlook__send-mail", `{"to":"bob"}`))
	reg.push(toolTurn("retrying", "tu-2", "outlook__send-mail", `{"to":"bob"}`))
	reg.push(toolTurn("dumping", "tu-3", "secret__dump", `{"to":"x"}`))
	reg.push(textTurn("sent"))

	pool, _, logs := runExposureTurn(t, reg, svc, specs)

	if got := pool.snapshot(); len(got) != 1 || got[0] != "outlook__send-mail" {
		t.Fatalf("dispatched calls = %v, want exactly the retried outlook__send-mail (the first call must not run, secret__dump never)", got)
	}
	reqs := reg.requests()
	if len(reqs) < 4 {
		t.Fatalf("model calls = %d, want 4", len(reqs))
	}
	if hasTool(reqs[0], "outlook__send-mail") || !hasTool(reqs[1], "outlook__send-mail") {
		t.Fatalf("send-mail carried on call 1 = %v, call 2 = %v; want false, true", hasTool(reqs[0], "outlook__send-mail"), hasTool(reqs[1], "outlook__send-mail"))
	}
	messagesOf := func(req corellm.GenerationRequest) string {
		raw, _ := json.Marshal(req.Messages)
		return string(raw)
	}
	if got := messagesOf(reqs[1]); !strings.Contains(got, `not_loaded`) || !strings.Contains(got, `"loaded_now":true`) {
		t.Errorf("call 2 does not carry the structured not_loaded result for tu-1: %s", got)
	}
	if got := messagesOf(reqs[3]); !strings.Contains(got, `not_available`) || !strings.Contains(got, "Settings") {
		t.Errorf("off tool result does not name the setting: %s", got)
	}
	for _, r := range reqs {
		if hasTool(r, "secret__dump") {
			t.Fatalf("an off tool reached a request: %v", toolNamesOf(r))
		}
	}
	var autoCounts []int
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "llm.request.composition" {
			if v, ok := rec["auto_activated"].(float64); ok {
				autoCounts = append(autoCounts, int(v))
			}
		}
	}
	sort.Ints(autoCounts)
	if len(autoCounts) < 2 || autoCounts[0] != 0 || autoCounts[len(autoCounts)-1] != 1 {
		t.Errorf("composition auto_activated across calls = %v, want 0 on call 1 then 1", autoCounts)
	}
}

// recordingPerms allows every call and records what it was asked.
type recordingPerms struct {
	mu    sync.Mutex
	asked []string
}

func (r *recordingPerms) Resolve(_ context.Context, _, server, tool string) (PermVerdict, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, server+"__"+tool)
	return PermVerdict{Server: server, Tool: tool, Policy: "auto_allow"}, nil
}

func (r *recordingPerms) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}

// TestToolLoop_LoadThenCallWithinTwoModelCalls is acceptance criterion 2
// end to end: call 1 is kenaz__load_tools(servers: outlook), call 2 is
// outlook__send-mail, which carries its schema, passes the permission
// resolver and dispatches; one tools.activated audit row.
func TestToolLoop_LoadThenCallWithinTwoModelCalls(t *testing.T) {
	specs := exposureCatalog()
	svc, _, au := newExposureService(t, specs)
	reg := &recordingScriptedRegistry{}
	reg.push(toolTurn("loading outlook", "tu-1", loadtools.Name, `{"servers":["outlook"]}`))
	reg.push(toolTurn("sending", "tu-2", "outlook__send-mail", `{"to":"bob"}`))
	reg.push(textTurn("sent"))
	perms := &recordingPerms{}

	pool, _, _ := runExposureTurnPerms(t, reg, svc, specs, perms)

	reqs := reg.requests()
	if len(reqs) < 2 || !hasTool(reqs[1], "outlook__send-mail") {
		t.Fatalf("call 2 does not carry outlook__send-mail")
	}
	if got := pool.snapshot(); len(got) != 1 || got[0] != "outlook__send-mail" {
		t.Fatalf("dispatched = %v, want [outlook__send-mail]", got)
	}
	askedSend := false
	for _, n := range perms.snapshot() {
		if n == "outlook__send-mail" {
			askedSend = true
		}
	}
	if !askedSend {
		t.Fatalf("permission resolver never saw outlook__send-mail: %v", perms.snapshot())
	}
	if au.count() != 1 {
		t.Fatalf("tools.activated rows = %d, want 1", au.count())
	}
}

// TestToolLoop_LoadOutsideTheTurnCatalogReportsNextTurn is defect 4: a
// tool kenaz__load_tools loads but this turn's catalog does not hold
// (its server started after the turn began) is reported as arriving on
// the next turn, not on the next call.
func TestToolLoop_LoadOutsideTheTurnCatalogReportsNextTurn(t *testing.T) {
	turnSpecs := exposureCatalog()
	all := append(append([]corellm.ToolSpec(nil), turnSpecs...), spec("fetch", "fetch"))
	svc, _, _ := newExposureService(t, all)
	reg := &recordingScriptedRegistry{}
	reg.push(toolTurn("loading", "tu-1", loadtools.Name, `{"tools":["fetch__fetch","outlook__send-mail"]}`))
	reg.push(textTurn("ok"))

	runExposureTurn(t, reg, svc, turnSpecs)

	reqs := reg.requests()
	if len(reqs) < 2 {
		t.Fatalf("model calls = %d", len(reqs))
	}
	raw, _ := json.Marshal(reqs[1].Messages)
	msgs := string(raw)
	if !strings.Contains(msgs, `"next_turn":["fetch__fetch"]`) || !strings.Contains(msgs, "available from your next turn") {
		t.Fatalf("load result does not report fetch__fetch for the next turn: %s", msgs)
	}
	if hasTool(reqs[1], "fetch__fetch") || !hasTool(reqs[1], "outlook__send-mail") {
		t.Fatalf("call 2 tools = %v", toolNamesOf(reqs[1]))
	}
}
