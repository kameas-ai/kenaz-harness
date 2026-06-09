// Package http hosts the HTTP transport implementation of
// transport.Connection. It speaks JSON-RPC 2.0 over POST against a
// recipe-supplied URL, applies a static header template (with
// ${VAR} env-var substitution at Open time), and runs a
// `tools/list` health probe on the cadence the recipe (or
// PoolOptions) requests.
//
// The HTTP connection is connection-per-recipe: each Recipe
// instance gets its own *http.Client + per-recipe Connection. The
// Pool wraps a set of these so the rest of the harness drives a
// uniform Connection interface no matter which transport the
// recipe declared.
package http

import (
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// HeaderTemplate captures the per-recipe header set plus the env
// vars used to expand ${VAR} tokens in header values. ResolveAt is
// the timestamp the resolved set was produced; the redaction logger
// stamps it on diagnostic events so an operator can correlate a
// missing-token warning with a specific Open attempt.
//
// The struct is intentionally small — the Connection holds a pointer
// to one of these per generation.
type HeaderTemplate struct {
	// Raw is the recipe-author-provided template, preserved so a
	// later refresh / restart can re-substitute against a refreshed
	// env without losing the literal source map.
	Raw map[string]string
	// Resolved is the post-substitution header set the transport
	// applies on every outbound POST. Nil when no substitution has
	// run yet.
	Resolved map[string]string
}

// NewHeaderTemplate constructs a HeaderTemplate from the recipe's
// HeadersTemplate. Resolve must be called before the headers are
// applied to outbound requests; until then Resolved is nil.
func NewHeaderTemplate(raw map[string]string) *HeaderTemplate {
	if raw == nil {
		return &HeaderTemplate{Raw: nil}
	}
	cp := make(map[string]string, len(raw))
	for k, v := range raw {
		cp[k] = v
	}
	return &HeaderTemplate{Raw: cp}
}

// Resolve applies env-var substitution to every header value and
// caches the result on Resolved. env carries the resolved env-var
// map produced by recipes.ResolveEnv; missing tokens fall through
// the substitution machinery's warn-and-leave-literal path so the
// transport can still attempt the request rather than failing
// silently. Returns the resolved map for callers that prefer not to
// reach back through the struct.
func (h *HeaderTemplate) Resolve(env map[string]string) map[string]string {
	if h == nil || h.Raw == nil {
		return nil
	}
	h.Resolved = recipes.SubstituteHeaders(h.Raw, env)
	return h.Resolved
}

// Apply writes every resolved header onto headerSetter. The setter
// shape mirrors http.Header / textproto.MIMEHeader so the same
// helper works for *http.Request.Header and any test fake that
// wants to capture the applied set.
//
// If Resolve has not run, Apply is a noop — the Connection is
// expected to call Resolve before any Send/Recv that depends on
// auth headers.
func (h *HeaderTemplate) Apply(setter HeaderSetter) {
	if h == nil || h.Resolved == nil {
		return
	}
	for k, v := range h.Resolved {
		setter.Set(k, v)
	}
}

// HeaderSetter is the minimal write surface HeaderTemplate.Apply
// needs. *http.Header satisfies it; tests can pass a fake to assert
// the applied header set without standing up an *http.Request.
type HeaderSetter interface {
	Set(key, value string)
}

// SensitiveHeaders is the canonical-cased set of header names the
// transport redacts from slog diagnostics. Lookups are
// case-insensitive — IsSensitiveHeader does the lower-casing for
// callers.
//
// The set covers the four header families a misconfigured server
// or a curious developer is most likely to sniff:
//
//   - Authorization: bearer / basic / token credentials.
//   - X-Api-Key: vendor-specific API key headers (GitHub, Anthropic,
//     OpenAI all accept variants).
//   - Cookie: session bearer tokens leak through cookies as readily
//     as through Authorization.
//   - Proxy-Authorization: same family as Authorization but for the
//     hop between client and corporate proxy.
var SensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"x-api-key":           {},
	"cookie":              {},
	"proxy-authorization": {},
}

// IsSensitiveHeader reports whether name names one of the redacted
// header families. Case-insensitive: callers do not need to canon-
// case before asking.
func IsSensitiveHeader(name string) bool {
	_, ok := SensitiveHeaders[strings.ToLower(name)]
	return ok
}

// RedactedValue is the placeholder substituted for any sensitive
// header value emitted to a slog record. Exported so tests can
// assert the exact string the redactor writes.
const RedactedValue = "REDACTED"

// RedactHeaders returns a copy of headers with every sensitive
// header value replaced by RedactedValue. Non-sensitive header
// values are passed through unchanged. nil input returns nil so
// log-record builders can call this unconditionally.
//
// The returned map's keys are the input keys verbatim — the
// redactor does not canon-case keys, so a recipe author who wrote
// `Authorization` and one who wrote `authorization` both surface
// in logs as they appear on the wire (the redaction matching is
// case-insensitive though).
func RedactHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if IsSensitiveHeader(k) {
			out[k] = RedactedValue
		} else {
			out[k] = v
		}
	}
	return out
}
