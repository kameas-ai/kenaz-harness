package slashcmd

import (
	"context"
	"errors"

	coreslashcmd "github.com/sigil-tech/kaneaz-harness/core/slashcmd"
)

// API is the concrete SlashAPI implementation. It delegates to the
// supplied *slashcmd.Registry; the registry owns all command-specific
// behaviour. The view's responsibility is wire shape conversion.
type API struct {
	registry *coreslashcmd.Registry
}

// New constructs the API. A nil registry is permitted — the surface
// degrades to a friendly error result on every Execute and an empty
// List, which matches the chassis-stays-bootable stance the rest of
// the rpc layer takes for the New(nil) test path.
func New(registry *coreslashcmd.Registry) *API {
	return &API{registry: registry}
}

// Execute parses raw and dispatches to the registry. Unknown commands
// and parse errors surface as ExecuteResults with Kind="error"; the
// returned error mirrors the registry's error so callers can log /
// distinguish.
func (a *API) Execute(ctx context.Context, sessionID, raw string) (ExecuteResult, error) {
	if a == nil || a.registry == nil {
		return ExecuteResult{
			Kind: coreslashcmd.ResultKindError,
			Text: "slash commands are not wired",
		}, errors.New("slashcmd view: nil registry")
	}
	res, err := a.registry.Execute(ctx, sessionID, raw)
	wire := ExecuteResult{
		Text:     res.Text,
		Kind:     res.Kind,
		Metadata: res.Metadata,
	}
	if wire.Kind == "" {
		wire.Kind = coreslashcmd.ResultKindInfo
	}
	return wire, err
}

// List returns the registry's visible commands as wire-shape entries.
// A nil registry yields an empty slice — the autocomplete dropdown
// renders the empty case as "no commands available".
func (a *API) List(_ context.Context) ([]CommandInfo, error) {
	if a == nil || a.registry == nil {
		return nil, nil
	}
	cmds := a.registry.List()
	out := make([]CommandInfo, 0, len(cmds))
	for _, cmd := range cmds {
		out = append(out, CommandInfo{
			Name:        cmd.Name(),
			Description: cmd.Description(),
			ComingSoon:  cmd.ComingSoon(),
		})
	}
	return out, nil
}

// Compile-time witness.
var _ SlashAPI = (*API)(nil)
