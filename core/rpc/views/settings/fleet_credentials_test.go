package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func credTestKey(tag string) string { return "sk" + "-" + "test" + "-" + tag + strings.Repeat("w", 24) }

func storeFleetKey(t *testing.T, iss, sub, org, id, val string) (secrets.FleetPrincipal, string) {
	t.Helper()
	p, err := secrets.NewFleetPrincipal(iss, sub, org)
	if err != nil {
		t.Fatal(err)
	}
	s, err := secrets.NewFleetCredentialStore(paths.KeychainNamespace(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(secrets.FleetNamespaceOrg, id, []byte(val)); err != nil {
		t.Fatal(err)
	}
	loc, _ := secrets.FleetCredentialLocator(secrets.FleetNamespaceOrg, paths.KeychainNamespace(), p, id)
	t.Cleanup(func() { _, _ = secrets.PurgeFleetCredentialsForSignOut(paths.KeychainNamespace(), p, nil) })
	return p, loc
}

func present(t *testing.T, loc string) bool {
	t.Helper()
	_, err := keyring.Get("kenaz-harness", loc)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// TestFleetSignOut_PurgesOnlyTheSignedOutAccountsKeys drives the real
// FleetSignOut: alice's Fleet keys go, bob's (same OS account, other Fleet
// user) and a local key stay.
func TestFleetSignOut_PurgesOnlyTheSignedOutAccountsKeys(t *testing.T) {
	r, _ := newRemovalRig(t) // signed in as sub-alice @ https://issuer.test
	_, aliceLoc := storeFleetKey(t, "https://issuer.test", "sub-alice", "zitadel-org-1", "cred-1", credTestKey("alice"))
	_, bobLoc := storeFleetKey(t, "https://issuer.test", "sub-bob", "zitadel-org-1", "cred-1", credTestKey("bob"))
	localLoc := "kenaz-harness/settings-test-local"
	if err := keyring.Set("kenaz-harness", localLoc, credTestKey("local")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyring.Delete("kenaz-harness", localLoc) })

	_ = r.api.FleetSignOut(context.Background())

	if present(t, aliceLoc) {
		t.Error("alice's Fleet key survived her sign-out")
	}
	if !present(t, bobLoc) {
		t.Error("alice's sign-out removed bob's Fleet key")
	}
	if !present(t, localLoc) {
		t.Error("sign-out touched a local key")
	}
}

// TestFleetSessionReset_DifferentAccountPurgesThePrevious: signing in as a
// different user (no sign-out in between) removes the previous account's
// keys; the signed-in account's stay.
func TestFleetSessionReset_DifferentAccountPurgesThePrevious(t *testing.T) {
	r, _ := newRemovalRig(t)
	_, aliceLoc := storeFleetKey(t, "https://issuer.test", "sub-alice", "zitadel-org-1", "cred-2", credTestKey("alice"))
	_, otherRealmLoc := storeFleetKey(t, "https://other-issuer.test", "sub-alice", "zitadel-org-1", "cred-2", credTestKey("realm2"))
	r.setToken(jwtFor("sub-bob", "zitadel-org-1"))
	if _, err := fleet.CredentialPrincipal(); err != nil {
		t.Fatal(err)
	}
	_, bobLoc := storeFleetKey(t, "https://issuer.test", "sub-bob", "zitadel-org-1", "cred-2", credTestKey("bob"))

	ReconcileFleetCredentialsOnSessionReset()

	if present(t, aliceLoc) || present(t, otherRealmLoc) {
		t.Error("a previous account's Fleet key survived another account signing in")
	}
	if !present(t, bobLoc) {
		t.Error("reconcile removed the signed-in account's key")
	}
}

// TestFleetSession_AccountKeyIsOpaqueAndPerAccount: the snapshot names the
// signed-in account by a hashed key (never the subject), a different
// account gets a different key, and signed out there is none.
func TestFleetSession_AccountKeyIsOpaqueAndPerAccount(t *testing.T) {
	r, _ := newRemovalRig(t)
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	alice := snap(t, r.api).AccountKey
	if alice == "" || strings.Contains(alice, "alice") {
		t.Fatalf("accountKey = %q, want an opaque non-empty key", alice)
	}
	r.setToken(jwtFor("sub-bob", "zitadel-org-1"))
	if bob := snap(t, r.api).AccountKey; bob == "" || bob == alice {
		t.Fatalf("bob's accountKey = %q (alice %q)", bob, alice)
	}
	r.setToken("")
	if k := snap(t, r.api).AccountKey; k != "" {
		t.Fatalf("signed out accountKey = %q, want empty", k)
	}
}
