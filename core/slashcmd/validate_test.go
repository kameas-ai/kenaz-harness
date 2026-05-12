package slashcmd

import (
	"errors"
	"testing"
)

func TestValidate_ValidCommands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cmd  UserCommand
	}{
		{
			name: "global text command",
			cmd: UserCommand{
				Name:        "standup",
				Scope:       ScopeGlobal,
				Kind:        KindText,
				Description: "Standup template",
				Body:        "What did I do yesterday?",
			},
		},
		{
			name: "project tool command",
			cmd: UserCommand{
				Name:             "deploy",
				Scope:            ScopeProject,
				ProjectID:        "proj-abc",
				Kind:             KindTool,
				Description:      "Deploy to staging",
				Tool:             "bash",
				ToolArgsTemplate: "./scripts/deploy.sh",
			},
		},
		{
			name: "global prompt command",
			cmd: UserCommand{
				Name:        "review",
				Scope:       ScopeGlobal,
				Kind:        KindPrompt,
				Description: "Code review prompt",
				Body:        "Review this: {{selection}}",
			},
		},
		{
			name: "command with enum input",
			cmd: UserCommand{
				Name:             "ship",
				Scope:            ScopeGlobal,
				Kind:             KindTool,
				Description:      "Ship to env",
				Tool:             "bash",
				ToolArgsTemplate: "./ship.sh {{env}}",
				Inputs: []UserCommandInput{
					{
						Name:       "env",
						Kind:       InputKindEnum,
						Required:   true,
						EnumValues: []string{"staging", "prod"},
						Default:    "staging",
					},
				},
			},
		},
		{
			name: "name with hyphens and digits",
			cmd: UserCommand{
				Name:        "my-cmd-2",
				Scope:       ScopeGlobal,
				Kind:        KindText,
				Description: "test",
				Body:        "body",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := Validate(tc.cmd); err != nil {
				t.Errorf("Validate(%q) = %v, want nil", tc.cmd.Name, err)
			}
		})
	}
}

func TestValidate_InvalidName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cmdName string
	}{
		{"starts with digit", "1foo"},
		{"starts with uppercase", "Foo"},
		{"starts with hyphen", "-foo"},
		{"empty", ""},
		{"too long", "abcdefghijklmnopqrstuvwxyzabcdef"}, // 32 chars
		{"contains space", "my cmd"},
		{"contains uppercase", "myCmd"},
		{"contains slash", "my/cmd"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := UserCommand{
				Name:        tc.cmdName,
				Scope:       ScopeGlobal,
				Kind:        KindText,
				Description: "test",
				Body:        "body",
			}
			err := Validate(cmd)
			if !errors.Is(err, ErrValidation) {
				t.Errorf("Validate(%q) = %v, want ErrValidation", tc.cmdName, err)
			}
		})
	}
}

func TestValidate_MaxLengthName(t *testing.T) {
	t.Parallel()
	// Exactly 31 chars: 1 letter + 30 = ok.
	cmd := UserCommand{
		Name:        "a" + "b" + string([]byte{98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98, 98}),
		Scope:       ScopeGlobal,
		Kind:        KindText,
		Description: "test",
		Body:        "body",
	}
	if err := Validate(cmd); err != nil {
		t.Errorf("31-char name should be valid: %v", err)
	}
}

func TestValidate_MissingProjectID(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:        "deploy",
		Scope:       ScopeProject,
		ProjectID:   "", // missing
		Kind:        KindTool,
		Description: "test",
		Tool:        "bash",
		ToolArgsTemplate: "./deploy.sh",
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for missing project_id, got %v", err)
	}
}

func TestValidate_GlobalWithProjectID(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:        "deploy",
		Scope:       ScopeGlobal,
		ProjectID:   "should-not-be-here",
		Kind:        KindText,
		Description: "test",
		Body:        "body",
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for global with project_id, got %v", err)
	}
}

func TestValidate_InvalidScope(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:        "foo",
		Scope:       "workspace", // invalid
		Kind:        KindText,
		Description: "test",
		Body:        "body",
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for invalid scope, got %v", err)
	}
}

func TestValidate_InvalidKind(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:        "foo",
		Scope:       ScopeGlobal,
		Kind:        "macro", // invalid
		Description: "test",
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for invalid kind, got %v", err)
	}
}

func TestValidate_ToolMissingTool(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:             "foo",
		Scope:            ScopeGlobal,
		Kind:             KindTool,
		Description:      "test",
		ToolArgsTemplate: "./foo.sh",
		// Tool field missing
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for tool missing Tool, got %v", err)
	}
}

func TestValidate_ToolMissingTemplate(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:        "foo",
		Scope:       ScopeGlobal,
		Kind:        KindTool,
		Description: "test",
		Tool:        "bash",
		// ToolArgsTemplate missing
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for tool missing template, got %v", err)
	}
}

func TestValidate_TextMissingBody(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:        "foo",
		Scope:       ScopeGlobal,
		Kind:        KindText,
		Description: "test",
		Body:        "", // missing
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for text missing body, got %v", err)
	}
}

func TestValidate_EnumMissingValues(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:             "foo",
		Scope:            ScopeGlobal,
		Kind:             KindTool,
		Description:      "test",
		Tool:             "bash",
		ToolArgsTemplate: "./foo.sh {{env}}",
		Inputs: []UserCommandInput{
			{
				Name:       "env",
				Kind:       InputKindEnum,
				Required:   true,
				EnumValues: nil, // missing
			},
		},
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for enum missing values, got %v", err)
	}
}

func TestValidate_EnumBadDefault(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:             "foo",
		Scope:            ScopeGlobal,
		Kind:             KindTool,
		Description:      "test",
		Tool:             "bash",
		ToolArgsTemplate: "./foo.sh {{env}}",
		Inputs: []UserCommandInput{
			{
				Name:       "env",
				Kind:       InputKindEnum,
				Required:   true,
				EnumValues: []string{"staging", "prod"},
				Default:    "qa", // not in enum_values
			},
		},
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for bad enum default, got %v", err)
	}
}

func TestValidate_InputMissingName(t *testing.T) {
	t.Parallel()
	cmd := UserCommand{
		Name:             "foo",
		Scope:            ScopeGlobal,
		Kind:             KindTool,
		Description:      "test",
		Tool:             "bash",
		ToolArgsTemplate: "./foo.sh",
		Inputs: []UserCommandInput{
			{Name: "", Kind: InputKindText},
		},
	}
	err := Validate(cmd)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for input missing name, got %v", err)
	}
}
