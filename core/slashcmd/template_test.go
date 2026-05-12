package slashcmd

import (
	"errors"
	"testing"
)

func TestRender_RequiredSubstitution(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{
		UserArgs: map[string]string{"env": "staging"},
	}
	got, err := Render("deploy to {{env}} now", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "deploy to staging now" {
		t.Errorf("Render = %q, want %q", got, "deploy to staging now")
	}
}

func TestRender_OptionalSubstitution_Present(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{
		UserArgs: map[string]string{"ticket": "JIRA-123"},
	}
	got, err := Render("ticket: {{?ticket}}", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "ticket: JIRA-123" {
		t.Errorf("Render = %q", got)
	}
}

func TestRender_OptionalSubstitution_Missing(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{UserArgs: map[string]string{}}
	got, err := Render("ticket: {{?ticket}}", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "ticket: " {
		t.Errorf("Render = %q, want %q", got, "ticket: ")
	}
}

func TestRender_RequiredSubstitution_Missing(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{UserArgs: map[string]string{}}
	_, err := Render("deploy to {{env}}", vars)
	if !errors.Is(err, ErrMissingRequiredVar) {
		t.Errorf("expected ErrMissingRequiredVar, got %v", err)
	}
}

func TestRender_ConditionalBlock_Present(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{
		UserArgs: map[string]string{"ticket": "JIRA-456"},
	}
	got, err := Render("./deploy.sh{{#ticket}} --ticket {{ticket}}{{/ticket}}", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "./deploy.sh --ticket JIRA-456" {
		t.Errorf("Render = %q", got)
	}
}

func TestRender_ConditionalBlock_Absent(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{UserArgs: map[string]string{}}
	got, err := Render("./deploy.sh{{#ticket}} --ticket {{ticket}}{{/ticket}}", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "./deploy.sh" {
		t.Errorf("Render = %q, want %q", got, "./deploy.sh")
	}
}

func TestRender_BuiltinVars(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{
		Selection: "selected text",
		CWD:       "/home/user/project",
		Date:      "2026-05-11",
		SessionID: "sess-abc",
		ProjectID: "proj-xyz",
		UserArgs:  map[string]string{},
	}
	cases := []struct {
		tmpl string
		want string
	}{
		{"{{selection}}", "selected text"},
		{"{{cwd}}", "/home/user/project"},
		{"{{date}}", "2026-05-11"},
		{"{{session_id}}", "sess-abc"},
		{"{{project_id}}", "proj-xyz"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := Render(tc.tmpl, vars)
			if err != nil {
				t.Fatalf("Render(%q): %v", tc.tmpl, err)
			}
			if got != tc.want {
				t.Errorf("Render(%q) = %q, want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

func TestRender_MultipleSubstitutions(t *testing.T) {
	t.Parallel()
	vars := TemplateVars{
		UserArgs:  map[string]string{"env": "staging", "ticket": "T-1"},
		SessionID: "s1",
	}
	tmpl := "env={{env}} ticket={{ticket}} session={{session_id}}"
	got, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "env=staging ticket=T-1 session=s1"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRender_NoSubstitutions(t *testing.T) {
	t.Parallel()
	got, err := Render("plain text with no vars", TemplateVars{UserArgs: map[string]string{}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "plain text with no vars" {
		t.Errorf("Render = %q", got)
	}
}

func TestSplitArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  []string
	}{
		{"./deploy.sh --env staging", []string{"./deploy.sh", "--env", "staging"}},
		{"  leading  spaces  ", []string{"leading", "spaces"}},
		{"", []string{}},
		{"single", []string{"single"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := SplitArgs(tc.input)
			if len(got) != len(tc.want) {
				t.Errorf("SplitArgs(%q) = %v, want %v", tc.input, got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("SplitArgs(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}
