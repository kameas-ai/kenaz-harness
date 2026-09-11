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

// ── UNIT-8: PauseSubagent / ResumeSubagent (subagent-control-and-
// background-tasks-01PMZB11, AC-09/AC-10 — owner ruling E-002 resolved
// 2026-09-10, "stop after the current turn". Abort/Steer are PR #331's
// scope; this branch adds Pause/Resume independently off the same
// origin/main base, per CLAUDE.md's shared-file conflict-zone note) ──

// fakeSubagentPauseControl is a race-safe fake SubagentPauseControl.
// Mirrors chat.SubagentPauseRegistry's changed-bool contract exactly
// (Pause/Resume report whether the call actually changed state) so
// these tests pin the SAME idempotency behaviour the real registry
// provides, without pulling in the full chat package.
type fakeSubagentPauseControl struct {
	mu      sync.Mutex
	paused  map[string]bool
	pauses  []string
	resumes []string
}

func newFakeSubagentPauseControl() *fakeSubagentPauseControl {
	return &fakeSubagentPauseControl{paused: map[string]bool{}}
}

func (f *fakeSubagentPauseControl) Pause(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauses = append(f.pauses, sessionID)
	if f.paused[sessionID] {
		return false
	}
	f.paused[sessionID] = true
	return true
}

func (f *fakeSubagentPauseControl) Resume(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes = append(f.resumes, sessionID)
	if !f.paused[sessionID] {
		return false
	}
	f.paused[sessionID] = false
	return true
}

func (f *fakeSubagentPauseControl) isPaused(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused[sessionID]
}

func (f *fakeSubagentPauseControl) pauseCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.pauses))
	copy(out, f.pauses)
	return out
}

func (f *fakeSubagentPauseControl) resumeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.resumes))
	copy(out, f.resumes)
	return out
}

// newSubagentPauseTestStack builds the same real conversation/session
// stack as newTestStack, plus the Pause/Resume dependencies
// (PauseControl/Cedar/Audit). gate is nil by default (default-allow);
// tests that need a real deny install one via api.cfg.Cedar after
// construction — same pattern PR #331's newSubagentTestStack uses for
// Abort/Steer.
func newSubagentPauseTestStack(t *testing.T) (api *API, sessMgr *session.Manager, pc *fakeSubagentPauseControl, em *fakeAuditEmitter) {
	t.Helper()
	sessStore := session.NewMemoryStore()
	sessMgr = session.NewManager(sessStore,
		session.WithClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }),
	)
	convStore := conversation.NewMemoryStore()
	convMgr := conversation.NewManager(convStore, sessMgr,
		conversation.WithClock(func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }),
	)
	pc = newFakeSubagentPauseControl()
	em = &fakeAuditEmitter{}
	api = New(Config{
		Conversations: convMgr,
		Sessions:      sessMgr,
		PauseControl:  pc,
		Audit:         em,
	})
	return api, sessMgr, pc, em
}

// forbidSubagentPauseEngine installs a REAL cedar.Engine with a REAL
// forbid rule for ActionToolSubagentPause scoped to branchID — not
// cedar.AllowAll{}, and not an absent rule that would resolve
// NotApplicable (which enforce() maps to nil / allow — the exact trap
// AC-09 calls out, core/policy/cedar/hooks.go's enforce()).
func forbidSubagentPauseEngine(t *testing.T, branchID string) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	src := fmt.Sprintf(`forbid (
    principal == User::"local",
    action == Action::"tool.subagent.pause",
    resource == SubagentBranch::"%s"
);`, branchID)
	if err := e.SetPolicyText("deny_pause.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

// forbidSubagentResumeEngine is forbidSubagentPauseEngine's resume-action
// mirror.
func forbidSubagentResumeEngine(t *testing.T, branchID string) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	src := fmt.Sprintf(`forbid (
    principal == User::"local",
    action == Action::"tool.subagent.resume",
    resource == SubagentBranch::"%s"
);`, branchID)
	if err := e.SetPolicyText("deny_resume.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

// TestAPI_PauseSubagent_AllowedByDefault_ArmsSignalAndAuditsOnce is
// AC-09's positive half for Pause: against a live sub-agent branch, the
// call produces its observable effect (PauseControl.Pause is actually
// invoked with the branch's child session id) and writes exactly one
// audit record.
func TestAPI_PauseSubagent_AllowedByDefault_ArmsSignalAndAuditsOnce(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, em := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	if err := api.PauseSubagent(ctx, br.ID); err != nil {
		t.Fatalf("PauseSubagent: %v", err)
	}
	if got := pc.pauseCalls(); len(got) != 1 || got[0] != br.ChildSessionID {
		t.Fatalf("pause calls = %v, want [%s]", got, br.ChildSessionID)
	}
	if !pc.isPaused(br.ChildSessionID) {
		t.Error("child session must be paused after PauseSubagent")
	}

	var found []audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentPaused {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("KindSubagentPaused count = %d, want 1 (got events: %v)", len(found), em.snapshot())
	}
	var payload audit.SubagentPausedPayload
	if err := json.Unmarshal(found[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal SubagentPausedPayload: %v", err)
	}
	if payload.BranchID != br.ID {
		t.Errorf("payload.BranchID = %q, want %q", payload.BranchID, br.ID)
	}
}

// TestAPI_PauseSubagent_DeniedByRealCedarPolicy is AC-09's negative
// half for Pause. Fails (as intended) if the cedar.GateSubagentPause
// call is removed from PauseSubagent — the deny case starts passing,
// exactly the mutation AC-09 names.
func TestAPI_PauseSubagent_DeniedByRealCedarPolicy(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, _ := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	api.cfg.Cedar = forbidSubagentPauseEngine(t, br.ID)

	err = api.PauseSubagent(ctx, br.ID)
	if !errors.Is(err, ErrCedarDenied) {
		t.Fatalf("PauseSubagent: got %v, want ErrCedarDenied", err)
	}
	if got := pc.pauseCalls(); len(got) != 0 {
		t.Errorf("pause calls = %v, want none — the gate must short-circuit before PauseControl.Pause", got)
	}
}

// TestAPI_PauseSubagent_Idempotent_OneAuditRecordNotTwo pins the
// mirrored idempotency contract: a second Pause while already paused
// writes one audit record, not two (AC-09).
func TestAPI_PauseSubagent_Idempotent_OneAuditRecordNotTwo(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, em := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	if err := api.PauseSubagent(ctx, br.ID); err != nil {
		t.Fatalf("first PauseSubagent: %v", err)
	}
	if err := api.PauseSubagent(ctx, br.ID); err != nil {
		t.Fatalf("second (idempotent) PauseSubagent: got error %v, want nil", err)
	}
	if got := pc.pauseCalls(); len(got) != 2 {
		t.Fatalf("pause calls = %v, want 2 (both calls should reach PauseControl.Pause; the SECOND is what proves idempotency, not a skipped call)", got)
	}

	var found []audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentPaused {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("KindSubagentPaused count = %d after 2 Pause calls, want exactly 1", len(found))
	}
}

// TestAPI_PauseSubagent_PauseControlUnavailable covers the
// degraded-boot case (Config.PauseControl unset).
func TestAPI_PauseSubagent_PauseControlUnavailable(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t) // no PauseControl wired
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := api.PauseSubagent(ctx, br.ID); !errors.Is(err, ErrSubagentPauseUnavailable) {
		t.Errorf("got %v, want ErrSubagentPauseUnavailable", err)
	}
}

// TestAPI_PauseSubagent_TerminalOrNeverDispatchedBranch defines the
// behaviour the mission brief asks for explicitly: "Pause on an
// already-terminal one" (mirroring Abort's idempotency posture from
// PR #331). Unlike Abort, Pause does not consult the task registry at
// all — it only arms a session-keyed signal the run loop may or may
// not ever consult again. Pausing a branch whose sub-agent already
// finished (or was never dispatched) is therefore well-defined and
// harmless: it succeeds and audits once, exactly like pausing a live
// one, because from this package's perspective nothing distinguishes
// the two cases (no TaskLookup dependency is wired for Pause/Resume).
func TestAPI_PauseSubagent_TerminalOrNeverDispatchedBranch(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, em := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	// No task registry / dispatch involved at all — this package never
	// learns whether a sub-agent run ever started or already finished.
	if err := api.PauseSubagent(ctx, br.ID); err != nil {
		t.Fatalf("PauseSubagent on a never-dispatched branch: got %v, want nil", err)
	}
	if !pc.isPaused(br.ChildSessionID) {
		t.Error("the pause signal must still be armed even though nothing may ever consult it")
	}
	var found int
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentPaused {
			found++
		}
	}
	if found != 1 {
		t.Errorf("KindSubagentPaused count = %d, want 1", found)
	}
}

// TestAPI_ResumeSubagent_AllowedByDefault_ClearsSignalAndAuditsOnce is
// AC-09's positive half for Resume.
func TestAPI_ResumeSubagent_AllowedByDefault_ClearsSignalAndAuditsOnce(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, em := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := api.PauseSubagent(ctx, br.ID); err != nil {
		t.Fatalf("PauseSubagent: %v", err)
	}

	if err := api.ResumeSubagent(ctx, br.ID); err != nil {
		t.Fatalf("ResumeSubagent: %v", err)
	}
	if got := pc.resumeCalls(); len(got) != 1 || got[0] != br.ChildSessionID {
		t.Fatalf("resume calls = %v, want [%s]", got, br.ChildSessionID)
	}
	if pc.isPaused(br.ChildSessionID) {
		t.Error("child session must not be paused after ResumeSubagent")
	}

	var found []audit.Event
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentResumed {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("KindSubagentResumed count = %d, want 1 (got events: %v)", len(found), em.snapshot())
	}
	var payload audit.SubagentResumedPayload
	if err := json.Unmarshal(found[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal SubagentResumedPayload: %v", err)
	}
	if payload.BranchID != br.ID {
		t.Errorf("payload.BranchID = %q, want %q", payload.BranchID, br.ID)
	}
}

// TestAPI_ResumeSubagent_DeniedByRealCedarPolicy is AC-09's negative
// half for Resume. Fails if cedar.GateSubagentResume is removed from
// ResumeSubagent.
func TestAPI_ResumeSubagent_DeniedByRealCedarPolicy(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, _ := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := api.PauseSubagent(ctx, br.ID); err != nil {
		t.Fatalf("PauseSubagent: %v", err)
	}
	api.cfg.Cedar = forbidSubagentResumeEngine(t, br.ID)

	err = api.ResumeSubagent(ctx, br.ID)
	if !errors.Is(err, ErrCedarDenied) {
		t.Fatalf("ResumeSubagent: got %v, want ErrCedarDenied", err)
	}
	if !pc.isPaused(br.ChildSessionID) {
		t.Error("child session must remain paused under a denying gate")
	}
}

// TestAPI_ResumeSubagent_NotPaused_Idempotent defines and tests the
// second explicitly-requested behaviour: "Resume on a non-paused
// sub-agent" — a no-op that writes no audit record, mirroring Abort's
// idempotency posture (the state never changed, so there is nothing
// new to attest to).
func TestAPI_ResumeSubagent_NotPaused_Idempotent(t *testing.T) {
	t.Parallel()
	api, sessMgr, pc, em := newSubagentPauseTestStack(t)
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	// Never paused.
	if err := api.ResumeSubagent(ctx, br.ID); err != nil {
		t.Fatalf("ResumeSubagent on a never-paused branch: got %v, want nil", err)
	}
	if got := pc.resumeCalls(); len(got) != 1 {
		t.Fatalf("resume calls = %v, want 1 (the call must still reach PauseControl.Resume)", got)
	}
	var found int
	for _, e := range em.snapshot() {
		if e.Kind == audit.KindSubagentResumed {
			found++
		}
	}
	if found != 0 {
		t.Errorf("KindSubagentResumed count = %d, want 0 (nothing changed, nothing to audit)", found)
	}
}

// TestAPI_ResumeSubagent_PauseControlUnavailable covers the
// degraded-boot case (Config.PauseControl unset).
func TestAPI_ResumeSubagent_PauseControlUnavailable(t *testing.T) {
	t.Parallel()
	api, sessMgr, _ := newTestStack(t) // no PauseControl wired
	ctx := context.Background()
	parent, _ := sessMgr.Create(ctx, "trunk")
	br, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: parent.ID, Title: "worker"})
	if err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := api.ResumeSubagent(ctx, br.ID); !errors.Is(err, ErrSubagentPauseUnavailable) {
		t.Errorf("got %v, want ErrSubagentPauseUnavailable", err)
	}
}

// TestAPI_PauseSubagent_InvalidArgs covers empty branchID.
func TestAPI_PauseSubagent_InvalidArgs(t *testing.T) {
	t.Parallel()
	api, _, _, _ := newSubagentPauseTestStack(t)
	ctx := context.Background()
	if err := api.PauseSubagent(ctx, ""); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("PauseSubagent(\"\"): got %v, want ErrInvalidArg", err)
	}
}

// TestAPI_ResumeSubagent_InvalidArgs covers empty branchID.
func TestAPI_ResumeSubagent_InvalidArgs(t *testing.T) {
	t.Parallel()
	api, _, _, _ := newSubagentPauseTestStack(t)
	ctx := context.Background()
	if err := api.ResumeSubagent(ctx, ""); !errors.Is(err, ErrInvalidArg) {
		t.Errorf("ResumeSubagent(\"\"): got %v, want ErrInvalidArg", err)
	}
}
