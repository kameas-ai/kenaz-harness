//go:build darwin || linux || freebsd

// Package uds is the Unix domain socket transport for ACP. It is the
// default local transport on macOS/Linux per FR-007. Filesystem
// permissions provide the access-control surface.
//
// On Windows the package compiles to a stub that refuses to construct
// (see uds_windows.go); the WP03 schema validator detects this and
// substitutes http_loopback at bundle load.
//
// DIRECTIVE_001 contract: this transport is self-contained. Adding or
// modifying it does not touch any other package beyond a single
// Envelope.RegisterDialer / RegisterListener call.
package uds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"kaneaz-harness/core/acp"
	"kaneaz-harness/core/acp/envelope"
)

// Kind is the transport kind constant.
const Kind = acp.TransportUDS

// Listen creates a Unix domain socket listener at socketPath with
// owner-only permissions (0600). Stale socket files at the path are
// removed before bind.
func Listen(_ context.Context, socketPath string) (net.Listener, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("uds: empty socket path")
	}
	dir := filepath.Dir(socketPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("uds: mkdir socket dir: %w", err)
		}
	}
	// Remove stale socket file if present (left over from a prior
	// crash). We only remove regular sockets, never arbitrary files.
	if info, err := os.Stat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("uds: refusing to overwrite non-socket file %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("uds: remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", acp.ErrTransportRefused, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("uds: chmod socket: %w", err)
	}
	return ln, nil
}

// Dial connects to a Unix domain socket at socketPath, honouring ctx
// cancellation.
func Dial(ctx context.Context, socketPath string) (net.Conn, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("%w: empty socket path", acp.ErrTransportRefused)
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", acp.ErrTransportRefused, err)
	}
	return conn, nil
}

// Dialer satisfies envelope.Dialer for the UDS transport.
type Dialer struct{}

// Kind reports the transport kind.
func (Dialer) Kind() acp.TransportKind { return Kind }

// Dial opens a UDS connection to peer.EndpointURL (interpreted as a
// filesystem path; "unix://" prefix optional).
func (Dialer) Dial(ctx context.Context, peer acp.PeerProfile) (envelope.Conn, error) {
	path := socketPathFromEndpoint(peer.EndpointURL)
	conn, err := Dial(ctx, path)
	if err != nil {
		return nil, err
	}
	return &udsConn{conn: conn}, nil
}

// Listener satisfies envelope.Listener for UDS.
type Listener struct{}

// Kind reports the transport kind.
func (Listener) Kind() acp.TransportKind { return Kind }

// Listen binds a UDS listener at addr.
func (Listener) Listen(ctx context.Context, addr string) (envelope.TransportListener, error) {
	ln, err := Listen(ctx, socketPathFromEndpoint(addr))
	if err != nil {
		return nil, err
	}
	return &udsListener{ln: ln, path: socketPathFromEndpoint(addr)}, nil
}

// udsConn is a minimal envelope.Conn over net.Conn. The v1 fixture
// path uses Send for synchronous request/response; streaming arrives
// through the SDK once the pin lands.
type udsConn struct {
	mu   sync.Mutex
	conn net.Conn
}

// Send writes a length-prefixed JSON frame and reads the response.
// The framing is the harness's internal placeholder until the SDK
// pin replaces it with JSON-RPC 2.0 over HTTP-on-UDS.
func (c *udsConn) Send(ctx context.Context, _ acp.MessageKind, body json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
	}
	frame := append([]byte{}, body...)
	if _, err := c.conn.Write(frame); err != nil {
		return nil, err
	}
	buf := make([]byte, 64*1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(buf[:n]), nil
}

// Close closes the underlying connection.
func (c *udsConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// udsListener wraps net.Listener as envelope.TransportListener.
type udsListener struct {
	ln   net.Listener
	path string
}

func (l *udsListener) Accept(_ context.Context) (envelope.Conn, error) {
	c, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	return &udsConn{conn: c}, nil
}

func (l *udsListener) Close() error {
	if l.ln == nil {
		return nil
	}
	err := l.ln.Close()
	if l.path != "" {
		_ = os.Remove(l.path)
	}
	return err
}

func (l *udsListener) Addr() string {
	if l.ln == nil {
		return ""
	}
	return l.ln.Addr().String()
}

// socketPathFromEndpoint strips an optional "unix://" prefix.
func socketPathFromEndpoint(s string) string {
	const prefix = "unix://"
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// Register installs the UDS dialer and listener with the envelope.
// FR-016 — adding a transport touches only this single registration
// site outside this package.
func Register(e *envelope.Envelope) {
	if e == nil {
		return
	}
	e.RegisterDialer(Dialer{})
	e.RegisterListener(Listener{})
}

// ErrUnsupportedPlatform is returned by Register on platforms where
// UDS is not available. Defined here so the cross-platform stub
// agrees on the sentinel.
var ErrUnsupportedPlatform = errors.New("uds: unsupported platform")
