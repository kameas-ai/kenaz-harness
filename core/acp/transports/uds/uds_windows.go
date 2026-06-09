//go:build windows

// Windows stub: UDS is not natively supported on the GOOS targets we
// ship to. The WP03 schema validator rewrites `transport: uds` to
// `transport: http_loopback` on Windows hosts at bundle load and emits
// a configuration warning. Should that substitution ever be bypassed,
// the constructors here refuse with ErrTransportRefused so the
// outbound dispatch path fails loudly rather than silently.
package uds

import (
	"context"
	"errors"
	"net"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/acp/envelope"
)

// Kind is the transport kind constant (mirrors the *nix build).
const Kind = acp.TransportUDS

// ErrUnsupportedPlatform is the Windows-specific refusal.
var ErrUnsupportedPlatform = errors.New("uds: unsupported on windows; use http_loopback")

// Listen always refuses on Windows.
func Listen(_ context.Context, _ string) (net.Listener, error) {
	return nil, ErrUnsupportedPlatform
}

// Dial always refuses on Windows.
func Dial(_ context.Context, _ string) (net.Conn, error) {
	return nil, ErrUnsupportedPlatform
}

// Dialer is the unsupported-on-Windows shape.
type Dialer struct{}

func (Dialer) Kind() acp.TransportKind                                  { return Kind }
func (Dialer) Dial(_ context.Context, _ acp.PeerProfile) (envelope.Conn, error) {
	return nil, acp.ErrTransportRefused
}

// Listener is the unsupported-on-Windows shape.
type Listener struct{}

func (Listener) Kind() acp.TransportKind { return Kind }
func (Listener) Listen(_ context.Context, _ string) (envelope.TransportListener, error) {
	return nil, acp.ErrTransportRefused
}

// Register installs the refusal-only Dialer / Listener so that any
// attempt to use UDS on Windows surfaces ErrTransportRefused at the
// dispatch site. This keeps the registration call site identical
// across platforms.
func Register(e *envelope.Envelope) {
	if e == nil {
		return
	}
	e.RegisterDialer(Dialer{})
	e.RegisterListener(Listener{})
}
