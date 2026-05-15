package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm/fallback"
)

// buildTestAPI constructs a minimal API with a tempdir-backed FSStore.
func buildFallbackTestAPI(t *testing.T) *API {
	t.Helper()
	store := fallback.NewFSStore(t.TempDir())
	return New(Config{FallbackStore: store})
}

func TestListFallbackChains_BundledDefaults(t *testing.T) {
	t.Parallel()

	api := buildFallbackTestAPI(t)
	ctx := context.Background()

	chains, err := api.ListFallbackChains(ctx)
	if err != nil {
		t.Fatalf("ListFallbackChains() error = %v", err)
	}
	// At least the 2 bundled chains must appear.
	if len(chains) < 2 {
		t.Errorf("ListFallbackChains() returned %d chains, want >= 2", len(chains))
	}
	for _, c := range chains {
		if !c.Bundled {
			// Only bundled chains present in a fresh store.
			t.Errorf("chain %q Bundled=false in fresh store", c.ID)
		}
	}
}

func TestSaveAndLoadChain(t *testing.T) {
	t.Parallel()

	api := buildFallbackTestAPI(t)
	ctx := context.Background()

	view := FallbackChainView{
		ID:          "my-test-chain",
		Name:        "My Test Chain",
		Description: "round-trip test",
		Entries: []FallbackChainEntryView{
			{
				ProviderID:     "openrouter",
				Model:          "openai/gpt-4o",
				Triggers:       []string{"error_5xx"},
				MaxAttempts:    2,
				ParamOverrides: map[string]any{"temperature": 0.5},
			},
		},
	}

	if err := api.SaveChain(ctx, view); err != nil {
		t.Fatalf("SaveChain() error = %v", err)
	}

	got, err := api.LoadChain(ctx, "my-test-chain")
	if err != nil {
		t.Fatalf("LoadChain() error = %v", err)
	}
	if got.ID != view.ID {
		t.Errorf("LoadChain() ID = %q, want %q", got.ID, view.ID)
	}
	if got.Name != view.Name {
		t.Errorf("LoadChain() Name = %q, want %q", got.Name, view.Name)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("LoadChain() Entries len = %d, want 1", len(got.Entries))
	}
	e := got.Entries[0]
	if e.ProviderID != "openrouter" {
		t.Errorf("entry ProviderID = %q, want openrouter", e.ProviderID)
	}
	if e.MaxAttempts != 2 {
		t.Errorf("entry MaxAttempts = %d, want 2", e.MaxAttempts)
	}
	if len(e.Triggers) != 1 || e.Triggers[0] != "error_5xx" {
		t.Errorf("entry Triggers = %v, want [error_5xx]", e.Triggers)
	}
}

func TestLoadChain_NotFound(t *testing.T) {
	t.Parallel()

	api := buildFallbackTestAPI(t)
	_, err := api.LoadChain(context.Background(), "does-not-exist-xyz")
	if !errors.Is(err, ErrFallbackChainNotFound) {
		t.Errorf("LoadChain(nonexistent) = %v; want ErrFallbackChainNotFound", err)
	}
}

func TestDeleteChain(t *testing.T) {
	t.Parallel()

	api := buildFallbackTestAPI(t)
	ctx := context.Background()

	// Save a chain.
	view := FallbackChainView{
		ID:   "delete-me",
		Name: "Delete Me",
		Entries: []FallbackChainEntryView{
			{ProviderID: "openrouter", Triggers: []string{"error_5xx"}},
		},
	}
	if err := api.SaveChain(ctx, view); err != nil {
		t.Fatalf("SaveChain() error = %v", err)
	}

	// Delete.
	if err := api.DeleteChain(ctx, "delete-me"); err != nil {
		t.Fatalf("DeleteChain() error = %v", err)
	}

	// Confirm it's gone from FSStore (falls through to bundled).
	_, err := api.LoadChain(ctx, "delete-me")
	if !errors.Is(err, ErrFallbackChainNotFound) {
		t.Errorf("LoadChain(deleted) = %v; want ErrFallbackChainNotFound", err)
	}

	// Idempotent: second delete is a no-op.
	if err := api.DeleteChain(ctx, "delete-me"); err != nil {
		t.Errorf("DeleteChain(idempotent) = %v; want nil", err)
	}
}

func TestSaveChain_MaxAttemptsValidation(t *testing.T) {
	t.Parallel()

	api := buildFallbackTestAPI(t)
	bad := FallbackChainView{
		ID:   "bad-chain",
		Name: "Bad Chain",
		Entries: []FallbackChainEntryView{
			{
				ProviderID:  "openrouter",
				Triggers:    []string{"error_5xx"},
				MaxAttempts: fallback.MaxWholeChainAttempts + 1,
			},
		},
	}
	if err := api.SaveChain(context.Background(), bad); err == nil {
		t.Error("SaveChain(max_attempts > ceiling) = nil; want error")
	}
}

func TestListFallbackChains_UserManagedOverridesBundled(t *testing.T) {
	t.Parallel()

	api := buildFallbackTestAPI(t)
	ctx := context.Background()

	// Override a bundled chain ID.
	view := FallbackChainView{
		ID:   "anthropic-with-openrouter-fallback",
		Name: "User override of bundled chain",
		Entries: []FallbackChainEntryView{
			{ProviderID: "bedrock", Triggers: []string{"error_5xx"}},
		},
	}
	if err := api.SaveChain(ctx, view); err != nil {
		t.Fatalf("SaveChain() error = %v", err)
	}

	chains, err := api.ListFallbackChains(ctx)
	if err != nil {
		t.Fatalf("ListFallbackChains() error = %v", err)
	}

	// Find the overridden chain — it must not be Bundled.
	found := false
	for _, c := range chains {
		if c.ID == "anthropic-with-openrouter-fallback" {
			found = true
			if c.Bundled {
				t.Errorf("chain %q Bundled=true; want false (user-managed override)", c.ID)
			}
		}
	}
	if !found {
		t.Error("overridden chain not found in ListFallbackChains result")
	}
}
