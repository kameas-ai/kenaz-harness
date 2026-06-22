package contexts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
)

// openLib is a test helper that opens a library in a temp directory.
func openLib(t *testing.T) *contexts.Library {
	t.Helper()
	lib, err := contexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return lib
}

// writeFile is a test helper that writes an absolute file.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// ── RootFile / IsModule ────────────────────────────────────────────────────

func TestRootFile_ContextMd(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("mymod"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// CreateFolder scaffolds context.md — check it's there.
	rel, ok := lib.RootFile("mymod")
	if !ok {
		t.Fatal("RootFile returned ok=false after CreateFolder scaffolding")
	}
	if rel != "mymod/context.md" {
		t.Fatalf("RootFile = %q; want mymod/context.md", rel)
	}
}

func TestRootFile_AgentsMd(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("agmod"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// Remove the scaffolded context.md so we can test agents.md fallback.
	if err := os.Remove(filepath.Join(lib.Root(), "agmod", "context.md")); err != nil {
		t.Fatalf("Remove context.md: %v", err)
	}
	// Place an agents.md.
	writeFile(t, filepath.Join(lib.Root(), "agmod", "agents.md"), "# agents")
	rel, ok := lib.RootFile("agmod")
	if !ok {
		t.Fatal("RootFile returned ok=false for agents.md")
	}
	if rel != "agmod/agents.md" {
		t.Fatalf("RootFile = %q; want agmod/agents.md", rel)
	}
}

func TestRootFile_NeitherExists(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	// Create a plain dir without any root file.
	if err := os.MkdirAll(filepath.Join(lib.Root(), "plain"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_, ok := lib.RootFile("plain")
	if ok {
		t.Fatal("RootFile returned ok=true for dir with no root file")
	}
}

func TestIsModule(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("mod"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if !lib.IsModule("mod") {
		t.Fatal("IsModule returned false after CreateFolder (should scaffold root)")
	}
	// A non-existent dir is not a module.
	if lib.IsModule("nonexistent") {
		t.Fatal("IsModule returned true for nonexistent dir")
	}
}

// ── Scaffold-on-create ────────────────────────────────────────────────────

func TestCreateFolder_ScaffoldsContextMd(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("newmod"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// The scaffolded file should exist and be readable.
	got, err := lib.Get("newmod/context.md")
	if err != nil {
		t.Fatalf("Get scaffolded context.md: %v", err)
	}
	// Stub should mention the always: key.
	if !strings.Contains(got, "always:") {
		t.Errorf("scaffold content missing 'always:'; got:\n%s", got)
	}
}

func TestCreateFolder_IdempotentDoesNotOverwriteRoot(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("idempotent"); err != nil {
		t.Fatalf("first CreateFolder: %v", err)
	}
	// Write a custom root.
	if err := lib.Save("idempotent/context.md", "# custom"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Second CreateFolder should be a no-op (dir exists); the custom
	// context.md should survive.
	if err := lib.CreateFolder("idempotent"); err != nil {
		t.Fatalf("second CreateFolder: %v", err)
	}
	got, err := lib.Get("idempotent/context.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "# custom" {
		t.Fatalf("context.md was overwritten; got %q", got)
	}
}

// ── Front-matter always: parse ────────────────────────────────────────────

func TestParseModule_AlwaysFiles(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("fm"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// Overwrite the scaffolded root with a real front-matter.
	root := "---\nalways:\n  - conventions.md\n  - glossary.md\n---\n# Module\n"
	if err := lib.Save("fm/context.md", root); err != nil {
		t.Fatalf("Save root: %v", err)
	}
	// Create the always-files.
	if err := lib.Save("fm/conventions.md", "# Conventions"); err != nil {
		t.Fatalf("Save conventions.md: %v", err)
	}
	if err := lib.Save("fm/glossary.md", "# Glossary"); err != nil {
		t.Fatalf("Save glossary.md: %v", err)
	}
	// Create an on-demand file.
	if err := lib.Save("fm/reference.md", "# Reference"); err != nil {
		t.Fatalf("Save reference.md: %v", err)
	}

	mod, err := lib.ParseModule("fm")
	if err != nil {
		t.Fatalf("ParseModule: %v", err)
	}
	if mod.Root != "fm/context.md" {
		t.Errorf("Root = %q; want fm/context.md", mod.Root)
	}
	if len(mod.Always) != 2 {
		t.Fatalf("len(Always) = %d; want 2; got %v", len(mod.Always), mod.Always)
	}
	if mod.Always[0] != "fm/conventions.md" || mod.Always[1] != "fm/glossary.md" {
		t.Errorf("Always = %v; want [fm/conventions.md fm/glossary.md]", mod.Always)
	}
	// reference.md must be in Others only.
	if len(mod.Others) != 1 || mod.Others[0] != "fm/reference.md" {
		t.Errorf("Others = %v; want [fm/reference.md]", mod.Others)
	}
}

func TestParseModule_NoFrontmatter(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	if err := lib.CreateFolder("nofm"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// Write a root without front-matter.
	if err := lib.Save("nofm/context.md", "# Just a doc"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	mod, err := lib.ParseModule("nofm")
	if err != nil {
		t.Fatalf("ParseModule: %v", err)
	}
	if mod.Root != "nofm/context.md" {
		t.Errorf("Root = %q; want nofm/context.md", mod.Root)
	}
	if len(mod.Always) != 0 {
		t.Errorf("Always = %v; want empty", mod.Always)
	}
}

func TestParseModule_NotAModule(t *testing.T) {
	t.Parallel()
	lib := openLib(t)
	// A plain directory with no root file.
	if err := os.MkdirAll(filepath.Join(lib.Root(), "plain"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_, err := lib.ParseModule("plain")
	if err == nil {
		t.Fatal("ParseModule returned nil error for non-module directory")
	}
}
