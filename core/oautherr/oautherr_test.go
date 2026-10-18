package oautherr

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"strings"
	"testing"
)

func strayJWT() string {
	return "eyJ" + strings.Repeat("a", 30) + ".eyJ" + strings.Repeat("b", 30) + ".sig"
}

func randomToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 30)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b) // 40 chars
}

// Allowlist: only RFC codes and Zitadel message IDs survive. Every bypass
// the deny-list review found, plus a random 40-char token, is dropped.
func TestFields_AllowlistOnly(t *testing.T) {
	b32 := base32.StdEncoding.EncodeToString([]byte("secret-refresh-token-value"))
	bypasses := map[string]string{
		"stray JWT":             strayJWT(),
		"random 40-char":        randomToken(t),
		"colon-split":           "rt:AbCdEfGhIj:KlMnOpQrSt:UvWxYz0123",
		"unequal chunks":        "AbCd EfGhIjK LmN OpQrStUvW XyZ",
		"base32 split":          strings.Join([]string{b32[:8], b32[8:16], b32[16:24], b32[24:]}, " "),
		"long prose + token":    "The refresh token you supplied could not be validated by the server: " + randomToken(t),
		"url-encoded":           "tok%2Bab%2Fcd%3D",
		"plain prose":           "The refresh token is expired",
		"control chars":         `bad\u001b[31m client`,
		"zitadel id + suffix":   "Errors.User.NotFound " + randomToken(t),
		"Errors + 72 hex":       "Errors." + strings.Repeat("a1b2c3d4", 9),
		"Errors + JWT parts":    "Errors.eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w",
		"Errors + ghp token":    "Errors.Token.ghp" + strings.Repeat("A1", 20),
		"Errors + long segment": "Errors." + strings.Repeat("Ab", 20),
		"Errors + 5 segments":   "Errors.A.B.C.D.E",
	}
	for name, d := range bypasses {
		code, desc := Fields([]byte(`{"error":"invalid_grant","error_description":"` + d + `"}`))
		if code != "invalid_grant" || desc != "" {
			t.Errorf("%s: got %q, %q; want invalid_grant with the description dropped", name, code, desc)
		}
	}
	for _, id := range []string{"Errors.User.NotFound", "Errors.Token.Invalid", "Errors.App.NotExisting", "Errors.OIDCSession.RefreshTokenInvalid"} {
		if _, desc := Fields([]byte(`{"error":"invalid_grant","error_description":"` + id + `"}`)); desc != id {
			t.Errorf("Zitadel ID %q was dropped (%q)", id, desc)
		}
	}
}

func TestFields_Codes(t *testing.T) {
	for _, c := range []string{"invalid_grant", "invalid_client", "authorization_pending", "slow_down", "invalid_client_metadata", "invalid_token"} {
		if got, _ := Fields([]byte(`{"error":"` + c + `"}`)); got != c {
			t.Errorf("standard code %q became %q", c, got)
		}
	}
	for _, c := range []string{"Bad " + strayJWT(), "custom_code", randomToken(t)} {
		if got, _ := Fields([]byte(`{"error":"` + c + `"}`)); got != UnknownCode {
			t.Errorf("non-standard code %q became %q, want %q", c, got, UnknownCode)
		}
	}
	if code, desc := Fields([]byte(`<html>` + strayJWT() + `</html>`)); code != "" || desc != "" {
		t.Errorf("html page: got %q, %q", code, desc)
	}
}

func TestSummary(t *testing.T) {
	if got := Summary(400, []byte(`{"error":"invalid_grant","error_description":"Errors.User.NotFound"}`)); got != "status 400: invalid_grant: Errors.User.NotFound" {
		t.Errorf("got %q", got)
	}
	if got := Summary(502, []byte(`<html>`+strayJWT()+`</html>`)); got != "status 502" {
		t.Errorf("got %q", got)
	}
}
