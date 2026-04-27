package slashcmd

import (
	"context"
	"strings"
	"testing"
)

func TestHelpCommand_ListsAllVisibleCommands(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, err := r.Execute(context.Background(), "s", "/help")
	if err != nil {
		t.Fatalf("Execute(/help): %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	for _, want := range []string{
		"/help", "/clear", "/model",
		"/memorize", "/recall", "/forget", "/branch",
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Text missing %q:\n%s", want, res.Text)
		}
	}
}

func TestHelpCommand_TagsComingSoonStubs(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "s", "/help")
	// Each stub line should carry the (coming soon) tag.
	for _, name := range []string{"/memorize", "/recall", "/forget", "/branch"} {
		// Find the line containing the command name; it must also
		// carry the (coming soon) annotation on the same line.
		lines := strings.Split(res.Text, "\n")
		var matched string
		for _, line := range lines {
			if strings.Contains(line, name+" ") {
				matched = line
				break
			}
		}
		if matched == "" {
			t.Errorf("no line for %s in:\n%s", name, res.Text)
			continue
		}
		if !strings.Contains(matched, "(coming soon)") {
			t.Errorf("expected (coming soon) on %s line: %q", name, matched)
		}
	}
}

func TestHelpCommand_RealCommandsHaveNoTag(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "s", "/help")
	lines := strings.Split(res.Text, "\n")
	for _, name := range []string{"/help", "/clear", "/model"} {
		var matched string
		for _, line := range lines {
			if strings.Contains(line, name+" ") {
				matched = line
				break
			}
		}
		if matched == "" {
			t.Errorf("no line for %s in:\n%s", name, res.Text)
			continue
		}
		if strings.Contains(matched, "(coming soon)") {
			t.Errorf("real command %s should not be tagged (coming soon): %q", name, matched)
		}
	}
}
