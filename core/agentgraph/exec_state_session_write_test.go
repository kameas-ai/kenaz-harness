package agentgraph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSessionWriteExecutor_EmptyTextVsMissingInput pins the split between
// the two states the executor used to conflate under `!ok || text == ""`:
//
//   - NO readable value on the port is a graph-wiring defect and stays a
//     hard error (EventNodeError, non-nil error).
//   - A value that is present but whose text is EMPTY is a legitimate
//     tool-only turn (dogfood 2026-10-06: openai/gpt-6-astra ran
//     kenaz__bash and ended the turn with no trailing prose, and the send
//     failed at persistence). It soft-skips: appended=false, message_id="",
//     a session_write event with skipped=true / reason=empty_text, no error,
//     and the HistoryWriter is never called.
//
// A []Message whose trailing message is empty skips too, even when an
// earlier message has text: on the chat path that earlier text was already
// persisted as an assistant_move by the turn journal, so reaching back for
// it would write the same paragraph twice.
func TestSessionWriteExecutor_EmptyTextVsMissingInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		in           PortValues
		wantErr      bool
		wantAppended bool
		wantBody     string // expected HistoryWriter body when appended
		wantEvent    EventKind
		wantSkipped  bool
	}{
		{
			name:      "no input on port is a wiring error",
			in:        PortValues{},
			wantErr:   true,
			wantEvent: EventNodeError,
		},
		{
			name:      "unreadable type on port is a wiring error",
			in:        PortValues{"assistant_text": 42},
			wantErr:   true,
			wantEvent: EventNodeError,
		},
		{
			name:        "empty string is a soft skip",
			in:          PortValues{"assistant_text": ""},
			wantEvent:   EventSessionWrite,
			wantSkipped: true,
		},
		{
			name:        "empty Message is a soft skip",
			in:          PortValues{"assistant_text": Message{Role: "assistant"}},
			wantEvent:   EventSessionWrite,
			wantSkipped: true,
		},
		{
			name: "trailing empty message after earlier text is a soft skip",
			in: PortValues{"assistant_text": []Message{
				{Role: "user", Content: "run the check"},
				{Role: "assistant", Content: "Running it now."},
				{Role: "tool", Content: "OK"},
				{Role: "assistant", Content: ""},
			}},
			wantEvent:   EventSessionWrite,
			wantSkipped: true,
		},
		{
			name:         "non-empty string persists unchanged",
			in:           PortValues{"assistant_text": "done"},
			wantAppended: true,
			wantBody:     "done",
			wantEvent:    EventSessionWrite,
		},
		{
			name: "non-empty trailing message persists unchanged",
			in: PortValues{"assistant_text": []Message{
				{Role: "user", Content: "q"},
				{Role: "assistant", Content: "the answer"},
			}},
			wantAppended: true,
			wantBody:     "the answer",
			wantEvent:    EventSessionWrite,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var bodies []string
			env := &Env{
				RunID:     "r",
				SessionID: "s1",
				HistoryWriter: HistoryWriterFunc(func(_ context.Context, _ string, e HistoryEntry) (string, error) {
					bodies = append(bodies, e.Content)
					return "msg-1", nil
				}),
			}
			applyEnvDefaults(env)
			node := &Node{ID: "assistant_write", Kind: NodeKindSessionWrite, Attrs: SessionWriteAttrs{
				Role: "assistant", TextInputPort: "assistant_text",
			}}
			res, err := sessionWriteExecutor{}.Execute(context.Background(), env, node, tc.in)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), `missing text on port "assistant_text"`) {
					t.Errorf("error message changed: %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantAppended {
				if len(bodies) != 1 || bodies[0] != tc.wantBody {
					t.Errorf("writer bodies = %q, want [%q]", bodies, tc.wantBody)
				}
				if res.Outputs["appended"] != true || res.Outputs["message_id"] != "msg-1" {
					t.Errorf("outputs = %+v", res.Outputs)
				}
			} else if len(bodies) != 0 {
				t.Errorf("writer must not be called, got %q", bodies)
			}
			if !tc.wantErr && !tc.wantAppended {
				if res.Outputs["appended"] != false {
					t.Errorf("appended = %v, want false", res.Outputs["appended"])
				}
				if mid, ok := res.Outputs["message_id"]; !ok || mid != "" {
					t.Errorf("message_id = %v (present=%v), want \"\"", mid, ok)
				}
			}

			var found *Event
			for i := range res.Events.Events {
				if res.Events.Events[i].Kind == tc.wantEvent {
					found = &res.Events.Events[i]
				}
			}
			if found == nil {
				t.Fatalf("missing %s event; got %+v", tc.wantEvent, res.Events.Events)
			}
			if tc.wantSkipped {
				var p map[string]any
				if err := json.Unmarshal(found.Payload, &p); err != nil {
					t.Fatalf("payload: %v", err)
				}
				if p["skipped"] != true || p["reason"] != "empty_text" || p["port"] != "assistant_text" {
					t.Errorf("skip payload = %v", p)
				}
				for _, e := range res.Events.Events {
					if e.Kind == EventNodeError {
						t.Errorf("soft skip must not emit node_error")
					}
				}
			}
		})
	}
}
