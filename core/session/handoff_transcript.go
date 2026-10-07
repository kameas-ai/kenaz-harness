package session

// handoff_transcript.go — the portable, self-contained transcript
// serialization a team handoff carries (device-keys-handoff-01DEVKH01
// OQ-3, shared by WP04 serialize and WP05 persist).
//
// Why not the session-sync stream payload: those are {"id","role"}
// POINTERS into the sender's local store, useless on another user's
// machine. A handoff event must stand alone.
//
// # Wire format "kenaz.handoff.event" v1 (one JSON object per event,
// encrypted per event by core/fleet; seq = 1..N in transcript order)
//
//	{
//	  "v": 1,                          // REQUIRED; a reader rejects any other value
//	  "role": "user|assistant|system|tool",
//	  "content": "...",                // display text (blocks flattened when Content was empty)
//	  "tool_calls": [{"id","name","result","is_error"}],   // DISPLAY layer only
//	  "move": {"kind","index","turn_seq"},                 // absent on classic rows
//	  "omitted_media": 2,              // image/document/generated-image blocks NOT shipped
//	  "created_at": "RFC3339Nano",
//	  "title": "..."                   // seq 1 only: the sender's session name
//	}
//
// Redaction (spec §4 two-layer rule): what is shared is what the user can
// read and export — the DISPLAY layer. Model-layer raw tool arguments
// (Message.modelToolArgs) and ToolCall.Arguments are never serialized, and
// neither are the secret-use flag, usage/cost or provider columns. On the
// receiving side a tool_call move without model args is dropped by the
// model-history composition and its tool_result swept as an orphan, so an
// accepted session the recipient continues can never ship a half pair.
//
// Attachments policy: media blocks (inline images/documents, generated
// images whose bytes live in the SENDER's artifact store) are not shipped —
// they routinely exceed fleet's 2 MiB/event cap and generated images are
// local references. The count travels as omitted_media and the recipient's
// copy says so in the message text. Tool results longer than
// handoffMaxToolResult are truncated with a visible marker.
//
// turn_seq names the seq of the user event that opened a move's turn, so
// the receiver can re-link the turn span to its own fresh row ids
// (ReplayTranscript's remap). 0 means the opening row was not shipped.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// HandoffEventVersion is the only version this build reads or writes.
const HandoffEventVersion = 1

// handoffMaxToolResult caps one tool result in a shared copy (bytes).
const handoffMaxToolResult = 256 << 10

const handoffTruncatedMarker = "\n…[truncated in the shared copy]"

// ErrHandoffEventVersion: an event carried a version this build cannot read.
var ErrHandoffEventVersion = errors.New("session: unsupported shared-session event version")

// ErrHandoffEventInvalid: an event failed validation (role, move contract).
var ErrHandoffEventInvalid = errors.New("session: invalid shared-session event")

// HandoffToolCall is the display-layer tool call: no arguments, ever.
type HandoffToolCall struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Result  string `json:"result,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

// HandoffMove is a move's metadata with the turn span expressed as a seq.
type HandoffMove struct {
	Kind    string `json:"kind"`
	Index   int    `json:"index"`
	TurnSeq uint64 `json:"turn_seq,omitempty"`
}

// HandoffEvent is one serialized transcript row.
type HandoffEvent struct {
	V            int               `json:"v"`
	Role         string            `json:"role"`
	Content      string            `json:"content,omitempty"`
	ToolCalls    []HandoffToolCall `json:"tool_calls,omitempty"`
	Move         *HandoffMove      `json:"move,omitempty"`
	OmittedMedia int               `json:"omitted_media,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	Title        string            `json:"title,omitempty"`
}

// HandoffTranscript is a decoded shared session.
type HandoffTranscript struct {
	// Title is the sender's session name (may be empty).
	Title string
	// Messages are ready for Manager.ReplayTranscript: ids are synthetic
	// ("handoff-seq-<n>") so the replay's turn-span remap re-links moves to
	// the destination's fresh user-row ids.
	Messages []Message
}

func handoffSourceID(seq uint64) string { return fmt.Sprintf("handoff-seq-%d", seq) }

// isMediaBlock reports a content block that is not shipped.
func isMediaBlock(b llm.ContentBlock) bool {
	switch b.Type {
	case "", "text", "tool_use", "tool_result":
		return false
	}
	return true
}

// EncodeHandoffTranscript serializes msgs (transcript order) into one
// self-contained event payload per row; payload i is seq i+1. title rides
// on seq 1.
//
// Privacy invariant: the returned bytes are plaintext transcript content;
// callers encrypt them immediately and never log them.
func EncodeHandoffTranscript(title string, msgs []Message) ([][]byte, error) {
	seqOfUser := make(map[string]uint64, len(msgs))
	for i, m := range msgs {
		if m.Role == RoleUser && m.ID != "" {
			seqOfUser[m.ID] = uint64(i + 1)
		}
	}
	out := make([][]byte, 0, len(msgs))
	for i, m := range msgs {
		ev := HandoffEvent{V: HandoffEventVersion, Role: string(m.Role), Content: m.Content}
		if ev.Content == "" && len(m.ContentBlocks) > 0 {
			ev.Content = llm.Message{Content: m.ContentBlocks}.Text()
		}
		for _, b := range m.ContentBlocks {
			if isMediaBlock(b) {
				ev.OmittedMedia++
			}
		}
		for _, tc := range m.ToolCalls {
			res := tc.Result
			if len(res) > handoffMaxToolResult {
				res = strings.ToValidUTF8(res[:handoffMaxToolResult], "") + handoffTruncatedMarker
			}
			ev.ToolCalls = append(ev.ToolCalls, HandoffToolCall{ID: tc.ID, Name: tc.Name, Result: res, IsError: tc.IsError})
		}
		if k := m.MoveKind(); k != "" {
			mv := &HandoffMove{Kind: string(k)}
			if idx := m.MoveIndex(); idx != nil {
				mv.Index = *idx
			}
			mv.TurnSeq = seqOfUser[m.TurnSpanID()]
			ev.Move = mv
		}
		if !m.CreatedAt.IsZero() {
			ev.CreatedAt = m.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		if i == 0 {
			ev.Title = title
		}
		b, err := json.Marshal(ev)
		if err != nil {
			return nil, fmt.Errorf("session: encode shared event %d: %w", i+1, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// DecodeHandoffTranscript parses events (payload i is seq i+1) back into
// messages for ReplayTranscript. Any unknown version or invalid row fails
// the whole decode — a partially imported session is worse than none.
func DecodeHandoffTranscript(payloads [][]byte) (HandoffTranscript, error) {
	evs := make([]HandoffEvent, 0, len(payloads))
	for i, p := range payloads {
		var ev HandoffEvent
		if err := json.Unmarshal(p, &ev); err != nil {
			return HandoffTranscript{}, fmt.Errorf("%w: event %d: %v", ErrHandoffEventInvalid, i+1, err)
		}
		if ev.V != HandoffEventVersion {
			return HandoffTranscript{}, fmt.Errorf("%w: event %d has v=%d", ErrHandoffEventVersion, i+1, ev.V)
		}
		switch Role(ev.Role) {
		case RoleUser, RoleAssistant, RoleSystem, RoleTool:
		default:
			return HandoffTranscript{}, fmt.Errorf("%w: event %d role %q", ErrHandoffEventInvalid, i+1, ev.Role)
		}
		evs = append(evs, ev)
	}
	msgs, err := handoffMessages(evs)
	if err != nil {
		return HandoffTranscript{}, err
	}
	t := HandoffTranscript{Messages: msgs}
	if len(evs) > 0 {
		t.Title = strings.TrimSpace(evs[0].Title)
	}
	return t, nil
}

// handoffDisplayContent renders an event's text plus an honest note when
// media was left out of the shared copy.
func handoffDisplayContent(ev HandoffEvent) string {
	if ev.OmittedMedia <= 0 {
		return ev.Content
	}
	note := "[1 attachment was not included in the shared copy]"
	if ev.OmittedMedia > 1 {
		note = fmt.Sprintf("[%d attachments were not included in the shared copy]", ev.OmittedMedia)
	}
	if ev.Content == "" {
		return note
	}
	return ev.Content + "\n\n" + note
}
