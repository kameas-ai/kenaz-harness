// Package fleet provides fleet-integration components including the
// tokenRoundTripper used by the OTLP export pipeline (spec
// harness-fleet-otlp-export-01NTLMEX01 FR-001/FR-002).
package fleet

import (
	"fmt"
	"net/http"
)

// BearerProvider is a function that returns the current Bearer token.
// Returning ("", nil) means "no token yet" (pre-login); the caller
// should skip the request. Returning ("", err) means an unrecoverable
// error.
type BearerProvider func() (string, error)

// DefaultBearerProvider returns a BearerProvider that reads the current
// access token from the OS keychain via LoadTokens.
func DefaultBearerProvider() BearerProvider {
	return func() (string, error) {
		ts, err := LoadTokens()
		if err != nil {
			// ErrTokensNotFound / ErrNotSignedIn — pre-login or signed-out.
			return "", nil //nolint:nilerr // empty token = skip, not error
		}
		return ts.AccessToken, nil
	}
}

// tokenRoundTripper implements http.RoundTripper for the fleet OTLP export
// pipeline. On each call it reads the current access token from the
// BearerProvider, injects "Authorization: Bearer <token>", and forwards
// the request to the underlying transport.
//
// Constraints (per spec FR-001/FR-002 and contract §1.2):
//   - Token is re-read on EVERY call (no caching) so a concurrent
//     SaveTokens refresh is immediately visible to the next flush.
//   - When the provider returns an empty token (pre-login / signed out),
//     the request is SKIPPED: a typed errNoToken is returned. The OTLP
//     exporter treats this as a transient error and logs a warning; the
//     ring-buffer remains intact and the next flush retries.
//   - The underlying transport defaults to http.DefaultTransport.
type tokenRoundTripper struct {
	bearer    BearerProvider
	transport http.RoundTripper
}

// errNoToken is returned by tokenRoundTripper when no token is available.
// It is a sentinel for "pre-login or signed out" — the caller (OTLP
// exporter flush path) logs a warning and returns without clearing the
// ring buffer.
type errNoToken struct{}

func (e errNoToken) Error() string { return "fleet/otlp: no bearer token (not signed in)" }

// NewTokenRoundTripper constructs a tokenRoundTripper. When bearer is nil,
// DefaultBearerProvider is used. When transport is nil,
// http.DefaultTransport is used.
func NewTokenRoundTripper(bearer BearerProvider, transport http.RoundTripper) http.RoundTripper {
	if bearer == nil {
		bearer = DefaultBearerProvider()
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &tokenRoundTripper{bearer: bearer, transport: transport}
}

// RoundTrip implements http.RoundTripper.
func (t *tokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := t.bearer()
	if err != nil {
		return nil, fmt.Errorf("fleet/otlp: bearer provider: %w", err)
	}
	if tok == "" {
		return nil, errNoToken{}
	}
	// Clone the request so we don't mutate the caller's headers.
	r2 := req.Clone(req.Context())
	r2.Header.Set("Authorization", "Bearer "+tok)
	return t.transport.RoundTrip(r2)
}
