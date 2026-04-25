// Package capabilities loads per-provider capability descriptors from
// YAML data files (plan §R4: descriptors live as data, not code) and
// gates outgoing requests against them (FR-013).
package capabilities

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

//go:embed data/*.yaml
var dataFS embed.FS

// providerSpec is the on-disk schema (capabilities/data/<provider>.yaml).
type providerSpec struct {
	Provider string         `yaml:"provider"`
	Defaults map[string]bool `yaml:"defaults"`
	Models   []modelEntry   `yaml:"models"`
}

type modelEntry struct {
	Match           string `yaml:"match"`
	Streaming       bool   `yaml:"streaming"`
	ToolCalling     bool   `yaml:"tool_calling"`
	Vision          bool   `yaml:"vision"`
	JSONMode        bool   `yaml:"json_mode"`
	PromptCaching   bool   `yaml:"prompt_caching"`
	Reasoning       bool   `yaml:"reasoning"`
	Cancellation    bool   `yaml:"cancellation"`
	UsageReporting  bool   `yaml:"usage_reporting"`
}

// Catalog holds the loaded per-provider data and answers per-(provider,
// model) descriptor lookups.
type Catalog struct {
	specs map[string]*providerSpec
}

// LoadDefault loads the embedded YAML data shipped with the connector.
func LoadDefault() (*Catalog, error) {
	return Load(dataFS, "data")
}

// Load reads YAML files at root inside fsys and returns a Catalog.
func Load(fsys fs.FS, root string) (*Catalog, error) {
	c := &Catalog{specs: map[string]*providerSpec{}}
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("capabilities: read dir: %w", err)
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(root, ent.Name())
		raw, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			return nil, fmt.Errorf("capabilities: read %s: %w", path, rerr)
		}
		var spec providerSpec
		if uerr := yaml.Unmarshal(raw, &spec); uerr != nil {
			return nil, fmt.Errorf("capabilities: parse %s: %w", path, uerr)
		}
		if spec.Provider == "" {
			return nil, fmt.Errorf("capabilities: %s: missing 'provider' field", path)
		}
		c.specs[spec.Provider] = &spec
	}
	return c, nil
}

// Describe returns the descriptor for (provider, model).
//
// If no entry matches the model, the descriptor is filled from the
// provider defaults and the Notes field carries an "unknown_model"
// breadcrumb (plan §R4 fallback: streaming-only safe baseline).
func (c *Catalog) Describe(provider, model string) llm.CapabilityDescriptor {
	desc := llm.CapabilityDescriptor{
		Provider: provider, Model: model,
		Supported: map[llm.Capability]bool{},
		Notes:     map[llm.Capability]string{},
	}
	spec, ok := c.specs[provider]
	if !ok {
		// Unknown provider entirely: streaming-only safe baseline so
		// that a brand-new adapter still has a usable default.
		desc.Supported[llm.CapStreaming] = true
		desc.Supported[llm.CapUsageReporting] = true
		desc.Notes[llm.CapStreaming] = "unknown_provider_default"
		return desc
	}
	// Start from defaults.
	applyDefaults(&desc, spec.Defaults)
	// Apply the first model glob that matches.
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			desc.Supported[llm.CapStreaming] = m.Streaming
			desc.Supported[llm.CapToolCalling] = m.ToolCalling
			desc.Supported[llm.CapVision] = m.Vision
			desc.Supported[llm.CapJSONMode] = m.JSONMode
			desc.Supported[llm.CapPromptCaching] = m.PromptCaching
			desc.Supported[llm.CapReasoning] = m.Reasoning
			desc.Supported[llm.CapCancellation] = m.Cancellation
			desc.Supported[llm.CapUsageReporting] = m.UsageReporting
			return desc
		}
	}
	// Unknown model under a known provider: keep defaults but mark.
	desc.Notes[llm.CapStreaming] = "unknown_model_default"
	return desc
}

func applyDefaults(desc *llm.CapabilityDescriptor, defaults map[string]bool) {
	keymap := map[string]llm.Capability{
		"streaming":       llm.CapStreaming,
		"tool_calling":    llm.CapToolCalling,
		"vision":          llm.CapVision,
		"json_mode":       llm.CapJSONMode,
		"prompt_caching":  llm.CapPromptCaching,
		"reasoning":       llm.CapReasoning,
		"cancellation":    llm.CapCancellation,
		"usage_reporting": llm.CapUsageReporting,
	}
	for k, v := range defaults {
		if cap, ok := keymap[k]; ok {
			desc.Supported[cap] = v
		}
	}
}

// matchGlob is a tiny prefix/suffix glob, sufficient for the connector's
// match expressions of shape "claude-sonnet-*" / "anthropic.claude-*".
// Only "*" is recognized as a wildcard, and only as a trailing or
// leading suffix.
func matchGlob(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(s, suffix)
	}
	return pattern == s
}
