package secrets

import (
	"crypto/sha256"
	"encoding/hex"
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
// account namespaces, scoped per env, per Fleet realm and per Fleet user, so
// a purge can never reach a key the user typed in and one Fleet account can
// never resolve another's keys on a shared OS account:
//
//	local (today, unchanged)  kenaz-harness/<profileID>
//	org / team plaintext      kenaz-harness/fleet/<env>/<realm>/<sub>/org/<credential_id>
//	synced user keys          kenaz-harness/fleet/<env>/<realm>/<sub>/user/<credential_id>
//
// <env> is core/paths.KeychainNamespace() — the same token every other
// per-env keychain item uses (fleet:<env>:access_token). <realm> and <sub>
// are FleetPrincipal's hashed forms of the token's iss and sub claims: a
// Zitadel subject may be an email, so neither appears in clear.
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

// fleetLocatorRoot is the account prefix shared by every Fleet namespace.
const fleetLocatorRoot = keyringService + "/fleet/"

// fleetIndexName is the per-namespace account that lists the credential ids
// stored there. The OS keychain APIs behind core/keyring cannot enumerate a
// service, so purge needs its own record of what it wrote. A leading "_"
// cannot collide with a credential id (validCredentialID rejects it).
const fleetIndexName = "_index"

// fleetPrincipalsName is the per-env account listing every principal that
// has stored a Fleet credential on this OS account, so a session change can
// purge principals that are no longer signed in.
const fleetPrincipalsName = "_principals"

var (
	// ErrInvalidCredentialID rejects an id that would not make a safe
	// keychain account name.
	ErrInvalidCredentialID = errors.New("secrets: invalid fleet credential id")
	// ErrInvalidFleetNamespace rejects a namespace other than org|user.
	ErrInvalidFleetNamespace = errors.New("secrets: invalid fleet credential namespace")
	// ErrInvalidEnv rejects an empty env token or one containing "/".
	ErrInvalidEnv = errors.New("secrets: invalid keychain env namespace")
	// ErrNoFleetPrincipal means there is no signed-in Fleet identity to
	// scope a Fleet credential to; Fleet keys do not resolve.
	ErrNoFleetPrincipal = errors.New("secrets: no signed-in Fleet identity for a Fleet credential")
)

// FleetPrincipal is the (realm, user, org) a Fleet credential belongs to, in
// hashed, non-PII form. Build it with NewFleetPrincipal.
type FleetPrincipal struct {
	// Realm is the first 16 hex chars of SHA-256 over the issuer.
	Realm string `json:"realm"`
	// Subject is the first 24 hex chars of SHA-256 over issuer and sub.
	Subject string `json:"subject"`
	// Org is the first 16 hex chars of SHA-256 over issuer and org id; it
	// is not part of the locator, only of the purge-on-org-change record.
	Org string `json:"org,omitempty"`
}

// NewFleetPrincipal hashes an access token's iss, sub and org claims into a
// FleetPrincipal. issuer and subject are required.
func NewFleetPrincipal(issuer, subject, orgID string) (FleetPrincipal, error) {
	if issuer == "" || subject == "" {
		return FleetPrincipal{}, ErrNoFleetPrincipal
	}
	p := FleetPrincipal{
		Realm:   hashToken("kenaz-fleet-realm-v1", issuer)[:16],
		Subject: hashToken("kenaz-fleet-subject-v1", issuer, subject)[:24],
	}
	if orgID != "" {
		p.Org = hashToken("kenaz-fleet-org-v1", issuer, orgID)[:16]
	}
	return p, nil
}

func hashToken(domain string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(domain))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (p FleetPrincipal) valid() bool {
	return isLowerHex(p.Realm, 16) && isLowerHex(p.Subject, 24) && (p.Org == "" || isLowerHex(p.Org, 16))
}

// sameAccount reports whether p and q are the same realm and user.
func (p FleetPrincipal) sameAccount(q FleetPrincipal) bool {
	return p.Realm == q.Realm && p.Subject == q.Subject
}

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

func principalPrefix(env string, p FleetPrincipal) string {
	return fleetLocatorRoot + env + "/" + p.Realm + "/" + p.Subject + "/"
}

// FleetCredentialLocator returns the keychain account for credentialID in
// namespace ns for principal p under env:
// kenaz-harness/fleet/<env>/<realm>/<sub>/<ns>/<credentialID>.
func FleetCredentialLocator(ns FleetNamespace, env string, p FleetPrincipal, credentialID string) (string, error) {
	if !ns.valid() {
		return "", ErrInvalidFleetNamespace
	}
	if !validEnv(env) {
		return "", ErrInvalidEnv
	}
	if !p.valid() {
		return "", ErrNoFleetPrincipal
	}
	if !validCredentialID(credentialID) {
		return "", ErrInvalidCredentialID
	}
	return principalPrefix(env, p) + string(ns) + "/" + credentialID, nil
}

func fleetIndexLocator(ns FleetNamespace, env string, p FleetPrincipal) string {
	return principalPrefix(env, p) + string(ns) + "/" + fleetIndexName
}

func fleetPrincipalsLocator(env string) string {
	return fleetLocatorRoot + env + "/" + fleetPrincipalsName
}

// fleetMu serialises every index and registry read-modify-write in this
// process, across stores.
var fleetMu sync.Mutex

// FleetCredentialStore writes, removes and purges Fleet-delivered keys in
// the OS keychain for one env and one principal. It is the only writer of
// the Fleet namespaces; reads go through the ordinary resolver path
// (core/llm/credref derives the same locator from Source + CredentialID and
// the signed-in principal).
//
// It never touches a local kenaz-harness/<profileID> entry, nor another
// principal's entries: every account it writes or deletes is built by
// FleetCredentialLocator for its own principal, and Purge only walks ids its
// own index recorded.
type FleetCredentialStore struct {
	env       string
	principal FleetPrincipal
	mem       *MemoryBackend
}

// NewFleetCredentialStore returns a store for env (pass
// paths.KeychainNamespace()) and principal p. mem is the process's shared
// in-memory backend, or nil; when set, Delete and Purge also drop any
// hot-cache copy so a purged key cannot resolve from memory.
func NewFleetCredentialStore(env string, p FleetPrincipal, mem *MemoryBackend) (*FleetCredentialStore, error) {
	if !validEnv(env) {
		return nil, ErrInvalidEnv
	}
	if !p.valid() {
		return nil, ErrNoFleetPrincipal
	}
	return &FleetCredentialStore{env: env, principal: p, mem: mem}, nil
}

// Put stores plaintext for credentialID in ns, then zeroes plaintext. The
// principal and the id are recorded before the secret is written, so a crash
// between the two leaves records Purge can clean up, never an unrecorded
// secret Purge cannot see.
func (s *FleetCredentialStore) Put(ns FleetNamespace, credentialID string, plaintext []byte) error {
	defer zeroFleetBytes(plaintext)
	loc, err := FleetCredentialLocator(ns, s.env, s.principal, credentialID)
	if err != nil {
		return err
	}
	fleetMu.Lock()
	defer fleetMu.Unlock()
	if err := registerPrincipalLocked(s.env, s.principal); err != nil {
		return err
	}
	ids, err := readIndexLocked(s.env, s.principal, ns)
	if err != nil {
		return err
	}
	if !containsFleetID(ids, credentialID) {
		if err := writeIndexLocked(s.env, s.principal, ns, append(ids, credentialID)); err != nil {
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
	loc, err := FleetCredentialLocator(ns, s.env, s.principal, credentialID)
	if err != nil {
		return err
	}
	fleetMu.Lock()
	defer fleetMu.Unlock()
	if err := deleteFleetEntryLocked(s.mem, loc); err != nil {
		return err
	}
	ids, err := readIndexLocked(s.env, s.principal, ns)
	if err != nil {
		return err
	}
	return writeIndexLocked(s.env, s.principal, ns, withoutFleetID(ids, credentialID))
}

// Purge removes every credential in ns for this principal and env and
// returns how many ids it removed. Neither namespace's purge reaches the
// other, another principal, or any local entry. On a partial failure the
// ids that could not be removed stay in the index, so a retry finishes.
func (s *FleetCredentialStore) Purge(ns FleetNamespace) (int, error) {
	if !ns.valid() {
		return 0, ErrInvalidFleetNamespace
	}
	fleetMu.Lock()
	defer fleetMu.Unlock()
	return purgeLocked(s.env, s.principal, ns, s.mem)
}

// IDs returns the credential ids recorded in ns, sorted.
func (s *FleetCredentialStore) IDs(ns FleetNamespace) ([]string, error) {
	if !ns.valid() {
		return nil, ErrInvalidFleetNamespace
	}
	fleetMu.Lock()
	defer fleetMu.Unlock()
	ids, err := readIndexLocked(s.env, s.principal, ns)
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out, nil
}

// PurgeFleetCredentialsForSignOut removes every Fleet credential — org/team
// and synced user keys — that principal p stored under env, and forgets p.
// Called on sign-out and node removal, while p is still known. Local keys
// and other principals are untouched.
func PurgeFleetCredentialsForSignOut(env string, p FleetPrincipal, mem *MemoryBackend) (int, error) {
	if !validEnv(env) {
		return 0, ErrInvalidEnv
	}
	if !p.valid() {
		return 0, ErrNoFleetPrincipal
	}
	fleetMu.Lock()
	defer fleetMu.Unlock()
	n, err := purgeAccountLocked(env, p, mem)
	if err != nil {
		return n, err
	}
	return n, unregisterPrincipalLocked(env, p)
}

// ReconcileFleetCredentialPrincipal is the session-change purge: given the
// principal now signed in, it removes every recorded principal that is a
// different realm or user (both namespaces), and, for the same user in a
// different org, that org's delivered keys (FleetNamespaceOrg). Synced user
// keys follow the user, not the org, and are kept. Returns how many
// credentials it removed.
func ReconcileFleetCredentialPrincipal(env string, current FleetPrincipal, mem *MemoryBackend) (int, error) {
	if !validEnv(env) {
		return 0, ErrInvalidEnv
	}
	if !current.valid() {
		return 0, ErrNoFleetPrincipal
	}
	fleetMu.Lock()
	defer fleetMu.Unlock()
	recorded, err := readPrincipalsLocked(env)
	if err != nil {
		return 0, err
	}
	var (
		total int
		keep  []FleetPrincipal
		errs  []error
	)
	for _, p := range recorded {
		switch {
		case !p.sameAccount(current):
			n, perr := purgeAccountLocked(env, p, mem)
			total += n
			if perr != nil {
				errs = append(errs, perr)
				keep = append(keep, p) // retry next time
			}
		case p.Org != current.Org:
			n, perr := purgeLocked(env, p, FleetNamespaceOrg, mem)
			total += n
			if perr != nil {
				errs = append(errs, perr)
				keep = append(keep, p)
				continue
			}
			keep = append(keep, current)
		default:
			keep = append(keep, p)
		}
	}
	if err := writePrincipalsLocked(env, dedupePrincipals(keep)); err != nil {
		errs = append(errs, err)
	}
	return total, errors.Join(errs...)
}

func purgeAccountLocked(env string, p FleetPrincipal, mem *MemoryBackend) (int, error) {
	n1, err1 := purgeLocked(env, p, FleetNamespaceOrg, mem)
	n2, err2 := purgeLocked(env, p, FleetNamespaceUser, mem)
	return n1 + n2, errors.Join(err1, err2)
}

func purgeLocked(env string, p FleetPrincipal, ns FleetNamespace, mem *MemoryBackend) (int, error) {
	ids, err := readIndexLocked(env, p, ns)
	if err != nil {
		return 0, err
	}
	var (
		removed int
		left    []string
		errs    []error
	)
	for _, id := range ids {
		loc, lerr := FleetCredentialLocator(ns, env, p, id)
		if lerr != nil {
			// An index entry that is not a valid id was never written
			// by Put; drop it.
			continue
		}
		if derr := deleteFleetEntryLocked(mem, loc); derr != nil {
			left = append(left, id)
			errs = append(errs, fmt.Errorf("%s: %w", id, derr))
			continue
		}
		removed++
	}
	if err := writeIndexLocked(env, p, ns, left); err != nil {
		errs = append(errs, err)
	}
	return removed, errors.Join(errs...)
}

func deleteFleetEntryLocked(mem *MemoryBackend, loc string) error {
	if !strings.HasPrefix(loc, fleetLocatorRoot) {
		// Structural guard: this code never deletes outside the Fleet
		// namespaces.
		return ErrInvalidFleetNamespace
	}
	if mem != nil {
		mem.ClearEntry(ref.RefKeychain, loc)
	}
	if err := keyring.Delete(keyringService, loc); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

func readJSONLocked(loc string, v any) (bool, error) {
	raw, err := keyring.Get(keyringService, loc)
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("secrets: read fleet credential record: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("secrets: parse fleet credential record: %w", err)
	}
	return true, nil
}

func writeJSONLocked(loc string, v any, empty bool) error {
	if empty {
		if err := keyring.Delete(keyringService, loc); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("secrets: clear fleet credential record: %w", err)
		}
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, loc, string(b)); err != nil {
		return fmt.Errorf("secrets: write fleet credential record: %w", err)
	}
	return nil
}

func readIndexLocked(env string, p FleetPrincipal, ns FleetNamespace) ([]string, error) {
	if !ns.valid() {
		return nil, ErrInvalidFleetNamespace
	}
	var ids []string
	_, err := readJSONLocked(fleetIndexLocator(ns, env, p), &ids)
	return ids, err
}

func writeIndexLocked(env string, p FleetPrincipal, ns FleetNamespace, ids []string) error {
	return writeJSONLocked(fleetIndexLocator(ns, env, p), ids, len(ids) == 0)
}

func readPrincipalsLocked(env string) ([]FleetPrincipal, error) {
	var ps []FleetPrincipal
	if _, err := readJSONLocked(fleetPrincipalsLocator(env), &ps); err != nil {
		return nil, err
	}
	out := ps[:0]
	for _, p := range ps {
		if p.valid() {
			out = append(out, p)
		}
	}
	return out, nil
}

func writePrincipalsLocked(env string, ps []FleetPrincipal) error {
	return writeJSONLocked(fleetPrincipalsLocator(env), ps, len(ps) == 0)
}

// registerPrincipalLocked records p (replacing a same-account record whose
// org differs, after that org's keys were purged by the reconcile).
func registerPrincipalLocked(env string, p FleetPrincipal) error {
	ps, err := readPrincipalsLocked(env)
	if err != nil {
		return err
	}
	for _, q := range ps {
		if q == p {
			return nil
		}
	}
	return writePrincipalsLocked(env, dedupePrincipals(append(ps, p)))
}

func unregisterPrincipalLocked(env string, p FleetPrincipal) error {
	ps, err := readPrincipalsLocked(env)
	if err != nil {
		return err
	}
	var keep []FleetPrincipal
	for _, q := range ps {
		if !q.sameAccount(p) {
			keep = append(keep, q)
		}
	}
	return writePrincipalsLocked(env, keep)
}

func dedupePrincipals(ps []FleetPrincipal) []FleetPrincipal {
	seen := map[FleetPrincipal]bool{}
	var out []FleetPrincipal
	for _, p := range ps {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
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
