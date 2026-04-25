package bundlekind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

const manifest = `name: acme-org-context
version: 1.4.2
layer: org
issuer: trust://acme/anchors/root
required: true
signature:
  path: signatures/pack.sig
  algorithm: sigstore-bundle
  anchor_id: trust://acme/anchors/root
`

const entropyEntry = `---
name: entropy
kind: glossary
---
A measure of disorder.
`

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// fakeRegistry records every handler registered. Implements [Registry].
type fakeRegistry struct {
	handlers []Handler
}

func (f *fakeRegistry) Register(h Handler) error {
	f.handlers = append(f.handlers, h)
	return nil
}

func TestRegister_AttachesContextPackHandler(t *testing.T) {
	reg := &fakeRegistry{}
	if err := Register(reg, pack.ValidatorOptions{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := len(reg.handlers); got != 1 {
		t.Fatalf("handlers len = %d, want 1", got)
	}
	if got := reg.handlers[0].Kind(); got != KindID {
		t.Fatalf("Kind() = %q, want %q", got, KindID)
	}
}

func TestRegister_NilRegistry(t *testing.T) {
	if err := Register(nil, pack.ValidatorOptions{}); err == nil {
		t.Fatalf("expected error registering nil registry")
	}
}

func TestDefaultHandler_ParseAndValidate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pack.yaml", manifest)
	writeFile(t, root, "entries/glossary/entropy.md", entropyEntry)

	h := &DefaultHandler{}
	got, err := h.Parse(context.Background(), FetchedArtifact{Root: root})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Ref.Name != "acme-org-context" {
		t.Errorf("Name = %q", got.Ref.Name)
	}
	if err := h.Validate(context.Background(), got); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestDefaultHandler_ContentHashMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pack.yaml", manifest)
	writeFile(t, root, "entries/glossary/entropy.md", entropyEntry)

	h := &DefaultHandler{}
	_, err := h.Parse(context.Background(), FetchedArtifact{
		Root:        root,
		ContentHash: "sha256:deadbeef",
	})
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
}

func TestDefaultHandler_LockfileProjection(t *testing.T) {
	p := &pack.ContextPack{
		Ref: pack.PackRef{
			Name:        "acme-org-context",
			Version:     "1.4.2",
			Layer:       pack.LayerOrg,
			ContentHash: "sha256:abcd",
		},
		Required: true,
	}
	h := &DefaultHandler{}
	entry := h.Lockfile(p, "oci://registry.example/pack:1.4.2", "sigstore-bundle:abc")
	if entry.Kind != KindID {
		t.Errorf("Kind = %q", entry.Kind)
	}
	if entry.Name != "acme-org-context" {
		t.Errorf("Name = %q", entry.Name)
	}
	if entry.Layer != "org" {
		t.Errorf("Layer = %q", entry.Layer)
	}
	if !entry.Required {
		t.Errorf("Required = false")
	}
	if entry.Source != "oci://registry.example/pack:1.4.2" {
		t.Errorf("Source = %q", entry.Source)
	}
	if entry.ContentHash != "sha256:abcd" {
		t.Errorf("ContentHash = %q", entry.ContentHash)
	}
}

// failRegistry exercises the error path Register propagates.
type failRegistry struct{}

func (failRegistry) Register(_ Handler) error { return errors.New("boom") }

func TestRegister_PropagatesError(t *testing.T) {
	if err := Register(failRegistry{}, pack.ValidatorOptions{}); err == nil {
		t.Fatalf("expected error from underlying registry")
	}
}
