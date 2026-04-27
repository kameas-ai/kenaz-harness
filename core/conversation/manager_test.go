package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

func TestManager_CreateBranch_ProducesChildSessionAndBranchRow(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore,
		session.WithClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }),
	)
	ctx := context.Background()
	parent, err := sessMgr.Create(ctx, "trunk")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	mgr := NewManager(store, sessMgr,
		WithIDGen(func() (string, error) { return "br-1", nil }),
		WithClock(func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }),
	)
	br, child, err := mgr.CreateBranch(ctx, ForkOptions{
		ParentSessionID: parent.ID,
		Title:           "side question",
		TaskHint:        "what's the latest version of dep X",
		ProviderID:      "anthropic",
		ModelID:         "haiku",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if br.ID != "br-1" {
		t.Errorf("branch id = %q, want br-1", br.ID)
	}
	if br.ParentSessionID != parent.ID {
		t.Errorf("parent id mismatch")
	}
	if br.ChildSessionID == "" || br.ChildSessionID == parent.ID {
		t.Errorf("child id = %q must be a fresh session", br.ChildSessionID)
	}
	if child.ID != br.ChildSessionID {
		t.Errorf("returned child id mismatch")
	}
	if child.Name != "trunk (branch)" {
		t.Errorf("default child name = %q, want %q", child.Name, "trunk (branch)")
	}
	if br.Status != BranchStatusActive {
		t.Errorf("status = %q, want active", br.Status)
	}
}

func TestManager_LifecycleTransitions(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	mgr := NewManager(store, nil,
		WithClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }),
	)
	ctx := context.Background()
	br := Branch{
		ID:              "br1",
		ParentSessionID: "p1",
		ChildSessionID:  "c1",
		CreatedAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := mgr.CreateRaw(ctx, br); err != nil {
		t.Fatalf("CreateRaw: %v", err)
	}

	if err := mgr.MarkMerging(ctx, "br1"); err != nil {
		t.Fatalf("MarkMerging: %v", err)
	}
	got, _ := mgr.Get(ctx, "br1")
	if got.Status != BranchStatusMerging {
		t.Errorf("status = %q, want merging", got.Status)
	}

	if err := mgr.MarkMerged(ctx, "br1"); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}
	got, _ = mgr.Get(ctx, "br1")
	if got.Status != BranchStatusMerged || got.MergedAt == nil {
		t.Errorf("post-merge: %+v", got)
	}

	br2 := Branch{
		ID: "br2", ParentSessionID: "p1", ChildSessionID: "c2",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	_ = mgr.CreateRaw(ctx, br2)
	if err := mgr.MarkAbandoned(ctx, "br2"); err != nil {
		t.Fatalf("MarkAbandoned: %v", err)
	}
	got2, _ := mgr.Get(ctx, "br2")
	if got2.Status != BranchStatusAbandoned || got2.AbandonedAt == nil {
		t.Errorf("post-abandon: %+v", got2)
	}
}

func TestManager_AppendMessageRef(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	mgr := NewManager(store, nil)
	ctx := context.Background()
	br := Branch{
		ID:              "br1",
		ParentSessionID: "p1",
		ChildSessionID:  "c1",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := mgr.CreateRaw(ctx, br); err != nil {
		t.Fatalf("CreateRaw: %v", err)
	}
	for _, mid := range []string{"m1", "m2"} {
		if err := mgr.AppendMessageRef(ctx, "br1", mid); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	refs, err := mgr.ListMessageRefs(ctx, "br1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %d, want 2", len(refs))
	}
}
