// Package toolloop — confirmation grants and the headless policy
// (confirm-each-enforcement-01PMAG05 WP03 + WP05).
//
// A `confirm_each` verdict asks the user. This file holds the two ways
// an answer can outlive the question, and the one way the question can
// be answered when there is nobody to ask.
//
//   - SessionGrantCache — "allow for this session". Per-tool,
//     per-session, process-lifetime, NEVER written to disk (owner
//     decision 2). Lost on restart, by design.
//   - PersistentGrantStore — "always allow". A durable, revocable rule
//     the chassis writes; the interface lives here so this package keeps
//     its stdlib-only posture and the Cedar file writer stays in
//     core/policy/cedar.
//   - HeadlessConfirmPolicy — what a `confirm_each` verdict means when
//     no prompt channel exists at all (owner decision 4). Default deny.
//
// None of these can widen past an explicit Cedar deny: the deny verdict
// short-circuits before any of this is consulted (FR-005).
package toolloop

import (
	"os"
	"strings"
	"sync"
)

// ---- session grants ("allow for this session") ----

// SessionGrantCache records per-(session, server, tool) approvals for
// the lifetime of the process.
//
// It is deliberately not a store: there is no path from this type to the
// filesystem, and there is no expiry other than process exit. "Allow for
// this session" is the low-commitment answer — the user who wants
// durability reaches for the visually-separated "always allow" control,
// which goes through PersistentGrantStore instead.
//
// Safe for concurrent use: dispatch fans out across a worker pool.
type SessionGrantCache struct {
	mu      sync.RWMutex
	granted map[string]struct{}
}

// NewSessionGrantCache returns an empty cache.
func NewSessionGrantCache() *SessionGrantCache {
	return &SessionGrantCache{granted: make(map[string]struct{})}
}

// grantKey is the cache key. NUL separators: none of the three
// components can contain one.
func grantKey(sessionID, server, tool string) string {
	return sessionID + "\x00" + server + "\x00" + tool
}

// Grant records that (server, tool) is approved for the rest of
// sessionID. Idempotent.
func (c *SessionGrantCache) Grant(sessionID, server, tool string) {
	if c == nil || tool == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.granted == nil {
		c.granted = make(map[string]struct{})
	}
	c.granted[grantKey(sessionID, server, tool)] = struct{}{}
}

// Has reports whether (server, tool) already carries a session grant.
// A nil cache reports false — no cache means no grants, which prompts.
func (c *SessionGrantCache) Has(sessionID, server, tool string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.granted[grantKey(sessionID, server, tool)]
	return ok
}

// RevokeSession drops every grant held for one session.
//
// Its production caller is session deletion: the chassis wires it as
// the sessions view's delete hook (core/rpc/api.go via
// sessions.WithDeleteHookOpt), so a deleted-then-recreated session id
// cannot inherit the approvals of a session the user threw away.
// (This closes confirm-each-enforcement-01PMAG05 review finding 7,
// which shipped with this method documented as having no caller;
// wired by the 2026-08-13 adversarial review.)
func (c *SessionGrantCache) RevokeSession(sessionID string) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := sessionID + "\x00"
	n := 0
	for k := range c.granted {
		if strings.HasPrefix(k, prefix) {
			delete(c.granted, k)
			n++
		}
	}
	return n
}

// Count reports the number of live grants. Test and telemetry helper.
func (c *SessionGrantCache) Count() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.granted)
}

// ---- persistent grants ("always allow") ----

// PersistentGrantStore is the durable half of owner decision 2: a rule
// that survives a restart and is revocable from the Settings surface
// that lists the user's accumulated grants.
//
// The interface is narrow on purpose. Implementations write whatever the
// deployment's policy substrate is (the desktop chassis writes a Cedar
// permit snippet under <DataDir>/policy/, which the permissions view
// already lists and revokes); this package never learns what that is.
//
// Both methods must be safe for concurrent use.
type PersistentGrantStore interface {
	// HasGrant reports whether a durable allow rule already covers
	// (server, tool). Consulted before prompting.
	HasGrant(server, tool string) bool

	// WriteGrant persists an allow rule for (server, tool). Must be
	// idempotent: the user can hit "always allow" twice.
	WriteGrant(server, tool string) error
}

// ---- headless policy ----

// HeadlessConfirmPolicy is what a `confirm_each` verdict resolves to
// when there is no prompt channel — served deployments with no attached
// UI, CI runs, embedded harnesses (owner decision 4).
//
// The zero value is HeadlessDeny. That is load-bearing: a deployment
// that forgets to configure this gets the safe answer, and the unsafe
// answer is only ever reachable by an operator typing it out.
type HeadlessConfirmPolicy string

const (
	// HeadlessDeny denies the call and returns a tool error the model
	// can read. The default.
	HeadlessDeny HeadlessConfirmPolicy = "deny"

	// HeadlessAllow dispatches the call without asking. This is the
	// behaviour the whole mission exists to eliminate as an *implicit*
	// default; as an explicit, audited, per-deployment operator choice
	// it is legitimate (a trusted CI runner, say).
	HeadlessAllow HeadlessConfirmPolicy = "allow"
)

// EnvConfirmEachHeadless is the environment variable that selects the
// headless policy for a deployment. Values are "deny" (default) and
// "allow"; anything else — including a typo — falls back to deny and is
// logged by the wiring layer.
//
// Setting this variable to a recognised value is also the operator's
// DECLARATION that the deployment is headless: the wiring layer
// (core/rpc) then applies the policy to every confirm_each verdict even
// though a prompt channel exists structurally (the broker publisher is
// always attached in a shipped binary, so channel presence alone cannot
// distinguish a workbench from a server with no human). Leave it unset
// in interactive deployments — unset means prompt-first, park-on-answer.
const EnvConfirmEachHeadless = "HARNESS_CONFIRM_EACH_HEADLESS"

// ParseHeadlessConfirmPolicy maps a configured string onto the policy.
// Unrecognised, empty, and whitespace-only inputs all return
// (HeadlessDeny, false) so callers can tell "operator chose deny" from
// "operator chose nothing recognisable" and log the latter.
func ParseHeadlessConfirmPolicy(s string) (HeadlessConfirmPolicy, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(HeadlessDeny):
		return HeadlessDeny, true
	case string(HeadlessAllow):
		return HeadlessAllow, true
	default:
		return HeadlessDeny, false
	}
}

// HeadlessConfirmPolicyFromEnv reads EnvConfirmEachHeadless. Returns
// (policy, recognised, raw) so the wiring layer can warn on a value it
// did not understand rather than silently denying everything for a
// reason the operator cannot see.
func HeadlessConfirmPolicyFromEnv() (policy HeadlessConfirmPolicy, recognised bool, raw string) {
	raw = os.Getenv(EnvConfirmEachHeadless)
	if strings.TrimSpace(raw) == "" {
		// Unset is not a misconfiguration: it is the documented default.
		return HeadlessDeny, true, raw
	}
	p, ok := ParseHeadlessConfirmPolicy(raw)
	return p, ok, raw
}
