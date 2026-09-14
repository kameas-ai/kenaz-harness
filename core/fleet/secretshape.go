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

// apiKeyShapePattern mirrors scripts/ci/check-no-cred-bytes-in-rpc.sh's
// Check 2 regexes (Anthropic + generic OpenAI-style key prefixes) so the
// runtime gate and the static source-code gate agree on what "looks like
// an API key" means. The CI script only scans .go source; this scans the
// actual wire payload at collect/apply time.
var apiKeyShapePattern = regexp.MustCompile(`sk-ant-[A-Za-z0-9]{16,}|sk-[A-Za-z0-9]{20,}`)

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
