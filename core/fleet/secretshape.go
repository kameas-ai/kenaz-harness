// Package fleet — secretshape.go
//
// Central secret-shape rejection (spec §2.2 / FR-006,
// fleet-generic-sync-framework-01NSYNC02 WP06).
//
// Before this file, SyncKind.SecretPolicy was validated for presence at
// registration (synckind.go's validate()) but never actually consulted —
// every kind's collector/applier was independently and *individually*
// responsible for never emitting credential bytes (see the HARD RULE
// comments on KindCollector/KindApplier and in sync_categories.go /
// sync_mcp.go). That per-kind honor system is real defense (installed_mcp's
// collector genuinely redacts secret env values) but it is not the
// "enforced centrally once, instead of per-kind" framework guarantee the
// spec promises: a new kind (or a bug in an existing one) that forgets to
// redact ships credential bytes off the device with nothing to catch it.
//
// SecretShapeReason is that central backstop. It is deliberately
// payload-shape-agnostic: every kind's Collect output and Apply input
// passes through here regardless of what fields the kind declares (wired
// in synckind.go's CategoryConfig() adapter for the user-scope LWW path,
// and in core/rpc/views/settings/fleet.go's org_config dispatch loop for
// the org-scope path) — a new kind gets this protection for free instead
// of writing its own scanner.
//
// Detection is intentionally narrow rather than a broad entropy scan:
// a payload here is structured JSON metadata (theme names, model prefs,
// recipe templates, ...), not free text, so a small set of precise
// signals catches the shapes FR-006 names — "an API key / token / @secret:
// reference" — without flagging legitimate fields that merely mention
// "token" in their name (e.g. RecipeAuth.TokenEnvVar, which names an env
// *variable*, not a credential value).
package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// apiKeyShapePattern is the two sk- value shapes Fleet's server-side sync
// scanner refuses with 422 (kenaz-fleet service/sync_schema.go, the
// value-scanner table). The client refuses exactly what the server would,
// before anything is sent. It is a catch-all on purpose: current keys put
// '-' and '_' after the prefix (sk-ant-api03-, sk-proj-, sk-svcacct-,
// sk-or-v1-), and a per-provider list misses the next new prefix. The
// pre-2026-10-14 pattern (alphanumerics only) matched none of them.
//
// scripts/ci/check-no-cred-bytes-in-rpc.sh check 2 is deliberately
// narrower (provider prefixes only): in Go source, `sk-` + 16 kebab-case
// characters is an ordinary identifier. In a sync payload it is refused by
// Fleet anyway, so refusing it here first loses nothing.
var apiKeyShapePattern = regexp.MustCompile(apiKeyShapeRegex)

const apiKeyShapeRegex = `sk-ant-[A-Za-z0-9_-]{8,}|\bsk-[A-Za-z0-9_-]{16,}`

// secretReferenceMarker is the @secret:<owner>:<locator> scheme
// core/tools/listsecrets and core/tools/bash resolve at tool-call time
// (core/tools/bash/bash.go's "@secret: reference substitution"). A sync
// payload carrying one is refused outright: even though it is a
// *reference* rather than a plaintext credential, the reference is
// meaningless (or dangerous, if the locator happens to collide) on a
// receiving device with a different credstore.
const secretReferenceMarker = "@secret:"

// secretFieldNames is matched by EXACT (case-insensitive) key name, not
// substring — substring matching would flag legitimate metadata fields
// like "token_env_var" (an env *variable name*, e.g. "GITHUB_TOKEN") or
// "client_id" purely because they contain "token"/"id"-adjacent text.
// Exact matching keeps the false-positive rate low while still catching
// the shapes a per-kind redaction bug would actually produce (e.g. an
// installed_mcp EnvOverrides map whose key happens to be literally
// "API_KEY" and whose value slipped past the SecretKeys() classifier).
var secretFieldNames = map[string]bool{
	"api_key":       true,
	"apikey":        true,
	"secret":        true,
	"client_secret": true,
	"clientsecret":  true,
	"password":      true,
	"passwd":        true,
	"access_token":  true,
	"accesstoken":   true,
	"private_key":   true,
	"privatekey":    true,
	"bearer_token":  true,
	"bearertoken":   true,
	"auth_token":    true,
	"authtoken":     true,
}

// SecretShapeReason inspects a JSON sync payload for content that must
// never leave the device through the generic sync framework. It returns
// "" for a clean payload, or a short human-readable reason identifying
// what tripped — the reason never repeats the offending value itself, so
// logging or returning it as an error never leaks the secret it caught.
//
// Checked, in order (cheapest / most specific first):
//  1. An "@secret:" reference substring anywhere in the raw bytes.
//  2. An API-key-shaped literal (sk-ant-…, sk-… — see apiKeyShapePattern).
//  3. A JSON field whose name exactly matches a known credential field
//     name (case-insensitive) and whose value is a non-empty string,
//     found by walking the payload recursively through objects and
//     arrays of arbitrary depth.
//
// A payload that fails to parse as JSON for step 3 is not itself an
// error here — SecretShapeReason degrades to just the substring/regex
// checks (steps 1-2) rather than rejecting on a decode failure, because
// malformed-JSON detection is the collector/applier's own job (it will
// fail there with a clearer error) and conflating the two would produce
// a misleading "looks like a secret" message for an unrelated bug.
func SecretShapeReason(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	if bytes.Contains(payload, []byte(secretReferenceMarker)) {
		return "contains an @secret: reference"
	}
	if apiKeyShapePattern.Match(payload) {
		return "contains an API-key-shaped literal"
	}
	var v any
	if err := json.Unmarshal(payload, &v); err == nil {
		if field, ok := findSecretShapedField(v, ""); ok {
			return fmt.Sprintf("field %q looks like a credential", field)
		}
		// The raw-bytes match above misses a key that follows a JSON escape
		// ("line1\nsk-…": the escape's 'n' defeats \b). Fleet decodes first
		// and scans each string, so do the same and agree with its 422.
		if reason := findSecretShapedValue(v); reason != "" {
			return reason
		}
	}
	return ""
}

// secretValuePatterns mirrors kenaz-fleet service/sync_schema.go
// secretValuePatterns (origin/main, 2026-10-15) entry for entry, so the
// client refuses exactly what Fleet's sync scanner would 422. Keep in step
// with that table. Reasons are fixed strings; matched text is never surfaced.
var secretValuePatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`@secret:`), "contains an @secret: credential reference"},
	{regexp.MustCompile(`-----BEGIN[ A-Z0-9]*PRIVATE KEY-----`), "contains a PEM private key"},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`), "contains a Bearer credential"},
	{regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9+/]{12,}={0,2}`), "contains a Basic auth credential"},
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`), "contains an Anthropic API key"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), "contains an sk- API key"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`), "contains a GitHub token"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{16,}`), "contains a GitHub fine-grained PAT"},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`), "contains a GitLab token"},
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), "contains a Slack token"},
	{regexp.MustCompile(`hooks\.slack\.com/services/[A-Za-z0-9/]+`), "contains a Slack webhook URL"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`), "contains a Google API key"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), "contains an AWS access key ID"},
	{regexp.MustCompile(`\b[rs]k_(?:live|test)_[A-Za-z0-9]{10,}`), "contains a Stripe key"},
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`), "contains an npm token"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), "contains a JWT"},
	{regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/\s@]*:[^/\s@]*@`), "contains a URL with embedded credentials"},
	{regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/\s@:]{20,}@`), "contains a URL with an embedded token"},
	{regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:password|passwd|pwd|passphrase|token|access[_-]?token|refresh[_-]?token|auth[_-]?token|secret|client[_-]?secret|api[_-]?key|apikey|access[_-]?key|private[_-]?key|credentials?)\s*[=:]\s*[^\s&;,"']{3,}`), "contains a credential key=value pair"},
}

// scanSecretValue is Fleet's scanSecretValue: a fixed reason, or "".
func scanSecretValue(s string) string {
	if len(s) < 8 {
		return ""
	}
	for _, p := range secretValuePatterns {
		if p.re.MatchString(s) {
			return p.reason
		}
	}
	return ""
}

// findSecretShapedValue scans every decoded string value AND every object
// key (free-form maps carry user-chosen keys) with scanSecretValue.
func findSecretShapedValue(v any) string {
	switch t := v.(type) {
	case string:
		return scanSecretValue(t)
	case map[string]any:
		for k, val := range t {
			if r := scanSecretValue(k); r != "" {
				return r
			}
			if r := findSecretShapedValue(val); r != "" {
				return r
			}
		}
	case []any:
		for _, val := range t {
			if r := findSecretShapedValue(val); r != "" {
				return r
			}
		}
	}
	return ""
}

// findSecretShapedField walks an arbitrary decoded-JSON value looking for
// an object key that exactly matches secretFieldNames (case-insensitive)
// whose value is a non-empty string. path is used only to build a
// dotted-path name for the returned field so a caller / test can pinpoint
// the hit; it never appears in the returned VALUE, only the field name.
func findSecretShapedField(v any, path string) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if secretFieldNames[strings.ToLower(k)] {
				if s, ok := val.(string); ok && s != "" {
					return childPath, true
				}
			}
			if field, ok := findSecretShapedField(val, childPath); ok {
				return field, true
			}
		}
	case []any:
		for i, val := range t {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if field, ok := findSecretShapedField(val, childPath); ok {
				return field, true
			}
		}
	}
	return "", false
}
