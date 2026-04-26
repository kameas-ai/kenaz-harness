package artifacts

import (
	"context"
	"sync"
	"testing"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// recordingCaptureManager captures every Capture call so the test can
// assert which candidates the sink forwarded and against which
// session_id.
type recordingCaptureManager struct {
	mu        sync.Mutex
	calls     [][]coreart.CaptureCandidate
	sessions  []string
	returnArt []coreart.Artifact
	returnErr error
}

func (m *recordingCaptureManager) Capture(_ context.Context, candidates []coreart.CaptureCandidate, sessionID string) ([]coreart.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, candidates)
	m.sessions = append(m.sessions, sessionID)
	return m.returnArt, m.returnErr
}

func (m *recordingCaptureManager) snapshotCalls() [][]coreart.CaptureCandidate {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]coreart.CaptureCandidate, len(m.calls))
	copy(out, m.calls)
	return out
}

// TestSink_OnAssistantMessage_FiresWhenEnabled — happy path: a fenced
// block with a title hint and ≥ MinBytes content produces one Capture
// call against the supplied session_id.
func TestSink_OnAssistantMessage_FiresWhenEnabled(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{
			AutoCaptureCodeBlocks: true,
			CodeBlockMinLines:     2,
			CodeBlockMinBytes:     10,
		}
	}
	s := NewSink(mgr, cfgFn, nil)
	text := "Here is a file:\n\n```html title=\"x.html\"\n<html>\n<body>\nhello world\n</body>\n</html>\n```\n"
	if err := s.OnAssistantMessage(context.Background(), "sess-1", "msg-1", text); err != nil {
		t.Fatalf("OnAssistantMessage: %v", err)
	}
	calls := mgr.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("Capture calls = %d, want 1", len(calls))
	}
	if len(calls[0]) != 1 {
		t.Fatalf("candidates per call = %d, want 1", len(calls[0]))
	}
	if calls[0][0].Title != "x.html" {
		t.Errorf("title = %q, want x.html", calls[0][0].Title)
	}
	if mgr.sessions[0] != "sess-1" {
		t.Errorf("session id forwarded = %q, want sess-1", mgr.sessions[0])
	}
}

// TestSink_OnAssistantMessage_DisabledShortCircuits — when settings
// disable code-block capture the sink must NOT invoke the manager.
func TestSink_OnAssistantMessage_DisabledShortCircuits(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{AutoCaptureCodeBlocks: false}
	}
	s := NewSink(mgr, cfgFn, nil)
	text := "```html title=\"x.html\"\n<html><body>hello</body></html>\n```\n"
	if err := s.OnAssistantMessage(context.Background(), "sess-1", "m", text); err != nil {
		t.Fatalf("OnAssistantMessage: %v", err)
	}
	if got := len(mgr.snapshotCalls()); got != 0 {
		t.Errorf("Capture calls = %d, want 0 (cfg disabled)", got)
	}
}

// TestSink_OnAssistantMessage_NoBlocksNoOp — no fenced blocks → no
// Capture call.
func TestSink_OnAssistantMessage_NoBlocksNoOp(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{AutoCaptureCodeBlocks: true}
	}
	s := NewSink(mgr, cfgFn, nil)
	if err := s.OnAssistantMessage(context.Background(), "sess-1", "m", "plain text only"); err != nil {
		t.Fatalf("OnAssistantMessage: %v", err)
	}
	if got := len(mgr.snapshotCalls()); got != 0 {
		t.Errorf("Capture calls = %d, want 0", got)
	}
}

// TestSink_OnPostToolUse_ImageEnvelope — image-shape result captured.
func TestSink_OnPostToolUse_ImageEnvelope(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{AutoCaptureToolOutputs: true}
	}
	s := NewSinkConcrete(mgr, cfgFn, nil)
	// "hello" base64 = aGVsbG8=
	ev := toolloop.PostToolUseEvent{
		SessionID:  "sess-2",
		Tool:       "render",
		Server:     "img-srv",
		Result:     []byte(`{"type":"image","data":"aGVsbG8=","mimeType":"image/png"}`),
		ToolCallID: "call-7",
	}
	s.OnPostToolUse(ev)
	calls := mgr.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("Capture calls = %d, want 1", len(calls))
	}
	if len(calls[0]) != 1 || calls[0][0].MimeType != "image/png" {
		t.Errorf("candidate = %+v", calls[0])
	}
	if mgr.sessions[0] != "sess-2" {
		t.Errorf("session id forwarded = %q, want sess-2", mgr.sessions[0])
	}
	if calls[0][0].SourceRef.ToolCallID != "call-7" {
		t.Errorf("ToolCallID forwarded = %q, want call-7", calls[0][0].SourceRef.ToolCallID)
	}
}

// TestSink_OnPostToolUse_DisabledShortCircuits — settings disable.
func TestSink_OnPostToolUse_DisabledShortCircuits(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{AutoCaptureToolOutputs: false}
	}
	s := NewSinkConcrete(mgr, cfgFn, nil)
	ev := toolloop.PostToolUseEvent{
		SessionID: "sess-2",
		Result:    []byte(`{"type":"image","data":"aGVsbG8=","mimeType":"image/png"}`),
	}
	s.OnPostToolUse(ev)
	if got := len(mgr.snapshotCalls()); got != 0 {
		t.Errorf("Capture calls = %d, want 0 (cfg disabled)", got)
	}
}

// TestSink_OnPostToolUse_PlainTextResultSkipped — non-file-shaped
// results are NOT captured.
func TestSink_OnPostToolUse_PlainTextResultSkipped(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{AutoCaptureToolOutputs: true}
	}
	s := NewSinkConcrete(mgr, cfgFn, nil)
	ev := toolloop.PostToolUseEvent{
		SessionID: "sess-2",
		Result:    []byte(`{"text":"hello"}`),
	}
	s.OnPostToolUse(ev)
	if got := len(mgr.snapshotCalls()); got != 0 {
		t.Errorf("Capture calls = %d, want 0", got)
	}
}

// TestSink_PostListener_FansThroughHookRunner — the listener returned
// by PostListener invokes OnPostToolUse, exercising the registration
// path that the rpc chassis uses.
func TestSink_PostListener_FansThroughHookRunner(t *testing.T) {
	t.Parallel()
	mgr := &recordingCaptureManager{}
	cfgFn := func() coreart.CaptureConfig {
		return coreart.CaptureConfig{AutoCaptureToolOutputs: true}
	}
	s := NewSinkConcrete(mgr, cfgFn, nil)
	runner := toolloop.NewNoopHookRunner()
	runner.RegisterPostListener(s.PostListener())
	runner.RunPostToolUse(context.Background(), toolloop.PostToolUseEvent{
		SessionID:  "sess-3",
		Result:     []byte(`{"type":"image","data":"aGVsbG8=","mimeType":"image/png"}`),
		ToolCallID: "c1",
	})
	if got := len(mgr.snapshotCalls()); got != 1 {
		t.Errorf("Capture calls = %d, want 1", got)
	}
}
