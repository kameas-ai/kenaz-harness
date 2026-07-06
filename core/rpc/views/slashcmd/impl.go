package slashcmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// auditEmitter is the minimal interface the slashcmd view needs for audit.
// Matches the interface used by core/rpc/views/catalog.
type auditEmitter interface {
	EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error
}

// SkillDeps bundles the fleet dependencies needed for skill publish/install.
// All fields are optional — the surface degrades gracefully when nil.
// (fleet-skills-sync-01NDFSEX18 WP02/03/04)
type SkillDeps struct {
	// SkillStore persists fleet-installed skills to disk.
	SkillStore *coreslashcmd.SkillStore
	// FleetClient is the fleet HTTP client for publish/install RPCs.
	FleetClient *corefleet.Client
	// Signer is the device signing key for payload signing.
	Signer *corefleet.DeviceSigner
	// GetCaps returns the current capability snapshot at call time so that
	// tier changes propagate within one poll interval without restart.
	// When nil, DefaultDenyCapabilities is used (all capability checks fail).
	GetCaps func() *corefleet.Capabilities
	// PubKeyBase64 is the fleet signing public key for install verification.
	PubKeyBase64 string
	// Emitter is the optional audit emitter for skill sync events
	// (fleet-skills-sync-01NDFSEX18 WP07, FR-501).
	// Nil means audit events are silently dropped.
	Emitter auditEmitter
}

// API is the concrete SlashAPI implementation. It delegates to the
// supplied *slashcmd.Registry; the registry owns all command-specific
// behaviour. The view's responsibility is wire shape conversion.
type API struct {
	registry   *coreslashcmd.Registry
	store      *coreslashcmd.Store    // may be nil before user-slashcmd WP02 wiring
	dispatch   *coreslashcmd.Dispatch // may be nil before WP03 wiring
	skillDeps  SkillDeps              // fleet-skills-sync-01NDFSEX18
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

// WithSkillDeps wires fleet skill dependencies into the API.
// Called from rpc.New() after the fleet client is configured.
// (fleet-skills-sync-01NDFSEX18 WP02)
func (a *API) WithSkillDeps(deps SkillDeps) *API {
	a.skillDeps = deps
	return a
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

// ── fleet skill methods (fleet-skills-sync-01NDFSEX18 WP02/03/04) ────────────

// SkillList returns all fleet-installed skills from the SkillStore.
func (a *API) SkillList(_ context.Context) ([]SkillItemWire, error) {
	if a.skillDeps.SkillStore == nil {
		return nil, nil // not wired — return empty, not an error
	}
	skills, err := a.skillDeps.SkillStore.List()
	if err != nil {
		return nil, err
	}
	out := make([]SkillItemWire, 0, len(skills))
	for _, sk := range skills {
		// Detect shadow: the Registry has the trigger but with a different type.
		shadowed := false
		if a.registry != nil {
			if cmd, ok := a.registry.Lookup(sk.EffectiveTrigger()); ok {
				// If the registry has the trigger but not via this skill's EffectiveTrigger,
				// it means a different command is occupying it.
				if cmd.Description() != sk.Description {
					shadowed = true
				}
			}
		}
		out = append(out, skillToWire(sk, shadowed))
	}
	return out, nil
}

// SkillPublish signs the user command named by (name, projectID) and
// publishes it to the fleet catalog as a skill with the given visibility.
func (a *API) SkillPublish(ctx context.Context, name, projectID, visibility string) error {
	if a.store == nil {
		return fmt.Errorf("slashcmd view: user command store not wired")
	}
	if a.skillDeps.FleetClient == nil {
		return corefleet.ErrFleetDisabled
	}
	if a.skillDeps.Signer == nil {
		return fmt.Errorf("slashcmd view: skill publish: signer not configured")
	}

	// Load the source user command.
	cmd, err := a.store.LoadUserOne(ctx, name, projectID)
	if err != nil {
		return fmt.Errorf("slashcmd view: skill publish load command: %w", err)
	}

	// Build a Skill from the UserCommand.
	skill := coreslashcmd.Skill{
		ID:               name,
		Trigger:          name,
		Kind:             cmd.Kind,
		Description:      cmd.Description,
		Body:             cmd.Body,
		Tool:             cmd.Tool,
		ToolArgsTemplate: cmd.ToolArgsTemplate,
		Inputs:           cmd.Inputs,
		Source:           coreslashcmd.SkillSourceCatalog,
	}

	vis := corefleet.CatalogVisibility(visibility)
	var caps *corefleet.Capabilities
	if a.skillDeps.GetCaps != nil {
		caps = a.skillDeps.GetCaps()
	}
	if caps == nil {
		c := corefleet.DefaultDenyCapabilities()
		caps = &c
	}

	item, err := corefleet.PublishSkill(ctx, a.skillDeps.FleetClient, caps, a.skillDeps.Signer, skill, vis)
	if err == nil && a.skillDeps.Emitter != nil {
		// FR-501: fleet.skill_published audit event.
		_ = a.skillDeps.Emitter.EmitFleetEvent(ctx, contextaudit.KindFleetSkillPublished,
			contextaudit.FleetSkillPublishedPayload{
				CatalogID:  item.ID,
				Slug:       item.Slug,
				Version:    item.Version,
				Visibility: visibility,
			})
	}
	return err
}

// SkillInstall downloads, verifies, and live-registers a skill from the catalog.
func (a *API) SkillInstall(ctx context.Context, catalogID, version string) error {
	if a.skillDeps.FleetClient == nil {
		return corefleet.ErrFleetDisabled
	}
	if a.skillDeps.SkillStore == nil {
		return fmt.Errorf("slashcmd view: skill store not wired")
	}
	if a.registry == nil {
		return fmt.Errorf("slashcmd view: registry not wired")
	}
	err := corefleet.InstallSkill(ctx, a.skillDeps.FleetClient,
		a.skillDeps.SkillStore, a.registry,
		a.skillDeps.PubKeyBase64, catalogID, version)
	if err == nil && a.skillDeps.Emitter != nil {
		// FR-501: fleet.skill_installed audit event.
		// Fetch the trigger from the store so the audit entry records it.
		trigger := catalogID // fallback when store lookup fails
		if sk, lookupErr := a.skillDeps.SkillStore.Get(catalogID); lookupErr == nil {
			trigger = sk.EffectiveTrigger()
		}
		_ = a.skillDeps.Emitter.EmitFleetEvent(ctx, contextaudit.KindFleetSkillInstalled,
			contextaudit.FleetSkillInstalledPayload{
				CatalogID: catalogID,
				Version:   version,
				Trigger:   trigger,
			})
	}
	return err
}

// SkillUninstall removes and live-unregisters a skill by store ID.
func (a *API) SkillUninstall(ctx context.Context, skillID string) error {
	if a.skillDeps.SkillStore == nil {
		return fmt.Errorf("slashcmd view: skill store not wired")
	}
	if a.registry == nil {
		return fmt.Errorf("slashcmd view: registry not wired")
	}
	// Capture trigger before removal for the audit record.
	var trigger string
	if sk, lookupErr := a.skillDeps.SkillStore.Get(skillID); lookupErr == nil {
		trigger = sk.EffectiveTrigger()
	}
	err := corefleet.UninstallSkill(a.skillDeps.SkillStore, a.registry, skillID)
	if err == nil && a.skillDeps.Emitter != nil {
		// FR-501: fleet.skill_uninstalled audit event.
		_ = a.skillDeps.Emitter.EmitFleetEvent(ctx, contextaudit.KindFleetSkillUninstalled,
			contextaudit.FleetSkillUninstalledPayload{
				SkillID: skillID,
				Trigger: trigger,
			})
	}
	return err
}

// SkillRenameLocalTrigger sets a local trigger alias to resolve a shadow conflict.
// An empty newTrigger clears the alias (reverts to canonical trigger).
// (fleet-skills-sync-01NDFSEX18 WP06, FR-401)
func (a *API) SkillRenameLocalTrigger(_ context.Context, skillID, newTrigger string) error {
	if a.skillDeps.SkillStore == nil {
		return fmt.Errorf("slashcmd view: skill store not wired")
	}
	if a.registry == nil {
		return fmt.Errorf("slashcmd view: registry not wired")
	}
	return coreslashcmd.RenameLocalTrigger(a.skillDeps.SkillStore, a.registry, skillID, newTrigger)
}

// skillToWire converts a Skill to its wire shape.
func skillToWire(sk coreslashcmd.Skill, shadowed bool) SkillItemWire {
	return SkillItemWire{
		ID:           sk.ID,
		CatalogID:    sk.CatalogID,
		Version:      sk.Version,
		Source:       string(sk.Source),
		Trigger:      sk.Trigger,
		LocalTrigger: sk.LocalTrigger,
		Kind:         string(sk.Kind),
		Description:  sk.Description,
		Body:         sk.Body,
		OrgManaged:   sk.OrgManaged,
		Disabled:     sk.Disabled,
		Shadowed:     shadowed,
		InstalledAt:  sk.InstalledAt,
		UpdatedAt:    sk.UpdatedAt,
	}
}

// Compile-time witness.
var _ SlashAPI = (*API)(nil)
