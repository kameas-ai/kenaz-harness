package branches

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/conversation"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

func newTestStack(t *testing.T) (*API, *session.Manager, *conversation.Manager) {
	t.Helper()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore,
		session.WithClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }),
	)
	convStore := conversation.NewMemoryStore()
	convMgr := conversation.NewManager(convStore, sessMgr,
		conversation.WithClock(func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }),
	)
	rec := agentgraph.NewBranchRecommender([]agentgraph.ModelInfo{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4", Tier: agentgraph.ModelTierMedium},
		{ProviderID: "anthropic", ModelID: "claude-haiku-4", Tier: agentgraph.ModelTierSmall},
		{ProviderID: "anthropic", ModelID: "claude-opus-4", Tier: agentgraph.ModelTierLarge},
		{ProviderID: "openai", ModelID: "gpt-4o", Tier: agentgraph.ModelTierMedium},
	})
	api := New(Config{
		Conversations: convMgr,
		Sessions:      sessMgr,
		Recommender:   rec,
	})
	return api, sessMgr, convMgr
}

func TestAPI_CreateBranch_HappyPath(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, err := sessMgr.Create(ctx, "trunk")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	br, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID,
		Title:           "side question",
		TaskHint:        "what's the latest version of dep X",
		ModelPreference: "smaller",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if br.ParentSessionID != parent.ID {
		t.Errorf("parent id mismatch")
	}
	if br.ChildSessionID == "" {
		t.Errorf("child session id missing")
	}
	if br.Status != "active" {
		t.Errorf("status = %q, want active", br.Status)
	}
	// Child session should have one message (the handoff/task hint).
	msgs, err := sessMgr.ListMessages(ctx, br.ChildSessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("child msgs = %d, want 1", len(msgs))
	}
	if msgs[0].Role != session.RoleUser {
		t.Errorf("first msg role = %q, want user", msgs[0].Role)
	}
}

func TestAPI_ListBranches(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	for i := 0; i < 3; i++ {
		if _, err := api.CreateBranch(ctx, CreateBranchOptions{
			ParentSessionID: parent.ID,
			Title:           "branch",
		}); err != nil {
			t.Fatalf("CreateBranch: %v", err)
		}
	}
	rows, err := api.ListBranches(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("rows = %d, want 3", len(rows))
	}
}

func TestAPI_MergeBranch(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID,
		Title:           "side",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	// Add an assistant message on the child.
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{
		Role: session.RoleAssistant, Content: "Here's the answer.",
	})
	if err := api.MergeBranch(ctx, br.ID); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	// Branch should be marked merged.
	st, err := api.GetBranchStatus(ctx, br.ID)
	if err != nil {
		t.Fatalf("GetBranchStatus: %v", err)
	}
	if st.Branch.Status != "merged" {
		t.Errorf("status = %q, want merged", st.Branch.Status)
	}
	// Parent should have a system message appended.
	msgs, _ := sessMgr.ListMessages(ctx, parent.ID)
	gotSystem := false
	for _, m := range msgs {
		if m.Role == session.RoleSystem {
			gotSystem = true
			break
		}
	}
	if !gotSystem {
		t.Error("parent missing system summary message")
	}
}

func TestAPI_AbandonBranch(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "side",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := api.AbandonBranch(ctx, br.ID); err != nil {
		t.Fatalf("AbandonBranch: %v", err)
	}
	st, _ := api.GetBranchStatus(ctx, br.ID)
	if st.Branch.Status != "abandoned" {
		t.Errorf("status = %q, want abandoned", st.Branch.Status)
	}
}

func TestAPI_RecommendModel(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")

	rec, err := api.RecommendModel(ctx, parent.ID, "deep dive on architecture", "")
	if err != nil {
		t.Fatalf("RecommendModel: %v", err)
	}
	// "deep dive" → step up. With no parent model recorded, the
	// recommender starts from medium → large.
	if rec.Tier != "large" {
		t.Errorf("tier = %q, want large", rec.Tier)
	}
}

func TestAPI_NilManager_ReturnsErrManagerUnavailable(t *testing.T) {
	t.Parallel()
	api := New(Config{}) // no manager
	ctx := context.Background()
	_, err := api.ListBranches(ctx, "p1")
	if !errors.Is(err, ErrManagerUnavailable) {
		t.Errorf("ListBranches: got %v, want ErrManagerUnavailable", err)
	}
	_, err = api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: "p1"})
	if !errors.Is(err, ErrManagerUnavailable) {
		t.Errorf("CreateBranch: got %v, want ErrManagerUnavailable", err)
	}
	if err := api.AbandonBranch(ctx, "b1"); !errors.Is(err, ErrManagerUnavailable) {
		t.Errorf("AbandonBranch: got %v, want ErrManagerUnavailable", err)
	}
}

func TestAPI_InvalidArgs(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestStack(t)
	ctx := context.Background()
	if _, err := api.ListBranches(ctx, ""); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("ListBranches empty parent: got %v", err)
	}
	if _, err := api.CreateBranch(ctx, CreateBranchOptions{}); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("CreateBranch empty parent: got %v", err)
	}
}
