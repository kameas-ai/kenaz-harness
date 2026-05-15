package sentry

import (
	"sync"
	"time"
)

// Client is the interface all callers use. The live implementation backs onto
// github.com/getsentry/sentry-go; nopClient is the default when tier=Off or
// HARNESS_SENTRY_DISABLED is set.
type Client interface {
	// Init initialises the underlying SDK. Called once at startup.
	// Returns nil when tier=Off (nop path).
	Init(tier Tier, dsn, release, gitsha string) error

	// CaptureException sends an error event. The event is redacted before
	// transmission. Panics in the redactor are caught and swallowed.
	CaptureException(err error, extra map[string]any)

	// CaptureMessage sends a message event.
	CaptureMessage(msg string, level string, extra map[string]any)

	// AddBreadcrumb appends a breadcrumb to the global ring buffer AND to
	// the SDK's own breadcrumb list (when a live client is active).
	AddBreadcrumb(b Breadcrumb)

	// Flush blocks until all buffered events are transmitted or timeout
	// elapses. Should be called before process exit.
	Flush(timeout time.Duration)
}

// processClient is the process-wide singleton.
var (
	processClientMu sync.RWMutex
	processClient   Client = nopClient{}
)

// SetClient installs a Client as the process-wide singleton. Only called
// from Init; not exported so external packages cannot bypass Init.
func setClient(c Client) {
	processClientMu.Lock()
	defer processClientMu.Unlock()
	processClient = c
}

// C returns the process-wide client. Callers in other packages call the
// package-level helpers (CaptureException, etc.) which route through this.
func C() Client {
	processClientMu.RLock()
	defer processClientMu.RUnlock()
	return processClient
}

// Init initialises the Sentry integration. When HARNESS_SENTRY_DISABLED is
// truthy or tier==TierOff, a nopClient is installed and Init returns nil.
//
// wire-up point 1 (main.go) calls this after reading Settings.
func Init(tier Tier, dsn, release, gitsha string) error {
	if Disabled() || tier == TierOff {
		setClient(nopClient{})
		return nil
	}
	c := &liveClient{}
	if err := c.Init(tier, dsn, release, gitsha); err != nil {
		// Fall back to nop on init failure — never block startup.
		setClient(nopClient{})
		return err
	}
	setClient(c)
	return nil
}

// CaptureException is a convenience shim on the process-wide client.
func CaptureException(err error, extra map[string]any) {
	C().CaptureException(err, extra)
}

// CaptureMessage is a convenience shim on the process-wide client.
func CaptureMessage(msg string, level string, extra map[string]any) {
	C().CaptureMessage(msg, level, extra)
}

// Flush is a convenience shim on the process-wide client.
func Flush(timeout time.Duration) {
	C().Flush(timeout)
}

// ── nopClient ────────────────────────────────────────────────────────────────

type nopClient struct{}

func (nopClient) Init(_ Tier, _, _, _ string) error     { return nil }
func (nopClient) CaptureException(_ error, _ map[string]any) {}
func (nopClient) CaptureMessage(_ string, _ string, _ map[string]any) {}
func (nopClient) AddBreadcrumb(b Breadcrumb)             { globalBreadcrumbs.Add(b) }
func (nopClient) Flush(_ time.Duration)                  {}

// ── liveClient ───────────────────────────────────────────────────────────────

// liveClient wraps github.com/getsentry/sentry-go. It lives in client_live.go
// which is compiled unconditionally (no build tag) so the dep is always
// present in go.sum. The nopClient above is the default; liveClient is only
// activated when Init is called with a non-Off tier and no kill-switch.
