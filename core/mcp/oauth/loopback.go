package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrStateMismatch is returned when the authorization callback's state does
// not match the value we sent — a CSRF guard.
var ErrStateMismatch = errors.New("oauth: authorization callback state mismatch")

// ErrAuthorizationDenied is returned when the authorization server reports an
// error on the redirect (e.g. the user denied consent).
var ErrAuthorizationDenied = errors.New("oauth: authorization denied")

// InteractiveConfig parameterizes one interactive authorization-code+PKCE run.
type InteractiveConfig struct {
	// AuthServer is the resolved authorization server metadata.
	AuthServer *AuthServerMetadata
	// ClientID is the registered OAuth client identifier. Required: GitHub
	// (and most providers) do not support dynamic client registration, so the
	// caller must supply a pre-registered public client id.
	ClientID string
	// Scopes requested for the grant.
	Scopes []string
	// OpenBrowser is invoked with the authorization URL. Injected so tests can
	// drive the callback without a real browser; production passes a
	// system-browser opener.
	OpenBrowser func(authURL string) error
	// HTTPClient is used for the token exchange; nil → http.DefaultClient.
	HTTPClient *http.Client
	// Now stamps token expiry; nil → time.Now.
	Now func() time.Time
}

// AuthorizeInteractive runs the full authorization-code + PKCE grant against a
// loopback redirect URI. It starts an ephemeral 127.0.0.1 listener, opens the
// authorization URL via cfg.OpenBrowser, waits for the redirect, validates
// state, and exchanges the code for tokens. The listener is always torn down
// before returning. ctx cancellation (and an internal timeout) bound the wait.
func AuthorizeInteractive(ctx context.Context, cfg InteractiveConfig) (*Tokens, error) {
	if cfg.AuthServer == nil {
		return nil, errors.New("oauth: nil authorization server metadata")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("oauth: empty client_id (provider requires a registered OAuth app)")
	}
	if cfg.OpenBrowser == nil {
		return nil, errors.New("oauth: no OpenBrowser hook provided")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	pkce, err := NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomState()
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("oauth: loopback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	type cbResult struct {
		code string
		err  error
	}
	resultCh := make(chan cbResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writeBrowserMessage(w, "Authorization failed. You may close this tab.")
			resultCh <- cbResult{err: fmt.Errorf("%w: %s: %s", ErrAuthorizationDenied, e, q.Get("error_description"))}
			return
		}
		if q.Get("state") != state {
			writeBrowserMessage(w, "State mismatch. You may close this tab.")
			resultCh <- cbResult{err: ErrStateMismatch}
			return
		}
		code := q.Get("code")
		if code == "" {
			writeBrowserMessage(w, "Missing authorization code. You may close this tab.")
			resultCh <- cbResult{err: errors.New("oauth: callback carried no code")}
			return
		}
		writeBrowserMessage(w, "Signed in. You may close this tab and return to the app.")
		resultCh <- cbResult{code: code}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := BuildAuthorizationURL(cfg.AuthServer, cfg.ClientID, redirectURI, state, cfg.Scopes, pkce)
	if err := cfg.OpenBrowser(authURL); err != nil {
		return nil, fmt.Errorf("oauth: open browser: %w", err)
	}

	// Bound the wait: ctx, or a 5-minute ceiling for the user to authorize.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	select {
	case <-waitCtx.Done():
		return nil, fmt.Errorf("oauth: authorization wait: %w", waitCtx.Err())
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		return ExchangeCode(ctx, cfg.HTTPClient, cfg.AuthServer, cfg.ClientID, res.code, redirectURI, pkce.Verifier, nowFn())
	}
}

func writeBrowserMessage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:system-ui;padding:2rem\"><p>%s</p></body></html>", msg)
}
