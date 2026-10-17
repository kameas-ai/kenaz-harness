package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
)

// fakeKey assembles a key-shaped test value at runtime so secret scanners
// do not flag this file (#413 pattern).
func fakeKey(tag string) string {
	return "sk" + "-" + "test" + "-" + tag + strings.Repeat("x", 24)
}

// uniqueEnv keeps each test's namespace disjoint in the process-wide
// keyring mock.
func uniqueEnv(t *testing.T) string {
	t.Helper()
	return "test" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

func principal(t *testing.T, iss, sub, org string) FleetPrincipal {
	t.Helper()
	p, err := NewFleetPrincipal(iss, sub, org)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFleetCredentialLocator_Shapes(t *testing.T) {
	alice := principal(t, "https://auth.kameas.ai", "alice@example.com", "org-1")
	got, err := FleetCredentialLocator(FleetNamespaceOrg, "prod", alice, "3f0c-aa")
	want := "kenaz-harness/fleet/prod/" + alice.Realm + "/" + alice.Subject + "/org/3f0c-aa"
	if err != nil || got != want {
		t.Fatalf("org locator = %q, %v; want %q", got, err, want)
	}
	if strings.Contains(got, "alice") || strings.Contains(got, "kameas.ai") {
		t.Fatalf("locator carries the subject or issuer in clear: %q", got)
	}
	got, err = FleetCredentialLocator(FleetNamespaceUser, "dev", alice, "abc")
	if err != nil || !strings.HasSuffix(got, "/user/abc") || !strings.HasPrefix(got, "kenaz-harness/fleet/dev/") {
		t.Fatalf("user locator = %q, %v", got, err)
	}
	for _, bad := range []string{"", "_index", "../x", "a/b", "a b", strings.Repeat("a", 129)} {
		if _, err := FleetCredentialLocator(FleetNamespaceOrg, "prod", alice, bad); !errors.Is(err, ErrInvalidCredentialID) {
			t.Errorf("id %q: err = %v, want ErrInvalidCredentialID", bad, err)
		}
	}
	if _, err := FleetCredentialLocator("team", "prod", alice, "a"); !errors.Is(err, ErrInvalidFleetNamespace) {
		t.Errorf("namespace team: err = %v", err)
	}
	if _, err := FleetCredentialLocator(FleetNamespaceOrg, "pr/od", alice, "a"); !errors.Is(err, ErrInvalidEnv) {
		t.Errorf("env with slash: err = %v", err)
	}
	if _, err := FleetCredentialLocator(FleetNamespaceOrg, "prod", FleetPrincipal{}, "a"); !errors.Is(err, ErrNoFleetPrincipal) {
		t.Errorf("no principal: err = %v", err)
	}
	if _, err := NewFleetPrincipal("", "sub", ""); !errors.Is(err, ErrNoFleetPrincipal) {
		t.Errorf("empty issuer accepted: %v", err)
	}
	// Same subject in two realms, two subjects in one realm: all distinct.
	other := principal(t, "https://auth.kameas.ai", "bob@example.com", "org-1")
	realm2 := principal(t, "https://auth.example.org", "alice@example.com", "org-1")
	if alice.sameAccount(other) || alice.sameAccount(realm2) || alice.Realm == realm2.Realm || alice.Subject == realm2.Subject {
		t.Fatal("principals collide across users or realms")
	}
}

// TestFleetCredentialStore_PurgeNeverTouchesLocal drives the real keyring
// seam (core/keyring selects go-keyring's mock under go test) and proves the
// two purges are disjoint and leave local kenaz-harness/<profileID> entries
// alone.
func TestFleetCredentialStore_PurgeNeverTouchesLocal(t *testing.T) {
	if keyring.ActiveBackend() != "mock" {
		t.Fatalf("keyring backend = %q, want mock", keyring.ActiveBackend())
	}
	env := uniqueEnv(t)
	mem := NewMemoryBackend()
	alice := principal(t, "https://auth.kameas.ai", "alice", "org-1")
	store, err := NewFleetCredentialStore(env, alice, mem)
	if err != nil {
		t.Fatal(err)
	}

	localLoc := "kenaz-harness/" + env + "-profile"
	localVal := fakeKey("local")
	if err := keyring.Set(keyringService, localLoc, localVal); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyring.Delete(keyringService, localLoc) })

	orgA, orgB, userA := fakeKey("orga"), fakeKey("orgb"), fakeKey("usera")
	if err := store.Put(FleetNamespaceOrg, "cred-a", []byte(orgA)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(FleetNamespaceOrg, "cred-b", []byte(orgB)); err != nil {
		t.Fatal(err)
	}
	buf := []byte(userA)
	if err := store.Put(FleetNamespaceUser, "cred-u", buf); err != nil {
		t.Fatal(err)
	}
	for _, c := range buf {
		if c != 0 {
			t.Fatal("Put did not zero the plaintext buffer")
		}
	}
	// Re-putting an id does not duplicate it in the index.
	if err := store.Put(FleetNamespaceOrg, "cred-a", []byte(orgA)); err != nil {
		t.Fatal(err)
	}

	// Resolution path: the derived locator resolves through the ordinary
	// keychain backend.
	orgLoc, _ := FleetCredentialLocator(FleetNamespaceOrg, env, alice, "cred-a")
	assertResolves(t, mem, orgLoc, orgA)
	// A hot-cache copy must not survive a purge either.
	mem.SetEntry(ref.RefKeychain, orgLoc, []byte(orgA))

	if ids, _ := store.IDs(FleetNamespaceOrg); strings.Join(ids, ",") != "cred-a,cred-b" {
		t.Fatalf("org ids = %v", ids)
	}

	n, err := store.Purge(FleetNamespaceOrg)
	if err != nil || n != 2 {
		t.Fatalf("Purge(org) = %d, %v; want 2, nil", n, err)
	}
	for _, id := range []string{"cred-a", "cred-b"} {
		loc, _ := FleetCredentialLocator(FleetNamespaceOrg, env, alice, id)
		if _, err := keyring.Get(keyringService, loc); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("%s still in keychain after org purge (err=%v)", id, err)
		}
		if _, err := mem.Resolve(context.Background(), ref.CredentialReference{Kind: ref.RefKeychain, Locator: loc}); err == nil {
			t.Errorf("%s still resolves after org purge", id)
		}
	}
	if ids, _ := store.IDs(FleetNamespaceOrg); len(ids) != 0 {
		t.Errorf("org index not cleared: %v", ids)
	}
	userLoc, _ := FleetCredentialLocator(FleetNamespaceUser, env, alice, "cred-u")
	assertResolves(t, mem, userLoc, userA)
	if v, err := keyring.Get(keyringService, localLoc); err != nil || v != localVal {
		t.Fatalf("local entry changed by org purge: %v", err)
	}

	n, err = store.Purge(FleetNamespaceUser)
	if err != nil || n != 1 {
		t.Fatalf("Purge(user) = %d, %v; want 1, nil", n, err)
	}
	if _, err := keyring.Get(keyringService, userLoc); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("user key survived user purge (err=%v)", err)
	}
	if v, err := keyring.Get(keyringService, localLoc); err != nil || v != localVal {
		t.Fatalf("local entry changed by user purge: %v", err)
	}

	// Purging an empty namespace is a no-op.
	if n, err := store.Purge(FleetNamespaceOrg); err != nil || n != 0 {
		t.Fatalf("second Purge(org) = %d, %v", n, err)
	}
}

func TestFleetCredentialStore_EnvsAreIsolated(t *testing.T) {
	envA, envB := uniqueEnv(t)+"a", uniqueEnv(t)+"b"
	p := principal(t, "https://auth.kameas.ai", "alice", "")
	a, _ := NewFleetCredentialStore(envA, p, nil)
	b, _ := NewFleetCredentialStore(envB, p, nil)
	if err := a.Put(FleetNamespaceOrg, "same", []byte(fakeKey("a"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(FleetNamespaceOrg, "same", []byte(fakeKey("b"))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Purge(FleetNamespaceOrg); err != nil {
		t.Fatal(err)
	}
	locB, _ := FleetCredentialLocator(FleetNamespaceOrg, envB, p, "same")
	if v, err := keyring.Get(keyringService, locB); err != nil || v != fakeKey("b") {
		t.Fatalf("env %s entry touched by env %s purge: %v", envB, envA, err)
	}
	_, _ = b.Purge(FleetNamespaceOrg)
}

func TestFleetCredentialStore_DeleteRemovesOneID(t *testing.T) {
	env := uniqueEnv(t)
	s, _ := NewFleetCredentialStore(env, principal(t, "https://auth.kameas.ai", "alice", ""), nil)
	_ = s.Put(FleetNamespaceOrg, "one", []byte(fakeKey("1")))
	_ = s.Put(FleetNamespaceOrg, "two", []byte(fakeKey("2")))
	if err := s.Delete(FleetNamespaceOrg, "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(FleetNamespaceOrg, "one"); err != nil {
		t.Fatalf("deleting an absent id: %v", err)
	}
	if ids, _ := s.IDs(FleetNamespaceOrg); strings.Join(ids, ",") != "two" {
		t.Fatalf("ids after delete = %v", ids)
	}
	_, _ = s.Purge(FleetNamespaceOrg)
}

func assertResolves(t *testing.T, b *MemoryBackend, loc, want string) {
	t.Helper()
	s, err := b.Resolve(context.Background(), ref.CredentialReference{Kind: ref.RefKeychain, Locator: loc})
	if err != nil {
		t.Fatalf("resolve %s: %v", loc, err)
	}
	defer s.Destroy()
	_ = s.Use(func(v []byte) error {
		if string(v) != want {
			t.Errorf("resolved value mismatch for %s", loc)
		}
		return nil
	})
}

// fleetFixture stores one org key and one synced user key, both with the id
// "same", for each principal, plus a local key.
type fleetFixture struct {
	env      string
	localLoc string
	stores   map[string]*FleetCredentialStore
	people   map[string]FleetPrincipal
}

func newFleetFixture(t *testing.T) *fleetFixture {
	t.Helper()
	f := &fleetFixture{env: uniqueEnv(t), stores: map[string]*FleetCredentialStore{}, people: map[string]FleetPrincipal{
		"alice":        principal(t, "https://auth.kameas.ai", "alice@example.com", "org-1"),
		"bob":          principal(t, "https://auth.kameas.ai", "bob@example.com", "org-1"),
		"alice-realm2": principal(t, "https://auth.example.org", "alice@example.com", "org-1"),
	}}
	f.localLoc = "kenaz-harness/" + f.env + "-local"
	if err := keyring.Set(keyringService, f.localLoc, fakeKey("local")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = keyring.Delete(keyringService, f.localLoc)
		for _, s := range f.stores {
			_, _ = s.Purge(FleetNamespaceOrg)
			_, _ = s.Purge(FleetNamespaceUser)
		}
		_ = writePrincipalsLocked(f.env, nil)
	})
	for name, p := range f.people {
		s, err := NewFleetCredentialStore(f.env, p, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.stores[name] = s
		if err := s.Put(FleetNamespaceOrg, "same", []byte(fakeKey("org-"+name))); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(FleetNamespaceUser, "same", []byte(fakeKey("user-"+name))); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fleetFixture) value(t *testing.T, ns FleetNamespace, who string) (string, bool) {
	t.Helper()
	loc, err := FleetCredentialLocator(ns, f.env, f.people[who], "same")
	if err != nil {
		t.Fatal(err)
	}
	v, err := keyring.Get(keyringService, loc)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return v, true
}

func (f *fleetFixture) assertLocal(t *testing.T) {
	t.Helper()
	if v, err := keyring.Get(keyringService, f.localLoc); err != nil || v != fakeKey("local") {
		t.Fatalf("local key touched: %v", err)
	}
}

// TestFleetCredentials_UsersAndRealmsAreIsolated: the same credential id
// stored by two users in one realm and by one user in two realms lands in
// three distinct entries, each resolving its own value. Before the fix the
// locator was per env only, so bob resolved alice's key.
func TestFleetCredentials_UsersAndRealmsAreIsolated(t *testing.T) {
	f := newFleetFixture(t)
	for who := range f.people {
		for _, ns := range []FleetNamespace{FleetNamespaceOrg, FleetNamespaceUser} {
			v, ok := f.value(t, ns, who)
			if !ok || v != fakeKey(string(ns)+"-"+who) {
				t.Errorf("%s/%s resolves someone else's key", who, ns)
			}
		}
	}
	f.assertLocal(t)
}

// TestFleetCredentials_SignOutPurgesOnlyThatUser.
func TestFleetCredentials_SignOutPurgesOnlyThatUser(t *testing.T) {
	f := newFleetFixture(t)
	n, err := PurgeFleetCredentialsForSignOut(f.env, f.people["alice"], nil)
	if err != nil || n != 2 {
		t.Fatalf("sign-out purge = %d, %v; want 2 (org + user)", n, err)
	}
	for _, ns := range []FleetNamespace{FleetNamespaceOrg, FleetNamespaceUser} {
		if _, ok := f.value(t, ns, "alice"); ok {
			t.Errorf("alice's %s key survived her sign-out", ns)
		}
		for _, other := range []string{"bob", "alice-realm2"} {
			if _, ok := f.value(t, ns, other); !ok {
				t.Errorf("alice's sign-out removed %s's %s key", other, ns)
			}
		}
	}
	f.assertLocal(t)
	ps, _ := readPrincipalsLocked(f.env)
	for _, p := range ps {
		if p.sameAccount(f.people["alice"]) {
			t.Error("alice still recorded after sign-out")
		}
	}
}

// TestFleetCredentials_ReconcileOnSessionChange: a different user signing
// in purges every other recorded account; the same user in a new org loses
// only the old org's delivered keys.
func TestFleetCredentials_ReconcileOnSessionChange(t *testing.T) {
	f := newFleetFixture(t)

	// Same account, same org: nothing to do.
	if n, err := ReconcileFleetCredentialPrincipal(f.env, f.people["alice"], nil); err != nil || n != 4 {
		// bob and alice-realm2 are different accounts: 2 keys each.
		t.Fatalf("reconcile as alice = %d, %v; want 4 (bob's and realm2's keys)", n, err)
	}
	for _, other := range []string{"bob", "alice-realm2"} {
		if _, ok := f.value(t, FleetNamespaceOrg, other); ok {
			t.Errorf("%s's key survived alice signing in", other)
		}
	}
	if _, ok := f.value(t, FleetNamespaceOrg, "alice"); !ok {
		t.Fatal("reconcile removed the signed-in user's own key")
	}

	// Alice moves to org-2: her org-1 delivered key goes, her synced key stays.
	moved := principal(t, "https://auth.kameas.ai", "alice@example.com", "org-2")
	if n, err := ReconcileFleetCredentialPrincipal(f.env, moved, nil); err != nil || n != 1 {
		t.Fatalf("reconcile after org change = %d, %v; want 1", n, err)
	}
	if _, ok := f.value(t, FleetNamespaceOrg, "alice"); ok {
		t.Error("previous org's key survived an org change")
	}
	if _, ok := f.value(t, FleetNamespaceUser, "alice"); !ok {
		t.Error("synced user key removed by an org change")
	}
	f.assertLocal(t)
}
