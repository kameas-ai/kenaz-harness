package event

import "encoding/json"

type Type string

const (
	TypeSessionStarted         Type = "session_started"
	TypeContextAssembled       Type = "context_assembled"
	TypeUserMessage            Type = "user_message"
	TypeAssistantMessageDelta  Type = "assistant_message_delta"
	TypeAssistantMessage       Type = "assistant_message"
	TypeToolCall               Type = "tool_call"
	TypeToolResultDelta        Type = "tool_result_delta"
	TypeToolResult             Type = "tool_result"
	TypeMemoryWrite            Type = "memory_write"
	TypeOutputCreated          Type = "output_created"
	TypeSessionEnded           Type = "session_ended"
)

type Actor string

const (
	ActorUser      Actor = "user"
	ActorAssistant Actor = "assistant"
	ActorSystem    Actor = "system"
)

func ActorTool(name string) Actor { return Actor("tool:" + name) }

type Event struct {
	SessionID   string          `json:"session_id"`
	Seq         int64           `json:"seq"`
	ParentSeq   *int64          `json:"parent_seq,omitempty"`
	Type        Type            `json:"type"`
	TS          int64           `json:"ts"`
	Actor       Actor           `json:"actor"`
	ContentHash string          `json:"content_hash"`
	Payload     json.RawMessage `json:"payload"`
}

type SessionStartedPayload struct {
	BundleSet   []BundleRef            `json:"bundle_set"`
	Model       string                 `json:"model"`
	Params      map[string]any         `json:"params,omitempty"`
	ParentEvent *EventRef              `json:"parent_event,omitempty"`
}

type BundleRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type EventRef struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

type ContextAssembledPayload struct {
	BlobDigest string `json:"blob_digest"`
	Bytes      int64  `json:"bytes"`
}

type UserMessagePayload struct {
	Text string `json:"text"`
}

type AssistantMessageDeltaPayload struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
}

type AssistantMessagePayload struct {
	TurnID string `json:"turn_id"`
	Text   string `json:"text"`
}

type ToolCallPayload struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type ToolResultDeltaPayload struct {
	CallID string `json:"call_id"`
	Chunk  string `json:"chunk"`
}

type ToolResultPayload struct {
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error,omitempty"`
}

type MemoryWritePayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type OutputCreatedPayload struct {
	BlobDigest string `json:"blob_digest"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	Name       string `json:"name,omitempty"`
}

type SessionEndedPayload struct {
	Reason string `json:"reason"`
}
