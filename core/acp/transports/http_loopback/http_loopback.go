// Package http_loopback is the HTTP loopback transport for ACP. It
// binds listeners to 127.0.0.1 / ::1 only, on an ephemeral port, and
// refuses any dial against a non-loopback host. Default transport on
// Windows hosts (FR-007); opt-in elsewhere.
//
// DIRECTIVE_001 contract: this transport is self-contained. The only
// integration point with the rest of core/acp is the
// envelope.RegisterDialer / RegisterListener call from Register.
package http_loopback

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/acp/envelope"
)

// Kind is the transport kind constant.
const Kind = acp.TransportLoopback

// Listen binds a TCP listener at addrSpec, refusing any explicit
// non-loopback bind address. addrSpec accepts:
//
//	""                 → 127.0.0.1:0 (ephemeral)
//	"127.0.0.1:0"      → 127.0.0.1:0
//	"[::1]:0"          → IPv6 loopback ephemeral
//	":0"               → 127.0.0.1:0 (we refuse the wildcard form)
//	non-loopback addr  → ErrTransportRefused
func Listen(_ context.Context, addrSpec string) (net.Listener, error) {
	host, port, err := parseBindAddr(addrSpec)
	if err != nil {
		return nil, err
	}
	if !IsLoopbackHost(host) {
		return nil, fmt.Errorf("%w: refusing non-loopback bind %q", acp.ErrTransportRefused, addrSpec)
	}
	addr := net.JoinHostPort(host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", acp.ErrTransportRefused, err)
	}
	return ln, nil
}

// Dial connects to endpointURL over TCP, refusing any URL whose host
// is not a loopback literal.
func Dial(ctx context.Context, endpointURL string) (net.Conn, error) {
	host, port, err := parseDialURL(endpointURL)
	if err != nil {
		return nil, err
	}
	if !IsLoopbackHost(host) {
		return nil, fmt.Errorf("%w: refusing non-loopback dial %q", acp.ErrTransportRefused, endpointURL)
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", acp.ErrTransportRefused, err)
	}
	return conn, nil
}

// Dialer satisfies envelope.Dialer.
type Dialer struct{}

// Kind reports the transport kind.
func (Dialer) Kind() acp.TransportKind { return Kind }

// Dial opens a loopback TCP connection to peer.EndpointURL.
func (Dialer) Dial(ctx context.Context, peer acp.PeerProfile) (envelope.Conn, error) {
	conn, err := Dial(ctx, peer.EndpointURL)
	if err != nil {
		return nil, err
	}
	return &lbConn{conn: conn}, nil
}

// Listener satisfies envelope.Listener.
type Listener struct{}

// Kind reports the transport kind.
func (Listener) Kind() acp.TransportKind { return Kind }

// Listen binds a loopback TCP listener at addr.
func (Listener) Listen(ctx context.Context, addr string) (envelope.TransportListener, error) {
	ln, err := Listen(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &lbListener{ln: ln}, nil
}

// lbConn wraps a TCP loopback connection.
type lbConn struct {
	mu   sync.Mutex
	conn net.Conn
}

func (c *lbConn) Send(ctx context.Context, _ acp.MessageKind, body json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
	}
	if _, err := c.conn.Write(body); err != nil {
		return nil, err
	}
	buf := make([]byte, 64*1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(buf[:n]), nil
}

func (c *lbConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// lbListener wraps a TCP listener.
type lbListener struct{ ln net.Listener }

func (l *lbListener) Accept(_ context.Context) (envelope.Conn, error) {
	c, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	return &lbConn{conn: c}, nil
}

func (l *lbListener) Close() error { return l.ln.Close() }
func (l *lbListener) Addr() string { return l.ln.Addr().String() }

// parseBindAddr splits addrSpec into (host, port). Empty addrSpec or
// the wildcard ":port" form is normalised to 127.0.0.1.
func parseBindAddr(addrSpec string) (string, string, error) {
	if addrSpec == "" {
		return "127.0.0.1", "0", nil
	}
	host, port, err := net.SplitHostPort(addrSpec)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", acp.ErrTransportRefused, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		// Wildcard / unspecified bind would expose the listener
		// beyond loopback. Refuse explicitly.
		return "", "", fmt.Errorf("%w: wildcard bind %q is not loopback", acp.ErrTransportRefused, addrSpec)
	}
	return host, port, nil
}

// parseDialURL extracts host + port from endpointURL, accepting
// "scheme://host:port[/path]" or bare "host:port".
func parseDialURL(endpointURL string) (string, string, error) {
	if strings.Contains(endpointURL, "://") {
		u, err := url.Parse(endpointURL)
		if err != nil {
			return "", "", fmt.Errorf("%w: %v", acp.ErrTransportRefused, err)
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "80"
		}
		return host, port, nil
	}
	return net.SplitHostPort(endpointURL)
}

// Register installs the loopback dialer and listener with the
// envelope. FR-016.
func Register(e *envelope.Envelope) {
	if e == nil {
		return
	}
	e.RegisterDialer(Dialer{})
	e.RegisterListener(Listener{})
}
