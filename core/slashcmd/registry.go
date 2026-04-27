package slashcmd

import (
	"context"
	"fmt"
	"sort"
)

// Deps bundles the dependencies the default v1 commands consume. The
// fields mirror Env's fields exactly — they are forwarded into the
// per-call Env at dispatch time. Nil fields are valid and result in
// individual commands returning a friendly error result.
type Deps struct {
	Sessions  SessionAppender
	Providers ProviderLister
}

// Registry is the dispatch table. Construct with NewRegistry(deps);
// register additional commands via Register before any Execute call.
//
// The Registry is safe for concurrent reads (Execute, List). Mutation
// (Register) is not goroutine-safe — register at construction and
// treat the Registry as immutable thereafter.
type Registry struct {
	deps     Deps
	commands map[string]Command
}

// NewRegistry constructs a Registry with the six v1 commands
// pre-registered.
//
// The default registration set:
//
//   - /help
//   - /clear
//   - /model
//   - /memorize (stub)
//   - /recall   (stub)
//   - /forget   (stub)
//   - /branch   (stub)
//
// Returns an error if a default command name collides — defensive
// only; the test suite catches drift.
func NewRegistry(deps Deps) (*Registry, error) {
	r := &Registry{
		deps:     deps,
		commands: make(map[string]Command),
	}
	for _, cmd := range defaultCommands() {
		if err := r.Register(cmd); err != nil {
			return nil, fmt.Errorf("slashcmd: register %q: %w", cmd.Name(), err)
		}
	}
	return r, nil
}

// Register adds cmd to the registry. Returns an error on duplicate
// name — the caller decides whether duplicates are programmer errors
// (NewRegistry treats them as such) or runtime conflicts (a future
// plugin path may treat them as recoverable).
func (r *Registry) Register(cmd Command) error {
	if cmd == nil {
		return fmt.Errorf("slashcmd: nil command")
	}
	name := cmd.Name()
	if name == "" {
		return fmt.Errorf("slashcmd: command with empty name")
	}
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("slashcmd: duplicate command name %q", name)
	}
	r.commands[name] = cmd
	return nil
}

// Lookup returns the command registered under name. The bool reports
// whether a command was found; callers wanting an error-shape
// response should use Execute or check against ErrUnknownCommand.
func (r *Registry) Lookup(name string) (Command, bool) {
	cmd, ok := r.commands[name]
	return cmd, ok
}

// List returns the visible commands sorted by name. Hidden commands
// are filtered out; ComingSoon commands stay in the list.
func (r *Registry) List() []Command {
	out := make([]Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		if cmd.Hidden() {
			continue
		}
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Execute parses raw and dispatches to the resolved command. The
// supplied sessionID is woven into the per-call Env; everything
// else comes from the Registry's Deps.
//
// Unknown commands return ErrUnknownCommand AND a Result whose Text
// is UnknownCommandMessage and whose Kind is ResultKindError. Both
// are returned so the RPC view can decide whether to surface the
// error inline (current behaviour) or escalate.
//
// Parse errors are surfaced the same way: a Result with
// ResultKindError + a friendly text body, plus the typed error.
func (r *Registry) Execute(ctx context.Context, sessionID, raw string) (Result, error) {
	name, args, err := parseRaw(raw)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: friendlyParseError(err),
		}, err
	}
	cmd, ok := r.Lookup(name)
	if !ok {
		return Result{
			Kind: ResultKindError,
			Text: UnknownCommandMessage,
		}, fmt.Errorf("%w: %q", ErrUnknownCommand, name)
	}
	env := Env{
		SessionID: sessionID,
		Sessions:  r.deps.Sessions,
		Providers: r.deps.Providers,
		Registry:  r,
	}
	return cmd.Run(ctx, env, args)
}

// friendlyParseError translates parse errors into user-visible text.
// The frontend renders this in an error bubble.
func friendlyParseError(err error) string {
	switch {
	case err == nil:
		return ""
	case isErr(err, ErrEmptyInput):
		return "empty input — type a slash command, e.g. /help"
	case isErr(err, ErrMissingSlash):
		return "slash commands must begin with /"
	case isErr(err, ErrMissingName):
		return "missing command name — type /help to list available commands"
	default:
		return err.Error()
	}
}

// isErr is a tiny errors.Is wrapper kept local so the parse.go file
// stays import-light.
func isErr(err, target error) bool {
	return err == target || (err != nil && err.Error() == target.Error())
}

// defaultCommands returns the v1 set in registration order.
// Registration order does not affect List output (that sorts), but
// keeping it deterministic helps debug.
func defaultCommands() []Command {
	return []Command{
		helpCommand{},
		clearCommand{},
		modelCommand{},
		stubCommand{name: "memorize", description: "Pin text to long-term memory (coming soon)."},
		stubCommand{name: "recall", description: "Recall memory chunks matching a query (coming soon)."},
		stubCommand{name: "forget", description: "Forget a memory chunk by id (coming soon)."},
		stubCommand{name: "branch", description: "Branch the conversation onto a smaller or larger model (coming soon)."},
	}
}
