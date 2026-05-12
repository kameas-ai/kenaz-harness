package narrative

import (
	"strings"
	"testing"
	"time"
)

func TestBuildToolNarrative_Schema(t *testing.T) {
	t.Parallel()
	b := NewExtractiveBuilder()
	n := b.BuildToolNarrative("read_file", `{"path": "/tmp/foo"}`, "hello world", 5*time.Millisecond, true)

	if n.Tool != "read_file" {
		t.Errorf("Tool = %q, want %q", n.Tool, "read_file")
	}
	if n.DurationMs != 5 {
		t.Errorf("DurationMs = %d, want 5", n.DurationMs)
	}
	if !n.Success {
		t.Error("Success should be true")
	}
	if n.ArgsSummary == "" {
		t.Error("ArgsSummary should not be empty")
	}
	if n.ResultSummary == "" {
		t.Error("ResultSummary should not be empty")
	}
}

func TestBuildToolNarrative_Truncation(t *testing.T) {
	t.Parallel()
	b := NewExtractiveBuilder()
	longArgs := strings.Repeat("a", 1000)
	longResult := strings.Repeat("b", 1000)
	n := b.BuildToolNarrative("mytool", longArgs, longResult, 10*time.Millisecond, false)

	if len(n.ArgsSummary) > maxSummaryBytes+10 {
		t.Errorf("ArgsSummary too long: %d bytes", len(n.ArgsSummary))
	}
	if len(n.ResultSummary) > maxSummaryBytes+10 {
		t.Errorf("ResultSummary too long: %d bytes", len(n.ResultSummary))
	}
	// Head+tail separator must be present.
	if !strings.Contains(n.ArgsSummary, "…") {
		t.Error("ArgsSummary should contain separator for long input")
	}
}

func TestBuildTurnFallback_Schema(t *testing.T) {
	t.Parallel()
	b := NewExtractiveBuilder()
	tools := []ToolNarrative{
		{Tool: "search", ArgsSummary: "query", ResultSummary: "3 results", DurationMs: 50, Success: true},
		{Tool: "read_file", ArgsSummary: "path", ResultSummary: "content", DurationMs: 10, Success: true},
	}
	fb := b.BuildTurnFallback("Please help me find X", "I found X in result 2.", tools)

	if fb.Request == "" {
		t.Error("Request should not be empty")
	}
	if !strings.Contains(fb.Investigated, "search") {
		t.Errorf("Investigated should mention 'search', got %q", fb.Investigated)
	}
	if !strings.Contains(fb.Investigated, "read_file") {
		t.Errorf("Investigated should mention 'read_file', got %q", fb.Investigated)
	}
	if !strings.Contains(fb.Learned, "search: 3 results") {
		t.Errorf("Learned should contain tool result, got %q", fb.Learned)
	}
	if fb.Outcome == "" {
		t.Error("Outcome should not be empty")
	}
}

func TestBuildTurnFallback_NoTools(t *testing.T) {
	t.Parallel()
	b := NewExtractiveBuilder()
	fb := b.BuildTurnFallback("Hello", "World", nil)
	if fb.Investigated != "(no tools called)" {
		t.Errorf("Investigated = %q, want (no tools called)", fb.Investigated)
	}
}

func TestHeadTailTruncate_Short(t *testing.T) {
	t.Parallel()
	s := "hello"
	got := headTailTruncate(s, 256)
	if got != s {
		t.Errorf("headTailTruncate(%q, 256) = %q, want identity", s, got)
	}
}

func TestHeadTailTruncate_Long(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 500)
	got := headTailTruncate(long, 100)
	if len(got) > 100+5 { // tiny margin for the separator
		t.Errorf("result too long: %d bytes", len(got))
	}
	if !strings.Contains(got, "…") {
		t.Error("expected separator in truncated result")
	}
}

func TestHeadTailTruncate_UTF8(t *testing.T) {
	t.Parallel()
	// 3-byte UTF-8 rune (€ = 0xE2 0x82 0xAC).
	s := strings.Repeat("€", 100)
	got := headTailTruncate(s, 50)
	if !strings.Contains(got, "…") {
		t.Error("expected separator")
	}
	// Result must be valid UTF-8.
	for i, r := range got {
		if r == '�' {
			t.Errorf("invalid UTF-8 replacement rune at position %d", i)
			break
		}
	}
}

func TestToolNarrative_String(t *testing.T) {
	t.Parallel()
	n := ToolNarrative{
		Tool: "bash", ArgsSummary: "ls", ResultSummary: "file.go", DurationMs: 3, Success: true,
	}
	s := n.String()
	if !strings.Contains(s, "bash") || !strings.Contains(s, "ok") {
		t.Errorf("String() = %q, expected tool name and status", s)
	}
}
