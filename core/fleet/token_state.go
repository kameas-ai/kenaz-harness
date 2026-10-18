package fleet

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// TokenState is the local, network-free view of the stored fleet session
// tokens (fleet-session-truth-01DOGF0A FR-1/FR-3).
//
// It exists because "am I signed in" had three answers in this codebase:
// Client.SignedIn (token expiry), a successful enroll, and the JWT's claims.
// The FleetSession snapshot (core/rpc/views/settings/fleet_session.go) keeps
// those as separate, named facts instead of collapsing them into one bool —
// "the tokens are valid" and "enroll succeeded" and "the token carries an org
// claim" are three different things, and the UI lied whenever it treated one
// as another (dogfood 2026-10-04 F5 / B3a).
type TokenState struct {
	// Present is true when an access token is stored (keychain, or the
	// external/brokered source in served mode).
	Present bool
	// AccessValid is true when the access token has no known expiry, or its
	// expiry is outside the grace window. Same arithmetic as Client.SignedIn.
	AccessValid bool
	// Refreshable is true when the session can be extended without the user:
	// a refresh token is stored, or renewal is externally owned (served mode).
	Refreshable bool
	// ExpiresAt is the stored access-token expiry (zero when unknown).
	ExpiresAt time.Time
	// Claims are the unverified identity claims the access token asserts.
	Claims TokenClaims
}

// Usable reports whether the stored session can still authenticate a
// request: a valid access token, or an expired one that can be refreshed.
// Token absence, or expiry with no way to refresh, is the only "signed out".
func (s TokenState) Usable() bool {
	return s.Present && (s.AccessValid || s.Refreshable)
}

// TokenClaims are the identity claims decoded (NOT verified — fleet verifies
// on every request) from the access token.
type TokenClaims struct {
	Subject string // sub
	OrgID   string // Zitadel resource-owner claim
	Issuer  string // iss
	// Email / Name are the standard OIDC claims, present only when the IdP
	// was asked for the email/profile scopes AND chose to put them in the
	// access token. Used as the FR-9 fallback when enroll omits them.
	Email string
	Name  string
}

// ReadTokenState loads the stored tokens and derives the local session
// state. It never touches the network. An unreadable store reads as
// "not present", which is the same answer Client.SignedIn gives.
func ReadTokenState() TokenState {
	ts, err := LoadTokens()
	if err != nil || ts.AccessToken == "" {
		return TokenState{}
	}
	st := TokenState{Present: true, ExpiresAt: ts.ExpiresAt}
	st.AccessValid = ts.ExpiresAt.IsZero() || !time.Now().Add(tokenExpiryGrace).After(ts.ExpiresAt)
	_, external := externalTokens()
	st.Refreshable = ts.RefreshToken != "" || external
	st.Claims = claimsFromJWT(ts.AccessToken)
	return st
}

// claimsFromJWT decodes the identity claims from a JWT payload. Malformed
// tokens (opaque access tokens are legal) yield zero claims, not an error:
// absence of a claim is exactly the fact the caller wants to report.
func claimsFromJWT(token string) TokenClaims {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return TokenClaims{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return TokenClaims{}
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return TokenClaims{}
	}
	str := func(k string) string {
		v, ok := raw[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}
	return TokenClaims{
		Subject: str("sub"),
		OrgID:   str(zitadelResourceOwnerClaim),
		Issuer:  str("iss"),
		Email:   str("email"),
		Name:    str("name"),
	}
}
