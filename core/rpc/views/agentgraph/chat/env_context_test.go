package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fixedClock returns a deterministic time source for the environment layer.
func fixedClock() func() time.Time {
	t := time.Date(2026, time.July, 25, 9, 30, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func TestBuildEnvContext_DeterministicSnapshot(t *testing.T) {
	got := buildEnvContext(envContextInput{
		Now:              fixedClock()(),
		GOOS:             "darwin",
		GOARCH:           "arm64",
		Model:            "openai/gpt-4o",
		WorkspaceDir:     "/data/agent-workspace",
		WorkspaceKnown:   true,
		WorkspaceEntries: 0,
		WorkspaceCounted: true,
		Tools: []corellm.ToolSpec{
			{Name: "kenaz__read_file"},
			{Name: "kenaz__web_fetch"},
			{Name: "github__list_issues"},
		},
	})

	want := strings.Join([]string{
		"## Environment",
		"- Kenaz Harness on darwin/arm64. Current date: 2026-07-25.",
		"- Model in use: openai/gpt-4o.",
		"- Workspace: /data/agent-workspace (empty) — a sandboxed agent workspace, not the user's project.",
		"- Some paths require approval via the request-filesystem-access tool.",
		"- Tools: 3 available across filesystem, web, and connected servers.",
	}, "\n")

	if got != want {
		t.Fatalf("env block mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildEnvContext_UnknownWorkspaceAndNoTools(t *testing.T) {
	got := buildEnvContext(envContextInput{
		Now:    fixedClock()(),
		GOOS:   "linux",
		GOARCH: "amd64",
		// no model, no workspace, no tools
	})
	if strings.Contains(got, "Model in use") {
		t.Errorf("expected no model line when Model empty:\n%s", got)
	}
	if !strings.Contains(got, "- Workspace: a sandboxed agent workspace, not the user's project.") {
		t.Errorf("expected generic workspace note when workspace unknown:\n%s", got)
	}
	if !strings.Contains(got, "- Tools: no tools are available this turn.") {
		t.Errorf("expected empty-tools note:\n%s", got)
	}
}

func TestBuildEnvContext_TokenBudget(t *testing.T) {
	// A generous fixture; the block should stay well under ~120 tokens.
	// Rough proxy: 4 chars/token → ~480 chars.
	got := buildEnvContext(envContextInput{
		Now:              fixedClock()(),
		GOOS:             "darwin",
		GOARCH:           "arm64",
		Model:            "anthropic/claude-opus-4",
		WorkspaceDir:     "/Users/example/.config/kenaz-harness/agent-workspace",
		WorkspaceKnown:   true,
		WorkspaceEntries: 12,
		WorkspaceCounted: true,
		Tools: []corellm.ToolSpec{
			{Name: "kenaz__read_file"}, {Name: "kenaz__write_file"},
			{Name: "kenaz__web_fetch"}, {Name: "kenaz__save_artifact"},
			{Name: "kenaz__todo_write"}, {Name: "github__list_issues"},
		},
	})
	if len(got) > 520 {
		t.Errorf("env block too large (%d chars, budget ~480): %q", len(got), got)
	}
}

func TestSummarizeToolInventory(t *testing.T) {
	if got := summarizeToolInventory(nil); got != "no tools are available this turn." {
		t.Errorf("empty inventory: got %q", got)
	}
	got := summarizeToolInventory([]corellm.ToolSpec{
		{Name: "kenaz__read_file"},
		{Name: "kenaz__edit_file"},
		{Name: "kenaz__save_artifact"},
		{Name: "kenaz__todo_write"},
		{Name: "slack__post_message"},
	})
	if !strings.HasPrefix(got, "5 available across ") {
		t.Errorf("expected count prefix, got %q", got)
	}
	for _, cat := range []string{"filesystem", "artifacts", "task tracking", "connected servers"} {
		if !strings.Contains(got, cat) {
			t.Errorf("expected category %q in %q", cat, got)
		}
	}
}

func TestComposeSystemPrompt(t *testing.T) {
	got := composeSystemPrompt("  base  ", "", "   ", "env", "user")
	want := "base\n\nenv\n\nuser"
	if got != want {
		t.Fatalf("compose: got %q want %q", got, want)
	}
	if composeSystemPrompt("", "  ") != "" {
		t.Errorf("all-empty layers should compose to empty string")
	}
}

// capturingRegistry records the GenerationRequest handed to Stream so the
// test can assert the composed System field. It returns a pre-canned
// closed stream.
//
// profile is the corellm.ProviderProfile Profile() returns; the zero
// value (default, unset — matches prior hardcoded behavior) unless a
// test opts in via withProfile, e.g. to exercise a custom Retry policy
// (model-request-path-live-01PMDL01 WP02).
type capturingRegistry struct {
	mu      sync.Mutex
	lastReq corellm.GenerationRequest
	profile corellm.ProviderProfile
}

// withProfile sets the ProviderProfile returned by Profile() and returns
// the receiver for fluent construction in tests.
func (r *capturingRegistry) withProfile(p corellm.ProviderProfile) *capturingRegistry {
	r.profile = p
	return r
}

func (r *capturingRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (r *capturingRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *capturingRegistry) Evict(_ string) error                           { return nil }
func (r *capturingRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return r.profile, nil
}
func (r *capturingRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (r *capturingRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.lastReq = req
	r.mu.Unlock()
	return &cannedStream{}, nil
}
func (r *capturingRegistry) snapshot() corellm.GenerationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastReq
}

// cannedStream is a corellm.Stream that yields no events and a trivial
// terminal response.
type cannedStream struct{}

func (s *cannedStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (s *cannedStream) Cancel() error { return nil }
func (s *cannedStream) Final() (corellm.Response, error) {
	return corellm.Response{FinishReason: "stop"}, nil
}

func TestGenerate_LayersEnvAfterNodePrompt(t *testing.T) {
	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil).
		WithEnvContext(fixedClock(), "")

	const base = "You are the chat node.\nBe concise."
	_, err := adapter.Generate(context.Background(), coreag.LLMRequest{
		SystemPrompt: base,
		Messages:     []coreag.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	sys := reg.snapshot().System
	if !strings.HasPrefix(sys, base) {
		t.Fatalf("composed System must start with the node prompt:\n%s", sys)
	}
	envIdx := strings.Index(sys, "## Environment")
	if envIdx <= 0 {
		t.Fatalf("environment block missing from composed System:\n%s", sys)
	}
	// Environment must come AFTER the node prompt.
	if envIdx < len(base) {
		t.Fatalf("environment block must follow the node prompt (base ends at %d, env at %d):\n%s",
			len(base), envIdx, sys)
	}
	if !strings.Contains(sys, "Kenaz Harness on ") {
		t.Errorf("expected platform line in composed System:\n%s", sys)
	}
	if !strings.Contains(sys, "Model in use: openai/gpt-4o.") {
		t.Errorf("expected model line in composed System:\n%s", sys)
	}
}

func TestBuildUserInstructionsBlock(t *testing.T) {
	// nil resolver → no block.
	a := &LLMProviderAdapter{}
	if got := a.buildUserInstructionsBlock(); got != "" {
		t.Errorf("nil resolver should yield empty block, got %q", got)
	}
	// blank resolver → no block.
	a.WithCustomInstructions(func() string { return "   \n  " })
	if got := a.buildUserInstructionsBlock(); got != "" {
		t.Errorf("blank instructions should yield empty block, got %q", got)
	}
	// present → labeled block, trimmed.
	a.WithCustomInstructions(func() string { return "  Always answer in British English.  " })
	got := a.buildUserInstructionsBlock()
	want := "## User instructions\n\nAlways answer in British English."
	if got != want {
		t.Errorf("user block: got %q want %q", got, want)
	}
}

func TestGenerate_CustomInstructionsLayerOrdering(t *testing.T) {
	const base = "You are the chat node."

	// Absent: no user layer, env still present.
	t.Run("absent", func(t *testing.T) {
		reg := &capturingRegistry{}
		adapter := NewLLMProviderAdapter(reg, "p", "m", nil, nil).
			WithEnvContext(fixedClock(), "")
		if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: base}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		sys := reg.snapshot().System
		if strings.Contains(sys, "## User instructions") {
			t.Errorf("no user layer expected when instructions unset:\n%s", sys)
		}
		if !strings.Contains(sys, "## Environment") {
			t.Errorf("environment layer should still be present:\n%s", sys)
		}
	})

	// Present: user layer appears LAST, after the environment layer.
	t.Run("present-after-env", func(t *testing.T) {
		reg := &capturingRegistry{}
		adapter := NewLLMProviderAdapter(reg, "p", "m", nil, nil).
			WithEnvContext(fixedClock(), "").
			WithCustomInstructions(func() string { return "Prefer tables over prose." })
		if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: base}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		sys := reg.snapshot().System
		baseIdx := strings.Index(sys, base)
		envIdx := strings.Index(sys, "## Environment")
		userIdx := strings.Index(sys, "## User instructions")
		if baseIdx != 0 {
			t.Fatalf("base must lead the composed System:\n%s", sys)
		}
		if envIdx <= baseIdx {
			t.Fatalf("environment must follow the node prompt:\n%s", sys)
		}
		if userIdx <= envIdx {
			t.Fatalf("user instructions must be the FINAL layer (after env):\n%s", sys)
		}
		if !strings.Contains(sys, "Prefer tables over prose.") {
			t.Errorf("expected user instructions text in composed System:\n%s", sys)
		}
	})
}
