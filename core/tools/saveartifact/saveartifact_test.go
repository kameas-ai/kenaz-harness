package saveartifact

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// recordingManager captures calls to Capture so tests assert the wire
// shape passed in. Returns canned artifacts on success.
type recordingManager struct {
	calls []captureCall
	out   []coreart.Artifact
	err   error
}

type captureCall struct {
	candidates []coreart.CaptureCandidate
	sessionID  string
}

func (m *recordingManager) Capture(_ context.Context, c []coreart.CaptureCandidate, sessionID string) ([]coreart.Artifact, error) {
	m.calls = append(m.calls, captureCall{candidates: c, sessionID: sessionID})
	if m.err != nil {
		return nil, m.err
	}
	return m.out, nil
}

func newSuccessManager(id, title, mime string, size int64) *recordingManager {
	return &recordingManager{
		out: []coreart.Artifact{{
			ID:       id,
			Title:    title,
			MimeType: mime,
			ByteSize: size,
		}},
	}
}

func TestTool_SuccessPath(t *testing.T) {
	mgr := newSuccessManager("01HKQ123", "Q4-summary.md", "text/markdown; charset=utf-8", 42)
	tool := New(Options{Manager: mgr})
	ctx := toolloop.WithSessionID(context.Background(), "sess-abc")

	args := json.RawMessage(`{"title":"Q4-summary.md","content":"# Q4 results\n42 widgets sold"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var got successResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal success: %v", err)
	}
	if got.ArtifactID != "01HKQ123" {
		t.Errorf("artifact_id = %q, want 01HKQ123", got.ArtifactID)
	}
	if got.Title != "Q4-summary.md" {
		t.Errorf("title = %q, want Q4-summary.md", got.Title)
	}
	if got.Size != 42 {
		t.Errorf("size = %d, want 42", got.Size)
	}

	if len(mgr.calls) != 1 {
		t.Fatalf("expected 1 capture call, got %d", len(mgr.calls))
	}
	call := mgr.calls[0]
	if call.sessionID != "sess-abc" {
		t.Errorf("session id = %q, want sess-abc", call.sessionID)
	}
	if len(call.candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(call.candidates))
	}
	c := call.candidates[0]
	if c.Source != coreart.SourceToolOutput {
		t.Errorf("source = %q, want %q", c.Source, coreart.SourceToolOutput)
	}
	if c.SourceRef.MessageID != "tool:"+ToolName {
		t.Errorf("source_ref.message_id = %q, want tool:%s", c.SourceRef.MessageID, ToolName)
	}
	if c.MimeType != "text/markdown; charset=utf-8" {
		// extension-detection on .md is platform-dependent on first run.
		// At minimum it must not be the default text/plain fallback —
		// the .md extension was visible. Skip if neither markdown
		// nor "text/markdown" registered on this host.
		if !strings.HasPrefix(c.MimeType, "text/") {
			t.Errorf("mime_type = %q, want text/* via .md extension", c.MimeType)
		}
	}
	if string(c.Bytes) != "# Q4 results\n42 widgets sold" {
		t.Errorf("bytes mismatch: %q", string(c.Bytes))
	}
}

func TestTool_ExplicitMimeWins(t *testing.T) {
	mgr := newSuccessManager("01H", "data.bin", "application/x-foo", 5)
	tool := New(Options{Manager: mgr})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"title":"data.bin","content":"hello","mime_type":"application/x-foo"}`)
	if _, err := tool.Call(ctx, args); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := mgr.calls[0].candidates[0].MimeType; got != "application/x-foo" {
		t.Errorf("mime type = %q, want application/x-foo", got)
	}
}

func TestTool_DefaultMimeFallback(t *testing.T) {
	mgr := newSuccessManager("01H", "untitled", "", 5)
	tool := New(Options{Manager: mgr})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"title":"untitled","content":"hello"}`)
	if _, err := tool.Call(ctx, args); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := mgr.calls[0].candidates[0].MimeType; got != DefaultMimeType {
		t.Errorf("mime type = %q, want %q", got, DefaultMimeType)
	}
}

func TestTool_DisabledShortCircuits(t *testing.T) {
	mgr := &recordingManager{}
	tool := New(Options{
		Manager: mgr,
		Enabled: func() bool { return false },
	})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"title":"x","content":"y"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if got.Error != errKindDisabled {
		t.Errorf("error kind = %q, want %q", got.Error, errKindDisabled)
	}
	if len(mgr.calls) != 0 {
		t.Errorf("manager called despite disabled: %d times", len(mgr.calls))
	}
}

func TestTool_ContentTooLarge(t *testing.T) {
	mgr := &recordingManager{}
	tool := New(Options{Manager: mgr})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	big := strings.Repeat("a", MaxContentBytes+1)
	args, _ := json.Marshal(saveArtifactArgs{Title: "huge.txt", Content: big})

	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if got.Error != errKindContentTooLarge {
		t.Errorf("error kind = %q, want %q", got.Error, errKindContentTooLarge)
	}
	if len(mgr.calls) != 0 {
		t.Errorf("manager called despite oversize: %d times", len(mgr.calls))
	}
}

func TestTool_InvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{"empty title", `{"title":"","content":"hi"}`},
		{"whitespace-only title", `{"title":"   ","content":"hi"}`},
		{"empty content", `{"title":"x","content":""}`},
		{"malformed json", `not json`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &recordingManager{}
			tool := New(Options{Manager: mgr})
			ctx := toolloop.WithSessionID(context.Background(), "sess-1")

			out, err := tool.Call(ctx, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			var got errorResult
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal err: %v", err)
			}
			if got.Error != errKindInvalidArgs {
				t.Errorf("error kind = %q, want %q", got.Error, errKindInvalidArgs)
			}
			if len(mgr.calls) != 0 {
				t.Errorf("manager called on invalid args")
			}
		})
	}
}

func TestTool_NoSessionInContext(t *testing.T) {
	mgr := &recordingManager{}
	tool := New(Options{Manager: mgr})

	args := json.RawMessage(`{"title":"x","content":"y"}`)
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if got.Error != errKindNoSession {
		t.Errorf("error kind = %q, want %q", got.Error, errKindNoSession)
	}
	if len(mgr.calls) != 0 {
		t.Errorf("manager called without session")
	}
}

func TestTool_CaptureFailure(t *testing.T) {
	mgr := &recordingManager{err: errors.New("media store full")}
	tool := New(Options{Manager: mgr})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"title":"x","content":"y"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if got.Error != errKindCaptureFailed {
		t.Errorf("error kind = %q, want %q", got.Error, errKindCaptureFailed)
	}
	if !strings.Contains(got.Message, "media store full") {
		t.Errorf("error message should include underlying err, got %q", got.Message)
	}
}

func TestTool_LongTitleRejected(t *testing.T) {
	mgr := &recordingManager{}
	tool := New(Options{Manager: mgr})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	tooLong := strings.Repeat("x", MaxTitleRunes+1)
	args, _ := json.Marshal(saveArtifactArgs{Title: tooLong, Content: "y"})
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if got.Error != errKindInvalidArgs {
		t.Errorf("error kind = %q, want %q", got.Error, errKindInvalidArgs)
	}
}

func TestTool_NameAndDescription(t *testing.T) {
	mgr := &recordingManager{}
	tool := New(Options{Manager: mgr})

	if tool.Name() != ToolName {
		t.Errorf("Name() = %q, want %q", tool.Name(), ToolName)
	}
	if !strings.HasPrefix(tool.Name(), "kaneaz__") {
		t.Errorf("Name() = %q, must have kaneaz__ prefix", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema.required missing")
	}
	if len(required) != 2 {
		t.Errorf("required len = %d, want 2 (title + content)", len(required))
	}
}

func TestTool_NilManagerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil Manager")
		}
	}()
	_ = New(Options{Manager: nil})
}

// Ensure Tool satisfies the toolloop.BuiltinTool shape at compile time.
// We can't import toolloop.BuiltinTool here without a cycle (toolloop
// doesn't import this package, so no cycle in practice — keep witness).
var _ interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
} = (*Tool)(nil)
