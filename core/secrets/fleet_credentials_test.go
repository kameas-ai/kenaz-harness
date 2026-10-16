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

func TestFleetCredentialLocator_Shapes(t *testing.T) {
	got, err := FleetCredentialLocator(FleetNamespaceOrg, "prod", "3f0c-aa")
	if err != nil || got != "kenaz-harness/fleet/prod/org/3f0c-aa" {
		t.Fatalf("org locator = %q, %v", got, err)
	}
	got, err = FleetCredentialLocator(FleetNamespaceUser, "dev", "abc")
	if err != nil || got != "kenaz-harness/fleet/dev/user/abc" {
		t.Fatalf("user locator = %q, %v", got, err)
	}
	for _, bad := range []string{"", "_index", "../x", "a/b", "a b", strings.Repeat("a", 129)} {
		if _, err := FleetCredentialLocator(FleetNamespaceOrg, "prod", bad); !errors.Is(err, ErrInvalidCredentialID) {
			t.Errorf("id %q: err = %v, want ErrInvalidCredentialID", bad, err)
		}
	}
	if _, err := FleetCredentialLocator("team", "prod", "a"); !errors.Is(err, ErrInvalidFleetNamespace) {
		t.Errorf("namespace team: err = %v", err)
	}
	if _, err := FleetCredentialLocator(FleetNamespaceOrg, "pr/od", "a"); !errors.Is(err, ErrInvalidEnv) {
		t.Errorf("env with slash: err = %v", err)
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
	store, err := NewFleetCredentialStore(env, mem)
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
	orgLoc, _ := FleetCredentialLocator(FleetNamespaceOrg, env, "cred-a")
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
		loc, _ := FleetCredentialLocator(FleetNamespaceOrg, env, id)
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
	userLoc, _ := FleetCredentialLocator(FleetNamespaceUser, env, "cred-u")
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
	a, _ := NewFleetCredentialStore(envA, nil)
	b, _ := NewFleetCredentialStore(envB, nil)
	if err := a.Put(FleetNamespaceOrg, "same", []byte(fakeKey("a"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(FleetNamespaceOrg, "same", []byte(fakeKey("b"))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Purge(FleetNamespaceOrg); err != nil {
		t.Fatal(err)
	}
	locB, _ := FleetCredentialLocator(FleetNamespaceOrg, envB, "same")
	if v, err := keyring.Get(keyringService, locB); err != nil || v != fakeKey("b") {
		t.Fatalf("env %s entry touched by env %s purge: %v", envB, envA, err)
	}
	_, _ = b.Purge(FleetNamespaceOrg)
}

func TestFleetCredentialStore_DeleteRemovesOneID(t *testing.T) {
	env := uniqueEnv(t)
	s, _ := NewFleetCredentialStore(env, nil)
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
