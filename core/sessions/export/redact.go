package export

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// RedactionMatch records a single substitution made by RedactValue.
type RedactionMatch struct {
	// ProfileID is the matcher name that fired (e.g. "anthropic-key",
	// "openai-key", "bearer-token").
	ProfileID string
	// Offset is the byte offset of the substitution in the original string.
	Offset int
}

// credMatcher couples a human-readable profile id to a compiled regex.
// For matchers with a capture group (SubmatchIndex > 0) only the captured
// substring is replaced; for whole-match matchers (SubmatchIndex == 0)
// the entire match is replaced.
type credMatcher struct {
	profileID     string
	re            *regexp.Regexp
	submatchIndex int
	// anchor is a lowercase literal that MUST appear in the haystack for
	// this matcher to have any chance of firing. When set, RedactValue
	// skips the regex entirely unless the (once-lowercased) input contains
	// it.
	//
	// This is a performance fence, and it is load-bearing rather than
	// decorative. Go's regexp is RE2: a pattern with one literal core is
	// scanned at memchr speed (~12 ms/MB here), but a pattern that opens
	// with a 20-way case-insensitive alternation has no literal to scan
	// for and runs the full automaton over every byte — measured at
	// 203 ms/MB, twenty times the cost of every other matcher in this
	// file and more than the whole rest of the catalog combined. Lowering
	// the string once and probing it for seven short literals costs
	// 2.8 ms/MB.
	//
	// Correctness note: the probe is computed from the ORIGINAL input, not
	// from the partially-substituted string each matcher sees. That is
	// safe in the only direction that matters — substitutions delete
	// credential text and insert `<REDACTED:…>`, so an anchor present at
	// matcher N was present at matcher 0.
	anchor string
}

// builtinMatchers is the canonical set of credential patterns RedactValue
// applies, in order. ORDER IS LOAD-BEARING: RedactValue runs each matcher
// over the output of the previous one, so the first matcher to claim a
// substring is the profile id that appears in the document. Specific
// provider shapes come first; `secret-named-key`, the catch-all that fires
// on a key NAME rather than a value shape, comes last so a recognised key
// keeps its own label.
//
// This list BEGAN as a copy of core/event/redact.defaultMatchers, and the
// comment here used to say it mirrored it. As of 2026-08-16 it does not:
// that catalog still carries the original ten patterns, including the
// `generic-password-querystring` matcher that cannot match JSON. The two
// have different jobs — that one feeds an HMAC pipeline for audit-log
// payloads, this one writes the human-readable `<REDACTED:…>` form FR-005
// specifies for a document — and widening a live audit pipeline is not a
// change to make from inside an export fix. The divergence is recorded in
// docs/unwired-ledger.md rather than papered over here.
//
// The provider-prefix set below is in step with the two sibling redactors
// that already carried it — core/sentry/redactor.go and
// core/fleet/telemetry_redactor.go — which is where the AWS `ASIA`/`AROA`
// prefixes, `AIzaSy` and `sk-proj-` were copied from. Those three had them
// and the export did not.
var builtinMatchers = []credMatcher{
	{
		// Widened 2026-08-16 from AKIA-only to the full AWS unique-id
		// prefix set the sibling redactors already carry: a temporary
		// (ASIA) or role (AROA) id leaked while a long-lived one did not.
		profileID: "aws-access-key-id",
		re:        regexp.MustCompile(`\b(?:AKIA|ASIA|AROA|AGPA|AIPA|ANPA|ANVA|APKA)[0-9A-Z]{16}\b`),
	},
	{
		profileID:     "aws-secret-key",
		re:            regexp.MustCompile(`(?i)(?:aws[_-]?secret[_-]?access[_-]?key|secret[_-]?key)\s*[:=]\s*["']?([A-Za-z0-9/+]{40})["']?`),
		submatchIndex: 1,
		anchor:        "secret",
	},
	{
		profileID: "jwt",
		re:        regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	},
	{
		profileID:     "bearer-token",
		re:            regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9_\-.~+/=]{20,})`),
		submatchIndex: 1,
	},
	{
		profileID:     "basic-auth",
		re:            regexp.MustCompile(`(?i)\bBasic\s+([A-Za-z0-9+/=]{16,})`),
		submatchIndex: 1,
	},
	{
		profileID: "pem-block",
		re:        regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
	},
	{
		// Both GitHub shapes in one pattern: the classic `gh?_` prefixes
		// and the fine-grained PAT GitHub issues today, which matches none
		// of them. Kept as one alternation rather than two matchers
		// because both alternatives share the literal `gh`, so RE2 still
		// scans at literal speed.
		profileID: "github-token",
		re:        regexp.MustCompile(`\b(?:(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	},
	{
		// Anthropic before OpenAI: `sk-ant-…` is a strict prefix of the
		// widened OpenAI shape below, and `anthropic-key` is the label the
		// export has used since the mission shipped. Because RedactValue
		// runs matchers in sequence, by the time the OpenAI pattern sees
		// the string every `sk-ant-…` has already become a marker.
		profileID: "anthropic-key",
		re:        regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	},
	{
		// Widened to the project / service-account / admin prefixes, which
		// OpenAI issues with `_` and `-` in the body.
		//
		// SPLIT FROM THE BARE `sk-` SHAPE BELOW, and that split is the one
		// core/sentry/redactor.go and core/fleet/telemetry_redactor.go
		// already make. Folding the two into one pattern applies the
		// permissive `[A-Za-z0-9_-]` body to the PREFIXLESS case, and `sk-`
		// followed by twenty kebab-case characters is an ordinary
		// identifier: `sk-button-primary-large-variant-x` was redacted as a
		// credential. Recall is unchanged — a prefixed key still matches
		// here, and a bare OpenAI key is base62 so it still matches below.
		profileID: "openai-key",
		re:        regexp.MustCompile(`\bsk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{20,}\b`),
	},
	{
		// The bare `sk-…` shape: base62 body only, matching the siblings.
		profileID: "openai-key",
		re:        regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
	},
	{
		profileID: "google-api-key",
		re:        regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	},
	{
		profileID: "slack-token",
		re:        regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`),
	},
	{
		profileID: "stripe-key",
		re:        regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),
	},
	{
		// A password inline in a connection string —
		// postgres://user:PASS@host/db, mongodb+srv://…, redis://…,
		// amqp://…. Only the password is replaced; the host and database
		// stay readable, which is the point of exporting the string at all.
		profileID:     "connection-string-password",
		re:            regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:@]+:([^\s/:@]+)@`),
		submatchIndex: 1,
		anchor:        "://",
	},
}

// secretNamedKeyValue is the assignment tail every key-name matcher below
// shares: an optional closing quote on the key, the separator, an optional
// opening quote on the value, then the value itself.
//
// `["']?` before `\s*[:=]` is the entire reason these matchers work on a
// real tool result. The pre-2026-08-16 pattern was
// `(?:password|secret|apikey|api_key|api-key|token)\s*[:=]\s*…`, which
// cannot match `{"password": "hunter2"}` — the key's CLOSING QUOTE sits
// between the name and the colon. Every structured tool result in this app
// is JSON, so in practice that matcher only ever fired on shell, env-file
// and query-string text. Verified against 146d9e54: `{"aws_secret_access_
// key": "wJalr…"}` in a tool result reached both exported files.
//
// `<` and `>` are excluded from the value class so a later matcher cannot
// re-redact a `<REDACTED:…>` marker an earlier one already wrote — that is
// what keeps the specific provider labels visible in the document instead
// of every one of them degrading to `secret-named-key`.
//
// The `{6,}` floor keeps `secret: none` and `key: id` out of it.
//
// The optional SCHEME prefix is why `{"authorization": "Basic Zm9v…"}` is
// covered. A header value is `<scheme> <credential>`, and a value class
// that stops at the first space captured `Basic` alone — five characters,
// under the floor — so the whole matcher failed and the credential went
// out verbatim. That is the exact shape the `authorization` / `cookie`
// matchers below were added for, so leaving it uncovered would have made
// their own docstring false. Only the fixed scheme words are allowed to
// carry a space; everything else still stops at whitespace, which is what
// keeps `authorization: Basic auth is required` (no 6-char token after the
// scheme, and only 5 without it) out of the match.
const secretNamedKeyValue = `["']?\s*[:=]\s*["']?((?:(?:Bearer|Basic|Token|ApiKey|Digest|Negotiate|SSWS|GenieKey)\s+)?[^"'\s,;&<>}\])]{6,})`

// keyNameMatchers are the catch-all half of the catalog: a credential that
// matches NO provider shape is still a credential when it sits under a key
// that names one. `authorization`, `cookie`, `set-cookie` and `x-api-key`
// were absent from the old alternation entirely, so an HTTP response
// dumped into a tool result carried its own credentials out verbatim.
//
// SPLIT BY ANCHOR, one literal core each, rather than written as the one
// 20-way alternation this started life as. See `credMatcher.anchor`: the
// single alternation measured 203 ms/MB against 12 ms/MB for each of these,
// and the anchors let a transcript that never says "cookie" skip the
// cookie matcher outright.
//
// NO LEADING WORD BOUNDARY, deliberately: `\b` finds no boundary before
// `secret` in `aws_secret_access_key`, because `_` is a word character.
// What stops a mid-word match is the mandatory `["']?\s*[:=]` — it is why
// `max_tokens: 4096` does not match `token` (the next character is `s`)
// and why `error: token expired` does not either.
//
// `key` is the one anchor kept as an alternation rather than a bare
// literal. A bare `(?i)key` + tail would redact `Primary key: user_id`,
// and over-redaction is its own failure: an export where everything is
// `<REDACTED:…>` passes every leak test in this package and is useless.
var keyNameMatchers = []credMatcher{
	{
		profileID: "secret-named-key",
		anchor:    "authorization",
		re:        regexp.MustCompile(`(?i)authorization` + secretNamedKeyValue),

		submatchIndex: 1,
	},
	{
		profileID:     "secret-named-key",
		anchor:        "cookie",
		re:            regexp.MustCompile(`(?i)cookie` + secretNamedKeyValue),
		submatchIndex: 1,
	},
	{
		profileID:     "secret-named-key",
		anchor:        "key",
		re:            regexp.MustCompile(`(?i)(?:api[_-]?key|apikey|secret[_-]?key|private[_-]?key|access[_-]?key|secret[_-]?access[_-]?key)` + secretNamedKeyValue),
		submatchIndex: 1,
	},
	{
		profileID:     "secret-named-key",
		anchor:        "token",
		re:            regexp.MustCompile(`(?i)token` + secretNamedKeyValue),
		submatchIndex: 1,
	},
	{
		profileID:     "secret-named-key",
		anchor:        "secret",
		re:            regexp.MustCompile(`(?i)secret` + secretNamedKeyValue),
		submatchIndex: 1,
	},
	{
		profileID:     "secret-named-key",
		anchor:        "passw",
		re:            regexp.MustCompile(`(?i)passw(?:or)?d` + secretNamedKeyValue),
		submatchIndex: 1,
	},
	{
		profileID:     "secret-named-key",
		anchor:        "passphrase",
		re:            regexp.MustCompile(`(?i)passphrase` + secretNamedKeyValue),
		submatchIndex: 1,
	},
}

// allMatchers is the applied order: every provider shape first, then the
// key-name catch-alls. Order is load-bearing — RedactValue runs each
// matcher over the OUTPUT of the previous one, so the first matcher to
// claim a substring is the profile id the document shows. A provider key
// under `"api_key"` is therefore labelled `anthropic-key`, not
// `secret-named-key`.
var allMatchers = append(append([]credMatcher{}, builtinMatchers...), keyNameMatchers...)

// secretNamingKeyRe recognises a STRUCTURED map key whose value is a
// secret regardless of what the value looks like. The string matcher
// above needs a `:` or `=` because it is scanning free text; here the map
// has already done the association, so the name alone is enough.
//
// Anchored, because a structured key is the whole name — `token` is a
// secret-naming key, `tokens_used` is not, and an unanchored match would
// redact the latter's count.
var secretNamingKeyRe = regexp.MustCompile(`(?i)^(?:x-)?(?:authorization|proxy-authorization|set-cookie|cookie|api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|session[_-]?token|aws[_-]?secret[_-]?access[_-]?key|secret[_-]?access[_-]?key|client[_-]?secret|secret[_-]?key|private[_-]?key|passphrase|password|passwd|pwd|token|secret|credentials?)$`)

// MaxRedactDepth bounds the structured walk in redactStructured.
//
// A tool argument object is JSON the model wrote; nothing legitimate in
// this app nests 24 levels deep, and an unbounded walk over a hostile or
// merely pathological payload is a stack overflow in a function whose
// entire job is to run on untrusted content. At the limit the value is
// replaced with a marker rather than passed through — a redactor that
// gives up must fail CLOSED, or the depth bound becomes the bypass.
const MaxRedactDepth = 24

// RedactValue scans s for credential substrings using the built-in
// matcher catalog and returns the redacted string plus a slice of match
// records. Matched substrings are replaced with the explicit literal
// <REDACTED:profile_id> so the substitution is visually obvious in the
// exported transcript.
//
// This is the canonical redaction entry point for the export package.
// It is intentionally stateless and allocation-light for the simple
// string-scan case. It does NOT use the HMAC pipeline from
// core/event/redact — that pipeline is for audit-log payloads; export
// redaction uses the human-readable <REDACTED:…> form specified by
// FR-005.
func RedactValue(s string) (string, []RedactionMatch) {
	if s == "" {
		return s, nil
	}
	var matches []RedactionMatch
	out := s

	// One lowercase copy for every anchored matcher's prefilter. Computed
	// lazily so a string with no anchored candidate never pays for it, and
	// from the ORIGINAL input — see the note on credMatcher.anchor for why
	// that stays correct as substitutions accumulate.
	var lowered string
	var loweredOK bool

	for _, m := range allMatchers {
		if m.anchor != "" {
			if !loweredOK {
				lowered = strings.ToLower(s)
				loweredOK = true
			}
			if !strings.Contains(lowered, m.anchor) {
				continue
			}
		}
		// FindStringIndex allocates nothing; ReplaceAllStringFunc copies
		// the whole string even when it substitutes nothing. On a
		// multi-megabyte transcript with no credentials in it that is one
		// full copy per matcher, for no result.
		if m.re.FindStringIndex(out) == nil {
			continue
		}
		out = m.re.ReplaceAllStringFunc(out, func(hit string) string {
			if m.submatchIndex == 0 {
				matches = append(matches, RedactionMatch{ProfileID: m.profileID})
				return fmt.Sprintf("<REDACTED:%s>", m.profileID)
			}
			groups := m.re.FindStringSubmatchIndex(hit)
			if groups == nil || m.submatchIndex*2+1 >= len(groups) {
				matches = append(matches, RedactionMatch{ProfileID: m.profileID})
				return fmt.Sprintf("<REDACTED:%s>", m.profileID)
			}
			start, end := groups[m.submatchIndex*2], groups[m.submatchIndex*2+1]
			if start < 0 || end < 0 {
				matches = append(matches, RedactionMatch{ProfileID: m.profileID})
				return fmt.Sprintf("<REDACTED:%s>", m.profileID)
			}
			matches = append(matches, RedactionMatch{ProfileID: m.profileID, Offset: start})
			return hit[:start] + fmt.Sprintf("<REDACTED:%s>", m.profileID) + hit[end:]
		})
	}
	return out, matches
}

// redactStructured returns a redacted DEEP COPY of an arbitrary decoded
// JSON value: nested objects to MaxRedactDepth, arrays, and map KEYS.
//
// Three rules, all of which the pre-2026-08-16 walk was missing:
//
//   - A key can BE a secret. `{"sk-ant-…": "x"}` is a flattened header
//     map with the credential on the wrong side of the colon. Keys go
//     through RedactValue like any other string.
//   - A key can NAME a secret. Under `authorization` / `api_key` /
//     `password` / `cookie` the VALUE is redacted whether or not it looks
//     like a credential — an opaque session id matches no pattern and is
//     no less of a credential for it. `forced` carries that decision down
//     through nested containers so `{"authorization": {"header": "…"}}`
//     is covered too, while preserving the container shape so a
//     names-and-types summary still reports `object` rather than
//     `string`.
//   - A leaf need not be a string. A json.Number, a []byte or a custom
//     type with a String() method stringifies into something that can be
//     secret-shaped, so every non-container leaf is formatted and scanned
//     rather than copied through on the strength of its Go type.
//
// `seen` holds the container pointers on the CURRENT path, so a cycle
// terminates without also collapsing a legitimately repeated sibling.
func redactStructured(v any, depth int, forced bool, seen map[uintptr]struct{}) any {
	if v == nil {
		return nil
	}
	if depth > MaxRedactDepth {
		return "<REDACTED:depth-limit>"
	}

	switch tv := v.(type) {
	case string:
		if forced {
			return "<REDACTED:secret-named-key>"
		}
		out, _ := RedactValue(tv)
		return out
	case bool:
		return tv
	case map[string]any:
		// The cycle guard belongs on the fast path too, not only on the
		// reflect fallback below: `map[string]any` is exactly the type
		// `encoding/json` produces, so it is the type a cyclic structure
		// arrives as. Depth alone is not a substitute — a map with two
		// self-references expands 2^MaxRedactDepth times before the bound
		// stops it.
		if len(tv) > 0 {
			p := reflect.ValueOf(tv).Pointer()
			if _, cycle := seen[p]; cycle {
				return "<REDACTED:cycle>"
			}
			seen[p] = struct{}{}
			defer delete(seen, p)
		}
		out := make(map[string]any, len(tv))
		for k, val := range tv {
			rk, _ := RedactValue(k)
			out[rk] = redactStructured(val, depth+1, forced || secretNamingKeyRe.MatchString(k), seen)
		}
		return out
	case []any:
		// `len > 0` because distinct empty slices can share a backing
		// pointer, which would read as a cycle that is not there.
		if len(tv) > 0 {
			p := reflect.ValueOf(tv).Pointer()
			if _, cycle := seen[p]; cycle {
				return "<REDACTED:cycle>"
			}
			seen[p] = struct{}{}
			defer delete(seen, p)
		}
		out := make([]any, len(tv))
		for i, val := range tv {
			out[i] = redactStructured(val, depth+1, forced, seen)
		}
		return out
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		if rv.Kind() == reflect.Pointer {
			if _, cycle := seen[rv.Pointer()]; cycle {
				return "<REDACTED:cycle>"
			}
			seen[rv.Pointer()] = struct{}{}
			defer delete(seen, rv.Pointer())
		}
		return redactStructured(rv.Elem().Interface(), depth+1, forced, seen)

	case reflect.Map:
		if _, cycle := seen[rv.Pointer()]; cycle {
			return "<REDACTED:cycle>"
		}
		seen[rv.Pointer()] = struct{}{}
		defer delete(seen, rv.Pointer())
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k := fmt.Sprintf("%v", iter.Key().Interface())
			rk, _ := RedactValue(k)
			out[rk] = redactStructured(iter.Value().Interface(), depth+1,
				forced || secretNamingKeyRe.MatchString(k), seen)
		}
		return out

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice {
			if rv.IsNil() {
				return nil
			}
			if _, cycle := seen[rv.Pointer()]; cycle {
				return "<REDACTED:cycle>"
			}
			seen[rv.Pointer()] = struct{}{}
			defer delete(seen, rv.Pointer())
		}
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = redactStructured(rv.Index(i).Interface(), depth+1, forced, seen)
		}
		return out
	}

	// Every other leaf: a number, a json.Number, a time, a Stringer.
	// Format it and scan the text, because that text is what any consumer
	// of this value will eventually print.
	if forced {
		return "<REDACTED:secret-named-key>"
	}
	text := fmt.Sprintf("%v", v)
	out, matches := RedactValue(text)
	if len(matches) == 0 {
		return v
	}
	return out
}

// redactMessages applies the credential scanner to every string the
// renderers can reach, and returns a new slice of redacted copies.
//
// DEEP COPY, not a struct copy: `session.Message` carries slices
// (`ContentBlocks`, `ToolCalls`) and a pointer (`MediaSource`) that a
// shallow copy would share with the caller's live message list, so
// rewriting an attachment's filename in place would mutate the session
// the user is still looking at.
func redactMessages(msgs []session.Message) []session.Message {
	out := make([]session.Message, len(msgs))
	for i, m := range msgs {
		m.Content, _ = RedactValue(m.Content)

		redactedCalls := make([]session.ToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			// The tool NAME reaches the document (`<code>%s</code>` in
			// markdown, `name` in JSON) and an MCP server chooses it.
			tc.Name, _ = RedactValue(tc.Name)
			// Arguments: the deep walk. No renderer prints an argument
			// VALUE today — v0.63.0 replaced that with a names-and-types
			// summary — so this is defence in depth, and the keys it
			// redacts are the half that IS printed.
			redacted := make(map[string]any, len(tc.Arguments))
			for k, v := range tc.Arguments {
				rk, _ := RedactValue(k)
				redacted[rk] = redactStructured(v, 1, secretNamingKeyRe.MatchString(k),
					make(map[uintptr]struct{}))
			}
			tc.Arguments = redacted
			tc.Result, _ = RedactValue(tc.Result)
			redactedCalls[j] = tc
		}
		m.ToolCalls = redactedCalls

		// Attachment metadata. `OriginalName` is written into the JSON
		// export and `URI` into the markdown one; a presigned URL is a
		// credential with an expiry date. `Source.Data` is deliberately
		// NOT scanned — it is base64 image bytes, it cannot carry a
		// pattern-matched credential in any readable form, and running
		// fifteen regexes over multi-megabyte blobs on every export is
		// the one way this pass gets expensive.
		if len(m.ContentBlocks) > 0 {
			blocks := make([]llm.ContentBlock, len(m.ContentBlocks))
			copy(blocks, m.ContentBlocks)
			for bi := range blocks {
				blocks[bi].Text, _ = RedactValue(blocks[bi].Text)
				if blocks[bi].Source == nil {
					continue
				}
				src := *blocks[bi].Source
				src.OriginalName, _ = RedactValue(src.OriginalName)
				src.URI, _ = RedactValue(src.URI)
				blocks[bi].Source = &src
			}
			m.ContentBlocks = blocks
		}

		out[i] = m
	}
	return out
}

// redactRecord returns a copy of the session row with its free-text
// fields scanned.
//
// The renderers print `Name` (the markdown H1, the JSON `session.name`,
// and — via DefaultFilename — the suggested filename) and `SystemPrompt`
// (the JSON `session.system_prompt`). Neither had ever been through the
// scanner: `redactMessages` is named for what it walks, and Render called
// nothing else. A key pasted into a session title, or a system prompt
// carrying the credential the user told the model to use, went to disk
// verbatim in every build before 2026-08-16.
func redactRecord(sess session.Record) session.Record {
	sess.Name, _ = RedactValue(sess.Name)
	sess.SystemPrompt, _ = RedactValue(sess.SystemPrompt)
	// ContextKind is a validated enum ("system" | "user_seed") so this can
	// only ever be a no-op — but it is a string the JSON export prints,
	// and "the validator upstream makes it safe" is exactly the reasoning
	// that left Name unscanned.
	sess.ContextKind, _ = RedactValue(sess.ContextKind)
	return sess
}

// sanitiseTitle converts a session title into a filesystem-safe slug.
// Spaces become hyphens; non-printable and non-alphanumeric chars (except
// hyphen and underscore) are dropped. The result is lowercased and capped
// at 64 chars to keep filenames sane.
func sanitiseTitle(title string) string {
	var sb strings.Builder
	for _, r := range title {
		switch {
		case r == ' ' || r == '\t':
			sb.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	slug := sb.String()
	// Collapse multiple consecutive hyphens.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 64 {
		slug = slug[:64]
	}
	return slug
}
