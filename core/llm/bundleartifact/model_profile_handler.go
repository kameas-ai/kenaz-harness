package bundleartifact

import (
	"context"
	"errors"
	"fmt"

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

// ModelProfileGate is the eval-fit regression gate contract
// (versioned-model-profile-01PMDL04 WP03) Activate consults before
// promoting a candidate ModelProfile. The real implementation is
// *eval.ModelProfileGate (core/eval/model_profile_gate.go), which drives
// core/eval's existing capture->replay->diff->RunMatrix primitives; this
// package only depends on the narrow Check method so tests here can supply
// a fake without pulling in eval fixtures.
//
// Check must return nil to permit promotion and a non-nil, descriptive
// error (naming the offending case) to refuse it. Activate only calls
// Check when the candidate profile actually carries a non-nil
// EvalManifest — a gate implementation does not need to special-case a nil
// manifest itself for the "opt-in, inert by default" guarantee to hold,
// though *eval.ModelProfileGate.Check also treats a nil manifest as pass,
// defense-in-depth.
type ModelProfileGate interface {
	Check(ctx context.Context, p llm.ModelProfile) error
}

// ModelProfileHandler is the bundleartifact ArtifactKindHandler for
// ModelProfile bundles. Its Parse/Validate/Activate shape deliberately
// mirrors Handler (handler.go) — same idiom, different parsed type and
// different backing store.
type ModelProfileHandler struct {
	store *llm.ModelProfileStore
	// gate is optional (nil by default via NewModelProfileHandler): a
	// profile with no EvalManifest activates exactly as it does today
	// whether or not a gate is configured, and a handler with no gate
	// configured never enforces eval-fit regression at all. Only
	// NewModelProfileHandlerWithGate wires one in.
	gate ModelProfileGate
}

// NewModelProfileHandler returns a ModelProfileHandler that activates
// parsed ModelProfile bundles into store, with no eval-fit gate — every
// profile activates exactly as it did before WP03. Use
// NewModelProfileHandlerWithGate to opt into gating.
func NewModelProfileHandler(store *llm.ModelProfileStore) *ModelProfileHandler {
	return &ModelProfileHandler{store: store}
}

// NewModelProfileHandlerWithGate returns a ModelProfileHandler that, in
// addition to NewModelProfileHandler's behavior, refuses to promote a
// candidate ModelProfile whose EvalManifest-referenced suite regresses
// (versioned-model-profile-01PMDL04 WP03). gate may be nil, in which case
// this is equivalent to NewModelProfileHandler — passing a nil gate is not
// an error, since the gate is opt-in by design.
func NewModelProfileHandlerWithGate(store *llm.ModelProfileStore, gate ModelProfileGate) *ModelProfileHandler {
	return &ModelProfileHandler{store: store, gate: gate}
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
//
// When a gate is configured (NewModelProfileHandlerWithGate) and parsed
// carries a non-nil EvalManifest, Activate runs the gate first and refuses
// promotion (the store is left untouched) when the gate reports a
// regression. A profile with no EvalManifest, or a handler with no gate
// configured, skips this check entirely — the gate is opt-in and cannot
// change behavior for profiles that never referenced an eval suite (WP03
// spec requirement: "a profile with no manifest must activate exactly as
// it does today").
func (h *ModelProfileHandler) Activate(ctx context.Context, parsed any) error {
	p, ok := parsed.(llm.ModelProfile)
	if !ok {
		return errors.New("bundleartifact: unexpected parsed type")
	}
	if h.store == nil {
		return errors.New("bundleartifact: no model profile store configured")
	}
	if h.gate != nil && p.EvalManifest != nil {
		if err := h.gate.Check(ctx, p); err != nil {
			return fmt.Errorf("bundleartifact: model profile %q version %q refused promotion: %w", p.ID, p.Version, err)
		}
	}
	return h.store.Load([]llm.ModelProfile{p})
}
