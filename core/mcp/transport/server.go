package transport

import "time"

// Default deadlines applied by per-transport servers when a recipe
// or PoolOptions does not override them.
const (
	// DefaultFirstByteTimeout caps the wait for the first byte on
	// the inbound side after Open, covering an `npx -y …` cold-
	// fetch. Distinct from initialize's response deadline — research
	// §"npx -y cold-spawn behavior".
	DefaultFirstByteTimeout = 30 * time.Second

	// DefaultInitTimeout is the response deadline once the
	// initialize request is on the wire.
	DefaultInitTimeout = 5 * time.Second

	// CloseGrace is how long Close waits between graceful-shutdown
	// nudge (closing stdin / cancelling the HTTP context) and the
	// hard kill / force-disconnect. Two seconds matches
	// `closeGrace` in the original stdio implementation.
	CloseGrace = 2 * time.Second
)

// SpawnSpec is the cross-transport per-server spawn descriptor.
// Stdio populates Command/Env; HTTP/SSE leave them empty and
// instead route via their own factory configuration. Timeout
// fields are universal and respected by every transport.
//
// WP02 currently only touches the stdio fields; WP03/WP04 may
// extend this struct or use an interface in front of it. For now
// the universal subset lives here as the contract every transport
// agrees on.
type SpawnSpec struct {
	// ID identifies the server in logs and on Tool.Server. The pool
	// uses ServerSpec.Name, which the recipe maps to its ID.
	ID string

	// Command is the argv vector of the child process (stdio only).
	// Command[0] is resolved against PATH.
	Command []string

	// Env is the additional environment merged on top of the
	// process's existing environment (stdio only). Empty values
	// clear a key.
	Env map[string]string

	// FirstByteTimeout overrides DefaultFirstByteTimeout when > 0.
	FirstByteTimeout time.Duration

	// InitTimeout overrides DefaultInitTimeout when > 0.
	InitTimeout time.Duration

	// SamplingEnabled tells the pool whether to advertise the
	// sampling capability during initialize. WP03 wires the actual
	// handler dispatch.
	SamplingEnabled bool

	// PingPeriod overrides DefaultPingPeriod when > 0; negative
	// values disable health pings entirely.
	PingPeriod time.Duration

	// PingTimeout overrides DefaultPingTimeout when > 0.
	PingTimeout time.Duration
}
