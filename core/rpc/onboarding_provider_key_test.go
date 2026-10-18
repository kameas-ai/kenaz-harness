package rpc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/personal"
	coreonboarding "github.com/kameas-ai/kenaz-harness/core/onboarding"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	onboardingview "github.com/kameas-ai/kenaz-harness/core/rpc/views/onboarding"
)

// Finding #107 (P0): onboarding's provider-connection step was a double lie —
// the connection test never ran (the FSM was always built with a nil
// LLMTester) and a successfully-tested key was discarded (no caller ever
// stored it). These tests drive the real production wiring seam
// (onboardingview.New(Config{Tester: ..., ProviderStore: ...})) with the two
// adapters this file's sibling, onboarding_wiring.go, defines, backed by a
// real llmview.API + a real personal.FileStore (not a bypassed fixture — see
// CLAUDE.md's "test fixtures that bypass the layer under test" blind spot)
// and a fake provider adapter standing in for the network call.

// fakeOnboardingAdapter is a minimal corellm.ProviderAdapter that also
// implements corellm.ModelLister (the capability onboardingLLMTesterAdapter
// actually drives — see that type's doc comment for why TestProviderKey is
// NOT the reused path). It simulates a real provider's key-validation
// behaviour without any network call: a designated "good" key succeeds, any
// other key fails exactly like a live 401 would.
type fakeOnboardingAdapter struct {
	kind    string
	goodKey string
}

func (f *fakeOnboardingAdapter) Kind() string { return f.kind }
func (f *fakeOnboardingAdapter) Capabilities(_ string) corellm.CapabilityDescriptor {
	return corellm.CapabilityDescriptor{}
}
func (f *fakeOnboardingAdapter) Stream(_ context.Context, _ corellm.GenerationRequest, _ corellm.ProviderProfile, _ []byte) (corellm.Stream, error) {
	return nil, errors.New("fakeOnboardingAdapter: Stream not implemented")
}

// ListModels implements corellm.ModelLister. This is the exact capability
// onboardingLLMTesterAdapter.TestProvider and AddProviderForm.vue's own
// pre-submit probe both drive.
func (f *fakeOnboardingAdapter) ListModels(_ context.Context, cred []byte) ([]corellm.ModelInfo, error) {
	if string(cred) != f.goodKey {
		return nil, errors.New("401 Unauthorized: invalid API key")
	}
	return []corellm.ModelInfo{{ID: "fake-model-1", DisplayName: "Fake Model 1"}}, nil
}

// fakeProviderKeyRegistry is a minimal corellm.Registry that also satisfies
// llmview.AdapterLookup, matching the *registry.Registry.Adapter contract
// AddProvider/ListModels rely on. LoadProfiles/Evict/Profile/PreflightAll/
// Stream are best-effort no-ops — this test asserts persistence through
// personal.FileStore + ListProviders, not through the registry.
type fakeProviderKeyRegistry struct {
	adapters map[string]corellm.ProviderAdapter
}

func newFakeProviderKeyRegistry() *fakeProviderKeyRegistry {
	return &fakeProviderKeyRegistry{adapters: map[string]corellm.ProviderAdapter{}}
}

func (r *fakeProviderKeyRegistry) RegisterAdapter(a corellm.ProviderAdapter) {
	r.adapters[a.Kind()] = a
}
func (r *fakeProviderKeyRegistry) Adapter(kind string) corellm.ProviderAdapter {
	return r.adapters[kind]
}
func (r *fakeProviderKeyRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *fakeProviderKeyRegistry) Evict(_ string) error                           { return nil }
func (r *fakeProviderKeyRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, errors.New("not found")
}
func (r *fakeProviderKeyRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult {
	return nil
}
func (r *fakeProviderKeyRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return nil, errors.New("fakeProviderKeyRegistry: Stream not implemented")
}

// fakeProviderKeyKeychain records writes without touching a real OS keychain.
type fakeProviderKeyKeychain struct {
	stored map[string][]byte
}

func (f *fakeProviderKeyKeychain) Write(_ context.Context, locator string, plaintext []byte) error {
	if f.stored == nil {
		f.stored = map[string][]byte{}
	}
	f.stored[locator] = append([]byte(nil), plaintext...)
	return nil
}

// fakeProviderKeyCompletion is a minimal in-memory onboardingview.CompletionMarker.
type fakeProviderKeyCompletion struct {
	completed bool
}

func (f *fakeProviderKeyCompletion) MarkOnboardingCompleted(_ context.Context) error {
	f.completed = true
	return nil
}
func (f *fakeProviderKeyCompletion) IsCompleted(_ context.Context) (bool, error) {
	return f.completed, nil
}

// newProviderKeyWiringFixture builds a real llmview.API (backed by a real
// personal.FileStore under a temp dir) plus the two production adapters
// under test, and wires them into a real onboardingview.API exactly the way
// core/rpc/api.go does. goodKey is the plaintext value the fake adapter
// treats as valid.
func newProviderKeyWiringFixture(t *testing.T, goodKey string) (onboardingAPI onboardingview.OnboardingAPI, llmAPI llmview.LLMConnectorAPI) {
	t.Helper()
	dir := t.TempDir()
	store, err := personal.NewFileStore(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("personal.NewFileStore: %v", err)
	}
	reg := newFakeProviderKeyRegistry()
	reg.RegisterAdapter(&fakeOnboardingAdapter{kind: "anthropic", goodKey: goodKey})

	llmAPI = llmview.New(llmview.Config{
		Registry: reg,
		Store:    store,
		Keychain: &fakeProviderKeyKeychain{},
	})

	onboardingAPI = onboardingview.New(onboardingview.Config{
		Tester:        onboardingLLMTesterAdapter{llmAPI: llmAPI},
		ProviderStore: onboardingProviderStoreAdapter{llmAPI: llmAPI},
		Completion:    &fakeProviderKeyCompletion{},
	})
	return onboardingAPI, llmAPI
}

// driveProviderKeyToEnterAPIKey walks the FSM from its initial state through
// pick-provider-kind, choosing anthropic, and returns the state string
// ("enter_api_key") the caller should now submit a key against.
func driveProviderKeyToEnterAPIKey(t *testing.T, api onboardingview.OnboardingAPI) string {
	t.Helper()
	ctx := context.Background()
	begin, err := api.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	r, err := api.Step(ctx, onboardingview.StepRequest{
		State: begin.State,
		Event: string(coreonboarding.EventNext),
	})
	if err != nil {
		t.Fatalf("Step(welcome/next): %v", err)
	}
	r, err = api.Step(ctx, onboardingview.StepRequest{
		State: r.State,
		Event: "anthropic",
	})
	if err != nil {
		t.Fatalf("Step(pick/anthropic): %v", err)
	}
	if r.State != string(coreonboarding.StateEnterAPIKey) {
		t.Fatalf("state = %q, want %q", r.State, coreonboarding.StateEnterAPIKey)
	}
	return r.State
}

// TestProviderKeyWiring_WrongKey_DoesNotAdvanceAndSurfacesError proves the
// first half of finding #107: a bad key must actually fail the connection
// test end-to-end through the real production wiring seam.
//
// Mutation: revert core/rpc/views/onboarding/impl.go's New() to
// `coreonboarding.NewFull(nil, nil, cfg.Signer)` (the pre-fix hardcoded nil
// tester) and this test fails — the bad key would report success and the
// flow would advance to account_step. (Verified by hand during development;
// see the PR description / commit message for the transcript.)
func TestProviderKeyWiring_WrongKey_DoesNotAdvanceAndSurfacesError(t *testing.T) {
	api, llmAPI := newProviderKeyWiringFixture(t, "sk-ant-good")
	enterState := driveProviderKeyToEnterAPIKey(t, api)

	r, err := api.Step(context.Background(), onboardingview.StepRequest{
		State:   enterState,
		Event:   string(coreonboarding.EventSubmitKey),
		Payload: map[string]string{"api_key": "sk-ant-totally-bogus"},
	})
	if err != nil {
		t.Fatalf("Step(submit bad key): %v", err)
	}
	if r.State != string(coreonboarding.StateEnterAPIKey) {
		t.Errorf("state = %q, want %q (a bad key must not advance past the provider step)", r.State, coreonboarding.StateEnterAPIKey)
	}
	if r.Card.ErrorMessage == "" {
		t.Error("card.ErrorMessage is empty; a bad key must surface a visible error")
	}

	providers, err := llmAPI.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("ListProviders returned %d providers, want 0 (a rejected key must not be persisted)", len(providers))
	}
}

// TestProviderKeyWiring_ValidKey_PersistsAndIsListable proves the second
// half of finding #107: a key that passes the connection test must actually
// become a usable, listed provider — asserted via the real
// llmview.LLMConnectorAPI.ListProviders read path, not via FSM state.
//
// Mutation: drop the ProviderStore field from core/rpc/api.go's onboarding
// Config literal (or from Config's plumbing in impl.go's New()) and this
// test fails — ListProviders stays empty because the tested key was
// discarded, exactly like the original finding #107 bug. (Verified by hand
// during development.)
func TestProviderKeyWiring_ValidKey_PersistsAndIsListable(t *testing.T) {
	const goodKey = "sk-ant-good-key"
	api, llmAPI := newProviderKeyWiringFixture(t, goodKey)
	enterState := driveProviderKeyToEnterAPIKey(t, api)

	r, err := api.Step(context.Background(), onboardingview.StepRequest{
		State:   enterState,
		Event:   string(coreonboarding.EventSubmitKey),
		Payload: map[string]string{"api_key": goodKey},
	})
	if err != nil {
		t.Fatalf("Step(submit good key): %v", err)
	}
	if r.State != string(coreonboarding.StateAccountStep) {
		t.Fatalf("state = %q, want %q", r.State, coreonboarding.StateAccountStep)
	}

	providers, err := llmAPI.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("ListProviders returned %d providers, want 1", len(providers))
	}
	if providers[0].Kind != "anthropic" {
		t.Errorf("provider.Kind = %q, want %q", providers[0].Kind, "anthropic")
	}
	if providers[0].Model == "" {
		t.Error("provider.Model is empty; a default model must be assigned")
	}
}

// TestProviderKeyWiring_ColdStart_ZeroProvidersStillBoots is the
// no-regression proof (task requirement #3): a user who never submits a
// key (or dismisses onboarding outright) must still get a working, empty-
// state boot — the Tester/ProviderStore wiring must not be invoked, and
// must not be required, on that path.
func TestProviderKeyWiring_ColdStart_ZeroProvidersStillBoots(t *testing.T) {
	api, llmAPI := newProviderKeyWiringFixture(t, "sk-ant-good")
	ctx := context.Background()

	if _, err := api.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := api.Dismiss(ctx); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	providers, err := llmAPI.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("ListProviders returned %d providers, want 0 on a skipped/dismissed flow", len(providers))
	}

	state, err := api.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !state.Completed {
		t.Error("state.Completed = false after Dismiss, want true")
	}
}
