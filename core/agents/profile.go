// Package agents implements the sub-agent profile registry for the
// branch-subagent-interactive-01KZNP3B mission.
//
// A Profile is a YAML document that names a persona, model, allowed tools,
// autonomy tier, budget limits, and merge policy. Profiles drive
// __subagent_dispatch: the model names a profile, the builtin tool resolves
// it and configures the spawned branch accordingly.
//
// Profiles live in two locations:
//   - core/agents/bundled/*.yaml — shipped inside the binary via //go:embed.
//   - <DataDir>/agents/*.yaml   — user-authored overrides.
//
// User profiles win on id collision (same semantics as slashcmd overrides).
// Bundled profiles are read-only: Validate returns ErrBundledReadOnly when
// the caller tries to save over a bundled id.
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package agents

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// ErrBundledReadOnly is returned by Save when the id matches a bundled
// profile. The caller should duplicate the profile with a new id instead.
var ErrBundledReadOnly = errors.New("agents: bundled profiles are read-only")

// ErrProfileNotFound is returned by LoadProfile / Delete when the id
// does not resolve to any known profile.
var ErrProfileNotFound = errors.New("agents: profile not found")

// MergePolicy controls how a sub-agent's output is returned to the parent
// session when the worker completes.
type MergePolicy string

const (
	// MergePolicyAuto merges the summary immediately on completion without
	// user confirmation. The default for most bundled profiles.
	MergePolicyAuto MergePolicy = "auto"

	// MergePolicyConfirm presents a merge-confirmation card to the user
	// before the summary is injected into the parent. Automatically
	// applied when the user has typed a steering message into the worker.
	MergePolicyConfirm MergePolicy = "confirm"

	// MergePolicyManual requires the parent session to explicitly call
	// __subagent_merge(branch_id) before any output reaches the parent.
	MergePolicyManual MergePolicy = "manual"
)

// Profile is the in-memory representation of a sub-agent profile document.
// The wire shape for YAML is the snake_case version of each field.
type Profile struct {
	// ID is the unique slug used by __subagent_dispatch. Must be lowercase,
	// alphanumeric + hyphens only. No two profiles may share an id; user
	// profiles win on collision with bundled profiles.
	ID string `yaml:"id" json:"id"`

	// Name is the human-readable display name shown in the Settings → Agents
	// panel and the BranchSidebar status chip.
	Name string `yaml:"name" json:"name"`

	// Description is a one-sentence explanation of what the profile does.
	Description string `yaml:"description" json:"description"`

	// WhenToUse guides the model on when to dispatch this profile. It is
	// injected into the parent session's tool description.
	WhenToUse string `yaml:"when_to_use" json:"whenToUse,omitempty"`

	// Model is the LLM to use in the spawned branch. Follows the
	// "<provider>/<model-id>" convention (e.g. "anthropic/claude-haiku-4-5").
	// Empty means inherit the parent session's model.
	Model string `yaml:"model" json:"model,omitempty"`

	// AutonomyTier controls the worker's auto-approval and iteration budget.
	// Stored as an autonomy.Tier int but serialised to/from YAML as a
	// lowercase string ("cautious", "default", etc.) via UnmarshalYAML.
	AutonomyTier autonomy.Tier `yaml:"-" json:"autonomyTier"`

	// AllowedTools is an explicit list of tool IDs the worker may call.
	// An empty slice means "all tools the session allows" (inherited).
	AllowedTools []string `yaml:"allowed_tools" json:"allowedTools,omitempty"`

	// DeniedTools is a list of tool IDs the worker is NEVER allowed to call,
	// even if AllowedTools would permit them. Deny wins over allow.
	DeniedTools []string `yaml:"denied_tools" json:"deniedTools,omitempty"`

	// BudgetTokens is the maximum number of tokens the worker may consume.
	// Zero means no limit beyond the parent session's limit.
	BudgetTokens int `yaml:"budget_tokens" json:"budgetTokens,omitempty"`

	// BudgetTimeS is the maximum wall-clock seconds the worker may run.
	// Zero means no time limit.
	BudgetTimeS int `yaml:"budget_time_s" json:"budgetTimeS,omitempty"`

	// SystemPromptOverride replaces the default system prompt in the spawned
	// branch. Empty means inherit the parent's system prompt.
	SystemPromptOverride string `yaml:"system_prompt_override" json:"systemPromptOverride,omitempty"`

	// MergePolicy controls how the worker's output is delivered back to the
	// parent session. Defaults to MergePolicyAuto when empty.
	MergePolicy MergePolicy `yaml:"merge_policy" json:"mergePolicy,omitempty"`

	// Bundled is set to true by the loader for profiles loaded from the
	// embedded bundled set. User-authored profiles have Bundled=false.
	// This field is never serialised to YAML.
	Bundled bool `yaml:"-" json:"bundled"`
}

// profileYAML is an intermediate type used for YAML decoding so the
// autonomy_tier string field can be converted to autonomy.Tier. We use
// a separate type rather than implementing yaml.Unmarshaler on Profile
// itself to avoid recursive unmarshal.
type profileYAML struct {
	ID                   string      `yaml:"id"`
	Name                 string      `yaml:"name"`
	Description          string      `yaml:"description"`
	WhenToUse            string      `yaml:"when_to_use"`
	Model                string      `yaml:"model"`
	AutonomyTierStr      string      `yaml:"autonomy_tier"`
	AllowedTools         []string    `yaml:"allowed_tools"`
	DeniedTools          []string    `yaml:"denied_tools"`
	BudgetTokens         int         `yaml:"budget_tokens"`
	BudgetTimeS          int         `yaml:"budget_time_s"`
	SystemPromptOverride string      `yaml:"system_prompt_override"`
	MergePolicy          MergePolicy `yaml:"merge_policy"`
}

// UnmarshalYAML implements yaml.Unmarshaler so AutonomyTier is decoded
// from the "autonomy_tier" string field.
func (p *Profile) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw profileYAML
	if err := unmarshal(&raw); err != nil {
		return err
	}
	tier, err := parseTierString(raw.AutonomyTierStr)
	if err != nil {
		return fmt.Errorf("agents: profile %q: %w", raw.ID, err)
	}
	p.ID = raw.ID
	p.Name = raw.Name
	p.Description = raw.Description
	p.WhenToUse = raw.WhenToUse
	p.Model = raw.Model
	p.AutonomyTier = tier
	p.AllowedTools = raw.AllowedTools
	p.DeniedTools = raw.DeniedTools
	p.BudgetTokens = raw.BudgetTokens
	p.BudgetTimeS = raw.BudgetTimeS
	p.SystemPromptOverride = raw.SystemPromptOverride
	p.MergePolicy = raw.MergePolicy
	return nil
}

// Validate checks that the profile is well-formed. It does not check
// whether tool IDs exist in the live registry; that invariant is tested
// separately by the CI bundled_test.
func Validate(p Profile) error {
	if p.ID == "" {
		return errors.New("agents.Validate: id is required")
	}
	if !isValidID(p.ID) {
		return fmt.Errorf("agents.Validate: id %q must be lowercase alphanumeric + hyphens", p.ID)
	}
	if p.Name == "" {
		return errors.New("agents.Validate: name is required")
	}
	switch p.MergePolicy {
	case "", MergePolicyAuto, MergePolicyConfirm, MergePolicyManual:
		// valid
	default:
		return fmt.Errorf("agents.Validate: unknown merge_policy %q; want auto|confirm|manual", p.MergePolicy)
	}
	// Check for duplicates between AllowedTools and DeniedTools — a tool
	// in both lists is a configuration error; deny always wins but the
	// contradiction is confusing.
	for _, d := range p.DeniedTools {
		for _, a := range p.AllowedTools {
			if a == d {
				return fmt.Errorf("agents.Validate: tool %q appears in both allowed_tools and denied_tools", d)
			}
		}
	}
	return nil
}

// EffectiveMergePolicy returns the resolved MergePolicy for a profile,
// defaulting to MergePolicyAuto when the field is empty.
func (p Profile) EffectiveMergePolicy() MergePolicy {
	if p.MergePolicy == "" {
		return MergePolicyAuto
	}
	return p.MergePolicy
}

// IsDenied reports whether the named tool is in the profile's DeniedTools list.
func (p Profile) IsDenied(toolID string) bool {
	for _, d := range p.DeniedTools {
		if d == toolID {
			return true
		}
	}
	return false
}

// IsAllowed reports whether the named tool is permitted by the profile's
// AllowedTools list. When AllowedTools is empty, every tool is implicitly
// allowed (subject to DeniedTools and session policy).
func (p Profile) IsAllowed(toolID string) bool {
	if len(p.AllowedTools) == 0 {
		return true
	}
	for _, a := range p.AllowedTools {
		if a == toolID {
			return true
		}
	}
	return false
}

// isValidID returns true when s is a non-empty lowercase alphanumeric +
// hyphen string (no leading/trailing hyphens).
func isValidID(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

// ProfileSummary is the lightweight wire shape returned by ListProfiles.
// The full Profile struct carries potentially large SystemPromptOverride
// bodies; the summary omits them for fast list rendering.
type ProfileSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model,omitempty"`
	MergePolicy string `json:"mergePolicy,omitempty"`
	Bundled     bool   `json:"bundled"`
}

// toSummary converts a Profile to a ProfileSummary.
func (p Profile) toSummary() ProfileSummary {
	return ProfileSummary{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Model:       p.Model,
		MergePolicy: string(p.EffectiveMergePolicy()),
		Bundled:     p.Bundled,
	}
}

// parseTierString parses a YAML tier string into autonomy.Tier.
// Returns autonomy.TierDefault when the string is empty.
func parseTierString(s string) (autonomy.Tier, error) {
	if s == "" {
		return autonomy.TierDefault, nil
	}
	return autonomy.ParseTier(strings.ToLower(strings.TrimSpace(s)))
}
