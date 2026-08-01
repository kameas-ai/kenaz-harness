package bundleartifact

import (
	"context"
	"errors"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// ModelProfileKind is the artifact-kind label for a behavioral
// ModelProfile bundle (versioned-model-profile-01PMDL04 WP02), kept
// deliberately distinct from Kind ("llm_provider") — which stays
// connection/credential (ProviderProfile) config only.
//
// ModelProfileHandler targets its own backing store
// (llm.ModelProfileStore), not registry.Registry, so connection
// profiles (Handler -> registry.Registry.profiles) and behavioral
// profiles (ModelProfileHandler -> llm.ModelProfileStore) resolve
// through entirely independent storage: rotating a credential
// (Handler.Activate) can never touch a ModelProfileStore, and promoting
// a new ModelProfile version (ModelProfileHandler.Activate) can never
// touch registry.Registry's profiles map. Spec §5 requires exactly this
// independence.
const ModelProfileKind = "model_profile"

// ModelProfileArtifactKindHandler matches the same upstream
// bundle-format-resolver contract as ArtifactKindHandler (Parse,
// Validate, Activate); kept as a separate interface name only so this
// file reads standalone — ModelProfileHandler below satisfies both.
type ModelProfileArtifactKindHandler interface {
	Kind() string
	Parse(raw []byte) (any, error)
	Validate(parsed any) error
	Activate(ctx context.Context, parsed any) error
}

// ModelProfileHandler is the bundleartifact ArtifactKindHandler for
// ModelProfile bundles. Its Parse/Validate/Activate shape deliberately
// mirrors Handler (handler.go) — same idiom, different parsed type and
// different backing store.
type ModelProfileHandler struct {
	store *llm.ModelProfileStore
}

// NewModelProfileHandler returns a ModelProfileHandler that activates
// parsed ModelProfile bundles into store.
func NewModelProfileHandler(store *llm.ModelProfileStore) *ModelProfileHandler {
	return &ModelProfileHandler{store: store}
}

// Kind reports the artifact kind label this handler accepts.
func (h *ModelProfileHandler) Kind() string { return ModelProfileKind }

// Parse decodes raw YAML into a ModelProfile.
//
// It routes through llm.ValidateModelProfileBundle rather than a plain
// yaml.Unmarshal: both encoding/json and yaml.v3 silently DROP keys that
// have no counterpart on the target struct, so a bundle carrying
// `cedar:` or `budget:` would parse cleanly and then sail through the
// struct-level ValidateModelProfile — which can only inspect fields that
// survived decoding. Strict unknown-field rejection at the parse
// boundary is what actually enforces the layering tenet that a model
// profile carries behaviour, never governance.
func (h *ModelProfileHandler) Parse(raw []byte) (any, error) {
	p, err := llm.ValidateModelProfileBundle(raw)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Validate runs the schema rules from llm.ValidateModelProfile.
func (h *ModelProfileHandler) Validate(parsed any) error {
	p, ok := parsed.(llm.ModelProfile)
	if !ok {
		return errors.New("bundleartifact: unexpected parsed type")
	}
	return llm.ValidateModelProfile(p)
}

// Activate installs parsed into the handler's ModelProfileStore.
func (h *ModelProfileHandler) Activate(_ context.Context, parsed any) error {
	p, ok := parsed.(llm.ModelProfile)
	if !ok {
		return errors.New("bundleartifact: unexpected parsed type")
	}
	if h.store == nil {
		return errors.New("bundleartifact: no model profile store configured")
	}
	return h.store.Load([]llm.ModelProfile{p})
}
