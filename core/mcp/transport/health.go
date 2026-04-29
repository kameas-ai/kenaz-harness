package transport

import "time"

// Default health-ping cadences shared across all transports. Per
// FR-008 stdio defaults to 30 s; HTTP/SSE override at recipe level
// (typically 60 s) but use the same machinery.
const (
	// DefaultPingPeriod is the interval between health pings when a
	// recipe does not override it.
	DefaultPingPeriod = 30 * time.Second

	// DefaultPingTimeout is the per-ping response deadline. After two
	// consecutive failures the supervisor restarts the server.
	DefaultPingTimeout = 5 * time.Second
)

// Ticker abstracts time.Ticker so health-ping tests can be driven
// from a fake. The shape mirrors *time.Ticker exactly: a channel of
// times + a Stop method.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// realTicker is the default Ticker — a thin wrapper around
// *time.Ticker. The interface change is .C() instead of .C so the
// fake doesn't need to embed a non-zero channel field.
type realTicker struct{ t *time.Ticker }

// NewRealTicker returns a Ticker backed by time.NewTicker. Exposed
// so per-transport pool factories can wire it as the default
// without each duplicating the wrapper.
func NewRealTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }
