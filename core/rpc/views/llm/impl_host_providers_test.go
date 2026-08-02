package llm

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/personal"
)

// hostProfile is the shape the served harness seeds from its environment:
// an env-backed indirect credential reference, never key bytes.
func hostProfile(id, kind string) corellm.ProviderProfile {
	return corellm.ProviderProfile{
		ID:    id,
		Kind:  kind,
		Model: "test-model",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "TEST_API_KEY"},
	}
}

// newHostAPI builds an API with host profiles plus a real (empty) personal
// store, matching the workbench shape: nothing local, everything from the
// control plane.
func newHostAPI(t *testing.T, hosts ...corellm.ProviderProfile) (*API, *fakeRegistry, *fakeBundles) {
	t.Helper()
	store, err := personal.NewFileStore(filepath.Join(t.TempDir(), "providers.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	reg := &fakeRegistry{}
	bundles := &fakeBundles{}
	api := New(Config{
		Registry:      reg,
		Store:         store,
		Bundles:       bundles,
		HostProviders: hosts,
	})
	return api, reg, bundles
}

func TestListProviders_SurfacesHostProfileWithHostSource(t *testing.T) {
	api, _, _ := newHostAPI(t, hostProfile("kenaz-host", "anthropic"))

	got, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 provider, got %d: %+v", len(got), got)
	}
	if got[0].Source != SourceHost {
		t.Errorf("source = %q, want %q", got[0].Source, SourceHost)
	}
	if got[0].Kind != "anthropic" || got[0].Model != "test-model" {
		t.Errorf("unexpected row: %+v", got[0])
	}
	if got[0].Cred.Kind != "env" || got[0].Cred.Locator != "TEST_API_KEY" {
		t.Errorf("cred = %+v, want the env var NAME", got[0].Cred)
	}
}

// The registry must learn about host profiles, or StartStream would reject
// the very profile ID the UI just told the user to use.
func TestListProviders_LoadsHostProfilesIntoRegistry(t *testing.T) {
	api, reg, _ := newHostAPI(t, hostProfile("kenaz-host", "anthropic"))

	if _, err := api.ListProviders(context.Background()); err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if _, err := reg.Profile("kenaz-host"); err != nil {
		t.Fatalf("host profile never reached the registry: %v", err)
	}
}

// Loading must happen even when there is no personal store at all — the
// pre-existing early return on a nil store used to skip registry loading
// entirely, which would have left a workbench's only provider unresolvable.
func TestListProviders_LoadsHostProfilesWithNoPersonalStore(t *testing.T) {
	reg := &fakeRegistry{}
	api := New(Config{
		Registry:      reg,
		HostProviders: []corellm.ProviderProfile{hostProfile("kenaz-host", "anthropic")},
	})
	if _, err := api.ListProviders(context.Background()); err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if _, err := reg.Profile("kenaz-host"); err != nil {
		t.Fatalf("host profile never reached the registry: %v", err)
	}
}

// A signed bundle still outranks a host grant on an ID collision.
func TestListProviders_BundleWinsOverHost(t *testing.T) {
	api, _, bundles := newHostAPI(t, hostProfile("shared-id", "anthropic"))
	bundles.profiles = []corellm.ProviderProfile{{
		ID:    "shared-id",
		Kind:  "openai",
		Model: "bundle-model",
		Cred:  corellm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/shared-id"},
	}}

	got, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 provider, got %d", len(got))
	}
	if got[0].Source != "bundle" || got[0].Model != "bundle-model" {
		t.Fatalf("bundle did not win the collision: %+v", got[0])
	}
}

// A host grant outranks whatever happens to sit in providers.json.
func TestListProviders_HostWinsOverPersonal(t *testing.T) {
	store, err := personal.NewFileStore(filepath.Join(t.TempDir(), "providers.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Add(corellm.ProviderProfile{
		ID:    "shared-id",
		Kind:  "openai",
		Model: "personal-model",
		Cred:  corellm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/shared-id"},
	}); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	api := New(Config{
		Registry:      &fakeRegistry{},
		Store:         store,
		HostProviders: []corellm.ProviderProfile{hostProfile("shared-id", "anthropic")},
	})

	got, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 provider, got %d: %+v", len(got), got)
	}
	if got[0].Source != SourceHost || got[0].Model != "test-model" {
		t.Fatalf("host did not win the collision: %+v", got[0])
	}
}

// Every mutation path must refuse a host provider with the SAME named error,
// so the UI can render one consistent read-only explanation.
func TestHostProvider_MutationsRejected(t *testing.T) {
	api, _, _ := newHostAPI(t, hostProfile("kenaz-host", "anthropic"))
	ctx := context.Background()
	in := AddProviderInput{
		ID:    "kenaz-host",
		Kind:  "anthropic",
		Model: "test-model",
		Cred:  CredentialReference{Kind: "keychain", Locator: "kenaz-harness/kenaz-host"},
	}

	cases := map[string]error{
		"AddProvider":              api.AddProvider(ctx, in),
		"UpdateProvider":           api.UpdateProvider(ctx, in),
		"RemoveProvider":           api.RemoveProvider(ctx, "kenaz-host"),
		"UpdateProviderCredential": api.UpdateProviderCredential(ctx, "kenaz-host", "sk-whatever"),
	}
	for name, err := range cases {
		if !errors.Is(err, ErrHostProviderImmutable) {
			t.Errorf("%s: want ErrHostProviderImmutable, got %v", name, err)
		}
	}
}

// Personal providers must stay fully editable alongside a host provider.
func TestHostProvider_DoesNotBlockPersonalMutations(t *testing.T) {
	api, _, _ := newHostAPI(t, hostProfile("kenaz-host", "anthropic"))
	api.keychain = newFakeKeychain()

	err := api.AddProvider(context.Background(), AddProviderInput{
		ID:              "my-own",
		Kind:            "anthropic",
		Model:           "claude-sonnet",
		Cred:            CredentialReference{Kind: "keychain", Locator: "kenaz-harness/my-own"},
		PlaintextAPIKey: "sk-secret",
	})
	if err != nil {
		t.Fatalf("AddProvider on a personal id: %v", err)
	}
}

// The desktop default: no HostProviders → nothing changes. This is the
// regression guard for "an ambient ANTHROPIC_API_KEY must never conjure a
// provider row on a developer's machine".
func TestNoHostProviders_ListIsUnchanged(t *testing.T) {
	api, reg, _ := newHostAPI(t)

	got, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want an empty list, got %+v", got)
	}
	if len(reg.loaded) != 0 {
		t.Fatalf("registry received profiles it should not have: %+v", reg.loaded)
	}
}
