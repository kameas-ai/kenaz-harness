package contexts_test

import (
	"context"
	"errors"
	"testing"

	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

func newAPI(t *testing.T) *contextsview.API {
	t.Helper()
	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return contextsview.New(lib)
}

func TestNewWithNilLibrary(t *testing.T) {
	t.Parallel()
	api := contextsview.New(nil)
	if _, err := api.List(context.Background()); !errors.Is(err, contextsview.ErrLibraryUnavailable) {
		t.Fatalf("List with nil library = %v; want ErrLibraryUnavailable", err)
	}
	if _, err := api.RootPath(context.Background()); !errors.Is(err, contextsview.ErrLibraryUnavailable) {
		t.Fatalf("RootPath with nil = %v; want ErrLibraryUnavailable", err)
	}
	// RecentlyApplied + List degrade gracefully on a nil library so
	// the frontend renders the empty-state card. The other mutators
	// reject — there's nothing to mutate.
	got, err := api.RecentlyApplied(context.Background(), 5)
	if err != nil {
		t.Fatalf("RecentlyApplied: %v", err)
	}
	if got == nil {
		t.Error("RecentlyApplied returned nil; want empty slice")
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	api := newAPI(t)
	ctx := context.Background()
	if err := api.Save(ctx, "notes/hello.md", "# hi"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	tree, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if tree.Kind != contextsview.KindFolder {
		t.Fatalf("root kind = %v; want folder", tree.Kind)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("root children = %d; want 1", len(tree.Children))
	}
	folder := tree.Children[0]
	if folder.Kind != contextsview.KindFolder || folder.Name != "notes" {
		t.Fatalf("child = %+v; want notes folder", folder)
	}
	if len(folder.Children) != 1 || folder.Children[0].Path != "notes/hello.md" {
		t.Fatalf("nested file missing: %+v", folder.Children)
	}
	got, err := api.Get(ctx, "notes/hello.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "# hi" {
		t.Fatalf("Get content = %q", got)
	}
}

func TestPathTraversalRejectedAtBindings(t *testing.T) {
	t.Parallel()
	api := newAPI(t)
	ctx := context.Background()
	if err := api.Save(ctx, "../escape.md", "x"); !errors.Is(err, corecontexts.ErrInvalidPath) {
		t.Fatalf("Save traversal = %v; want ErrInvalidPath", err)
	}
	if _, err := api.Get(ctx, "../escape.md"); !errors.Is(err, corecontexts.ErrInvalidPath) {
		t.Fatalf("Get traversal = %v; want ErrInvalidPath", err)
	}
}

func TestDeleteAndRename(t *testing.T) {
	t.Parallel()
	api := newAPI(t)
	ctx := context.Background()
	if err := api.Save(ctx, "a.md", "x"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := api.Rename(ctx, "a.md", "b.md"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := api.Delete(ctx, "b.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	tree, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tree.Children) != 0 {
		t.Fatalf("post-delete tree had children: %+v", tree.Children)
	}
}
