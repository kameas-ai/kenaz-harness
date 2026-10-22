// Package tooldisplay renders a tool call's arguments for the chat
// transcript's tool chips — the single source of truth for which
// argument VALUES a user may see on screen.
//
// # Why this exists
//
// The chat chip used to show argument names and value TYPES only
// (`kenaz__read_file(path=<string>)`), because a tool argument is often
// the most sensitive text in a session. That hid the one thing a user
// reading a trajectory needs — which file, which URL, which command —
// so the product decision (audit finding HR-7, 2026-10-21) is: show the
// value of an argument unless it is, or looks like, a secret.
//
// # What it is NOT
//
// This is a DISPLAY-only rendering. It is not what a tool_call row
// persists as its Content (that stays chat.displayArgsSummary's
// names-and-types string, which is also what session export prints), it
// is not the confirm modal's structural summary
// (toolloop.SummarizeArgs), and it never reaches an audit record, a
// hook payload, a log line or telemetry. Those surfaces stay
// metadata-only. Callers compute a Summary on the way to the screen and
// nowhere else.
//
// # The policy
//
// A value is shown when ALL of these hold, and replaced with Redacted
// otherwise:
//
//   - the tool has not opted out (ToolOptsOut: a tool whose arguments
//     are inherently credential-bearing renders types only);
//   - the argument's name — and every enclosing object key — is not
//     secret-like (token, key, secret, password, auth, cookie, session,
//     credential, bearer, private, …; see secretNameWords);
//   - the value is a scalar (string, number, bool, null) or a bounded
//     object / array whose leaves pass the same tests;
//   - a string value does not look like a credential: a known provider
//     prefix (sk-, ghp_, github_pat_, xox?-, AKIA…, AIza…, glpat-, …),
//     a PEM block, a JWT, an Authorization-style `Bearer`/`Basic`
//     credential, a `password=…`-style assignment, or a long
//     high-entropy token;
//   - for file-writing tools, the target path is not a credential file
//     (.env, .netrc, id_rsa, *.pem, …) — then the content-like
//     arguments are redacted while the path itself stays visible.
//
// Shown strings are single-lined and truncated (MaxValueRunes), objects
// and arrays are bounded in depth and width, and the whole summary is
// capped (MaxSummaryRunes). The package is a stdlib-only leaf so both
// the chat runner (which may not import core/session) and the sessions
// view can share it.
package tooldisplay

import (
	"encoding/json"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// Redacted replaces a value the policy will not show.
	Redacted = "<redacted>"

	// MaxValueRunes bounds one rendered string value.
	MaxValueRunes = 120
	// MaxSummaryRunes bounds the whole rendered call.
	MaxSummaryRunes = 400
	// MaxDepth bounds object/array nesting; deeper levels render as
	// `{…}` / `[…]`.
	MaxDepth = 3
	// MaxItems bounds how many keys / elements of one object / array are
	// rendered before a `…+N` marker.
	MaxItems = 8

	ellipsis = "…"
)

// Summary renders `name(k=v, …)` for display, applying the redaction
// policy to every value. Keys are sorted so the live chip and the
// reloaded chip render byte-identically.
func Summary(tool string, args map[string]any) string {
	if len(args) == 0 {
		return tool + "()"
	}
	keys := sortedKeys(args)
	parts := make([]string, 0, len(keys))
	if ToolOptsOut(tool) {
		for _, k := range keys {
			parts = append(parts, k+"=<"+typeName(args[k])+">")
		}
		return capRunes(tool+"("+strings.Join(parts, ", ")+")", MaxSummaryRunes)
	}
	credFile := targetsCredentialFile(args)
	for _, k := range keys {
		if credFile && isContentKey(k) {
			parts = append(parts, k+"="+Redacted)
			continue
		}
		parts = append(parts, k+"="+renderField(k, args[k], 0))
	}
	return capRunes(tool+"("+strings.Join(parts, ", ")+")", MaxSummaryRunes)
}

// SummaryJSON is Summary over a raw JSON argument object, as persisted
// for the model layer. ok is false when raw is not a JSON object — the
// caller then falls back to its names-and-types string rather than
// guessing.
func SummaryJSON(tool, raw string) (summary string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	var args map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil || args == nil {
		return "", false
	}
	return Summary(tool, args), true
}

// renderField renders one key's value: a secret-like name redacts the
// whole value whatever its shape.
func renderField(key string, v any, depth int) string {
	if IsSecretName(key) {
		return Redacted
	}
	return renderValue(v, depth)
}

func renderValue(v any, depth int) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return renderNumberString(t.String())
	case float64:
		return renderFloat(t)
	case float32:
		return renderFloat(float64(t))
	case int:
		return strconv.Itoa(t)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case uint32:
		return strconv.FormatUint(uint64(t), 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case string:
		return renderString(t)
	case map[string]any:
		if depth+1 >= MaxDepth {
			return "{" + ellipsis + "}"
		}
		keys := sortedKeys(t)
		parts := make([]string, 0, min(len(keys), MaxItems)+1)
		for i, k := range keys {
			if i == MaxItems {
				parts = append(parts, ellipsis+"+"+strconv.Itoa(len(keys)-MaxItems))
				break
			}
			parts = append(parts, k+"="+renderField(k, t[k], depth+1))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		if depth+1 >= MaxDepth {
			return "[" + ellipsis + "]"
		}
		parts := make([]string, 0, min(len(t), MaxItems)+1)
		for i, e := range t {
			if i == MaxItems {
				parts = append(parts, ellipsis+"+"+strconv.Itoa(len(t)-MaxItems))
				break
			}
			parts = append(parts, renderValue(e, depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		// A non-JSON Go value: nothing guarantees its String form is
		// safe, so fail closed.
		return Redacted
	}
}

func renderFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Redacted
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// renderNumberString passes a json.Number through only when it is
// short; a 40-digit "number" is an identifier, and identifiers are where
// numeric secrets (PINs, account numbers) live.
func renderNumberString(s string) string {
	if len(s) > 20 {
		return Redacted
	}
	return s
}

func renderString(s string) string {
	if !utf8.ValidString(s) {
		return Redacted
	}
	if LooksLikeSecret(s) {
		return Redacted
	}
	return truncateRunes(singleLine(s), MaxValueRunes)
}

// ---- secret-like names ---------------------------------------------------

// secretNameWords are whole words (after splitting a key on case and
// separators) that mark the key's value as secret.
var secretNameWords = map[string]bool{
	"token":  true, // not "tokens": that is a count (max_tokens)
	"secret": true, "secrets": true,
	"key": true, "keys": true,
	"password": true, "passwords": true, "passwd": true, "pwd": true,
	"passphrase": true, "pass": true,
	"auth": true, "authorization": true, "authn": true,
	"credential": true, "credentials": true, "creds": true, "cred": true,
	"cookie": true, "cookies": true,
	"session": true, "sessionid": true, "sid": true,
	"bearer":  true,
	"private": true, "privkey": true,
	"apikey": true, "otp": true, "totp": true, "mfa": true, "pin": true,
	"signature": true, "sig": true, "nonce": true, "salt": true,
	"jwt": true, "oauth": true, "csrf": true, "xsrf": true,
}

// secretNameSubstrings catch joined spellings the word split cannot see
// ("apikey", "accesstoken", "xapikey").
var secretNameSubstrings = []string{
	"apikey", "accesskey", "secretkey", "privatekey", "accesstoken",
	"refreshtoken", "idtoken", "authtoken", "sessiontoken", "clientsecret",
	"password", "passwd", "passphrase", "credential", "bearer", "cookie",
	"secret",
}

// IsSecretName reports whether an argument / object key names a secret.
// Matching is case-insensitive and separator-insensitive; a key is split
// into words so `tokens_used` (a count) is not mistaken for `token`
// while `api_key`, `X-Api-Key`, `sessionToken` and `AUTH` all are.
func IsSecretName(key string) bool {
	joined := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, key))
	if joined == "" {
		return false
	}
	for _, sub := range secretNameSubstrings {
		if strings.Contains(joined, sub) {
			return true
		}
	}
	for _, w := range splitWords(key) {
		if secretNameWords[w] {
			return true
		}
	}
	return false
}

// splitWords lower-cases key and splits it on separators and camelCase
// boundaries: "X-Api-Key" → [x api key], "sessionToken" → [session token].
func splitWords(key string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(key)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) ||
			(i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// ---- secret-looking values -----------------------------------------------

// secretValuePatterns match credential SHAPES anywhere in a string. Any
// hit redacts the whole value — a partial mask still leaks length and
// context. The set follows core/sessions/export's catalog (which this
// leaf package cannot import) plus the brief's prefixes.
var secretValuePatterns = []*regexp.Regexp{
	// AWS access key ids.
	regexp.MustCompile(`\b(?:AKIA|ASIA|AROA|AGPA|AIDA|AIPA|ANPA|ANVA|APKA)[0-9A-Z]{16}\b`),
	// OpenAI / Anthropic / Stripe-style keys.
	regexp.MustCompile(`(?:^|[^A-Za-z0-9])(?:sk|rk|pk)[-_](?:live_|test_|proj-|ant-[a-z0-9]+-)?[A-Za-z0-9_-]{16,}`),
	// GitHub tokens, classic and fine-grained.
	regexp.MustCompile(`\b(?:(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	// Slack tokens.
	regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`),
	// GitLab, Google API, npm, Hugging Face.
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`),
	// PEM blocks (any BEGIN line is enough — a truncated key is still a key).
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*(?:PRIVATE KEY|CERTIFICATE|PGP)`),
	// JWT: three base64url segments, the first a JSON header.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// Authorization header values.
	regexp.MustCompile(`(?i)\b(?:Bearer|Basic|Token)\s+[A-Za-z0-9_\-.~+/=]{16,}`),
	// key=value / key: value assignments naming a secret, in free text
	// such as a shell command or a config snippet.
	regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?key|secret|token|passw(?:or)?d|passphrase|pwd|auth|credential|cookie|session[_-]?id)["']?\s*[:=]\s*["']?[^\s"',;&]{6,}`),
	// URL userinfo with a password: scheme://user:pass@host.
	regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`),
}

// highEntropySplit separates the tokens a long-random-string check runs
// on. `/` is a separator so ordinary paths are judged segment by segment.
var highEntropySplit = regexp.MustCompile(`[\s"'=:,;/\\@()\[\]{}<>|&?#]+`)

// LooksLikeSecret reports whether a string value has a credential shape
// or contains a long high-entropy token.
func LooksLikeSecret(s string) bool {
	for _, re := range secretValuePatterns {
		if re.MatchString(s) {
			return true
		}
	}
	for _, tok := range highEntropySplit.Split(s, -1) {
		if isHighEntropyToken(tok) {
			return true
		}
	}
	return false
}

// isHighEntropyToken flags a token of at least 32 characters that mixes
// three character classes (upper, lower, digit, symbol) and carries at
// least 4 bits of Shannon entropy per character — the shape of a
// base64/base62 secret. Hex digests (git SHAs, sha256 sums) use two
// classes and stay visible: they are identifiers users need to read.
func isHighEntropyToken(tok string) bool {
	if len(tok) < 32 {
		return false
	}
	var upper, lower, digit, other bool
	counts := map[rune]int{}
	n := 0
	for _, r := range tok {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
		counts[r]++
		n++
	}
	classes := 0
	for _, b := range []bool{upper, lower, digit, other} {
		if b {
			classes++
		}
	}
	if classes < 3 {
		return false
	}
	var h float64
	for _, c := range counts {
		p := float64(c) / float64(n)
		h -= p * math.Log2(p)
	}
	return h >= 4.0
}

// ---- per-tool opt-out ----------------------------------------------------

// sensitiveTools render types only, whatever their arguments look like.
// Matched on the bare tool name (after any `server__` prefix).
var sensitiveTools = map[string]bool{
	"list_secrets": true,
}

// sensitiveToolWords opt a tool out when they appear as a word of its
// name (server or tool part): a tool that sets an env var, stores a credential or logs in
// takes arguments that are secrets by construction, including MCP tools
// this harness has never seen.
var sensitiveToolWords = map[string]bool{
	"secret": true, "secrets": true, "credential": true, "credentials": true,
	"password": true, "passwords": true, "token": true, "tokens": true,
	"apikey": true, "keyring": true, "keychain": true, "vault": true,
	"env": true, "dotenv": true, "login": true, "auth": true, "oauth": true,
	"key": true, "keys": true,
}

// ToolOptsOut reports whether a tool's arguments are inherently
// sensitive, so its chip shows argument types only.
func ToolOptsOut(tool string) bool {
	bare := tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		bare = tool[i+2:]
	}
	bare = strings.ToLower(bare)
	if sensitiveTools[bare] {
		return true
	}
	// Every word of the full name, server included: a `vault__write` or
	// `secrets-manager__get` is sensitive because of the server it
	// belongs to.
	for _, w := range splitWords(tool) {
		if sensitiveToolWords[w] {
			return true
		}
	}
	return false
}

// ---- credential-file targets ---------------------------------------------

var pathKeys = []string{"path", "file_path", "filepath", "file", "filename", "target", "dest", "destination"}

var contentKeys = map[string]bool{
	"content": true, "contents": true, "text": true, "body": true, "data": true,
	"new_string": true, "old_string": true, "new_str": true, "old_str": true,
	"patch": true, "diff": true, "edits": true, "value": true,
}

func isContentKey(k string) bool { return contentKeys[strings.ToLower(k)] }

// targetsCredentialFile reports whether a path-like argument names a
// file whose contents are credentials, so content-like arguments must
// not be shown even when they look innocuous.
func targetsCredentialFile(args map[string]any) bool {
	for _, k := range pathKeys {
		p, ok := args[k].(string)
		if !ok || p == "" {
			continue
		}
		if isCredentialPath(p) {
			return true
		}
	}
	return false
}

func isCredentialPath(p string) bool {
	base := strings.ToLower(path.Base(strings.ReplaceAll(p, `\`, "/")))
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."),
		base == ".netrc", base == "_netrc", base == ".npmrc", base == ".pypirc",
		base == ".pgpass", base == ".git-credentials", base == "credentials",
		base == "credentials.json", base == "kubeconfig", base == ".dockercfg",
		strings.HasPrefix(base, "id_rsa"), strings.HasPrefix(base, "id_ed25519"),
		strings.HasPrefix(base, "id_ecdsa"), strings.HasPrefix(base, "id_dsa"),
		strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".key"),
		strings.HasSuffix(base, ".p12"), strings.HasSuffix(base, ".pfx"):
		return true
	}
	lp := strings.ToLower(p)
	return strings.Contains(lp, "/.ssh/") || strings.Contains(lp, "/.aws/") ||
		strings.Contains(lp, "/.docker/config.json") || strings.Contains(lp, "/.kube/config")
}

// ---- helpers ---------------------------------------------------------------

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// typeName is the coarse type word the opt-out rendering uses; it
// matches the persisted names-and-types summary.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case float64, float32, int, int32, int64, uint, uint32, uint64, json.Number:
		return "number"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}

// singleLine collapses every run of whitespace (newlines included) and
// drops control characters, so one value cannot break the chip's line.
func singleLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + ellipsis
}

// capRunes bounds the whole summary, keeping a closing paren so the
// chip still reads as a call.
func capRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-2]) + ellipsis + ")"
}
