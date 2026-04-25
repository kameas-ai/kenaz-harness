// Package bundleartifact registers the connector's
// kind: llm_provider artifact-kind handler with the bundle-format-resolver
// (plan §6.3 / WP02). The handler parses YAML, validates against the
// schema in core/llm.ValidateProfile, and activates by registering with
// a core/llm.Registry.
//
// The bundle-format-resolver mission is developed separately; the
// ArtifactKindHandler interface here is the connector's stub of that
// upstream contract. When the upstream handler shape lands, this file
// becomes a one-line adapter.
package bundleartifact

import (
	"context"
	"errors"

	"gopkg.in/yaml.v3"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Kind is the artifact kind label registered with the bundle resolver.
const Kind = "llm_provider"

// ArtifactKindHandler matches the upstream bundle-format-resolver
// contract (Parse, Validate, Activate). It is intentionally minimal so
// the upstream wiring change is mechanical when the real interface
// lands.
type ArtifactKindHandler interface {
	Kind() string
	Parse(raw []byte) (any, error)
	Validate(parsed any) error
	Activate(ctx context.Context, parsed any) error
}

// Handler is the connector's ArtifactKindHandler implementation.
type Handler struct {
	registry llm.Registry
}

// NewHandler returns a Handler that activates parsed profiles into r.
func NewHandler(r llm.Registry) *Handler {
	return &Handler{registry: r}
}

// Kind reports the artifact kind label this handler accepts.
func (h *Handler) Kind() string { return Kind }

// Parse decodes raw YAML into a ProviderProfile.
func (h *Handler) Parse(raw []byte) (any, error) {
	var p llm.ProviderProfile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return p, nil
}

// Validate runs the schema rules from core/llm.ValidateProfile.
func (h *Handler) Validate(parsed any) error {
	p, ok := parsed.(llm.ProviderProfile)
	if !ok {
		return errors.New("bundleartifact: unexpected parsed type")
	}
	return llm.ValidateProfile(p)
}

// Activate registers parsed with the connector's Registry.
func (h *Handler) Activate(_ context.Context, parsed any) error {
	p, ok := parsed.(llm.ProviderProfile)
	if !ok {
		return errors.New("bundleartifact: unexpected parsed type")
	}
	if h.registry == nil {
		return errors.New("bundleartifact: no registry configured")
	}
	return h.registry.LoadProfiles([]llm.ProviderProfile{p})
}
