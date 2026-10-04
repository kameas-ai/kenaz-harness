package cedar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// graph-fs-gate-01GFSG01 WP04: Reload used to rebuild the PolicySet from
// embedded + disk sources only, silently uninstalling everything
// LoadHarnessSnippets had added (the harness-self and graph-authoring
// policies) — including on the "fix your policy file and reload" path
// WP01's Policy-view banner points users at.

const (
	snipForbidMemory  = `forbid (principal, action == Action::"memory_write", resource);`
	snipForbidUseTool = `forbid (principal, action == Action::"use_tool", resource);`
)

func snippetListed(e *Engine, name string) bool {
	for _, f := range e.ListPolicies() {
		if f.Name == name && f.Embedded && f.ParseOK {
			return true
		}
	}
	return false
}

func TestReload_RetainsHarnessSnippets(t *testing.T) {
	e, _ := newDiskEngine(t, nil)
	ctx := context.Background()
	if err := e.LoadHarnessSnippets(map[string][]byte{"zz_snip_memory.cedar": []byte(snipForbidMemory)}); err != nil {
		t.Fatalf("LoadHarnessSnippets: %v", err)
	}
	if !IsPolicyDenied(CheckMemoryWrite(ctx, e, "global")) {
		t.Fatal("snippet forbid not active after LoadHarnessSnippets")
	}

	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// (1) still in the active set ...
	err := CheckMemoryWrite(ctx, e, "global")
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("snippet forbid DROPPED by Reload: memory_write = %v", err)
	}
	if !strings.HasPrefix(pde.Decision.MatchedPolicy, "harness-self/zz_snip_memory.cedar#") {
		t.Errorf("denial not attributed to the retained snippet: matched=%q", pde.Decision.MatchedPolicy)
	}
	// (2) ... and still listed.
	if !snippetListed(e, "zz_snip_memory.cedar") {
		t.Fatalf("snippet missing from ListPolicies after Reload: %+v", e.ListPolicies())
	}

	// A second load of the same name replaces, not duplicates.
	if err := e.LoadHarnessSnippets(map[string][]byte{"zz_snip_memory.cedar": []byte(snipForbidMemory)}); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range e.ListPolicies() {
		if f.Name == "zz_snip_memory.cedar" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("snippet listed %d times after reload, want 1", n)
	}
}

func TestLoadHarnessSnippets_ParseErrorContractPreserved(t *testing.T) {
	e, _ := newDiskEngine(t, nil)
	err := e.LoadHarnessSnippets(map[string][]byte{"zz_bad.cedar": []byte("forbid ( {{{")})
	if err == nil || !strings.Contains(err.Error(), "LoadHarnessSnippets parse zz_bad.cedar") {
		t.Fatalf("parse error contract changed: %v", err)
	}
	// A rejected snippet is not retained, so Reload does not resurrect it.
	if err := e.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	for _, f := range e.ListPolicies() {
		if f.Name == "zz_bad.cedar" {
			t.Fatalf("rejected snippet listed after Reload: %+v", f)
		}
	}
}

// The two features compose: with a corrupt user file present, the
// graph fail-closed gate denies its guarded actions (even one a
// retained snippet also governs), non-guarded actions still reach the
// retained snippet, and after fix + Reload the snippet — not the
// fail-closed gate — decides again.
func TestReload_RetainedSnippets_ComposeWithFailClosed(t *testing.T) {
	e, dir := newDiskEngine(t, nil)
	ctx := context.Background()
	if err := e.LoadHarnessSnippets(map[string][]byte{
		"zz_snip_memory.cedar":   []byte(snipForbidMemory),
		"zz_snip_use_tool.cedar": []byte(snipForbidUseTool),
	}); err != nil {
		t.Fatal(err)
	}
	g := FailClosedOnLoadError(e, nil)

	bad := filepath.Join(dir, PolicyDir, "user.cedar")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("permit ( {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = e.Reload(ctx)

	var pde *PolicyDeniedError
	if err := CheckUseTool(ctx, g, "kenaz", "bash"); !errors.As(err, &pde) || pde.Decision.MatchedPolicy != "fail-closed/policy-load-error" {
		t.Fatalf("use_tool under a corrupt user policy: want fail-closed deny, got %v", err)
	}
	if err := CheckMemoryWrite(ctx, g, "global"); !errors.As(err, &pde) || !strings.HasPrefix(pde.Decision.MatchedPolicy, "harness-self/zz_snip_memory.cedar#") {
		t.Fatalf("memory_write after reload with a corrupt user file: want the retained snippet's deny, got %v", err)
	}
	if !snippetListed(e, "zz_snip_use_tool.cedar") || !snippetListed(e, "zz_snip_memory.cedar") {
		t.Fatalf("snippets not listed alongside the corrupt file: %+v", e.ListPolicies())
	}

	if err := os.WriteFile(bad, []byte("// fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := CheckFileWrite(ctx, g, "/tmp/x"); err != nil {
		t.Fatalf("file_write still denied after fix: %v", err)
	}
	if err := CheckUseTool(ctx, g, "kenaz", "bash"); !errors.As(err, &pde) || !strings.HasPrefix(pde.Decision.MatchedPolicy, "harness-self/zz_snip_use_tool.cedar#") {
		t.Fatalf("use_tool after fix: want the retained snippet's deny (not fail-closed), got %v", err)
	}
}
