// skill_catalog_test.go — tests for the skill-catalog pre_send builtin.
//
// model-invoked-skills-catalog-01KZNP3E WP03.
package hooks

import (
	"context"
	"strings"
	"testing"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// fakeSkillStore implements SkillLister for tests.
type fakeSkillStore struct {
	skills []coreslashcmd.UserCommand
	err    error
}

func (f *fakeSkillStore) ListModelInvokable(_ context.Context) ([]coreslashcmd.UserCommand, error) {
	return f.skills, f.err
}

func TestBuildCatalog_Empty(t *testing.T) {
	t.Parallel()
	got := buildCatalog(nil, MaxCatalogBytes)
	if got != "" {
		t.Errorf("buildCatalog(nil) = %q, want empty", got)
	}
}

func TestBuildCatalog_SingleSkill_ContainsHeader(t *testing.T) {
	t.Parallel()
	skills := []coreslashcmd.UserCommand{
		{Name: "summarize", Description: "Summarize a document.", WhenToUse: "When the user wants a summary."},
	}
	got := buildCatalog(skills, MaxCatalogBytes)
	if !strings.Contains(got, "## Available skills") {
		t.Errorf("catalog missing header: %q", got)
	}
	if !strings.Contains(got, "### /summarize") {
		t.Errorf("catalog missing skill entry: %q", got)
	}
	if !strings.Contains(got, "Summarize a document.") {
		t.Errorf("catalog missing description: %q", got)
	}
	if !strings.Contains(got, "When to use:") {
		t.Errorf("catalog missing when_to_use: %q", got)
	}
}

func TestBuildCatalog_Truncation(t *testing.T) {
	t.Parallel()
	// A tiny budget forces truncation even on one entry.
	skills := []coreslashcmd.UserCommand{
		{Name: "a", Description: strings.Repeat("x", 200)},
		{Name: "b", Description: strings.Repeat("y", 200)},
		{Name: "c", Description: strings.Repeat("z", 200)},
	}
	// Budget so small even first entry overflows.
	got := buildCatalog(skills, 20)
	if !strings.Contains(got, catalogTruncationNote) {
		t.Errorf("expected truncation note in tiny-budget catalog: %q", got)
	}
}

func TestBuildCatalog_DoesNotTruncate_WhenFitsExact(t *testing.T) {
	t.Parallel()
	skills := []coreslashcmd.UserCommand{
		{Name: "s", Description: "short"},
	}
	got := buildCatalog(skills, MaxCatalogBytes)
	if strings.Contains(got, catalogTruncationNote) {
		t.Error("unexpected truncation note for small catalog within budget")
	}
}

func TestFormatSkillEntry_AllFields(t *testing.T) {
	t.Parallel()
	cmd := coreslashcmd.UserCommand{
		Name:          "explain",
		Description:   "Explain a concept.",
		WhenToUse:     "On complex topics.",
		DoesNotHandle: "Code fixes.",
		Inputs: []coreslashcmd.UserCommandInput{
			{Name: "audience", Required: true, Hint: "Target audience"},
			{Name: "depth", Required: false},
		},
	}
	got := formatSkillEntry(cmd)
	for _, want := range []string{
		"### /explain",
		"Explain a concept.",
		"When to use: On complex topics.",
		"Does not handle: Code fixes.",
		"Arguments:",
		"- audience (required): Target audience",
		"- depth",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatSkillEntry missing %q in: %q", want, got)
		}
	}
}

func TestRegisterSkillCatalogBuiltin_NilReg_NoOp(t *testing.T) {
	t.Parallel()
	// Should not panic when reg is nil.
	RegisterSkillCatalogBuiltin(nil, SkillCatalogDeps{})
}

func TestSkillCatalogBuiltin_NilStore_Passthrough(t *testing.T) {
	t.Parallel()
	reg := NewBuiltinRegistry()
	RegisterSkillCatalogBuiltin(reg, SkillCatalogDeps{Store: nil})

	ev := PreSendEvent{
		Messages: []HookMessage{{Role: "user", Content: "help"}},
	}
	got, err := runPreSendBuiltin(t, reg, BuiltinSkillCatalog, ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Errorf("expected 1 message (nil store = passthrough), got %d", len(got.Messages))
	}
}

func TestSkillCatalogBuiltin_EmptySkills_Passthrough(t *testing.T) {
	t.Parallel()
	store := &fakeSkillStore{skills: nil}
	reg := NewBuiltinRegistry()
	RegisterSkillCatalogBuiltin(reg, SkillCatalogDeps{Store: store})

	ev := PreSendEvent{
		Messages: []HookMessage{{Role: "user", Content: "hello"}},
	}
	got, err := runPreSendBuiltin(t, reg, BuiltinSkillCatalog, ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Errorf("expected 1 message (no skills), got %d", len(got.Messages))
	}
}

func TestSkillCatalogBuiltin_InjectsAfterSystemMessages(t *testing.T) {
	t.Parallel()
	store := &fakeSkillStore{
		skills: []coreslashcmd.UserCommand{
			{Name: "summarize", Description: "Summarize.", WhenToUse: "On long docs."},
		},
	}
	reg := NewBuiltinRegistry()
	RegisterSkillCatalogBuiltin(reg, SkillCatalogDeps{Store: store})

	ev := PreSendEvent{
		Messages: []HookMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Summarize this doc."},
		},
	}
	got, err := runPreSendBuiltin(t, reg, BuiltinSkillCatalog, ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 3 messages: system, injected catalog, user.
	if len(got.Messages) != 3 {
		t.Fatalf("expected 3 messages after injection, got %d: %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != "system" {
		t.Errorf("messages[0] should be system, got %q", got.Messages[0].Role)
	}
	if got.Messages[1].Role != "system" {
		t.Errorf("messages[1] (injected) should be system, got %q", got.Messages[1].Role)
	}
	if !strings.Contains(got.Messages[1].Content, "Available skills") {
		t.Errorf("injected message missing catalog header: %q", got.Messages[1].Content)
	}
	if got.Messages[2].Role != "user" {
		t.Errorf("messages[2] should be user, got %q", got.Messages[2].Role)
	}
}

func TestSkillCatalogBuiltin_NoSystemMessages_InjectsAtFront(t *testing.T) {
	t.Parallel()
	store := &fakeSkillStore{
		skills: []coreslashcmd.UserCommand{
			{Name: "explain", Description: "Explain a concept."},
		},
	}
	reg := NewBuiltinRegistry()
	RegisterSkillCatalogBuiltin(reg, SkillCatalogDeps{Store: store})

	ev := PreSendEvent{
		Messages: []HookMessage{
			{Role: "user", Content: "explain quantum computing"},
		},
	}
	got, err := runPreSendBuiltin(t, reg, BuiltinSkillCatalog, ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" {
		t.Errorf("injected catalog should be first (system role), got %q", got.Messages[0].Role)
	}
}

// runPreSendBuiltin invokes the named pre-send builtin from the registry with
// empty config and returns the resulting event.
func runPreSendBuiltin(t *testing.T, reg *BuiltinRegistry, name string, ev PreSendEvent) (PreSendEvent, error) {
	t.Helper()
	fn, ok := reg.preSend[name]
	if !ok {
		t.Fatalf("builtin %q not registered", name)
	}
	return fn(context.Background(), ev, nil)
}
