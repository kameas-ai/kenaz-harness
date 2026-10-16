package fleet

import (
	"strings"
	"testing"
)

// The token-endpoint error path keeps only the OAuth error fields: a
// non-200 body that carries anything else (a stray token, an HTML page) is
// never logged or returned.
func TestOAuthErrorFields_KeepsOnlyErrorFields(t *testing.T) {
	stray := "eyJ" + strings.Repeat("a", 30) + ".eyJ" + strings.Repeat("b", 30) + ".sig"
	for _, tc := range []struct {
		body, code, desc string
	}{
		{`{"error":"invalid_grant","error_description":"code expired","access_token":"` + stray + `"}`, "invalid_grant", "code expired"},
		{`<html>proxy error ` + stray + `</html>`, "", ""},
		{`{"error":"Bad Thing ` + stray + `"}`, "", ""},
		{`{"error":"invalid_client","error_description":"` + strings.Repeat("x", 300) + `"}`, "invalid_client", strings.Repeat("x", 200) + "…"},
	} {
		code, desc := oauthErrorFields([]byte(tc.body))
		if code != tc.code || desc != tc.desc {
			t.Errorf("oauthErrorFields(%.40q) = %q, %.20q; want %q, %.20q", tc.body, code, desc, tc.code, tc.desc)
		}
		if strings.Contains(code+desc, stray) {
			t.Errorf("a stray token survived: %q %q", code, desc)
		}
	}
}
