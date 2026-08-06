package connectors

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// fakePool records OpenOne specs; a per-id error is injectable.
type fakePool struct {
	mu     sync.Mutex
	specs  []coremcp.ServerSpec
	failID string
}

func (p *fakePool) OpenOne(_ context.Context, spec coremcp.ServerSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.specs = append(p.specs, spec)
	if spec.Name == p.failID {
		return context.DeadlineExceeded
	}
	return nil
}

func (p *fakePool) snapshot() []coremcp.ServerSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]coremcp.ServerSpec, len(p.specs))
	copy(out, p.specs)
	return out
}

// captureConn satisfies net.Conn; writes land in a shared buffer.
type captureConn struct {
	mu  *sync.Mutex
	buf *[]byte
}

func (c captureConn) Read([]byte) (int, error) { return 0, nil }
func (c captureConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.buf = append(*c.buf, b...)
	return len(b), nil
}
func (c captureConn) Close() error                       { return nil }
func (c captureConn) LocalAddr() net.Addr                { return nil }
func (c captureConn) RemoteAddr() net.Addr               { return nil }
func (c captureConn) SetDeadline(time.Time) error        { return nil }
func (c captureConn) SetReadDeadline(time.Time) error    { return nil }
func (c captureConn) SetWriteDeadline(time.Time) error   { return nil }

// captureLedger returns an enabled emitter whose emitted NDJSON lines can
// be decoded from the shared buffer.
func captureLedger() (*LedgerEmitter, func() []map[string]any) {
	var mu sync.Mutex
	var buf []byte
	e := newLedgerEmitter("test-sock", "unix", "wb-test", slog.Default())
	e.dialFn = func(string, string) (net.Conn, error) {
		return captureConn{mu: &mu, buf: &buf}, nil
	}
	return e, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(buf)), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Kind    string         `json:"kind"`
				Source  string         `json:"source"`
				Payload map[string]any `json:"payload"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			p := rec.Payload
			p["_kind"] = rec.Kind
			p["_source"] = rec.Source
			out = append(out, p)
		}
		return out
	}
}

func testCatalog() func() *recipes.Catalog {
	return func() *recipes.Catalog {
		return &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{
			{
				ID:          "datadog",
				DisplayName: "Datadog",
				Category:    "observability",
				Transport:   "http",
				URL:         "https://api.datadoghq.com/mcp",
				EnvKeys:     []recipes.EnvKey{{Name: "DD_API_KEY", Required: true}},
			},
			{
				ID:          "google-drive",
				DisplayName: "Google Drive",
				Category:    "files",
				Command:     []string{"npx", "gdrive-mcp"},
				EnvKeys:     []recipes.EnvKey{{Name: "GDRIVE_TOKEN", Required: true}},
			},
			{
				ID:          "slack",
				DisplayName: "Slack",
				Category:    "communication",
				Transport:   "http",
				URL:         "https://mcp.slack.com/mcp",
				Auth:        &recipes.RecipeAuth{Kind: recipes.AuthKindMCPOAuth},
			},
		}}
	}
}

type fakeTokens struct{ token string }

func (f fakeTokens) ConnectorToken(context.Context, string) (string, error) {
	return f.token, nil
}

func phases(events []map[string]any, phase string) []map[string]any {
	var out []map[string]any
	for _, e := range events {
		if e["phase"] == phase {
			out = append(out, e)
		}
	}
	return out
}

// TestSupervisor_Bootstrap covers the whitelist-driven boot: catalog
// resolution, env de-namespacing + isolation, OAuth broker token
// injection, unknown-id warn-and-drop, and the FR-014 ledger events.
func TestSupervisor_Bootstrap(t *testing.T) {
	pool := &fakePool{}
	ledger, drain := captureLedger()
	env := map[string]string{
		"MCP_DATADOG__DD_API_KEY":    "dd-secret",
		"MCP_GOOGLE_DRIVE__GDRIVE_TOKEN": "gd-secret",
	}
	sup := NewSupervisor(SupervisorConfig{
		Provisioning: Provisioning{Provisioned: true,
			IDs: []string{"datadog", "google-drive", "slack", "ghost"}},
		Getenv:  func(k string) string { return env[k] },
		Tokens:  fakeTokens{token: "broker-token"},
		Ledger:  ledger,
		Catalog: testCatalog(),
		Logger:  slog.Default(),
	})
	sup.SetPool(pool)

	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	specs := pool.snapshot()
	if len(specs) != 3 {
		t.Fatalf("opened %d specs, want 3 (ghost must be dropped)", len(specs))
	}
	byName := map[string]coremcp.ServerSpec{}
	for _, s := range specs {
		byName[s.Name] = s
		if !s.IsolateEnv {
			t.Errorf("spec %q spawned without IsolateEnv", s.Name)
		}
	}
	// Env isolation across connectors.
	if byName["datadog"].Env["DD_API_KEY"] != "dd-secret" {
		t.Error("datadog env grant not resolved")
	}
	if _, leaked := byName["datadog"].Env["GDRIVE_TOKEN"]; leaked {
		t.Error("google-drive secret leaked into datadog spec")
	}
	if _, leaked := byName["google-drive"].Env["DD_API_KEY"]; leaked {
		t.Error("datadog secret leaked into google-drive spec")
	}
	// OAuth broker token injection (D8).
	if got := byName["slack"].HeadersTemplate["Authorization"]; got != "Bearer broker-token" {
		t.Errorf("slack Authorization = %q, want broker bearer", got)
	}

	// States: whitelist order, ghost unavailable.
	states := sup.States()
	if len(states) != 4 {
		t.Fatalf("States() len = %d, want 4", len(states))
	}
	if states[3].ID != "ghost" || states[3].Available || states[3].SpawnState != SpawnStateUnavailable ||
		states[3].Reason != ReasonUnknownRecipe {
		t.Errorf("ghost state = %+v, want unavailable/unknown_recipe", states[3])
	}
	for _, st := range states[:3] {
		if !st.Enabled || st.SpawnState != SpawnStateOK {
			t.Errorf("state %+v, want enabled+ok", st)
		}
	}

	// Ledger: three enabled events, three spawn ok events; ghost got none.
	events := drain()
	if got := len(phases(events, "connector.enabled")); got != 3 {
		t.Errorf("connector.enabled events = %d, want 3", got)
	}
	spawns := phases(events, "connector.spawn")
	if len(spawns) != 3 {
		t.Fatalf("connector.spawn events = %d, want 3", len(spawns))
	}
	for _, s := range spawns {
		if s["result"] != "ok" {
			t.Errorf("spawn result = %v, want ok", s["result"])
		}
		if s["workbench_id"] != "wb-test" {
			t.Errorf("workbench_id = %v", s["workbench_id"])
		}
	}
	// Privacy: no credential bytes anywhere in the ledger stream.
	for _, e := range events {
		for _, v := range e {
			if s, ok := v.(string); ok && (strings.Contains(s, "secret") || strings.Contains(s, "broker-token")) {
				t.Errorf("credential material in ledger event: %v", e)
			}
		}
	}
}

// TestSupervisor_MissingRequiredEnv verifies a connector whose required
// grant is absent fails visibly (missing_env) without blocking the boot,
// and its spawn failure reaches the ledger as a reason class.
func TestSupervisor_MissingRequiredEnv(t *testing.T) {
	pool := &fakePool{}
	ledger, drain := captureLedger()
	sup := NewSupervisor(SupervisorConfig{
		Provisioning: Provisioning{Provisioned: true, IDs: []string{"datadog"}},
		Getenv:       func(string) string { return "" },
		Ledger:       ledger,
		Catalog:      testCatalog(),
	})
	sup.SetPool(pool)
	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(pool.snapshot()) != 0 {
		t.Error("spawned a connector with a missing required grant")
	}
	st := sup.States()[0]
	if st.SpawnState != SpawnStateFailed || st.Reason != ReasonMissingEnv {
		t.Errorf("state = %+v, want failed/missing_env", st)
	}
	spawns := phases(drain(), "connector.spawn")
	if len(spawns) != 1 || spawns[0]["result"] != "fail" || spawns[0]["reason"] != ReasonMissingEnv {
		t.Errorf("spawn event = %v, want fail/missing_env", spawns)
	}
}

// TestSupervisor_OpenFailure verifies a pool failure is warn-and-drop with
// the open_failed class — never a boot error.
func TestSupervisor_OpenFailure(t *testing.T) {
	pool := &fakePool{failID: "datadog"}
	env := map[string]string{"MCP_DATADOG__DD_API_KEY": "x"}
	sup := NewSupervisor(SupervisorConfig{
		Provisioning: Provisioning{Provisioned: true, IDs: []string{"datadog"}},
		Getenv:       func(k string) string { return env[k] },
		Catalog:      testCatalog(),
	})
	sup.SetPool(pool)
	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap must not fail the boot: %v", err)
	}
	st := sup.States()[0]
	if st.SpawnState != SpawnStateFailed || st.Reason != ReasonOpenFailed {
		t.Errorf("state = %+v, want failed/open_failed", st)
	}
}

// TestSupervisor_NotProvisioned verifies the block-all boot spawns
// nothing and reports no states.
func TestSupervisor_NotProvisioned(t *testing.T) {
	pool := &fakePool{}
	sup := NewSupervisor(SupervisorConfig{
		Provisioning: Provisioning{Reason: ReasonNotProvisioned},
		Catalog:      testCatalog(),
	})
	sup.SetPool(pool)
	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(pool.snapshot()) != 0 || len(sup.States()) != 0 {
		t.Error("unprovisioned boot must spawn nothing")
	}
}

// TestSupervisor_ObserveToolCall verifies connector.tool_call is emitted
// for whitelisted connector ids only, with tool NAME only.
func TestSupervisor_ObserveToolCall(t *testing.T) {
	ledger, drain := captureLedger()
	sup := NewSupervisor(SupervisorConfig{
		Provisioning: Provisioning{Provisioned: true, IDs: []string{"datadog"}},
		Ledger:       ledger,
		Catalog:      testCatalog(),
	})
	sup.ObserveToolCall("datadog", "query_metrics")
	sup.ObserveToolCall("bash", "run") // not a connector — no event
	calls := phases(drain(), "connector.tool_call")
	if len(calls) != 1 {
		t.Fatalf("tool_call events = %d, want 1", len(calls))
	}
	if calls[0]["connector_id"] != "datadog" || calls[0]["tool"] != "query_metrics" {
		t.Errorf("tool_call event = %v", calls[0])
	}
}
