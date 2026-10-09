package rpc

// Dials-to-consumer for every FR-K1 control of tool-context-budget-
// 01TCBUD01 (WP08): each control is written the way its binding writes
// it, then the NEXT chat turn is driven through newLLMStack's real chat
// runner, discoverer, dispatch pool and anthropic adapter (over an
// httptest SSE fixture that records the wire request), over real sqlite
// (core.New on a temp DataDir). Every test asserts the wire request —
// which tool schemas the provider received, and what the digest lists —
// never the stored setting (CLAUDE.md sweep pass 4: a read that only
// copies a value is not consumption).
//
//	control (FR-K1 / K2 / K4)          binding                     test
//	schema budget                      Settings_SetToolExposure    TestToolExposureDial_SchemaBudgetFitsTheNextRequest
//	activation TTL                     Settings_SetToolExposure    TestToolExposureDial_ActivationTTLExpiresInTheNextTurn
//	per-server tier (user layer)       Settings_SetToolExposure    TestToolExposureDial_UserServerAndToolTiersReachTheRequest
//	per-tool tier override (user)      Settings_SetToolExposure    (same test)
//	project tier (Pin for project)     Projects_SetToolExposure    TestToolExposureDial_ProjectTierReachesItsSessionsOnly
//	session Load / Unload              Sessions_LoadTools /        TestToolExposureDial_SessionLoadThenUnload
//	                                   Sessions_SetToolExposure
//	org pin (tier + budget)            signed bundle → fleet state TestToolExposureDial_OrgPinsReachTheRequest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	chat "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// exposureRig is one real chat chassis over real sqlite with the dogfood
// catalog shape attached: outlook 94, filesystem 14, harness-self 7,
// fetch 1, plus every built-in with its Settings dial on.
type exposureRig struct {
	c        *core.Core
	stack    llmStack
	settings *settings.API
	graph    *graphview.Impl
	profID   string

	mu     sync.Mutex
	bodies [][]byte
}

// wireCall is one model call as the provider received it.
type wireCall struct {
	tools  []corellm.ToolSpec
	system string
}

func (w wireCall) names() map[string]bool {
	out := make(map[string]bool, len(w.tools))
	for _, t := range w.tools {
		out[t.Name] = true
	}
	return out
}

// countPrefix counts the sent tools of one server.
func (w wireCall) countPrefix(server string) int {
	n := 0
	for _, t := range w.tools {
		if strings.HasPrefix(t.Name, server+toolexposure.NameSeparator) {
			n++
		}
	}
	return n
}

// digest is the per-call "Available but not loaded" section ("" when
// the call carries none).
func (w wireCall) digest() string {
	i := strings.Index(w.system, "## "+loadtools.DigestHeading)
	if i < 0 {
		return ""
	}
	return w.system[i:]
}

// toolsTokens is the sent tools under the shared estimator — the
// composition's tools_tokens_est for this call.
func (w wireCall) toolsTokens() int { return corellm.ToolsTokens(w.tools) }

type rigOptions struct {
	// orgState, when set, is written as <dataDir>/fleet/tool_exposure_applied.json
	// before the settings API is given a fleet client (the state an
	// applied signed bundle leaves on disk).
	orgState string
}

func newExposureRig(t *testing.T, opts rigOptions) *exposureRig {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	if opts.orgState != "" {
		t.Setenv("HARNESS_FLEET_DISABLED", "")
		if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "fleet", "tool_exposure_applied.json"), []byte(opts.orgState), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	cedarEngine := buildCedarEngineOrNil(dataDir, nil)
	memStore := openMemoryStore(c)
	bashStore := corebash.NewStore()
	graphMgr, _, _, _ := newGraphManagerWithDeps(c, nil, nil, memStore, nil, bashStore, nil, cedarEngine, nil, nil)
	if graphMgr == nil {
		t.Fatal("newGraphManagerWithDeps returned a nil manager")
	}
	fs, err := settings.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, save := range []func(bool) error{fs.SaveWebSearch, fs.SaveBash, fs.SaveWebFetchEnabled, fs.SaveFSReadEnabled, fs.SaveFSWriteEnabled, fs.SaveTodoEnabled} {
		if err := save(true); err != nil {
			t.Fatal(err)
		}
	}
	settingsImpl := settings.NewAPI(fs)
	if opts.orgState != "" {
		fc, ferr := fleet.NewClient(fleet.ClientOpts{DataDir: dataDir})
		if ferr != nil {
			t.Fatalf("fleet.NewClient: %v", ferr)
		}
		settingsImpl.SetFleetClient(fc, dataDir)
	}

	r := &exposureRig{c: c, settings: settingsImpl, graph: graphview.New(graphMgr)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, f := range []string{
			`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"zz-te-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	broker := NewStreamBroker(NewMultiEmitter())
	stack := newLLMStack(c, broker, newPersonalStore(c), nil, nil, func() bool { return false },
		nil, nil, settingsImpl, bashStore, nil, graphMgr, nil, nil, nil, nil,
		nil, nil, nil, nil, confirmAuditEmitter{}, nil, cedarEngine, nil, nil, nil, nil)
	if stack.compactionScheduler != nil {
		t.Cleanup(stack.compactionScheduler.Stop)
	}
	if stack.loadTools == nil || stack.chatRunner == nil {
		t.Fatal("newLLMStack wired no load core / chat runner over a real core and settings")
	}
	r.stack = stack
	registerSyntheticServer(t, stack, "outlook", 94)
	registerSyntheticServer(t, stack, "filesystem", 14)
	registerSyntheticServer(t, stack, harnessmcp.ServerName, 7)
	registerSyntheticServer(t, stack, "fetch", 1)

	stack.reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))
	t.Setenv("ZZ_TOOLDIALS_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{
		ID: "zz-tooldials", Kind: anthropic.Kind, Model: "default",
		Cred: corellm.CredentialReference{Kind: "env", Locator: "ZZ_TOOLDIALS_KEY"},
	}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	r.profID = prof.ID
	return r
}

// session creates a session, in projectID when non-empty.
func (r *exposureRig) session(t *testing.T, projectID string) string {
	t.Helper()
	var pid *string
	if projectID != "" {
		pid = &projectID
	}
	rec, err := r.c.SessionManager().CreateInProject(context.Background(), "zz-tooldials", pid)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

// turn drives one real chat turn on sessionID and returns the first
// model call it sent.
func (r *exposureRig) turn(t *testing.T, sessionID string) wireCall {
	t.Helper()
	ctx := context.Background()
	r.mu.Lock()
	start := len(r.bodies)
	r.mu.Unlock()
	userRow, err := r.c.SessionManager().AppendMessage(ctx, sessionID, session.Message{Role: session.RoleUser, Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := r.stack.chatRunner.StartStream(ctx, r.profID, sessionID, "", chat.UserTurn{MessageID: userRow.ID, Text: "hi", Announce: true})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := r.graph.GetRunStatus(ctx, runID)
		if err == nil && st.State == graphview.RunStateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete: %+v, %v", st, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) <= start {
		t.Fatal("the turn sent no request to the provider")
	}
	return decodeAnthropicWire(t, r.bodies[start])
}

func decodeAnthropicWire(t *testing.T, body []byte) wireCall {
	t.Helper()
	var wire struct {
		System json.RawMessage `json:"system"`
		Tools  []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode wire request: %v", err)
	}
	var out wireCall
	for _, tl := range wire.Tools {
		out.tools = append(out.tools, corellm.ToolSpec{Name: tl.Name, Description: tl.Description, InputSchema: tl.InputSchema})
	}
	if err := json.Unmarshal(wire.System, &out.system); err != nil {
		var blocks []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(wire.System, &blocks); err != nil {
			t.Fatalf("decode system: %v (%s)", err, wire.System)
		}
		for _, bl := range blocks {
			out.system += bl.Text
		}
	}
	return out
}

// load writes a Sessions_LoadTools activation (the binding's core).
func (r *exposureRig) load(t *testing.T, sessionID string, servers []string, sticky bool) loadtools.Result {
	t.Helper()
	res, err := r.stack.loadTools.Load(context.Background(), sessionID,
		loadtools.Request{Servers: servers, Sticky: sticky}, contextaudit.ToolsActivatedByUser)
	if err != nil {
		t.Fatalf("Load(%v): %v", servers, err)
	}
	return res
}

func (r *exposureRig) setUser(t *testing.T, s toolexposure.Settings) {
	t.Helper()
	if err := r.settings.SetToolExposure(context.Background(), s); err != nil {
		t.Fatalf("Settings SetToolExposure(%+v): %v", s, err)
	}
}

func serverTier(server string, tier toolexposure.Tier) toolexposure.Exposure {
	return toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{server: {Tier: tier}}}
}

// TestToolExposureDial_SchemaBudgetFitsTheNextRequest: the schema budget
// setting decides how many loaded tools the next request carries, and
// the request's tools_tokens_est stays at or under it.
func TestToolExposureDial_SchemaBudgetFitsTheNextRequest(t *testing.T) {
	r := newExposureRig(t, rigOptions{})
	sid := r.session(t, "")
	r.load(t, sid, []string{"outlook"}, true)

	atDefault := r.turn(t, sid)
	if atDefault.toolsTokens() > toolexposure.DefaultSchemaBudgetTokens {
		t.Fatalf("default budget: tools_tokens_est %d > %d", atDefault.toolsTokens(), toolexposure.DefaultSchemaBudgetTokens)
	}
	defaultOutlook := atDefault.countPrefix("outlook")
	if defaultOutlook == 0 || defaultOutlook == 94 {
		t.Fatalf("default budget sent %d of 94 loaded outlook tools; want some, fitted to %d tokens",
			defaultOutlook, toolexposure.DefaultSchemaBudgetTokens)
	}

	const budget = 8000
	r.setUser(t, toolexposure.Settings{SchemaBudgetTokens: budget})
	low := r.turn(t, sid)
	if got := low.toolsTokens(); got > budget {
		t.Fatalf("budget %d: next request's tools_tokens_est = %d, over the budget", budget, got)
	}
	lowOutlook := low.countPrefix("outlook")
	if lowOutlook == 0 || lowOutlook >= defaultOutlook {
		t.Fatalf("budget %d sent %d outlook tools (default budget sent %d): the setting did not change the request",
			budget, lowOutlook, defaultOutlook)
	}
	if !strings.Contains(low.digest(), "outlook") {
		t.Errorf("evicted outlook tools are not back in the digest:\n%s", low.digest())
	}
	t.Logf("schema budget: default %d → %d outlook tools (%d tokens); %d → %d outlook tools (%d tokens)",
		toolexposure.DefaultSchemaBudgetTokens, defaultOutlook, atDefault.toolsTokens(), budget, lowOutlook, low.toolsTokens())
}

// TestToolExposureDial_ActivationTTLExpiresInTheNextTurn: a non-sticky
// load is still sent one turn later at the default TTL, and is gone —
// its server back in the digest — at TTL 1.
func TestToolExposureDial_ActivationTTLExpiresInTheNextTurn(t *testing.T) {
	r := newExposureRig(t, rigOptions{})

	// Default TTL (6): two turns after the load the tools are still sent.
	control := r.session(t, "")
	r.load(t, control, []string{"fetch"}, false)
	if got := r.turn(t, control).countPrefix("fetch"); got != 1 {
		t.Fatalf("turn 1 after a fetch load sent %d fetch tools, want 1", got)
	}
	if got := r.turn(t, control).countPrefix("fetch"); got != 1 {
		t.Fatalf("default TTL: turn 2 sent %d fetch tools, want 1 (not yet expired)", got)
	}

	r.setUser(t, toolexposure.Settings{ActivationTTLTurns: 1})
	sid := r.session(t, "")
	r.load(t, sid, []string{"fetch"}, false)
	first := r.turn(t, sid)
	if first.countPrefix("fetch") != 1 {
		t.Fatalf("TTL 1: turn 1 sent %d fetch tools, want 1", first.countPrefix("fetch"))
	}
	second := r.turn(t, sid)
	if got := second.countPrefix("fetch"); got != 0 {
		t.Fatalf("TTL 1: turn 2 still sent %d fetch tools — the TTL setting did not reach the request", got)
	}
	if !strings.Contains(second.digest(), "fetch (1 tool)") {
		t.Errorf("TTL 1: expired fetch is not back in the digest:\n%s", second.digest())
	}
}

// TestToolExposureDial_UserServerAndToolTiersReachTheRequest: the user
// layer's per-server tier (full / off) and per-tool override change the
// next request with no load.
func TestToolExposureDial_UserServerAndToolTiersReachTheRequest(t *testing.T) {
	r := newExposureRig(t, rigOptions{})
	sid := r.session(t, "")
	before := r.turn(t, sid)
	if before.countPrefix("filesystem") != 0 || before.countPrefix("outlook") != 0 {
		t.Fatalf("harness default sent filesystem/outlook tools: %v", before.names())
	}
	if !strings.Contains(before.digest(), "fetch (1 tool)") {
		t.Fatalf("harness default digest lacks fetch:\n%s", before.digest())
	}

	e := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"filesystem": {Tier: toolexposure.TierFull},
		"fetch":      {Tier: toolexposure.TierOff},
		"outlook":    {Tools: map[string]toolexposure.Tier{"tool-03": toolexposure.TierFull}},
	}}
	// 15 full schemas of ~1.5k tokens each do not fit the default 24k
	// budget (pinned tools are then evicted with the composer warning —
	// TestToolExposureDial_SchemaBudgetFitsTheNextRequest's territory), so
	// this test raises it to observe the tier alone.
	r.setUser(t, toolexposure.Settings{Exposure: e, SchemaBudgetTokens: 100_000})
	after := r.turn(t, sid)
	if got := after.countPrefix("filesystem"); got != 14 {
		t.Errorf("user filesystem=full: next request sent %d filesystem tools, want 14", got)
	}
	if got := after.countPrefix("outlook"); got != 1 || !after.names()["outlook"+toolexposure.NameSeparator+"tool-03"] {
		t.Errorf("user outlook tool-03=full: next request sent %d outlook tools (%v), want only tool-03", got, after.names())
	}
	if after.countPrefix("fetch") != 0 || strings.Contains(after.digest(), "fetch (") {
		t.Errorf("user fetch=off: fetch still sent or listed in the digest:\n%s", after.digest())
	}
	if !strings.Contains(after.digest(), "outlook (93 tools)") {
		t.Errorf("digest should list the 93 outlook tools still summary:\n%s", after.digest())
	}
}

// TestToolExposureDial_ProjectTierReachesItsSessionsOnly: a project's
// layer (the Tools menu's "Pin for project") changes the next request of
// a session in that project and of no other session.
func TestToolExposureDial_ProjectTierReachesItsSessionsOnly(t *testing.T) {
	r := newExposureRig(t, rigOptions{})
	ctx := context.Background()
	proj, err := r.c.ProjectManager().Create(ctx, "zz-dials-proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.c.ProjectManager().SetToolExposure(ctx, proj.ID, serverTier("fetch", toolexposure.TierFull)); err != nil {
		t.Fatalf("project SetToolExposure: %v", err)
	}
	inProj := r.session(t, proj.ID)
	loose := r.session(t, "")
	if got := r.turn(t, inProj).countPrefix("fetch"); got != 1 {
		t.Errorf("project fetch=full: session in the project sent %d fetch tools, want 1", got)
	}
	if got := r.turn(t, loose).countPrefix("fetch"); got != 0 {
		t.Errorf("project fetch=full leaked to a loose session: sent %d fetch tools", got)
	}
}

// TestToolExposureDial_SessionLoadThenUnload: Sessions_LoadTools puts a
// server's schemas in the next request; the Tools menu's Unload (a
// session-layer off) takes them out of the next one and out of the
// digest, while the activation itself survives.
func TestToolExposureDial_SessionLoadThenUnload(t *testing.T) {
	r := newExposureRig(t, rigOptions{})
	ctx := context.Background()
	// Room for all 14 filesystem schemas (~21k tokens) beside the hot set.
	r.setUser(t, toolexposure.Settings{SchemaBudgetTokens: 100_000})
	sid := r.session(t, "")
	if res := r.load(t, sid, []string{"filesystem"}, true); res.LoadedByServer["filesystem"] != 14 {
		t.Fatalf("Load(filesystem) = %+v, want 14 filesystem tools loaded", res)
	}
	if got := r.turn(t, sid).countPrefix("filesystem"); got != 14 {
		t.Fatalf("after Load: next request sent %d filesystem tools, want 14", got)
	}
	if err := r.c.SessionManager().SetToolExposure(ctx, sid, serverTier("filesystem", toolexposure.TierOff)); err != nil {
		t.Fatalf("session SetToolExposure(filesystem off): %v", err)
	}
	after := r.turn(t, sid)
	if got := after.countPrefix("filesystem"); got != 0 {
		t.Fatalf("after Unload: next request still sent %d filesystem tools", got)
	}
	if strings.Contains(after.digest(), "filesystem (") {
		t.Errorf("after Unload: an off server is listed in the digest (FR-E2):\n%s", after.digest())
	}
	st, err := r.c.SessionManager().SessionToolExposure(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Activations) != 14 {
		t.Errorf("Unload dropped the activations (%d left); Undo unload needs them", len(st.Activations))
	}
	// A second session is untouched by the first one's layer.
	other := r.session(t, "")
	if got := r.turn(t, other).countPrefix("filesystem"); got != 0 {
		t.Errorf("another session sent %d filesystem tools", got)
	}
}

// TestToolExposureDial_OrgPinsReachTheRequest: the organisation's applied
// bundle — a pinned full server, a pinned off server and a pinned schema
// budget — decides the next request over the user's own settings.
func TestToolExposureDial_OrgPinsReachTheRequest(t *testing.T) {
	state := `{"bundle_id":5,"tool_exposure":{"servers":{` +
		`"fetch":{"tier":"full","pinned":true},` +
		`"filesystem":{"tier":"off","pinned":true}},` +
		`"budget_tokens":8000}}`
	r := newExposureRig(t, rigOptions{orgState: state})
	ctx := context.Background()
	sid := r.session(t, "")

	first := r.turn(t, sid)
	if got := first.countPrefix("fetch"); got != 1 {
		t.Errorf("org fetch pinned full: first request sent %d fetch tools, want 1", got)
	}
	if strings.Contains(first.digest(), "filesystem (") {
		t.Errorf("org filesystem pinned off: still listed in the digest:\n%s", first.digest())
	}
	if res := r.load(t, sid, []string{"filesystem"}, true); len(res.Loaded) != 0 || res.LoadedByServer["filesystem"] != 0 || len(res.NotLoaded) == 0 {
		t.Errorf("org-off filesystem: Load = %+v, want every tool refused with a reason", res)
	}

	// The org budget (8000) beats the user's default (24000): a loaded
	// outlook fits to 8000, not 24000.
	if err := r.c.SessionManager().SetToolActivations(ctx, sid, nil); err != nil {
		t.Fatal(err)
	}
	r.load(t, sid, []string{"outlook"}, true)
	next := r.turn(t, sid)
	if got := next.toolsTokens(); got > 8000 {
		t.Errorf("org budget 8000: next request's tools_tokens_est = %d", got)
	}
	if next.countPrefix("outlook") == 0 {
		t.Errorf("org budget 8000: no outlook tool fitted at all: %v", next.names())
	}
	if next.countPrefix("filesystem") != 0 {
		t.Errorf("org filesystem pinned off: sent anyway: %v", next.names())
	}
}
