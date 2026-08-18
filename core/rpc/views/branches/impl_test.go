package branches

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
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

// ── WP04: CreateBranch subagent enrichment ───────────────────────────────

func TestAPI_CreateBranch_SubagentEnrichment(t *testing.T) {
	t.Parallel()
	api, sessMgr, convMgr := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID:   parent.ID,
		Title:             "Parallel task",
		RecommendationID:  "rec-abc123",
		AdvisorSignals:    []string{"can_you_also", "while_youre_at_it"},
		AdvisorConfidence: 0.91,
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if !br.SubagentBranch {
		t.Error("SubagentBranch = false, want true")
	}
	if br.RecommendationID != "rec-abc123" {
		t.Errorf("RecommendationID = %q, want rec-abc123", br.RecommendationID)
	}
	if len(br.AdvisorSignals) != 2 {
		t.Errorf("AdvisorSignals len = %d, want 2", len(br.AdvisorSignals))
	}
	// Verify the row was persisted with subagent metadata.
	row, err := convMgr.Get(ctx, br.ID)
	if err != nil {
		t.Fatalf("Get branch: %v", err)
	}
	if !row.SubagentBranch {
		t.Error("persisted SubagentBranch = false, want true")
	}
}

// ── WP04: SetAdvisorDismissed ────────────────────────────────────────────

func TestAPI_SetAdvisorDismissed(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	sess, _ := sessMgr.Create(ctx, "my-session")
	if err := api.SetAdvisorDismissed(ctx, sess.ID, true); err != nil {
		t.Fatalf("SetAdvisorDismissed: %v", err)
	}
	// Verify the store has the dismissed flag.
	rec, err := sessMgr.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !rec.BranchAdvisorDismissed {
		t.Error("BranchAdvisorDismissed = false, want true")
	}
	// Unset it.
	if err := api.SetAdvisorDismissed(ctx, sess.ID, false); err != nil {
		t.Fatalf("SetAdvisorDismissed(false): %v", err)
	}
	rec2, _ := sessMgr.Get(ctx, sess.ID)
	if rec2.BranchAdvisorDismissed {
		t.Error("BranchAdvisorDismissed = true after unset, want false")
	}
}

func TestAPI_SetAdvisorDismissed_EmptyID(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestStack(t)
	if err := api.SetAdvisorDismissed(context.Background(), "", true); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("got %v, want ErrInvalidArg", err)
	}
}

// ── WP05: ProposeReintegrationSummary ───────────────────────────────────

func TestAPI_ProposeReintegrationSummary_HappyPath(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "side",
	})
	// Add some assistant messages on the child branch.
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{Role: session.RoleUser, Content: "user turn"})
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{Role: session.RoleAssistant, Content: "assistant turn one"})
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{Role: session.RoleAssistant, Content: "assistant turn two"})

	prop, err := api.ProposeReintegrationSummary(ctx, br.ChildSessionID)
	if err != nil {
		t.Fatalf("ProposeReintegrationSummary: %v", err)
	}
	if prop.ProposedSummary == "" {
		t.Error("ProposedSummary = empty, want non-empty")
	}
	if prop.TokenCount <= 0 {
		t.Errorf("TokenCount = %d, want > 0", prop.TokenCount)
	}
	if prop.Model != "rule_based" {
		t.Errorf("Model = %q, want rule_based", prop.Model)
	}
}

func TestAPI_ProposeReintegrationSummary_EmptyBranch(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "empty-branch",
	})
	// Branch has only the handoff user message — no assistant turns.
	prop, err := api.ProposeReintegrationSummary(ctx, br.ChildSessionID)
	if err != nil {
		t.Fatalf("ProposeReintegrationSummary: %v", err)
	}
	if prop.ProposedSummary != "" {
		t.Errorf("ProposedSummary = %q, want empty for no-assistant-turns case", prop.ProposedSummary)
	}
}

// TestAPI_ProposeReintegrationSummary_RespectsPersistedMaxTokens pins
// FR-003 (engineer-truth-pass-01PMTP01 WP02, finding B2): before WP02,
// impl.go hardcoded `const maxTokens = 2000` and Settings.
// BranchReintegrationMaxTokens / EffectiveBranchReintegrationMaxTokens
// had zero callers, so a persisted non-default value was silently
// ignored. This drives a real 500-token budget through Config.Settings
// and asserts the truncation actually uses it.
func TestAPI_ProposeReintegrationSummary_RespectsPersistedMaxTokens(t *testing.T) {
	t.Parallel()
	sessStore := session.NewMemoryStore()
	sessMgr := session.NewManager(sessStore,
		session.WithClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }),
	)
	convStore := conversation.NewMemoryStore()
	convMgr := conversation.NewManager(convStore, sessMgr,
		conversation.WithClock(func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }),
	)
	api := New(Config{
		Conversations: convMgr,
		Sessions:      sessMgr,
		Settings: func() settings.Settings {
			return settings.Settings{BranchReintegrationMaxTokens: 500}
		},
	})
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "side",
	})
	// 3000 runes of assistant content — comfortably past the 500-token
	// (2000-rune) budget, so a real clamp is exercised either way.
	long := strings.Repeat("a", 3000)
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{Role: session.RoleAssistant, Content: long})

	prop, err := api.ProposeReintegrationSummary(ctx, br.ChildSessionID)
	if err != nil {
		t.Fatalf("ProposeReintegrationSummary: %v", err)
	}
	const wantRunes = 500 * 4 // runesPerToken (impl.go) * persisted 500-token budget
	if got := utf8.RuneCountInString(prop.ProposedSummary); got != wantRunes {
		t.Errorf("summary runes = %d, want %d (persisted BranchReintegrationMaxTokens=500 ignored)", got, wantRunes)
	}
	if prop.TokenCount != 500 {
		t.Errorf("TokenCount = %d, want 500", prop.TokenCount)
	}
}

// TestAPI_ProposeReintegrationSummary_UnsetSettingsUsesDefault pins the
// second half of FR-003: a nil Config.Settings (or one that returns the
// zero Settings) must still fall back to
// settings.DefaultBranchReintegrationMaxTokens (2000), the same value
// the pre-WP02 hardcoded constant produced — so callers that never set
// the field see no behaviour change.
func TestAPI_ProposeReintegrationSummary_UnsetSettingsUsesDefault(t *testing.T) {
	t.Parallel()
	// api has no Config.Settings at all (newTestStack's Config leaves it nil).
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "side",
	})
	long := strings.Repeat("b", 9000)
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{Role: session.RoleAssistant, Content: long})

	prop, err := api.ProposeReintegrationSummary(ctx, br.ChildSessionID)
	if err != nil {
		t.Fatalf("ProposeReintegrationSummary: %v", err)
	}
	const wantRunes = settings.DefaultBranchReintegrationMaxTokens * 4 // 8000
	if got := utf8.RuneCountInString(prop.ProposedSummary); got != wantRunes {
		t.Errorf("summary runes = %d, want %d (default budget)", got, wantRunes)
	}
	if prop.TokenCount != settings.DefaultBranchReintegrationMaxTokens {
		t.Errorf("TokenCount = %d, want %d", prop.TokenCount, settings.DefaultBranchReintegrationMaxTokens)
	}
}

func TestAPI_ProposeReintegrationSummary_EmptyID(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestStack(t)
	if _, err := api.ProposeReintegrationSummary(context.Background(), ""); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("got %v, want ErrInvalidArg", err)
	}
}

// ── WP07: CommitReintegration ────────────────────────────────────────────

func TestAPI_CommitReintegration_HappyPath(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "side-work",
	})
	_, _ = sessMgr.AppendMessage(ctx, br.ChildSessionID, session.Message{
		Role: session.RoleAssistant, Content: "Here is what I did on the side task.",
	})

	err := api.CommitReintegration(ctx, CommitReintegrationOptions{
		BranchSessionID:  br.ChildSessionID,
		FinalSummaryText: "The side task was completed successfully. Endpoint deployed.",
		WasEdited:        true,
	})
	if err != nil {
		t.Fatalf("CommitReintegration: %v", err)
	}

	// Branch should be merged.
	st, _ := api.GetBranchStatus(ctx, br.ID)
	if st.Branch.Status != "merged" {
		t.Errorf("status = %q, want merged", st.Branch.Status)
	}

	// Parent should have a system message with the summary.
	msgs, _ := sessMgr.ListMessages(ctx, parent.ID)
	var found bool
	for _, m := range msgs {
		if m.Role == session.RoleSystem && strings.Contains(m.Content, "The side task was completed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("parent missing system message with committed summary")
	}
}

func TestAPI_CommitReintegration_EmptyID(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestStack(t)
	err := api.CommitReintegration(context.Background(), CommitReintegrationOptions{
		BranchSessionID:  "",
		FinalSummaryText: "summary",
	})
	if !errors.Is(err, ErrInvalidArg) {
		t.Errorf("got %v, want ErrInvalidArg", err)
	}
}

func TestAPI_CommitReintegration_EmptySummary(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID, Title: "side",
	})
	err := api.CommitReintegration(ctx, CommitReintegrationOptions{
		BranchSessionID:  br.ChildSessionID,
		FinalSummaryText: "   ",
	})
	if err == nil {
		t.Error("expected error for empty summary, got nil")
	}
}
