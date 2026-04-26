package llm

import (
	"context"
	"strings"
	"testing"

	coreatt "github.com/sigil-tech/kaneaz-harness/core/attachments"
	coreprojects "github.com/sigil-tech/kaneaz-harness/core/projects"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// TestIntegration_ResolvedSystemPromptOrdering wires the real (in-memory)
// project / session / attachment managers behind the LLM API and asserts
// that buildMessages emits the resolved [global..., project...,
// session..., history] order on the wire (acceptance A8).
//
// The fixture seeds two attachments at each of the three scopes —
// global, project, session — so the test exercises both intra-scope
// ordering (Position) and inter-scope ordering (resolution sequence).
//
// This is the WP07 T003 closing acceptance test for the context-library
// mission's "resolution panel order" requirement; it complements the
// per-package unit tests in core/attachments and core/rpc/views/llm.
func TestIntegration_ResolvedSystemPromptOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── managers ────────────────────────────────────────────────────────
	pm := coreprojects.NewManager(coreprojects.NewMemoryStore())
	sm := session.NewManager(session.NewMemoryStore())
	am := coreatt.NewManager(coreatt.NewMemoryStore())

	// ── seed entities ───────────────────────────────────────────────────
	proj, err := pm.Create(ctx, "alpha", "alpha project")
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	sess, err := sm.Create(ctx, "session-1")
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	if err := sm.MoveToProject(ctx, sess.ID, &proj.ID); err != nil {
		t.Fatalf("session move-to-project: %v", err)
	}
	if _, err := sm.AppendMessage(ctx, sess.ID, session.Message{
		Role:    session.RoleUser,
		Content: "ping",
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	// ── seed attachments at all three scopes ────────────────────────────
	type attSpec struct {
		scopeKind string
		scopeID   string
		body      string
		pos       int
	}
	specs := []attSpec{
		{coreatt.ScopeKindGlobal, "", "global1", 0},
		{coreatt.ScopeKindGlobal, "", "global2", 1},
		{coreatt.ScopeKindProject, proj.ID, "project1", 0},
		{coreatt.ScopeKindProject, proj.ID, "project2", 1},
		{coreatt.ScopeKindSession, sess.ID, "session1", 0},
		{coreatt.ScopeKindSession, sess.ID, "session2", 1},
	}
	for _, s := range specs {
		if _, err := am.Add(ctx, coreatt.Attachment{
			ScopeKind:     s.scopeKind,
			ScopeID:       s.scopeID,
			ContentSource: "inline:fixture-" + s.scopeKind + "-" + s.body,
			Content:       s.body,
			Kind:          coreatt.KindSystem,
			Position:      s.pos,
		}); err != nil {
			t.Fatalf("attachment add (%s/%s): %v", s.scopeKind, s.body, err)
		}
	}

	// ── adapters that bridge core types into the llm view's interfaces ──
	historyAdapter := &integrationHistory{mgr: sm}
	resolverAdapter := &integrationResolver{mgr: am, sessions: sm}

	api := New(Config{
		History:     historyAdapter,
		Attachments: resolverAdapter,
	})

	// buildMessages is the boundary the rpc / Wails surface invokes from
	// StartStream. It is the same code path the production LLM connector
	// receives, so asserting on its output is equivalent to asserting on
	// the on-the-wire GenerationRequest.Messages payload.
	msgs, err := api.buildMessages(ctx, sess.ID, "model-x", "anthropic")
	if err != nil {
		t.Fatalf("buildMessages: %v", err)
	}

	// Expected order: 6 system attachments + 1 user history turn.
	want := []struct {
		role string
		text string
	}{
		{"system", "global1"},
		{"system", "global2"},
		{"system", "project1"},
		{"system", "project2"},
		{"system", "session1"},
		{"system", "session2"},
		{"user", "ping"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("len(msgs)=%d, want %d (%+v)", len(msgs), len(want), msgs)
	}
	for i, w := range want {
		if string(msgs[i].Role) != w.role {
			t.Errorf("msgs[%d].Role = %q, want %q", i, msgs[i].Role, w.role)
		}
		if len(msgs[i].Content) == 0 {
			t.Fatalf("msgs[%d] has no content parts", i)
		}
		if got := msgs[i].Content[0].Text; got != w.text {
			t.Errorf("msgs[%d].Text = %q, want %q", i, got, w.text)
		}
	}
}

// TestIntegration_LooseSession_OmitsProjectScope verifies that a session
// without a project sees only [global..., session..., history] — the
// project layer is dropped.
func TestIntegration_LooseSession_OmitsProjectScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pm := coreprojects.NewManager(coreprojects.NewMemoryStore())
	sm := session.NewManager(session.NewMemoryStore())
	am := coreatt.NewManager(coreatt.NewMemoryStore())

	proj, _ := pm.Create(ctx, "isolated", "")
	sess, _ := sm.Create(ctx, "loose")
	if _, err := sm.AppendMessage(ctx, sess.ID, session.Message{
		Role:    session.RoleUser,
		Content: "hi",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// project attachment should NOT appear because the session is loose.
	_, _ = am.Add(ctx, coreatt.Attachment{
		ScopeKind:     coreatt.ScopeKindGlobal,
		ContentSource: "inline:g",
		Content:       "G",
		Kind:          coreatt.KindSystem,
	})
	_, _ = am.Add(ctx, coreatt.Attachment{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       proj.ID,
		ContentSource: "inline:p",
		Content:       "P",
		Kind:          coreatt.KindSystem,
	})
	_, _ = am.Add(ctx, coreatt.Attachment{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       sess.ID,
		ContentSource: "inline:s",
		Content:       "S",
		Kind:          coreatt.KindSystem,
	})

	api := New(Config{
		History:     &integrationHistory{mgr: sm},
		Attachments: &integrationResolver{mgr: am, sessions: sm},
	})
	msgs, err := api.buildMessages(ctx, sess.ID, "", "")
	if err != nil {
		t.Fatalf("buildMessages: %v", err)
	}
	// [global, session, history-user] — 3 entries, no project layer.
	if len(msgs) != 3 {
		t.Fatalf("len=%d msgs=%+v", len(msgs), msgs)
	}
	if got := msgs[0].Content[0].Text; got != "G" {
		t.Errorf("msgs[0]=%q want G", got)
	}
	if got := msgs[1].Content[0].Text; got != "S" {
		t.Errorf("msgs[1]=%q want S", got)
	}
	if string(msgs[2].Role) != "user" {
		t.Errorf("msgs[2].Role=%q want user", msgs[2].Role)
	}
	// Defensive: project content must not leak into the message stream.
	for _, m := range msgs {
		if len(m.Content) > 0 && strings.Contains(m.Content[0].Text, "P") {
			t.Errorf("project content leaked into a loose session: %+v", m)
		}
	}
}

// integrationHistory bridges core/session.Manager into the llm view's
// SessionMessageReader.
type integrationHistory struct {
	mgr *session.Manager
}

func (h *integrationHistory) ListMessages(ctx context.Context, sessionID string) ([]SessionMessage, error) {
	rows, err := h.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionMessage{
			Role:    string(r.Role),
			Content: r.Content,
		})
	}
	return out, nil
}

// integrationResolver bridges core/attachments.Manager + the session
// manager (for project lookup) into the llm view's AttachmentsResolver.
type integrationResolver struct {
	mgr      *coreatt.Manager
	sessions *session.Manager
}

func (r *integrationResolver) ListResolved(ctx context.Context, sessionID string) ([]ResolvedAttachment, error) {
	rows, err := r.mgr.ListResolved(ctx, &integrationProjReader{mgr: r.sessions}, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedAttachment, 0, len(rows))
	for _, a := range rows {
		out = append(out, ResolvedAttachment{
			Content: a.Content,
			Kind:    a.Kind,
		})
	}
	return out, nil
}

// integrationProjReader is the SessionProjectReader the attachments
// resolver consumes.
type integrationProjReader struct {
	mgr *session.Manager
}

func (p *integrationProjReader) ProjectID(ctx context.Context, sessionID string) (*string, error) {
	rec, err := p.mgr.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if rec.ProjectID == nil {
		return nil, nil
	}
	v := *rec.ProjectID
	return &v, nil
}

// Compile-time witness: integrationResolver matches AttachmentsResolver,
// integrationHistory matches SessionMessageReader.
var _ AttachmentsResolver = (*integrationResolver)(nil)
var _ SessionMessageReader = (*integrationHistory)(nil)
