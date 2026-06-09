// Package envelope is the SOLE Go package permitted to import
// github.com/a2aproject/a2a-go (FR-015, NFR-010, C-001).
//
// Every other core/acp package depends only on the SDK-agnostic types
// defined in core/acp. This boundary is enforced by:
//
//  1. The .golangci.yml depguard rule that denies imports of a2a-go
//     from any package outside core/acp/envelope/...
//  2. A unit test in this package that asserts the public envelope API
//     surface uses zero a2a-go types in its signatures.
//
// # SDK pin
//
// The a2a-go SDK is pinned to a specific minor version in go.mod. See
// UPGRADE.md for the upgrade procedure. A non-breaking SDK upgrade
// touches only this package and its tests; a breaking upgrade requires
// an ADR per plan §8 R1.
//
// # v1 status
//
// The Envelope public surface (Dispatch, FetchCard, CancelTask,
// AcceptTask, Respond) is fully defined. The implementation that
// translates between core/acp.* types and SDK shapes is staged behind
// the SDK pin: v1 ships an in-memory back-end suitable for the
// loopback test fixtures the rest of the mission builds against. When
// the SDK pin is selected by the maintainer (research note PICK
// `github.com/a2aproject/a2a-go`), the converter helpers below are
// rebound to call into the SDK; no public API changes are required.
package envelope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/acp"
)

// Dialer is the transport-neutral seam used by the envelope to acquire
// a connection to a peer. Implemented by each
// core/acp/transports/<kind> sub-package. FR-016.
type Dialer interface {
	Dial(ctx context.Context, peer acp.PeerProfile) (Conn, error)
	Kind() acp.TransportKind
}

// Listener is the transport-neutral seam used by the envelope to
// accept inbound connections. Implemented by each transport.
type Listener interface {
	Listen(ctx context.Context, addr string) (TransportListener, error)
	Kind() acp.TransportKind
}

// Conn is the minimum surface a transport must expose to the
// envelope. Concrete implementations live in transport sub-packages.
type Conn interface {
	// Send delivers a wire payload and returns the immediate response.
	// Streaming responses arrive on the channel returned from Dispatch
	// and not from Send.
	Send(ctx context.Context, kind acp.MessageKind, body json.RawMessage) (json.RawMessage, error)
	// Close releases the connection.
	Close() error
}

// TransportListener mirrors a net.Listener, kept abstract here so
// transports can implement non-net listeners (in-memory pipes for
// tests).
type TransportListener interface {
	Accept(ctx context.Context) (Conn, error)
	Close() error
	Addr() string
}

// AcceptedTask is the payload an inbound transport hands to the
// envelope on accepting a connection. Servers translate this into a
// core/acp.Task once policy and the skill router approve the call.
type AcceptedTask struct {
	A2ATaskID     string
	ClaimedPeerID string
	ClaimedCard   acp.AgentCard
	SkillID       string
	Body          json.RawMessage
}

// SkillHandler is the callback invoked by AcceptTask once the inbound
// gate machinery has approved a call.
type SkillHandler func(ctx context.Context, in AcceptedTask) (out json.RawMessage, err error)

// Envelope is the harness-shaped surface that the client and server
// packages depend on. It is the only point in the harness that
// (eventually) calls into the a2a-go SDK.
type Envelope struct {
	mu       sync.RWMutex
	dialers  map[acp.TransportKind]Dialer
	listeners map[acp.TransportKind]Listener
	cards    map[string]acp.AgentCard // keyed by peer_id; for inline + manual sources

	// inMemoryPeers is a registry of in-process server-side handlers
	// that the v1 envelope routes to when a transport's Conn is the
	// InProcConn fixture used by the test harness. Real transports
	// bypass this map entirely.
	mu2           sync.Mutex
	inMemoryPeers map[string]inMemoryPeer
}

type inMemoryPeer struct {
	card    acp.AgentCard
	handler SkillHandler
}

// New constructs an Envelope with the given dialers/listeners.
func New() *Envelope {
	return &Envelope{
		dialers:       map[acp.TransportKind]Dialer{},
		listeners:     map[acp.TransportKind]Listener{},
		cards:         map[string]acp.AgentCard{},
		inMemoryPeers: map[string]inMemoryPeer{},
	}
}

// RegisterDialer wires a transport's Dialer into the envelope. FR-016.
// Transport packages call this from their package init() so adding a
// transport is a one-line change.
func (e *Envelope) RegisterDialer(d Dialer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dialers[d.Kind()] = d
}

// RegisterListener wires a transport's Listener into the envelope.
func (e *Envelope) RegisterListener(l Listener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners[l.Kind()] = l
}

// DialerFor returns the registered Dialer for kind or an error.
func (e *Envelope) DialerFor(kind acp.TransportKind) (Dialer, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	d, ok := e.dialers[kind]
	if !ok {
		return nil, fmt.Errorf("envelope: no dialer registered for transport %q", kind)
	}
	return d, nil
}

// ListenerFor returns the registered Listener for kind or an error.
func (e *Envelope) ListenerFor(kind acp.TransportKind) (Listener, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	l, ok := e.listeners[kind]
	if !ok {
		return nil, fmt.Errorf("envelope: no listener registered for transport %q", kind)
	}
	return l, nil
}

// FetchCard returns the AgentCard for peer per its CardSource. v1
// supports CardSourceInline directly; well_known and manual_url paths
// route through the registered Dialer for the peer's transport.
//
// Translation to/from SDK card types happens inside this method; no
// SDK type leaks to the caller.
func (e *Envelope) FetchCard(ctx context.Context, peer acp.PeerProfile) (acp.AgentCard, error) {
	switch peer.CardSource {
	case acp.CardSourceInline:
		if peer.InlineCard == nil {
			return acp.AgentCard{}, errors.New("envelope: inline card source declared but no card present")
		}
		return *peer.InlineCard, nil
	case acp.CardSourceWellKnown, acp.CardSourceManualURL:
		// In-memory peers (the test fixture path) are served from the
		// in-process registry. Real transports speak SDK FetchCard.
		e.mu2.Lock()
		mp, ok := e.inMemoryPeers[peer.PeerID]
		e.mu2.Unlock()
		if ok {
			return mp.card, nil
		}
		// Fallback path placeholder until SDK pin is selected. Real
		// implementation: dial via DialerFor(peer.Transport), invoke
		// SDK FetchCard, convert SDK card to acp.AgentCard.
		_, err := e.DialerFor(peer.Transport)
		if err != nil {
			return acp.AgentCard{}, err
		}
		return acp.AgentCard{}, fmt.Errorf("envelope: FetchCard over %s not implemented (awaits a2a-go SDK pin)", peer.Transport)
	default:
		return acp.AgentCard{}, fmt.Errorf("envelope: unknown card source %q", peer.CardSource)
	}
}

// Dispatch builds and sends a Task request to peer. The returned
// channel receives Messages converted from SDK stream events; it is
// closed when the Task reaches a terminal state.
//
// The taskID returned is the wire-level a2a_task_id; the harness's own
// task_id is minted by the client package.
func (e *Envelope) Dispatch(ctx context.Context, peer acp.PeerProfile, skillID string, body json.RawMessage) (a2aTaskID string, stream <-chan acp.Message, err error) {
	// In-memory fast path: when the peer has been registered as an
	// in-process server (test fixture), invoke the handler directly.
	e.mu2.Lock()
	mp, ok := e.inMemoryPeers[peer.PeerID]
	e.mu2.Unlock()
	if ok {
		return e.dispatchInMemory(ctx, mp, peer, skillID, body)
	}

	// Real path: dial via the registered Dialer, hand off to the SDK.
	// Until the SDK pin is selected, this returns an unimplemented
	// error; transports still register and the public surface
	// compiles against acp.* types only.
	d, err := e.DialerFor(peer.Transport)
	if err != nil {
		return "", nil, err
	}
	conn, err := d.Dial(ctx, peer)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = conn.Close() }()
	// SDK call site: the converter functions below are the seam for
	// the SDK pin. Until the pin is selected we surface a typed
	// error so the caller's error handling is fully exercised.
	return "", nil, fmt.Errorf("envelope: SDK dispatch over %s not implemented (awaits a2a-go SDK pin)", peer.Transport)
}

// dispatchInMemory delivers the request to an in-process handler and
// pumps the response onto a Message channel. This is the v1 happy path
// for the loopback test fixtures the rest of the mission builds
// against.
func (e *Envelope) dispatchInMemory(ctx context.Context, mp inMemoryPeer, peer acp.PeerProfile, skillID string, body json.RawMessage) (string, <-chan acp.Message, error) {
	a2aTaskID := newA2ATaskID()
	out := make(chan acp.Message, 4)

	go func() {
		defer close(out)
		// Echo the request as the first inbound (from the peer's
		// perspective) message. The caller side records this as a
		// message_sent.
		now := time.Now().UTC()
		out <- acp.Message{
			MessageID: newMessageID(),
			TaskID:    a2aTaskID,
			Direction: acp.DirOutbound,
			Kind:      acp.KindRequest,
			Payload:   body,
			Sequence:  1,
			EmittedAt: now,
		}

		resp, err := mp.handler(ctx, AcceptedTask{
			A2ATaskID:     a2aTaskID,
			ClaimedPeerID: peer.PeerID,
			ClaimedCard:   mp.card,
			SkillID:       skillID,
			Body:          body,
		})
		if err != nil {
			payload, _ := json.Marshal(map[string]string{"error": err.Error()})
			out <- acp.Message{
				MessageID: newMessageID(),
				TaskID:    a2aTaskID,
				Direction: acp.DirInbound,
				Kind:      acp.KindError,
				Payload:   payload,
				Sequence:  2,
				EmittedAt: time.Now().UTC(),
			}
			return
		}
		out <- acp.Message{
			MessageID: newMessageID(),
			TaskID:    a2aTaskID,
			Direction: acp.DirInbound,
			Kind:      acp.KindResponse,
			Payload:   resp,
			Sequence:  2,
			EmittedAt: time.Now().UTC(),
		}
	}()

	return a2aTaskID, out, nil
}

// CancelTask sends a cancel signal for the given wire-level task id.
// Honors ctx cancellation. FR-010, NFR-002.
func (e *Envelope) CancelTask(_ context.Context, _ string) error {
	// In-memory fixtures rely on context cancellation propagated
	// through the parent context — no explicit cancel needed. Real
	// SDK path posts a JSON-RPC tasks/cancel request.
	return nil
}

// RegisterInProcessPeer is the v1 fixture entry point: it lets the
// test harness wire an in-process SkillHandler under a peer_id so the
// client package can exercise the full Dispatch path without an SDK
// build dependency. Real production paths use Dial/Listen via the
// registered transports.
func (e *Envelope) RegisterInProcessPeer(peerID string, card acp.AgentCard, h SkillHandler) {
	e.mu2.Lock()
	defer e.mu2.Unlock()
	e.inMemoryPeers[peerID] = inMemoryPeer{card: card, handler: h}
}

// AcceptTask is the inbound entry point. The server package wires its
// SkillRouter into this callback so each accepted connection routes
// through (PolicyGuard → Verifier → SkillRouter → SkillDispatcher).
//
// In v1 the AcceptedTask flows through the in-process peer registry
// for fixture tests. Real wire reception lands when the SDK pin is
// selected.
func (e *Envelope) AcceptTask(ctx context.Context, accepted AcceptedTask, h SkillHandler) (json.RawMessage, error) {
	if h == nil {
		return nil, errors.New("envelope: AcceptTask handler must not be nil")
	}
	return h(ctx, accepted)
}

// Respond writes a response Message onto an outbound stream. v1
// implementation is a no-op for the in-process path; real SDK path
// forwards to the SDK's response writer.
func (e *Envelope) Respond(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}
