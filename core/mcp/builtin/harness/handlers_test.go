package harness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

type stubProviderLister struct{ items []ProviderSummary }

func (s stubProviderLister) ListProviders(_ context.Context) ([]ProviderSummary, error) {
	return s.items, nil
}

type stubProviderWriter struct{ added ProviderSummary }

func (s *stubProviderWriter) AddProvider(_ context.Context, kind, name, model, _ string) (ProviderSummary, error) {
	s.added = ProviderSummary{ID: "p1", Kind: kind, Name: name, Model: model}
	return s.added, nil
}
func (s *stubProviderWriter) RemoveProvider(_ context.Context, _ string) error { return nil }

// TestRegisterAll_HappyPath asserts the wiring registers the canonical
// 14 tools (13 from WP04/WP05 minus harness_write_set_setting, removed
// by harness-self-attach-01PMHS01 UNIT-8, G-4 — see the doc comment
// above harness.ProjectWriter — plus harness_read_materialize_run /
// harness_write_draft_agent_graph from model-authored-graphs-01PMGA01
// UNIT-7) and dispatches AddProvider end-to-end. The count is a
// registration-mechanics regression pin, not a reachability claim —
// spec.md §11.2 (model-authored-graphs-01PMGA01) is explicit that a
// tool count proves nothing about whether the server is attached to
// anything; see core/rpc/harness_self_attach_test.go for that.
func TestRegisterAll_HappyPath(t *testing.T) {
	t.Parallel()
	w := &stubProviderWriter{}
	srv := RegisterAll(NewServer(), Managers{
		Providers:       stubProviderLister{items: []ProviderSummary{{ID: "p0", Kind: "anthropic"}}},
		ProvidersWriter: w,
	})
	if got := len(srv.Tools()); got != 14 {
		t.Fatalf("registered tool count = %d, want 14", got)
	}

	// Drive add_provider via the handler directly.
	args, _ := json.Marshal(map[string]string{
		"kind":    "anthropic",
		"name":    "primary",
		"model":   "claude-3-5-sonnet",
		"api_key": "sk-secret",
	})
	spec, _ := srv.Lookup(ToolAddProvider)
	res, err := spec.Handler(context.Background(), args)
	if err != nil {
		t.Fatalf("AddProvider handler: %v", err)
	}
	tr, ok := res.(ToolResult)
	if !ok || !tr.OK {
		t.Fatalf("expected ok ToolResult, got %#v", res)
	}
	if w.added.Name != "primary" {
		t.Errorf("provider not stored: %#v", w.added)
	}
	if got := spec.Redact; len(got) != 1 || got[0] != "api_key" {
		t.Errorf("Redact = %v, want [api_key]", got)
	}
}

// TestRegisterAll_NoSetSettingTool is harness-self-attach-01PMHS01
// UNIT-8's regression pin (G-4): harness_write_set_setting must not be
// registered at all, and the identifiers backing it
// (SettingsWriter/handleSetSetting/SettingsAllowlist/ToolSetSetting)
// must not exist. The tool previously reported OK:true for every one of
// its five allowlisted keys while silently discarding the write
// (settingsKeyToJSON's `_`-prefixed sentinels, core/rpc/harness_wiring.go)
// — a lie the owner ruled to end by removing the tool, not by shipping
// it with an allowlist that shrank to zero.
//
// Mutation: re-add harness_write_set_setting to RegisterAll. Must fail.
func TestRegisterAll_NoSetSettingTool(t *testing.T) {
	t.Parallel()
	srv := RegisterAll(NewServer(), Managers{})
	if _, ok := srv.Lookup("harness_write_set_setting"); ok {
		t.Fatal("harness_write_set_setting is still registered — UNIT-8 removed it; " +
			"a tool that reports success for a write it silently discards is the class this mission exists to end")
	}
}

// TestToolsCall_SetSetting_FailsHonestly drives the actual JSON-RPC
// tools/call entrypoint (HandleEnvelope) the way a real MCP client would,
// end to end — not just the registration-catalog check above. Before
// UNIT-8 this call would have returned {"ok":true,"message":"Set
// OnboardingCompleted"} while silently discarding the write
// (settingsKeyToJSON's sentinel). It must now fail as an ordinary
// unknown-tool error: honest failure, not a lie with a 200-shaped
// envelope.
func TestToolsCall_SetSetting_FailsHonestly(t *testing.T) {
	t.Parallel()
	srv := RegisterAll(NewServer(), Managers{})
	params, _ := json.Marshal(map[string]any{
		"name":      "harness_write_set_setting",
		"arguments": map[string]any{"key": "OnboardingCompleted", "value": true},
	})
	res, rpcErr := srv.HandleEnvelope(context.Background(), transport.RequestEnvelope{
		Method: transport.MethodToolsCall,
		Params: json.RawMessage(params),
	})
	if rpcErr == nil {
		t.Fatalf("tools/call harness_write_set_setting: expected an error, got a result: %#v", res)
	}
	if rpcErr.Code != transport.ErrCodeMethodNotFound {
		t.Errorf("rpcErr.Code = %d, want ErrCodeMethodNotFound (%d)", rpcErr.Code, transport.ErrCodeMethodNotFound)
	}
}

// ---- WP04 read stubs ----

type stubSessionLister struct{ items []SessionSummary }

func (s stubSessionLister) ListSessions(_ context.Context) ([]SessionSummary, error) {
	return s.items, nil
}

type stubModelLister struct{ items []ModelSummary }

func (s stubModelLister) ListModels(_ context.Context) ([]ModelSummary, error) {
	return s.items, nil
}

// ---- WP05 write stubs ----

type stubSessionCreator struct{ created SessionSummary }

func (s *stubSessionCreator) CreateSession(_ context.Context, name, kind string) (SessionSummary, error) {
	s.created = SessionSummary{ID: "sess-1", Name: name, Kind: kind}
	return s.created, nil
}

// TestListSessions_HappyPath asserts list_sessions returns sessions from
// the injected lister.
func TestListSessions_HappyPath(t *testing.T) {
	t.Parallel()
	items := []SessionSummary{{ID: "s1", Name: "first", Kind: "chat"}}
	m := Managers{Sessions: stubSessionLister{items: items}}
	res, err := m.handleListSessions(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleListSessions: %v", err)
	}
	tr, ok := res.(ToolResult)
	if !ok || !tr.OK {
		t.Fatalf("expected ok ToolResult, got %#v", res)
	}
}

// TestListSessions_NilManager asserts list_sessions returns errNotConfigured
// when Sessions is nil.
func TestListSessions_NilManager(t *testing.T) {
	t.Parallel()
	m := Managers{}
	_, err := m.handleListSessions(context.Background(), nil)
	if err == nil {
		t.Errorf("expected errNotConfigured, got nil")
	}
}

// TestListModels_HappyPath asserts list_models returns models from the
// injected lister.
func TestListModels_HappyPath(t *testing.T) {
	t.Parallel()
	items := []ModelSummary{{ProviderID: "p1", ModelID: "claude-3-5-sonnet"}}
	m := Managers{Models: stubModelLister{items: items}}
	res, err := m.handleListModels(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleListModels: %v", err)
	}
	tr, ok := res.(ToolResult)
	if !ok || !tr.OK {
		t.Fatalf("expected ok ToolResult, got %#v", res)
	}
}

// TestCreateSession_HappyPath asserts create_session creates a session
// with the given name and kind.
func TestCreateSession_HappyPath(t *testing.T) {
	t.Parallel()
	sc := &stubSessionCreator{}
	m := Managers{SessionsWriter: sc}
	args, _ := json.Marshal(map[string]string{"name": "my-session", "kind": "chat"})
	res, err := m.handleCreateSession(context.Background(), args)
	if err != nil {
		t.Fatalf("handleCreateSession: %v", err)
	}
	tr, ok := res.(ToolResult)
	if !ok || !tr.OK {
		t.Fatalf("expected ok ToolResult, got %#v", res)
	}
	if sc.created.Name != "my-session" {
		t.Errorf("created.Name = %q, want my-session", sc.created.Name)
	}
	if sc.created.Kind != "chat" {
		t.Errorf("created.Kind = %q, want chat", sc.created.Kind)
	}
}

// TestCreateSession_DefaultKind asserts create_session defaults kind to
// "chat" when omitted.
func TestCreateSession_DefaultKind(t *testing.T) {
	t.Parallel()
	sc := &stubSessionCreator{}
	m := Managers{SessionsWriter: sc}
	args, _ := json.Marshal(map[string]string{"name": "no-kind"})
	if _, err := m.handleCreateSession(context.Background(), args); err != nil {
		t.Fatalf("handleCreateSession: %v", err)
	}
	if sc.created.Kind != "chat" {
		t.Errorf("default kind = %q, want chat", sc.created.Kind)
	}
}

// TestCreateSession_NilManager asserts create_session returns
// errNotConfigured when SessionsWriter is nil.
func TestCreateSession_NilManager(t *testing.T) {
	t.Parallel()
	m := Managers{}
	args, _ := json.Marshal(map[string]string{"name": "x"})
	_, err := m.handleCreateSession(context.Background(), args)
	if err == nil {
		t.Errorf("expected errNotConfigured, got nil")
	}
}

// TestCreateSession_MissingName asserts create_session rejects a missing
// name.
func TestCreateSession_MissingName(t *testing.T) {
	t.Parallel()
	sc := &stubSessionCreator{}
	m := Managers{SessionsWriter: sc}
	args, _ := json.Marshal(map[string]string{})
	_, err := m.handleCreateSession(context.Background(), args)
	if err == nil {
		t.Errorf("expected error for missing name, got nil")
	}
}
