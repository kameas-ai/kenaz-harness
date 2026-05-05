package conversation

// integration_test.go — branching-ux-polish-01KQ8TD7 WP07.
//
// Full explicit-branch round-trip:
//   parent session → 5 turns → CreateBranchAtMessage at turn 3 →
//   child has exactly 3 turns + branch row + audit event.
//
// Also exercises the ListBranchesByProject aggregation and the
// cascade-delete helper (DeleteChildrenOf).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// TestIntegration_ExplicitBranch_FullRoundTrip verifies:
//  1. A parent session with 5 messages is created.
//  2. CreateBranchAtMessage at msg[2] (0-indexed → "turn 3") produces
//     a child session whose ListMessages returns exactly 3 messages.
//  3. The branches row carries CreationPath="explicit" and
//     ParentMessageID = msg[2].ID.
//  4. ListBranchesByProject returns the branch under the parent's project.
func TestIntegration_ExplicitBranch_FullRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── Set up stores ─────────────────────────────────────────────────
	sessStore := session.NewMemoryStore()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	sessMgr := session.NewManager(sessStore, session.WithClock(clock))
	brStore := NewMemoryStore()
	brMgr := NewManager(brStore, sessMgr, WithClock(clock))

	// ── Create parent session inside a project ────────────────────────
	projectID := "proj-1"
	parent, err := sessMgr.CreateInProject(ctx, "alpha", &projectID)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// ── Append 5 messages to parent ───────────────────────────────────
	roles := []session.Role{"user", "assistant", "user", "assistant", "user"}
	var msgIDs []string
	for i, role := range roles {
		msg, err := sessMgr.AppendMessage(ctx, parent.ID, session.Message{
			Role:    role,
			Content: fmt.Sprintf("turn %d", i+1),
		})
		if err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
		msgIDs = append(msgIDs, msg.ID)
	}

	if len(msgIDs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgIDs))
	}

	// ── Branch at message index 2 (turn 3) ───────────────────────────
	anchorID := msgIDs[2]
	br, child, err := brMgr.CreateBranchAtMessage(ctx, ForkAtMessageOptions{
		ParentSessionID: parent.ID,
		ParentMessageID: anchorID,
		Title:           "branch at turn 3",
	})
	if err != nil {
		t.Fatalf("CreateBranchAtMessage: %v", err)
	}

	// ── Assert: branch row metadata ───────────────────────────────────
	if br.CreationPath != "explicit" {
		t.Errorf("CreationPath = %q, want explicit", br.CreationPath)
	}
	if br.ParentMessageID != anchorID {
		t.Errorf("ParentMessageID = %q, want %q", br.ParentMessageID, anchorID)
	}
	if br.BranchTitle != "branch at turn 3" {
		t.Errorf("BranchTitle = %q, want %q", br.BranchTitle, "branch at turn 3")
	}
	if br.ParentSessionID != parent.ID {
		t.Errorf("ParentSessionID mismatch")
	}
	if child.ID == "" || child.ID == parent.ID {
		t.Errorf("child session id %q is invalid", child.ID)
	}

	// ── Assert: child has exactly 3 messages (turns 0-2 inclusive) ────
	childMsgs, err := sessMgr.ListMessages(ctx, child.ID)
	if err != nil {
		t.Fatalf("ListMessages for child: %v", err)
	}
	if len(childMsgs) != 3 {
		t.Errorf("child message count = %d, want 3", len(childMsgs))
	}
	// Content should match the first three turns of the parent.
	for i, m := range childMsgs {
		want := fmt.Sprintf("turn %d", i+1)
		if m.Content != want {
			t.Errorf("child msg[%d] content = %q, want %q", i, m.Content, want)
		}
	}

	// ── Assert: ListBranchesByProject finds the branch ────────────────
	branches, err := brMgr.ListBranchesByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListBranchesByProject: %v", err)
	}
	if len(branches) != 1 {
		t.Errorf("branches count = %d, want 1", len(branches))
	}
	if len(branches) > 0 && branches[0].ID != br.ID {
		t.Errorf("branch id mismatch: %q != %q", branches[0].ID, br.ID)
	}
}

// TestIntegration_DeleteChildrenOf_Cascade verifies that deleting
// children recursively removes grandchildren too, leaving the parent
// untouched.
func TestIntegration_DeleteChildrenOf_Cascade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	brStore := NewMemoryStore()
	brMgr := NewManager(brStore, sessMgr)

	// Create: root → child → grandchild.
	root, _ := sessMgr.Create(ctx, "root")
	child, _ := sessMgr.Create(ctx, "child")
	grand, _ := sessMgr.Create(ctx, "grandchild")

	_ = brMgr.CreateRaw(ctx, Branch{
		ID: "br-rc", ParentSessionID: root.ID, ChildSessionID: child.ID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	_ = brMgr.CreateRaw(ctx, Branch{
		ID: "br-cg", ParentSessionID: child.ID, ChildSessionID: grand.ID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// Delete children of root.
	if err := brMgr.DeleteChildrenOf(ctx, root.ID); err != nil {
		t.Fatalf("DeleteChildrenOf: %v", err)
	}

	// Root must still exist.
	if _, err := sessMgr.Get(ctx, root.ID); err != nil {
		t.Errorf("root was deleted: %v", err)
	}
	// Child must be gone.
	if _, err := sessMgr.Get(ctx, child.ID); err == nil {
		t.Errorf("child session still exists after cascade delete")
	}
	// Grandchild must be gone.
	if _, err := sessMgr.Get(ctx, grand.ID); err == nil {
		t.Errorf("grandchild session still exists after cascade delete")
	}
}

// TestIntegration_ImplicitFork_CreationPath verifies that a branch
// created via CreateBranch (the implicit edit-resend path) correctly
// carries CreationPath="edit_resend" when specified.
func TestIntegration_ImplicitFork_CreationPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore)
	brStore := NewMemoryStore()
	brMgr := NewManager(brStore, sessMgr,
		WithIDGen(func() (string, error) { return "br-implicit", nil }),
	)

	parent, err := sessMgr.Create(ctx, "parent-session")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	br, _, err := brMgr.CreateBranch(ctx, ForkOptions{
		ParentSessionID: parent.ID,
		CreationPath:    "edit_resend",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	if br.CreationPath != "edit_resend" {
		t.Errorf("CreationPath = %q, want edit_resend", br.CreationPath)
	}
}
