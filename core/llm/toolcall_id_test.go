package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSynthesizeToolCallID(t *testing.T) {
	if got := SynthesizeToolCallID("gen-abc", 0); got != "gen-abc-0" {
		t.Errorf("with genID: got %q, want %q", got, "gen-abc-0")
	}
	if got := SynthesizeToolCallID("gen-abc", 2); got != "gen-abc-2" {
		t.Errorf("with genID+index: got %q, want %q", got, "gen-abc-2")
	}
	if got := SynthesizeToolCallID("", 1); got != "call_1" {
		t.Errorf("without genID: got %q, want %q", got, "call_1")
	}
	// Always non-empty.
	if SynthesizeToolCallID("", 0) == "" {
		t.Error("synthesized id must never be empty")
	}
}

func TestEnsureToolCallID(t *testing.T) {
	if got := EnsureToolCallID("call_or_9", "gen-x", 0); got != "call_or_9" {
		t.Errorf("non-empty id must pass through: got %q", got)
	}
	if got := EnsureToolCallID("", "gen-x", 3); got != "gen-x-3" {
		t.Errorf("empty id must be synthesized: got %q, want %q", got, "gen-x-3")
	}
	if got := EnsureToolCallID("   ", "gen-x", 3); got != "gen-x-3" {
		t.Errorf("whitespace id must be synthesized: got %q, want %q", got, "gen-x-3")
	}
}

func toolUseMsg(id, name string) Message {
	return Message{Role: RoleAssistant, Content: []ContentBlock{
		{Type: "tool_use", ToolUse: &ToolUse{ID: id, Name: name, Input: json.RawMessage(`{}`)}},
	}}
}

func toolResultMsg(id string) Message {
	return Message{Role: RoleUser, Content: []ContentBlock{
		{Type: "tool_result", ToolResult: &ToolResult{ToolUseID: id, Content: json.RawMessage(`"ok"`)}},
	}}
}

func TestValidateToolCallIDs(t *testing.T) {
	tests := []struct {
		name    string
		msgs    []Message
		wantErr string // substring; "" means no error
	}{
		{
			name: "valid paired ids",
			msgs: []Message{toolUseMsg("call_0", "bash"), toolResultMsg("call_0")},
		},
		{
			name:    "empty tool_use id",
			msgs:    []Message{toolUseMsg("", "bash")},
			wantErr: "tool_use",
		},
		{
			name:    "empty tool_result id",
			msgs:    []Message{toolUseMsg("call_0", "bash"), toolResultMsg("")},
			wantErr: "empty tool_use_id",
		},
		{
			name: "plain text messages are fine",
			msgs: []Message{NewTextMessage(RoleUser, "hi"), NewTextMessage(RoleAssistant, "hello")},
		},
		{
			name: "legacy ToolData block is skipped",
			msgs: []Message{{Role: RoleUser, Content: []ContentBlock{
				{Type: "tool_result", ToolData: json.RawMessage(`"legacy"`)},
			}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateToolCallIDs(tc.msgs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
