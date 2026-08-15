package export

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

func makeTestSession() session.Record {
	return session.Record{
		ID:          "sess-test-1",
		Name:        "My Test Session",
		CreatedAt:   time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		SystemPrompt: "",
		ContextKind: "system",
	}
}

func makeTestMessages() []session.Message {
	return []session.Message{
		{
			ID:        "msg-1",
			SessionID: "sess-test-1",
			Sequence:  1,
			Role:      session.RoleUser,
			Content:   "Hello, how are you?",
			CreatedAt: time.Date(2026, 5, 14, 9, 1, 0, 0, time.UTC),
		},
		{
			ID:        "msg-2",
			SessionID: "sess-test-1",
			Sequence:  2,
			Role:      session.RoleAssistant,
			Content:   "I'm doing well, thank you!",
			CreatedAt: time.Date(2026, 5, 14, 9, 2, 0, 0, time.UTC),
			ToolCalls: []session.ToolCall{
				{
					ID:        "tc-1",
					Name:      "bash",
					Arguments: map[string]any{"command": "ls -la"},
					Result:    "total 0\ndrwxr-xr-x  2 user group  64 May 14 09:02 .",
				},
			},
		},
	}
}

func TestRender_Markdown_Golden(t *testing.T) {
	t.Parallel()
	sess := makeTestSession()
	msgs := makeTestMessages()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	b, sidecars, err := Render(FormatMarkdown, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	if len(sidecars) != 0 {
		t.Errorf("expected 0 sidecars for text-only session, got %d", len(sidecars))
	}
	s := string(b)

	// H1 title.
	if !strings.Contains(s, "# My Test Session") {
		t.Errorf("Markdown missing H1 title:\n%s", s)
	}
	// Meta header fields.
	if !strings.Contains(s, "session_id: sess-test-1") {
		t.Errorf("Markdown missing session_id:\n%s", s)
	}
	if !strings.Contains(s, fmt.Sprintf("export_format_version: %d", ExportFormatVersion)) {
		t.Errorf("Markdown missing export_format_version:\n%s", s)
	}
	if !strings.Contains(s, "exported_at:") {
		t.Errorf("Markdown missing exported_at:\n%s", s)
	}
	// Turn headings.
	if !strings.Contains(s, "## Turn 1") {
		t.Errorf("Markdown missing Turn 1 heading:\n%s", s)
	}
	if !strings.Contains(s, "## Turn 2") {
		t.Errorf("Markdown missing Turn 2 heading:\n%s", s)
	}
	// Message content.
	if !strings.Contains(s, "Hello, how are you?") {
		t.Errorf("Markdown missing user message:\n%s", s)
	}
	if !strings.Contains(s, "I'm doing well, thank you!") {
		t.Errorf("Markdown missing assistant message:\n%s", s)
	}
	// Tool call as <details>.
	if !strings.Contains(s, "<details>") {
		t.Errorf("Markdown missing tool-call <details>:\n%s", s)
	}
	if !strings.Contains(s, "bash") {
		t.Errorf("Markdown missing tool name 'bash':\n%s", s)
	}
}

func TestRender_JSON_Golden(t *testing.T) {
	t.Parallel()
	sess := makeTestSession()
	msgs := makeTestMessages()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	b, sidecars, err := Render(FormatJSON, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render json: %v", err)
	}
	if len(sidecars) != 0 {
		t.Errorf("expected 0 sidecars for JSON format, got %d", len(sidecars))
	}

	var payload jsonExport
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	// Schema version.
	if payload.Meta.ExportFormatVersion != ExportFormatVersion {
		t.Errorf("ExportFormatVersion = %d, want %d", payload.Meta.ExportFormatVersion, ExportFormatVersion)
	}
	// Session fields.
	if payload.Session.ID != "sess-test-1" {
		t.Errorf("Session.ID = %q, want %q", payload.Session.ID, "sess-test-1")
	}
	if payload.Session.Name != "My Test Session" {
		t.Errorf("Session.Name = %q, want %q", payload.Session.Name, "My Test Session")
	}
	// Messages.
	if len(payload.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(payload.Messages))
	}
	if payload.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want user", payload.Messages[0].Role)
	}
	if len(payload.Messages[1].ToolCalls) != 1 {
		t.Errorf("Messages[1].ToolCalls len = %d, want 1", len(payload.Messages[1].ToolCalls))
	}
	if payload.Messages[1].ToolCalls[0].Name != "bash" {
		t.Errorf("ToolCalls[0].Name = %q, want bash", payload.Messages[1].ToolCalls[0].Name)
	}
}

func TestRender_JSON_SchemaVersion(t *testing.T) {
	t.Parallel()
	sess := makeTestSession()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	b, _, err := Render(FormatJSON, sess, nil, now)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Verify export_format_version is the first field in meta.
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, ok := raw["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta field not found or wrong type")
	}
	ver, ok := meta["export_format_version"].(float64)
	if !ok {
		t.Fatalf("export_format_version not found or wrong type")
	}
	if int(ver) != ExportFormatVersion {
		t.Errorf("export_format_version = %d, want %d", int(ver), ExportFormatVersion)
	}
}

func TestRedact_CredentialReplaced(t *testing.T) {
	t.Parallel()
	// Anthropic key pattern.
	input := "Here is my key: sk-ant-api01-abc12345678901234567890123456789012345678901"
	got, matches := RedactValue(input)
	if strings.Contains(got, "sk-ant-") {
		t.Errorf("RedactValue did not redact anthropic key; got: %s", got)
	}
	if !strings.Contains(got, "<REDACTED:anthropic-key>") {
		t.Errorf("RedactValue did not produce <REDACTED:...> marker; got: %s", got)
	}
	if len(matches) == 0 {
		t.Errorf("RedactValue returned no matches")
	}
}

func TestRedact_OpenAIKey(t *testing.T) {
	t.Parallel()
	input := "my openai key is sk-abcdefghijklmnopqrstu"
	got, matches := RedactValue(input)
	if strings.Contains(got, "sk-abcdefghijklmnopqrstu") {
		t.Errorf("RedactValue did not redact openai key; got: %s", got)
	}
	if !strings.Contains(got, "<REDACTED:openai-key>") {
		t.Errorf("RedactValue did not produce <REDACTED:...> marker; got: %s", got)
	}
	if len(matches) == 0 {
		t.Errorf("RedactValue returned no matches")
	}
}

func TestRedact_CleanString_NoChange(t *testing.T) {
	t.Parallel()
	input := "This is a normal string with no credentials."
	got, matches := RedactValue(input)
	if got != input {
		t.Errorf("RedactValue modified clean string: %q → %q", input, got)
	}
	if len(matches) != 0 {
		t.Errorf("RedactValue returned matches for clean string: %v", matches)
	}
}

func TestRender_Markdown_WithAttachment(t *testing.T) {
	t.Parallel()
	sess := makeTestSession()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	// Tiny 1x1 PNG (base64 encoded).
	pngBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

	msgs := []session.Message{
		{
			ID:        "msg-img",
			SessionID: "sess-test-1",
			Sequence:  1,
			Role:      session.RoleUser,
			Content:   "Here is an image:",
			CreatedAt: now,
			ContentBlocks: []llm.ContentBlock{
				{
					Type: "image",
					Source: &llm.MediaSource{
						Kind:      "base64",
						MediaType: "image/png",
						Data:      pngBase64,
					},
				},
			},
		},
	}

	b, sidecars, err := Render(FormatMarkdown, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(sidecars) != 1 {
		t.Fatalf("expected 1 sidecar for image attachment, got %d", len(sidecars))
	}
	if !strings.HasSuffix(sidecars[0].RelPath, ".png") {
		t.Errorf("sidecar RelPath should end with .png; got %q", sidecars[0].RelPath)
	}
	if !strings.Contains(string(b), "![attachment]") {
		t.Errorf("Markdown missing image link:\n%s", b)
	}
}

func TestDefaultFilename(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		title  string
		format Format
		want   string
	}{
		{"My Session", FormatMarkdown, "my-session-2026-05-14.md"},
		{"My Session", FormatJSON, "my-session-2026-05-14.json"},
		{"", FormatMarkdown, "session-2026-05-14.md"},
		{"Hello World!", FormatJSON, "hello-world-2026-05-14.json"},
	}
	for _, tc := range cases {
		got := DefaultFilename(tc.title, tc.format, date)
		if got != tc.want {
			t.Errorf("DefaultFilename(%q, %q) = %q, want %q", tc.title, tc.format, got, tc.want)
		}
	}
}
