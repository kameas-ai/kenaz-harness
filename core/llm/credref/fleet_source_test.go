package credref

import (
	"context"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func testKey(tag string) string {
	return "sk" + "-" + "test" + "-" + tag + strings.Repeat("y", 24)
}

// TestResolve_FleetSourcesUseTheirNamespace stores one value per namespace
// plus a local key and proves each Source resolves its own entry by
// CredentialID, and that the local path is unchanged.
func TestResolve_FleetSourcesUseTheirNamespace(t *testing.T) {
	t.Setenv("KENAZ_HARNESS_ENV", "dev")
	env := paths.KeychainNamespace()
	mem := secrets.NewMemoryBackend()
	store, err := secrets.NewFleetCredentialStore(env, mem)
	if err != nil {
		t.Fatal(err)
	}
	orgVal, userVal, localVal := testKey("org"), testKey("user"), testKey("local")
	if err := store.Put(secrets.FleetNamespaceOrg, "cred-1", []byte(orgVal)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(secrets.FleetNamespaceUser, "cred-1", []byte(userVal)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.Purge(secrets.FleetNamespaceOrg)
		_, _ = store.Purge(secrets.FleetNamespaceUser)
	})
	mem.SetEntry(secrets.RefKeychain, "kenaz-harness/p-local", []byte(localVal))

	r := New(mem)
	cases := []struct {
		name string
		ref  llm.CredentialReference
		want string
	}{
		{"org", llm.CredentialReference{Kind: "keychain", Source: llm.CredSourceOrg, CredentialID: "cred-1"}, orgVal},
		{"team shares the org namespace", llm.CredentialReference{Kind: "keychain", Source: llm.CredSourceTeam, CredentialID: "cred-1"}, orgVal},
		{"user_sync", llm.CredentialReference{Kind: "keychain", Source: llm.CredSourceUserSync, CredentialID: "cred-1"}, userVal},
		{"locator ignored for fleet", llm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/p-local", Source: llm.CredSourceOrg, CredentialID: "cred-1"}, orgVal},
		{"local zero value", llm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/p-local"}, localVal},
		{"local explicit", llm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/p-local", Source: llm.CredSourceLocal}, localVal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := r.Resolve(context.Background(), "p", tc.ref)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			defer c.Destroy()
			if string(c.Bytes()) != tc.want {
				t.Fatal("resolved the wrong entry")
			}
		})
	}
}

func TestResolve_MalformedFleetReferenceFailsClosed(t *testing.T) {
	r := New(secrets.NewMemoryBackend())
	for name, ref := range map[string]llm.CredentialReference{
		"missing id":     {Kind: "keychain", Source: llm.CredSourceOrg},
		"path in id":     {Kind: "keychain", Source: llm.CredSourceOrg, CredentialID: "../x"},
		"non-keychain":   {Kind: "env", Source: llm.CredSourceOrg, CredentialID: "cred-1"},
		"unknown source": {Kind: "keychain", Source: "partner", CredentialID: "cred-1"},
	} {
		if _, err := r.Resolve(context.Background(), "p", ref); err == nil {
			t.Errorf("%s: resolved, want an error", name)
		}
	}
}
