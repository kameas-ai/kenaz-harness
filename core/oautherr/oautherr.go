// Package oautherr reduces an OAuth token-endpoint error body to the parts
// that are safe to log or return. A token endpoint's response is
// credential-bearing by nature, and a misbehaving IdP or proxy can put
// anything in an error body, so callers must never log or wrap the raw body.
//
// It is an ALLOWLIST, not a deny-list: a deny-list of "token-looking" text
// is always beatable (split, encoded or prefixed tokens), so nothing from
// the body survives unless it matches a known-safe shape.
package oautherr

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// knownCodes are the error codes registered by RFC 6749 §5.2/§4.1.2.1,
// RFC 6750 §3.1, RFC 8628 §3.5 and RFC 7591 §3.2.2. Any other `error`
// value is reported as UnknownCode.
var knownCodes = map[string]bool{
	// RFC 6749
	"invalid_request": true, "invalid_client": true, "invalid_grant": true,
	"unauthorized_client": true, "unsupported_grant_type": true, "invalid_scope": true,
	"access_denied": true, "unsupported_response_type": true,
	"server_error": true, "temporarily_unavailable": true,
	// RFC 6750
	"invalid_token": true, "insufficient_scope": true,
	// RFC 8628
	"authorization_pending": true, "slow_down": true, "expired_token": true,
	// RFC 7591
	"invalid_redirect_uri": true, "invalid_client_metadata": true,
	"invalid_software_statement": true, "unapproved_software_statement": true,
}

// UnknownCode stands in for an `error` value outside knownCodes.
const UnknownCode = "unknown_error"

// zitadelID is the only error_description shape kept: Zitadel's message
// IDs, e.g. "Errors.User.NotFound". Each segment is letters only and starts
// upper case, at most 32 characters, with at most 4 segments, so hex,
// JWT-style and vendor tokens can't hide in a segment. Anything else is
// dropped.
var zitadelID = regexp.MustCompile(`^Errors(\.[A-Z][A-Za-z]{0,31}){1,4}$`)

// maxDescLen caps a kept Zitadel ID.
const maxDescLen = 64

// Fields extracts RFC 6749 §5.2 `error` and `error_description` from body,
// keeping only allowlisted values:
//   - code is a known RFC code, UnknownCode for any other non-empty
//     `error`, or "" when the body is not an OAuth error object;
//   - desc is kept only if it is a Zitadel message ID (≤ 64 chars), else "".
//
// Nothing else from the body is ever returned.
func Fields(body []byte) (code, desc string) {
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error == "" {
		return "", ""
	}
	code = UnknownCode
	if knownCodes[e.Error] {
		code = e.Error
	}
	if len(e.ErrorDescription) <= maxDescLen && zitadelID.MatchString(e.ErrorDescription) {
		desc = e.ErrorDescription
	}
	return code, desc
}

// Summary is "status N" plus ": code" and ": desc" when present. Use it in
// returned errors and log fields instead of the body.
func Summary(status int, body []byte) string {
	code, desc := Fields(body)
	switch {
	case code == "":
		return fmt.Sprintf("status %d", status)
	case desc == "":
		return fmt.Sprintf("status %d: %s", status, code)
	default:
		return fmt.Sprintf("status %d: %s: %s", status, code, desc)
	}
}
