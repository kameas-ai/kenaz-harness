package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// Transport is a Go-native, in-process MCP transport bound to a single
// Server. Production callers wire one Transport per Server instance into
// the supervisor, where it satisfies the same role stdio.Connection plays
// for subprocess-backed servers.
//
// Status: WP02 ships a synchronous Send/Recv pair sufficient to drive
// initialize / tools/list / tools/call round-trips. Notifications and
// server-initiated requests (sampling/createMessage) are TODO(v0.3.x);
// the harness-self tool surface today does not need either.
type Transport struct {
	server *Server
	// queue holds responses produced by a Send call until the next Recv.
	// The transport is request/response only — no async server-pushed
	// frames in the WP02 cut.
	queue chan transport.ResponseEnvelope
	// closed flips on Close so Send / Recv return ErrTransportClosed.
	closed chan struct{}
}

// NewTransport binds a transport to a Server. The returned Transport is
// safe for use from a single goroutine pair (one writer / one reader),
// matching the existing Connection contract in core/mcp/transport.
func NewTransport(s *Server) *Transport {
	if s == nil {
		s = NewServer()
	}
	return &Transport{
		server: s,
		queue:  make(chan transport.ResponseEnvelope, 16),
		closed: make(chan struct{}),
	}
}

// ErrTransportClosed is returned by Send / Recv after Close.
var ErrTransportClosed = errors.New("harness: in-process transport closed")

// Send dispatches a request envelope synchronously: it invokes the
// server handler and queues the response for the next Recv. It never
// blocks on the queue under normal use because each Send pairs with a
// subsequent Recv; an over-eager caller will block on a full queue
// (intentional backpressure).
func (t *Transport) Send(ctx context.Context, env transport.RequestEnvelope) error {
	select {
	case <-t.closed:
		return ErrTransportClosed
	default:
	}

	// Normalize Params to json.RawMessage so HandleEnvelope can decode
	// it uniformly. Callers are expected to pre-marshal their params,
	// but support the convenience of passing a structured value too.
	if env.Params != nil {
		if _, ok := env.Params.(json.RawMessage); !ok {
			b, err := json.Marshal(env.Params)
			if err != nil {
				return fmt.Errorf("harness: marshal params: %w", err)
			}
			env.Params = json.RawMessage(b)
		}
	} else {
		env.Params = json.RawMessage(nil)
	}

	result, rpcErr := t.server.HandleEnvelope(ctx, env)
	idBytes, err := json.Marshal(env.ID)
	if err != nil {
		return fmt.Errorf("harness: marshal id: %w", err)
	}
	resp := transport.ResponseEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      idBytes,
	}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	select {
	case t.queue <- resp:
		return nil
	case <-t.closed:
		return ErrTransportClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Recv blocks until the next response envelope is available or the
// transport is closed.
func (t *Transport) Recv(ctx context.Context) (transport.ResponseEnvelope, error) {
	select {
	case env := <-t.queue:
		return env, nil
	case <-t.closed:
		return transport.ResponseEnvelope{}, ErrTransportClosed
	case <-ctx.Done():
		return transport.ResponseEnvelope{}, ctx.Err()
	}
}

// Close releases the transport. Subsequent Send / Recv return
// ErrTransportClosed.
func (t *Transport) Close() error {
	select {
	case <-t.closed:
		return nil
	default:
		close(t.closed)
		return nil
	}
}
