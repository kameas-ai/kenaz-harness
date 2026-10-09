package rpc

// On-demand tool exposure through the production wiring
// (tool-context-budget-01TCBUD01 WP03): newLLMStack's real chat runner,
// tool discoverer, dispatch pool and anthropic adapter (over an httptest
// SSE fixture that records the wire request), and New()'s real API over
// real sqlite.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	chat "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// syntheticMCPToolTokens is the per-tool schema size the synthetic MCP
// servers below carry: the 2026-10-08 dogfood measured 224,798 prompt
// tokens for 143 tools on a near-empty first turn (spec §0), about 1,570
// tokens per tool. Built-in schemas are the real ones.
const syntheticMCPToolTokens = 1500

// registerSyntheticServer attaches an in-process MCP server with n tools
// whose schema carries syntheticMCPToolTokens tokens of description text
// (4 runes per token under the shared estimator), so each tool estimates
// to just over that.
func registerSyntheticServer(t *testing.T, stack llmStack, name string, n int) {
	t.Helper()
	srv := toolserver.NewServer(name, "1.0.0")
	for i := 0; i < n; i++ {
		tool := fmt.Sprintf("tool-%02d", i)
		schema := fmt.Sprintf(`{"type":"object","properties":{"q":{"type":"string","description":%q}}}`,
			strings.Repeat("x", syntheticMCPToolTokens*4))
		srv.Register(toolserver.ToolSpec{
			Name:        tool,
			Description: "Does " + tool + ".",
			InputSchema: json.RawMessage(schema),
			Handler: func(context.Context, json.RawMessage) (any, error) {
				return map[string]bool{"ok": true}, nil
			},
		})
	}
	if err := stack.dispatchPool.RegisterInProcess(context.Background(), name, harnessmcp.NewTransport(srv)); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
}

// TestToolExposureWiring_FirstTurnToolTokens drives one real first turn
// with the dogfood catalog shape — outlook 94, filesystem 14,
// harness-self 7, fetch 1, plus every built-in with its Settings dial on
// — and measures what the wire request carried against what the whole
// catalog costs. Only the hot set travels in full; everything else is a
// line in kenaz__load_tools' description.
func TestToolExposureWiring_FirstTurnToolTokens(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
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

	var (
		mu     sync.Mutex
		bodies [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, f := range []string{
			`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"zz-te-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
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
	registerSyntheticServer(t, stack, "outlook", 94)
	registerSyntheticServer(t, stack, "filesystem", 14)
	registerSyntheticServer(t, stack, harnessmcp.ServerName, 7)
	registerSyntheticServer(t, stack, "fetch", 1)

	stack.reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))
	t.Setenv("ZZ_TOOLEXPOSURE_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{
		ID: "zz-toolexposure", Kind: anthropic.Kind, Model: "default",
		Cred: corellm.CredentialReference{Kind: "env", Locator: "ZZ_TOOLEXPOSURE_KEY"},
	}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	ctx := context.Background()
	rec, err := c.SessionManager().Create(ctx, "zz-toolexposure")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := stack.toolDiscoverer.Tools(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	perServer := map[string]int{}
	for _, s := range catalog {
		perServer[s.Server]++
	}
	if perServer["outlook"] != 94 || perServer["filesystem"] != 14 || perServer[harnessmcp.ServerName] != 7 || perServer["fetch"] != 1 {
		t.Fatalf("discovered catalog by server = %v", perServer)
	}
	before := corellm.ToolsTokens(catalog)

	userRow, err := c.SessionManager().AppendMessage(ctx, rec.ID, session.Message{Role: session.RoleUser, Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := stack.chatRunner.StartStream(ctx, prof.ID, rec.ID, "", chat.UserTurn{MessageID: userRow.ID, Text: "hi", Announce: true})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	gapi := graphview.New(graphMgr)
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := gapi.GetRunStatus(ctx, runID)
		if err == nil && st.State == graphview.RunStateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete: %+v, %v", st, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	if len(bodies) == 0 {
		mu.Unlock()
		t.Fatal("no request reached the provider")
	}
	first := bodies[0]
	mu.Unlock()
	var wire struct {
		System   json.RawMessage   `json:"system"`
		Messages []json.RawMessage `json:"messages"`
		Tools    []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(first, &wire); err != nil {
		t.Fatalf("decode wire request: %v", err)
	}
	var sent []corellm.ToolSpec
	for _, tl := range wire.Tools {
		sent = append(sent, corellm.ToolSpec{Name: tl.Name, Description: tl.Description, InputSchema: tl.InputSchema})
		if !toolexposure.InHotSet(tl.Name) {
			t.Errorf("first turn sent %q, which is not in the hot set", tl.Name)
		}
	}
	// The system field as sent (a string or text blocks); its text
	// carries the digest as the last section.
	var systemText string
	if err := json.Unmarshal(wire.System, &systemText); err != nil {
		var blocks []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(wire.System, &blocks); err != nil {
			t.Fatalf("decode system: %v (%s)", err, wire.System)
		}
		for _, bl := range blocks {
			systemText += bl.Text
		}
	}
	i := strings.Index(systemText, "## "+loadtools.DigestHeading)
	if i < 0 {
		t.Fatalf("first-turn system prompt has no digest section:\n%s", systemText)
	}
	digest := systemText[i:]
	for _, want := range []string{"outlook (94 tools)", "filesystem (14 tools)", "harness-self (7 tools)", "fetch (1 tool)"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
	after := corellm.ToolsTokens(sent)
	// The whole first-turn prompt — system (digest included), tools and
	// the one user message — under the shared estimator (AC1).
	whole := corellm.EstimateTokens(systemText) + after
	for _, m := range wire.Messages {
		whole += corellm.EstimateTokens(string(m))
	}
	t.Logf("first-turn tool schemas: before %d tools ≈ %d tokens; after %d tools ≈ %d tokens; digest %d chars; whole prompt ≈ %d tokens",
		len(catalog), before, len(sent), after, len(digest), whole)
	if after >= 15000 || before < 10*after {
		t.Fatalf("tool tokens before %d, after %d: want after < 15000 and a >=10x cut", before, after)
	}
	if whole >= 15000 {
		t.Fatalf("whole first-turn prompt ≈ %d tokens, want < 15000 (AC1)", whole)
	}
}

// TestToolExposureWiring_LoadToolsAndGuardThroughNew drives New()'s real
// API over real sqlite: Sessions_LoadTools' core persists a sticky
// activation that survives a restart and writes an audit row, and all
// three exposure writers refuse turning kenaz__load_tools off while
// summary tools exist (FR-E3) — proving New() installed the loader and
// the guard.
func TestToolExposureWiring_LoadToolsAndGuardThroughNew(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	ctx := context.Background()

	rec, err := c.SessionManager().Create(ctx, "zz-load")
	if err != nil {
		t.Fatal(err)
	}
	res, err := api.Sessions().LoadTools(ctx, rec.ID, nil, []string{"kenaz__sleep", "nosuch__x"}, true)
	if err != nil {
		t.Fatalf("LoadTools: %v", err)
	}
	if len(res.Loaded) != 1 || res.Loaded[0] != "kenaz__sleep" ||
		len(res.NotLoaded) != 1 || res.NotLoaded[0].Name != "nosuch__x" || res.NotLoaded[0].Reason != loadtools.ReasonUnknown {
		t.Fatalf("LoadTools result = %+v", res)
	}
	entry, ok := findAuditEntry(t, api, string(contextaudit.KindToolsActivated))
	if !ok {
		t.Fatal("no tools.activated row reached the persisted audit store")
	}
	if !strings.Contains(entry.Trailing, `"by":"user"`) || !strings.Contains(entry.Trailing, `"sticky":true`) {
		t.Errorf("audit row = %+v, want by user, sticky", entry)
	}

	off := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		toolexposure.BuiltinServer: {Tools: map[string]toolexposure.Tier{"load_tools": toolexposure.TierOff}},
	}}
	if err := api.Settings().SetToolExposure(ctx, toolexposure.Settings{Exposure: off}); !errors.Is(err, toolexposure.ErrLoadToolsRequired) {
		t.Errorf("Settings SetToolExposure(load_tools off) err = %v, want ErrLoadToolsRequired", err)
	}
	if got, _ := api.Settings().GetToolExposure(ctx); !got.Exposure.IsZero() {
		t.Errorf("refused write was stored: %+v", got.Exposure)
	}
	if err := api.Sessions().SetToolExposure(ctx, rec.ID, off); !errors.Is(err, toolexposure.ErrLoadToolsRequired) {
		t.Errorf("Sessions SetToolExposure(load_tools off) err = %v, want ErrLoadToolsRequired", err)
	}
	proj, err := c.ProjectManager().Create(ctx, "zz-proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Projects().SetToolExposure(ctx, proj.ID, off); !errors.Is(err, toolexposure.ErrLoadToolsRequired) {
		t.Errorf("Projects SetToolExposure(load_tools off) err = %v, want ErrLoadToolsRequired", err)
	}

	// Restart: the sticky activation was written through real sqlite.
	api.Shutdown()
	if err := c.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	c2, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = c2.Shutdown(context.Background()) })
	st, err := c2.SessionManager().SessionToolExposure(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Activations) != 1 || st.Activations[0].Name != "kenaz__sleep" || !st.Activations[0].Sticky {
		t.Fatalf("activations after reopen = %+v, want kenaz__sleep sticky", st.Activations)
	}
}

// TestScheduledRunContainment_LoadToolsIsNotContained: a contained run
// may call kenaz__load_tools (it only loads tools the contained catalog
// already lists); every other off-list tool is still refused.
func TestScheduledRunContainment_LoadToolsIsNotContained(t *testing.T) {
	r := NewScheduledRunContainmentRegistry(nil)
	r.Contain("s1", "run-1", []string{"fetch__fetch"})
	ctx := context.Background()
	if _, denied := r.Check(ctx, "s1", toolexposure.BuiltinServer, "load_tools"); denied {
		t.Fatal("kenaz__load_tools denied in a contained run")
	}
	if _, denied := r.Check(ctx, "s1", "fetch", "fetch"); denied {
		t.Fatal("allowlisted tool denied")
	}
	if _, denied := r.Check(ctx, "s1", "outlook", "send-mail"); !denied {
		t.Fatal("off-list tool allowed")
	}
}
