// loader_test.go — tests for InstallBundledSkills.
//
// model-invoked-skills-catalog-01KZNP3E WP04.
package skill

import (
	"context"
	"errors"
	"sync"
	"testing"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// inMemoryStore is a minimal in-memory SkillStore used in tests.
type inMemoryStore struct {
	mu       sync.Mutex
	commands map[string]coreslashcmd.UserCommand
	saveErr  error
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{commands: make(map[string]coreslashcmd.UserCommand)}
}

func (s *inMemoryStore) LoadUserOne(_ context.Context, name, _ string) (coreslashcmd.UserCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.commands[name]
	if !ok {
		return coreslashcmd.UserCommand{}, coreslashcmd.ErrCommandNotFound
	}
	return cmd, nil
}

func (s *inMemoryStore) SaveUser(_ context.Context, cmd coreslashcmd.UserCommand) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands[cmd.Name] = cmd
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────

func TestInstallBundledSkills_NilStore_NoError(t *testing.T) {
	t.Parallel()
	if err := InstallBundledSkills(context.Background(), nil); err != nil {
		t.Errorf("nil store should be a no-op, got error: %v", err)
	}
}

func TestInstallBundledSkills_InstallsAllBundled(t *testing.T) {
	t.Parallel()
	store := newInMemoryStore()
	if err := InstallBundledSkills(context.Background(), store); err != nil {
		t.Fatalf("InstallBundledSkills returned error: %v", err)
	}

	// Verify all 5 bundled skill names are present.
	expected := []string{"summarize", "explain", "improve-writing", "generate-tests", "standup"}
	for _, name := range expected {
		if _, err := store.LoadUserOne(context.Background(), name, ""); err != nil {
			t.Errorf("bundled skill %q not installed: %v", name, err)
		}
	}
}

func TestInstallBundledSkills_ModelInvokableSetTrue(t *testing.T) {
	t.Parallel()
	store := newInMemoryStore()
	if err := InstallBundledSkills(context.Background(), store); err != nil {
		t.Fatalf("InstallBundledSkills: %v", err)
	}
	cmd, err := store.LoadUserOne(context.Background(), "summarize", "")
	if err != nil {
		t.Fatalf("LoadUserOne(summarize): %v", err)
	}
	if !cmd.ModelInvokable {
		t.Error("expected ModelInvokable=true on installed bundled skill")
	}
}

func TestInstallBundledSkills_Idempotent(t *testing.T) {
	t.Parallel()
	store := newInMemoryStore()

	// First install.
	if err := InstallBundledSkills(context.Background(), store); err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Manually overwrite one command to simulate user customisation.
	store.mu.Lock()
	original := store.commands["summarize"]
	custom := original
	custom.Description = "my custom description"
	store.commands["summarize"] = custom
	store.mu.Unlock()

	// Second install should not overwrite user's version.
	if err := InstallBundledSkills(context.Background(), store); err != nil {
		t.Fatalf("second install failed: %v", err)
	}
	cmd, err := store.LoadUserOne(context.Background(), "summarize", "")
	if err != nil {
		t.Fatalf("LoadUserOne after second install: %v", err)
	}
	if cmd.Description != "my custom description" {
		t.Errorf("second install overwrote user customisation; got description %q", cmd.Description)
	}
}

func TestInstallBundledSkills_SaveError_CollectedNotFatal(t *testing.T) {
	t.Parallel()
	store := newInMemoryStore()
	store.saveErr = errors.New("disk full")

	err := InstallBundledSkills(context.Background(), store)
	if err == nil {
		t.Fatal("expected combined error when SaveUser fails, got nil")
	}
	if !errors.Is(err, store.saveErr) && !containsText(err.Error(), "disk full") {
		t.Errorf("error should mention save failure, got: %v", err)
	}
}

func TestInstallBundledSkills_ScopeDefaultGlobal(t *testing.T) {
	t.Parallel()
	store := newInMemoryStore()
	if err := InstallBundledSkills(context.Background(), store); err != nil {
		t.Fatalf("InstallBundledSkills: %v", err)
	}
	for name, cmd := range store.commands {
		if cmd.Scope != coreslashcmd.ScopeGlobal {
			t.Errorf("skill %q has scope %q, want %q", name, cmd.Scope, coreslashcmd.ScopeGlobal)
		}
	}
}

func containsText(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && (s == sub || len(s) >= len(sub) && findSub(s, sub))
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
