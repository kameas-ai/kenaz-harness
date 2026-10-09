package rpc

// Acceptance criterion 3 of tool-context-budget-01TCBUD01 (WP08): a
// 131k-window model completes a scheduled chat that uses harness-self
// tools.
//
// The 2026-10-08 dogfood failure, reproduced end to end and inverted: a
// scheduled chat on OpenRouter `aion-labs/aion-2.0` (context_length
// 131072) with Outlook 94 + Filesystem 14 + harness-self 7 + Fetch 1
// installed. Before the mission every request carried ~225k provider
// tokens of schemas and the provider refused it on an
// empty session. Here the run goes through the production pieces:
// ChatRunDispatcher → the llm view's StartStream → newLLMStack's chat
// runner, exposure partition, OpenRouter adapter (whose live /models list
// supplies the 131k window the schema budget is capped against) → an
// httptest provider that REFUSES any request over 131,072 tokens counted
// at 2.5 bytes per token — the density the dogfood's recorded frames
// showed for tool-definition JSON (composition_recorded_frames_test.go),
// which is also the estimator's schema rule. The model loads harness-self
// from the digest, calls a harness-self tool, and answers.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

const (
	smallWindowModel  = "aion-labs/aion-2.0"
	smallWindowTokens = 131072
	// providerBytesPerToken is how densely the provider is assumed to
	// tokenize the request (see the file header).
	providerBytesPerToken = 2.5
)

func TestToolExposureAcceptance_131kWindowScheduledChatUsesHarnessSelf(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	cedarEngine := buildCedarEngineOrNil(dataDir, nil)
	bashStore := corebash.NewStore()
	graphMgr, _, _, _ := newGraphManagerWithDeps(c, nil, nil, openMemoryStore(c), nil, bashStore, nil, cedarEngine, nil, nil)
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

	// The provider: /models lists the 131k model; /chat/completions
	// enforces the window and plays load → call → answer.
	var (
		mu       sync.Mutex
		calls    int
		sizes    []int
		toolSets [][]string
		refused  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":[{"id":%q,"name":"Aion 2.0","context_length":%d,"pricing":{"prompt":"0.000001","completion":"0.000002"}}]}`,
				smallWindowModel, smallWindowTokens)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(body, &req)
		var names []string
		for _, tl := range req.Tools {
			names = append(names, tl.Function.Name)
		}
		mu.Lock()
		calls++
		n := calls
		sizes = append(sizes, len(body))
		toolSets = append(toolSets, names)
		mu.Unlock()
		if float64(len(body))/providerBytesPerToken > smallWindowTokens {
			mu.Lock()
			refused++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":{"message":"This endpoint's maximum context length is %d tokens.","code":400}}`, smallWindowTokens)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		var frames []string
		toolCall := func(id, name, args string) []string {
			return []string{
				fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`, id, name, args),
				`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":5}}`,
			}
		}
		switch n {
		case 1:
			frames = toolCall("call_1", loadtools.Name, `{"servers":["harness-self"]}`)
		case 2:
			frames = toolCall("call_2", harnessmcp.ServerName+toolexposure.NameSeparator+"harness_read_list_sessions", `{}`)
		default:
			frames = []string{
				`{"choices":[{"index":0,"delta":{"role":"assistant","content":"You have no other sessions."},"finish_reason":null}]}`,
				`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":7}}`,
			}
		}
		frames = append(frames, `[DONE]`)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	bus := NewEventBus()
	broker := NewStreamBroker(NewMultiEmitter(&busEmitter{bus: bus}))
	stack := newLLMStack(c, broker, newPersonalStore(c), nil, nil, func() bool { return false },
		nil, nil, settingsImpl, bashStore, nil, graphMgr, nil, nil, nil, nil,
		nil, nil, nil, nil, confirmAuditEmitter{}, nil, cedarEngine, nil, nil, nil, nil)
	if stack.compactionScheduler != nil {
		t.Cleanup(stack.compactionScheduler.Stop)
	}
	if stack.loadTools == nil || stack.chatRunner == nil || stack.api == nil {
		t.Fatal("newLLMStack wired no load core / chat runner / llm view")
	}
	registerSyntheticServer(t, stack, "outlook", 94)
	registerSyntheticServer(t, stack, "filesystem", 14)
	registerSyntheticServer(t, stack, "fetch", 1)
	var listCalls int
	var listMu sync.Mutex
	hs := toolserver.NewServer(harnessmcp.ServerName, "1.0.0")
	for _, name := range []string{
		"harness_read_list_sessions", "harness_read_list_models", "harness_read_list_providers",
		"harness_read_list_settings", "harness_read_list_mcp_recipes",
		"harness_read_get_onboarding_recommendations", "harness_read_materialize_run",
	} {
		name := name
		hs.Register(toolserver.ToolSpec{
			Name:        name,
			Description: "Reads the harness: " + name + ".",
			InputSchema: json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"q":{"type":"string","description":%q}}}`,
				syntheticSchemaPadding())),
			Handler: func(context.Context, json.RawMessage) (any, error) {
				if name == "harness_read_list_sessions" {
					listMu.Lock()
					listCalls++
					listMu.Unlock()
				}
				return map[string]any{"sessions": []string{}}, nil
			},
		})
	}
	if err := stack.dispatchPool.RegisterInProcess(context.Background(), harnessmcp.ServerName, harnessmcp.NewTransport(hs)); err != nil {
		t.Fatalf("register harness-self: %v", err)
	}

	ad := openrouter.New(openrouter.WithEndpoint(srv.URL + "/api/v1/chat/completions"))
	stack.reg.RegisterAdapter(ad)
	if _, err := ad.ListModels(context.Background(), nil); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	t.Setenv("ZZ_SMALLWINDOW_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{
		ID: "zz-smallwindow", Kind: openrouter.Kind, Model: smallWindowModel,
		Cred: corellm.CredentialReference{Kind: "env", Locator: "ZZ_SMALLWINDOW_KEY"},
	}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	// The whole catalog would not fit: the pre-mission request.
	ctx := context.Background()
	probe, err := c.SessionManager().Create(ctx, "zz-probe")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := stack.toolDiscoverer.Tools(ctx, probe.ID)
	if err != nil {
		t.Fatal(err)
	}
	full := corellm.ToolsTokens(catalog)
	if full <= smallWindowTokens {
		t.Fatalf("whole catalog ≈ %d tokens fits %d: the scenario is not the dogfood's", full, smallWindowTokens)
	}

	store := scheduler.NewSQLiteChatStore(c.Storage())
	now := time.Now().UTC()
	if err := store.Create(ctx, scheduler.ChatRunRecord{
		ID: "cr-131k", Name: "Dogfood sentinel", PromptTemplate: "How many sessions do I have?",
		Cron: "*/3 * * * *", OutputSink: "none", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	d := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          store,
		Sessions:       sessionsview.NewManagerAPI(c.SessionManager()),
		LLM:            stack.api,
		Bus:            bus,
		DefaultProfile: func() string { return prof.ID },
		DefaultModel:   func(string) string { return smallWindowModel },
		Timeout:        20 * time.Second,
	})
	logBuf := &lockedLogBuffer{}
	logging.Replace(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	t.Cleanup(func() { logging.Replace(logging.FileHandler()) })
	rec, err := d.DispatchChatRun(ctx, scheduler.Job{ID: "cr-131k", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: "cr-131k"}}, now)
	if err != nil {
		t.Fatalf("DispatchChatRun: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, sz := range sizes {
		t.Logf("call %d: %d bytes ≈ %d provider tokens at %.1f B/token; %d tools", i+1, sz, int(float64(sz)/providerBytesPerToken), providerBytesPerToken, len(toolSets[i]))
	}
	t.Logf("whole catalog: %d tools ≈ %d estimated tokens (> %d window)", len(catalog), full, smallWindowTokens)
	if rec.Status != "completed" {
		t.Fatalf("scheduled run = %+v (refused %d calls), want completed", rec, refused)
	}
	if refused != 0 {
		t.Fatalf("%d requests exceeded the %d-token window", refused, smallWindowTokens)
	}
	if calls != 3 {
		t.Fatalf("model calls = %d, want 3 (load, harness-self call, answer)", calls)
	}
	hsName := harnessmcp.ServerName + toolexposure.NameSeparator + "harness_read_list_sessions"
	has := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}
	if has(toolSets[0], hsName) || !has(toolSets[0], loadtools.Name) {
		t.Errorf("call 1 tools = %v: want kenaz__load_tools and no harness-self schema yet", toolSets[0])
	}
	if !has(toolSets[1], hsName) {
		t.Errorf("call 2 tools = %v: kenaz__load_tools' activation did not reach the next call", toolSets[1])
	}
	listMu.Lock()
	defer listMu.Unlock()
	if listCalls != 1 {
		t.Errorf("harness-self harness_read_list_sessions ran %d times, want 1", listCalls)
	}
	// Every call was fitted to 15 % of the window the adapter's live
	// /models entry reported, not to the 24k setting.
	want := toolexposure.EffectiveBudget(0, smallWindowTokens)
	var budgets []float64
	for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
		var recLine map[string]any
		if json.Unmarshal([]byte(line), &recLine) == nil && recLine["msg"] == "llm.request.composition" {
			b, _ := recLine["budget"].(float64)
			budgets = append(budgets, b)
		}
	}
	if len(budgets) != 3 {
		t.Errorf("llm.request.composition lines = %d, want 3", len(budgets))
	}
	for i, b := range budgets {
		if int(b) != want {
			t.Errorf("call %d schema budget = %v, want %d (15 %% of %d)", i+1, b, want, smallWindowTokens)
		}
	}
	if !strings.Contains(rec.OutputSnippet, "no other sessions") {
		t.Errorf("OutputSnippet = %q, want the model's answer", rec.OutputSnippet)
	}
}

// lockedLogBuffer is a goroutine-safe log sink.
type lockedLogBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
