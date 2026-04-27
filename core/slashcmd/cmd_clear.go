package slashcmd

import (
	"context"
)

// clearDividerText is the canned content /clear appends to the
// session transcript. The frontend's MessageBubble renders system-
// role messages with a distinct visual weight, so the divider stands
// out from user / assistant turns. Persisted history is preserved —
// the divider is just a visual cue.
const clearDividerText = "— conversation cleared —"

// clearCommand implements /clear. The on-disk message history is
// preserved (privacy + future audit value); a single system message
// is appended so the user sees a clear visual reset.
type clearCommand struct{}

func (clearCommand) Name() string        { return "clear" }
func (clearCommand) Description() string { return "Insert a divider in the current chat (history preserved)." }
func (clearCommand) Hidden() bool        { return false }
func (clearCommand) ComingSoon() bool    { return false }

func (clearCommand) Run(ctx context.Context, env Env, _ []string) (Result, error) {
	if env.SessionID == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/clear: no active session",
		}, nil
	}
	if env.Sessions == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/clear: session manager not wired",
		}, nil
	}
	if _, err := env.Sessions.AppendSystemMessage(ctx, env.SessionID, clearDividerText); err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/clear: " + err.Error(),
		}, err
	}
	return Result{
		Kind: ResultKindSystem,
		Text: clearDividerText,
	}, nil
}
