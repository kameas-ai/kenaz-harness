package chat

// tool-error-legibility-01PMDL02 WP02: kernelToolAdapter.Call wraps a
// genuine pool.Call error verbatim into an IsError ToolResult
// (kernel_tool_adapter.go ~205-208). This exercises the environment-drift
// classifier wired in on that path: a signature-matched failure gets the
// standard diagnostic suffix appended; an unrelated failure is passed
// through unchanged.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// failingToolPool is a minimal ToolPool whose Call always fails with a
// scripted error, so tests can exercise the callErr wrapping path in
// kernelToolAdapter.Call (as opposed to a tool returning a well-formed
// IsError result, which never reaches that code path).
type failingToolPool struct {
	server, tool string
	errMsg       string
}

func (p *failingToolPool) Tools(_ context.Context) ([]ToolEntry, error) {
	return []ToolEntry{{Server: p.server, Name: p.tool}}, nil
}

func (p *failingToolPool) Call(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	return nil, errors.New(p.errMsg)
}

func TestKernelToolAdapter_Call_EnvironmentDriftHintAppended(t *testing.T) {
	t.Parallel()

	pool := &failingToolPool{server: "myserver", tool: "read_file", errMsg: "open /workspace/gone.txt: no such file or directory"}
	perms := &recordingPermResolver{verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "allow"}}

	adapter := newKernelToolAdapter(pool, perms, "sess-drift")
	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: unexpected error %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false; want true")
	}
	if !strings.Contains(result.Content, "no such file or directory") {
		t.Fatalf("original error text must be preserved verbatim; got %q", result.Content)
	}
	if !strings.Contains(result.Content, "Environment-drift hint:") {
		t.Errorf("expected environment-drift hint appended; got %q", result.Content)
	}
}

func TestKernelToolAdapter_Call_UnrelatedErrorNoHint(t *testing.T) {
	t.Parallel()

	pool := &failingToolPool{server: "myserver", tool: "crashy", errMsg: "panic: index out of range [3] with length 2"}
	perms := &recordingPermResolver{verdict: PermVerdict{Server: "myserver", Tool: "crashy", Policy: "allow"}}

	adapter := newKernelToolAdapter(pool, perms, "sess-nodrift")
	result, err := adapter.Call(context.Background(), makeCall("myserver", "crashy"))
	if err != nil {
		t.Fatalf("Call: unexpected error %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false; want true")
	}
	if result.Content != pool.errMsg {
		t.Errorf("expected unrelated error left unmodified; got %q, want %q", result.Content, pool.errMsg)
	}
}
