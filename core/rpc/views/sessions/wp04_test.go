package sessions

// wp04_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-2 / WP04. AC-006, AC-007, AC-008. Real sqlite throughout (spec §6
// rule 1) — session/branch cascade-delete correctness is exactly the
// class of assertion CLAUDE.md's blind spot #2 warns an in-memory store
// fixture can silently get wrong.

import (
	"context"
	"testing"

	coreconv "github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

func newWP04TestRig(t *testing.T) (SessionsAPI, *coreconv.Manager, *session.Manager) {
	t.Helper()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	convStore := coreconv.NewSQLStore(coreconv.NewStorageDB(db))
	convMgr := coreconv.NewManager(convStore, sessMgr)
	api := NewManagerAPI(sessMgr)
	return api, convMgr, sessMgr
}

func mustCreateBranch(t *testing.T, ctx context.Context, convMgr *coreconv.Manager, parentID, title string) session.Record {
	t.Helper()
	_, child, err := convMgr.CreateBranch(ctx, coreconv.ForkOptions{
		ParentSessionID: parentID,
		Title:           title,
		ProviderID:      "anthropic",
		ModelID:         "haiku",
	})
	if err != nil {
		t.Fatalf("CreateBranch(%q): %v", title, err)
	}
	return child
}

// TestWP04_AC006_CascadeOn_DeletesBothChildren.
// Mutation: remove the branch-cascade block in DeleteWithOptions
// (the ON case). Must fail.
func TestWP04_AC006_CascadeOn_DeletesBothChildren(t *testing.T) {
	ctx := context.Background()
	api, convMgr, sessMgr := newWP04TestRig(t)
	api = WithDeleteChildrenOf(api, convMgr.DeleteChildrenOf, func() bool { return true })

	parent, err := sessMgr.Create(ctx, "parent")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child1 := mustCreateBranch(t, ctx, convMgr, parent.ID, "child-1")
	child2 := mustCreateBranch(t, ctx, convMgr, parent.ID, "child-2")

	if err := api.Delete(ctx, parent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := sessMgr.Get(ctx, child1.ID); err == nil {
		t.Errorf("child1 %s still exists after cascade delete", child1.ID)
	}
	if _, err := sessMgr.Get(ctx, child2.ID); err == nil {
		t.Errorf("child2 %s still exists after cascade delete", child2.ID)
	}
}

// TestWP04_AC006_CascadeOff_LeavesChildrenOrphaned.
func TestWP04_AC006_CascadeOff_LeavesChildrenOrphaned(t *testing.T) {
	ctx := context.Background()
	api, convMgr, sessMgr := newWP04TestRig(t)
	api = WithDeleteChildrenOf(api, convMgr.DeleteChildrenOf, func() bool { return false })

	parent, err := sessMgr.Create(ctx, "parent")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child1 := mustCreateBranch(t, ctx, convMgr, parent.ID, "child-1")
	child2 := mustCreateBranch(t, ctx, convMgr, parent.ID, "child-2")

	if err := api.Delete(ctx, parent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := sessMgr.Get(ctx, child1.ID); err != nil {
		t.Errorf("child1 %s should survive as an orphan, got error: %v", child1.ID, err)
	}
	if _, err := sessMgr.Get(ctx, child2.ID); err != nil {
		t.Errorf("child2 %s should survive as an orphan, got error: %v", child2.ID, err)
	}
}

// TestWP04_AC007_PlainDeleteAlsoHonoursTheSetting — Sessions_Delete
// (the plain Delete method, not DeleteWithOptions with explicit
// options) must go through the same cascade, since Delete delegates to
// DeleteWithOptions(ctx, id, DeleteOptions{}).
func TestWP04_AC007_PlainDeleteAlsoHonoursTheSetting(t *testing.T) {
	ctx := context.Background()
	api, convMgr, sessMgr := newWP04TestRig(t)
	api = WithDeleteChildrenOf(api, convMgr.DeleteChildrenOf, func() bool { return true })

	parent, err := sessMgr.Create(ctx, "parent")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child := mustCreateBranch(t, ctx, convMgr, parent.ID, "child")

	// api.Delete is the plain path — not DeleteWithOptions.
	if err := api.Delete(ctx, parent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := sessMgr.Get(ctx, child.ID); err == nil {
		t.Errorf("child %s still exists after api.Delete (plain path)", child.ID)
	}
}

// TestWP04_AC008_NilDeleteChildrenOf_ErrorsRatherThanSilentlySucceeding
// is the class-A guard: a setting that claims cascade-delete is on must
// not silently no-op when the implementation was never wired.
func TestWP04_AC008_NilDeleteChildrenOf_ErrorsRatherThanSilentlySucceeding(t *testing.T) {
	ctx := context.Background()
	api, convMgr, sessMgr := newWP04TestRig(t)
	// enabledFn says ON, but deleteFn is nil — the exact shape this
	// mission's class A defect takes (an assigned Config field the
	// caller forgot to also implement).
	api = WithDeleteChildrenOf(api, nil, func() bool { return true })

	parent, err := sessMgr.Create(ctx, "parent")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child := mustCreateBranch(t, ctx, convMgr, parent.ID, "child")

	err = api.Delete(ctx, parent.ID)
	if err == nil {
		t.Fatalf("expected Delete to error when DeleteBranchesWithParent is on but no cascade implementation is wired — got nil (silent success)")
	}

	// The parent must NOT have been deleted either — a class-A guard
	// that errors AFTER already deleting the parent would leave the
	// children permanently orphaned with no parent to re-attempt from.
	if _, gerr := sessMgr.Get(ctx, parent.ID); gerr != nil {
		t.Errorf("parent %s should NOT be deleted when the cascade guard errors, got: %v", parent.ID, gerr)
	}
	if _, gerr := sessMgr.Get(ctx, child.ID); gerr != nil {
		t.Errorf("child %s should still exist, got: %v", child.ID, gerr)
	}
}
