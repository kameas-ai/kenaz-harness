package settings

import (
	"context"
	"testing"
)

// TestFileStore_ChatCustomInstructionsDefaultEmpty pins that a fresh
// install returns the empty string (no user layer).
// (system-prompt-layers WP04)
func TestFileStore_ChatCustomInstructionsDefaultEmpty(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadChatCustomInstructions()
	if err != nil {
		t.Fatalf("LoadChatCustomInstructions: %v", err)
	}
	if got != "" {
		t.Errorf("default = %q, want empty", got)
	}
}

// TestFileStore_ChatCustomInstructionsRoundTrip exercises Save + Load
// through the on-disk JSON file, including clearing back to empty.
func TestFileStore_ChatCustomInstructionsRoundTrip(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	const want = "Always respond in British English.\nAvoid emoji."
	if err := store.SaveChatCustomInstructions(want); err != nil {
		t.Fatalf("SaveChatCustomInstructions: %v", err)
	}
	got, err := store.LoadChatCustomInstructions()
	if err != nil {
		t.Fatalf("LoadChatCustomInstructions: %v", err)
	}
	if got != want {
		t.Errorf("round-trip = %q, want %q", got, want)
	}
	// Full-record read should surface the same value.
	all, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if all.ChatCustomInstructions != want {
		t.Errorf("LoadAll.ChatCustomInstructions = %q, want %q", all.ChatCustomInstructions, want)
	}
	// Clear back to empty.
	if err := store.SaveChatCustomInstructions(""); err != nil {
		t.Fatalf("clear SaveChatCustomInstructions: %v", err)
	}
	got, _ = store.LoadChatCustomInstructions()
	if got != "" {
		t.Errorf("after clear = %q, want empty", got)
	}
}

// TestAPI_ChatCustomInstructions exercises the SettingsAPI Get/Set pair
// over both store backends.
func TestAPI_ChatCustomInstructions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store SettingsStore
	}{
		{"memory", newMemoryStore()},
	} {
		fileStore, err := NewFileStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewFileStore: %v", err)
		}
		stores := map[string]SettingsStore{tc.name: tc.store, "file": fileStore}
		for name, st := range stores {
			t.Run(name, func(t *testing.T) {
				api := &API{store: st}
				ctx := context.Background()
				if err := api.SetChatCustomInstructions(ctx, "Be terse."); err != nil {
					t.Fatalf("SetChatCustomInstructions: %v", err)
				}
				got, err := api.GetChatCustomInstructions(ctx)
				if err != nil {
					t.Fatalf("GetChatCustomInstructions: %v", err)
				}
				if got != "Be terse." {
					t.Errorf("Get = %q, want %q", got, "Be terse.")
				}
			})
		}
	}
}
