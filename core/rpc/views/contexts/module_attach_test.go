package contexts_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

// fakeAttachmentAdder records Add calls.
type fakeAttachmentAdder struct {
	last contextsview.AttachmentInput
	err  error
}

func (f *fakeAttachmentAdder) Add(_ context.Context, in contextsview.AttachmentInput) (contextsview.ModuleAttachment, error) {
	if f.err != nil {
		return contextsview.ModuleAttachment{}, f.err
	}
	f.last = in
	return contextsview.ModuleAttachment{
		ID:            "fake-id",
		ScopeKind:     in.ScopeKind,
		ScopeID:       in.ScopeID,
		ContentSource: in.ContentSource,
		Content:       in.Content,
		Kind:          in.Kind,
	}, nil
}

// newAPIWithLib builds a view API backed by a real temporary library.
func newAPIWithLib(t *testing.T) (*contextsview.API, *contexts.Library) {
	t.Helper()
	lib, err := contexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return contextsview.New(lib), lib
}

// ── AttachModule happy path ────────────────────────────────────────────────

func TestAttachModule_RootAndAlwaysInjected(t *testing.T) {
	t.Parallel()
	api, lib := newAPIWithLib(t)
	adder := &fakeAttachmentAdder{}
	api.WithAttachmentAdder(adder)

	// Create a module with context.md (scaffolded by CreateFolder) +
	// conventions.md (always-file) + reference.md (on-demand).
	ctx := context.Background()
	if err := api.CreateFolder(ctx, "proj"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// Overwrite the scaffold with a real front-matter.
	root := "---\nalways:\n  - conventions.md\n---\n# Project\n"
	if err := api.Save(ctx, "proj/context.md", root); err != nil {
		t.Fatalf("Save context.md: %v", err)
	}
	if err := api.Save(ctx, "proj/conventions.md", "# Conventions"); err != nil {
		t.Fatalf("Save conventions.md: %v", err)
	}
	if err := api.Save(ctx, "proj/reference.md", "# Reference"); err != nil {
		t.Fatalf("Save reference.md: %v", err)
	}

	// Unused — verify library is wired correctly.
	_ = lib

	att, err := api.AttachModule(ctx, "session", "sess-1", "proj")
	if err != nil {
		t.Fatalf("AttachModule: %v", err)
	}

	// ContentSource must use the module: scheme.
	if att.ContentSource != "module:proj" {
		t.Errorf("ContentSource = %q; want module:proj", att.ContentSource)
	}

	// Content must contain the root and conventions (the always-file).
	if !strings.Contains(att.Content, "# Project") {
		t.Error("Content missing root file (# Project)")
	}
	if !strings.Contains(att.Content, "# Conventions") {
		t.Error("Content missing always-file (# Conventions)")
	}
	// The on-demand file must NOT be included.
	if strings.Contains(att.Content, "# Reference") {
		t.Error("Content unexpectedly includes on-demand file (# Reference)")
	}

	// Attachment adder must have been called with the right scope.
	if adder.last.ScopeKind != "session" || adder.last.ScopeID != "sess-1" {
		t.Errorf("adder.last scope = %q/%q; want session/sess-1",
			adder.last.ScopeKind, adder.last.ScopeID)
	}
	if adder.last.Kind != "system" {
		t.Errorf("adder.last.Kind = %q; want system", adder.last.Kind)
	}
}

// ── AttachModule — not a module ────────────────────────────────────────────

func TestAttachModule_NotAModule(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithLib(t)
	ctx := context.Background()

	// Create a plain directory without context.md.
	if err := api.Save(ctx, "plain/doc.md", "hello"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// AttachModule must fail when the dir is not a valid module.
	_, err := api.AttachModule(ctx, "global", "", "plain")
	if err == nil {
		t.Fatal("AttachModule should fail for non-module dir")
	}
	if !errors.Is(err, contextsview.ErrInvalidModule) {
		t.Errorf("error = %v; want ErrInvalidModule", err)
	}
}

// ── AttachModule — nil library ─────────────────────────────────────────────

func TestAttachModule_NilLibrary(t *testing.T) {
	t.Parallel()
	api := contextsview.New(nil)
	_, err := api.AttachModule(context.Background(), "global", "", "mod")
	if !errors.Is(err, contextsview.ErrLibraryUnavailable) {
		t.Fatalf("error = %v; want ErrLibraryUnavailable", err)
	}
}

// ── AttachModule — no attachment adder wired ──────────────────────────────

func TestAttachModule_NoAdder_ReturnsContent(t *testing.T) {
	t.Parallel()
	api, _ := newAPIWithLib(t)
	// No adder wired — AttachModule should still return resolved content.
	ctx := context.Background()
	if err := api.CreateFolder(ctx, "bare"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	att, err := api.AttachModule(ctx, "project", "proj-1", "bare")
	if err != nil {
		t.Fatalf("AttachModule: %v", err)
	}
	// Content should contain the scaffold content.
	if !strings.Contains(att.Content, "always:") {
		t.Errorf("expected scaffold content; got: %q", att.Content)
	}
	if att.ContentSource != "module:bare" {
		t.Errorf("ContentSource = %q; want module:bare", att.ContentSource)
	}
}

// ── Module resolution: root+always injected, others not ───────────────────

func TestModuleResolution_EagerVsOnDemand(t *testing.T) {
	t.Parallel()
	api, lib := newAPIWithLib(t)
	ctx := context.Background()
	_ = lib

	if err := api.CreateFolder(ctx, "eager"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	root := "---\nalways:\n  - a.md\n---\n# Root\n"
	if err := api.Save(ctx, "eager/context.md", root); err != nil {
		t.Fatalf("Save root: %v", err)
	}
	if err := api.Save(ctx, "eager/a.md", "# A"); err != nil {
		t.Fatalf("Save a.md: %v", err)
	}
	if err := api.Save(ctx, "eager/b.md", "# B (on-demand)"); err != nil {
		t.Fatalf("Save b.md: %v", err)
	}

	att, err := api.AttachModule(ctx, "global", "", "eager")
	if err != nil {
		t.Fatalf("AttachModule: %v", err)
	}
	if !strings.Contains(att.Content, "# Root") {
		t.Error("Content missing root")
	}
	if !strings.Contains(att.Content, "# A") {
		t.Error("Content missing always-file a.md")
	}
	if strings.Contains(att.Content, "# B (on-demand)") {
		t.Error("Content should NOT include on-demand file b.md")
	}
}
