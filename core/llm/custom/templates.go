// Package custom implements the custom OpenAI-compatible endpoint adapter.
//
// # Template registry (WP01)
//
// LoadEmbeddedTemplates returns the 9 built-in templates (vllm, together,
// groq, fireworks, anyscale, deepseek, mistral, llamacpp, litellm).
// Resolve(host, explicitID) applies the decision order:
//
//  1. explicit id → exact match in registry
//  2. glob match against Template.Glob (path.Match semantics)
//  3. nil (no template recognised)
//
// The YAML schema is intentionally additive: unknown fields in an entry
// are silently ignored, allowing downstream missions (e.g. local-runtimes)
// to extend entries without breaking this package.
package custom

import (
	_ "embed"
	"fmt"
	"net/url"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed templates.yaml
var embeddedTemplatesYAML []byte

// AuthScheme is the credential scheme for a custom endpoint.
type AuthScheme string

const (
	// AuthSchemeBearer sends the credential as "Bearer <key>" in Authorization.
	AuthSchemeBearer AuthScheme = "bearer"
	// AuthSchemeAPIKeyHeader sends the credential in a named header (default: X-API-Key).
	AuthSchemeAPIKeyHeader AuthScheme = "api-key-header"
	// AuthSchemeCustom allows the adapter to forward a header name+value pair
	// specified in the profile's Defaults map (key "auth_header", "auth_value").
	AuthSchemeCustom AuthScheme = "custom"
	// AuthSchemeNone sends no credential material.
	AuthSchemeNone AuthScheme = "none"
)

// Template is one entry in the template registry. Additional YAML fields not
// listed here are ignored (additive schema for extensibility).
type Template struct {
	// ID is the stable slug (e.g. "vllm", "groq").
	ID string `yaml:"id"`
	// Name is the human-readable display label.
	Name string `yaml:"name"`
	// BaseURL is the default base URL (no trailing slash). The adapter
	// appends /chat/completions for the chat endpoint.
	BaseURL string `yaml:"base_url"`
	// AuthScheme is the credential scheme.
	AuthScheme AuthScheme `yaml:"auth_scheme"`
	// AuthHeader is the header name for api-key-header scheme.
	// Defaults to "X-API-Key" when empty.
	AuthHeader string `yaml:"auth_header,omitempty"`
	// Glob is the host glob pattern matched by Resolve when no explicit ID
	// is provided. Uses path.Match semantics.
	Glob string `yaml:"glob,omitempty"`
	// Notes is free-text shown in the UI. May be empty.
	Notes string `yaml:"notes,omitempty"`
}

// templateFile is the on-disk YAML shape.
type templateFile struct {
	Templates []Template `yaml:"templates"`
}

// Registry is an immutable set of templates loaded at process start.
// All methods are safe for concurrent use.
type Registry struct {
	templates []Template
	byID      map[string]*Template
}

// LoadEmbeddedTemplates parses the embedded templates.yaml and returns
// a validated Registry. Returns a non-nil error if:
//   - YAML is malformed
//   - Duplicate IDs exist
//   - Required fields (id, name, base_url, auth_scheme) are missing
//   - Two templates with non-empty Glob match the same fixture host
//     (api.together.xyz) — ensures unambiguous resolution for known hosts
func LoadEmbeddedTemplates() (*Registry, error) {
	return loadTemplatesFromBytes(embeddedTemplatesYAML)
}

func loadTemplatesFromBytes(data []byte) (*Registry, error) {
	var f templateFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("custom: parse templates.yaml: %w", err)
	}
	return newRegistry(f.Templates)
}

func newRegistry(templates []Template) (*Registry, error) {
	byID := make(map[string]*Template, len(templates))
	entries := make([]Template, len(templates))
	copy(entries, templates)

	for i := range entries {
		t := &entries[i]
		if err := validateTemplate(t); err != nil {
			return nil, err
		}
		if _, exists := byID[t.ID]; exists {
			return nil, fmt.Errorf("custom: duplicate template id %q", t.ID)
		}
		byID[t.ID] = t
	}

	// Cross-template validation: for the fixture host "api.together.xyz",
	// only the "together" template should match. Any additional glob that
	// matches it would make resolution ambiguous.
	const fixtureHost = "api.together.xyz"
	var matchCount int
	for _, t := range entries {
		if t.Glob == "" {
			continue
		}
		if ok, _ := path.Match(t.Glob, fixtureHost); ok {
			matchCount++
			if matchCount > 1 {
				return nil, fmt.Errorf("custom: more than one template glob matches %q (ambiguous resolution)", fixtureHost)
			}
		}
	}

	return &Registry{templates: entries, byID: byID}, nil
}

func validateTemplate(t *Template) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("custom: template missing required field 'id'")
	}
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("custom: template %q missing required field 'name'", t.ID)
	}
	if strings.TrimSpace(t.BaseURL) == "" {
		return fmt.Errorf("custom: template %q missing required field 'base_url'", t.ID)
	}
	switch t.AuthScheme {
	case AuthSchemeBearer, AuthSchemeAPIKeyHeader, AuthSchemeCustom, AuthSchemeNone:
	default:
		if t.AuthScheme == "" {
			return fmt.Errorf("custom: template %q missing required field 'auth_scheme'", t.ID)
		}
		return fmt.Errorf("custom: template %q unknown auth_scheme %q", t.ID, t.AuthScheme)
	}
	return nil
}

// Resolve returns the best-matching Template for a given host and optional
// explicit ID. Decision order:
//  1. explicitID non-empty → return template with that ID (or nil if not found)
//  2. glob match on host
//  3. nil
//
// The host argument should be just the hostname (no scheme or path).
func (r *Registry) Resolve(rawURL, explicitID string) *Template {
	if explicitID != "" {
		t, ok := r.byID[explicitID]
		if !ok {
			return nil
		}
		return t
	}
	host := hostFromURL(rawURL)
	if host == "" {
		return nil
	}
	for i := range r.templates {
		t := &r.templates[i]
		if t.Glob == "" {
			continue
		}
		if ok, _ := path.Match(t.Glob, host); ok {
			return t
		}
	}
	return nil
}

// All returns all templates in the registry, in declaration order.
func (r *Registry) All() []Template {
	out := make([]Template, len(r.templates))
	copy(out, r.templates)
	return out
}

// Lookup returns the template with the given id, or nil.
func (r *Registry) Lookup(id string) *Template {
	t, ok := r.byID[id]
	if !ok {
		return nil
	}
	return t
}

// Len returns the number of templates in the registry.
func (r *Registry) Len() int { return len(r.templates) }

// hostFromURL extracts the hostname from a raw URL string. Falls back
// to treating the whole input as a hostname when parsing fails.
func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Add a scheme if missing so url.Parse works.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Summary is the abbreviated template shape returned by ListCustomTemplates
// (WP06 RPC surface). It omits Glob and Notes which are internal details.
type Summary struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	BaseURL    string     `json:"base_url"`
	AuthScheme AuthScheme `json:"auth_scheme"`
}

// Summarize converts a Template to its Summary form.
func Summarize(t Template) Summary {
	return Summary{
		ID:         t.ID,
		Name:       t.Name,
		BaseURL:    t.BaseURL,
		AuthScheme: t.AuthScheme,
	}
}
