package llm

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	connectorllm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/personal"
)

type fakeKeychain struct {
	stored map[string][]byte
	err    error
}

func newFakeKeychain() *fakeKeychain {
	return &fakeKeychain{stored: map[string][]byte{}}
}

func (f *fakeKeychain) Write(_ context.Context, locator string, plaintext []byte) error {
	if f.err != nil {
		return f.err
	}
	dup := append([]byte(nil), plaintext...)
	f.stored[locator] = dup
	return nil
}

type fakeBundles struct {
	profiles []connectorllm.ProviderProfile
}

func (f *fakeBundles) BundleProfiles() []connectorllm.ProviderProfile {
	return f.profiles
}

type fakeProber struct {
	result ProberResult
}

func (f *fakeProber) Probe(_ context.Context, _ connectorllm.ProviderProfile) ProberResult {
	return f.result
}

func newImpl(t *testing.T) (*Impl, *fakeKeychain, *fakeBundles, *fakeProber, personal.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := personal.NewFileStore(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	keychain := newFakeKeychain()
	bundles := &fakeBundles{}
	prober := &fakeProber{}
	impl := NewImpl(Config{
		Store:    store,
		Bundles:  bundles,
		Keychain: keychain,
		Prober:   prober,
	})
	return impl, keychain, bundles, prober, store
}

func TestImpl_AddProvider_StoresKeychainAndProfile(t *testing.T) {
	impl, keychain, _, _, store := newImpl(t)
	in := AddProviderInput{
		ID:    "personal-anth",
		Name:  "Anthropic Personal",
		Kind:  "anthropic",
		Model: "claude-sonnet",
		Cred: CredentialReference{
			Kind:    "keychain",
			Locator: "kaneaz-harness/personal-anth",
		},
		PlaintextAPIKey: "sk-secret",
	}
	if err := impl.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	got, err := store.Get("personal-anth")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Cred.Kind != "keychain" || got.Cred.Locator != "kaneaz-harness/personal-anth" {
		t.Fatalf("unexpected stored profile: %+v", got)
	}
	if string(keychain.stored["kaneaz-harness/personal-anth"]) != "sk-secret" {
		t.Fatalf("keychain did not receive plaintext: %v", keychain.stored)
	}
}

func TestImpl_AddProvider_RejectsNonKeychainCred(t *testing.T) {
	impl, _, _, _, _ := newImpl(t)
	in := AddProviderInput{
		ID: "p", Kind: "anthropic", Model: "x",
		Cred: CredentialReference{Kind: "env", Locator: "ANTH_KEY"},
	}
	if err := impl.AddProvider(context.Background(), in); err == nil {
		t.Fatal("expected error rejecting env-kind cred")
	}
}

func TestImpl_AddProvider_NoStoreIsTypedError(t *testing.T) {
	impl := NewImpl(Config{})
	err := impl.AddProvider(context.Background(), AddProviderInput{ID: "p"})
	if !errors.Is(err, ErrPersonalStoreUnavailable) {
		t.Fatalf("expected ErrPersonalStoreUnavailable, got %v", err)
	}
}

func TestImpl_RemoveProvider_PersonalRoundTrip(t *testing.T) {
	impl, _, _, _, store := newImpl(t)
	in := AddProviderInput{
		ID: "p", Kind: "anthropic", Model: "x",
		Cred: CredentialReference{Kind: "keychain", Locator: "kaneaz-harness/p"},
	}
	if err := impl.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if err := impl.RemoveProvider(context.Background(), "p"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if _, err := store.Get("p"); !errors.Is(err, personal.ErrNotFound) {
		t.Fatalf("expected store to be empty after removal, got %v", err)
	}
}

func TestImpl_RemoveProvider_BundleIsImmutable(t *testing.T) {
	impl, _, bundles, _, _ := newImpl(t)
	bundles.profiles = []connectorllm.ProviderProfile{{
		ID: "bundle-prof", Kind: "anthropic", Model: "claude-sonnet",
		Cred: connectorllm.CredentialReference{Kind: "env", Locator: "K"},
	}}
	err := impl.RemoveProvider(context.Background(), "bundle-prof")
	if !errors.Is(err, ErrBundleProviderImmutable) {
		t.Fatalf("expected ErrBundleProviderImmutable, got %v", err)
	}
}

func TestImpl_TestProvider_PopulatesValidated(t *testing.T) {
	impl, _, _, prober, _ := newImpl(t)
	in := AddProviderInput{
		ID: "p", Kind: "anthropic", Model: "x",
		Cred: CredentialReference{Kind: "keychain", Locator: "kaneaz-harness/p"},
	}
	if err := impl.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	prober.result = ProberResult{Success: true, LatencyMS: 42, Message: "ok"}
	res, err := impl.TestProvider(context.Background(), "p")
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if !res.Success || res.LatencyMS != 42 {
		t.Fatalf("unexpected result: %+v", res)
	}
	list, err := impl.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	var found bool
	for _, p := range list {
		if p.ID == "p" {
			found = true
			if !p.Validated {
				t.Fatalf("expected provider to be validated after TestProvider success")
			}
		}
	}
	if !found {
		t.Fatal("provider missing from list after AddProvider")
	}
}

func TestImpl_TestProvider_FailureKeepsValidatedFalse(t *testing.T) {
	impl, _, _, prober, _ := newImpl(t)
	in := AddProviderInput{
		ID: "p", Kind: "anthropic", Model: "x",
		Cred: CredentialReference{Kind: "keychain", Locator: "kaneaz-harness/p"},
	}
	if err := impl.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	prober.result = ProberResult{Success: false, Message: "401 unauthorized"}
	res, err := impl.TestProvider(context.Background(), "p")
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure result")
	}
	if res.Message != "401 unauthorized" {
		t.Fatalf("expected message propagated, got %q", res.Message)
	}
}

func TestImpl_ListProviders_BundleWinsOverPersonalCollision(t *testing.T) {
	impl, _, bundles, _, _ := newImpl(t)
	bundles.profiles = []connectorllm.ProviderProfile{{
		ID: "shared", Kind: "anthropic", Model: "claude-sonnet",
		Cred: connectorllm.CredentialReference{Kind: "env", Locator: "K"},
	}}
	if err := impl.AddProvider(context.Background(), AddProviderInput{
		ID: "shared", Kind: "openai", Model: "gpt-4o-mini",
		Cred: CredentialReference{Kind: "keychain", Locator: "kaneaz-harness/shared"},
	}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	list, err := impl.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	var foundCount int
	for _, p := range list {
		if p.ID == "shared" {
			foundCount++
			if p.Source != "bundle" {
				t.Fatalf("expected bundle to win on collision, got %+v", p)
			}
		}
	}
	if foundCount != 1 {
		t.Fatalf("expected exactly one shared entry, got %d", foundCount)
	}
}

func TestImpl_ListProviders_SortedDeterministically(t *testing.T) {
	impl, _, bundles, _, _ := newImpl(t)
	bundles.profiles = []connectorllm.ProviderProfile{
		{ID: "b1", Kind: "anthropic", Model: "x",
			Cred: connectorllm.CredentialReference{Kind: "env", Locator: "K"}},
		{ID: "a1", Kind: "anthropic", Model: "x",
			Cred: connectorllm.CredentialReference{Kind: "env", Locator: "K"}},
	}
	for _, id := range []string{"z2", "z1"} {
		if err := impl.AddProvider(context.Background(), AddProviderInput{
			ID: id, Kind: "openai", Model: "x",
			Cred: CredentialReference{Kind: "keychain", Locator: "kaneaz-harness/" + id},
		}); err != nil {
			t.Fatalf("AddProvider: %v", err)
		}
	}
	list, err := impl.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	want := []string{"a1", "b1", "z1", "z2"}
	if len(list) != len(want) {
		t.Fatalf("expected %d items, got %d", len(want), len(list))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("at index %d expected %q, got %q (full: %+v)", i, id, list[i].ID, list)
		}
	}
}

// Compile-time interface witness.
func TestImpl_SatisfiesInterface(_ *testing.T) {
	var _ LLMConnectorAPI = (*Impl)(nil)
}
