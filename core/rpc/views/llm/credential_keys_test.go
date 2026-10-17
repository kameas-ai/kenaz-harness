package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	connectorllm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func TestCredentialKeys_LabelsScopeAndCarriesNoLocator(t *testing.T) {
	provs := []Provider{
		{ID: "mine", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine"}},
		{ID: "org-a", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: "org", CredentialID: "c-org"}},
		{ID: "team-a", Kind: "anthropic", Cred: CredentialReference{Kind: "keychain", Source: "team", CredentialID: "c-team"}},
	}
	keys := CredentialKeys(provs)
	if len(keys) != 3 {
		t.Fatalf("got %d keys: %+v", len(keys), keys)
	}
	want := map[string]string{"local:mine": "local", "c-org": "org", "c-team": "team"}
	for _, k := range keys {
		if want[k.ID] != k.Source {
			t.Errorf("%s: source %q, want %q", k.ID, k.Source, want[k.ID])
		}
		if k.UserSelectable {
			t.Errorf("%s: selectable with no credential policy (must fail closed)", k.ID)
		}
	}
	b, _ := json.Marshal(keys)
	if strings.Contains(string(b), "kenaz-harness/") || strings.Contains(string(b), "locator") {
		t.Fatalf("key list leaks a locator: %s", b)
	}
}

func TestCredConversion_RoundTripsFleetBinding(t *testing.T) {
	in := CredentialReference{Kind: "keychain", Source: "user_sync", CredentialID: "c-1"}
	if got := fromCoreCred(toCoreCred(in)); got != in {
		t.Fatalf("round trip: got %+v want %+v", got, in)
	}
}

// TestProviderForm_RefusesClientFleetBinding: only the Fleet delivery
// paths (WP05/WP06) may bind a profile to a Fleet key. Before the fix,
// AddProvider accepted {source: org, credentialId: X} from any caller and
// only refused when a plaintext key came with it.
func TestProviderForm_RefusesClientFleetBinding(t *testing.T) {
	api, keychain, _, _, store := newProvidersAPI(t)
	ctx := context.Background()
	for name, cred := range map[string]CredentialReference{
		"org source":         {Kind: "keychain", Source: "org", CredentialID: "c-org"},
		"team source":        {Kind: "keychain", Source: "team", CredentialID: "c-team"},
		"user_sync source":   {Kind: "keychain", Source: "user_sync", CredentialID: "c-u"},
		"id on local source": {Kind: "keychain", Locator: "kenaz-harness/x", CredentialID: "c-org"},
	} {
		in := AddProviderInput{ID: "p-" + strings.ReplaceAll(name, " ", "-"), Kind: "openai", Model: "gpt-4o", Cred: cred}
		if err := api.AddProvider(ctx, in); !errors.Is(err, ErrFleetKeyReadOnly) {
			t.Errorf("AddProvider(%s): err = %v, want ErrFleetKeyReadOnly", name, err)
		}
		if err := api.UpdateProvider(ctx, in); !errors.Is(err, ErrFleetKeyReadOnly) {
			t.Errorf("UpdateProvider(%s): err = %v, want ErrFleetKeyReadOnly", name, err)
		}
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Fatalf("a client-supplied Fleet binding was persisted: %+v", list)
	}

	// A local key is still accepted.
	local := AddProviderInput{ID: "mine", Kind: "openai", Model: "gpt-4o",
		Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine", Source: "local"}}
	if err := api.AddProvider(ctx, local); err != nil {
		t.Fatalf("local AddProvider: %v", err)
	}

	// A profile Fleet bound (written below the RPC layer, as WP05 will) can
	// not have its key replaced from the form or the rotate flow.
	if err := store.Add(connectorllm.ProviderProfile{ID: "org-openai", Kind: "openai", Model: "gpt-4o",
		Cred: connectorllm.CredentialReference{Kind: "keychain", Source: connectorllm.CredSourceOrg, CredentialID: "c-org"}}); err != nil {
		t.Fatal(err)
	}
	key := "sk" + "-" + "x" + strings.Repeat("z", 24)
	if err := api.UpdateProviderCredential(ctx, "org-openai", key); !errors.Is(err, ErrFleetKeyReadOnly) {
		t.Errorf("UpdateProviderCredential on a Fleet key: err = %v", err)
	}
	if _, err := api.TestAndRotateKey(ctx, "org-openai", key, "settings"); !errors.Is(err, ErrFleetKeyReadOnly) {
		t.Errorf("TestAndRotateKey on a Fleet key: err = %v", err)
	}
	if len(keychain.stored) != 0 {
		t.Fatalf("plaintext reached the keychain writer: %d entries", len(keychain.stored))
	}
}
