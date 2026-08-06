package authbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// connectorBroker is a fake host broker for POST /connector/{id}/token.
type connectorBroker struct {
	mu        sync.Mutex
	calls     []string // request paths
	status    int
	expiresIn int
	token     string
}

func (b *connectorBroker) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.calls = append(b.calls, r.URL.Path)
		status, expiresIn, token := b.status, b.expiresIn, b.token
		b.mu.Unlock()

		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer session-secret" {
			t.Errorf("Authorization = %q, want session bearer", got)
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": token,
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}
}

func (b *connectorBroker) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

func newConnectorTokensForTest(t *testing.T, b *connectorBroker, now func() time.Time) *ConnectorTokens {
	t.Helper()
	srv := httptest.NewServer(b.handler(t))
	t.Cleanup(srv.Close)
	cfg := Config{
		BrokerAddr:  strings.TrimPrefix(srv.URL, "http://"),
		BrokerToken: "session-secret",
	}
	opts := []ConnectorTokensOption{WithConnectorHTTPClient(srv.Client())}
	if now != nil {
		opts = append(opts, WithConnectorClock(now))
	}
	return NewConnectorTokens(cfg, nil, opts...)
}

func TestConnectorTokens_FetchAndCache(t *testing.T) {
	broker := &connectorBroker{token: "at-1", expiresIn: 3600}
	tokens := newConnectorTokensForTest(t, broker, nil)

	got, err := tokens.ConnectorToken(context.Background(), "datadog")
	if err != nil {
		t.Fatalf("ConnectorToken: %v", err)
	}
	if got != "at-1" {
		t.Errorf("token = %q, want at-1", got)
	}
	broker.mu.Lock()
	if broker.calls[0] != "/connector/datadog/token" {
		t.Errorf("path = %q, want /connector/datadog/token", broker.calls[0])
	}
	broker.mu.Unlock()

	// A fresh token (>300s to expiry) is served from cache.
	if _, err := tokens.ConnectorToken(context.Background(), "datadog"); err != nil {
		t.Fatalf("cached ConnectorToken: %v", err)
	}
	if broker.callCount() != 1 {
		t.Errorf("broker calls = %d, want 1 (cache hit)", broker.callCount())
	}
}

func TestConnectorTokens_RenewsWithinThreshold(t *testing.T) {
	// expires_in 600s; advance the clock past expiry-300s so the 300s
	// renewal threshold (shared with the SSO session) forces a re-fetch.
	base := time.Now()
	current := base
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return current }

	broker := &connectorBroker{token: "at-1", expiresIn: 600}
	tokens := newConnectorTokensForTest(t, broker, now)

	if _, err := tokens.ConnectorToken(context.Background(), "slack"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	mu.Lock()
	current = base.Add(400 * time.Second) // 200s to expiry < 300s threshold
	mu.Unlock()
	broker.mu.Lock()
	broker.token = "at-2"
	broker.mu.Unlock()

	got, err := tokens.ConnectorToken(context.Background(), "slack")
	if err != nil {
		t.Fatalf("renewal fetch: %v", err)
	}
	if got != "at-2" {
		t.Errorf("token = %q, want renewed at-2", got)
	}
	if broker.callCount() != 2 {
		t.Errorf("broker calls = %d, want 2", broker.callCount())
	}
}

func TestConnectorTokens_KeepsUnexpiredTokenOnBrokerFailure(t *testing.T) {
	base := time.Now()
	current := base
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return current }

	broker := &connectorBroker{token: "at-1", expiresIn: 600}
	tokens := newConnectorTokensForTest(t, broker, now)
	if _, err := tokens.ConnectorToken(context.Background(), "slack"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Within the renewal window but before expiry; broker now erroring.
	mu.Lock()
	current = base.Add(400 * time.Second)
	mu.Unlock()
	broker.mu.Lock()
	broker.status = http.StatusForbidden
	broker.mu.Unlock()

	got, err := tokens.ConnectorToken(context.Background(), "slack")
	if err != nil || got != "at-1" {
		t.Errorf("got (%q, %v), want unexpired cached token", got, err)
	}

	// Past expiry the failure surfaces.
	mu.Lock()
	current = base.Add(700 * time.Second)
	mu.Unlock()
	if _, err := tokens.ConnectorToken(context.Background(), "slack"); err == nil {
		t.Error("expected error once the cached token expired and the broker refuses")
	}
}

func TestConnectorTokens_NotConfigured(t *testing.T) {
	tokens := NewConnectorTokens(Config{}, nil)
	if tokens.Configured() {
		t.Error("Configured() = true for empty config")
	}
	_, err := tokens.ConnectorToken(context.Background(), "datadog")
	if !errors.Is(err, ErrBrokerNotConfigured) {
		t.Errorf("err = %v, want ErrBrokerNotConfigured", err)
	}
}

func TestConnectorTokens_RejectsInvalidID(t *testing.T) {
	broker := &connectorBroker{token: "x", expiresIn: 3600}
	tokens := newConnectorTokensForTest(t, broker, nil)
	for _, id := range []string{"", "Bad", "a/b", "a b", "a_b", "../token"} {
		if _, err := tokens.ConnectorToken(context.Background(), id); err == nil {
			t.Errorf("id %q accepted, want rejection", id)
		}
	}
	if broker.callCount() != 0 {
		t.Errorf("broker reached with an invalid id (%d calls)", broker.callCount())
	}
}
