package fleet

// device_keys_stepup.go — step-up re-authentication for device-key
// replacement (kenaz-fleet ER-40; contract §10.1 "Device-key changes in ADR
// 0001 phase 0").
//
// Fleet gates a key REPLACEMENT (a different key for a purpose that already
// has an active key on this node) behind a recent interactive sign-in. Zitadel
// access tokens carry no auth_time, so the harness proves it with an OIDC ID
// token from a max_age=0 re-authentication by the SAME client, sent in the
// X-Kenaz-Step-Up-Token header (azp = our client, sub = the caller, fresh
// auth_time, non-empty amr, no act). Under FLEET_NODE_KEYS_STEP_UP=enforce,
// without it:
//
//	PUT  /api/v1/me/nodes/{id}/keys → 403 reauth_required (nothing written)
//	POST /api/v1/enroll             → 200, active key kept,
//	                                  keys_reauth_required: true
//
// The 403 is deliberately not a 401: refreshing the access token proves
// nothing, so it must never be retried by refresh. The harness instead asks
// the user to sign in again (Settings → Account), runs StepUpFlow, and retries
// the PUT exactly once (settings view, FleetDeviceKeysReauth).
//
// The step-up ID token is held IN MEMORY ONLY on the Client (never the
// keychain, never logged) and only until Fleet's freshness window closes; it
// is dropped as soon as the retry it was minted for has run.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// StepUpHeader carries the step-up OIDC ID token (fleet contract §10.1).
const StepUpHeader = "X-Kenaz-Step-Up-Token"

// stepUpWindow is Fleet's default FLEET_NODE_KEYS_STEP_UP_MAX_AGE: how long
// after the re-authentication Fleet still accepts the ID token. The held
// token is dropped locally once it is surely stale.
const stepUpWindow = 10 * time.Minute

// stepUpClockSkew mirrors Fleet's one-minute future tolerance on auth_time.
const stepUpClockSkew = time.Minute

// KeyRegReauthRequired: Fleet kept this device's previous active key because
// replacing it needs a fresh sign-in (403 reauth_required on PUT keys, or
// keys_reauth_required on enroll). Recoverable: sign in again and retry.
const KeyRegReauthRequired = "reauth_required"

// keyRegReauthCopy is the user-facing copy for KeyRegReauthRequired (shown
// verbatim by Settings → Account, which has the button; the shared-sessions
// inbox adds its own pointer to Settings → Account).
const keyRegReauthCopy = "Sign in again to update this device's keys. Until you do, sessions shared with this device may not open here."

// KeyRegReauthRefused: a fresh sign-in WAS presented and Fleet still refused
// the replacement (or the identity provider ignored the request for a fresh
// sign-in). Signing in again will not help on its own: Fleet requires an amr
// claim that Zitadel does not emit for SSO-only (external IdP) sessions until
// a Fleet setting is turned on. Distinct from reauth_required so the UI says
// "contact your administrator" instead of offering a retry that will fail,
// and sticky: a later tokenless enroll (app start, identity refresh) does NOT
// downgrade it, including after a restart (persisted per account,
// device_keys_refused.go). Cleared by a successful registration or a
// different account.
const KeyRegReauthRefused = "reauth_refused"

// keyRegRefusedCopy is the user-facing copy for KeyRegReauthRefused.
const keyRegRefusedCopy = "Your organization's sign-in can't confirm this change yet. Contact your administrator."

// ErrKeysReauthRequired: Fleet refused to replace an active device key
// without a fresh sign-in. The previous key stays active; the state is
// recorded in KeyRegistration (status reauth_required).
var ErrKeysReauthRequired = errors.New("fleet: sign in again to update this device's keys")

// ErrStepUpRefused: a fresh sign-in was presented but cannot confirm the key
// change (Fleet refused it, or its auth_time is already too old). Wraps
// ErrKeysReauthRequired. Records status reauth_refused.
var ErrStepUpRefused = fmt.Errorf("%w: your organization's sign-in can't confirm this change yet", ErrKeysReauthRequired)

// ErrStepUpNoIDToken: the re-authentication returned no ID token (the
// openid scope missing, or the IdP declined to issue one).
var ErrStepUpNoIDToken = errors.New("fleet: the sign-in did not return an ID token")

// ErrStepUpWrongAccount: the user re-authenticated as a different account
// than the one this device is signed in with.
var ErrStepUpWrongAccount = errors.New("fleet: you signed in with a different account than the one this device uses")

// stepUpScopes is the scope set for the step-up sign-in: the regular scopes
// minus offline_access, so the throwaway re-authentication mints no refresh
// token (only its ID token is used).
func stepUpScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s != "offline_access" {
			out = append(out, s)
		}
	}
	return out
}

// stepUpFlowFn is the browser re-authentication, a seam for tests.
var stepUpFlowFn = stepUpFlow

// StepUpFlow runs an interactive re-authentication (max_age=0,
// prompt=login) through the same PKCE loopback flow as sign-in and returns
// ONLY the ID token. The access token it also yields is discarded and
// nothing is persisted: the caller holds the ID token in memory for one
// step-up retry (Client.HoldStepUpToken). Cancellable through ctx.
func StepUpFlow(ctx context.Context, profile EnvProfile) (string, error) {
	return stepUpFlowFn(ctx, profile)
}

func stepUpFlow(ctx context.Context, profile EnvProfile) (string, error) {
	extra := url.Values{"max_age": {"0"}, "prompt": {"login"}}
	ts, err := authCodeFlow(ctx, profile, stepUpScopes(profile.OIDCScopes), extra)
	if err != nil {
		return "", err
	}
	if ts.IDToken == "" {
		return "", ErrStepUpNoIDToken
	}
	return ts.IDToken, nil
}

// idTokenClaims are the unverified claims the harness reads from an ID token
// to schedule and sanity-check a step-up (Fleet verifies the signature).
type idTokenClaims struct {
	Sub      string `json:"sub"`
	AuthTime int64  `json:"auth_time"`
	Exp      int64  `json:"exp"`
}

func parseIDTokenClaims(raw string) (idTokenClaims, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return idTokenClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return idTokenClaims{}, false
		}
	}
	var c idTokenClaims
	if json.Unmarshal(payload, &c) != nil {
		return idTokenClaims{}, false
	}
	return c, true
}

// stepUpHolder keeps at most one step-up ID token in memory.
type stepUpHolder struct {
	mu      sync.Mutex
	token   string
	expires time.Time
	now     func() time.Time // nil → time.Now (tests)
}

func (h *stepUpHolder) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *stepUpHolder) set(token string, expires time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.token, h.expires = token, expires
}

func (h *stepUpHolder) get() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.token == "" {
		return ""
	}
	if !h.clock().Before(h.expires) {
		h.token, h.expires = "", time.Time{}
		return ""
	}
	return h.token
}

func (h *stepUpHolder) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.token, h.expires = "", time.Time{}
}

// HoldStepUpToken validates and holds (in memory only) an ID token from a
// max_age=0 re-authentication for the next key-registration call(s).
//
// The token's sub must match the signed-in access token's sub, else
// ErrStepUpWrongAccount (Fleet would refuse it anyway; this gives the user a
// clear reason). A token whose auth_time is missing or already older than
// Fleet's freshness window (an IdP that ignored prompt=login / max_age=0) is
// refused locally — it is bound to fail — with ErrStepUpRefused and status
// reauth_refused. Otherwise it is held until auth_time + the window, capped
// by its exp.
func (c *Client) HoldStepUpToken(idToken string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	claims, ok := parseIDTokenClaims(idToken)
	if !ok || claims.Sub == "" {
		return ErrStepUpNoIDToken
	}
	if cur := ReadTokenState().Claims.Subject; cur != "" && cur != claims.Sub {
		return ErrStepUpWrongAccount
	}
	now := c.stepUp.clock()
	at := time.Unix(claims.AuthTime, 0)
	if claims.AuthTime <= 0 || now.Sub(at) > stepUpWindow {
		c.setReauthRefused()
		return ErrStepUpRefused
	}
	expires := at.Add(stepUpWindow)
	if at.After(now) {
		// Local clock behind the IdP's: Fleet's clock decides.
		expires = now.Add(stepUpWindow)
	}
	if claims.Exp > 0 {
		if exp := time.Unix(claims.Exp, 0); exp.Before(expires) {
			expires = exp
		}
	}
	c.stepUp.set(idToken, expires)
	return nil
}

// OfferSignInIDToken holds the ID token of a regular interactive sign-in as a
// step-up proof, but only when its auth_time shows the user really just
// authenticated (an SSO-session reuse carries an old auth_time and is
// ignored). This lets the enroll that follows a sign-in replace a key
// without a second prompt. Silently a no-op otherwise.
func (c *Client) OfferSignInIDToken(idToken string) {
	if c == nil || c.isNop || idToken == "" {
		return
	}
	claims, ok := parseIDTokenClaims(idToken)
	if !ok || claims.AuthTime <= 0 {
		return
	}
	now := c.stepUp.clock()
	at := time.Unix(claims.AuthTime, 0)
	if at.After(now.Add(stepUpClockSkew)) || now.Sub(at) > stepUpWindow {
		return
	}
	_ = c.HoldStepUpToken(idToken)
}

// ClearStepUpToken drops any held step-up token.
func (c *Client) ClearStepUpToken() {
	if c == nil {
		return
	}
	c.stepUp.clear()
}

// heldStepUpToken returns the held token while it is still fresh, else "".
func (c *Client) heldStepUpToken() string {
	if c == nil {
		return ""
	}
	return c.stepUp.get()
}

// isReauthRequired reports Fleet's 403 reauth_required envelope.
func isReauthRequired(status int, body []byte) bool {
	if status != 403 {
		return false
	}
	env, ok := parseFleetError(body)
	return ok && env.Code == "reauth_required"
}

// setReauthRequired records Fleet's refusal to replace a key. sentProof
// says whether a step-up token was sent with the refused call: then the
// fresh sign-in itself was refused (reauth_refused). Without one, it is the
// recoverable reauth_required — unless this same account was already
// refused after a fresh sign-in, which a tokenless call must not downgrade.
func (c *Client) setReauthRequired(sentProof bool) {
	if sentProof {
		c.setReauthRefused()
		return
	}
	// After a restart the refusal comes from disk (device_keys_refused.go).
	c.loadPersistedRefusal()
	c.keyReg.mu.Lock()
	sticky := c.keyReg.last.Status == KeyRegReauthRefused &&
		c.keyReg.refusedFor != "" && c.keyReg.refusedFor == currentSubject()
	c.keyReg.mu.Unlock()
	if sticky {
		return
	}
	c.setKeyReg(KeyRegistration{Status: KeyRegReauthRequired, Message: keyRegReauthCopy})
}

// setReauthRefused records reauth_refused for the signed-in account, in
// memory and on disk (so a restart keeps it; device_keys_refused.go).
func (c *Client) setReauthRefused() {
	c.loadPersistedRefusal()
	sub := currentSubject()
	at := time.Now().UTC()
	c.keyReg.mu.Lock()
	c.keyReg.last = KeyRegistration{Status: KeyRegReauthRefused, Message: keyRegRefusedCopy, At: at}
	c.keyReg.refusedFor = sub
	c.keyReg.mu.Unlock()
	c.persistReauthRefused(sub, at)
}

// currentSubject is the signed-in access token's sub ("" when unknown).
func currentSubject() string {
	return ReadTokenState().Claims.Subject
}

// errReauth is the error for a refused key replacement.
func errReauth(sentProof bool) error {
	if sentProof {
		return ErrStepUpRefused
	}
	return ErrKeysReauthRequired
}
