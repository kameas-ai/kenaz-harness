package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
)

// Fleet credential keychain namespaces (provider-credentials-client-01PCCLI01
// B1). Keys that arrive through Fleet live under the same OS-keychain service
// as the user's own keys (keyringService, "kenaz-harness") but in their own
// account namespaces, so a purge can never reach a key the user typed in:
//
//	local (today, unchanged)  kenaz-harness/<profileID>
//	org / team plaintext      kenaz-harness/fleet/<env>/org/<credential_id>
//	synced user keys          kenaz-harness/fleet/<env>/user/<credential_id>
//
// <env> is core/paths.KeychainNamespace() — the same token every other
// per-env keychain item uses (fleet:<env>:access_token).
//
// LOGGING: nothing in this file logs, and nothing may. Plaintext, locators
// and secret-typed values never reach a log sink; the credential id is the
// only thing a caller may record. scripts/ci/check-no-credential-in-fleet-logs.sh
// enforces this for every file in the namespace.

// FleetNamespace selects one of the two Fleet keychain namespaces.
type FleetNamespace string

const (
	// FleetNamespaceOrg holds org- and team-delivered plaintext (WP05).
	FleetNamespaceOrg FleetNamespace = "org"
	// FleetNamespaceUser holds the user's own keys synced from their
	// other devices (WP06).
	FleetNamespaceUser FleetNamespace = "user"
)

// fleetLocatorRoot is the account prefix shared by both namespaces.
const fleetLocatorRoot = keyringService + "/fleet/"

// fleetIndexName is the per-namespace account that lists the credential ids
// stored there. The OS keychain APIs behind core/keyring cannot enumerate a
// service, so purge needs its own record of what it wrote. A leading "_"
// cannot collide with a credential id (validCredentialID rejects it).
const fleetIndexName = "_index"

var (
	// ErrInvalidCredentialID rejects an id that would not make a safe
	// keychain account name.
	ErrInvalidCredentialID = errors.New("secrets: invalid fleet credential id")
	// ErrInvalidFleetNamespace rejects a namespace other than org|user.
	ErrInvalidFleetNamespace = errors.New("secrets: invalid fleet credential namespace")
	// ErrInvalidEnv rejects an empty env token or one containing "/".
	ErrInvalidEnv = errors.New("secrets: invalid keychain env namespace")
)

func (ns FleetNamespace) valid() bool {
	return ns == FleetNamespaceOrg || ns == FleetNamespaceUser
}

// validCredentialID mirrors core/llm.ValidCredentialID (this package cannot
// import core/llm): 1-128 of [A-Za-z0-9._-], first character alphanumeric.
func validCredentialID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 && !alnum {
			return false
		}
		if !alnum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func validEnv(env string) bool {
	return env != "" && !strings.ContainsAny(env, "/\\")
}

// FleetCredentialLocator returns the keychain account for credentialID in
// namespace ns under env: kenaz-harness/fleet/<env>/<ns>/<credentialID>.
func FleetCredentialLocator(ns FleetNamespace, env, credentialID string) (string, error) {
	if !ns.valid() {
		return "", ErrInvalidFleetNamespace
	}
	if !validEnv(env) {
		return "", ErrInvalidEnv
	}
	if !validCredentialID(credentialID) {
		return "", ErrInvalidCredentialID
	}
	return fleetLocatorRoot + env + "/" + string(ns) + "/" + credentialID, nil
}

func fleetIndexLocator(ns FleetNamespace, env string) string {
	return fleetLocatorRoot + env + "/" + string(ns) + "/" + fleetIndexName
}

// FleetCredentialStore writes, removes and purges Fleet-delivered keys in
// the OS keychain for one env. It is the only writer of the two Fleet
// namespaces; reads go through the ordinary resolver path
// (core/llm/credref derives the same locator from Source + CredentialID).
//
// It never touches a local kenaz-harness/<profileID> entry: every account it
// writes or deletes is built by FleetCredentialLocator, and Purge only walks
// ids its own index recorded.
type FleetCredentialStore struct {
	env string
	mem *MemoryBackend

	mu sync.Mutex
}

// NewFleetCredentialStore returns a store for env (pass
// paths.KeychainNamespace()). mem is the process's shared in-memory backend,
// or nil; when set, Delete and Purge also drop any hot-cache copy so a
// purged key cannot resolve from memory.
func NewFleetCredentialStore(env string, mem *MemoryBackend) (*FleetCredentialStore, error) {
	if !validEnv(env) {
		return nil, ErrInvalidEnv
	}
	return &FleetCredentialStore{env: env, mem: mem}, nil
}

// Put stores plaintext for credentialID in ns, then zeroes plaintext. The id
// is recorded in the namespace index before the secret is written, so a
// crash between the two leaves an index entry Purge can clean up, never an
// unindexed secret Purge cannot see.
func (s *FleetCredentialStore) Put(ns FleetNamespace, credentialID string, plaintext []byte) error {
	defer zeroFleetBytes(plaintext)
	loc, err := FleetCredentialLocator(ns, s.env, credentialID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.readIndexLocked(ns)
	if err != nil {
		return err
	}
	if !containsFleetID(ids, credentialID) {
		if err := s.writeIndexLocked(ns, append(ids, credentialID)); err != nil {
			return err
		}
	}
	if err := keyring.Set(keyringService, loc, string(plaintext)); err != nil {
		return fmt.Errorf("secrets: store fleet credential %s: %w", credentialID, err)
	}
	return nil
}

// Delete removes credentialID from ns. Removing an absent id is not an
// error.
func (s *FleetCredentialStore) Delete(ns FleetNamespace, credentialID string) error {
	loc, err := FleetCredentialLocator(ns, s.env, credentialID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.deleteLocked(loc); err != nil {
		return err
	}
	ids, err := s.readIndexLocked(ns)
	if err != nil {
		return err
	}
	return s.writeIndexLocked(ns, withoutFleetID(ids, credentialID))
}

// Purge removes every credential in ns for this env and returns how many
// ids it removed. Purge(FleetNamespaceOrg) is the org/team purge (lockdown,
// node_removed, sign-out, org change — WP05); Purge(FleetNamespaceUser) is
// the synced-user-key purge (WP06). Neither reaches the other namespace or
// any local entry. On a partial failure the ids that could not be removed
// stay in the index, so a retry finishes the job.
func (s *FleetCredentialStore) Purge(ns FleetNamespace) (int, error) {
	if !ns.valid() {
		return 0, ErrInvalidFleetNamespace
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.readIndexLocked(ns)
	if err != nil {
		return 0, err
	}
	var (
		removed int
		left    []string
		errs    []error
	)
	for _, id := range ids {
		loc, lerr := FleetCredentialLocator(ns, s.env, id)
		if lerr != nil {
			// An index entry that is not a valid id was never written
			// by Put; drop it.
			continue
		}
		if derr := s.deleteLocked(loc); derr != nil {
			left = append(left, id)
			errs = append(errs, fmt.Errorf("%s: %w", id, derr))
			continue
		}
		removed++
	}
	if err := s.writeIndexLocked(ns, left); err != nil {
		errs = append(errs, err)
	}
	return removed, errors.Join(errs...)
}

// IDs returns the credential ids recorded in ns, sorted.
func (s *FleetCredentialStore) IDs(ns FleetNamespace) ([]string, error) {
	if !ns.valid() {
		return nil, ErrInvalidFleetNamespace
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.readIndexLocked(ns)
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out, nil
}

func (s *FleetCredentialStore) deleteLocked(loc string) error {
	if !strings.HasPrefix(loc, fleetLocatorRoot) {
		// Structural guard: this store never deletes outside the
		// Fleet namespaces.
		return ErrInvalidFleetNamespace
	}
	if s.mem != nil {
		s.mem.ClearEntry(ref.RefKeychain, loc)
	}
	if err := keyring.Delete(keyringService, loc); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

func (s *FleetCredentialStore) readIndexLocked(ns FleetNamespace) ([]string, error) {
	if !ns.valid() {
		return nil, ErrInvalidFleetNamespace
	}
	raw, err := keyring.Get(keyringService, fleetIndexLocator(ns, s.env))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: read fleet credential index: %w", err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("secrets: parse fleet credential index: %w", err)
	}
	return ids, nil
}

func (s *FleetCredentialStore) writeIndexLocked(ns FleetNamespace, ids []string) error {
	loc := fleetIndexLocator(ns, s.env)
	if len(ids) == 0 {
		if err := keyring.Delete(keyringService, loc); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("secrets: clear fleet credential index: %w", err)
		}
		return nil
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, loc, string(b)); err != nil {
		return fmt.Errorf("secrets: write fleet credential index: %w", err)
	}
	return nil
}

func zeroFleetBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func containsFleetID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func withoutFleetID(ids []string, id string) []string {
	out := ids[:0:0]
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}
