package slashcmd

import (
	"context"
	"fmt"
	"strings"
)

// helpCommand implements /help — enumerates every visible command in
// the registry, one per line, with the description and a
// "(coming soon)" tag for stubs.
type helpCommand struct{}

func (helpCommand) Name() string        { return "help" }
func (helpCommand) Description() string { return "List available slash commands." }
func (helpCommand) Hidden() bool        { return false }
func (helpCommand) ComingSoon() bool    { return false }

func (helpCommand) Run(_ context.Context, env Env, _ []string) (Result, error) {
	if env.Registry == nil {
		// Defensive — Registry.Execute always sets this. A nil
		// Registry would mean a hand-constructed Env in a test;
		// return a friendly error rather than panic.
		return Result{
			Kind: ResultKindError,
			Text: "/help: registry not wired",
		}, nil
	}
	cmds := env.Registry.List()
	var b strings.Builder
	b.WriteString("Available slash commands:\n")
	for _, cmd := range cmds {
		tag := ""
		if cmd.ComingSoon() {
			tag = " (coming soon)"
		}
		fmt.Fprintf(&b, "  /%s — %s%s\n", cmd.Name(), cmd.Description(), tag)
	}
	// Trim the trailing newline so the rendered bubble doesn't
	// have a dangling blank line.
	out := strings.TrimRight(b.String(), "\n")
	return Result{
		Kind: ResultKindInfo,
		Text: out,
	}, nil
}
