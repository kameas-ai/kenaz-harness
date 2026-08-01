package llm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/compaction"
)

// ValidateModelProfile applies the versioned-model-profile-01PMDL04 WP01
// schema rules to a parsed ModelProfile. It mirrors ValidateProfile's
// style (core/llm/profile_schema.go) but validates the *behavioral*
// shape rather than connection/credential config.
//
// The zero value (IsZero()==true) is always valid — an absent profile
// means "today's behavior, unchanged", never a forced default, so it
// short-circuits before any field rule runs. This is the type/schema
// half of the mission only: nothing here yet rejects Cedar governance
// actions or budget-policy fields smuggled into a profile (WP06 extends
// this function to add that layering check) and nothing here resolves
// family inheritance or (model_id, version) lookups (WP02).
//
// Returns nil on success or a typed error describing the first failure.
func ValidateModelProfile(p ModelProfile) error {
	if p.IsZero() {
		return nil
	}
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("llm: model profile id is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("llm: model profile %q: version is required", p.ID)
	}
	if p.PromptTemplate != nil {
		if err := validatePromptTemplateRef(p.ID, p.PromptTemplate); err != nil {
			return err
		}
	}
	if p.ToolDialect != nil {
		if p.ToolDialect.MaxToolDescriptionBytes < 0 {
			return fmt.Errorf("llm: model profile %q: tool_dialect.max_tool_description_bytes must be >= 0", p.ID)
		}
	}
	if p.Context != nil {
		if err := validateContextPolicy(p.ID, p.Context); err != nil {
			return err
		}
	}
	if p.Retry != nil {
		if err := validateRetryPolicy(p.ID, p.Retry); err != nil {
			return err
		}
	}
	if p.EvalManifest != nil {
		if strings.TrimSpace(p.EvalManifest.ID) == "" {
			return fmt.Errorf("llm: model profile %q: eval_manifest.id is required when eval_manifest is set", p.ID)
		}
	}
	return nil
}

var knownPromptFormats = map[string]struct{}{
	"":         {}, // unset — default renderer
	"xml":      {},
	"markdown": {},
}

func validatePromptTemplateRef(profileID string, ref *PromptTemplateRef) error {
	if strings.TrimSpace(ref.ID) == "" {
		return fmt.Errorf("llm: model profile %q: prompt_template.id is required when prompt_template is set", profileID)
	}
	if _, ok := knownPromptFormats[ref.Format]; !ok {
		return fmt.Errorf("llm: model profile %q: unknown prompt_template.format %q (must be xml|markdown|\"\")", profileID, ref.Format)
	}
	return nil
}

// knownAggressiveness enumerates core/compaction's canonical tier
// constants. Built from the exported constants (not a locally-invented
// list) so this validator can't drift from compaction.Tier's own set.
var knownAggressiveness = map[compaction.CompactionAggressiveness]struct{}{
	"":                                    {}, // unset — session/app default
	compaction.AggressivenessOff:          {},
	compaction.AggressivenessConservative: {},
	compaction.AggressivenessBalanced:     {},
	compaction.AggressivenessAggressive:   {},
	compaction.AggressivenessMaximal:      {},
}

func validateContextPolicy(profileID string, c *ContextPolicy) error {
	if _, ok := knownAggressiveness[c.Aggressiveness]; !ok {
		return fmt.Errorf("llm: model profile %q: unknown context.aggressiveness %q", profileID, c.Aggressiveness)
	}
	if c.ContextWindowOverride < 0 {
		return fmt.Errorf("llm: model profile %q: context.context_window_override must be >= 0", profileID)
	}
	return nil
}
