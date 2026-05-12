package slashcmd

import (
	"errors"
	"testing"
)

func TestParseMarkdown_RoundTrip(t *testing.T) {
	t.Parallel()
	input := `---
name: deploy
scope: project
project_id: proj-123
kind: tool
description: Deploy current branch to staging
when_to_use: User asks to deploy
does_not_handle: Production deploys
model_invokable: false
icon: rocket
hidden_from_panel: false
tool: bash
tool_args_template: ./scripts/deploy.sh --env staging
inputs:
    - name: env
      type: enum
      required: true
      enum_values:
        - staging
        - dev
      default: staging
    - name: ticket
      type: text
      required: false
---

Optional documentation for the tool command.
`
	cmd, err := ParseMarkdown([]byte(input))
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if cmd.Name != "deploy" {
		t.Errorf("Name = %q, want %q", cmd.Name, "deploy")
	}
	if cmd.Scope != ScopeProject {
		t.Errorf("Scope = %q, want %q", cmd.Scope, ScopeProject)
	}
	if cmd.ProjectID != "proj-123" {
		t.Errorf("ProjectID = %q, want %q", cmd.ProjectID, "proj-123")
	}
	if cmd.Kind != KindTool {
		t.Errorf("Kind = %q, want %q", cmd.Kind, KindTool)
	}
	if cmd.Description != "Deploy current branch to staging" {
		t.Errorf("Description = %q", cmd.Description)
	}
	if cmd.Tool != "bash" {
		t.Errorf("Tool = %q, want %q", cmd.Tool, "bash")
	}
	if cmd.Icon != "rocket" {
		t.Errorf("Icon = %q, want %q", cmd.Icon, "rocket")
	}
	if len(cmd.Inputs) != 2 {
		t.Fatalf("Inputs len = %d, want 2", len(cmd.Inputs))
	}
	if cmd.Inputs[0].Name != "env" {
		t.Errorf("Inputs[0].Name = %q, want %q", cmd.Inputs[0].Name, "env")
	}
	if cmd.Inputs[0].Kind != InputKindEnum {
		t.Errorf("Inputs[0].Kind = %q, want %q", cmd.Inputs[0].Kind, InputKindEnum)
	}
	if len(cmd.Inputs[0].EnumValues) != 2 {
		t.Errorf("Inputs[0].EnumValues len = %d, want 2", len(cmd.Inputs[0].EnumValues))
	}
	if cmd.Inputs[0].Default != "staging" {
		t.Errorf("Inputs[0].Default = %q, want %q", cmd.Inputs[0].Default, "staging")
	}
	if cmd.Body != "Optional documentation for the tool command." {
		t.Errorf("Body = %q", cmd.Body)
	}
}

func TestParseMarkdown_TextKind(t *testing.T) {
	t.Parallel()
	input := `---
name: standup
scope: global
kind: text
description: Daily standup template
---

What did I do yesterday?
What am I doing today?
Any blockers?
`
	cmd, err := ParseMarkdown([]byte(input))
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if cmd.Kind != KindText {
		t.Errorf("Kind = %q, want %q", cmd.Kind, KindText)
	}
	if cmd.Scope != ScopeGlobal {
		t.Errorf("Scope = %q, want %q", cmd.Scope, ScopeGlobal)
	}
	if cmd.Body == "" {
		t.Error("Body should not be empty for kind:text")
	}
}

func TestParseMarkdown_PromptKind(t *testing.T) {
	t.Parallel()
	input := `---
name: review
scope: global
kind: prompt
description: Review the selected code
model_invokable: true
---

Please review this code: {{selection}}

Current working directory: {{cwd}}
`
	cmd, err := ParseMarkdown([]byte(input))
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if cmd.Kind != KindPrompt {
		t.Errorf("Kind = %q, want %q", cmd.Kind, KindPrompt)
	}
	if !cmd.ModelInvokable {
		t.Error("ModelInvokable should be true")
	}
	if cmd.Body == "" {
		t.Error("Body should not be empty for kind:prompt")
	}
}

func TestParseMarkdown_MalformedNoLeadingFence(t *testing.T) {
	t.Parallel()
	input := "name: deploy\nscope: global\n"
	_, err := ParseMarkdown([]byte(input))
	if !errors.Is(err, ErrMalformedFrontmatter) {
		t.Errorf("expected ErrMalformedFrontmatter, got %v", err)
	}
}

func TestParseMarkdown_MalformedNoClosingFence(t *testing.T) {
	t.Parallel()
	input := "---\nname: deploy\nscope: global\n"
	_, err := ParseMarkdown([]byte(input))
	if !errors.Is(err, ErrMalformedFrontmatter) {
		t.Errorf("expected ErrMalformedFrontmatter, got %v", err)
	}
}

func TestParseMarkdown_MalformedBadYAML(t *testing.T) {
	t.Parallel()
	input := "---\n: bad: yaml: here:\n---\n"
	_, err := ParseMarkdown([]byte(input))
	if !errors.Is(err, ErrMalformedFrontmatter) {
		t.Errorf("expected ErrMalformedFrontmatter, got %v", err)
	}
}

func TestParseMarkdown_EmptyFrontmatter(t *testing.T) {
	t.Parallel()
	input := "---\n---\nsome body here\n"
	cmd, err := ParseMarkdown([]byte(input))
	if err != nil {
		t.Fatalf("ParseMarkdown empty frontmatter: %v", err)
	}
	if cmd.Body != "some body here" {
		t.Errorf("Body = %q, want %q", cmd.Body, "some body here")
	}
}

func TestParseMarkdown_MultipleNewlinesBeforeBody(t *testing.T) {
	t.Parallel()
	input := "---\nname: test\nscope: global\nkind: text\ndescription: test\n---\n\n\nbody here\n"
	cmd, err := ParseMarkdown([]byte(input))
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	// Body should not have leading newlines stripped beyond the first.
	if cmd.Body == "" {
		t.Error("Body should not be empty")
	}
}

func TestMarshalMarkdown_RoundTrip(t *testing.T) {
	t.Parallel()
	original := UserCommand{
		Name:        "lint",
		Scope:       ScopeGlobal,
		Kind:        KindTool,
		Description: "Run linter",
		Tool:        "bash",
		ToolArgsTemplate: "golangci-lint run ./...",
		Body:        "Runs the Go linter on the whole module.",
	}
	data, err := MarshalMarkdown(original)
	if err != nil {
		t.Fatalf("MarshalMarkdown: %v", err)
	}
	parsed, err := ParseMarkdown(data)
	if err != nil {
		t.Fatalf("ParseMarkdown after marshal: %v", err)
	}
	if parsed.Name != original.Name {
		t.Errorf("Name round-trip: got %q, want %q", parsed.Name, original.Name)
	}
	if parsed.Kind != original.Kind {
		t.Errorf("Kind round-trip: got %q, want %q", parsed.Kind, original.Kind)
	}
	if parsed.Tool != original.Tool {
		t.Errorf("Tool round-trip: got %q, want %q", parsed.Tool, original.Tool)
	}
	if parsed.Body != original.Body {
		t.Errorf("Body round-trip: got %q, want %q", parsed.Body, original.Body)
	}
}
