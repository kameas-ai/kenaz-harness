package slashcmd

import (
	"context"
	"testing"
)

// fakeProviders is a deterministic ProviderLister for /model tests.
type fakeProviders struct {
	providers []Provider
	err       error
}

func (f *fakeProviders) ListProviders(_ context.Context) ([]Provider, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.providers, nil
}

func TestModelCommand_HappyPath(t *testing.T) {
	t.Parallel()
	provs := &fakeProviders{providers: []Provider{
		{ID: "anthropic-1", Name: "Anthropic", Kind: "anthropic", DefaultModel: "claude-sonnet-4-7", Models: []string{"claude-sonnet-4-7", "claude-haiku-4-5"}},
		{ID: "openai-1", Name: "OpenAI", Kind: "openai", DefaultModel: "gpt-4o", Models: []string{"gpt-4o", "gpt-4o-mini"}},
	}}
	r, _ := NewRegistry(Deps{Providers: provs})
	res, err := r.Execute(context.Background(), "s", "/model gpt-4o")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q", res.Kind)
	}
	if got, want := res.Metadata[MetaKeyProviderID], "openai-1"; got != want {
		t.Errorf("providerId = %v, want %v", got, want)
	}
	if got, want := res.Metadata[MetaKeyModelID], "gpt-4o"; got != want {
		t.Errorf("modelId = %v, want %v", got, want)
	}
	if !contains(res.Text, "gpt-4o") || !contains(res.Text, "OpenAI") {
		t.Errorf("Text missing model+provider name: %q", res.Text)
	}
}

func TestModelCommand_FirstMatchWins(t *testing.T) {
	t.Parallel()
	// Two providers authorise the same model id; we expect the
	// first registered to win.
	provs := &fakeProviders{providers: []Provider{
		{ID: "first", Name: "First", DefaultModel: "shared-1"},
		{ID: "second", Name: "Second", DefaultModel: "shared-1"},
	}}
	r, _ := NewRegistry(Deps{Providers: provs})
	res, _ := r.Execute(context.Background(), "s", "/model shared-1")
	if got, want := res.Metadata[MetaKeyProviderID], "first"; got != want {
		t.Errorf("providerId = %v, want %v (first match wins)", got, want)
	}
}

func TestModelCommand_FallbackToDefaultModel(t *testing.T) {
	t.Parallel()
	// A provider with no Models slice should still match its
	// DefaultModel via AuthorisedModels().
	provs := &fakeProviders{providers: []Provider{
		{ID: "p1", Name: "Solo", DefaultModel: "only-model"},
	}}
	r, _ := NewRegistry(Deps{Providers: provs})
	res, err := r.Execute(context.Background(), "s", "/model only-model")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if got := res.Metadata[MetaKeyProviderID]; got != "p1" {
		t.Errorf("providerId = %v, want p1", got)
	}
}

func TestModelCommand_UnknownModel(t *testing.T) {
	t.Parallel()
	provs := &fakeProviders{providers: []Provider{
		{ID: "p1", Name: "P1", DefaultModel: "real-model"},
	}}
	r, _ := NewRegistry(Deps{Providers: provs})
	res, err := r.Execute(context.Background(), "s", "/model fake-model-9000")
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !contains(res.Text, "fake-model-9000") {
		t.Errorf("Text missing model id: %q", res.Text)
	}
	if res.Metadata[MetaKeyProviderID] != nil {
		t.Errorf("Metadata[providerId] should be nil on miss, got %v", res.Metadata[MetaKeyProviderID])
	}
}

func TestModelCommand_MissingArg(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{Providers: &fakeProviders{}})
	res, err := r.Execute(context.Background(), "s", "/model")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !contains(res.Text, "missing model id") {
		t.Errorf("Text = %q", res.Text)
	}
}

func TestModelCommand_TooManyArgs(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{Providers: &fakeProviders{}})
	res, _ := r.Execute(context.Background(), "s", "/model gpt-4o claude-haiku-4-5")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
}

func TestModelCommand_NoProvidersWired(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "s", "/model gpt-4o")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !contains(res.Text, "not wired") {
		t.Errorf("Text = %q", res.Text)
	}
}
