package chat

import (
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// Broker is the narrow surface the StreamBridge fans events onto. It
// mirrors the rpc.StreamBroker shape (Emit(topic, payload)) without
// importing core/rpc — keeping the chat package usable from anywhere
// in the chassis.
type Broker interface {
	Emit(topic string, payload any)
}

// StreamChunkPayload mirrors core/rpc/views/llm.StreamChunkPayload so
// the chassis broker continues fanning the existing "llm:stream-chunk"
// topic with the byte-equal wire shape. Defined locally to keep the
// chat package self-contained — the LLM view's payload type lives in
// a sibling package and importing it would create a needless cycle.
type StreamChunkPayload struct {
	SubID     string              `json:"sub_id"`
	SessionID string              `json:"session_id,omitempty"`
	Chunk     corellm.StreamEvent `json:"chunk"`
}

// StreamClosedPayload mirrors core/rpc/views/llm.StreamClosedPayload.
//
// PartialMessageID + PartialFailureKind + PartialRecoverable carry the
// resume-flow metadata stamped onto the closed payload by the
// chat-runner driveRun terminal path
// (long-turn-resilience-01KR3PRS WP03). Empty PartialMessageID means
// no partial row was persisted (either no text was emitted before the
// drop or no PartialPersister was wired). When non-empty, the frontend
// uses it to surface the Resume button on the freshly-committed
// partial bubble; PartialFailureKind selects the failure copy and
// PartialRecoverable gates the button's visibility.
type StreamClosedPayload struct {
	SubID              string `json:"sub_id"`
	SessionID          string `json:"session_id,omitempty"`
	Reason             string `json:"reason"`
	Message            string `json:"message,omitempty"`
	FinishReason       string `json:"finish_reason,omitempty"`
	PartialMessageID   string `json:"partial_message_id,omitempty"`
	PartialFailureKind string `json:"partial_failure_kind,omitempty"`
	PartialRecoverable bool   `json:"partial_recoverable,omitempty"`
}

// StreamBridge implements agentgraph.StreamSink by translating each
// kernel-bound StreamEvent into a corellm.StreamEvent and emitting
// it on the broker's "llm:stream-chunk" topic.
//
// Per-LLMNode dispatch the kernel pins one StreamBridge onto the
// run's Env via withStreamSink; a single chat run reuses the bridge
// across multiple LLM dispatches inside the LoopNode body. Close is
// idempotent — the runner's terminal-emit path calls it explicitly so
// the chat surface always sees an "llm:stream-closed" payload even
// when the kernel exits via a non-paused path.
type StreamBridge struct {
	broker    Broker
	subID     string
	sessionID string

	mu     sync.Mutex
	closed bool

	// partialText accumulates every text-delta the bridge sees so the
	// chat-runner driveRun terminal path can persist a partial assistant
	// row when the kernel returns an error mid-stream
	// (long-turn-resilience-01KR3PRS WP03). Reset to "" only when a
	// new turn opens via NewStreamBridge.
	partialText []byte
	// hasToolEvent flips true on the first StreamEventTool so the
	// runner knows tool_use already executed; a continuation prompt
	// would then double-bill, so the resume button is suppressed and
	// the user sees the "re-issue" footer instead.
	hasToolEvent bool
}

// NewStreamBridge constructs a bridge that fans every emitted event
// onto broker as the topics:
//   - llm:stream-chunk  for every Emit
//   - llm:stream-closed once Close fires (idempotent)
func NewStreamBridge(broker Broker, subID, sessionID string) *StreamBridge {
	return &StreamBridge{broker: broker, subID: subID, sessionID: sessionID}
}

// Emit satisfies agentgraph.StreamSink. Translates the kernel-side
// StreamEvent shape into a core/llm.StreamEvent (the wire shape the
// frontend already consumes via useSession.ts) and pushes it on the
// broker.
//
// Side effect: accumulates text deltas + flips the tool-seen flag so
// the chat-runner terminal path can decide whether to persist a partial
// row (long-turn-resilience-01KR3PRS WP03).
func (b *StreamBridge) Emit(ev coreag.StreamEvent) {
	if b == nil || b.broker == nil {
		return
	}
	// Track partial-text + tool-use side state under the same mutex
	// that guards the closed flag so a concurrent close doesn't race
	// with a final batch of deltas.
	b.mu.Lock()
	switch ev.Kind {
	case coreag.StreamEventText:
		if ev.Text != "" {
			b.partialText = append(b.partialText, ev.Text...)
		}
	case coreag.StreamEventTool:
		b.hasToolEvent = true
	}
	b.mu.Unlock()
	chunk := translateAGStreamEvent(ev)
	b.broker.Emit("llm:stream-chunk", StreamChunkPayload{
		SubID:     b.subID,
		SessionID: b.sessionID,
		Chunk:     chunk,
	})
}

// PartialState snapshots the accumulated text + tool-seen flag for the
// chat-runner driveRun terminal path so it can persist a partial
// assistant row when the kernel exits with an error
// (long-turn-resilience-01KR3PRS WP03). Safe to call concurrently with
// Emit.
func (b *StreamBridge) PartialState() (text string, hasTool bool) {
	if b == nil {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.partialText), b.hasToolEvent
}

// Close satisfies agentgraph.StreamSink. Emits a terminal
// llm:stream-closed payload exactly once. The chat runner's terminal
// path calls Close after the kernel run exits so the chat surface
// always sees a close signal.
func (b *StreamBridge) Close() {
	if b == nil || b.broker == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	b.broker.Emit("llm:stream-closed", StreamClosedPayload{
		SubID:     b.subID,
		SessionID: b.sessionID,
		Reason:    "completed",
	})
}

// EmitClosed is the runner-side terminal-emit helper — used when the
// kernel exits via a non-completed reason (cancelled, errored,
// budget-cap-hit). Idempotent: subsequent calls are silently dropped.
func (b *StreamBridge) EmitClosed(reason, message, finishReason string) {
	b.EmitClosedPartial(reason, message, finishReason, "", "", false)
}

// EmitClosedPartial is the WP03 superset of EmitClosed that stamps the
// resume-flow metadata onto the closed payload. Idempotent — once
// closed, subsequent EmitClosed* calls are silently dropped. When
// partialMessageID is empty + partialFailureKind is empty, this
// behaves identically to the legacy EmitClosed (so the wire shape
// degrades cleanly when no PartialPersister is wired).
//
// long-turn-resilience-01KR3PRS WP03.
func (b *StreamBridge) EmitClosedPartial(reason, message, finishReason,
	partialMessageID, partialFailureKind string, partialRecoverable bool) {
	if b == nil || b.broker == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	b.broker.Emit("llm:stream-closed", StreamClosedPayload{
		SubID:              b.subID,
		SessionID:          b.sessionID,
		Reason:             reason,
		Message:            message,
		FinishReason:       finishReason,
		PartialMessageID:   partialMessageID,
		PartialFailureKind: partialFailureKind,
		PartialRecoverable: partialRecoverable,
	})
}

// translateAGStreamEvent maps a kernel-side agentgraph.StreamEvent
// onto the corellm.StreamEvent shape the frontend already consumes.
// The two enums are intentionally string-equal; this function exists
// so both sides can evolve independently if the kernel ever grows a
// new event kind that has no llm-side analogue.
func translateAGStreamEvent(ev coreag.StreamEvent) corellm.StreamEvent {
	out := corellm.StreamEvent{
		Text:   ev.Text,
		Finish: ev.Finish,
		Err:    ev.ErrMsg,
	}
	if ev.ToolName != "" || ev.ToolID != "" || ev.ToolArgs != "" {
		out.Tool = &corellm.ToolUse{
			ID:    ev.ToolID,
			Name:  ev.ToolName,
			Input: []byte(ev.ToolArgs),
		}
	}
	if ev.Reasoning != "" {
		out.Reasoning = &corellm.ReasoningBlock{
			Type:    "reasoning",
			Content: ev.Reasoning,
		}
	}
	if ev.UsageInputTokens != 0 || ev.UsageOutputTokens != 0 || ev.UsageReasoningTokens != 0 {
		out.Usage = &corellm.Usage{
			InputTokens:     ev.UsageInputTokens,
			OutputTokens:    ev.UsageOutputTokens,
			ReasoningTokens: ev.UsageReasoningTokens,
		}
	}
	switch ev.Kind {
	case coreag.StreamEventText:
		out.Kind = corellm.StreamText
	case coreag.StreamEventTool:
		out.Kind = corellm.StreamTool
	case coreag.StreamEventReasoning:
		out.Kind = corellm.StreamReasoning
	case coreag.StreamEventUsage:
		out.Kind = corellm.StreamUsage
	case coreag.StreamEventFinish:
		out.Kind = corellm.StreamFinish
	case coreag.StreamEventError:
		out.Kind = corellm.StreamError
	default:
		out.Kind = corellm.StreamEventKind(string(ev.Kind))
	}
	return out
}
