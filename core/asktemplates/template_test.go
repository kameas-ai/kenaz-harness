package asktemplates

// template_test.go — WP07: template parser + bundled template tests.
//
// Tests cover:
//  1. BundledFS — all five bundled templates load and parse without error.
//  2. Variable substitution — strings, nested presence, optional missing vars.
//  3. Missing required var returns a clear error.
//  4. User template shadows bundled one with the same id.
//  5. Unknown template name returns a not-found error.
//  6. List returns all available template ids.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bundledNames is the expected set of bundled template IDs.
var bundledNames = []string{
	"confirm-destructive",
	"pick-file",
	"rank-options",
	"clarify-scope",
	"confirm-deploy",
}

// ─── bundled load ─────────────────────────────────────────────────────────────

func TestBundledTemplates_AllLoad(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")
	for _, name := range bundledNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := loader.load(name)
			if err != nil {
				t.Fatalf("load(%q): %v", name, err)
			}
			if tmpl.ID != name {
				t.Errorf("ID = %q, want %q", tmpl.ID, name)
			}
			if len(tmpl.Questions) == 0 {
				t.Errorf("template %q has no questions", name)
			}
		})
	}
}

// ─── variable substitution ────────────────────────────────────────────────────

func TestResolve_VariableSubstitution(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")

	qs, err := loader.Resolve("confirm-destructive", map[string]any{
		"action": "Delete production database",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(qs) != 1 {
		t.Fatalf("questions len = %d, want 1", len(qs))
	}
	q := qs[0]
	if !strings.Contains(q.Prompt, "Delete production database") {
		t.Errorf("prompt %q missing substituted action", q.Prompt)
	}
	if !q.Destructive {
		t.Errorf("confirm-destructive template should set destructive=true")
	}
}

func TestResolve_ConfirmDeploy_WithEnvironment(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")

	qs, err := loader.Resolve("confirm-deploy", map[string]any{
		"environment": "prod",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(qs) != 1 {
		t.Fatalf("questions len = %d, want 1", len(qs))
	}
	if !strings.Contains(qs[0].Prompt, "prod") {
		t.Errorf("prompt %q should contain environment name", qs[0].Prompt)
	}
	if qs[0].Affirmative != "Deploy" {
		t.Errorf("affirmative = %q, want Deploy", qs[0].Affirmative)
	}
}

func TestResolve_ClarifyScope_MultiQuestion(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")

	qs, err := loader.Resolve("clarify-scope", map[string]any{
		"task": "search refactor",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(qs) < 2 {
		t.Fatalf("clarify-scope should have >= 2 questions, got %d", len(qs))
	}
	if qs[0].Kind != "text" {
		t.Errorf("first question kind = %q, want text", qs[0].Kind)
	}
	if qs[1].Kind != "checkbox" {
		t.Errorf("second question kind = %q, want checkbox", qs[1].Kind)
	}
}

// ─── missing required var ─────────────────────────────────────────────────────

func TestResolve_MissingRequiredVar_Error(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")

	_, err := loader.Resolve("confirm-destructive", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required var 'action', got nil")
	}
	if !strings.Contains(err.Error(), "action") {
		t.Errorf("error should mention 'action', got: %v", err)
	}
}

func TestResolve_OptionalVarMissing_NoError(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")

	// commits is optional in confirm-deploy
	_, err := loader.Resolve("confirm-deploy", map[string]any{
		"environment": "staging",
	})
	if err != nil {
		t.Fatalf("optional var missing should not error: %v", err)
	}
}

// ─── user shadow ──────────────────────────────────────────────────────────────

func TestResolve_UserTemplateShadowsBundled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a user "confirm-deploy" that overrides the bundled one.
	userYAML := `
id: confirm-deploy
description: User override
questions:
  - id: confirm
    kind: confirm
    prompt: "Custom deploy prompt for {{.environment}}"
`
	if err := os.WriteFile(filepath.Join(dir, "confirm-deploy.yaml"), []byte(userYAML), 0600); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(BundledFS, dir)
	qs, err := loader.Resolve("confirm-deploy", map[string]any{"environment": "dev"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(qs[0].Prompt, "Custom deploy prompt") {
		t.Errorf("user template not shadowing bundled; prompt = %q", qs[0].Prompt)
	}
}

// ─── not found ────────────────────────────────────────────────────────────────

func TestResolve_UnknownTemplate_Error(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")

	_, err := loader.Resolve("does-not-exist", nil)
	if err == nil {
		t.Fatal("expected error for unknown template, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say not found: %v", err)
	}
}

// ─── list ─────────────────────────────────────────────────────────────────────

func TestList_BundledOnly(t *testing.T) {
	t.Parallel()
	loader := NewLoader(BundledFS, "")
	names, err := loader.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for _, want := range bundledNames {
		if !got[want] {
			t.Errorf("List missing bundled template %q; got %v", want, names)
		}
	}
}

func TestList_IncludesUserTemplates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "my-custom.yaml"), []byte(`
id: my-custom
questions:
  - id: q1
    kind: text
    prompt: "What do you want?"
`), 0600); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(BundledFS, dir)
	names, err := loader.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "my-custom" {
			found = true
		}
	}
	if !found {
		t.Errorf("List should include user template 'my-custom'; got %v", names)
	}
}
