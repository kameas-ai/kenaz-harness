package rpc

// v0.86.0 unwired sweep (2026-10-04): session_start and subagent_start
// additional_context used to be discarded at both fire sites. These tests
// drive a REAL hooks.Runner + Registry and a REAL sqlite-backed
// attachments.Manager (CLAUDE.md blind spot #2: no in-memory fixture) and
// read the result back the way the chat path does — ListResolved, the
// source of LLMProviderAdapter.buildAttachmentsBlock.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreatt "github.com/kameas-ai/kenaz-harness/core/attachments"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
)

// contextHookRunner returns a real Runner whose one enabled builtin hook
// on event returns additional_context text.
func contextHookRunner(t *testing.T, event, text string) *hooks.Runner {
	t.Helper()
	reg, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("test.context",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{AdditionalContext: text}, nil
		},
		hooks.BuiltinDescriptor{ID: "test.context", Name: "context", Events: []string{event}})
	if err := reg.Add(hooks.Hook{
		ID: "h", Name: "n", Event: event, Kind: hooks.KindBuiltin,
		Enabled: true, Builtin: "test.context",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	return hooks.NewRunner(hooks.Config{Registry: reg, Builtins: builtins})
}

func systemAttachmentContents(t *testing.T, mgr *coreatt.Manager, sessionID string) []coreatt.Attachment {
	t.Helper()
	rows, err := mgr.ListResolved(context.Background(), nil, sessionID)
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	var out []coreatt.Attachment
	for _, r := range rows {
		if r.Kind == coreatt.KindSystem {
			out = append(out, r)
		}
	}
	return out
}

func TestSessionStartAdditionalContext_AttachedToNewSession(t *testing.T) {
	const text = "session_start says: this project uses Go 1.25"
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	mgr := newAttachmentsManager(c, newMediaStore(c))
	if mgr == nil {
		t.Fatal("newAttachmentsManager returned nil over a real Core")
	}
	// Mirrors api.go's New(): decorator installed before the first
	// SessionManager() call.
	c.SetSessionHookRunner(&sessionStartContextRunner{
		SessionHookRunner: &hooks.SessionRunnerAdapter{Runner: contextHookRunner(t, hooks.EventSessionStart, text)},
		attach:            newHookContextAttacher(mgr),
	})

	rec, err := c.SessionManager().Create(context.Background(), "s")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	atts := systemAttachmentContents(t, mgr, rec.ID)
	if len(atts) != 1 || atts[0].Content != text {
		t.Fatalf("system attachments on the new session = %+v, want exactly the hook's additional_context", atts)
	}
	if atts[0].Position == 0 {
		t.Fatalf("hook context took position 0, the slot Sessions_SetSystemPrompt owns and deletes")
	}

	// A later SetSystemPrompt (NewSessionDialog's flow) must not wipe it.
	sessAPI := sessionsview.NewManagerAPIWithAttachmentsAndArtifacts(c.SessionManager(), mgr, nil, nil, c.DataDir())
	if err := sessAPI.SetSystemPrompt(context.Background(), rec.ID, "user starting context", "system"); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}
	var found bool
	for _, a := range systemAttachmentContents(t, mgr, rec.ID) {
		if a.Content == text {
			found = true
		}
	}
	if !found {
		t.Fatal("SetSystemPrompt removed the session_start hook context")
	}
}

func TestSubagentStartAdditionalContext_AttachedToChildBeforeFirstTurn(t *testing.T) {
	const text = "subagent_start says: report findings as a bullet list"
	stack := buildSubagentSpawnerTestStack(t, "sub-agent worker done")
	mgr := newAttachmentsManager(stack.core, newMediaStore(stack.core))
	if mgr == nil {
		t.Fatal("newAttachmentsManager returned nil over a real Core")
	}
	attach := newHookContextAttacher(mgr)

	var (
		mu          sync.Mutex
		attachedTo  []string
		modelBefore int
	)
	recording := func(ctx context.Context, sessionID, s string) error {
		mu.Lock()
		attachedTo = append(attachedTo, sessionID)
		modelBefore = len(stack.model.snapshotRequests())
		mu.Unlock()
		return attach(ctx, sessionID, s)
	}
	stack.seam.SetRunSpawner(NewSubagentRunSpawner(SubagentRunSpawnerDeps{
		LLM:               stack.llmAPI,
		Bus:               stack.bus,
		Tasks:             stack.tasks,
		DefaultProfile:    func() string { return "test-profile" },
		Timeout:           5 * time.Second,
		HookRunner:        contextHookRunner(t, hooks.EventSubagentStart, text),
		AttachHookContext: recording,
	}))

	parent, err := stack.sessionsAPI.Create(context.Background(), "parent session")
	if err != nil {
		t.Fatalf("create parent session: %v", err)
	}
	tool := coresubagent.New(coresubagent.Options{DataDir: t.TempDir(), Seam: stack.seam})
	ctx := toolloop.WithSessionID(context.Background(), parent.ID)
	raw, err := tool.Call(ctx, json.RawMessage(`{"profile":"explore","prompt":"find all usages","run_in_background":false}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !strings.Contains(string(raw), "complete") {
		t.Fatalf("sub-agent run did not complete: %s", raw)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(attachedTo) != 1 {
		t.Fatalf("attacher called %d times, want 1", len(attachedTo))
	}
	child := attachedTo[0]
	if child == "" || child == parent.ID {
		t.Fatalf("context attached to %q, want the CHILD session (parent is %q)", child, parent.ID)
	}
	if modelBefore != 0 {
		t.Fatalf("model had been called %d time(s) before the context was attached — it must land before the child's first turn", modelBefore)
	}
	atts := systemAttachmentContents(t, mgr, child)
	if len(atts) != 1 || atts[0].Content != text {
		t.Fatalf("child system attachments = %+v, want the hook's additional_context", atts)
	}
	if got := systemAttachmentContents(t, mgr, parent.ID); len(got) != 0 {
		t.Fatalf("parent session got attachments %+v; the context belongs to the child", got)
	}
}
