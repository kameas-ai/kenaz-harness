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
// 11 tools and dispatches AddProvider end-to-end.
func TestRegisterAll_HappyPath(t *testing.T) {
	t.Parallel()
	w := &stubProviderWriter{}
	srv := RegisterAll(NewServer(), Managers{
		Providers:       stubProviderLister{items: []ProviderSummary{{ID: "p0", Kind: "anthropic"}}},
		ProvidersWriter: w,
	})
	if got := len(srv.Tools()); got != 11 {
		t.Fatalf("registered tool count = %d, want 11", got)
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
