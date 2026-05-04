package conversation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// ── WP02 tests ─────────────────────────────────────────────────────────

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

func TestManager_CreateBranchAtMessage_CopiesMessages(t *testing.T) {
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

	// Append 5 messages to the parent.
	var msgIDs []string
	for i, content := range []string{"u1", "a1", "u2", "a2", "u3"} {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		m, err := sessMgr.AppendMessage(ctx, parent.ID, session.Message{Role: role, Content: content})
		if err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		msgIDs = append(msgIDs, m.ID)
	}

	mgr := NewManager(store, sessMgr,
		WithIDGen(func() (string, error) { return "br-explicit-1", nil }),
	)

	// Branch at turn 3 (the 3rd message, index 2).
	br, child, err := mgr.CreateBranchAtMessage(ctx, ForkAtMessageOptions{
		ParentSessionID: parent.ID,
		ParentMessageID: msgIDs[2],
		Title:           "explicit-branch",
	})
	if err != nil {
		t.Fatalf("CreateBranchAtMessage: %v", err)
	}
	if br.CreationPath != "explicit" {
		t.Errorf("creation_path = %q, want explicit", br.CreationPath)
	}
	if br.ParentMessageID != msgIDs[2] {
		t.Errorf("parent_message_id = %q, want %q", br.ParentMessageID, msgIDs[2])
	}

	// Child should have exactly 3 messages (messages 0..2 inclusive).
	childMsgs, err := sessMgr.ListMessages(ctx, child.ID)
	if err != nil {
		t.Fatalf("ListMessages child: %v", err)
	}
	if len(childMsgs) != 3 {
		t.Fatalf("child message count = %d, want 3", len(childMsgs))
	}
	// Verify content order.
	wantContents := []string{"u1", "a1", "u2"}
	for i, msg := range childMsgs {
		if msg.Content != wantContents[i] {
			t.Errorf("child msg[%d].Content = %q, want %q", i, msg.Content, wantContents[i])
		}
	}
}

func TestManager_CreateBranchAtMessage_InvalidMessageID(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")

	mgr := NewManager(store, sessMgr)
	_, _, err := mgr.CreateBranchAtMessage(ctx, ForkAtMessageOptions{
		ParentSessionID: parent.ID,
		ParentMessageID: "non-existent-msg",
	})
	if err == nil {
		t.Fatalf("expected error for non-existent message id")
	}
}

func TestManager_ListBranchesByProject(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	ctx := context.Background()

	// Create a project with two sessions; branch one of them.
	proj := "proj-1"
	parent, err := sessMgr.CreateInProject(ctx, "trunk", &proj)
	if err != nil {
		t.Fatalf("CreateInProject: %v", err)
	}
	// Add a message so branch creation works.
	m, _ := sessMgr.AppendMessage(ctx, parent.ID, session.Message{Role: session.RoleUser, Content: "hello"})

	mgr := NewManager(store, sessMgr,
		WithIDGen(func() (string, error) { return "br-list-1", nil }),
	)
	_, _, err = mgr.CreateBranchAtMessage(ctx, ForkAtMessageOptions{
		ParentSessionID: parent.ID,
		ParentMessageID: m.ID,
	})
	if err != nil {
		t.Fatalf("CreateBranchAtMessage: %v", err)
	}

	branches, err := mgr.ListBranchesByProject(ctx, proj)
	if err != nil {
		t.Fatalf("ListBranchesByProject: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("branch count = %d, want 1", len(branches))
	}
	if branches[0].ParentSessionID != parent.ID {
		t.Errorf("parent id mismatch")
	}
}

func TestManager_DeleteChildrenOf_CascadesRecursively(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	ctx := context.Background()

	// Build a tree: root → child → grandchild.
	root, err := sessMgr.Create(ctx, "root")
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	mRoot, _ := sessMgr.AppendMessage(ctx, root.ID, session.Message{Role: session.RoleUser, Content: "hi"})

	mgr := NewManager(store, sessMgr,
		WithIDGen(func() (string, error) {
			// sequential id generator using a closure counter.
			return "br-cascade-1", nil
		}),
	)
	counter := 0
	mgr.idGen = func() (string, error) {
		counter++
		return fmt.Sprintf("br-cas-%d", counter), nil
	}

	// Create child branch.
	_, child, err := mgr.CreateBranchAtMessage(ctx, ForkAtMessageOptions{
		ParentSessionID: root.ID,
		ParentMessageID: mRoot.ID,
		Title:           "child",
	})
	if err != nil {
		t.Fatalf("CreateBranchAtMessage (child): %v", err)
	}

	// Append a message to child and create grandchild.
	mChild, _ := sessMgr.AppendMessage(ctx, child.ID, session.Message{Role: session.RoleUser, Content: "child-msg"})
	_, grandchild, err := mgr.CreateBranchAtMessage(ctx, ForkAtMessageOptions{
		ParentSessionID: child.ID,
		ParentMessageID: mChild.ID,
		Title:           "grandchild",
	})
	if err != nil {
		t.Fatalf("CreateBranchAtMessage (grandchild): %v", err)
	}

	// DeleteChildrenOf(root) should remove child and grandchild sessions.
	if err := mgr.DeleteChildrenOf(ctx, root.ID); err != nil {
		t.Fatalf("DeleteChildrenOf: %v", err)
	}

	// Child session should be gone.
	if _, err := sessMgr.Get(ctx, child.ID); err == nil {
		t.Errorf("child session %q should have been deleted", child.ID)
	}
	// Grandchild session should be gone.
	if _, err := sessMgr.Get(ctx, grandchild.ID); err == nil {
		t.Errorf("grandchild session %q should have been deleted", grandchild.ID)
	}
	// Root session should be untouched.
	if _, err := sessMgr.Get(ctx, root.ID); err != nil {
		t.Errorf("root session %q should still exist: %v", root.ID, err)
	}
}

func TestManager_ImplicitFork_CreationPath(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")

	mgr := NewManager(store, sessMgr,
		WithIDGen(func() (string, error) { return "br-edit-1", nil }),
	)
	// Simulate the edit-and-resend path by calling CreateBranch with
	// CreationPath="edit_resend".
	br, _, err := mgr.CreateBranch(ctx, ForkOptions{
		ParentSessionID: parent.ID,
		CreationPath:    "edit_resend",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if br.CreationPath != "edit_resend" {
		t.Errorf("creation_path = %q, want edit_resend", br.CreationPath)
	}
}
