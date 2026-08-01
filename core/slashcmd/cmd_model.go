package slashcmd

import (
	"context"
	"fmt"
	"strings"
)

// modelCommand implements /model <id>. It looks up the supplied
// model id across the configured providers, picks the first provider
// that authorises it, and returns metadata the frontend applies via
// SessionsView.pickModel(providerId, modelId).
type modelCommand struct{}

func (modelCommand) Name() string        { return "model" }
func (modelCommand) Description() string { return "Switch the active model — e.g. /model gpt-4o" } // model-lit-allow: help-text example, not classification logic
func (modelCommand) Hidden() bool        { return false }
func (modelCommand) ComingSoon() bool    { return false }

func (modelCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{
			Kind: ResultKindError,
			Text: "/model: missing model id — usage: /model <model-id>",
		}, nil
	}
	if len(args) > 1 {
		// Defensive — model ids do not contain whitespace today.
		// Accept the first token and warn rather than guess.
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/model: too many arguments (%d) — usage: /model <model-id>", len(args)),
		}, nil
	}
	want := strings.TrimSpace(args[0])
	if want == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/model: missing model id — usage: /model <model-id>",
		}, nil
	}
	if env.Providers == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/model: provider registry not wired",
		}, nil
	}
	providers, err := env.Providers.ListProviders(ctx)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/model: " + err.Error(),
		}, err
	}
	for _, p := range providers {
		for _, m := range p.AuthorisedModels() {
			if m == want {
				display := p.Name
				if display == "" {
					display = p.ID
				}
				return Result{
					Kind: ResultKindInfo,
					Text: fmt.Sprintf("switched to %s on %s", want, display),
					Metadata: map[string]any{
						MetaKeyProviderID: p.ID,
						MetaKeyModelID:    want,
					},
				}, nil
			}
		}
	}
	return Result{
		Kind: ResultKindError,
		Text: fmt.Sprintf("no configured provider authorises model %s", want),
	}, nil
}
