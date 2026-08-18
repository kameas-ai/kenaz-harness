package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// harnessToolNamePattern matches any harness_* identifier token that could
// appear in a starter prompt's markdown body. Mirrors AC-003's
// `grep -o 'harness_[a-z_]*'` contract.
var harnessToolNamePattern = regexp.MustCompile(`harness_[a-z_]+`)

// registeredHarnessToolNames returns the live registered tool-name set by
// actually calling RegisterAll — not a hand-written list in the test, which
// is exactly the drift that let code.md:23 survive the
// harness_write_propose_cedar_policy deletion (first-run-onboarding-01PMOB01
// spec §"Prompt truth pass"). A zero-value Managers{} is fine: RegisterAll
// only wires handler funcs, it never invokes them.
func registeredHarnessToolNames(t *testing.T) map[string]bool {
	t.Helper()
	srv := RegisterAll(NewServer(), Managers{})
	out := make(map[string]bool)
	for _, spec := range srv.Tools() {
		out[spec.Name] = true
	}
	return out
}

// TestStarterPromptsNameOnlyRegisteredTools is WP01's load-bearing test
// (FR-001 / AC-003): every harness_* identifier extracted from a shipped
// starter's markdown body must resolve to a tool RegisterAll actually
// registered. It is a table test over the full shipped starter set so a
// future starter addition is covered automatically.
//
// Mutation: re-add `harness_write_propose_cedar_policy` to code.md (the
// tool the 2026-08-14 sweep deleted from register.go). This test must fail.
func TestStarterPromptsNameOnlyRegisteredTools(t *testing.T) {
	t.Parallel()
	registered := registeredHarnessToolNames(t)

	starters, err := LoadStarters("")
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	if len(starters) == 0 {
		t.Fatal("LoadStarters(\"\") returned no starters — nothing to check")
	}

	for _, s := range starters {
		s := s
		t.Run(s.ID, func(t *testing.T) {
			t.Parallel()
			names := harnessToolNamePattern.FindAllString(s.SystemPrompt, -1)
			for _, name := range names {
				if !registered[name] {
					t.Errorf("starter %q names %q, which is not in register.go's registered tool set", s.ID, name)
				}
			}
		})
	}
}

// TestLoadStarters_Embedded asserts the five canonical starters ship.
func TestLoadStarters_Embedded(t *testing.T) {
	t.Parallel()
	got, err := LoadStarters("")
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	want := map[string]bool{"chat": false, "code": false, "data": false, "research": false, "writing": false}
	for _, s := range got {
		if _, ok := want[s.ID]; ok {
			want[s.ID] = true
		}
		if s.Source != "embedded" {
			t.Errorf("starter %q source = %q, want embedded", s.ID, s.Source)
		}
	}
	for id, ok := range want {
		if !ok {
			t.Errorf("missing starter %q", id)
		}
	}
}

// TestLoadStarters_UserOverride verifies a user-dropped file shadows the
// embedded entry with the same id.
func TestLoadStarters_UserOverride(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "onboarding", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	override := []byte("---\nid: code\ntitle: My Custom Code Setup\ndescription: override\n---\nMy custom prompt body.\n")
	if err := os.WriteFile(filepath.Join(dir, "code.md"), override, 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	got, err := LoadStarters(dataDir)
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	var code Starter
	for _, s := range got {
		if s.ID == "code" {
			code = s
			break
		}
	}
	if code.Title != "My Custom Code Setup" {
		t.Errorf("override title not applied: %q", code.Title)
	}
	if code.Source != "user" {
		t.Errorf("override source = %q, want user", code.Source)
	}
}
