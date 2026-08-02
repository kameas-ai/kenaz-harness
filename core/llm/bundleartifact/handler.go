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

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
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
//
// Routes through llm.ValidateProviderProfileBundle rather than a plain
// yaml.Unmarshal (WP08, versioned-model-profile-01PMDL04): a foreign
// top-level key is rejected instead of silently dropped, and — the live
// vector this closes — a key placed inside Defaults (a map[string]any,
// so unlike every other ProviderProfile field it is NOT dropped by
// tolerant-of-unknown-fields unmarshal) is checked against the explicit
// allow-list of sampling knobs and provider-specific config keys
// adapters actually read. Mirrors ModelProfileHandler.Parse's use of
// llm.ValidateModelProfileBundle (model_profile_handler.go) for the
// same reason: unknown keys must be rejected at the parse boundary,
// before they are silently dropped or, for Defaults, silently kept.
func (h *Handler) Parse(raw []byte) (any, error) {
	p, err := llm.ValidateProviderProfileBundle(raw)
	if err != nil {
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
