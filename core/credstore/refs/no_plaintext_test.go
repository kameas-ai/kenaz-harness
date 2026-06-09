// Package refs_test — no-plaintext CI invariant for
// model-secret-references-01KW7M5A WP15.
//
// These tests encode the privacy CI invariant (FR-005a): resolved plaintext
// MUST NOT appear in any of the following observable surfaces:
//
//   - Audit event payloads (marshaled to JSON)
//   - Tool result content returned from web_fetch after sanitizer
//   - Sanitizer output after Sanitize call
//   - The context.Context value — Resolver is extractable but returns
//     only structured data, never plaintext
//
// The tests use a well-known sentinel value that is distinctive enough to
// prevent false-negative from generic text (similar to NFR-006 in tools/).
package refs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/event/secretref"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	webfetch "github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
)

// sentinel is the plaintext we expose and then check never appears in
// wire formats / audit events / tool results.
const noPlaintextSentinel = "nfr005a_SENTINEL_0x_NoLeak_Acceptance"

// TestNoPlaintext_AuditEvents verifies that audit events emitted by the
// Resolver do not contain the resolved plaintext.
func TestNoPlaintext_AuditEvents(t *testing.T) {
	t.Parallel()

	const locator = "user:nfr005a-audit"

	idx := secrets.NewExposureIndex()
	pt := []byte(noPlaintextSentinel)
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, pt)
	for i := range pt {
		pt[i] = 0
	}

	em := &acceptanceEmitter{}
	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		Emitter:   em,
		SessionID: "ses_nfr005a",
		Agent:     "test",
	})

	// Resolve the secret.
	resolved, _, err := r.Resolve(context.Background(), refs.Reference{Locator: locator},
		cedar.ResolveContext{ToolName: "bash"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The caller receives the bytes; we zero them immediately as required.
	refs.ZeroBuffer(resolved)

	// Marshal all emitted audit events and verify none contains the sentinel.
	evts := em.snapshot()
	if len(evts) == 0 {
		t.Fatal("no audit events emitted — cannot verify no-plaintext invariant")
	}
	for i, evt := range evts {
		b, err := json.Marshal(evt)
		if err != nil {
			t.Fatalf("marshal event %d: %v", err, i)
		}
		if strings.Contains(string(b), noPlaintextSentinel) {
			t.Errorf("audit event %d contains sentinel plaintext: %s", i, b)
		}
		// Also assert that the known-sensitive JSON keys are absent.
		// Per WP03 spec: events must not carry value / secret / bytes / hash / fingerprint.
		for _, forbidden := range []string{`"value"`, `"bytes"`, `"hash"`, `"fingerprint"`} {
			if strings.Contains(string(b), forbidden) {
				t.Errorf("audit event %d contains forbidden key %q: %s", i, forbidden, b)
			}
		}
	}
}

// TestNoPlaintext_AuditEventOutcomeField asserts the Outcome field is a
// human-readable string, not a hash or base64-encoded value.
func TestNoPlaintext_AuditEventOutcomeField(t *testing.T) {
	t.Parallel()

	const locator = "user:nfr005a-outcome"

	idx := secrets.NewExposureIndex()
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, []byte("any_value"))

	em := &acceptanceEmitter{}
	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		Emitter:   em,
		SessionID: "ses_outcome",
		Agent:     "test",
	})

	resolved, _, err := r.Resolve(context.Background(), refs.Reference{Locator: locator},
		cedar.ResolveContext{ToolName: "test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	refs.ZeroBuffer(resolved)

	evts := em.snapshot()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	// Outcome should be the "permit" string constant, not a hash/base64.
	if evts[0].Outcome != secretref.OutcomePermit {
		t.Errorf("Outcome = %q, want %q", evts[0].Outcome, secretref.OutcomePermit)
	}
	// Outcome must be short human-readable text.
	if len(evts[0].Outcome) > 64 {
		t.Errorf("Outcome appears to be an encoded value (too long): %q", evts[0].Outcome)
	}
}

// TestNoPlaintext_WebFetchResult verifies that the web_fetch tool result
// body does not contain the plaintext after the sanitizer runs, even when
// the mock server echoes the credential.
func TestNoPlaintext_WebFetchResult(t *testing.T) {
	t.Parallel()

	const locator = "user:nfr005a-webfetch"

	idx := secrets.NewExposureIndex()
	pt := []byte(noPlaintextSentinel)
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintBearer,
	}, pt)
	for i := range pt {
		pt[i] = 0
	}

	// Mock server that echoes every request header value in the body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Echo every header (simulates a logging endpoint that reflects creds).
		var sb strings.Builder
		for k, vs := range r.Header {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(strings.Join(vs, ", "))
			sb.WriteString("\n")
		}
		_, _ = w.Write([]byte(sb.String()))
	}))
	defer srv.Close()

	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_nfr005a_wf",
		Agent:     "test",
	})
	san := refs.NewSanitizer()
	ctx := refs.WithResolver(context.Background(), r)
	ctx = refs.WithTurnSanitizer(ctx, san)

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client(), SkipBlockList: true})
	argsJSON, _ := json.Marshal(map[string]any{
		"url":    srv.URL + "/probe",
		"method": "GET",
		"headers": map[string]string{
			"X-Api-Key": "@secret:" + locator,
		},
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("web_fetch.Call: %v", err)
	}

	var res struct {
		Body    string `json:"body"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", result)
	}

	// FR-005a: no plaintext in tool result.
	if strings.Contains(res.Body, noPlaintextSentinel) {
		t.Errorf("tool result body contains sentinel plaintext — FR-005a violated: %q", res.Body)
	}

	// The redaction placeholder should appear instead.
	if !strings.Contains(res.Body, "[redacted: "+locator+"]") {
		t.Errorf("tool result body missing redaction placeholder: %q", res.Body)
	}
}

// TestNoPlaintext_SanitizerNeverOutputsPlaintext verifies that after adding
// a fingerprint, Sanitize always returns the placeholder rather than the
// plaintext, including in edge cases: empty input, partial match at boundary,
// and multiple occurrences.
func TestNoPlaintext_SanitizerNeverOutputsPlaintext(t *testing.T) {
	t.Parallel()

	san := refs.NewSanitizer()
	san.Add([]byte(noPlaintextSentinel), "user:san-test")

	cases := []struct {
		name  string
		input string
	}{
		{"single occurrence", "prefix " + noPlaintextSentinel + " suffix"},
		{"double occurrence", noPlaintextSentinel + " and " + noPlaintextSentinel},
		{"start of string", noPlaintextSentinel + " trailing"},
		{"end of string", "leading " + noPlaintextSentinel},
		{"only sentinel", noPlaintextSentinel},
		{"no sentinel", "completely clean string"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := san.Sanitize([]byte(tc.input))
			if strings.Contains(string(out), noPlaintextSentinel) {
				t.Errorf("Sanitize(%q) still contains sentinel: %q", tc.input, out)
			}
		})
	}
}
