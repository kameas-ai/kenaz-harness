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
