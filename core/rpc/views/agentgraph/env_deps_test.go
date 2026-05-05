package agentgraph_test

import (
	"context"
	"errors"
	"testing"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/conversation"
	graphview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	corebash "github.com/sigil-tech/kaneaz-harness/core/tools/bash"
)

// TestEnvDeps_BashOutputStoreWiring exercises the bash store + adapter
// loop: a transcript written to corebash.Store via the adapter is
// readable through the agentgraph.BashOutputStore seam.
func TestEnvDeps_BashOutputStoreWiring(t *testing.T) {
	t.Parallel()
	store := corebash.NewStore()
	store.Put("run-abc", corebash.Record{
		RunID:    "run-abc",
		Command:  "echo hi",
		Stdout:   "hi\n",
		ExitCode: 0,
	})

	adapter := graphview.NewBashOutputStoreAdapter(store)
	out, err := adapter.Get(context.Background(), "run-abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.Stdout != "hi\n" {
		t.Errorf("Stdout = %q want %q", out.Stdout, "hi\n")
	}
	if out.RunID != "run-abc" {
		t.Errorf("RunID = %q want %q", out.RunID, "run-abc")
	}

	// Missing id surfaces ErrNoBashOutput so the kernel's executor
	// short-circuits with the correct sentinel.
	if _, err := adapter.Get(context.Background(), "missing"); !errors.Is(err, coreag.ErrNoBashOutput) {
		t.Errorf("missing-id err = %v want ErrNoBashOutput", err)
	}
}

// TestEnvDeps_BashOutputStoreNilFallback verifies that a nil store
// still satisfies the seam without panicking — the seam returns
// ErrNoBashOutput so the executor falls through to its no-cache path.
func TestEnvDeps_BashOutputStoreNilFallback(t *testing.T) {
	t.Parallel()
	adapter := graphview.NewBashOutputStoreAdapter(nil)
	_, err := adapter.Get(context.Background(), "any")
	if !errors.Is(err, coreag.ErrNoBashOutput) {
		t.Errorf("nil store err = %v want ErrNoBashOutput", err)
	}
}

// TestEnvDeps_PolicyGateAllowAll asserts the AllowAll fallback passes
// every check. This is the boot-stage default the chassis ships with.
func TestEnvDeps_PolicyGateAllowAll(t *testing.T) {
	t.Parallel()
	gate := graphview.NewPolicyGateAdapter(nil) // nil ⇒ AllowAll
	ctx := context.Background()
	if err := gate.CheckFileRead(ctx, "/tmp/foo"); err != nil {
		t.Errorf("CheckFileRead = %v want nil", err)
	}
	if err := gate.CheckFileWrite(ctx, "/tmp/foo"); err != nil {
		t.Errorf("CheckFileWrite = %v want nil", err)
	}
	if err := gate.CheckStateRead(ctx, "file"); err != nil {
		t.Errorf("CheckStateRead = %v want nil", err)
	}
	if err := gate.CheckStateWrite(ctx, "file"); err != nil {
		t.Errorf("CheckStateWrite = %v want nil", err)
	}
}

// TestEnvDeps_CorpusAdapterNilManager asserts the corpus adapter
// degrades cleanly when no manager is wired.
func TestEnvDeps_CorpusAdapterNilManager(t *testing.T) {
	t.Parallel()
	adapter := graphview.NewCorpusBackendAdapter(nil)
	_, err := adapter.Search(context.Background(), []string{"c1"}, "q", 5)
	if !errors.Is(err, coreag.ErrNoCorpus) {
		t.Errorf("Search err = %v want ErrNoCorpus", err)
	}
	if _, err := adapter.Enqueue(context.Background(), "c1", "/tmp"); !errors.Is(err, coreag.ErrNoCorpus) {
		t.Errorf("Enqueue err = %v want ErrNoCorpus", err)
	}
}

// TestEnvDeps_BranchSeamNilFallback asserts nil managers degrade
// cleanly to ErrNoBranchSeam.
func TestEnvDeps_BranchSeamNilFallback(t *testing.T) {
	t.Parallel()
	adapter := graphview.NewBranchSeamAdapter(nil, nil)
	_, err := adapter.Fork(context.Background(), coreag.ForkRequest{ParentSessionID: "p"})
	if !errors.Is(err, coreag.ErrNoBranchSeam) {
		t.Errorf("Fork err = %v want ErrNoBranchSeam", err)
	}
	if _, err := adapter.PullChildTail(context.Background(), "br", 5); !errors.Is(err, coreag.ErrNoBranchSeam) {
		t.Errorf("PullChildTail err = %v want ErrNoBranchSeam", err)
	}
	if err := adapter.MarkMerged(context.Background(), "br"); !errors.Is(err, coreag.ErrNoBranchSeam) {
		t.Errorf("MarkMerged err = %v want ErrNoBranchSeam", err)
	}
}

// TestEnvDeps_BranchSeamAdapter_CreationPath verifies that the implicit
// edit-and-resend fork path (via BranchSeamAdapter.Fork) persists
// CreationPath="edit_resend" on the branch row.
// (branching-ux-polish-01KQ8TD7 WP07 — agentgraph integration extension)
func TestEnvDeps_BranchSeamAdapter_CreationPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Wire in-memory stores — same approach as the conversation unit tests.
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	brStore := conversation.NewMemoryStore()
	brMgr := conversation.NewManager(brStore, sessMgr)

	// Create a parent session.
	parent, err := sessMgr.Create(ctx, "parent")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	adapter := graphview.NewBranchSeamAdapter(brMgr, sessMgr)
	handle, err := adapter.Fork(ctx, coreag.ForkRequest{
		ParentSessionID: parent.ID,
		Title:           "fork-test",
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if handle.BranchID == "" {
		t.Fatal("handle.BranchID is empty")
	}

	// Look up the branch row and verify CreationPath.
	branches, err := brMgr.ListByParent(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ListByParent: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("branches count = %d, want 1", len(branches))
	}
	if branches[0].CreationPath != "edit_resend" {
		t.Errorf("CreationPath = %q, want edit_resend", branches[0].CreationPath)
	}
}

// TestEnvDeps_MemoryStoreNilFallback asserts the memory adapter
// surfaces ErrNoMemory when no store is wired.
func TestEnvDeps_MemoryStoreNilFallback(t *testing.T) {
	t.Parallel()
	adapter := graphview.NewMemoryStoreAdapter(nil, nil)
	_, _, err := adapter.Write(context.Background(), coreag.MemoryWrite{Content: "x"})
	if !errors.Is(err, coreag.ErrNoMemory) {
		t.Errorf("Write err = %v want ErrNoMemory", err)
	}
	_, err = adapter.Read(context.Background(), coreag.MemoryReadFilter{Query: "q"})
	if !errors.Is(err, coreag.ErrNoMemory) {
		t.Errorf("Read err = %v want ErrNoMemory", err)
	}
}

// TestEnvDeps_AppliedToEnv exercises the WithEnvDeps option: the
// Manager threads each non-nil seam onto the per-Run Env so the
// kernel's nil-stub fallbacks are bypassed.
func TestEnvDeps_AppliedToEnv(t *testing.T) {
	t.Parallel()
	bashStore := corebash.NewStore()
	bashStore.Put("run-x", corebash.Record{RunID: "run-x", Stdout: "ok"})
	deps := graphview.EnvDeps{
		Branch:     graphview.NewBranchSeamAdapter(nil, nil),
		Corpus:     graphview.NewCorpusBackendAdapter(nil),
		Memory:     graphview.NewMemoryStoreAdapter(nil, nil),
		BashOutput: graphview.NewBashOutputStoreAdapter(bashStore),
		Policy:     graphview.NewPolicyGateAdapter(nil),
		BashStore:  bashStore,
	}
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(t.TempDir()),
		graphview.WithEnvDeps(deps),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Sanity: the manager constructed without panicking. The wiring
	// is exercised the next time startRun fires; that path is covered
	// by the kernel-level integration tests (kernel_test.go) which
	// thread Env directly. Here we just witness that the option
	// signature is stable and the manager accepts the deps.
	if mgr == nil {
		t.Fatal("nil manager")
	}
}
