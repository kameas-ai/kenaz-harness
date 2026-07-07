package rpc

// wf_adapters_test.go — unit tests for the workflow runner adapter layer
// introduced in workflows-finalization-01NWFX01 WP05.
//
// Tests covered:
//   1. wfMCPCallerAdapter happy path — args marshalled, response unwrapped.
//   2. wfMCPCallerAdapter actionable error — "unknown server" → user-facing
//      "MCP server X is not installed — install it from Tools".
//   3. wfMCPCallerAdapter nil pool — returns non-nil error (not a panic).
//   4. translateMCPError — known patterns all produce the actionable string.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
)

// ─── fake MCP pool ────────────────────────────────────────────────────────────

type fakeMCPPool struct {
	// capturedServer/tool/args record the last Call invocation.
	capturedServer string
	capturedTool   string
	capturedArgs   json.RawMessage
	// result is the json.RawMessage to return.
	result json.RawMessage
	// err, if non-nil, is returned instead of result.
	err error
}

func (f *fakeMCPPool) Open(_ context.Context, _ []coremcp.ServerSpec) error { return nil }
func (f *fakeMCPPool) Close(_ context.Context) error                         { return nil }
func (f *fakeMCPPool) Tools(_ context.Context) ([]coremcp.Tool, error)       { return nil, nil }
func (f *fakeMCPPool) Call(_ context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	f.capturedServer = server
	f.capturedTool = tool
	f.capturedArgs = args
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestWFMCPCallerAdapter_HappyPath(t *testing.T) {
	t.Parallel()
	pool := &fakeMCPPool{result: json.RawMessage(`{"ok":true}`)}
	adapter := &wfMCPCallerAdapter{pool: pool}

	got, err := adapter.Call(context.Background(), "slack", "list_channels", map[string]any{"limit": 10})
	if err != nil {
		t.Fatalf("Call: unexpected error: %v", err)
	}
	// Result is JSON object → returned as-is (not a bare string).
	if !strings.Contains(got, `"ok"`) {
		t.Errorf("result=%q does not contain expected key", got)
	}
	if pool.capturedServer != "slack" || pool.capturedTool != "list_channels" {
		t.Errorf("dispatched to %s.%s want slack.list_channels", pool.capturedServer, pool.capturedTool)
	}
	// Args must have been marshalled.
	if pool.capturedArgs == nil {
		t.Error("args not marshalled")
	}
}

func TestWFMCPCallerAdapter_StringResult_Unquoted(t *testing.T) {
	t.Parallel()
	pool := &fakeMCPPool{result: json.RawMessage(`"hello world"`)}
	adapter := &wfMCPCallerAdapter{pool: pool}

	got, err := adapter.Call(context.Background(), "fs", "read_file", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// JSON string result must be unquoted.
	if got != "hello world" {
		t.Errorf("result=%q want %q", got, "hello world")
	}
}

func TestWFMCPCallerAdapter_UnknownServer_ActionableError(t *testing.T) {
	t.Parallel()
	pool := &fakeMCPPool{err: errors.New("stdio: unknown server \"slack\"")}
	adapter := &wfMCPCallerAdapter{pool: pool}

	_, err := adapter.Call(context.Background(), "slack", "send_message", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "slack") {
		t.Errorf("error %q does not name the server", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "install") {
		t.Errorf("error %q does not contain remediation hint", msg)
	}
}

func TestWFMCPCallerAdapter_OtherError_Passthrough(t *testing.T) {
	t.Parallel()
	pool := &fakeMCPPool{err: errors.New("connection reset")}
	adapter := &wfMCPCallerAdapter{pool: pool}

	_, err := adapter.Call(context.Background(), "github", "list_prs", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("expected original error, got: %v", err)
	}
}

func TestWFMCPCallerAdapter_NilPool_Error(t *testing.T) {
	t.Parallel()
	adapter := &wfMCPCallerAdapter{pool: nil}

	_, err := adapter.Call(context.Background(), "slack", "send", nil)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestTranslateMCPError_KnownPatterns(t *testing.T) {
	t.Parallel()
	patterns := []string{
		"stdio: unknown server \"slack\"",
		"server not in pool",
		"server not found",
	}
	for _, p := range patterns {
		err := translateMCPError("slack", errors.New(p))
		if err == nil {
			t.Errorf("pattern %q: expected actionable error, got nil", p)
			continue
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "install") {
			t.Errorf("pattern %q: actionable error %q lacks install hint", p, err.Error())
		}
	}
}

func TestTranslateMCPError_NilPassthrough(t *testing.T) {
	if err := translateMCPError("any", nil); err != nil {
		t.Errorf("nil error should pass through as nil, got %v", err)
	}
}

func TestTranslateMCPError_UnknownError_Passthrough(t *testing.T) {
	original := errors.New("some transient network failure")
	got := translateMCPError("slack", original)
	if got != original {
		t.Errorf("unexpected error translation: got %v want %v", got, original)
	}
}
