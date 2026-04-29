package transport

import (
	"context"
)

// Connection is the bytes-layer abstraction the per-transport
// implementations (stdio, http, sse) plug into. The supervisor and
// the JSON-RPC machinery (Framer, Router) sit above this interface
// so they don't have to know whether the wire under them is a
// subprocess pipe pair, an HTTP POST, or an SSE stream.
//
// Lifetime contract:
//
//   - Open establishes the bytes layer and runs any transport-
//     specific handshake preamble. It returns once the connection
//     is ready to carry an MCP `initialize` request, OR an error
//     describing why it failed.
//   - Send delivers a single JSON-RPC envelope. Concurrent Send
//     calls MUST be safe — the transport serialises if the wire
//     can't tolerate interleaved frames (stdio's pipe write).
//   - Recv reads the next envelope. Exactly one goroutine should own
//     the read side — same contract as Framer.Read.
//   - Close shuts the connection down idempotently. After Close,
//     Send returns an error and Recv returns io.EOF or a transport-
//     specific shutdown sentinel.
//
// Capability hooks (PID, StderrTail) are best-effort: stdio fills
// them, HTTP/SSE return zero values. The supervisor uses them only
// for status surfaces — it does not branch on them for restart
// decisions.
//
// Implementations:
//   - stdio (`core/mcp/transport/stdio`): wraps an exec.Cmd's
//     stdin/stdout pipes with the standard Framer.
//   - http (`core/mcp/transport/http`, WP03): wraps a long-lived
//     *http.Client; Send POSTs JSON-RPC, Recv polls a response
//     queue fed by the POST callbacks.
//   - sse (`core/mcp/transport/sse`, WP04): keeps a long-lived
//     event stream open for inbound, POSTs a sibling URL for
//     outbound.
type Connection interface {
	// Open establishes the connection. Idempotent only when paired
	// with Close: once Open returns nil, subsequent Open calls on
	// the same instance return an error.
	Open(ctx context.Context) error

	// Send marshals v as a JSON-RPC envelope and writes it on the
	// wire. Safe for concurrent use across goroutines.
	Send(v any) error

	// Recv blocks for the next inbound envelope. Returns io.EOF
	// when the wire is gone or has been closed.
	Recv() (RawMessage, error)

	// Close shuts the connection down. Idempotent. Pending Recv
	// calls unblock with io.EOF (or the transport's equivalent
	// shutdown sentinel).
	Close() error

	// PID returns the OS process id when the connection wraps a
	// child process (stdio). HTTP and SSE return 0.
	PID() int

	// StderrTail returns up to maxBytes of recent stderr output
	// when the connection captures one (stdio). Other transports
	// return "".
	StderrTail(maxBytes int) string
}

// ConnectionFactory builds a Connection ready to be Open()ed. The
// factory carries any transport-specific configuration (command +
// env for stdio, URL + headers for HTTP, etc.) so the generic Pool
// in pool.go can spawn connections without knowing which transport
// it's driving.
//
// WP02 makes the stdio Pool consume this; WP03 and WP04 add HTTP
// and SSE factories.
type ConnectionFactory interface {
	// NewConnection returns a fresh, unopened Connection for the
	// recipe identified by id. The same factory can be invoked
	// repeatedly (each invocation returns an independent connection)
	// so the supervisor can restart cleanly without state leaking
	// between generations.
	NewConnection(id string) (Connection, error)
}
