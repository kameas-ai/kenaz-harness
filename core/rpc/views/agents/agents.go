// Package agents is the view-scoped RPC surface for the sub-agent profile
// registry (branch-subagent-interactive-01KZNP3B WP01).
//
// The four RPCs mirror the core/agents CRUD contract and are bound via the
// Wails binding layer in core/rpc/api.go.
package agents

import (
	"context"
	"errors"
	"fmt"

	coreagents "github.com/kameas-ai/kenaz-harness/core/agents"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// ProfileWire is the full wire shape for a sub-agent profile. Mirrors
// coreagents.Profile with JSON tags for the Wails boundary.
type ProfileWire struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	WhenToUse            string   `json:"whenToUse,omitempty"`
	Model                string   `json:"model,omitempty"`
	AutonomyTier         string   `json:"autonomyTier"`
	AllowedTools         []string `json:"allowedTools,omitempty"`
	DeniedTools          []string `json:"deniedTools,omitempty"`
	BudgetTokens         int      `json:"budgetTokens,omitempty"`
	BudgetTimeS          int      `json:"budgetTimeS,omitempty"`
	SystemPromptOverride string   `json:"systemPromptOverride,omitempty"`
	MergePolicy          string   `json:"mergePolicy"`
	Bundled              bool     `json:"bundled"`
}

// ProfileSummaryWire is the lightweight wire shape returned by ListProfiles.
type ProfileSummaryWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model,omitempty"`
	MergePolicy string `json:"mergePolicy"`
	Bundled     bool   `json:"bundled"`
}

// toWire converts a coreagents.Profile to its Wails wire shape.
func toWire(p coreagents.Profile) ProfileWire {
	return ProfileWire{
		ID:                   p.ID,
		Name:                 p.Name,
		Description:          p.Description,
		WhenToUse:            p.WhenToUse,
		Model:                p.Model,
		AutonomyTier:         p.AutonomyTier.String(),
		AllowedTools:         p.AllowedTools,
		DeniedTools:          p.DeniedTools,
		BudgetTokens:         p.BudgetTokens,
		BudgetTimeS:          p.BudgetTimeS,
		SystemPromptOverride: p.SystemPromptOverride,
		MergePolicy:          string(p.EffectiveMergePolicy()),
		Bundled:              p.Bundled,
	}
}

// toSummaryWire converts a coreagents.Profile to its summary wire shape.
func toSummaryWire(p coreagents.Profile) ProfileSummaryWire {
	return ProfileSummaryWire{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Model:       p.Model,
		MergePolicy: string(p.EffectiveMergePolicy()),
		Bundled:     p.Bundled,
	}
}

// fromWire converts the Wails wire shape back to a coreagents.Profile.
func fromWire(w ProfileWire) (coreagents.Profile, error) {
	tier, err := autonomy.ParseTier(w.AutonomyTier)
	if err != nil {
		return coreagents.Profile{}, fmt.Errorf("agents: invalid autonomyTier %q: %w", w.AutonomyTier, err)
	}
	return coreagents.Profile{
		ID:                   w.ID,
		Name:                 w.Name,
		Description:          w.Description,
		WhenToUse:            w.WhenToUse,
		Model:                w.Model,
		AutonomyTier:         tier,
		AllowedTools:         w.AllowedTools,
		DeniedTools:          w.DeniedTools,
		BudgetTokens:         w.BudgetTokens,
		BudgetTimeS:          w.BudgetTimeS,
		SystemPromptOverride: w.SystemPromptOverride,
		MergePolicy:          coreagents.MergePolicy(w.MergePolicy),
		Bundled:              false, // user-supplied profiles are never bundled
	}, nil
}

// API is the RPC surface for the agents view.
type API struct {
	dataDir string
}

// New constructs an agents API bound to dataDir.
func New(dataDir string) *API {
	return &API{dataDir: dataDir}
}

// ListProfiles returns summary entries for all known profiles (bundled + user).
func (a *API) ListProfiles(_ context.Context) ([]ProfileSummaryWire, error) {
	profiles, err := coreagents.LoadAll(a.dataDir)
	if err != nil {
		return nil, fmt.Errorf("agents.ListProfiles: %w", err)
	}
	out := make([]ProfileSummaryWire, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, toSummaryWire(p))
	}
	return out, nil
}

// LoadProfile returns the full profile for the given id.
func (a *API) LoadProfile(_ context.Context, id string) (ProfileWire, error) {
	p, err := coreagents.LoadProfile(a.dataDir, id)
	if err != nil {
		if errors.Is(err, coreagents.ErrProfileNotFound) {
			return ProfileWire{}, fmt.Errorf("agents.LoadProfile: %w", coreagents.ErrProfileNotFound)
		}
		return ProfileWire{}, fmt.Errorf("agents.LoadProfile: %w", err)
	}
	return toWire(p), nil
}

// SaveProfile creates or updates a user-authored profile. Returns an error
// for bundled ids (the frontend must duplicate the profile first).
func (a *API) SaveProfile(_ context.Context, w ProfileWire) error {
	p, err := fromWire(w)
	if err != nil {
		return fmt.Errorf("agents.SaveProfile: %w", err)
	}
	if err := coreagents.Save(a.dataDir, p); err != nil {
		return fmt.Errorf("agents.SaveProfile: %w", err)
	}
	return nil
}

// DeleteProfile removes a user-authored profile by id. Returns an error for
// bundled ids or profiles that do not exist.
func (a *API) DeleteProfile(_ context.Context, id string) error {
	if err := coreagents.Delete(a.dataDir, id); err != nil {
		return fmt.Errorf("agents.DeleteProfile: %w", err)
	}
	return nil
}
