package updateartifact

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// recordingUpdater captures calls to WriteVersion and returns canned results.
type recordingUpdater struct {
	calls []writeVersionCall
	out   coreart.ArtifactVersion
	err   error
}

type writeVersionCall struct {
	artifactID string
	bytes      []byte
	mimeType   string
	summary    *string
	path       *string
}

func (u *recordingUpdater) WriteVersion(
	_ context.Context,
	artifactID string,
	bytes []byte,
	mimeType string,
	summary, path *string,
) (coreart.ArtifactVersion, error) {
	u.calls = append(u.calls, writeVersionCall{
		artifactID: artifactID,
		bytes:      bytes,
		mimeType:   mimeType,
		summary:    summary,
		path:       path,
	})
	if u.err != nil {
		return coreart.ArtifactVersion{}, u.err
	}
	return u.out, nil
}

func newSuccessUpdater(artifactID string, version int, mime string, size int64) *recordingUpdater {
	return &recordingUpdater{
		out: coreart.ArtifactVersion{
			ArtifactID: artifactID,
			Version:    version,
			MimeType:   mime,
			ByteSize:   size,
		},
	}
}

// TestTool_SuccessPath verifies a minimal happy-path call.
func TestTool_SuccessPath(t *testing.T) {
	updater := newSuccessUpdater("ART-001", 1, "text/markdown; charset=utf-8", 42)
	tool := New(Options{Updater: updater})
	ctx := toolloop.WithSessionID(context.Background(), "sess-abc")

	args := json.RawMessage(`{"artifact_id":"ART-001","content":"# Revised Q4\n43 widgets sold"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var got successResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal success: %v", err)
	}
	if got.ArtifactID != "ART-001" {
		t.Errorf("artifact_id = %q, want ART-001", got.ArtifactID)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if got.Size != 42 {
		t.Errorf("size = %d, want 42", got.Size)
	}

	if len(updater.calls) != 1 {
		t.Fatalf("expected 1 WriteVersion call, got %d", len(updater.calls))
	}
	call := updater.calls[0]
	if call.artifactID != "ART-001" {
		t.Errorf("artifactID = %q, want ART-001", call.artifactID)
	}
	if string(call.bytes) != "# Revised Q4\n43 widgets sold" {
		t.Errorf("bytes mismatch: %q", string(call.bytes))
	}
	if call.summary != nil {
		t.Errorf("summary = %v, want nil (not supplied)", call.summary)
	}
	if call.path != nil {
		t.Errorf("path = %v, want nil (not supplied)", call.path)
	}
}

// TestTool_WithSummaryAndPath verifies optional fields are forwarded.
func TestTool_WithSummaryAndPath(t *testing.T) {
	updater := newSuccessUpdater("ART-002", 2, "text/plain", 10)
	tool := New(Options{Updater: updater})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{
		"artifact_id":"ART-002",
		"content":"updated text",
		"summary":"fixed typo in line 3",
		"path":"/Users/alice/projects/notes.txt"
	}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var got successResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}

	call := updater.calls[0]
	if call.summary == nil || *call.summary != "fixed typo in line 3" {
		t.Errorf("summary = %v, want 'fixed typo in line 3'", call.summary)
	}
	if call.path == nil || *call.path != "/Users/alice/projects/notes.txt" {
		t.Errorf("path = %v, want '/Users/alice/projects/notes.txt'", call.path)
	}
}

// TestTool_ArtifactNotFound verifies ErrArtifactNotFound maps to artifact_not_found.
func TestTool_ArtifactNotFound(t *testing.T) {
	updater := &recordingUpdater{err: coreart.ErrArtifactNotFound}
	tool := New(Options{Updater: updater})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"artifact_id":"MISSING","content":"hi"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal err: %v", err)
	}
	if got.Error != errKindNotFound {
		t.Errorf("error kind = %q, want %q", got.Error, errKindNotFound)
	}
	if !strings.Contains(got.Message, "MISSING") {
		t.Errorf("message should contain artifact id, got %q", got.Message)
	}
}

// TestTool_DisabledShortCircuits verifies the enabled guard fires before WriteVersion.
func TestTool_DisabledShortCircuits(t *testing.T) {
	updater := &recordingUpdater{}
	tool := New(Options{
		Updater: updater,
		Enabled: func() bool { return false },
	})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"artifact_id":"X","content":"y"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error != errKindDisabled {
		t.Errorf("error kind = %q, want %q", got.Error, errKindDisabled)
	}
	if len(updater.calls) != 0 {
		t.Errorf("updater called despite disabled: %d times", len(updater.calls))
	}
}

// TestTool_ContentTooLarge verifies the size cap fires.
func TestTool_ContentTooLarge(t *testing.T) {
	updater := &recordingUpdater{}
	tool := New(Options{Updater: updater})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	big := strings.Repeat("a", MaxContentBytes+1)
	args, _ := json.Marshal(updateArtifactArgs{ArtifactID: "ART-1", Content: big})
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error != errKindContentTooLarge {
		t.Errorf("error kind = %q, want %q", got.Error, errKindContentTooLarge)
	}
	if len(updater.calls) != 0 {
		t.Errorf("updater called despite oversize: %d times", len(updater.calls))
	}
}

// TestTool_InvalidArgs covers the mandatory-field guard.
func TestTool_InvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{"empty artifact_id", `{"artifact_id":"","content":"hi"}`},
		{"whitespace artifact_id", `{"artifact_id":"   ","content":"hi"}`},
		{"empty content", `{"artifact_id":"X","content":""}`},
		{"malformed json", `not json`},
		{"empty args", ``},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updater := &recordingUpdater{}
			tool := New(Options{Updater: updater})
			ctx := toolloop.WithSessionID(context.Background(), "sess-1")

			out, err := tool.Call(ctx, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			var got errorResult
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Error != errKindInvalidArgs {
				t.Errorf("error kind = %q, want %q", got.Error, errKindInvalidArgs)
			}
			if len(updater.calls) != 0 {
				t.Errorf("updater called on invalid args")
			}
		})
	}
}

// TestTool_NoSessionInContext verifies the no-session guard.
func TestTool_NoSessionInContext(t *testing.T) {
	updater := &recordingUpdater{}
	tool := New(Options{Updater: updater})

	// No session stuffed into context.
	args := json.RawMessage(`{"artifact_id":"X","content":"y"}`)
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error != errKindNoSession {
		t.Errorf("error kind = %q, want %q", got.Error, errKindNoSession)
	}
	if len(updater.calls) != 0 {
		t.Errorf("updater called without session")
	}
}

// TestTool_WriteVersionFailure verifies generic updater failures are surfaced.
func TestTool_WriteVersionFailure(t *testing.T) {
	updater := &recordingUpdater{err: errors.New("disk full")}
	tool := New(Options{Updater: updater})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	args := json.RawMessage(`{"artifact_id":"X","content":"y"}`)
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got errorResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error != errKindUpdateFailed {
		t.Errorf("error kind = %q, want %q", got.Error, errKindUpdateFailed)
	}
	if !strings.Contains(got.Message, "disk full") {
		t.Errorf("message should include underlying err, got %q", got.Message)
	}
}

// TestTool_NameAndDescriptionAndSchema verifies the tool metadata.
func TestTool_NameAndDescriptionAndSchema(t *testing.T) {
	updater := &recordingUpdater{}
	tool := New(Options{Updater: updater})

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
		t.Errorf("required len = %d, want 2 (artifact_id + content)", len(required))
	}
}

// TestTool_NilUpdaterPanics verifies the nil guard.
func TestTool_NilUpdaterPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil Updater")
		}
	}()
	_ = New(Options{Updater: nil})
}

// Interface compile-time check.
var _ interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
} = (*Tool)(nil)
