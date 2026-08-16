package export

// Over-redaction coverage (adversarial review, 2026-08-16).
//
// The leak tests in redact_leak_test.go can only get MORE aggressive: every
// one of them passes harder as the catalog widens, and a catalog that
// replaced the whole document with `<REDACTED:…>` would score a perfect
// green. This file is the other half of the contract — the assertion that
// ordinary technical content comes out of an export unchanged.
//
// It exists because the widened catalog shipped one real false positive that
// no test in this package could see. `\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z
// 0-9_-]{20,}\b` folded the prefixed and BARE OpenAI shapes into one pattern
// and so applied the permissive `_-` body to the prefixless case, which made
// `sk-button-primary-large-variant-x` a credential. core/sentry/redactor.go
// and core/fleet/telemetry_redactor.go both keep those two split, precisely
// because the bare shape is base62; the split is now restored here.
//
// The three inputs listed as knownAggressive are the ones that DO get a
// marker. Each reads literally as `password=<something>`, each behaved the
// same way before this change (verified against 146d9e54), and each is a case
// where redacting is the defensible answer even though the content is code.
// They are named rather than silently tolerated so a future widening that
// adds a fourth has to come here and say so.

import (
	"sort"
	"strings"
	"testing"
)

// knownAggressive are inputs that legitimately trip the scanner. Pre-existing
// on 146d9e54; listed so the set cannot grow unnoticed.
var knownAggressive = map[string]bool{
	"code pw assign": true, // self.password = get_password_from_vault()
	"code pw kwarg":  true, // connect(..., password=settings.DB_PASSWORD)
	"yaml empty key": true, // `secret:` with no value eats the next line
}

// TestRedactValue_OrdinaryTechnicalContentSurvives is the over-redaction
// fence. Every string here is content a real transcript carries.
func TestRedactValue_OrdinaryTechnicalContentSurvives(t *testing.T) {
	t.Parallel()
	benign := map[string]string{
		"git sha long":      "commit a3f5b8c9d0e1f2a3b4c5d6e7f8091a2b3c4d5e6f landed",
		"git sha short":     "see 9d9ebbce for the fix",
		"uuid":              "session 550e8400-e29b-41d4-a716-446655440000 opened",
		"sha256":            "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"docker digest":     "image@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		"base64 png":        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk",
		"code pw compare":   "if user.password == request.form['pw']: raise",
		"prose password":    "Reset your password by clicking the link we emailed.",
		"docs password col": "The password column is hashed with bcrypt.",
		"token counts":      `{"tokens_used": 1234, "max_tokens": 4096}`,
		"secretariat":       "The secretariat published the minutes.",
		"token prose":       "Each token costs roughly 4 characters.",
		"jwt prose":         "We validate the token signature server-side.",
		"error token":       "error: token expired, please re-authenticate",
		"primary key":       "Primary key: user_id (bigint, not null)",
		"api_key prose":     "Set your api_key in the settings panel.",
		// The regression this file was added for.
		"kebab sk class":  "class sk-button-primary-large-variant-x is defined",
		"kebab sk file":   "wrote sk-report-2026-08-16-final-revision.md",
		"url no creds":    "https://api.example.com/v1/users?limit=50",
		"go struct":       `type Cfg struct { Token string; Secret string }`,
		"env placeholder": "export API_KEY=<your-key-here>",
		"semver":          "upgraded to v1.24.0-beta.3+build.5678901234",
		"cookie prose":    "We use a cookie to remember your theme choice.",
		"authz prose":     "The authorization header must be present.",
		"npm install":     "npm install @anthropic-ai/sdk@0.30.1 --save-exact",
		"file path":       "/Users/alec/src/kenaz/core/sessions/export/redact.go",
		"sql":             "SELECT id, name FROM sessions WHERE archived_at IS NULL",
		"basic prose":     "authorization: Basic auth is required for this route.",
	}

	var got []string
	for name, in := range benign {
		out, matches := RedactValue(in)
		if len(matches) > 0 && !knownAggressive[name] {
			got = append(got, name+": "+out)
		}
	}
	if len(got) > 0 {
		sort.Strings(got)
		t.Errorf("OVER-REDACTION — ordinary content got a marker:\n  %s",
			strings.Join(got, "\n  "))
	}
}

// TestRedactValue_KnownAggressiveSetIsExactlyThese pins the other direction:
// if a matcher is narrowed so one of these stops firing, the list is stale and
// should shrink. A test that only tolerates false positives never notices when
// they go away.
func TestRedactValue_KnownAggressiveSetIsExactlyThese(t *testing.T) {
	t.Parallel()
	inputs := map[string]string{
		"code pw assign": "self.password = get_password_from_vault()",
		"code pw kwarg":  "connect(host=h, password=settings.DB_PASSWORD)",
		"yaml empty key": "config:\n  secret:\n  password: hunter2hunter2",
	}
	for name, in := range inputs {
		if _, m := RedactValue(in); len(m) == 0 {
			t.Errorf("%s no longer redacts — remove it from knownAggressive: %q",
				name, in)
		}
	}
}

// TestRedactValue_HeaderSchemeValuesAreCaught covers the shape the key-name
// matchers were added for and did not originally reach: an HTTP header value
// is `<scheme> <credential>`, and a value class that stopped at the first
// space captured only the scheme word — under the length floor, so the whole
// matcher failed and the credential went out verbatim.
func TestRedactValue_HeaderSchemeValuesAreCaught(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"basic in json":     `{"authorization": "Basic Zm9vOmJhcjpiYXo="}`,
		"token in json":     `{"Authorization": "Token abc123xyz789"}`,
		"nested authz":      `{"req": {"headers": {"Authorization": "Token abc123xyz789"}}}`,
		"bearer under key":  `{"auth_token": "Bearer abc123xyz789def"}`,
		"apikey scheme":     `{"authorization": "ApiKey k-9182736455"}`,
		"no scheme in json": `{"x-api-key": "opaque-value-9182"}`,
	}
	for name, in := range cases {
		out, m := RedactValue(in)
		if len(m) == 0 {
			t.Errorf("%s: credential survived: %s", name, out)
		}
	}
}

// TestRedactValue_BareOpenAIKeyStillCaught guards the narrowing in the split
// above: the false positive was fixed by restricting the PREFIXLESS shape to
// base62, and a real bare key is base62, so recall must be unchanged.
func TestRedactValue_BareOpenAIKeyStillCaught(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"sk-abcdefghijklmnopqrstuvwxyz0123456789",
		"sk-proj-abcdefghijklmnop_qrstuvwxyz-0123",
		"sk-svcacct-abcdefghijklmnopqrstuvwxyz01",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
	} {
		out, m := RedactValue("key is " + key + " ok")
		if len(m) == 0 || strings.Contains(out, key) {
			t.Errorf("%q was not redacted: %s", key, out)
		}
	}
}
