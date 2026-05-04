package slashcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// recallDefaultK is the number of memory hits /recall returns when the
// user does not specify a count. Kept small so the chat bubble stays
// readable; the inspector view is the right surface for deep listings.
const recallDefaultK = 5

// memorizeCommand implements /memorize <text>. The text is embedded and
// stored as a new chunk in the long-term memory store, then pinned so
// the background prune sweep leaves it alone (FR-028 — manual_pin
// equivalent).
type memorizeCommand struct{}

func (memorizeCommand) Name() string { return "memorize" }
func (memorizeCommand) Description() string {
	return "Pin text to long-term memory — e.g. /memorize prefer rg over grep"
}
func (memorizeCommand) Hidden() bool     { return false }
func (memorizeCommand) ComingSoon() bool { return false }

func (memorizeCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/memorize: missing text — usage: /memorize <text>",
		}, nil
	}
	if env.Memory == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/memorize: memory subsystem not wired",
		}, nil
	}
	id, err := env.Memory.Memorize(ctx, env.SessionID, text)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/memorize: " + err.Error(),
		}, err
	}
	return Result{
		Kind: ResultKindInfo,
		Text: fmt.Sprintf("memorized (%s) — pinned, %d chars", id, len(text)),
	}, nil
}

// recallCommand implements /recall <query>. Queries the memory store
// for the top-N matches and renders them inline in the chat. The hit
// list is rendered as a numbered list with similarity scores.
type recallCommand struct{}

func (recallCommand) Name() string        { return "recall" }
func (recallCommand) Description() string { return "Recall memory chunks matching a query — e.g. /recall vim shortcuts" }
func (recallCommand) Hidden() bool        { return false }
func (recallCommand) ComingSoon() bool    { return false }

func (recallCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/recall: missing query — usage: /recall <text>",
		}, nil
	}
	if env.Memory == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/recall: memory subsystem not wired",
		}, nil
	}
	hits, err := env.Memory.Recall(ctx, env.SessionID, query, recallDefaultK)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/recall: " + err.Error(),
		}, err
	}
	if len(hits) == 0 {
		return Result{
			Kind: ResultKindInfo,
			Text: fmt.Sprintf("/recall: no matches for %q", query),
		}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recalled %d chunk", len(hits))
	if len(hits) > 1 {
		b.WriteString("s")
	}
	fmt.Fprintf(&b, " for %q:\n", query)
	for i, h := range hits {
		fmt.Fprintf(&b, "  %d. [%s · %.0f%%] %s\n", i+1, h.ID, h.Score*100, summariseLine(h.Content))
	}
	return Result{
		Kind: ResultKindInfo,
		Text: strings.TrimRight(b.String(), "\n"),
	}, nil
}

// summariseLine collapses whitespace and trims the chunk to a single
// readable line in the recall list. The full content is still in the
// store; the inspector view is the right surface for long bodies.
func summariseLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 120
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// forgetCommand implements /forget <id>. Removes the chunk by id and
// renders a friendly success / not-found message.
type forgetCommand struct{}

func (forgetCommand) Name() string        { return "forget" }
func (forgetCommand) Description() string { return "Forget a memory chunk by id — e.g. /forget mem-abc123" }
func (forgetCommand) Hidden() bool        { return false }
func (forgetCommand) ComingSoon() bool    { return false }

func (forgetCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{
			Kind: ResultKindError,
			Text: "/forget: missing id — usage: /forget <chunk-id>",
		}, nil
	}
	if len(args) > 1 {
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/forget: too many arguments (%d) — usage: /forget <chunk-id>", len(args)),
		}, nil
	}
	id := strings.TrimSpace(args[0])
	if id == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/forget: missing id — usage: /forget <chunk-id>",
		}, nil
	}
	if env.Memory == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/forget: memory subsystem not wired",
		}, nil
	}
	err := env.Memory.Forget(ctx, id)
	if err != nil {
		if errors.Is(err, ErrMemoryChunkNotFound) {
			return Result{
				Kind: ResultKindWarning,
				Text: fmt.Sprintf("/forget: no chunk with id %q", id),
			}, nil
		}
		return Result{
			Kind: ResultKindError,
			Text: "/forget: " + err.Error(),
		}, err
	}
	return Result{
		Kind: ResultKindInfo,
		Text: fmt.Sprintf("forgot %s", id),
	}, nil
}
