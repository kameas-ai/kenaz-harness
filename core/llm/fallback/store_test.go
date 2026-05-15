package fallback

import (
	"context"
	"testing"
)

func TestFSStoreRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := NewFSStore(dir)
	ctx := context.Background()

	chain := &Chain{
		ID:          "test-chain-rt",
		Name:        "Round-trip Test",
		Description: "tests that all fields survive persist + load",
		Entries: []ChainEntry{
			{
				ProviderID: "openrouter",
				Model:      "openai/gpt-4o",
				ParamOverrides: map[string]any{
					"temperature": 0.7,
					"max_tokens":  1024,
				},
				Triggers:    []TriggerCondition{TriggerError5xx, TriggerError429},
				MaxAttempts: 2,
			},
		},
	}

	// Save
	if err := s.Save(ctx, chain); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Get
	got, err := s.Get(ctx, chain.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil {
		t.Fatal("Get() returned nil; want chain")
	}
	if got.ID != chain.ID {
		t.Errorf("Get() ID = %q, want %q", got.ID, chain.ID)
	}
	if got.Name != chain.Name {
		t.Errorf("Get() Name = %q, want %q", got.Name, chain.Name)
	}
	if got.Description != chain.Description {
		t.Errorf("Get() Description = %q, want %q", got.Description, chain.Description)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("Get() Entries len = %d, want 1", len(got.Entries))
	}
	e := got.Entries[0]
	if e.ProviderID != "openrouter" {
		t.Errorf("entry ProviderID = %q, want openrouter", e.ProviderID)
	}
	if e.Model != "openai/gpt-4o" {
		t.Errorf("entry Model = %q, want openai/gpt-4o", e.Model)
	}
	if e.MaxAttempts != 2 {
		t.Errorf("entry MaxAttempts = %d, want 2", e.MaxAttempts)
	}
	// ParamOverrides must survive round-trip.
	if e.ParamOverrides == nil {
		t.Error("entry ParamOverrides = nil; want map")
	}
	if len(e.Triggers) != 2 {
		t.Errorf("entry Triggers len = %d, want 2", len(e.Triggers))
	}

	// List
	chains, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(chains) != 1 {
		t.Errorf("List() len = %d, want 1", len(chains))
	}

	// Delete
	if err := s.Delete(ctx, chain.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Get after delete returns nil, nil
	deleted, err := s.Get(ctx, chain.ID)
	if err != nil {
		t.Fatalf("Get(deleted) error = %v", err)
	}
	if deleted != nil {
		t.Error("Get(deleted) returned non-nil; want nil")
	}

	// Delete idempotent
	if err := s.Delete(ctx, chain.ID); err != nil {
		t.Errorf("Delete(deleted) second call error = %v; want nil", err)
	}
}

func TestFSStoreGetNotFound(t *testing.T) {
	t.Parallel()

	s := NewFSStore(t.TempDir())
	got, err := s.Get(context.Background(), "nonexistent-id")
	if err != nil {
		t.Fatalf("Get(nonexistent) error = %v; want nil", err)
	}
	if got != nil {
		t.Errorf("Get(nonexistent) = non-nil; want nil")
	}
}

func TestFSStoreSaveValidatesMaxAttempts(t *testing.T) {
	t.Parallel()

	s := NewFSStore(t.TempDir())
	bad := &Chain{
		ID:   "bad-chain",
		Name: "Bad",
		Entries: []ChainEntry{
			{
				ProviderID:  "openrouter",
				Triggers:    []TriggerCondition{TriggerError5xx},
				MaxAttempts: MaxWholeChainAttempts + 1,
			},
		},
	}
	if err := s.Save(context.Background(), bad); err == nil {
		t.Error("Save(max_attempts > ceiling) = nil; want error")
	}
}

func TestFSStoreListEmptyDir(t *testing.T) {
	t.Parallel()

	s := NewFSStore(t.TempDir())
	chains, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List(empty dir) error = %v; want nil", err)
	}
	if len(chains) != 0 {
		t.Errorf("List(empty dir) len = %d; want 0", len(chains))
	}
}
