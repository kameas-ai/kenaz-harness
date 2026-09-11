package branches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
)

// fakeAuditEmitter records audit.Event emissions in a thread-safe slice.
// Race-safe per CLAUDE.md's canonical fake pattern: reads only ever go
// through snapshot().
type fakeAuditEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (f *fakeAuditEmitter) Emit(_ context.Context, e audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeAuditEmitter) snapshot() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]audit.Event, len(f.events))
	copy(out, f.events)
	return out
}

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

// TestAPI_CreateBranch_LegacyPathEmitsAudit is the falsifying test for
// audit-that-tells-the-truth-01PMZA10 WP06 / docs/unwired-ledger.md's
// "branch.created is audited on one path and not the other" finding.
//
// The ordinary "+ Fork" button flow (CreateBranchModal.vue) never sets
// ParentMessageID, which routes CreateBranch through the "legacy" branch
// below rather than CreateBranchAtMessage. Before this WP's fix, that
// branch never called audit.MustEmit at all — so the common case produced
// zero audit trail while only the rare explicit-parent-message path was
// recorded. Run against the pre-fix code, this test fails with
// "no branch.created event reached the configured audit emitter; got []",
// which is the exact failure this test is designed to catch.
func TestAPI_CreateBranch_LegacyPathEmitsAudit(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	em := &fakeAuditEmitter{}
	api.cfg.Audit = em
	ctx := context.Background()
	parent, err := sessMgr.Create(ctx, "trunk")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	br, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID,
		Title:           "side question",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	var found *audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindBranchCreated {
			ev := e
			found = &ev
			break
		}
	}
	if found == nil {
		t.Fatalf("no branch.created event reached the configured audit emitter; got %v", em.snapshot())
	}
	var payload audit.BranchCreatedPayload
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("unmarshal BranchCreatedPayload: %v", err)
	}
	if payload.ParentSessionID != parent.ID {
		t.Errorf("payload.ParentSessionID = %q, want %q", payload.ParentSessionID, parent.ID)
	}
	if payload.BranchSessionID != br.ChildSessionID {
		t.Errorf("payload.BranchSessionID = %q, want %q", payload.BranchSessionID, br.ChildSessionID)
	}
	if payload.ParentMessageID != "" {
		t.Errorf("payload.ParentMessageID = %q, want empty (legacy path has no anchor message)", payload.ParentMessageID)
	}
	// The legacy path never set opts.CreationPath, so the manager's own
	// default ("unknown") is what should reach both storage and the
	// audit payload — see conversation.Manager.CreateBranch.
	if payload.CreationPath != "unknown" {
		t.Errorf("payload.CreationPath = %q, want %q", payload.CreationPath, "unknown")
	}
}

// TestAPI_CreateBranch_LegacyPathThreadsCreationPath verifies that a
// caller-supplied CreationPath (e.g. "edit_resend" from the edit-and-
// resend flow) actually reaches both the persisted branch row and the
// audit event, instead of being silently dropped by the legacy path's
// ForkOptions construction.
func TestAPI_CreateBranch_LegacyPathThreadsCreationPath(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	em := &fakeAuditEmitter{}
	api.cfg.Audit = em
	ctx := context.Background()
	parent, err := sessMgr.Create(ctx, "trunk")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	br, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID,
		Title:           "resend with edits",
		CreationPath:    "edit_resend",
	})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	_ = br

	var found *audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindBranchCreated {
			ev := e
			found = &ev
			break
		}
	}
	if found == nil {
		t.Fatalf("no branch.created event reached the configured audit emitter; got %v", em.snapshot())
	}
	var payload audit.BranchCreatedPayload
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("unmarshal BranchCreatedPayload: %v", err)
	}
	if payload.CreationPath != "edit_resend" {
		t.Errorf("payload.CreationPath = %q, want %q (caller-supplied value must not be dropped)", payload.CreationPath, "edit_resend")
	}
}

// TestAPI_CreateBranch_ExplicitPathStillEmitsAudit pins the existing
// explicit-fork behaviour (unchanged by this WP) so a future regression
// on the shared emit call is caught by the same test file as the legacy
// path fix.
func TestAPI_CreateBranch_ExplicitPathStillEmitsAudit(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t)
	em := &fakeAuditEmitter{}
	api.cfg.Audit = em
	ctx := context.Background()
	parent, err := sessMgr.Create(ctx, "trunk")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	msg, err := sessMgr.AppendMessage(ctx, parent.ID, session.Message{Role: session.RoleUser, Content: "hi"})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	if _, err := api.CreateBranch(ctx, CreateBranchOptions{
		ParentSessionID: parent.ID,
		ParentMessageID: msg.ID,
		Title:           "explicit fork",
	}); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	var found *audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindBranchCreated {
			ev := e
			found = &ev
			break
		}
	}
	if found == nil {
		t.Fatalf("no branch.created event reached the configured audit emitter; got %v", em.snapshot())
	}
	var payload audit.BranchCreatedPayload
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("unmarshal BranchCreatedPayload: %v", err)
	}
	if payload.CreationPath != "explicit" {
		t.Errorf("payload.CreationPath = %q, want %q", payload.CreationPath, "explicit")
	}
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

// ── UNIT-8: AbortSubagent / SteerSubagent (subagent-control-and-
// background-tasks-01PMZB11, AC-09/AC-10-adjacent — Abort/Steer only;
// Pause/Resume are out of scope, gated on unresolved E-002) ─────────────

// fakeSubagentTasks is a race-safe fake SubagentTaskRegistry. Abort
// mirrors core/tasks.Registry.Abort's real contract: aborting an
// already-terminal task returns coretasks.ErrAlreadyTerminal instead of
// silently succeeding twice — that's the behaviour AbortSubagent's
// idempotency handling depends on.
type fakeSubagentTasks struct {
	mu       sync.Mutex
	terminal map[string]bool
	aborts   []string
}

func newFakeSubagentTasks() *fakeSubagentTasks {
	return &fakeSubagentTasks{terminal: map[string]bool{}}
}

func (f *fakeSubagentTasks) Abort(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborts = append(f.aborts, id)
	if f.terminal[id] {
		return coretasks.ErrAlreadyTerminal
	}
	f.terminal[id] = true
	return nil
}

func (f *fakeSubagentTasks) abortCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.aborts))
	copy(out, f.aborts)
	return out
}

// fakeTaskLookup is a race-safe fake SubagentTaskLookup — the test
// double for BranchSeamAdapter.TaskIDForBranch.
type fakeTaskLookup struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeTaskLookup() *fakeTaskLookup {
	return &fakeTaskLookup{m: map[string]string{}}
}

func (f *fakeTaskLookup) set(branchID, taskID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[branchID] = taskID
}

func (f *fakeTaskLookup) TaskIDForBranch(branchID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.m[branchID]
	return id, ok
}

// newSubagentTestStack builds the same real conversation/session stack
// as newTestStack, plus the sub-agent control-verb dependencies
// (Tasks/TaskLookup/Cedar/Audit) AbortSubagent and SteerSubagent need.
// gate is nil by default (default-allow, matching every other gate-hook
// call site's nil-Gate posture); tests that need a real deny install one
// via api.cfg.Cedar after construction.
func newSubagentTestStack(t *testing.T) (api *API, sessMgr *session.Manager, tasks *fakeSubagentTasks, lookup *fakeTaskLookup, em *fakeAuditEmitter) {
	t.Helper()
	sessStore := session.NewMemoryStore()
	sessMgr = session.NewManager(sessStore,
		session.WithClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }),
	)
	convStore := conversation.NewMemoryStore()
	convMgr := conversation.NewManager(convStore, sessMgr,
		conversation.WithClock(func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }),
	)
	tasks = newFakeSubagentTasks()
	lookup = newFakeTaskLookup()
	em = &fakeAuditEmitter{}
	api = New(Config{
		Conversations: convMgr,
		Sessions:      sessMgr,
		Tasks:         tasks,
		TaskLookup:    lookup,
		Audit:         em,
	})
	return api, sessMgr, tasks, lookup, em
}

// forbidSubagentAbortEngine installs a REAL cedar.Engine with a REAL
// forbid rule for ActionToolSubagentAbort scoped to branchID — not
// cedar.AllowAll{}, and not an absent rule that would resolve
// NotApplicable (which enforce() maps to nil / allow — the exact trap
// AC-09 calls out, core/policy/cedar/hooks.go's enforce()). SetPolicyText
// is the engine's documented test seam for installing a policy bundle
// (engine.go: "the test seam ... the engine boots even when every disk
// file is broken" path) — a real engine evaluating a real installed
// policy, exactly as AC-09 requires.
func forbidSubagentAbortEngine(t *testing.T, branchID string) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	src := fmt.Sprintf(`forbid (
    principal == User::"local",
    action == Action::"tool.subagent.abort",
    resource == SubagentBranch::"%s"
);`, branchID)
	if err := e.SetPolicyText("deny_abort.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

// forbidSubagentSteerEngine is forbidSubagentAbortEngine's steer-action
// mirror.
func forbidSubagentSteerEngine(t *testing.T, branchID string) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	src := fmt.Sprintf(`forbid (
    principal == User::"local",
    action == Action::"tool.subagent.steer",
    resource == SubagentBranch::"%s"
);`, branchID)
	if err := e.SetPolicyText("deny_steer.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

// TestAPI_AbortSubagent_AllowedByDefault_StopsTaskAndAuditsOnce is
// AC-09's positive half for Abort: against a live sub-agent (a tracked,
// not-yet-terminal task), the call produces its observable effect
// (Tasks.Abort is actually invoked with the resolved task id) and
// writes exactly one audit record.
func TestAPI_AbortSubagent_AllowedByDefault_StopsTaskAndAuditsOnce(t *testing.T) {
	t.Parallel()
	api, sessMgr, tasks, lookup, em := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	lookup.set(br.ID, "task-1")

	if err := api.AbortSubagent(ctx, br.ID); err != nil {
		t.Fatalf("AbortSubagent: %v", err)
	}
	if got := tasks.abortCalls(); len(got) != 1 || got[0] != "task-1" {
		t.Fatalf("abort calls = %v, want [task-1]", got)
	}

	var found []audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentAborted {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("KindSubagentAborted count = %d, want 1 (got events: %v)", len(found), em.snapshot())
	}
	var payload audit.SubagentAbortedPayload
	if err := json.Unmarshal(found[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal SubagentAbortedPayload: %v", err)
	}
	if payload.BranchID != br.ID || payload.TaskID != "task-1" {
		t.Errorf("payload = %+v, want BranchID=%q TaskID=task-1", payload, br.ID)
	}
}

// TestAPI_AbortSubagent_DeniedByRealCedarPolicy is AC-09's negative
// half for Abort. Fails (as intended) if the cedar.GateSubagentAbort
// call is removed from AbortSubagent — the deny case starts passing,
// exactly the mutation AC-09 names. Manually verified 2026-09-10: with
// the gate call commented out, this test goes red with "AbortSubagent:
// got <nil>, want ErrCedarDenied" and the abort call count assertion
// also fails (the fake registry records a call that should never have
// happened); reverting the comment-out restores green.
func TestAPI_AbortSubagent_DeniedByRealCedarPolicy(t *testing.T) {
	t.Parallel()
	api, sessMgr, tasks, lookup, _ := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	lookup.set(br.ID, "task-1")
	api.cfg.Cedar = forbidSubagentAbortEngine(t, br.ID)

	err = api.AbortSubagent(ctx, br.ID)
	if !errors.Is(err, ErrCedarDenied) {
		t.Fatalf("AbortSubagent: got %v, want ErrCedarDenied", err)
	}
	if got := tasks.abortCalls(); len(got) != 0 {
		t.Errorf("abort calls = %v, want none — the gate must short-circuit before Tasks.Abort", got)
	}
}

// TestAPI_AbortSubagent_Idempotent_OneAuditRecordNotTwo pins the spec's
// explicit idempotency requirement: "Abort on an already-terminal
// sub-agent is idempotent and writes one audit record, not two."
func TestAPI_AbortSubagent_Idempotent_OneAuditRecordNotTwo(t *testing.T) {
	t.Parallel()
	api, sessMgr, tasks, lookup, em := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	lookup.set(br.ID, "task-1")

	if err := api.AbortSubagent(ctx, br.ID); err != nil {
		t.Fatalf("first AbortSubagent: %v", err)
	}
	// Second call: the fake registry now reports task-1 as terminal
	// (mirrors coretasks.Registry.Abort's real ErrAlreadyTerminal
	// behaviour). AbortSubagent must treat this as a successful no-op,
	// not an error.
	if err := api.AbortSubagent(ctx, br.ID); err != nil {
		t.Fatalf("second (idempotent) AbortSubagent: got error %v, want nil", err)
	}
	if got := tasks.abortCalls(); len(got) != 2 {
		t.Fatalf("abort calls = %v, want 2 (both calls should reach Tasks.Abort; the SECOND one is what proves idempotency, not a skipped call)", got)
	}

	var found []audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentAborted {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("KindSubagentAborted count = %d after 2 Abort calls, want exactly 1", len(found))
	}
}

// TestAPI_AbortSubagent_NoTrackedTask covers a branch that was never a
// spawner-backed dispatch (or whose mapping was already evicted by
// WaitForChildRun) — AbortSubagent must refuse cleanly, not panic or
// silently no-op.
func TestAPI_AbortSubagent_NoTrackedTask(t *testing.T) {
	t.Parallel()
	api, sessMgr, _, _, _ := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	// lookup.set is never called for br.ID.
	if err := api.AbortSubagent(ctx, br.ID); !errors.Is(err, ErrSubagentTaskNotFound) {
		t.Errorf("got %v, want ErrSubagentTaskNotFound", err)
	}
}

// TestAPI_AbortSubagent_TasksUnavailable covers the degraded-boot case
// (Config.Tasks / Config.TaskLookup unset).
func TestAPI_AbortSubagent_TasksUnavailable(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t) // no Tasks/TaskLookup wired
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := api.AbortSubagent(ctx, br.ID); !errors.Is(err, ErrSubagentUnavailable) {
		t.Errorf("got %v, want ErrSubagentUnavailable", err)
	}
}

// TestAPI_SteerSubagent_AppendsToChildSession_AndAuditsOnce is AC-09's
// positive half for Steer.
func TestAPI_SteerSubagent_AppendsToChildSession_AndAuditsOnce(t *testing.T) {
	t.Parallel()
	api, sessMgr, _, _, em := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	before, _ := sessMgr.ListMessages(ctx, br.ChildSessionID)

	if err := api.SteerSubagent(ctx, br.ID, "also check the retry path"); err != nil {
		t.Fatalf("SteerSubagent: %v", err)
	}

	after, err := sessMgr.ListMessages(ctx, br.ChildSessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("child message count = %d, want %d", len(after), len(before)+1)
	}
	last := after[len(after)-1]
	if last.Role != session.RoleUser || last.Content != "also check the retry path" {
		t.Errorf("appended message = %+v, want role=user content=%q", last, "also check the retry path")
	}

	var found []audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentSteered {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("KindSubagentSteered count = %d, want 1", len(found))
	}
	var payload audit.SubagentSteeredPayload
	if err := json.Unmarshal(found[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal SubagentSteeredPayload: %v", err)
	}
	if payload.BranchID != br.ID {
		t.Errorf("payload.BranchID = %q, want %q", payload.BranchID, br.ID)
	}
	if payload.MessageLength != utf8.RuneCountInString("also check the retry path") {
		t.Errorf("payload.MessageLength = %d, want %d", payload.MessageLength, utf8.RuneCountInString("also check the retry path"))
	}
}

// TestAPI_SteerSubagent_DeniedByRealCedarPolicy is AC-09's negative
// half for Steer. Fails if cedar.GateSubagentSteer is removed from
// SteerSubagent.
func TestAPI_SteerSubagent_DeniedByRealCedarPolicy(t *testing.T) {
	t.Parallel()
	api, sessMgr, _, _, _ := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	before, _ := sessMgr.ListMessages(ctx, br.ChildSessionID)
	api.cfg.Cedar = forbidSubagentSteerEngine(t, br.ID)

	err = api.SteerSubagent(ctx, br.ID, "keep going")
	if !errors.Is(err, ErrCedarDenied) {
		t.Fatalf("SteerSubagent: got %v, want ErrCedarDenied", err)
	}
	after, _ := sessMgr.ListMessages(ctx, br.ChildSessionID)
	if len(after) != len(before) {
		t.Errorf("child message count changed under a denying gate: before=%d after=%d", len(before), len(after))
	}
}

// TestAPI_SteerSubagent_InvalidArgs covers empty branchID and
// empty/whitespace-only message.
func TestAPI_SteerSubagent_InvalidArgs(t *testing.T) {
	t.Parallel()
	api, sessMgr, _, _, _ := newSubagentTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, _ := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})

	if err := api.SteerSubagent(ctx, "", "hi"); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("empty branchID: got %v, want ErrInvalidArg", err)
	}
	if err := api.SteerSubagent(ctx, br.ID, "   "); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("whitespace-only message: got %v, want ErrInvalidArg", err)
	}
}
