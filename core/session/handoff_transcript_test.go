package session

// handoff_transcript_test.go — device-keys-handoff-01DEVKH01 OQ-3 codec.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

func idx(i int) *int { return &i }

func handoffSample() []Message {
	t0 := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	return []Message{
		{ID: "u1", Role: RoleUser, Content: "what's in the repo?", CreatedAt: t0},
		{ID: "m1", Role: RoleTool, Content: "list_files(path: string)", moveKind: MoveKindToolCall, moveIndex: idx(0), moveTurnSpanID: "u1",
			ToolCalls:     []ToolCall{{ID: "call-1", Name: "list_files", Arguments: map[string]any{"path": "/secret/raw"}}},
			modelToolArgs: map[string]string{"call-1": `{"path":"/secret/raw"}`}},
		{ID: "m2", Role: RoleTool, Content: "a.go b.go", moveKind: MoveKindToolResult, moveIndex: idx(1), moveTurnSpanID: "u1",
			ToolCalls: []ToolCall{{ID: "call-1", Name: "list_files", Result: strings.Repeat("r", handoffMaxToolResult+10), UsedSecrets: true}}},
		{ID: "m3", Role: RoleAssistant, Content: "Two Go files.", moveKind: MoveKindFinal, moveIndex: idx(2), moveTurnSpanID: "u1"},
		{ID: "u2", Role: RoleUser, ContentBlocks: []llm.ContentBlock{
			{Type: "text", Text: "look at this"},
			{Type: "image", Source: &llm.MediaSource{Kind: "base64", Data: "AAAA"}},
		}},
	}
}

func TestHandoffTranscript_RoundTrip(t *testing.T) {
	payloads, err := EncodeHandoffTranscript("Repo tour", handoffSample())
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 5 {
		t.Fatalf("payloads = %d", len(payloads))
	}
	for i, p := range payloads {
		// Display layer only: no raw argument value, no model-layer args,
		// no secret-use flag, on ANY event.
		if strings.Contains(string(p), "/secret/raw") || strings.Contains(string(p), "usedSecrets") ||
			strings.Contains(string(p), "arguments") {
			t.Fatalf("event %d leaks model-layer data: %s", i+1, p)
		}
		var ev map[string]any
		_ = json.Unmarshal(p, &ev)
		if ev["v"] != float64(1) {
			t.Fatalf("event %d version = %v", i+1, ev["v"])
		}
	}
	tr, err := DecodeHandoffTranscript(payloads)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title != "Repo tour" || len(tr.Messages) != 5 {
		t.Fatalf("decoded = %q %d", tr.Title, len(tr.Messages))
	}
	m := tr.Messages
	if m[1].MoveKind() != MoveKindToolCall || *m[1].MoveIndex() != 0 || m[1].TurnSpanID() != "handoff-seq-1" || m[0].ID != "handoff-seq-1" {
		t.Fatalf("move metadata not re-linked: %+v / span %q", m[1].MoveKind(), m[1].TurnSpanID())
	}
	if m[1].ModelLayerToolArgs() != nil || m[1].ToolCalls[0].Arguments != nil {
		t.Fatal("decoded tool_call must carry no arguments")
	}
	if !strings.HasSuffix(m[2].ToolCalls[0].Result, handoffTruncatedMarker) || m[2].ToolCalls[0].UsedSecrets {
		t.Fatalf("tool result not truncated / flag leaked")
	}
	if m[3].MoveKind() != MoveKindFinal || m[3].Content != "Two Go files." {
		t.Fatalf("final = %+v", m[3])
	}
	if m[4].Content != "look at this\n\n[1 attachment was not included in the shared copy]" {
		t.Fatalf("media note = %q", m[4].Content)
	}
}

func TestHandoffTranscript_RejectsBadEvents(t *testing.T) {
	for name, p := range map[string]string{
		"version":     `{"v":2,"role":"user","content":"x","event_count":1}`,
		"no version":  `{"role":"user","content":"x","event_count":1}`,
		"role":        `{"v":1,"role":"root","content":"x","event_count":1}`,
		"kind":        `{"v":1,"role":"assistant","move":{"kind":"bogus","index":0},"event_count":1}`,
		"index":       `{"v":1,"role":"assistant","move":{"kind":"final","index":-1},"event_count":1}`,
		"forward":     `{"v":1,"role":"assistant","move":{"kind":"final","index":0,"turn_seq":5},"event_count":1}`,
		"json":        `{"v":1,`,
		"no count":    `{"v":1,"role":"user","content":"x"}`,
		"wrong count": `{"v":1,"role":"user","content":"x","event_count":3}`,
	} {
		_, err := DecodeHandoffTranscript([][]byte{[]byte(p)})
		if !errors.Is(err, ErrHandoffEventInvalid) && !errors.Is(err, ErrHandoffEventVersion) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Review fix #2: tail truncation is detected via the AEAD-covered
// event_count on seq 1.
func TestHandoffTranscript_TailTruncationDetected(t *testing.T) {
	payloads, err := EncodeHandoffTranscript("t", handoffSample())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payloads[0]), `"event_count":5`) {
		t.Fatalf("seq 1 lacks event_count: %s", payloads[0])
	}
	if _, err := DecodeHandoffTranscript(payloads[:4]); !errors.Is(err, ErrHandoffEventInvalid) {
		t.Fatalf("dropped tail must fail: %v", err)
	}
}
