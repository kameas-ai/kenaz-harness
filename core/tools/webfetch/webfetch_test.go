package webfetch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cedarraw "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	policycedar "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
)

// ─── Cedar gate fake ──────────────────────────────────────────────────────────

type fakeGate struct {
	outcome policycedar.Outcome
	reason  string
}

func (g *fakeGate) Evaluate(
	_ context.Context,
	_ cedarraw.EntityUID,
	_ string,
	_ cedarraw.EntityUID,
	_ map[cedarraw.String]cedarraw.Value,
) policycedar.Decision {
	return policycedar.Decision{
		Outcome: g.outcome,
		Reason:  g.reason,
	}
}

// setupResolver creates an ExposureIndex with the given secrets and
// wraps it in a Resolver attached to a Sanitizer.
func setupResolver(t *testing.T, secretMap map[string][]byte) (*refs.Resolver, *refs.Sanitizer, context.Context) {
	t.Helper()
	idx := secrets.NewExposureIndex()
	for locator, plain := range secretMap {
		entry := secrets.ExposedEntry{
			Locator:     locator,
			Description: "test secret",
			Scope:       secrets.ScopeSession,
			KindHint:    secrets.KindHintBearer,
		}
		idx.Add(entry, plain)
	}
	san := refs.NewSanitizer()
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "chat",
	})
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)
	return resolver, san, ctx
}

// TestWebFetch_AuthorizationHeaderSubstitution verifies that a
// @secret: reference in an Authorization header is resolved to plaintext
// before the outbound request, and that the response body has it redacted
// when the server echoes it back.
func TestWebFetch_AuthorizationHeaderSubstitution(t *testing.T) {
	const secret = "my-api-token-xyz"
	// Test server that echoes the Authorization header value in its response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.Contains(auth, secret) {
			t.Errorf("server did not receive plaintext: Authorization=%q", auth)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("echo: " + auth))
	}))
	defer srv.Close()

	resolver, san, ctx := setupResolver(t, map[string][]byte{
		"user:mytoken": []byte(secret),
	})
	_ = resolver

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client(), SkipBlockList: true})
	args := map[string]any{
		"url":    srv.URL + "/api",
		"method": "GET",
		"headers": map[string]string{
			"Authorization": "Bearer @secret:user:mytoken",
		},
	}
	argsJSON, _ := json.Marshal(args)
	raw, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != 200 {
		t.Errorf("status = %d", result.Status)
	}
	// The body should be redacted by the sanitizer (server echoed the token).
	if strings.Contains(result.Body, secret) {
		t.Errorf("plaintext not redacted in tool result: %q", result.Body)
	}
	if !strings.Contains(result.Body, "[redacted: user:mytoken]") {
		t.Errorf("redaction placeholder missing: %q", result.Body)
	}
	_ = san
}

// TestWebFetch_NoReference verifies that a request without @secret:
// references passes through unchanged.
func TestWebFetch_NoReference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client(), SkipBlockList: true})
	args := map[string]any{"url": srv.URL, "method": "GET"}
	argsJSON, _ := json.Marshal(args)
	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != 204 {
		t.Errorf("status = %d; want 204", result.Status)
	}
}

// ─── F-002: IP block list + Cedar network gate ────────────────────────────────

// TestWebFetch_BlockList_IMDS verifies that requests to 169.254.169.254
// (AWS/Azure IMDS endpoint) are blocked at the IP layer before any network
// access.
func TestWebFetch_BlockList_IMDS(t *testing.T) {
	tool := webfetch.New(webfetch.Options{})
	args := map[string]any{"url": "http://169.254.169.254/latest/meta-data/"}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call should not return Go error, got %v", err)
	}
	var result struct {
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected is_error=true for IMDS address")
	}
	if !strings.Contains(result.Error, "blocked") {
		t.Errorf("expected 'blocked' in error message, got: %q", result.Error)
	}
}

// TestWebFetch_BlockList_RFC1918 verifies that requests to a private RFC 1918
// address (10.x.x.x) are blocked unconditionally.
func TestWebFetch_BlockList_RFC1918(t *testing.T) {
	tool := webfetch.New(webfetch.Options{})
	args := map[string]any{"url": "http://10.0.0.1/secret"}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call should not return Go error, got %v", err)
	}
	var result struct {
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected is_error=true for RFC 1918 address 10.0.0.1, got result=%v", result)
	}
}

// TestWebFetch_BlockList_Loopback verifies that requests to 127.0.0.1
// (loopback) are blocked unconditionally.
func TestWebFetch_BlockList_Loopback(t *testing.T) {
	tool := webfetch.New(webfetch.Options{})
	args := map[string]any{"url": "http://127.0.0.1/admin"}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call should not return Go error, got %v", err)
	}
	var result struct {
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected is_error=true for loopback 127.0.0.1")
	}
}

// TestWebFetch_BlockList_RedirectToPrivate verifies that when a server
// redirects to a private address the redirect is blocked. The initial URL
// is allowed (via SkipBlockList) so the hardened client's CheckRedirect
// callback is the thing under test.
func TestWebFetch_BlockList_RedirectToPrivate(t *testing.T) {
	// Server that redirects to a loopback address.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.2/internal", http.StatusFound)
	}))
	defer srv.Close()

	// SkipBlockList=true: bypasses the initial URL check so the test server
	// (127.0.0.1) is reachable. The hardened client's CheckRedirect is still
	// active and will block the 127.0.0.2 redirect target.
	// No custom HTTPClient: the hardened client (with CheckRedirect) is used.
	tool := webfetch.New(webfetch.Options{SkipBlockList: true})
	args := map[string]any{"url": srv.URL + "/start"}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call should not return Go error, got %v", err)
	}
	var result struct {
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected redirect to 127.0.0.2 to be blocked, got: %+v", result)
	}
}

// TestWebFetch_CedarDenial_BlocksRequest verifies that a Cedar Deny on the
// network gate blocks the request before any HTTP dispatch.
func TestWebFetch_CedarDenial_BlocksRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	gate := &fakeGate{outcome: policycedar.Deny, reason: "network policy"}
	tool := webfetch.New(webfetch.Options{
		HTTPClient:    srv.Client(),
		Gate:          gate,
		SkipBlockList: true, // test server binds to loopback; skip IP check so Cedar gate is tested
	})
	args := map[string]any{"url": srv.URL + "/api"}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("Call should not return Go error, got %v", err)
	}
	if called {
		t.Error("HTTP server was called despite Cedar Deny")
	}
	var result struct {
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected is_error=true on Cedar Deny")
	}
}

// TestWebFetch_DeniedResolution verifies that a denied resolution causes
// the tool to return an error result rather than sending the request.
func TestWebFetch_DeniedResolution(t *testing.T) {
	// Setup an index with no entries (so resolution will fail).
	idx := secrets.NewExposureIndex()
	san := refs.NewSanitizer()
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "chat",
	})
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)

	// Server that should NOT be called.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tool := webfetch.New(webfetch.Options{HTTPClient: srv.Client(), SkipBlockList: true})
	args := map[string]any{
		"url":     srv.URL,
		"headers": map[string]string{"Authorization": "Bearer @secret:user:nonexistent"},
	}
	argsJSON, _ := json.Marshal(args)
	raw, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("unexpected error (should return error result, not Go error): %v", err)
	}
	if called {
		t.Error("server was called despite failed resolution")
	}
	var errResult struct {
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &errResult); err != nil {
		t.Fatalf("unmarshal error result: %v", err)
	}
	if errResult.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// ─── LEAK 1 regression: error-path sanitization ───────────────────────────────

// erroringTransport is an http.RoundTripper that always fails, standing in
// for a real DNS/TLS/connection failure. http.Client.Do wraps whatever error
// this returns in a *url.Error that embeds req.URL.String() verbatim — which
// is exactly how a real "no such host" failure leaks a resolved @secret:
// value baked into the request URL.
type erroringTransport struct{}

func (erroringTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial tcp: lookup %s: no such host", req.URL.Hostname())
}

const leakSentinel = "SUPERSECRET-PLAINTEXT-123"

// TestWebFetch_ErrorResult_DoesNotLeakSecret is the falsification test for
// LEAK 1: web_fetch's error-path results bypass the sanitizer entirely.
// A URL carrying a @secret: reference in its query string, on ANY transport
// failure (DNS, TLS, connection reset, timeout — reproduced here via a
// RoundTripper that always errors), has its resolved plaintext embedded in
// *url.Error.Error() by the net/http/net/url machinery, and errorResult()
// marshals that raw error string straight into the tool result with no
// sanitizer pass. That JSON becomes the persisted tool_result move and is
// indexed into FTS verbatim (core/session/migrations_search_fts_tool_rows.go).
//
// This test asserts on `raw` — the actual marshalled bytes Call() returns,
// i.e. the same bytes that get persisted — not on any in-memory intermediate.
func TestWebFetch_ErrorResult_DoesNotLeakSecret(t *testing.T) {
	_, _, ctx := setupResolver(t, map[string][]byte{
		"user:tok": []byte(leakSentinel),
	})

	tool := webfetch.New(webfetch.Options{
		HTTPClient:    &http.Client{Transport: erroringTransport{}},
		SkipBlockList: true,
	})
	args := map[string]any{
		"url": "http://this-host-does-not-exist.invalid/x?api_key=@secret:user:tok",
	}
	argsJSON, _ := json.Marshal(args)

	raw, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call should not return a Go error, got %v", err)
	}

	if strings.Contains(string(raw), leakSentinel) {
		t.Errorf("LEAK: resolved secret plaintext present in marshalled error result: %s", raw)
	}

	var result struct {
		Error   string `json:"error"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected is_error=true for a transport failure, got: %s", raw)
	}
	if !strings.Contains(result.Error, "[redacted:") {
		t.Errorf("expected the error message to carry a redaction placeholder, got: %q", result.Error)
	}
}
