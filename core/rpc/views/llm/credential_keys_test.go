package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
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
		if !k.UserSelectable {
			t.Errorf("%s: not selectable, but no policy restricts selection yet", k.ID)
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

func TestAddProvider_FleetBoundProfileKeepsBindingAndRefusesPlaintext(t *testing.T) {
	api, keychain, _, _, store := newProvidersAPI(t)
	fleetCred := CredentialReference{Kind: "keychain", Source: "org", CredentialID: "c-org"}
	in := AddProviderInput{ID: "org-openai", Kind: "openai", Model: "gpt-4o", Cred: fleetCred}
	if err := api.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	got, err := store.Get("org-openai")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Cred.Source) != "org" || got.Cred.CredentialID != "c-org" {
		t.Fatalf("binding dropped: %+v", got.Cred)
	}
	in.PlaintextAPIKey = "sk" + "-" + "x" + strings.Repeat("z", 24)
	if err := api.UpdateProvider(context.Background(), in); !errors.Is(err, ErrFleetKeyReadOnly) {
		t.Fatalf("UpdateProvider with plaintext on a Fleet key: err = %v", err)
	}
	if len(keychain.stored) != 0 {
		t.Fatalf("plaintext reached the keychain writer: %d entries", len(keychain.stored))
	}
}
