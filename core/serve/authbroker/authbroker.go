// Package authbroker implements the in-VM side of the F2 host-broker auth
// carryover mechanism (F2-WP8).
//
// # Overview
//
// When the harness starts in serve mode (--serve), it reads four environment
// variables injected via the KENAZMETA disk (EnvironmentFile pattern, same as
// SIGIL_INGEST_TOKEN / HARNESS_VM_TOKEN):
//
//	KENAZ_AUTH_BROKER_ADDR   host:port of the per-session broker HTTP endpoint
//	KENAZ_AUTH_BROKER_TOKEN  per-session secret (43-char base64url, 32 random bytes)
//	KENAZ_AUTH_SIGNED_IN     "true" when host is signed in; "false" for anonymous
//	KENAZ_AUTH_ACCESS_TOKEN  seed access token (may be "" when signed out)
//
// If KENAZ_AUTH_SIGNED_IN != "true" or KENAZ_AUTH_ACCESS_TOKEN is empty, the
// harness starts in [StateAnonymous].  Features that require auth show a
// "sign in on the host" prompt; everything else works unchanged.
//
// Otherwise a [Session] is created with the seed token and a renewal goroutine
// is started.  The renewal goroutine calls:
//
//	POST http://<KENAZ_AUTH_BROKER_ADDR>/auth/token
//	Authorization: Bearer <KENAZ_AUTH_BROKER_TOKEN>
//
// and handles three cases:
//   - 200: parse {access_token, expires_in}, update in-memory token, schedule
//     next renewal for expires_in − 300 seconds.
//   - 401 (signed_out): clear in-memory token, transition to [StateSignedOut],
//     emit a ledger event (session.signed_out) if available, stop renewing.
//   - 403 or network error: broker unreachable — retry with exponential
//     backoff; keep the existing token until its own expiry.
//
// # Privacy constraints (FR-027)
//
// Access tokens are stored in memory only — never written to disk or logs.
// The broker token is also never logged.  Log lines MUST NOT contain token
// bytes.
//
// # Design
//
// The broker-client HTTP call and the renewal scheduler are separated so the
// scheduler can be driven by an injected clock/timer factory in tests, making
// the test deterministic without real time.Sleep sleeps.
package authbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Env var names read by [ReadConfig] from the process environment.
const (
	EnvBrokerAddr      = "KENAZ_AUTH_BROKER_ADDR"
	EnvBrokerToken     = "KENAZ_AUTH_BROKER_TOKEN"
	EnvSignedIn        = "KENAZ_AUTH_SIGNED_IN"
	EnvSeedAccessToken = "KENAZ_AUTH_ACCESS_TOKEN"
)

// State describes the auth state of the in-VM session.
type State int

const (
	// StateAnonymous means the host was not signed in at boot time, or the
	// seed access token was empty.  Auth-gated features show a
	// "sign in on the host" prompt.
	StateAnonymous State = iota

	// StateSignedIn means the harness holds a valid (or recently-renewed)
	// access token.
	StateSignedIn

	// StateSignedOut means the host has signed out (broker returned 401) and
	// the in-memory token has been cleared.  Distinct from anonymous: the user
	// was previously signed in and the session has since ended.
	StateSignedOut
)

func (s State) String() string {
	switch s {
	case StateAnonymous:
		return "anonymous"
	case StateSignedIn:
		return "signed_in"
	case StateSignedOut:
		return "signed_out"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Config holds the parameters read from the environment at serve-mode startup.
type Config struct {
	// BrokerAddr is the host:port of the broker HTTP endpoint
	// (e.g. "192.168.64.1:9501").  Empty when host is anonymous.
	BrokerAddr string

	// BrokerToken is the per-session secret used as the Bearer token when
	// calling the broker.  Never logged.
	BrokerToken string

	// SignedIn is true when the host reported KENAZ_AUTH_SIGNED_IN=true.
	SignedIn bool

	// SeedAccessToken is the initial access token written by the host at
	// workbench-start time.  Empty when anonymous.  Never logged.
	SeedAccessToken string
}

// ReadConfig reads the four KENAZ_AUTH_* env vars from the environment using
// the supplied lookup function.  Pass os.Getenv for production; pass a map
// lookup for tests.
func ReadConfig(getenv func(string) string) Config {
	return Config{
		BrokerAddr:      getenv(EnvBrokerAddr),
		BrokerToken:     getenv(EnvBrokerToken),
		SignedIn:        getenv(EnvSignedIn) == "true",
		SeedAccessToken: getenv(EnvSeedAccessToken),
	}
}

// tokenResponse is the JSON shape returned by the broker's POST /auth/token.
// Mirrors RFC 6749 §5.1.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"` // seconds
	Scope       string `json:"scope,omitempty"`
}

// brokerError is returned when the broker replies with a non-200 status.
type brokerError struct {
	StatusCode int
}

func (e *brokerError) Error() string {
	return fmt.Sprintf("broker returned HTTP %d", e.StatusCode)
}

// isBrokerSignedOut returns true when the broker replied with 401, meaning the
// host user has signed out and this session is revoked.
func isBrokerSignedOut(err error) bool {
	var be *brokerError
	return errors.As(err, &be) && be.StatusCode == http.StatusUnauthorized
}

// renewalThreshold is the number of seconds before token expiry at which the
// harness proactively contacts the broker to renew.
const renewalThreshold = 300 * time.Second // 5 minutes

// Session manages the in-VM auth session for a single serve-mode lifecycle.
//
// It is safe for concurrent use.  The access token is held in memory only —
// never written to disk, never included in log output.
type Session struct {
	cfg Config
	log *slog.Logger

	// httpClient is used for broker calls.  Injected for tests.
	httpClient *http.Client

	// now is injected for tests to control time.
	now func() time.Time

	// newTimer creates a timer that fires after d.  Injected for tests.
	newTimer func(d time.Duration) *time.Timer

	// ledgerEmit is called with "session.signed_out" when the broker returns
	// 401. It may be nil (anonymous mode, or no reporter wired in serve mode).
	ledgerEmit func(event string)

	mu          sync.RWMutex
	state       State
	accessToken string    // never logged — privacy constraint
	expiresAt   time.Time // estimated wall-clock expiry of the current token

	// on401Ch accepts a signal from NotifyOn401 to trigger an immediate renewal.
	// Sized 1 so rapid 401s collapse into a single renewal attempt.
	// Only valid after renewalLoop() has started; it is created in NewSession
	// before the goroutine starts.
	on401Ch chan struct{}

	// stateChangedCh receives a notification each time the auth state changes.
	// Buffered; non-blocking send — a slow consumer misses intermediate transitions
	// but always reads the latest state.
	stateChangedCh chan struct{}
}

// SessionOption is a functional option for [NewSession].
type SessionOption func(*Session)

// WithHTTPClient replaces the default http.Client used for broker calls.
// Primarily for tests (fake broker server).
func WithHTTPClient(c *http.Client) SessionOption {
	return func(s *Session) { s.httpClient = c }
}

// WithClock injects a custom time source.  The renewalThreshold math uses this
// clock, making tests deterministic.
func WithClock(now func() time.Time) SessionOption {
	return func(s *Session) { s.now = now }
}

// WithTimerFactory injects a custom timer factory.  When set, the renewal
// goroutine uses newTimer(d) instead of time.NewTimer(d), enabling tests to
// control when renewals fire without real sleeps.
func WithTimerFactory(f func(d time.Duration) *time.Timer) SessionOption {
	return func(s *Session) { s.newTimer = f }
}

// WithLedgerEmit wires a callback that is called with the event name when an
// auth state transition produces a ledger-worthy event (currently only
// "session.signed_out").  Pass the emitter that targets the reporter ingest
// socket when available; pass nil to skip ledger emission.
func WithLedgerEmit(fn func(event string)) SessionOption {
	return func(s *Session) { s.ledgerEmit = fn }
}

// NewSession constructs a Session from Config and starts the renewal goroutine
// when the host is signed in.
//
// The goroutine is bound to ctx; cancel ctx to stop it cleanly.
// For anonymous mode (cfg.SignedIn == false or empty seed token), no goroutine
// is started and [Session.State] returns [StateAnonymous] immediately.
func NewSession(ctx context.Context, cfg Config, log *slog.Logger, opts ...SessionOption) *Session {
	if log == nil {
		log = slog.Default()
	}
	s := &Session{
		cfg:            cfg,
		log:            log,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		now:            time.Now,
		newTimer:       time.NewTimer,
		on401Ch:        make(chan struct{}, 1),
		stateChangedCh: make(chan struct{}, 16),
	}
	for _, o := range opts {
		o(s)
	}

	if !cfg.SignedIn || cfg.SeedAccessToken == "" {
		s.state = StateAnonymous
		s.log.Info("harness.authbroker: anonymous mode (host not signed in or no seed token)")
		return s
	}

	// Signed-in path: seed the token and start the renewal goroutine.
	// Token bytes are never logged — only whether one is present.
	s.state = StateSignedIn
	s.accessToken = cfg.SeedAccessToken
	// We don't know the exact expiry of the seed token; assume 1 hour from now
	// (Zitadel default for kameas-native).  The first actual broker call will
	// return an accurate expires_in and update this estimate.
	s.expiresAt = s.now().Add(3600 * time.Second)

	s.log.Info("harness.authbroker: signed-in mode, starting renewal goroutine",
		"broker_addr", cfg.BrokerAddr,
		"broker_token_set", cfg.BrokerToken != "",
	)

	go s.renewalLoop(ctx)
	return s
}

// State returns the current auth state.
func (s *Session) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// AccessToken returns the current in-memory access token.  Returns "" in
// anonymous or signed-out mode.  Callers MUST NOT log the returned value.
func (s *Session) AccessToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accessToken
}

// StateChangedCh returns a channel that receives an empty struct each time the
// auth state changes.  The channel has a small buffer; a slow consumer will
// miss intermediate transitions but always see the latest state via [State].
//
// Intended for use in tests and for pushing auth-state events to the served
// frontend over the EventBus.
func (s *Session) StateChangedCh() <-chan struct{} {
	return s.stateChangedCh
}

// NotifyOn401 signals the renewal goroutine that an upstream API call received
// a 401, which may mean the current access token has been revoked.  The
// goroutine will attempt an immediate broker renewal instead of waiting for
// the scheduled expiry timer.
//
// This is a fire-and-forget notification; if the goroutine is not running or
// the session is not signed in, the call is silently dropped.
func (s *Session) NotifyOn401() {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	if state != StateSignedIn {
		return
	}
	select {
	case s.on401Ch <- struct{}{}:
	default:
	}
}

// renewalLoop is the background goroutine that keeps the access token fresh.
// It exits when ctx is cancelled or when the broker signals session end (401).
func (s *Session) renewalLoop(ctx context.Context) {
	backoffBase := 2 * time.Second
	backoffMax := 5 * time.Minute
	backoff := backoffBase

	// Schedule the first renewal relative to the estimated expiry.
	nextRenewal := s.nextRenewalDuration()
	timer := s.newTimer(nextRenewal)
	defer timer.Stop()

	s.log.Debug("harness.authbroker: renewal loop started", "first_renewal_in", nextRenewal)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("harness.authbroker: renewal loop context cancelled")
			return
		case <-timer.C:
			// Scheduled renewal (expiry approaching).
		case <-s.on401Ch:
			// Upstream API call returned 401 — renew immediately.
		}

		s.log.Debug("harness.authbroker: attempting token renewal")

		newTok, expiresIn, err := s.callBroker(ctx)
		if err == nil {
			// Success: update token and schedule the next renewal.
			s.mu.Lock()
			s.accessToken = newTok
			s.expiresAt = s.now().Add(time.Duration(expiresIn) * time.Second)
			s.state = StateSignedIn
			s.mu.Unlock()

			backoff = backoffBase // reset backoff on success
			next := s.nextRenewalDuration()
			s.log.Info("harness.authbroker: token renewed", "expires_in_s", expiresIn, "next_renewal_in", next)
			timer.Reset(next)
			s.notifyStateChanged()
			continue
		}

		if isBrokerSignedOut(err) {
			// 401: host has signed out — tear down the session.
			s.mu.Lock()
			s.state = StateSignedOut
			s.accessToken = "" // clear from memory — privacy constraint
			s.mu.Unlock()

			s.log.Info("harness.authbroker: host signed out — session ended")
			s.notifyStateChanged()

			if s.ledgerEmit != nil {
				s.ledgerEmit("session.signed_out")
			}
			// No more renewals.
			return
		}

		// 403, network error, or other: broker unreachable — backoff and retry.
		// Keep the existing token until it expires; do not clear it.
		s.log.Warn("harness.authbroker: broker unreachable, will retry",
			"err", err, "backoff", backoff,
		)
		timer.Reset(backoff)
		backoff = minDuration(backoff*2, backoffMax)
	}
}

// callBroker issues POST /auth/token to the broker and returns the new access
// token and its expires_in (seconds).  Token bytes are never logged.
func (s *Session) callBroker(ctx context.Context) (accessToken string, expiresIn int, err error) {
	url := "http://" + s.cfg.BrokerAddr + "/auth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("authbroker: build request: %w", err)
	}
	// Broker token set in header — never logged.
	req.Header.Set("Authorization", "Bearer "+s.cfg.BrokerToken)
	req.ContentLength = 0

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("authbroker: http do: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", 0, &brokerError{StatusCode: resp.StatusCode}
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", 0, fmt.Errorf("authbroker: decode response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", 0, fmt.Errorf("authbroker: empty access_token in response")
	}
	// expiresIn=0 is suspicious; treat as 3600 to avoid a tight renewal loop.
	if tok.ExpiresIn <= 0 {
		tok.ExpiresIn = 3600
	}
	return tok.AccessToken, tok.ExpiresIn, nil
}

// nextRenewalDuration returns how long to wait before the next proactive
// renewal attempt.  Always >= 1 second to prevent a tight loop.
func (s *Session) nextRenewalDuration() time.Duration {
	s.mu.RLock()
	expiry := s.expiresAt
	s.mu.RUnlock()

	remaining := expiry.Sub(s.now())
	// Renew when remaining < renewalThreshold.
	wait := remaining - renewalThreshold
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}

// notifyStateChanged sends a non-blocking notification on stateChangedCh.
func (s *Session) notifyStateChanged() {
	select {
	case s.stateChangedCh <- struct{}{}:
	default:
	}
}

// minDuration returns the smaller of a and b.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
