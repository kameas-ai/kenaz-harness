package slashcmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// API is the concrete SlashAPI implementation. It delegates to the
// supplied *slashcmd.Registry; the registry owns all command-specific
// behaviour. The view's responsibility is wire shape conversion.
type API struct {
	registry *coreslashcmd.Registry
	store    *coreslashcmd.Store    // may be nil before user-slashcmd WP02 wiring
	dispatch *coreslashcmd.Dispatch // may be nil before WP03 wiring
}

// New constructs the API. A nil registry is permitted — the surface
// degrades to a friendly error result on every Execute and an empty
// List, which matches the chassis-stays-bootable stance the rest of
// the rpc layer takes for the New(nil) test path.
func New(registry *coreslashcmd.Registry) *API {
	return &API{registry: registry}
}

// NewWithStore constructs the API with user-command store + dispatch wired.
func NewWithStore(registry *coreslashcmd.Registry, store *coreslashcmd.Store, dispatch *coreslashcmd.Dispatch) *API {
	return &API{registry: registry, store: store, dispatch: dispatch}
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
			IsUser:      false,
		})
	}
	return out, nil
}

// ── user command CRUD ─────────────────────────────────────────────────

// UserList returns all user commands visible to the given projectID.
func (a *API) UserList(ctx context.Context, projectID string) ([]UserCommandSummaryWire, error) {
	if a.store == nil {
		return nil, fmt.Errorf("slashcmd view: user command store not wired")
	}
	cmds, err := a.store.LoadUser(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]UserCommandSummaryWire, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, UserCommandSummaryWire{
			Name:           c.Name,
			Scope:          string(c.Scope),
			ProjectID:      c.ProjectID,
			Kind:           string(c.Kind),
			Description:    c.Description,
			ModelInvokable: c.ModelInvokable,
			Icon:           c.Icon,
			UpdatedAt:      c.UpdatedAt,
		})
	}
	return out, nil
}

// UserGet returns the full detail of a single user command.
func (a *API) UserGet(ctx context.Context, name, projectID string) (UserCommandWire, error) {
	if a.store == nil {
		return UserCommandWire{}, fmt.Errorf("slashcmd view: user command store not wired")
	}
	cmd, err := a.store.LoadUserOne(ctx, name, projectID)
	if err != nil {
		return UserCommandWire{}, err
	}
	return coreToWire(cmd), nil
}

// UserSave creates or updates a user command.
// Returns an error when HARNESS_USER_SLASHCMD=false (WP09 flag gate).
func (a *API) UserSave(ctx context.Context, wire UserCommandWire) error {
	if a.store == nil {
		return fmt.Errorf("slashcmd view: user command store not wired")
	}
	if !coreslashcmd.UserSlashcmdEnabled() {
		return coreslashcmd.ErrFeatureDisabled
	}
	cmd := wireToCoreCmd(wire)
	return a.store.SaveUser(ctx, cmd)
}

// UserDelete removes a user command.
func (a *API) UserDelete(ctx context.Context, name, projectID string) error {
	if a.store == nil {
		return fmt.Errorf("slashcmd view: user command store not wired")
	}
	return a.store.DeleteUser(ctx, name, projectID)
}

// UserRun dispatches a user command by name.
func (a *API) UserRun(ctx context.Context, name string, args map[string]string, sessionID, projectID, cwd, selection string) (RunResultWire, error) {
	if a.dispatch == nil {
		return RunResultWire{}, fmt.Errorf("slashcmd view: dispatch not wired")
	}
	sc := coreslashcmd.SessionContext{
		SessionID: sessionID,
		ProjectID: projectID,
		CWD:       cwd,
		Selection: selection,
		Date:      time.Now().Format("2006-01-02"),
	}
	result, err := a.dispatch.Run(ctx, name, args, sc)
	wire := RunResultWire{
		Kind:         result.Kind,
		Text:         result.Text,
		RenderedArgs: result.RenderedArgs,
		ToolName:     result.ToolName,
		Metadata:     result.Metadata,
	}
	return wire, err
}

// ── wire conversion helpers ───────────────────────────────────────────

func coreToWire(c coreslashcmd.UserCommand) UserCommandWire {
	return UserCommandWire{
		Name:             c.Name,
		Scope:            string(c.Scope),
		ProjectID:        c.ProjectID,
		Kind:             string(c.Kind),
		Description:      c.Description,
		WhenToUse:        c.WhenToUse,
		DoesNotHandle:    c.DoesNotHandle,
		ModelInvokable:   c.ModelInvokable,
		Icon:             c.Icon,
		HiddenFromPanel:  c.HiddenFromPanel,
		Body:             c.Body,
		Tool:             c.Tool,
		ToolArgsTemplate: c.ToolArgsTemplate,
		Inputs:           c.Inputs,
		PayloadPath:      c.PayloadPath,
		CreatedAt:        c.CreatedAt,
		UpdatedAt:        c.UpdatedAt,
	}
}

func wireToCoreCmd(w UserCommandWire) coreslashcmd.UserCommand {
	return coreslashcmd.UserCommand{
		Name:             w.Name,
		Scope:            coreslashcmd.UserCommandScope(w.Scope),
		ProjectID:        w.ProjectID,
		Kind:             coreslashcmd.UserCommandKind(w.Kind),
		Description:      w.Description,
		WhenToUse:        w.WhenToUse,
		DoesNotHandle:    w.DoesNotHandle,
		ModelInvokable:   w.ModelInvokable,
		Icon:             w.Icon,
		HiddenFromPanel:  w.HiddenFromPanel,
		Body:             w.Body,
		Tool:             w.Tool,
		ToolArgsTemplate: w.ToolArgsTemplate,
		Inputs:           w.Inputs,
		CreatedAt:        w.CreatedAt,
		UpdatedAt:        w.UpdatedAt,
	}
}

// Compile-time witness.
var _ SlashAPI = (*API)(nil)
