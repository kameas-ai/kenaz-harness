package oautherr

import (
	"strings"
	"testing"
)

func strayJWT() string {
	return "eyJ" + strings.Repeat("a", 30) + ".eyJ" + strings.Repeat("b", 30) + ".sig"
}

func TestFields_KeepsOnlySafeErrorFields(t *testing.T) {
	stray := strayJWT()
	for _, tc := range []struct{ name, body, code, desc string }{
		{"oauth error + stray token field", `{"error":"invalid_grant","error_description":"code expired","access_token":"` + stray + `"}`, "invalid_grant", "code expired"},
		{"html page", `<html>proxy ` + stray + `</html>`, "", ""},
		{"bad code", `{"error":"Bad ` + stray + `"}`, "", ""},
		{"token inside description", `{"error":"invalid_grant","error_description":"refresh token ` + stray + ` is revoked"}`, "invalid_grant", "(details withheld)"},
		{"control and ANSI chars", "{\"error\":\"invalid_client\",\"error_description\":\"bad\\u001b[31m client\\nsecret\"}", "invalid_client", "bad[31m client secret"},
		{"long description capped", `{"error":"invalid_request","error_description":"` + strings.Repeat("ab ", 100) + `"}`, "invalid_request", strings.TrimSpace(strings.Repeat("ab ", 100))[:maxDesc] + "..."},
	} {
		code, desc := Fields([]byte(tc.body))
		if code != tc.code || desc != tc.desc {
			t.Errorf("%s: got %q, %q; want %q, %q", tc.name, code, desc, tc.code, tc.desc)
		}
		if strings.Contains(code+desc, stray) || strings.ContainsRune(desc, 0x1b) {
			t.Errorf("%s: unsafe content survived: %q", tc.name, desc)
		}
	}
}

func TestSummary(t *testing.T) {
	if got := Summary(400, []byte(`{"error":"invalid_grant"}`)); got != "status 400: invalid_grant" {
		t.Errorf("got %q", got)
	}
	if got := Summary(502, []byte(`<html>`+strayJWT()+`</html>`)); got != "status 502" {
		t.Errorf("got %q", got)
	}
}

// #431 delta review: the 20-char run rule alone could be beaten by splitting
// or encoding a token. Each bypass is withheld, and plain prose survives.
func TestFields_DescriptionBypassesWithheld(t *testing.T) {
	jwt := strayJWT()
	chunk := func(s, sep string, n int) string {
		var parts []string
		for len(s) > n {
			parts = append(parts, s[:n])
			s = s[n:]
		}
		return strings.Join(append(parts, s), sep)
	}
	for name, d := range map[string]string{
		"space-split JWT":   chunk(jwt, " ", 7),
		"colon-split JWT":   chunk(jwt, ":", 7),
		"url-encoded token": "tok%2Bab%2Fcd%3D",
		"letters-only key":  "your key ABCDEFGHIJKLMNOPQRST is bad",
		"digit-heavy":       "code 1234 5678 9012",
	} {
		_, got := Fields([]byte(`{"error":"invalid_grant","error_description":"` + d + `"}`))
		if got != "(details withheld)" {
			t.Errorf("%s: description %q survived as %q", name, d, got)
		}
	}
	for _, prose := range []string{"The refresh token is expired", "Client authentication failed (unknown client)", "Invalid redirect URI"} {
		if _, got := Fields([]byte(`{"error":"invalid_grant","error_description":"` + prose + `"}`)); got != prose {
			t.Errorf("plain prose %q was withheld (%q)", prose, got)
		}
	}
}
