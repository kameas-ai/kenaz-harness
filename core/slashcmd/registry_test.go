package slashcmd

import (
	"context"
	"errors"
	"testing"
)

func TestParseRaw_HappyPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		raw      string
		wantName string
		wantArgs []string
	}{
		{"bare", "/help", "help", nil},
		{"one_arg", "/model gpt-4o", "model", []string{"gpt-4o"}},
		{"multi_arg", "/memorize always use camelCase", "memorize", []string{"always", "use", "camelCase"}},
		{"surrounding_whitespace", "   /clear   ", "clear", nil},
		{"internal_runs_of_whitespace", "/recall    foo   bar", "recall", []string{"foo", "bar"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotArgs, err := parseRaw(tc.raw)
			if err != nil {
				t.Fatalf("parseRaw(%q): %v", tc.raw, err)
			}
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if len(gotArgs) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", gotArgs, tc.wantArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != tc.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], tc.wantArgs[i])
				}
			}
		})
	}
}

func TestParseRaw_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"empty", "", ErrEmptyInput},
		{"whitespace_only", "   ", ErrEmptyInput},
		{"no_slash", "help", ErrMissingSlash},
		{"slash_only", "/", ErrMissingName},
		{"slash_and_whitespace", "/   ", ErrMissingName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parseRaw(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRegistry_DefaultsRegistered(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	want := []string{"branch", "clear", "effort", "forget", "help", "memorize", "model", "recall", "secret", "wf"}
	got := r.List()
	if len(got) != len(want) {
		t.Fatalf("List len = %d, want %d (got=%v)", len(got), len(want), names(got))
	}
	for i, name := range want {
		if got[i].Name() != name {
			t.Errorf("List[%d] = %q, want %q (full=%v)", i, got[i].Name(), name, names(got))
		}
	}
}

func TestRegistry_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.Register(helpCommand{}); err == nil {
		t.Fatal("Register(duplicate) should fail")
	}
}

func TestRegistry_RejectsNilOrEmpty(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.Register(nil); err == nil {
		t.Fatal("Register(nil) should fail")
	}
	if err := r.Register(stubCommand{name: "", description: "x"}); err == nil {
		t.Fatal("Register(empty name) should fail")
	}
}

func TestRegistry_Execute_UnknownCommand(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, err := r.Execute(context.Background(), "session-1", "/foobar")
	if err == nil {
		t.Fatal("Execute(/foobar) should fail")
	}
	if !errors.Is(err, ErrUnknownCommand) {
		t.Errorf("err = %v, want ErrUnknownCommand", err)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want %q", res.Kind, ResultKindError)
	}
	if res.Text != UnknownCommandMessage {
		t.Errorf("Text = %q, want %q", res.Text, UnknownCommandMessage)
	}
}

func TestRegistry_Execute_ParseErrorsFriendly(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	cases := []struct {
		name      string
		raw       string
		wantSubs  string
	}{
		{"empty", "", "empty input"},
		{"no_slash", "help", "must begin with /"},
		{"slash_only", "/", "missing command name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := r.Execute(context.Background(), "s", tc.raw)
			if err == nil {
				t.Fatal("expected error")
			}
			if res.Kind != ResultKindError {
				t.Errorf("Kind = %q, want %q", res.Kind, ResultKindError)
			}
			if !contains(res.Text, tc.wantSubs) {
				t.Errorf("Text = %q, want substring %q", res.Text, tc.wantSubs)
			}
		})
	}
}

// helpers

func names(cmds []Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name()
	}
	return out
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
