package oauth

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// timeUnix is a test helper that converts an epoch second to time.Time.
func timeUnix(sec int64) time.Time {
	return time.Unix(sec, 0)
}

func newTestStore(t *testing.T) (*DCRStore, *fakeCredStore) {
	t.Helper()
	creds := &fakeCredStore{secrets: make(map[string]string)}
	dir := t.TempDir()
	store := NewDCRStore(
		filepath.Join(dir, "dcr_clients.json"),
		creds.save,
		creds.load,
	)
	store.nowFn = func() time.Time { return timeUnix(5000) } // stable test clock
	return store, creds
}

// fakeCredStore is a thread-safe in-memory stand-in for the OS credstore.
type fakeCredStore struct {
	mu      sync.Mutex
	secrets map[string]string
}

func (f *fakeCredStore) save(key, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets[key] = secret
	return nil
}

func (f *fakeCredStore) load(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.secrets[key], nil // returns "" when absent — same as missing
}

func TestDCRStore_RoundTrip(t *testing.T) {
	store, creds := newTestStore(t)

	key := DCRKey{Issuer: "https://as.example.com", Resource: "https://api.example.com/mcp", Scopes: []string{"read", "write"}}
	rc := &RegisteredClient{
		ClientID:         "cid-test",
		ClientIDIssuedAt: 1700000000,
	}

	if err := store.Save(key, rc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientID != "cid-test" {
		t.Errorf("client_id = %q", got.ClientID)
	}
	if got.ClientIDIssuedAt != 1700000000 {
		t.Errorf("client_id_issued_at = %d", got.ClientIDIssuedAt)
	}
	if got.ClientSecret != "" {
		t.Errorf("unexpected secret in load result for public-client registration")
	}
	// No secret was issued so creds store should be empty.
	if len(creds.secrets) != 0 {
		t.Errorf("credstore should be empty for public client, got %v", creds.secrets)
	}
}

func TestDCRStore_RoundTrip_WithSecret(t *testing.T) {
	store, creds := newTestStore(t)

	key := DCRKey{Issuer: "https://as.example.com", Resource: "https://r", Scopes: nil}
	rc := &RegisteredClient{
		ClientID:     "cid-conf",
		ClientSecret: "s3cr3t!",
	}

	if err := store.Save(key, rc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Secret must be in credstore.
	if len(creds.secrets) != 1 {
		t.Errorf("want 1 credstore entry, got %d", len(creds.secrets))
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientID != "cid-conf" {
		t.Errorf("client_id = %q", got.ClientID)
	}
	if got.ClientSecret != "s3cr3t!" {
		t.Errorf("client_secret = %q", got.ClientSecret)
	}
}

func TestDCRStore_Load_NotFound(t *testing.T) {
	store, _ := newTestStore(t)
	key := DCRKey{Issuer: "https://missing.example.com", Resource: "r", Scopes: nil}
	_, err := store.Load(key)
	if !errors.Is(err, ErrDCRNotFound) {
		t.Errorf("want ErrDCRNotFound, got %v", err)
	}
}

func TestDCRStore_Load_ExpiredSecret_TriggersReregistration(t *testing.T) {
	store, _ := newTestStore(t)
	// nowFn returns timeUnix(5000); set expiry to 4999 — already expired.
	key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: nil}
	rc := &RegisteredClient{
		ClientID:              "cid-expired",
		ClientSecret:          "old-secret",
		ClientSecretExpiresAt: 4999, // expires at 4999 < 5000 (now)
	}

	if err := store.Save(key, rc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := store.Load(key)
	if !errors.Is(err, ErrDCRExpired) {
		t.Errorf("want ErrDCRExpired, got %v", err)
	}

	// After ErrDCRExpired the entry should have been purged so the next
	// Load returns ErrDCRNotFound.
	_, err = store.Load(key)
	if !errors.Is(err, ErrDCRNotFound) {
		t.Errorf("want ErrDCRNotFound after expiry purge, got %v", err)
	}
}

func TestDCRStore_Load_NonExpiredSecret(t *testing.T) {
	store, _ := newTestStore(t)
	// nowFn returns timeUnix(5000); set expiry to 9999 — not yet expired.
	key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: nil}
	rc := &RegisteredClient{
		ClientID:              "cid-valid",
		ClientSecret:          "valid-secret",
		ClientSecretExpiresAt: 9999,
	}

	if err := store.Save(key, rc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientID != "cid-valid" {
		t.Errorf("client_id = %q", got.ClientID)
	}
}

func TestDCRStore_Delete(t *testing.T) {
	store, _ := newTestStore(t)
	key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: nil}
	rc := &RegisteredClient{ClientID: "cid-to-delete"}

	if err := store.Save(key, rc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.Load(key)
	if !errors.Is(err, ErrDCRNotFound) {
		t.Errorf("want ErrDCRNotFound after delete, got %v", err)
	}
}

func TestDCRStore_Persistence_AcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dcr_clients.json")
	creds := &fakeCredStore{secrets: make(map[string]string)}

	key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: []string{"a"}}
	rc := &RegisteredClient{ClientID: "cid-persistent"}

	// Write with instance #1.
	s1 := NewDCRStore(path, creds.save, creds.load)
	s1.nowFn = func() time.Time { return timeUnix(5000) }
	if err := s1.Save(key, rc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read with a fresh instance #2 (simulates process restart).
	s2 := NewDCRStore(path, creds.save, creds.load)
	s2.nowFn = func() time.Time { return timeUnix(5000) }
	got, err := s2.Load(key)
	if err != nil {
		t.Fatalf("Load on fresh instance: %v", err)
	}
	if got.ClientID != "cid-persistent" {
		t.Errorf("client_id after reload = %q", got.ClientID)
	}
}

func TestDCRKey_ScopesAreSorted(t *testing.T) {
	k1 := DCRKey{Issuer: "i", Resource: "r", Scopes: []string{"b", "a", "c"}}
	k2 := DCRKey{Issuer: "i", Resource: "r", Scopes: []string{"a", "b", "c"}}
	if k1.String() != k2.String() {
		t.Errorf("scope ordering should not matter: %q vs %q", k1.String(), k2.String())
	}
}

func TestDCRKey_DifferentScopesDifferentKey(t *testing.T) {
	k1 := DCRKey{Issuer: "i", Resource: "r", Scopes: []string{"read"}}
	k2 := DCRKey{Issuer: "i", Resource: "r", Scopes: []string{"write"}}
	if k1.String() == k2.String() {
		t.Errorf("different scopes should produce different keys")
	}
}

func TestDefaultDCRStorePath(t *testing.T) {
	p := DefaultDCRStorePath("/home/user/.kenaz/harness/prod")
	expected := "/home/user/.kenaz/harness/prod/oauth/dcr_clients.json"
	if p != expected {
		t.Errorf("DefaultDCRStorePath = %q, want %q", p, expected)
	}
}

func TestDCRStore_NoCredstoreFns_PublicClient(t *testing.T) {
	// Nil saveFn/loadFn should work fine for public-client registrations.
	dir := t.TempDir()
	store := NewDCRStore(filepath.Join(dir, "dcr_clients.json"), nil, nil)
	store.nowFn = func() time.Time { return timeUnix(5000) }

	key := DCRKey{Issuer: "https://as.example.com", Resource: "r"}
	rc := &RegisteredClient{ClientID: "public-cid"}

	if err := store.Save(key, rc); err != nil {
		t.Fatalf("Save with nil credstore fns: %v", err)
	}
	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load with nil credstore fns: %v", err)
	}
	if got.ClientID != "public-cid" {
		t.Errorf("client_id = %q", got.ClientID)
	}
}

func TestDCRStore_Concurrent(t *testing.T) {
	store, _ := newTestStore(t)
	const n = 20

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := DCRKey{Issuer: "https://as.example.com", Resource: "r", Scopes: []string{"s"}}
			_ = store.Save(key, &RegisteredClient{ClientID: "cid"})
			_, _ = store.Load(key)
			if i%3 == 0 {
				_ = store.Delete(key)
			}
		}()
	}
	wg.Wait()
}
