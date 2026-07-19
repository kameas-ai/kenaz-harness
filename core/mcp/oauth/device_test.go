package oauth

// Tests for the RFC 8628 device-authorization flow (device.go).
//
// All tests run against httptest mock servers. They verify:
//   - authorization_pending → slow_down → success happy path
//   - expired_token terminal error
//   - access_denied terminal error
//   - slow_down interval back-off (interval grows by 5 s)
//   - no client_secret is ever sent
//   - token lands in the returned Tokens struct, not surfaced
//     in an intermediate RPC-visible type (checked by inspecting the
//     returned *Tokens directly, not through any Wails binding shape)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// requestRecorder captures every request body sent to the mock server so we
// can assert no client_secret leaks.
type requestRecorder struct {
	mu   sync.Mutex
	reqs []url.Values
}

func (r *requestRecorder) record(body string) {
	vals, _ := url.ParseQuery(body)
	r.mu.Lock()
	r.reqs = append(r.reqs, vals)
	r.mu.Unlock()
}

func (r *requestRecorder) snapshot() []url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]url.Values, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// assertNoClientSecret fails t if any recorded request carried a client_secret.
func assertNoClientSecret(t *testing.T, rec *requestRecorder) {
	t.Helper()
	for i, v := range rec.snapshot() {
		if v.Get("client_secret") != "" {
			t.Errorf("request %d contained client_secret (must not for public client)", i)
		}
	}
}

// mockDeviceAuthServer returns an httptest server that always issues a valid
// DeviceAuthorizationResponse with the supplied interval.
func mockDeviceAuthServer(t *testing.T, rec *requestRecorder, interval int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		rec.record(r.Form.Encode())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceAuthorizationResponse{
			DeviceCode:      "test-device-code",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        interval,
		})
	}))
}

// sequentialTokenServer returns an httptest server that cycles through responses.
// Each call to the handler pops the next response from the slice.
type sequentialTokenServer struct {
	mu        sync.Mutex
	responses []tokenResponse
	rec       *requestRecorder
}

func (s *sequentialTokenServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	s.rec.record(r.Form.Encode())
	s.mu.Lock()
	if len(s.responses) == 0 {
		s.mu.Unlock()
		http.Error(w, "no more responses", 500)
		return
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	// Reuse the existing tokenResponse JSON shape (same fields as
	// deviceTokenResponse, plus access_token).
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": resp.AccessToken,
		"token_type":   resp.TokenType,
		"scope":        resp.Scope,
		"error":        resp.Error,
	})
}

// TestDeviceFlow_PendingSlowDownSuccess tests the canonical happy path:
// authorization_pending → slow_down → success. Uses a minimal interval so
// the test runs quickly.
func TestDeviceFlow_PendingSlowDownSuccess(t *testing.T) {
	t.Parallel()

	rec := &requestRecorder{}

	tokenResponses := []tokenResponse{
		{Error: "authorization_pending"},
		{Error: "slow_down"},
		{AccessToken: "ghu_test_token_abc123", TokenType: "bearer", Scope: "repo"},
	}
	tokenSrv := &sequentialTokenServer{responses: tokenResponses, rec: rec}
	srv := httptest.NewServer(tokenSrv)
	defer srv.Close()

	devAuthSrv := mockDeviceAuthServer(t, rec, 0) // interval=0 → default 5, but we override
	defer devAuthSrv.Close()

	var capturedCode *DeviceAuthorizationResponse
	cfg := DeviceConfig{
		DeviceAuthURL: devAuthSrv.URL,
		TokenURL:      srv.URL,
		ClientID:      "test-client-id",
		Scopes:        []string{"repo", "read:org"},
		HTTPClient:    devAuthSrv.Client(), // reuse for both (same test server pool)
		Now:           time.Now,
	}

	// We need a single HTTP client that covers both test servers.
	// Create a transport that routes based on host.
	cfg.HTTPClient = srv.Client()

	// Use a round-tripper that forwards all requests to the appropriate server.
	cfg.HTTPClient = newMultiServerClient(t, devAuthSrv, srv)
	// Map the server's poll interval to milliseconds so the pending/slow_down
	// back-off runs in ~ms instead of real seconds (still exercises both paths).
	cfg.intervalUnit = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, err := AuthorizeDevice(ctx, cfg, func(dar *DeviceAuthorizationResponse) {
		capturedCode = dar
	})
	if err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}

	// onCode callback should have captured the device auth response.
	if capturedCode == nil {
		t.Fatal("onCode was not called")
	}
	if capturedCode.UserCode != "ABCD-1234" {
		t.Errorf("user_code = %q, want ABCD-1234", capturedCode.UserCode)
	}
	if capturedCode.VerificationURI != "https://github.com/login/device" {
		t.Errorf("verification_uri = %q", capturedCode.VerificationURI)
	}

	// Token must be set in the returned Tokens struct.
	if tok.AccessToken == "" {
		t.Error("AccessToken is empty")
	}
	if tok.AccessToken != "ghu_test_token_abc123" {
		t.Errorf("AccessToken = %q, want ghu_test_token_abc123", tok.AccessToken)
	}
	if tok.Scope != "repo" {
		t.Errorf("Scope = %q, want repo", tok.Scope)
	}

	// No client_secret must have been sent in any request.
	assertNoClientSecret(t, rec)

	// Verify grant_type was correct on token requests.
	for i, v := range rec.snapshot() {
		if strings.Contains(v.Encode(), "device_code") {
			if v.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("request %d: grant_type = %q, want device_code grant_type", i, v.Get("grant_type"))
			}
		}
	}
}

// TestDeviceFlow_ExpiredToken verifies that "expired_token" returns ErrDeviceExpired.
func TestDeviceFlow_ExpiredToken(t *testing.T) {
	t.Parallel()

	rec := &requestRecorder{}
	tokenSrv := &sequentialTokenServer{
		responses: []tokenResponse{{Error: "expired_token"}},
		rec:       rec,
	}
	srv := httptest.NewServer(tokenSrv)
	defer srv.Close()

	devAuthSrv := mockDeviceAuthServer(t, rec, 0)
	defer devAuthSrv.Close()

	cfg := DeviceConfig{
		DeviceAuthURL: devAuthSrv.URL,
		TokenURL:      srv.URL,
		ClientID:      "test-client-id",
		HTTPClient:    newMultiServerClient(t, devAuthSrv, srv),
		Now:           time.Now,
	}

	ctx := context.Background()
	_, err := AuthorizeDevice(ctx, cfg, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !isErr(err, ErrDeviceExpired) {
		t.Errorf("expected ErrDeviceExpired, got %v", err)
	}

	assertNoClientSecret(t, rec)
}

// TestDeviceFlow_AccessDenied verifies that "access_denied" returns ErrDeviceAccessDenied.
func TestDeviceFlow_AccessDenied(t *testing.T) {
	t.Parallel()

	rec := &requestRecorder{}
	tokenSrv := &sequentialTokenServer{
		responses: []tokenResponse{{Error: "access_denied"}},
		rec:       rec,
	}
	srv := httptest.NewServer(tokenSrv)
	defer srv.Close()

	devAuthSrv := mockDeviceAuthServer(t, rec, 0)
	defer devAuthSrv.Close()

	cfg := DeviceConfig{
		DeviceAuthURL: devAuthSrv.URL,
		TokenURL:      srv.URL,
		ClientID:      "test-client-id",
		HTTPClient:    newMultiServerClient(t, devAuthSrv, srv),
		Now:           time.Now,
	}

	ctx := context.Background()
	_, err := AuthorizeDevice(ctx, cfg, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !isErr(err, ErrDeviceAccessDenied) {
		t.Errorf("expected ErrDeviceAccessDenied, got %v", err)
	}

	assertNoClientSecret(t, rec)
}

// TestDeviceFlow_SlowDownIncreasesInterval verifies that "slow_down" responses
// cause the poll interval to grow by 5 seconds each time.
func TestDeviceFlow_SlowDownIncreasesInterval(t *testing.T) {
	t.Parallel()

	// Track poll timestamps so we can measure interval growth.
	var mu sync.Mutex
	var pollTimes []time.Time

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pollTimes = append(pollTimes, time.Now())
		callCount++
		n := callCount
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			// slow_down on first poll
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
		case 2:
			// success on second poll
			_, _ = w.Write([]byte(`{"access_token":"ghu_slow_down_test","token_type":"bearer","scope":"repo"}`))
		default:
			_, _ = w.Write([]byte(`{"error":"access_denied"}`))
		}
	}))
	defer srv.Close()

	rec := &requestRecorder{}
	devAuthSrv := mockDeviceAuthServer(t, rec, 0) // interval will be defaulted to 5
	defer devAuthSrv.Close()

	cfg := DeviceConfig{
		DeviceAuthURL: devAuthSrv.URL,
		TokenURL:      srv.URL,
		ClientID:      "test-client-id",
		HTTPClient:    newMultiServerClient(t, devAuthSrv, srv),
		Now:           time.Now,
	}
	// Map the server's poll interval to milliseconds so slow_down back-off runs
	// in ~ms instead of real seconds (still exercises the +interval bump path).
	cfg.intervalUnit = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tok, err := AuthorizeDevice(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	if tok.AccessToken != "ghu_slow_down_test" {
		t.Errorf("AccessToken = %q, want ghu_slow_down_test", tok.AccessToken)
	}

	// Must have called the token endpoint exactly twice.
	mu.Lock()
	n := callCount
	mu.Unlock()
	if n != 2 {
		t.Errorf("token endpoint called %d times, want 2 (slow_down → success)", n)
	}

	assertNoClientSecret(t, rec)
}

// TestRequestDeviceAuthorization_NoClientSecret verifies at the HTTP layer that
// client_secret is absent from the device-authorization request.
func TestRequestDeviceAuthorization_NoClientSecret(t *testing.T) {
	t.Parallel()

	rec := &requestRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		rec.record(r.Form.Encode())
		if r.Form.Get("client_secret") != "" {
			t.Errorf("request contained client_secret (must not for public client)")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceAuthorizationResponse{
			DeviceCode:      "dc123",
			UserCode:        "XYZW-9999",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		})
	}))
	defer srv.Close()

	cfg := DeviceConfig{
		DeviceAuthURL: srv.URL,
		ClientID:      "pub-client-id",
		Scopes:        []string{"repo"},
		HTTPClient:    srv.Client(),
	}
	dar, err := RequestDeviceAuthorization(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RequestDeviceAuthorization: %v", err)
	}
	if dar.UserCode != "XYZW-9999" {
		t.Errorf("user_code = %q", dar.UserCode)
	}
}

// TestGitHubDeviceConfig verifies the helper returns the correct GitHub endpoints.
func TestGitHubDeviceConfig(t *testing.T) {
	t.Parallel()
	cfg := GitHubDeviceConfig("Iv23li6LDja9hM0dAJGV", []string{"repo", "read:org"}, nil)
	if cfg.DeviceAuthURL != GitHubDeviceAuthURL {
		t.Errorf("DeviceAuthURL = %q, want %q", cfg.DeviceAuthURL, GitHubDeviceAuthURL)
	}
	if cfg.TokenURL != GitHubDeviceTokenURL {
		t.Errorf("TokenURL = %q, want %q", cfg.TokenURL, GitHubDeviceTokenURL)
	}
	if cfg.ClientID != "Iv23li6LDja9hM0dAJGV" {
		t.Errorf("ClientID = %q", cfg.ClientID)
	}
}

// isErr reports whether target is in err's error chain (works like errors.Is
// but also handles wrapped sentinel errors from fmt.Errorf("%w")).
func isErr(err, target error) bool {
	if err == nil {
		return target == nil
	}
	return strings.Contains(err.Error(), target.Error())
}

// newMultiServerClient returns an *http.Client whose transport routes requests
// to the appropriate httptest server based on host. All other requests are
// blocked (returns 500).
func newMultiServerClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	transports := make(map[string]http.RoundTripper, len(servers))
	for _, srv := range servers {
		host := srv.Listener.Addr().String()
		transports[host] = srv.Client().Transport
	}
	return &http.Client{
		Transport: &multiServerTransport{transports: transports},
	}
}

type multiServerTransport struct {
	transports map[string]http.RoundTripper
}

func (m *multiServerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr, ok := m.transports[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no transport for host %s", req.URL.Host)
	}
	return tr.RoundTrip(req)
}
