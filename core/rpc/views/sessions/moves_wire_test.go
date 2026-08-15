package sessions

// moves_wire_test.go — the additive-wire half of
// model-moves-transcript-01PMCH01 WP01.
//
// Spec §4: "Wire shape additive: transcript entries gain kind/move
// metadata (assistant_move, tool_call, tool_result, final) with
// omitempty; old clients see messages they already understand."
//
// "Old clients see what they already understand" is only true if the
// serialized bytes of a kind-less entry are unchanged. There was no
// session-message golden in this repo before this file, so the goldens
// below are constructed: they are the bytes messageToView produced
// before the move fields were added, transcribed literally.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

var wireTestClock = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

// TestMessageToView_ClassicEntryBytesUnchanged pins the pre-mission
// serialization of an entry with no move metadata.
//
// Mutation checks (each one kills this test):
//   - drop `omitempty` from Kind / MoveIndex / TurnSpanID on the wire
//     struct → `"kind":""`, `"turnSpanId":""` appear;
//   - have messageToView stamp a default kind (e.g. "final") on
//     assistant rows → `"kind":"final"` appears;
//   - make MoveIndex a plain int on the wire struct and set it → 0 is
//     omitted here, but TestMessageToView_MoveIndexZeroReachesTheWire
//     catches that direction.
func TestMessageToView_ClassicEntryBytesUnchanged(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   session.Message
		want string
	}{
		{
			name: "minimal assistant row",
			in: session.Message{
				ID:        "m1",
				SessionID: "s1",
				Role:      session.RoleAssistant,
				Content:   "hello",
				CreatedAt: wireTestClock,
			},
			want: `{"id":"m1","sessionId":"s1","role":"assistant","content":"hello",` +
				`"createdAt":"2026-08-14T12:00:00Z"}`,
		},
		{
			name: "user row with a tool call and usage",
			in: session.Message{
				ID:                "m2",
				SessionID:         "s1",
				Role:              session.RoleUser,
				Content:           "run it",
				CreatedAt:         wireTestClock,
				ToolCalls:         []session.ToolCall{{ID: "tc-1", Name: "bash", UsedSecrets: true}},
				PromptTokens:      iptr(11),
				CompletionTokens:  iptr(22),
				CostUSD:           fptr(0.5),
				MessageCostSource: "provider",
			},
			want: `{"id":"m2","sessionId":"s1","role":"user","content":"run it",` +
				`"createdAt":"2026-08-14T12:00:00Z",` +
				`"toolCalls":[{"id":"tc-1","name":"bash","argsSummary":"","usedSecrets":true}],` +
				`"promptTokens":11,"completionTokens":22,"costUsd":0.5,` +
				`"messageCostSource":"provider"}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(messageToView(tc.in))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("wire bytes changed for a kind-less entry.\n got: %s\nwant: %s",
					b, tc.want)
			}
		})
	}
}

// appendMove writes one move through the single seam and hands back the
// persisted row. Move metadata is unexported on session.Message, so
// this is the ONLY way a test in this package can obtain a move-kind
// entry — which is the single-writer rule doing its job.
func appendMove(t *testing.T, e session.TranscriptEntry) session.Message {
	t.Helper()
	ctx := context.Background()
	mgr := session.NewManager(session.NewMemoryStore())
	rec, err := mgr.Create(ctx, "s")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := mgr.AppendTranscriptEntry(ctx, rec.ID, e)
	if err != nil {
		t.Fatalf("AppendTranscriptEntry: %v", err)
	}
	return stored
}

// TestMessageToView_MoveMetadataReachesTheWire is the positive half:
// a move-kind entry projects all three fields.
//
// Mutation: delete any of the three assignments at the tail of
// messageToView → the corresponding subtest fails.
func TestMessageToView_MoveMetadataReachesTheWire(t *testing.T) {
	t.Parallel()

	for i, kind := range session.MoveKinds() {
		kind := kind
		idx := i
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			stored := appendMove(t, session.TranscriptEntry{
				Role:       session.RoleAssistant,
				Content:    "move text",
				Kind:       kind,
				MoveIndex:  idx,
				TurnSpanID: "turn-7",
			})
			view := messageToView(stored)
			if view.Kind != string(kind) {
				t.Errorf("Kind = %q, want %q", view.Kind, kind)
			}
			if view.MoveIndex == nil {
				t.Fatalf("MoveIndex is nil, want %d", idx)
			}
			if *view.MoveIndex != idx {
				t.Errorf("MoveIndex = %d, want %d", *view.MoveIndex, idx)
			}
			if view.TurnSpanID != "turn-7" {
				t.Errorf("TurnSpanID = %q, want turn-7", view.TurnSpanID)
			}
		})
	}
}

// TestMessageToView_MoveIndexZeroReachesTheWire is the reason
// Message.MoveIndex is *int on the wire. Index 0 is the first move of
// every turn; with a plain int + omitempty it would silently vanish and
// WP04's bubble ordering would start at 1.
//
// Mutation: change the wire field to `MoveIndex int
// json:"moveIndex,omitempty"` → the marshalled bytes lose "moveIndex"
// and this fails.
func TestMessageToView_MoveIndexZeroReachesTheWire(t *testing.T) {
	t.Parallel()
	stored := appendMove(t, session.TranscriptEntry{
		Role:       session.RoleAssistant,
		Content:    "first move",
		Kind:       session.MoveKindAssistantMove,
		MoveIndex:  0,
		TurnSpanID: "turn-7",
	})
	b, err := json.Marshal(messageToView(stored))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, ok := decoded["moveIndex"]
	if !ok {
		t.Fatalf("moveIndex absent from the wire for index 0: %s", b)
	}
	if got != float64(0) {
		t.Errorf("moveIndex = %v, want 0", got)
	}
	if decoded["kind"] != string(session.MoveKindAssistantMove) {
		t.Errorf("kind = %v, want %q", decoded["kind"], session.MoveKindAssistantMove)
	}
	if decoded["turnSpanId"] != "turn-7" {
		t.Errorf("turnSpanId = %v, want turn-7", decoded["turnSpanId"])
	}
}
