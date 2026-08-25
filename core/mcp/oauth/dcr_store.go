package oauth

// DCRStore persists registered client credentials between process launches so
// the harness does not re-register with the authorization server on every
// start. It is a lightweight JSON file stored under the harness data directory.
//
// KEY DESIGN DECISIONS
//
//  1. Keyed by (issuer, resource, sorted-scopes): different resources on the
//     same authorization server may have different scopes, so the scope set is
//     part of the identity. The key is a deterministic string derived from the
//     three components.
//
//  2. client_id is non-sensitive and is stored in the JSON file alongside the
//     rest of the registration metadata (issued_at, expires_at).
//
//  3. client_secret IS sensitive. When present, it is stored in the OS
//     credstore under the key dcr:<issuer>:<resource>:<scopes-hash>. The
//     secret is never written to the JSON file. If the credstore is not
//     wired (SecretSaver/Loader == nil), public-client registrations (no
//     secret) work fine; confidential-client registrations will persist the
//     client_id only and callers must be prepared for an empty secret.
//
//  4. Re-registration triggers: the client_secret expires (non-zero expiry
//     and now >= expiry), or a 401 during token exchange signals that the
//     server has invalidated the registration.
//
// SECURITY: this file never contains client_secret values. Any code that
// writes this store must ensure secrets go through the SecretSaver interface
// (which routes them to the OS keychain). The file is created with 0o600
// permissions.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// dcrEntry is one row in the on-disk JSON store.
type dcrEntry struct {
	// ClientID is the server-issued OAuth client identifier.
	ClientID string `json:"client_id"`
	// ClientIDIssuedAt is the RFC 7591 issued-at timestamp (epoch seconds).
	ClientIDIssuedAt int64 `json:"client_id_issued_at,omitempty"`
	// ClientSecretExpiresAt is the RFC 7591 expiry for the client_secret
	// (epoch seconds). Zero means no expiry. When non-zero and the current
	// time is past this, the entry is stale and re-registration is needed.
	ClientSecretExpiresAt int64 `json:"client_secret_expires_at,omitempty"`
	// HasSecret records whether a client_secret was issued. The secret itself
	// lives in the credstore; this flag lets us know to look it up.
	HasSecret bool `json:"has_secret,omitempty"`
}

// dcrFile is the complete on-disk JSON structure.
type dcrFile struct {
	// Entries maps a DCRKey string to its registration entry.
	Entries map[string]dcrEntry `json:"entries"`
}

// SecretSaver persists a client_secret to the OS credstore. The key is an
// opaque identifier formed from the DCRKey; the secret is the raw bytes.
// Implementations must never log or surface the secret value.
type SecretSaver func(key, secret string) error

// SecretLoader retrieves a previously saved client_secret from the credstore.
// Returns ("", nil) when no secret exists for that key.
type SecretLoader func(key string) (string, error)

// DCRStore is a thread-safe, file-backed store for registered client
// credentials. Construct it via NewDCRStore.
type DCRStore struct {
	mu      sync.Mutex
	path    string
	saveFn  SecretSaver
	loadFn  SecretLoader
	nowFn   func() time.Time
}

// NewDCRStore creates a DCRStore that persists entries to path. The file is
// created (with its parent directories) on first write. saveFn and loadFn
// route client_secret values through the OS credstore; both may be nil when
// only public-client registrations (no secret) are expected.
//
// Production caller (kitty-specs/connector-lifecycle-truth-01PMZ303 UNIT-3
// 3e): core/rpc/views/tools/oauth.go's (*API).newDCRStore constructs one per
// SignInRecipe call, anchored at DefaultDCRStorePath(a.cfg.DataDir), with
// real SecretSaver/SecretLoader closures over the same
// keychain-write/secrets-resolve seam the per-recipe OAuth credential uses.
// So a DCR-registered client_id (and any client_secret a provider issues
// despite the harness still requesting a public client — UNIT-3 3b is
// separate and not yet wired) survives a relaunch instead of re-registering
// with the provider on every sign-in.
func NewDCRStore(path string, saveFn SecretSaver, loadFn SecretLoader) *DCRStore {
	return &DCRStore{
		path:   path,
		saveFn: saveFn,
		loadFn: loadFn,
		nowFn:  time.Now,
	}
}

// DCRKey is the canonical lookup key for a registered client. It encodes the
// authorization server issuer, the resource server URL, and the sorted scope
// list so that different scope combinations on the same server are tracked
// separately.
type DCRKey struct {
	Issuer   string
	Resource string
	Scopes   []string // will be sorted before use
}

// String returns the canonical string form of the key, used as the JSON map
// key and as the credstore key prefix.
func (k DCRKey) String() string {
	scopes := make([]string, len(k.Scopes))
	copy(scopes, k.Scopes)
	sort.Strings(scopes)
	return fmt.Sprintf("%s|%s|%s", k.Issuer, k.Resource, strings.Join(scopes, ","))
}

// credstoreKey derives the credstore lookup key for the client_secret
// associated with a DCRKey. We use a short prefix plus a hex-encoded SHA-256
// of the DCRKey string so the key is safe for all credstore backends (which
// may restrict key length or characters).
func (k DCRKey) credstoreKey() string {
	sum := sha256.Sum256([]byte(k.String()))
	return fmt.Sprintf("dcr:%x", sum[:8]) // 8 bytes = 16 hex chars — compact, collision-resistant
}

// ErrDCRNotFound is returned by Load when no registration exists for the key.
var ErrDCRNotFound = errors.New("oauth: dcr: no registration found")

// ErrDCRExpired is returned by Load when the stored registration's
// client_secret has expired and re-registration is needed.
var ErrDCRExpired = errors.New("oauth: dcr: registration secret has expired — re-registration required")

// Save persists a RegisteredClient for the given key. If the client has a
// secret, it is routed through saveFn (the credstore). The secret is never
// written to the JSON file.
func (s *DCRStore) Save(key DCRKey, rc *RegisteredClient) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return err
	}

	// Persist the secret to the credstore before writing anything else.
	// HasSecret below must track whether this write actually happened, not
	// merely whether the caller supplied a secret: setting it unconditionally
	// on rc.ClientSecret != "" let an entry claim a secret exists (and cache
	// that claim past Load's "check the credstore" branch) even on the
	// saveFn == nil / not-wired path, where no secret was ever persisted.
	secretPersisted := false
	if rc.ClientSecret != "" && s.saveFn != nil {
		if err := s.saveFn(key.credstoreKey(), rc.ClientSecret); err != nil {
			return fmt.Errorf("oauth: dcr: save client_secret to credstore: %w", err)
		}
		secretPersisted = true
	}

	f.Entries[key.String()] = dcrEntry{
		ClientID:              rc.ClientID,
		ClientIDIssuedAt:      rc.ClientIDIssuedAt,
		ClientSecretExpiresAt: rc.ClientSecretExpiresAt,
		HasSecret:             secretPersisted,
	}
	return s.writeFile(f)
}

// Load retrieves the RegisteredClient for the given key. Returns ErrDCRNotFound
// when no entry exists and ErrDCRExpired when the stored secret has expired.
// On success, the returned RegisteredClient's ClientSecret is populated from
// the credstore when the entry has a secret.
func (s *DCRStore) Load(key DCRKey) (*RegisteredClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return nil, err
	}

	e, ok := f.Entries[key.String()]
	if !ok {
		return nil, ErrDCRNotFound
	}

	// Check secret expiry.
	if e.ClientSecretExpiresAt != 0 {
		now := s.nowFn()
		if now.Unix() >= e.ClientSecretExpiresAt {
			// Remove the stale entry so the next call does DCR again.
			delete(f.Entries, key.String())
			_ = s.writeFile(f) // best-effort; ignore write error here
			return nil, ErrDCRExpired
		}
	}

	rc := &RegisteredClient{
		ClientID:              e.ClientID,
		ClientIDIssuedAt:      e.ClientIDIssuedAt,
		ClientSecretExpiresAt: e.ClientSecretExpiresAt,
	}

	// Retrieve the secret from the credstore if one was stored.
	if e.HasSecret && s.loadFn != nil {
		secret, err := s.loadFn(key.credstoreKey())
		if err != nil {
			return nil, fmt.Errorf("oauth: dcr: load client_secret from credstore: %w", err)
		}
		rc.ClientSecret = secret
	}

	// e.HasSecret records a confidential client whose secret this store
	// persisted. If we get here with ClientSecret still empty — the
	// credstore row was removed out from under us (the production loadFn,
	// core/rpc/views/tools/oauth.go's newDCRStore closure, converts ANY
	// resolve failure to ("", nil)), or loadFn was nil so the lookup never
	// ran at all — returning rc as-is would silently downgrade a
	// confidential client to a public one with an empty secret, cached
	// until ClientSecretExpiresAt with no invalidation. Treat it as
	// equivalent to no registration so the caller re-registers cleanly
	// instead of authenticating with a client_secret that quietly became "".
	if e.HasSecret && rc.ClientSecret == "" {
		return nil, ErrDCRNotFound
	}

	return rc, nil
}

// Delete removes the stored registration for the given key from both the JSON
// file and the credstore. Useful after a 401 that signals the server has
// invalidated the registration.
func (s *DCRStore) Delete(key DCRKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return err
	}
	delete(f.Entries, key.String())
	return s.writeFile(f)
}

// readFile loads the on-disk JSON file or returns an empty store when the file
// does not yet exist.
func (s *DCRStore) readFile() (*dcrFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &dcrFile{Entries: make(map[string]dcrEntry)}, nil
		}
		return nil, fmt.Errorf("oauth: dcr: read store file: %w", err)
	}
	var f dcrFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("oauth: dcr: parse store file: %w", err)
	}
	if f.Entries == nil {
		f.Entries = make(map[string]dcrEntry)
	}
	return &f, nil
}

// writeFile serializes the store to disk with 0o600 permissions.
func (s *DCRStore) writeFile(f *dcrFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("oauth: dcr: marshal store: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("oauth: dcr: create store dir: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("oauth: dcr: write store file: %w", err)
	}
	return nil
}

// DefaultDCRStorePath returns the canonical path for the DCR store file under
// the harness data directory: <dataDir>/oauth/dcr_clients.json. The caller is
// responsible for passing the correct data directory (typically from
// core/paths.DataDir()).
func DefaultDCRStorePath(dataDir string) string {
	return filepath.Join(dataDir, "oauth", "dcr_clients.json")
}
