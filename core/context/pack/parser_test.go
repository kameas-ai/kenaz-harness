package pack

import (
	"os"
	"path/filepath"
	"testing"
)

// makeFile is a tiny fixture helper for table-driven tests; using a
// helper keeps every test case obvious about what's on disk.
func makeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const minimalManifest = `name: acme-org-context
version: 1.4.2
layer: org
issuer: trust://acme/anchors/root
required: true
signature:
  path: signatures/pack.sig
  algorithm: sigstore-bundle
  anchor_id: trust://acme/anchors/root
`

const validEntry = `---
name: entropy
kind: glossary
tags: [physics]
---
Entropy is a thermodynamic quantity.
`

const workflowScopedEntry = `---
name: rollup
kind: skill
scope:
  workflows: [quarterly-rollup]
  agents: [analyst]
---
Run the quarterly rollup.
`

const noFrontmatter = `Just plain markdown.
`

const malformedFrontmatter = `---
name: broken
kind: ?garbage??
  bad: yaml: structure: again:
---
Body.
`

func TestParseAndValidate_HappyPath(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "pack.yaml", minimalManifest)
	makeFile(t, root, "entries/glossary/entropy.md", validEntry)
	makeFile(t, root, "entries/skills/rollup.md", workflowScopedEntry)

	p, err := ParseAndValidate(root, ValidatorOptions{})
	if err != nil {
		t.Fatalf("ParseAndValidate: %v", err)
	}
	if got, want := p.Ref.Name, "acme-org-context"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
	if got, want := p.Ref.Layer, LayerOrg; got != want {
		t.Errorf("Layer = %q, want %q", got, want)
	}
	if got := len(p.Entries); got != 2 {
		t.Fatalf("Entries length = %d, want 2", got)
	}
	// Deterministic order: entropy < rollup.
	if p.Entries[0].Name != "entropy" || p.Entries[1].Name != "rollup" {
		t.Errorf("entries not sorted by name: %v", p.EntryNames())
	}
	// Workflow-scoped entry retains its scope.
	if e := p.Entries[1]; len(e.Scope.Workflows) != 1 || e.Scope.Workflows[0] != "quarterly-rollup" {
		t.Errorf("scope.workflows = %v, want [quarterly-rollup]", e.Scope.Workflows)
	}
	if p.Ref.ContentHash == "" {
		t.Error("pack content hash is empty")
	}
	for _, e := range p.Entries {
		if e.ContentHash == "" {
			t.Errorf("entry %q has empty content hash", e.Name)
		}
		if e.SourceLayer != LayerOrg {
			t.Errorf("entry %q source layer = %q, want org", e.Name, e.SourceLayer)
		}
	}
}

func TestParse_DeterministicHashAcrossRuns(t *testing.T) {
	// Byte-identity of pack/entry hashes is required for SC-005 (replay).
	root := t.TempDir()
	makeFile(t, root, "pack.yaml", minimalManifest)
	makeFile(t, root, "entries/glossary/entropy.md", validEntry)
	makeFile(t, root, "entries/skills/rollup.md", workflowScopedEntry)

	p1, err := ParseDir(root)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	p2, err := ParseDir(root)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if p1.Ref.ContentHash != p2.Ref.ContentHash {
		t.Fatalf("non-deterministic pack hash: %q vs %q", p1.Ref.ContentHash, p2.Ref.ContentHash)
	}
	for i := range p1.Entries {
		if p1.Entries[i].ContentHash != p2.Entries[i].ContentHash {
			t.Errorf("entry %s hash drift: %q vs %q",
				p1.Entries[i].Name, p1.Entries[i].ContentHash, p2.Entries[i].ContentHash)
		}
	}
}

func TestParse_MissingManifest(t *testing.T) {
	root := t.TempDir()
	_, err := ParseDir(root)
	if !HasCode(err, CodeManifestNotFound) {
		t.Fatalf("expected CodeManifestNotFound, got %v", err)
	}
}

func TestParse_MalformedManifest(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "pack.yaml", "::: not yaml :::")
	_, err := ParseDir(root)
	if !HasCode(err, CodeManifestMalformed) {
		t.Fatalf("expected CodeManifestMalformed, got %v", err)
	}
}

func TestParse_FrontmatterMissing(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "pack.yaml", minimalManifest)
	makeFile(t, root, "entries/glossary/entropy.md", noFrontmatter)
	_, err := ParseDir(root)
	if !HasCode(err, CodeFrontmatterMissing) {
		t.Fatalf("expected CodeFrontmatterMissing, got %v", err)
	}
}

func TestParse_FrontmatterMalformed(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "pack.yaml", minimalManifest)
	makeFile(t, root, "entries/glossary/x.md", malformedFrontmatter)
	_, err := ParseDir(root)
	if !HasCode(err, CodeFrontmatterInvalid) {
		t.Fatalf("expected CodeFrontmatterInvalid, got %v", err)
	}
}

func TestSafeJoin_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"../escape",
		"/abs/path",
		"foo/../../escape",
	}
	for _, rel := range cases {
		if _, err := safeJoin(root, rel); !HasCode(err, CodePathTraversal) {
			t.Errorf("safeJoin(%q) error = %v, want CodePathTraversal", rel, err)
		}
	}
}

func TestParse_KindInferredFromDirectory(t *testing.T) {
	root := t.TempDir()
	makeFile(t, root, "pack.yaml", minimalManifest)
	makeFile(t, root, "entries/glossary/term.md", `---
name: term
---
Body
`)
	p, err := ParseAndValidate(root, ValidatorOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Entries[0].Kind != KindGlossary {
		t.Errorf("inferred kind = %q, want %q", p.Entries[0].Kind, KindGlossary)
	}
}
