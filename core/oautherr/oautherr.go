// Package oautherr reduces an OAuth token-endpoint error body to the parts
// that are safe to log or return. A token endpoint's response is
// credential-bearing by nature, and a misbehaving IdP or proxy can put
// anything in an error body (a stray token, an HTML page, terminal escape
// codes), so callers must never log or wrap the raw body.
package oautherr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// maxDesc is the error_description cap, in bytes. The text is ASCII once
// sanitised.
const maxDesc = 120

// tokenish matches a run that looks like a credential: 20+ characters from
// the base64 / base64url / JWT alphabet. A description containing one is
// dropped whole. Over-redacting a human message is fine; leaking a token
// is not.
var tokenish = regexp.MustCompile(`[A-Za-z0-9._~+/=-]{20,}`)

// Fields extracts RFC 6749 §5.2 `error` and `error_description` from body.
// code must be a short [a-z_] token, otherwise both are "". desc keeps
// printable ASCII only, is dropped if anything in it looks like a
// credential, and is capped at maxDesc. Nothing else from the body is
// returned.
func Fields(body []byte) (code, desc string) {
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error == "" || len(e.Error) > 64 {
		return "", ""
	}
	for _, r := range e.Error {
		if (r < 'a' || r > 'z') && r != '_' {
			return "", ""
		}
	}
	var b strings.Builder
	for _, r := range e.ErrorDescription {
		if r >= 0x20 && r <= 0x7e {
			b.WriteRune(r)
		} else if r == '\n' || r == '\t' || r == '\r' {
			b.WriteByte(' ')
		}
	}
	d := strings.TrimSpace(b.String())
	if tokenish.MatchString(d) {
		d = "(details withheld)"
	}
	if len(d) > maxDesc {
		d = d[:maxDesc] + "..."
	}
	return e.Error, d
}

// Summary is "status N" plus ": code: desc" when the body is an OAuth error
// object. Use it in returned errors and log fields instead of the body.
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
