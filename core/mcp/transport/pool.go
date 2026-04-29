package transport

import (
	"log/slog"
	"time"
)

// PoolOptions are the cross-transport knobs every per-transport
// pool accepts. Concrete pools (stdio, http, sse) embed this and
// add their own fields; this struct is the shared subset.
//
// WP02 wires *stdio.Pool to consume this directly; for now it
// documents the shape so downstream pool implementations can
// converge.
type PoolOptions struct {
	// Sampler is the SamplingHandler dispatched on
	// sampling/createMessage. Nil → -32601 (per the FR-015 cost-
	// amplification gate, the default is OFF anyway).
	Sampler SamplingHandler

	// Roots is the RootsHandler dispatched on roots/list. Nil →
	// reply with an empty roots list.
	Roots RootsHandler

	// Broker is the EventPublisher progress notifications fan out
	// onto. Nil → progress events drop silently.
	Broker EventPublisher

	// Logger is the slog.Logger used for transport-internal
	// diagnostics. Nil → slog.Default.
	Logger *slog.Logger

	// FirstByteTimeout caps the wait for the first byte after a
	// connection is established. Stdio uses it for `npx -y` cold
	// fetches; HTTP/SSE use it for the first response's headers.
	FirstByteTimeout time.Duration

	// InitTimeout is the response deadline once `initialize` is on
	// the wire.
	InitTimeout time.Duration

	// PingPeriod is the cadence between health pings. Negative
	// values disable health pings entirely (used by tests that
	// drive the ping path manually). Zero → DefaultPingPeriod.
	PingPeriod time.Duration

	// PingTimeout is the per-ping response deadline. Two
	// consecutive failures trip a restart.
	PingTimeout time.Duration

	// Now is the clock-injection hook used by the supervisor for
	// restartHistory pruning and LastRestartAt timestamps. Nil →
	// time.Now.
	Now func() time.Time

	// Sleep is the supervisor's backoff sleep. Nil → time.Sleep.
	Sleep func(d time.Duration)

	// NewTicker is healthPinger's ticker factory. Nil →
	// NewRealTicker.
	NewTicker func(d time.Duration) Ticker
}

// ApplyDefaults fills in zero-value option fields with their
// transport-level defaults. Per-transport pools call this before
// constructing instances so every transport observes the same
// fall-back behavior.
func (o *PoolOptions) ApplyDefaults() {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.NewTicker == nil {
		o.NewTicker = NewRealTicker
	}
}
