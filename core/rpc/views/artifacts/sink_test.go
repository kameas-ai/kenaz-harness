package artifacts

import (
	"context"
	"sync"
	"testing"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
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
