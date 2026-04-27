package conversation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_CreateGetList(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	b := Branch{
		ID:              "br1",
		ParentSessionID: "p1",
		ChildSessionID:  "c1",
		ModelID:         "haiku",
		ProviderID:      "anthropic",
		Title:           "side question",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "br1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != BranchStatusActive {
		t.Errorf("default status = %q, want active", got.Status)
	}
	if got.Kind != BranchKindFork {
		t.Errorf("default kind = %q, want fork", got.Kind)
	}
	if got.Title != "side question" {
		t.Errorf("title round-trip mismatch: %q", got.Title)
	}

	// Same id rejected.
	if err := store.Create(ctx, b); !errors.Is(err, ErrBranchExists) {
		t.Errorf("re-create: got %v, want ErrBranchExists", err)
	}

	// Required fields.
	bad := Branch{ID: "", ParentSessionID: "p1", ChildSessionID: "c2"}
	if err := store.Create(ctx, bad); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("missing id: got %v, want ErrInvalidArg", err)
	}

	// ListByParent ordering.
	older := Branch{ID: "br2", ParentSessionID: "p1", ChildSessionID: "c2",
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	if err := store.Create(ctx, older); err != nil {
		t.Fatalf("create older: %v", err)
	}
	rows, err := store.ListByParent(ctx, "p1")
	if err != nil {
		t.Fatalf("ListByParent: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID != "br2" {
		t.Errorf("oldest first = %q, want br2", rows[0].ID)
	}
}

func TestMemoryStore_Lifecycle(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	b := Branch{
		ID:              "br1",
		ParentSessionID: "p1",
		ChildSessionID:  "c1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mergeAt := now.Add(time.Minute)
	if err := store.MarkMerged(ctx, "br1", mergeAt); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}
	got, _ := store.Get(ctx, "br1")
	if got.Status != BranchStatusMerged {
		t.Errorf("status = %q, want merged", got.Status)
	}
	if got.MergedAt == nil || !got.MergedAt.Equal(mergeAt) {
		t.Errorf("merged_at = %v, want %v", got.MergedAt, mergeAt)
	}

	// Abandon a different branch.
	b2 := Branch{
		ID:              "br2",
		ParentSessionID: "p1",
		ChildSessionID:  "c2",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.Create(ctx, b2); err != nil {
		t.Fatalf("Create br2: %v", err)
	}
	if err := store.MarkAbandoned(ctx, "br2", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkAbandoned: %v", err)
	}
	got2, _ := store.Get(ctx, "br2")
	if got2.Status != BranchStatusAbandoned {
		t.Errorf("status = %q, want abandoned", got2.Status)
	}
	if got2.AbandonedAt == nil {
		t.Errorf("abandoned_at unset")
	}

	if err := store.MarkMerged(ctx, "missing", now); !errors.Is(err, ErrBranchNotFound) {
		t.Errorf("MarkMerged missing: got %v, want ErrBranchNotFound", err)
	}
}

func TestMemoryStore_MessageRefs(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	b := Branch{
		ID: "br1", ParentSessionID: "p1", ChildSessionID: "c1",
		CreatedAt: now, UpdatedAt: now,
	}
	_ = store.Create(ctx, b)

	for i, msgID := range []string{"m1", "m2", "m3"} {
		ref := MessageRef{BranchID: "br1", Seq: int64(i), ParentMsgID: msgID}
		if err := store.AppendMessageRef(ctx, ref); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	rows, err := store.ListMessageRefs(ctx, "br1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].ParentMsgID != "m1" || rows[2].ParentMsgID != "m3" {
		t.Errorf("ordering: %+v", rows)
	}

	// Upgrade one ref to point at a child message id.
	if err := store.UpdateChildMsgID(ctx, "br1", 1, "child-m2"); err != nil {
		t.Fatalf("UpdateChildMsgID: %v", err)
	}
	rows2, _ := store.ListMessageRefs(ctx, "br1")
	if rows2[1].ChildMsgID != "child-m2" {
		t.Errorf("child id: %v", rows2[1])
	}
}

func TestTreeOps(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	// p1 → c1 (br1), p1 → c2 (br2), c1 → c3 (br3) (defensive multi-level)
	mustCreate := func(id, parent, child string, t time.Time) {
		if err := store.Create(ctx, Branch{
			ID: id, ParentSessionID: parent, ChildSessionID: child,
			CreatedAt: t, UpdatedAt: t,
		}); err != nil {
			panic(err)
		}
	}
	mustCreate("br1", "p1", "c1", now)
	mustCreate("br2", "p1", "c2", now.Add(time.Minute))
	mustCreate("br3", "c1", "c3", now.Add(2*time.Minute))

	children, err := ListChildren(ctx, store, "p1")
	if err != nil {
		t.Fatalf("ListChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}

	// Walk c3 → c1 → p1.
	walk, err := WalkToRoot(ctx, store, "c3")
	if err != nil {
		t.Fatalf("WalkToRoot: %v", err)
	}
	if len(walk) != 2 {
		t.Fatalf("walk len = %d, want 2", len(walk))
	}
	if walk[0].ID != "br3" || walk[1].ID != "br1" {
		t.Errorf("walk order: %v", walk)
	}

	// Common ancestor of c2 and c3 is p1.
	got, err := FindCommonAncestor(ctx, store, "c2", "c3")
	if err != nil {
		t.Fatalf("FindCommonAncestor: %v", err)
	}
	if got != "p1" {
		t.Errorf("ancestor = %q, want p1", got)
	}

	// Same session is its own ancestor.
	got2, _ := FindCommonAncestor(ctx, store, "c1", "c1")
	if got2 != "c1" {
		t.Errorf("self-ancestor = %q, want c1", got2)
	}
}
