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

func TestHelpCommand_NoCommandTaggedComingSoon(t *testing.T) {
	t.Parallel()
	// All seven default commands are now real implementations — none
	// should render the (coming soon) tag in /help. Tests for stubbed
	// behavior previously asserted the four memory/branch commands had
	// the tag; that contract was lifted when /memorize, /recall,
	// /forget, /branch were wired to their subsystems.
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "s", "/help")
	lines := strings.Split(res.Text, "\n")
	for _, name := range []string{"/help", "/clear", "/model", "/memorize", "/recall", "/forget", "/branch"} {
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
			t.Errorf("%s should not be tagged (coming soon): %q", name, matched)
		}
	}
}
