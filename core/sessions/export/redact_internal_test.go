package export

// Internal coverage for `redactStructured` — the recursive walk over a
// decoded JSON value.
//
// WHY THIS FILE IS NOT AN EXPORTED-BYTES TEST, stated plainly because the
// rule elsewhere in this package is that assertions go on the file:
//
// No renderer prints a tool ARGUMENT VALUE. v0.63.0 (ExportFormatVersion
// 1 → 2) removed `tool_calls[].arguments` from the JSON export and
// replaced the markdown raw-JSON block with a names-and-types summary,
// precisely because the scanner could not walk a nested object. So the
// deep walk added here has no byte-reachable path TODAY — its
// byte-observable half (argument KEYS, which the summary does print) is
// pinned on the exported bytes in redact_leak_test.go, and everything
// below is the half that would matter the moment a renderer, a share
// payload, or a future export version reads an argument value again.
//
// That is a real risk, not a hypothetical one: `moves.go` records that
// nothing but convention keeps `Message.ModelLayerToolArgs()` out of the
// document, and no gate enforces it. A scanner that fails closed on
// values it cannot reach is the difference between "we removed the
// printer" and "we fixed the leak".
//
// MUTATION EVIDENCE — each RUN, results as observed
// (`go test ./core/sessions/export/ -count=1`):
//
//   - stop recursing in `redactStructured`'s `map[string]any` and `[]any`
//     fast paths → TestRedactStructured_WalksNestedAndArrays,
//     TestRedactStructured_KeyNameForcesValue,
//     TestRedactStructured_DepthBoundFailsClosed,
//     TestRedactStructured_CycleTerminates and
//     TestRedactMessages_ScansArgumentKeysAndValues FAIL.
//   - drop `forced || secretNamingKeyRe.MatchString(k)` →
//     TestRedactStructured_KeyNameForcesValue and
//     TestRedactMessages_ScansArgumentKeysAndValues FAIL.
//   - return `v` instead of the marker at `depth > MaxRedactDepth` →
//     TestRedactStructured_DepthBoundFailsClosed FAILS (the secret below
//     the limit comes back verbatim).
//   - remove the `seen` cycle guard from the `map[string]any` fast path →
//     TestRedactStructured_CycleTerminates FAILS on its 10 s watchdog
//     ("redactStructured did not terminate on a cyclic map"). Note the
//     guard has to be on the FAST path: `map[string]any` is what
//     `encoding/json` produces, so it is the type a cyclic structure
//     actually arrives as, and the reflect fallback below never sees it.
//   - revert `redactMessages` to the pre-fix shallow argument loop →
//     TestRedactMessages_ScansArgumentKeysAndValues FAILS. This is the
//     ONLY test in the package that mutation kills; see the note on that
//     function for why no exported-bytes test can.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

func walk(v any) any {
	return redactStructured(v, 0, false, make(map[uintptr]struct{}))
}

// mustJSON renders the walk result so assertions read against the shape a
// consumer would serialise, not against Go types.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

const walkSecret = "sk-ant-api03-WALKSECRET0123456789abcdefg"

func TestRedactStructured_WalksNestedAndArrays(t *testing.T) {
	t.Parallel()
	cases := map[string]any{
		"three levels deep":  map[string]any{"a": map[string]any{"b": map[string]any{"c": walkSecret}}},
		"inside an array":    []any{"ok", walkSecret},
		"array in an object": map[string]any{"body": []any{"ok", walkSecret}},
		"object in an array": []any{map[string]any{"k": walkSecret}},
		"array in an array":  []any{[]any{[]any{walkSecret}}},
	}
	for name, in := range cases {
		got := mustJSON(t, walk(in))
		if strings.Contains(got, walkSecret) {
			t.Errorf("%s: secret survived the walk: %s", name, got)
		}
		if !strings.Contains(got, "REDACTED:anthropic-key") {
			t.Errorf("%s: no marker written, so nothing was scanned: %s", name, got)
		}
	}
}

// TestRedactStructured_KeyNameForcesValue: the value matches no
// credential pattern. The key is the entire reason it is a secret.
func TestRedactStructured_KeyNameForcesValue(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"authorization", "Authorization", "api_key", "apiKey", "x-api-key",
		"password", "passwd", "cookie", "set-cookie", "client_secret",
		"aws_secret_access_key", "refresh_token", "private_key", "token", "secret",
	} {
		in := map[string]any{key: "plain-opaque-value-1234"}
		got := mustJSON(t, walk(in))
		if strings.Contains(got, "plain-opaque-value-1234") {
			t.Errorf("key %q did not force its value to be redacted: %s", key, got)
		}
	}

	// The force carries into a nested container, and the container SHAPE
	// survives so a names-and-types summary still reports `object`.
	nested := map[string]any{"authorization": map[string]any{"scheme": "Custom", "value": "opaque-1234"}}
	got := mustJSON(t, walk(nested))
	if strings.Contains(got, "opaque-1234") || strings.Contains(got, "Custom") {
		t.Errorf("force did not carry into the nested container: %s", got)
	}
	if !strings.Contains(got, `"scheme"`) {
		t.Errorf("the container collapsed instead of being redacted in place: %s", got)
	}
}

// TestRedactStructured_KeyNameRuleIsAnchored is the over-redaction guard
// for the key-name rule: `tokens_used` is a count, not a credential.
func TestRedactStructured_KeyNameRuleIsAnchored(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"tokens_used":  4096,
		"max_tokens":   8192,
		"secretariat":  "a horse",
		"passwordless": true,
		"description":  "rotate the token when it expires",
	}
	got := mustJSON(t, walk(in))
	for _, want := range []string{"4096", "8192", "a horse", "rotate the token when it expires"} {
		if !strings.Contains(got, want) {
			t.Errorf("OVER-REDACTION: %q was eaten by the key-name rule: %s", want, got)
		}
	}
}

func TestRedactStructured_KeyThatIsItselfASecret(t *testing.T) {
	t.Parallel()
	in := map[string]any{walkSecret: "harmless"}
	got := mustJSON(t, walk(in))
	if strings.Contains(got, walkSecret) {
		t.Errorf("a map KEY that is a credential was copied through: %s", got)
	}
	if !strings.Contains(got, "harmless") {
		t.Errorf("redacting the key destroyed the value: %s", got)
	}
}

// TestRedactStructured_NonStringLeavesAreScanned: a json.Number, a
// []byte, or any Stringer stringifies into something a consumer prints.
func TestRedactStructured_NonStringLeavesAreScanned(t *testing.T) {
	t.Parallel()
	var decoded any
	dec := json.NewDecoder(strings.NewReader(`{"n": 12345678901234567890}`))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A number is not a credential — it must survive.
	if got := mustJSON(t, walk(decoded)); !strings.Contains(got, "12345678901234567890") {
		t.Errorf("a json.Number was mangled: %s", got)
	}
	// A []byte holding a key is.
	in := map[string]any{"blob": []byte(walkSecret)}
	if got := mustJSON(t, walk(in)); strings.Contains(got, walkSecret) {
		t.Errorf("a []byte leaf carried a credential through: %s", got)
	}
}

// TestRedactStructured_DepthBoundFailsClosed: past MaxRedactDepth the
// value is replaced, not passed through. A redactor that gives up must
// fail closed or the depth bound is the bypass.
func TestRedactStructured_DepthBoundFailsClosed(t *testing.T) {
	t.Parallel()
	var deep any = walkSecret
	for i := 0; i < MaxRedactDepth+10; i++ {
		deep = map[string]any{"n": deep}
	}
	got := mustJSON(t, walk(deep))
	if strings.Contains(got, walkSecret) {
		t.Errorf("a secret below the depth limit was returned verbatim: %s", got)
	}
	if !strings.Contains(got, "REDACTED:depth-limit") {
		t.Errorf("expected the depth-limit marker: %s", got)
	}

	// And a structure just inside the limit is still walked normally.
	var shallow any = walkSecret
	for i := 0; i < MaxRedactDepth-2; i++ {
		shallow = map[string]any{"n": shallow}
	}
	if got := mustJSON(t, walk(shallow)); !strings.Contains(got, "REDACTED:anthropic-key") {
		t.Errorf("a structure inside the depth limit was not walked: %s", got)
	}
}

// TestRedactStructured_CycleTerminates: a self-referential map must not
// recurse forever. The branching case (two self-references) is the one
// depth alone cannot save you from — 2^depth expansions.
func TestRedactStructured_CycleTerminates(t *testing.T) {
	t.Parallel()
	cyclic := map[string]any{"name": "root", "leak": walkSecret}
	cyclic["self"] = cyclic
	cyclic["also"] = cyclic

	done := make(chan string, 1)
	go func() { done <- mustJSONNoFatal(walk(cyclic)) }()
	select {
	case got := <-done:
		if strings.Contains(got, walkSecret) {
			t.Errorf("the cycle guard let the secret through: %s", got)
		}
		if !strings.Contains(got, "REDACTED:cycle") {
			t.Errorf("expected a cycle marker: %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("redactStructured did not terminate on a cyclic map")
	}
}

// TestRedactMessages_ScansArgumentKeysAndValues pins `redactMessages`
// itself rather than the exported bytes, and the reason is worth stating
// because it is a finding, not a shortcut.
//
// Argument KEYS are scanned TWICE on the byte-reachable path: once here
// and once in `argsSummaryFromValues` (moves.go), which calls RedactValue
// on every name it prints. Verified by mutation: removing EITHER scan
// leaves every exported-bytes assertion in this package green; removing
// BOTH kills TestExport_ArgumentKeysAreScanned,
// TestExport_RedactsArgumentNames and
// TestExport_AdversarialFixture_NothingReachesEitherFile. So no
// exported-bytes test can pin this half on its own — the redundancy is
// real and is the point, but it means the pin has to live at this level.
func TestRedactMessages_ScansArgumentKeysAndValues(t *testing.T) {
	t.Parallel()
	in := []session.Message{{
		Role: session.RoleAssistant,
		ToolCalls: []session.ToolCall{{
			Name: "http",
			Arguments: map[string]any{
				walkSecret: "harmless",
				"deep":     map[string]any{"a": []any{map[string]any{"b": walkSecret}}},
				"headers":  map[string]any{"authorization": "opaque-value-1234"},
				"plain":    "nothing to see",
			},
		}},
	}}
	got := redactMessages(in)
	args := got[0].ToolCalls[0].Arguments

	for k := range args {
		if strings.Contains(k, walkSecret) {
			t.Errorf("argument KEY was not scanned: %q", k)
		}
	}
	blob := mustJSON(t, args)
	if strings.Contains(blob, walkSecret) {
		t.Errorf("a nested argument value survived redactMessages: %s", blob)
	}
	if strings.Contains(blob, "opaque-value-1234") {
		t.Errorf("a value under `authorization` survived redactMessages: %s", blob)
	}
	if !strings.Contains(blob, "nothing to see") {
		t.Errorf("OVER-REDACTION: an ordinary argument value was eaten: %s", blob)
	}

	// And the input must not have been mutated — the caller is still
	// looking at these messages in the chat window.
	if _, ok := in[0].ToolCalls[0].Arguments[walkSecret]; !ok {
		t.Error("redactMessages mutated the caller's argument map in place")
	}
}

// TestRedactMessages_DoesNotMutateTheCallersMessages pins the deep copy.
// `session.Message` carries slices and a `*MediaSource`; a struct copy
// shares them, so redacting an attachment filename in place would rewrite
// the session the user is still reading.
func TestRedactMessages_DoesNotMutateTheCallersMessages(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "uri", MediaType: "image/png", URI: "https://x/" + walkSecret}
	in := []session.Message{{
		Role:          session.RoleUser,
		Content:       "hello " + walkSecret,
		ContentBlocks: []llm.ContentBlock{{Type: "image", Source: src}},
	}}
	got := redactMessages(in)

	if strings.Contains(got[0].ContentBlocks[0].Source.URI, walkSecret) {
		t.Error("attachment URI was not scanned")
	}
	if !strings.Contains(src.URI, walkSecret) {
		t.Error("redactMessages rewrote the caller's MediaSource in place")
	}
	if !strings.Contains(in[0].Content, walkSecret) {
		t.Error("redactMessages rewrote the caller's Content in place")
	}
}

func mustJSONNoFatal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "marshal error: " + err.Error()
	}
	return string(b)
}
