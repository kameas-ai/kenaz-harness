package dispatch

// dogfood 2026-10-08 round 2: filesystem__list_directory and fetch__fetch
// were advertised to the model on every request and failed every call
// with "stdio: server not in pool"; Capabilities showed the servers
// STOPPED with 0 tools; the app log had no line for the failure.
//
// Root cause: the persisted-recipe bootstrap opens recipes on the stdio
// sub-pool DIRECTLY (makeMCPRecipeBootstrap takes *stdio.Pool), so the
// dispatch pool never recorded ownership — Tools (which reads the
// sub-pools) listed them, Call/RecipeStatus/ServerTools (which read the
// ownership map) did not know them. A second path to the same state:
// dispatch.Open dropped ownership for a whole transport bucket when any
// one spec in it failed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/stdio"
)

// statefulStdioPool is a StdioSubPool whose servers carry a lifecycle
// state, and whose bulk Open can fail some specs while others come up —
// both as *stdio.Pool does.
type statefulStdioPool struct {
	mu      sync.Mutex
	states  map[string]string // server -> "running" | "failed" | "stopped"
	failing map[string]bool   // spec names Open refuses
}

func newStatefulStdioPool() *statefulStdioPool {
	return &statefulStdioPool{states: map[string]string{}, failing: map[string]bool{}}
}

func (f *statefulStdioPool) Open(_ context.Context, specs []coremcp.ServerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var errs []string
	for _, s := range specs {
		if f.failing[s.Name] {
			errs = append(errs, s.Name+": spawn failed")
			continue
		}
		f.states[s.Name] = "running"
	}
	if len(errs) > 0 {
		return errors.New("stdio: " + strings.Join(errs, "; "))
	}
	return nil
}
func (f *statefulStdioPool) OpenOne(ctx context.Context, spec coremcp.ServerSpec) error {
	return f.Open(ctx, []coremcp.ServerSpec{spec})
}
func (f *statefulStdioPool) Close(context.Context) error { return nil }
func (f *statefulStdioPool) CloseOne(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.states, id)
	return nil
}
func (f *statefulStdioPool) Tools(context.Context) ([]coremcp.Tool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []coremcp.Tool
	for name := range f.states {
		// A crashed server keeps its cached tools/list, as a real
		// ServerInstance does.
		out = append(out, coremcp.Tool{Server: name, Name: "do"})
	}
	return out, nil
}
func (f *statefulStdioPool) Call(_ context.Context, server, _ string, _ json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states[server] != "running" {
		return nil, errors.New("stdio: server " + server + " is " + f.states[server])
	}
	return json.RawMessage(`"ok"`), nil
}
func (f *statefulStdioPool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[id]
	if !ok {
		return stdio.RecipeStatus{}, false
	}
	return stdio.RecipeStatus{ID: id, Enabled: true, State: st}, true
}
func (f *statefulStdioPool) ServerTools(id string) []coremcp.Tool {
	return []coremcp.Tool{{Server: id, Name: "do"}}
}
func (f *statefulStdioPool) AllRecipeStatuses() []stdio.RecipeStatus { return nil }

func toolServers(tools []coremcp.Tool) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		out[t.Server] = true
	}
	return out
}

// The bootstrap path: servers opened on the sub-pool directly are
// callable through the dispatch pool, report their status, and list
// their tools — instead of "server not in pool" / STOPPED / 0 tools.
func TestDispatch_ServerOpenedOnSubPoolDirectlyIsCallable(t *testing.T) {
	t.Parallel()
	sp := newStatefulStdioPool()
	d := New(Options{Stdio: sp})
	// What makeMCPRecipeBootstrap does: Open on *stdio.Pool, not on d.
	if err := sp.Open(context.Background(), []coremcp.ServerSpec{{Name: "filesystem"}, {Name: "fetch"}}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := d.Call(context.Background(), "fetch", "fetch", nil); err != nil {
		t.Fatalf("Call(fetch) = %v, want success — the server is running in the sub-pool", err)
	}
	if st, ok := d.RecipeStatus("filesystem"); !ok || st.State != "running" {
		t.Errorf("RecipeStatus(filesystem) = %+v ok=%v, want running", st, ok)
	}
	if got := d.ServerTools("filesystem"); len(got) == 0 {
		t.Errorf("ServerTools(filesystem) is empty, want the cached list")
	}
	tools, _ := d.Tools(context.Background())
	if s := toolServers(tools); !s["filesystem"] || !s["fetch"] {
		t.Errorf("Tools servers = %v, want filesystem and fetch", s)
	}
}

// One failing spec must not strip ownership from the healthy servers
// opened in the same bucket.
func TestDispatch_PartialOpenKeepsHealthyServersCallable(t *testing.T) {
	t.Parallel()
	sp := newStatefulStdioPool()
	sp.failing["broken-recipe"] = true
	d := New(Options{Stdio: sp})
	err := d.Open(context.Background(), []coremcp.ServerSpec{{Name: "filesystem"}, {Name: "broken-recipe"}})
	if err == nil {
		t.Fatal("Open: want the aggregated failure for broken-recipe")
	}
	d.mu.RLock()
	tag, owned := d.ownership["filesystem"]
	_, brokenOwned := d.ownership["broken-recipe"]
	d.mu.RUnlock()
	if !owned || tag != "stdio" {
		t.Fatalf("filesystem ownership = %q/%v after a partial Open, want stdio", tag, owned)
	}
	if brokenOwned {
		t.Errorf("broken-recipe owned though it never came up")
	}
	if _, err := d.Call(context.Background(), "filesystem", "do", nil); err != nil {
		t.Errorf("Call(filesystem) = %v, want success", err)
	}
}

// Tools never advertises a tool whose server cannot answer: failed or
// stopped servers keep a cached tool list in their sub-pool, but every
// call to them fails.
func TestDispatch_ToolsOmitsFailedAndStoppedServers(t *testing.T) {
	t.Parallel()
	sp := newStatefulStdioPool()
	d := New(Options{Stdio: sp})
	if err := d.Open(context.Background(), []coremcp.ServerSpec{{Name: "ok"}, {Name: "crashed"}, {Name: "halted"}}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	sp.mu.Lock()
	sp.states["crashed"] = "failed"
	sp.states["halted"] = "stopped"
	sp.mu.Unlock()

	tools, err := d.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	s := toolServers(tools)
	if !s["ok"] {
		t.Errorf("running server's tools missing: %v", s)
	}
	if s["crashed"] || s["halted"] {
		t.Errorf("Tools advertised an unservable server's tools: %v", s)
	}
}

// A failed call leaves a mcp.tool.call.failed line naming server, tool
// and error — and never the arguments.
func TestDispatch_CallFailureIsLogged(t *testing.T) { //nolint:paralleltest // swaps the process logger
	var buf bytes.Buffer
	prev := logging.L().Handler()
	logging.Replace(slog.NewJSONHandler(&buf, nil))
	t.Cleanup(func() { logging.Replace(prev) })

	d := New(Options{Stdio: newStatefulStdioPool()})
	_, err := d.Call(context.Background(), "fetch", "fetch", json.RawMessage(`{"url":"https://secret.example/token"}`))
	if !errors.Is(err, stdio.ErrServerNotFound) {
		t.Fatalf("Call(unknown) = %v, want ErrServerNotFound", err)
	}
	line := buf.String()
	for _, want := range []string{`"msg":"mcp.tool.call.failed"`, `"server":"fetch"`, `"tool":"fetch"`, "server not in pool"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q missing %s", line, want)
		}
	}
	if strings.Contains(line, "secret.example") {
		t.Errorf("log carries the call arguments: %q", line)
	}
}
