package harness

import (
	"context"
	"encoding/json"
	"testing"
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
// 14 tools and dispatches AddProvider end-to-end.
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

// TestSetSetting_Allowlist asserts non-allowlisted keys are rejected.
func TestSetSetting_Allowlist(t *testing.T) {
	t.Parallel()
	m := Managers{SettingsWriter: stubSettingsWriter{}}
	args, _ := json.Marshal(map[string]any{"key": "EvilKey", "value": 1})
	if _, err := m.handleSetSetting(context.Background(), args); err == nil {
		t.Errorf("expected error for non-allowlisted key")
	}
	args, _ = json.Marshal(map[string]any{"key": "OnboardingCompleted", "value": true})
	if _, err := m.handleSetSetting(context.Background(), args); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

type stubSettingsWriter struct{}

func (stubSettingsWriter) SetSetting(_ context.Context, _ string, _ any) error { return nil }

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
