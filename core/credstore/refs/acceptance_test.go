// Package refs_test — end-to-end acceptance scenario for
// model-secret-references-01KW7M5A WP15.
//
// This file exercises spec §11 steps 1-7 programmatically:
//  1. Expose user:example-api-token via ExposureIndex.Add.
//  2. Verify the locator is resolvable via Resolver.Resolve.
//  3. Run web_fetch against a mock HTTP server with
//     Authorization: Bearer @secret:user:example-api-token.
//  4. Assert mock server received plaintext.
//  5. Assert tool result body contains [redacted: user:example-api-token].
//  6. Assert exactly one KindSecretReferenceResolved audit event.
//  7. Assert substitution with an unknown locator returns ErrUnknownLocator.
package refs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/credstore/refs"
	"github.com/sigil-tech/kaneaz-harness/core/event/secretref"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	webfetch "github.com/sigil-tech/kaneaz-harness/core/tools/webfetch"
)

// ── acceptance helpers ────────────────────────────────────────────────────

// acceptanceEmitter is a thread-safe audit emitter for acceptance tests.
type acceptanceEmitter struct {
	mu     sync.Mutex
	events []secretref.ResolvedEvent
}

func (e *acceptanceEmitter) EmitSecretResolved(_ context.Context, evt secretref.ResolvedEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, evt)
}

func (e *acceptanceEmitter) snapshot() []secretref.ResolvedEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]secretref.ResolvedEvent, len(e.events))
	copy(out, e.events)
	return out
}

func (e *acceptanceEmitter) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = e.events[:0]
}

// buildResolverCtx constructs a resolver backed by the given index and
// attaches it + a fresh sanitizer to ctx.
func buildResolverCtx(
	ctx context.Context,
	idx *secrets.ExposureIndex,
	budget *refs.Budget,
	emitter refs.AuditEmitter,
) (context.Context, *refs.Resolver, *refs.Sanitizer) {
	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		Budget:    budget,
		Emitter:   emitter,
		SessionID: "ses_acceptance",
		Agent:     "chat",
	})
	san := refs.NewSanitizer()
	ctx = refs.WithResolver(ctx, r)
	ctx = refs.WithTurnSanitizer(ctx, san)
	return ctx, r, san
}

// ── step 1-6: happy-path end-to-end ──────────────────────────────────────

// TestAcceptance_WebFetch_SecretSubstituteAndSanitize exercises the core
// e2e flow: expose → resolve → HTTP fetch → assert plaintext on wire,
// redacted in result.
func TestAcceptance_WebFetch_SecretSubstituteAndSanitize(t *testing.T) {
	t.Parallel()

	const (
		locator   = "user:example-api-token"
		plaintext = "Bearer sk-SUPER-SECRET-XYZ"
	)

	// Step 1: expose the locator via ExposureIndex.
	idx := secrets.NewExposureIndex()
	pt := []byte(plaintext)
	idx.Add(secrets.ExposedEntry{
		Locator:     locator,
		Description: "Example API token",
		Scope:       secrets.ScopeSession,
		KindHint:    secrets.KindHintBearer,
	}, pt)
	// Zero our copy after Add.
	for i := range pt {
		pt[i] = 0
	}

	// Step 4: set up a mock HTTP server that echoes the Authorization header
	// in the response body — simulating an API that reflects the token.
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		// Echo the auth header in the response body (simulates a misconfigured
		// API that reflects the credential — worst-case sanitizer test).
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("received: " + gotAuthHeader))
	}))
	defer srv.Close()

	// Step 3: construct resolver + sanitizer and run web_fetch.
	em := &acceptanceEmitter{}
	budget := refs.NewBudget(refs.DefaultBudget)
	ctx, _, _ := buildResolverCtx(context.Background(), idx, budget, em)

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client(), SkipBlockList: true})

	argsJSON, _ := json.Marshal(map[string]any{
		"url":    srv.URL + "/test",
		"method": "GET",
		"headers": map[string]string{
			"Authorization": "@secret:" + locator,
		},
	})
	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("web_fetch.Call: %v", err)
	}

	// Step 4: assert mock server received the plaintext, not the token.
	if gotAuthHeader != plaintext {
		t.Errorf("server received Authorization = %q, want %q", gotAuthHeader, plaintext)
	}

	// Step 5: assert tool result body contains [redacted:…], NOT plaintext.
	var res struct {
		Body    string `json:"body"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error result: %s", result)
	}
	if strings.Contains(res.Body, "SUPER-SECRET") {
		t.Errorf("tool result body contains plaintext — sanitizer failed: %q", res.Body)
	}
	if !strings.Contains(res.Body, "[redacted: "+locator+"]") {
		t.Errorf("tool result body missing redaction placeholder: %q", res.Body)
	}

	// Step 6: exactly one KindSecretReferenceResolved audit event.
	evts := em.snapshot()
	if len(evts) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(evts))
	}
	if evts[0].Locator != locator {
		t.Errorf("event.Locator = %q, want %q", evts[0].Locator, locator)
	}
	if evts[0].Outcome != secretref.OutcomePermit {
		t.Errorf("event.Outcome = %q, want %q", evts[0].Outcome, secretref.OutcomePermit)
	}
	if evts[0].Tool != webfetch.ToolName {
		t.Errorf("event.Tool = %q, want %q", evts[0].Tool, webfetch.ToolName)
	}
}

// ── step 7: denied resolution ─────────────────────────────────────────────

// TestAcceptance_UnknownLocatorDenied verifies that a reference to a locator
// that has not been exposed returns ErrUnknownLocator via Substitute.
func TestAcceptance_UnknownLocatorDenied(t *testing.T) {
	t.Parallel()

	idx := secrets.NewExposureIndex() // empty — no entries
	em := &acceptanceEmitter{}
	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		Emitter:   em,
		SessionID: "ses_acceptance",
		Agent:     "chat",
	})

	ctx := context.Background()
	_, _, err := r.Substitute(ctx, "Authorization: @secret:user:not-exposed", cedar.ResolveContext{
		ToolName: "web_fetch",
	})
	if !errors.Is(err, refs.ErrUnknownLocator) {
		t.Errorf("expected ErrUnknownLocator, got %v", err)
	}

	// Exactly one audit event with OutcomeUnknownLocator.
	evts := em.snapshot()
	if len(evts) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(evts))
	}
	if evts[0].Outcome != secretref.OutcomeUnknownLocator {
		t.Errorf("event.Outcome = %q, want %q", evts[0].Outcome, secretref.OutcomeUnknownLocator)
	}
}

// ── no-plaintext invariant ────────────────────────────────────────────────

// TestAcceptance_NoPlaintextInResolvedResult verifies that after a Substitute
// call, the result string contains the plaintext (caller's responsibility to
// zero it) but the sanitizer correctly redacts any echoed copy. The test
// directly exercises the Sanitizer interface, not the DB.
func TestAcceptance_NoPlaintextInResolvedResult(t *testing.T) {
	t.Parallel()

	const (
		locator   = "user:no-plaintext-test"
		plaintext = "ULTRA_SECRET_VALUE_ACCEPTANCE"
	)

	idx := secrets.NewExposureIndex()
	pt := []byte(plaintext)
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, pt)
	for i := range pt {
		pt[i] = 0
	}

	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_no_plaintext",
		Agent:     "test",
	})

	san := refs.NewSanitizer()
	ctx := refs.WithTurnSanitizer(context.Background(), san)

	// Substitute populates the sanitizer.
	resolved, decs, err := r.Substitute(ctx, "@secret:"+locator, cedar.ResolveContext{ToolName: "test"})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if len(decs) != 1 {
		t.Errorf("expected 1 decision, got %d", len(decs))
	}
	// resolved contains the plaintext — caller would zero it; for the test
	// we verify it resolves correctly then check the sanitizer.
	if resolved != plaintext {
		t.Errorf("resolved = %q, want %q", resolved, plaintext)
	}

	// Sanitizer now has one entry: any body echoing the token is redacted.
	if san.Len() != 1 {
		t.Errorf("sanitizer.Len() = %d, want 1", san.Len())
	}
	echoed := []byte("Response body echoed: " + plaintext + " end of body.")
	sanitized := san.Sanitize(echoed)
	if strings.Contains(string(sanitized), plaintext) {
		t.Errorf("sanitizer did not redact plaintext in echoed body: %q", sanitized)
	}
	if !strings.Contains(string(sanitized), "[redacted: "+locator+"]") {
		t.Errorf("sanitizer missing redaction placeholder: %q", sanitized)
	}

	// Zero the resolved buffer as the caller would.
	refs.ZeroBuffer([]byte(resolved))
}

// TestAcceptance_BudgetEnforcedAcrossResolutions verifies that the per-session
// budget blocks resolutions once the limit is reached.
func TestAcceptance_BudgetEnforcedAcrossResolutions(t *testing.T) {
	t.Parallel()

	const locator = "user:budget-test"

	idx := secrets.NewExposureIndex()
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, []byte("token_value"))

	budget := refs.NewBudget(2)
	r := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		Budget:    budget,
		SessionID: "ses_budget",
		Agent:     "test",
	})

	ctx := context.Background()
	rctx := cedar.ResolveContext{ToolName: "bash"}

	for i := 0; i < 2; i++ {
		resolved, _, err := r.Resolve(ctx, refs.Reference{Locator: locator}, rctx)
		if err != nil {
			t.Fatalf("resolve %d: %v", i+1, err)
		}
		refs.ZeroBuffer(resolved)
	}

	// Third resolution exceeds budget.
	_, _, err := r.Resolve(ctx, refs.Reference{Locator: locator}, rctx)
	if !errors.Is(err, refs.ErrBudgetExhausted) {
		t.Errorf("expected ErrBudgetExhausted on 3rd resolve, got %v", err)
	}
}
