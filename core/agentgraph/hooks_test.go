package agentgraph

import (
	"context"
	"strings"
	"testing"
)

func TestHookManager_FireWritesToMemory(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "session-1", "project-1")
	b := h.Fire(context.Background(), HookPostLLM, "session", "ttl", "hello world", "src")
	if b.Len() != 1 {
		t.Errorf("expected one event, got %d", b.Len())
	}
	if mem.writeCount() != 1 {
		t.Errorf("expected 1 store write, got %d", mem.writeCount())
	}
	rec := h.Journal()
	if len(rec) != 1 || !rec[0].Written {
		t.Errorf("journal: %+v", rec)
	}
}

func TestHookManager_DedupSameContent(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "s", "p")
	for i := 0; i < 3; i++ {
		h.Fire(context.Background(), HookPostLLM, "session", "x", "duplicate text", "src")
	}
	if mem.writeCount() != 1 {
		t.Errorf("expected dedup; writes=%d", mem.writeCount())
	}
	if got := len(h.Journal()); got != 3 {
		t.Errorf("journal len = %d, want 3", got)
	}
}

func TestHookManager_DisabledSkips(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "s", "p")
	h.SetEnabled(false)
	h.Fire(context.Background(), HookPostLLM, "session", "t", "content", "src")
	if mem.writeCount() != 0 {
		t.Errorf("disabled hook still wrote (writes=%d)", mem.writeCount())
	}
	if !h.Journal()[0].Skipped {
		t.Error("expected journal Skipped=true")
	}
}

type upperRedactor struct{}

func (upperRedactor) Redact(s string) string { return strings.ToUpper(s) }

func TestHookManager_RedactorAppliedBeforeStore(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "s", "p")
	h.SetRedactor(upperRedactor{})
	h.Fire(context.Background(), HookPostLLM, "session", "t", "secret", "src")
	if mem.writes[0].Content != "SECRET" {
		t.Errorf("redactor not applied: %q", mem.writes[0].Content)
	}
}

func TestHookBoundaries_Coverage(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "s", "p")
	for _, b := range AllHookBoundaries() {
		h.Fire(context.Background(), b, "session", "title", "content "+string(b), "src")
	}
	if mem.writeCount() != len(AllHookBoundaries()) {
		t.Errorf("expected %d writes, got %d", len(AllHookBoundaries()), mem.writeCount())
	}
}

func TestHookManager_GlobalScopeIDIsEmpty(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "s", "p")
	h.Fire(context.Background(), HookOnExplicitPin, "global", "t", "content global", "src")
	w := mem.writes[0]
	if w.Scope != "global" {
		t.Errorf("scope = %q", w.Scope)
	}
	if w.ScopeID != "" {
		t.Errorf("global scope_id should be empty, got %q", w.ScopeID)
	}
}

func TestHookManager_ProjectScopeUsesProjectID(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	h := NewHookManager(mem, "s", "p-99")
	h.Fire(context.Background(), HookPostLLM, "project", "t", "content proj", "src")
	if mem.writes[0].ScopeID != "p-99" {
		t.Errorf("project scope_id = %q", mem.writes[0].ScopeID)
	}
}
