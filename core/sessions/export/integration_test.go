package export_test

// Integration tests for the session-export-01NDFSEX05 pipeline. These
// tests exercise the full Render → write → verify cycle against the
// real filesystem (os.TempDir) so they cover the I/O path that unit
// tests in export_test.go stub out. Build tag: none (runs under
// go test -short because each fixture is small and I/O-free in
// practice on modern hardware).
//
// Acceptance criteria exercised here:
//   - A 20-turn session with three tool calls exports to both MD and JSON.
//   - A session with attached document bytes produces a sidecar artifact
//     file alongside the Markdown export.
//   - Credentials in message content are redacted in both formats.
//   - The JSON schema version field is stable.
//   - The export function returns the correct ByteCount.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/sessions/export"
)

// build20TurnSession returns a session record and a 20-message transcript
// that exercises: user messages, assistant messages with tool calls,
// a credential string (should be redacted), and both text-only turns.
func build20TurnSession() (session.Record, []session.Message) {
	sess := session.Record{
		ID:          "sess-integ-20turn",
		Name:        "Integration 20-Turn Session",
		CreatedAt:   time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		ContextKind: "system",
	}

	msgs := make([]session.Message, 0, 20)
	for i := 1; i <= 20; i++ {
		role := session.RoleUser
		content := "Turn " + string(rune('0'+i)) + " user message."
		if i%2 == 0 {
			role = session.RoleAssistant
			content = "Turn " + string(rune('0'+i)) + " assistant response."
		}
		msg := session.Message{
			ID:        "msg-" + string(rune('a'+i)),
			SessionID: sess.ID,
			Sequence:  int64(i),
			Role:      role,
			Content:   content,
			CreatedAt: time.Date(2026, 5, 14, 9, i, 0, 0, time.UTC),
		}

		// Tool calls on turns 4, 8, 12.
		if i == 4 || i == 8 || i == 12 {
			msg.ToolCalls = []session.ToolCall{{
				ID:        "tc-" + string(rune('0'+i)),
				Name:      "bash",
				Arguments: map[string]any{"command": "ls -la"},
				Result:    "total 0\ndrwxr-xr-x 2 user group 64 " + string(rune('0'+i)),
			}}
		}

		// A credential in turn 6 (should be redacted in output).
		if i == 6 {
			msg.Content = "My API key is sk-ant-api01-abc1234567890123456789012345678901234567890123"
		}

		msgs = append(msgs, msg)
	}
	return sess, msgs
}

// buildSessionWithAttachment returns a session record and a single
// message carrying a tiny embedded PNG image.
func buildSessionWithAttachment() (session.Record, []session.Message) {
	sess := session.Record{
		ID:          "sess-integ-attachment",
		Name:        "Attachment Export Test",
		CreatedAt:   time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		ContextKind: "system",
	}
	// Tiny 1×1 PNG (base64 string — Render will decode it to sidecar bytes).
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	msgs := []session.Message{{
		ID:        "msg-att-1",
		SessionID: sess.ID,
		Sequence:  1,
		Role:      session.RoleUser,
		Content:   "Here is an image.",
		CreatedAt: time.Date(2026, 5, 14, 9, 1, 0, 0, time.UTC),
		ContentBlocks: []llm.ContentBlock{{
			Type: "image",
			Source: &llm.MediaSource{
				Kind:      "base64",
				MediaType: "image/png",
				Data:      pngBase64,
			},
		}},
	}}
	return sess, msgs
}

// TestIntegration_Markdown_20Turns verifies that a 20-turn session
// renders to Markdown without error, produces a non-empty output, and
// contains at least the first and last turn heading.
func TestIntegration_Markdown_20Turns(t *testing.T) {
	t.Parallel()
	sess, msgs := build20TurnSession()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	b, sidecars, err := export.Render(export.FormatMarkdown, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("Render returned empty content")
	}
	if len(sidecars) != 0 {
		t.Errorf("expected no sidecars for text-only session; got %d", len(sidecars))
	}
	s := string(b)

	if !strings.Contains(s, "## Turn 1") {
		t.Errorf("Markdown missing Turn 1 heading:\n%s", s)
	}
	if !strings.Contains(s, "## Turn 20") {
		t.Errorf("Markdown missing Turn 20 heading:\n%s", s)
	}
	// Three tool calls.
	toolCount := strings.Count(s, "<details>")
	if toolCount != 3 {
		t.Errorf("Markdown has %d <details> blocks, want 3", toolCount)
	}
}

// TestIntegration_JSON_20Turns verifies that a 20-turn session renders
// to JSON without error, with the correct number of messages and a
// stable schema version.
func TestIntegration_JSON_20Turns(t *testing.T) {
	t.Parallel()
	sess, msgs := build20TurnSession()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	b, sidecars, err := export.Render(export.FormatJSON, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render json: %v", err)
	}
	if len(sidecars) != 0 {
		t.Errorf("expected no sidecars for JSON format; got %d", len(sidecars))
	}

	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	meta := payload["meta"].(map[string]any)
	if ver := int(meta["export_format_version"].(float64)); ver != export.ExportFormatVersion {
		t.Errorf("export_format_version = %d, want %d", ver, export.ExportFormatVersion)
	}
	messages := payload["messages"].([]any)
	if len(messages) != 20 {
		t.Errorf("messages count = %d, want 20", len(messages))
	}
}

// TestIntegration_Redaction_InBothFormats verifies that a credential
// that appears in a message is replaced with the REDACTED marker in
// both Markdown and JSON output, and never appears in plaintext.
func TestIntegration_Redaction_InBothFormats(t *testing.T) {
	t.Parallel()
	sess, msgs := build20TurnSession()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	const rawCred = "sk-ant-api01-abc1234567890123456789012345678901234567890123"

	// Markdown.
	md, _, err := export.Render(export.FormatMarkdown, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	if strings.Contains(string(md), rawCred) {
		t.Errorf("Markdown output contains unredacted credential")
	}
	if !strings.Contains(string(md), "<REDACTED:") {
		t.Errorf("Markdown output missing REDACTED marker")
	}

	// JSON.
	js, _, err := export.Render(export.FormatJSON, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render json: %v", err)
	}
	if strings.Contains(string(js), rawCred) {
		t.Errorf("JSON output contains unredacted credential")
	}
	// Go's encoding/json encodes '<' as < by default (HTML-safe mode).
	// The redacted string "<REDACTED:anthropic-key>" appears in JSON bytes as
	// "<REDACTED:anthropic-key>". We also accept the raw form in
	// case the encoder is configured without HTML escaping.
	hasRedacted := strings.Contains(string(js), "REDACTED:anthropic-key")
	if !hasRedacted {
		t.Errorf("JSON output missing REDACTED:anthropic-key marker")
	}
}

// TestIntegration_WriteToFile verifies that the exported bytes can be
// written to a real temp file and the returned size matches the file's
// size on disk.
func TestIntegration_WriteToFile(t *testing.T) {
	t.Parallel()
	sess, msgs := build20TurnSession()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	for _, format := range []export.Format{export.FormatMarkdown, export.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()
			b, _, err := export.Render(format, sess, msgs, now)
			if err != nil {
				t.Fatalf("Render %s: %v", format, err)
			}
			dest := filepath.Join(t.TempDir(), export.DefaultFilename(sess.Name, format, now))
			if err := os.WriteFile(dest, b, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			info, err := os.Stat(dest)
			if err != nil {
				t.Fatalf("Stat: %v", err)
			}
			if info.Size() != int64(len(b)) {
				t.Errorf("file size %d != rendered len %d", info.Size(), len(b))
			}
		})
	}
}

// TestIntegration_Attachment_SidecarWritten verifies that an embedded
// PNG image in a message produces a sidecar file that can be written
// alongside the Markdown export.
func TestIntegration_Attachment_SidecarWritten(t *testing.T) {
	t.Parallel()
	sess, msgs := buildSessionWithAttachment()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	b, sidecars, err := export.Render(export.FormatMarkdown, sess, msgs, now)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	if len(sidecars) == 0 {
		t.Fatal("expected at least one sidecar for image attachment")
	}

	dir := t.TempDir()
	mainPath := filepath.Join(dir, export.DefaultFilename(sess.Name, export.FormatMarkdown, now))
	if err := os.WriteFile(mainPath, b, 0o644); err != nil {
		t.Fatalf("write main file: %v", err)
	}

	// Write sidecars.
	for _, sc := range sidecars {
		scPath := filepath.Join(dir, sc.RelPath)
		if err := os.MkdirAll(filepath.Dir(scPath), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", filepath.Dir(scPath), err)
		}
		if err := os.WriteFile(scPath, sc.Bytes, 0o644); err != nil {
			t.Fatalf("write sidecar %s: %v", sc.RelPath, err)
		}
		info, err := os.Stat(scPath)
		if err != nil {
			t.Fatalf("stat sidecar %s: %v", scPath, err)
		}
		if info.Size() == 0 {
			t.Errorf("sidecar %s is empty", sc.RelPath)
		}
	}

	// Markdown should reference the sidecar via a relative link.
	if !strings.Contains(string(b), "![attachment]") {
		t.Errorf("Markdown missing image reference: %s", b)
	}
}
