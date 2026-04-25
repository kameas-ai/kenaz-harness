// Package http_lan binds A2A endpoints to a non-loopback local
// interface. Opt-in only — never a default — per FR-007.
//
// v1 ships the dialer/listener stubs and the depguard / DIRECTIVE_001
// invariants intact; the full LAN exposure flow (mDNS discovery,
// per-interface allowlist) lands in a v1.x mission. Until then the
// constructor refuses with ErrTransportRefused so misconfiguration
// surfaces loudly at dispatch time.
//
// DIRECTIVE_001 contract: this transport is self-contained.
package http_lan

import (
	"context"

	"kaneaz-harness/core/acp"
	"kaneaz-harness/core/acp/envelope"
)

// Kind is the transport kind constant.
const Kind = acp.TransportLAN

// Dialer satisfies envelope.Dialer. v1 always refuses; the spec calls
// http_lan opt-in and the bundle resolver gates this transport behind
// explicit configuration.
type Dialer struct{}

// Kind reports the transport kind.
func (Dialer) Kind() acp.TransportKind { return Kind }

// Dial refuses with ErrTransportRefused in v1.
func (Dialer) Dial(_ context.Context, _ acp.PeerProfile) (envelope.Conn, error) {
	return nil, acp.ErrTransportRefused
}

// Listener satisfies envelope.Listener. v1 always refuses.
type Listener struct{}

// Kind reports the transport kind.
func (Listener) Kind() acp.TransportKind { return Kind }

// Listen refuses with ErrTransportRefused in v1.
func (Listener) Listen(_ context.Context, _ string) (envelope.TransportListener, error) {
	return nil, acp.ErrTransportRefused
}

// Register installs the http_lan dialer / listener with the
// envelope. v1 leaves the transport visible so that bundle config
// using `transport: http_lan` produces a clear runtime refusal rather
// than an envelope "no dialer registered" message.
func Register(e *envelope.Envelope) {
	if e == nil {
		return
	}
	e.RegisterDialer(Dialer{})
	e.RegisterListener(Listener{})
}
