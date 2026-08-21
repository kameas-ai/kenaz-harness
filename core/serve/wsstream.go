package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
)

// wsstream.go owns the served-mode WebSocket fan-out: it bridges the
// in-process rpc.EventBus to one browser client, with an explicit,
// documented backpressure policy.
//
// # Why this is not a plain `for ev := range busCh { write(ev) }`
//
// rpc.EventBus.Publish is deliberately non-blocking: a subscriber whose
// buffer is full has the event DISCARDED so one slow reader cannot stall
// the whole harness. That trade-off is fine for coarse "something
// changed, go re-read it" topics — the next event repairs the gap. It is
// NOT fine for `llm:stream-chunk`, where every event carries a slice of
// the model's answer that exists nowhere else on the wire. A handler that
// wrote frames straight from the bus channel would block on the socket
// whenever the browser was slow, let the 64-slot bus buffer overflow, and
// render a truncated answer that LOOKS complete. Silent truncation of
// model output is the worst possible failure here: the user cannot tell
// the difference between "the model said this" and "the model said more
// and we lost it".
//
// # Backpressure policy
//
//  1. A dedicated pump goroutine is the only reader of the bus channel.
//     It never blocks on the socket — it only transforms an event into a
//     frame and hands it to a per-connection queue — so the bus buffer
//     stays drained and bus-level drops stay near-zero even while the
//     client is stalled.
//
//  2. The per-connection queue is BOUNDED (streamQueueCap frames).
//     Unbounded buffering would trade a visible truncation for an
//     invisible OOM, which is worse.
//
//  3. When the queue is full the newest frame is dropped and counted.
//     Newest-drop (not oldest-drop) keeps the prefix the user has already
//     read contiguous, so the truncation marker lands at the right point
//     in the transcript instead of leaving a hole in the middle.
//
//  4. Drops are NEVER silent. Both queue-level drops and bus-level drops
//     (rpc.Subscription.Dropped) are summed, and the writer emits a
//     [TopicStreamTruncated] frame carrying the running total before the
//     next frame it writes. The frontend renders a persistent, visible
//     "this live stream is incomplete" notice. The full assistant turn is
//     still persisted server-side by the chat runner, so the notice can
//     honestly tell the user to reload the conversation to see all of it.
//
//  5. Every socket write carries a deadline. A client that stops reading
//     forever gets disconnected instead of pinning a server goroutine and
//     a queue for the lifetime of the process.
//
//  6. The bus subscription is taken BEFORE the initial snapshot is built,
//     because an unsubscribed connection loses events silently — the bus
//     discards them, so they are not even counted as drops and rule 4
//     never fires. Subscribing first makes "client received the snapshot"
//     imply "client is subscribed". See [Server.streamSessions].

const (
	// TopicStreamTruncated is the synthetic WS event this server emits when
	// it could not deliver every event to this client. It is NOT an
	// rpc.EventBus topic — no backend publishes it — it is a transport-level
	// signal produced here. Payload: [StreamTruncatedPayload].
	TopicStreamTruncated = "served:stream-truncated"

	// topicLLMStreamChunk / topicLLMStreamClosed mirror the literals the chat
	// runner's stream bridge publishes on
	// (core/rpc/views/agentgraph/chat/stream_bridge.go). They cannot be
	// imported from that package because core/rpc imports it, not the other
	// way round; if those literals ever change, this list must change too and
	// TestServedChat_StreamTopicsMatchProducer will fail loudly.
	topicLLMStreamChunk  = "llm:stream-chunk"
	topicLLMStreamClosed = "llm:stream-closed"

	// defaultStreamQueueCap is the per-connection frame queue depth. 512
	// frames is roughly 30 s of token-level chunks at a typical streaming
	// rate — enough to ride out a browser GC pause or a repaint stall
	// without ever truncating, while capping worst-case memory per client.
	defaultStreamQueueCap = 512

	// busBufferSize is the bus-subscription buffer.
	//
	// The pump drains it eagerly, so in steady state it barely fills — but
	// "eagerly" means "as soon as the Go scheduler runs the pump", and a
	// publisher can emit a whole burst before that happens. Anything the
	// burst overruns is a bus-level drop, which is reported to the client
	// as truncation. Sizing this well above any plausible single burst
	// keeps that report meaningful: a truncation notice should mean the
	// BROWSER could not keep up, not that a goroutine was briefly
	// descheduled. Cost is one channel of small structs per WS client.
	busBufferSize = 1024

	// wsWriteTimeout bounds a single socket write. A client that has stopped
	// reading entirely is disconnected rather than pinning resources.
	wsWriteTimeout = 30 * time.Second
)

// StreamTruncatedPayload is the body of a [TopicStreamTruncated] frame.
//
// Dropped is the RUNNING TOTAL of events this connection failed to
// deliver, not a per-notice delta, so a client that itself missed a
// truncation notice still converges on the right number.
type StreamTruncatedPayload struct {
	Dropped uint64 `json:"dropped"`
	// Reason is stable, machine-readable copy for the UI to key on.
	// SD-15 (served-mode-is-a-real-mode-01PMZ707 WP08): this WAS an
	// unhonoured promise — the field rode the wire with nothing branching
	// on it. Now consumed by SessionsView.vue's streamTruncatedCopy(),
	// which looks the value up in a UI-owned copy table (decoupling the
	// notice's wording from this file's prose) and falls back to Message
	// for any reason it does not recognise. Only "slow-consumer" is
	// emitted today (below); adding a second value here needs a matching
	// entry in that lookup or it silently degrades to Message, which is
	// intentional, not a bug to "fix" by keeping the two in lockstep.
	Reason string `json:"reason"`
	// Message is human-readable guidance. It must stay actionable from
	// INSIDE a workbench — never "run the desktop app".
	Message string `json:"message"`
}

// passthroughTopics are bus topics forwarded to the browser verbatim: the
// WS event name is the bus topic name, and the payload is the bus payload
// unchanged. The served frontend subscribes to these through the same
// useEventStream() composable the desktop build uses, so the two surfaces
// consume identical event shapes.
var passthroughTopics = []string{
	// Chat streaming — the reason this file exists.
	topicLLMStreamChunk,
	topicLLMStreamClosed,
	rpc.TopicLLMFallbackAttempted,

	// Per-turn accounting the chat surface renders inline.
	rpc.TopicSessionUsageUpdated,
	// rpc.TopicCostThresholdCrossed stays SUBSCRIBED (it must, or
	// TestPassthroughTopics_MatchServedStreamTopicsTS fails against the
	// frontend's SERVED_STREAM_TOPICS) but never DELIVERED: its payload
	// (usage.ThresholdCrossedPayload) is a calendar-month account
	// aggregate with no SessionID field at all, unlike every other
	// entry in this list. frameFor's sessionIDOf probe therefore always
	// returns ok=false for it, and D-705 ("a topic whose payload
	// carries no session is not forwarded until it does — fail closed")
	// drops it for every connection rather than guessing it is safe to
	// broadcast the way TopicMigrationDriftDetected deliberately is
	// (processWideTopics, below — that exemption is earned by an
	// explicit product decision this topic does not have). Net effect:
	// the cost-threshold toast does not fire in served mode until a
	// follow-up gives this payload a session, or a product decision
	// adds it to processWideTopics. Recorded, not resolved, per E-701's
	// pattern — see WP01's commit message and report.
	rpc.TopicCostThresholdCrossed,

	// Interactive gates. Without these a tool call inside a workbench
	// would block on a permission modal that never opens, and the turn
	// would hang until it timed out with no explanation.
	rpc.TopicBashPermissionPending,
	rpc.TopicCredPermissionPending,
	rpc.TopicFSPermissionPending,
	rpc.TopicToolPermissionPending,

	// Blocking elicitation (kenaz__ask_user_question).
	rpc.TopicElicitPending,

	// Mid-turn context-overflow recovery (01PMGX01 WP17). Without this
	// a served-mode user watching a stalled stream gets no explanation
	// for the pause while the runner compacts and re-drives; the
	// terminal session_full report rides llm:stream-closed and is a
	// different event with a different meaning.
	rpc.TopicChatOverflowRecovery,

	// Confirm-each tool confirmation (confirm-each-enforcement-01PMAG05
	// WP02). Same reasoning as the permission gates above: the tool call
	// is PARKED with no deadline, so a dropped confirm-pending frame is
	// not a missing notification — it is a turn that never resumes.
	rpc.TopicToolConfirmPending,

	// Boot-time migration drift, severity:"error" only
	// (upgrade-path-coverage-01PMUG01 WP04, FR-3c). Without this a served
	// workbench user with a corrupted ledger gets no signal at all —
	// the desktop build's toast is the only surface, and served mode has
	// no other path to Settings → Health either.
	rpc.TopicMigrationDriftDetected,
}

// subscribedTopics is every topic the WS handler subscribes to: the
// pass-through set plus the session-list topic, which is transformed
// rather than forwarded verbatim.
func subscribedTopics() []string {
	out := make([]string, 0, len(passthroughTopics)+1)
	out = append(out, passthroughTopics...)
	out = append(out, rpc.TopicSessionListChanged)
	return out
}

// outFrame is a frame queued for delivery to one client.
type outFrame struct {
	event string
	data  any
}

// streamPump moves events from a tracked bus subscription into a bounded
// per-connection queue, counting anything it has to drop.
type streamPump struct {
	sub     *rpc.Subscription
	out     chan outFrame
	dropped atomic.Uint64
	done    chan struct{}
}

// enqueue offers f to the queue without ever blocking. Returns false when
// the frame was dropped (queue full) so the caller can account for it.
func (p *streamPump) enqueue(f outFrame) bool {
	select {
	case p.out <- f:
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

// totalDropped is queue-level drops plus bus-level drops. Both are real
// data loss for this client and both must be reported.
func (p *streamPump) totalDropped() uint64 {
	return p.dropped.Load() + p.sub.Dropped()
}

// streamQueueCap returns the configured per-connection queue depth.
func (s *Server) streamQueueCap() int {
	if s.queueCap > 0 {
		return s.queueCap
	}
	return defaultStreamQueueCap
}

// streamSessions subscribes to the bus, sends the initial snapshots, then
// pushes bus events to the client in real time under the backpressure policy
// documented at the top of this file.
//
// Receipt of the `sessions:snapshot` frame is the client's proof that the
// subscription is live: it is written after [rpc.EventBus.SubscribeTracked]
// returns, so no event published after that frame arrives can be silently
// lost. Callers and tests may rely on that ordering.
//
// The caller is the Sessions_Stream WS method handler, which has already
// rejected an absent/empty sessionID (AC-703) — every session-bearing
// frame this function forwards or snapshots is scoped to sessionID
// (served-mode-is-a-real-mode-01PMZ707 WP01).
func (s *Server) streamSessions(ctx context.Context, ws *websocket.Conn, sessionID string) {
	// writeFrame sends a frame under a deadline and reports whether the
	// connection is still usable.
	writeFrame := func(event string, data any) bool {
		_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return websocket.JSON.Send(ws, wsFrame{Event: event, Data: data}) == nil
	}

	// SUBSCRIBE FIRST, SNAPSHOT SECOND. The order matters and it is the
	// opposite of what it looks like it should be.
	//
	// The subscription is what makes this connection exist as far as the bus
	// is concerned, and rpc.EventBus.Publish DISCARDS events that have no
	// subscriber. Taking the snapshot first left a window — a Sessions().List
	// and an elicit ListPending, both real queries, plus two socket writes —
	// during which every event published for this client went nowhere. Not
	// dropped-and-counted: dropped and invisible. The connection was not
	// subscribed, so [streamPump.totalDropped] stayed zero, no
	// [TopicStreamTruncated] notice was owed, and the client was told nothing.
	//
	// That is the silent truncation this whole file exists to prevent, just
	// relocated to connection setup: a browser reconnecting mid-answer lost
	// precisely the tokens it reconnected to see, and every surface reported
	// success. It also made TestServedChat_SlowConsumerIsToldItWasTruncated
	// flaky on loaded CI, where the window stretched far enough to swallow an
	// entire 2000-event burst and the client read zero frames.
	//
	// Subscribing first converts that window from lost events into buffered
	// events: anything published while the snapshot is being built waits in
	// the subscription buffer and is delivered once the pump starts. It also
	// gives clients a guarantee worth having — receiving `sessions:snapshot`
	// now PROVES the subscription is live, so nothing published after it can
	// be missed.
	sub := s.bus.SubscribeTracked(busBufferSize, subscribedTopics()...)
	defer sub.Close()

	// Initial sessions snapshot, so the client always sees current state even
	// if no events ever arrive.
	sessions, err := s.api.Sessions().List(ctx)
	if err != nil {
		_ = websocket.JSON.Send(ws, wsFrame{Error: "sessions list: " + err.Error()})
		return
	}
	if !writeFrame("sessions:snapshot", sessions) {
		return
	}

	// Any currently-pending elicitation asks FOR THIS SESSION, so a
	// reconnecting frontend can reconstruct dialog state (FR-007).
	// Scoped (WP01, AC-704's sibling): the unscoped ListPending() is
	// still what the Wails-bound Elicit_ListPending RPC calls for
	// desktop's single-process, all-sessions-visible behaviour — this
	// is a different call, for the served WS reconnect snapshot only.
	if pending, perr := s.elicitAPI().ListPendingForSession(ctx, sessionID); perr == nil && len(pending) > 0 {
		if !writeFrame("elicit:pending:snapshot", pending) {
			return
		}
	}

	// Any currently-parked tool confirmations FOR THIS SESSION, so a
	// reconnecting frontend rebuilds the batched modal rather than
	// leaving the run parked behind a dialog the browser forgot about.
	// Same contract as the elicitation snapshot above: the pause lives
	// in the harness process, not in the dialog.
	//
	// Scoped (WP01, AC-704): "" was the all-sessions branch
	// (core/rpc/views/confirm/api.go ListPending) — passing it here
	// was half of the leak this WP closes. The served frontend's own
	// explicit Confirm_ListPending('') RPC call (ConfirmToolModal.vue)
	// is untouched and stays cross-session on purpose; this is only
	// the automatic WS-reconnect snapshot.
	if parked, cerr := s.api.Confirm().ListPending(ctx, sessionID); cerr == nil && len(parked) > 0 {
		if !writeFrame("tool:confirm-pending:snapshot", parked) {
			return
		}
	}

	pump := &streamPump{
		sub:  sub,
		out:  make(chan outFrame, s.streamQueueCap()),
		done: make(chan struct{}),
	}
	go s.runPump(ctx, pump, sessionID)

	// Drain incoming messages so a client ping or disconnect reaches us
	// promptly. Recover panics (FR-003).
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer recoverWS("serve.server.ws_read.panic")
		for {
			var ignored json.RawMessage
			if err := websocket.JSON.Receive(ws, &ignored); err != nil {
				return // connection closed
			}
		}
	}()

	// noticedDrops is the drop total the client has already been told about.
	var noticedDrops uint64

	for {
		select {
		case <-ctx.Done():
			_ = writeFrame("closed", nil)
			return
		case <-readDone:
			return // client closed the connection
		case <-pump.done:
			_ = writeFrame("closed", nil)
			return
		case f, ok := <-pump.out:
			if !ok {
				_ = writeFrame("closed", nil)
				return
			}
			// Report loss BEFORE the next frame so the notice lands at the
			// point in the transcript where the gap actually is.
			if total := pump.totalDropped(); total > noticedDrops {
				noticedDrops = total
				if !writeFrame(TopicStreamTruncated, StreamTruncatedPayload{
					Dropped: total,
					Reason:  "slow-consumer",
					Message: "This browser could not keep up with the live stream, so part of it was not delivered. The full reply is saved — reopen this conversation to read all of it.",
				}) {
					return
				}
			}
			if !writeFrame(f.event, f.data) {
				return
			}
		}
	}
}

// runPump is the sole reader of the bus subscription. It transforms each
// event into a client frame and hands it to the bounded queue without
// ever blocking on the socket.
func (s *Server) runPump(ctx context.Context, p *streamPump, sessionID string) {
	defer close(p.done)
	defer recoverWS("serve.server.ws_pump.panic")

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-p.sub.C:
			if !ok {
				return // bus closed (server shutting down)
			}
			f, forward := s.frameFor(ctx, ev, sessionID)
			if !forward {
				continue
			}
			p.enqueue(f)
		}
	}
}

// processWideTopics are the bus topics frameFor forwards to every
// connected client regardless of which session it subscribed to,
// because the event genuinely has no per-session owner — not because
// nobody got around to adding one.
//
// TopicMigrationDriftDetected: a boot-time storage-health signal for
// the whole process (upgrade-path-coverage-01PMUG01 WP04); there is no
// session to scope it to. Anything added here must be justified the
// same way SD-14's disposition required for the filter itself: this is
// an intentional exemption from D-705, not a shortcut around it.
var processWideTopics = map[string]bool{
	rpc.TopicMigrationDriftDetected: true,
}

// sessionIDOf extracts the session id from a bus event payload without
// importing every producer package's concrete type: payloads already
// cross the wire as JSON (outFrame.data, below), so re-marshalling here
// to probe one field costs nothing this file does not already pay at
// the socket write, and it keeps frameFor correct for any future
// session-bearing payload without a matching type-assertion branch.
//
// Session id fields use two json-tag spellings across existing
// producers (session_id: toolloop.ConfirmRequest, FlatPermissionRequest,
// StreamChunkPayload, StreamClosedPayload, FallbackAttemptedEvent,
// elicit.ElicitRequest, the chat:overflow-recovery map; sessionId:
// SessionUsagePayload) — both are probed. ok is false when neither key
// is present or both are empty, which is the D-705 "fails closed" case:
// a payload with no session id is indistinguishable here from one that
// forgot to carry it, and forwarding it anyway on the assumption "it's
// probably fine" is exactly the leak this WP closes.
func sessionIDOf(payload any) (id string, ok bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	var probe struct {
		Snake string `json:"session_id"`
		Camel string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", false
	}
	if probe.Snake != "" {
		return probe.Snake, true
	}
	if probe.Camel != "" {
		return probe.Camel, true
	}
	return "", false
}

// frameFor maps a bus event to the frame the browser receives, scoped to
// sessionID — the session this connection's Sessions_Stream subscribed
// to (served-mode-is-a-real-mode-01PMZ707 WP01).
//
// Most topics are forwarded verbatim so the served frontend consumes the
// exact payload shape the desktop build consumes. TopicSessionListChanged
// is the one transformation: the client wants a consistent list, not a
// bare delta, so the current list is re-read here (in the pump, off the
// socket-write path) and shipped alongside the change metadata. The
// session list itself is not scoped to one session (it is the sidebar's
// full list, same as the desktop build shows), so it is forwarded like
// the processWideTopics below.
//
// Every other topic is forwarded only when its payload's session id
// matches sessionID. This is the fix for the cross-session
// authorization leak (SD-14, now live rather than the dead
// `if !forward { continue }` the 2026-08-18 dead-code audit found
// unreachable at dead-code-audit-2026-08-18.md:1572): before this, every
// served WS client received every session's tool:confirm-pending,
// elicit:pending, *:permission-pending and llm:stream-chunk frames — and
// could resolve confirmations and elicitations it never should have
// seen.
func (s *Server) frameFor(ctx context.Context, ev rpc.BusEvent, sessionID string) (outFrame, bool) {
	if ev.Topic == rpc.TopicSessionListChanged {
		updated, err := s.api.Sessions().List(ctx)
		if err != nil {
			return outFrame{event: "error", data: map[string]string{"message": err.Error()}}, true
		}
		return outFrame{event: "sessions:update", data: map[string]any{
			"sessions": updated,
			"change":   ev.Payload,
		}}, true
	}
	if !processWideTopics[ev.Topic] {
		payloadSessionID, ok := sessionIDOf(ev.Payload)
		if !ok || payloadSessionID != sessionID {
			return outFrame{}, false
		}
	}
	return outFrame{event: ev.Topic, data: ev.Payload}, true
}

// recoverWS is the shared panic handler for the WS goroutines (FR-003):
// a panic tears down that goroutine cleanly and is logged, but never
// crashes the harness.
func recoverWS(logKey string) {
	if r := recover(); r != nil {
		logging.L().Error(logKey,
			"panic", fmt.Sprintf("%v", r),
			"stack", string(debug.Stack()),
		)
	}
}
