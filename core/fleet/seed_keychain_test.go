package fleet

// seed_keychain_test.go — review fix #1 (device-keys-handoff-01DEVKH01):
// a keychain error that is NOT keyring.ErrNotFound must never be read as
// "no seed" — SeedKey must not write, and enroll must proceed WITHOUT a
// handoff key, recording KeyRegUnavailable.

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
)

// withSeedKeychain swaps the seed keychain seam for the test.
func withSeedKeychain(t *testing.T, get func(string, string) (string, error), set func(string, string, string) error) {
	t.Helper()
	og, os := seedKeyringGet, seedKeyringSet
	seedKeyringGet, seedKeyringSet = get, set
	t.Cleanup(func() { seedKeyringGet, seedKeyringSet = og, os })
}

func TestSeedKey_LockedKeychain_NeverOverwrites(t *testing.T) {
	var sets atomic.Int32
	locked := errors.New("user interaction is not allowed")
	withSeedKeychain(t,
		func(string, string) (string, error) { return "", locked },
		func(string, string, string) error { sets.Add(1); return nil })
	if _, err := SeedKey(); !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("SeedKey err = %v, want ErrKeychainUnavailable", err)
	}
	if _, err := LoadContextSeed(); !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("LoadContextSeed err = %v, want ErrKeychainUnavailable (not 'not found')", err)
	}
	if sets.Load() != 0 {
		t.Fatalf("seed written %d times on a locked keychain — root secret overwritten", sets.Load())
	}
}

// Enroll on a locked keychain still enrolls, without the handoff key.
func TestEnroll_LockedKeychain_EnrollsWithoutHandoffKey(t *testing.T) {
	f := newKeyFleet(t)
	c, _ := keyedClient(t, f)
	var sets atomic.Int32
	withSeedKeychain(t,
		func(string, string) (string, error) { return "", errors.New("keychain locked") },
		func(string, string, string) error { sets.Add(1); return nil })
	if _, err := c.RefreshIdentity(t.Context(), "node-L", "darwin", "0.93.0"); err != nil {
		t.Fatalf("enroll must not be blocked by the keychain: %v", err)
	}
	body := f.snapshot()[0].Body
	if body["handoff_public_key"] != nil {
		t.Fatal("no handoff key may be sent without the real seed")
	}
	if body["signing_public_key"] == nil {
		t.Fatal("the signing key (file-backed) should still register")
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegUnavailable || kr.Message == "" {
		t.Fatalf("key registration = %+v", kr)
	}
	if sets.Load() != 0 {
		t.Fatal("enroll overwrote the seed on a locked keychain")
	}
}

// Concurrent first-run callers mint exactly ONE seed.
func TestSeedKey_ConcurrentFirstRun_OneSeed(t *testing.T) {
	var mu sync.Mutex
	stored := ""
	var sets atomic.Int32
	withSeedKeychain(t,
		func(string, string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			if stored == "" {
				return "", keyring.ErrNotFound
			}
			return stored, nil
		},
		func(_, _, v string) error {
			time.Sleep(time.Millisecond) // widen the race window
			mu.Lock()
			stored = v
			mu.Unlock()
			sets.Add(1)
			return nil
		})
	var wg sync.WaitGroup
	seeds := make([][]byte, 8)
	for i := range seeds {
		wg.Add(1)
		go func(i int) { defer wg.Done(); seeds[i], _ = SeedKey() }(i)
	}
	wg.Wait()
	if sets.Load() != 1 {
		t.Fatalf("seed minted %d times, want 1", sets.Load())
	}
	for i := range seeds {
		if string(seeds[i]) != string(seeds[0]) || len(seeds[i]) != seedSize {
			t.Fatalf("caller %d got a different seed", i)
		}
	}
}
