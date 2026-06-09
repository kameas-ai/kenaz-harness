// Package slashcmd — cmd_skill.go
//
// skillCommand wraps a fleet-installed Skill as a Command so it can be
// registered in the Registry and dispatched like any built-in.
//
// (fleet-skills-sync-01NDFSEX18 WP01)
package slashcmd

import (
	"context"
	"fmt"
)

// skillCommand implements Command for a fleet-installed Skill.
type skillCommand struct {
	skill Skill
}

// Compile-time witness.
var _ Command = skillCommand{}

func (c skillCommand) Name() string        { return c.skill.EffectiveTrigger() }
func (c skillCommand) Description() string { return c.skill.Description }
func (c skillCommand) Hidden() bool        { return false }
func (c skillCommand) ComingSoon() bool    { return false }

// Run executes the skill. The behaviour mirrors the user-command dispatch
// in core/slashcmd/dispatch.go:
//   - kind:text  → returns the skill body as an info result.
//   - kind:prompt → returns the templated body (no args rendering in this
//     path; the full template engine is in Dispatch.Run which the frontend
//     calls for user invocations).
//   - kind:tool  → returns a "dispatched via tool" summary; the frontend
//     routes the actual tool call.
//
// This Run path is the model-invoked path (e.g. /standup from within a
// session transcript). The frontend-invoked path goes through Dispatch.Run.
func (c skillCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	switch c.skill.Kind {
	case KindText:
		return Result{Kind: ResultKindInfo, Text: c.skill.Body}, nil
	case KindPrompt:
		body := c.skill.Body
		if len(args) > 0 {
			body = fmt.Sprintf("%s\n%s", body, joinArgs(args))
		}
		return Result{Kind: ResultKindInfo, Text: body}, nil
	case KindTool:
		summary := fmt.Sprintf("/%s: dispatching %s", c.skill.EffectiveTrigger(), c.skill.Tool)
		return Result{Kind: ResultKindSystem, Text: summary}, nil
	default:
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/%s: unknown skill kind %q", c.skill.EffectiveTrigger(), c.skill.Kind),
		}, fmt.Errorf("slashcmd: skill %q: unknown kind %q", c.skill.ID, c.skill.Kind)
	}
}

// joinArgs joins args with a single space. Mirrors the minimal join used
// by the built-in text commands.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
