package credref

import (
	"context"
	"errors"
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
	alice, err := secrets.NewFleetPrincipal("https://auth.kameas.ai", "alice", "org-1")
	if err != nil {
		t.Fatal(err)
	}
	store, err := secrets.NewFleetCredentialStore(env, alice, mem)
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

	r := New(mem).WithFleetPrincipal(func() (secrets.FleetPrincipal, error) { return alice, nil })
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

// TestResolve_FleetKeyIsScopedToTheSignedInUser: another Fleet account on
// the same OS account, or nobody signed in, cannot resolve alice's key.
func TestResolve_FleetKeyIsScopedToTheSignedInUser(t *testing.T) {
	t.Setenv("KENAZ_HARNESS_ENV", "dev")
	env := paths.KeychainNamespace()
	alice, _ := secrets.NewFleetPrincipal("https://auth.kameas.ai", "alice", "org-1")
	bob, _ := secrets.NewFleetPrincipal("https://auth.kameas.ai", "bob", "org-1")
	store, _ := secrets.NewFleetCredentialStore(env, alice, nil)
	if err := store.Put(secrets.FleetNamespaceOrg, "cred-scope", []byte(testKey("alice"))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = secrets.PurgeFleetCredentialsForSignOut(env, alice, nil) })
	ref := llm.CredentialReference{Kind: "keychain", Source: llm.CredSourceOrg, CredentialID: "cred-scope"}

	asBob := New(secrets.NewMemoryBackend()).WithFleetPrincipal(func() (secrets.FleetPrincipal, error) { return bob, nil })
	if _, err := asBob.Resolve(context.Background(), "p", ref); err == nil {
		t.Fatal("bob resolved alice's org key")
	}
	signedOut := New(secrets.NewMemoryBackend())
	if _, err := signedOut.Resolve(context.Background(), "p", ref); !errors.Is(err, secrets.ErrNoFleetPrincipal) {
		t.Fatalf("no principal: err = %v, want ErrNoFleetPrincipal", err)
	}
	asAlice := New(secrets.NewMemoryBackend()).WithFleetPrincipal(func() (secrets.FleetPrincipal, error) { return alice, nil })
	c, err := asAlice.Resolve(context.Background(), "p", ref)
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	c.Destroy()
}
