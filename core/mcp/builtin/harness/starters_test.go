package harness

import (
	"os"
	"path/filepath"
	"testing"
)

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
