package fsrequest_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/tools/fsrequest"
)

// stubDelegate is a test double for RequestAdditionalAllowedDir.
type stubDelegate struct {
	granted  bool
	expanded string
	err      error
	// recorded args
	lastRecipeID string
	lastPath     string
	lastReason   string
}

func (s *stubDelegate) RequestAdditionalAllowedDir(_ context.Context, recipeID, path, reason string) (bool, string, error) {
	s.lastRecipeID = recipeID
	s.lastPath = path
	s.lastReason = reason
	return s.granted, s.expanded, s.err
}

func mustTool(delegate fsrequest.RequestAdditionalAllowedDir) *fsrequest.Tool {
	return fsrequest.New(fsrequest.Options{Delegate: delegate})
}

func unmarshalOutput(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return m
}

func TestTool_Name(t *testing.T) {
	t.Parallel()
	tool := mustTool(&stubDelegate{})
	if tool.Name() != fsrequest.ToolName {
		t.Errorf("Name() = %q; want %q", tool.Name(), fsrequest.ToolName)
	}
}

func TestTool_InputSchema_Valid(t *testing.T) {
	t.Parallel()
	tool := mustTool(&stubDelegate{})
	schema := tool.InputSchema()
	if len(schema) == 0 {
		t.Fatal("InputSchema() returned empty")
	}
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}
}

func TestTool_Call_HappyPath(t *testing.T) {
	t.Parallel()
	delegate := &stubDelegate{
		granted:  true,
		expanded: "/Users/alice/docs",
	}
	tool := mustTool(delegate)

	args, _ := json.Marshal(map[string]string{
		"path":   "/Users/alice/docs",
		"reason": "need to read project files",
	})
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	out := unmarshalOutput(t, raw)
	if granted, _ := out["granted"].(bool); !granted {
		t.Errorf("granted = false; want true")
	}
	if ep, _ := out["expanded_path"].(string); ep != "/Users/alice/docs" {
		t.Errorf("expanded_path = %q; want %q", ep, "/Users/alice/docs")
	}
	if delegate.lastPath != "/Users/alice/docs" {
		t.Errorf("delegate received path %q; want %q", delegate.lastPath, "/Users/alice/docs")
	}
	if delegate.lastReason != "need to read project files" {
		t.Errorf("delegate received reason %q", delegate.lastReason)
	}
}

func TestTool_Call_DenyPath(t *testing.T) {
	t.Parallel()
	delegate := &stubDelegate{
		granted:  false,
		expanded: "",
	}
	tool := mustTool(delegate)

	args, _ := json.Marshal(map[string]string{
		"path":   "/Users/alice/docs",
		"reason": "testing deny flow",
	})
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	out := unmarshalOutput(t, raw)
	if granted, _ := out["granted"].(bool); granted {
		t.Error("granted = true; want false")
	}
}

func TestTool_Call_DelegateDeniedPrefixPath(t *testing.T) {
	t.Parallel()
	// Simulate the delegate rejecting the path (pre-prompt denial by the
	// backend, e.g. deny-list hit).
	delegate := &stubDelegate{
		granted:  false,
		expanded: "",
		err:      errors.New("path rejected before prompt: path is in deny-list"),
	}
	tool := mustTool(delegate)

	args, _ := json.Marshal(map[string]string{
		"path":   "/etc/passwd",
		"reason": "trying to access etc",
	})
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned Go error: %v", err)
	}
	out := unmarshalOutput(t, raw)
	if granted, _ := out["granted"].(bool); granted {
		t.Error("granted = true for denied path; want false")
	}
	if msg, _ := out["message"].(string); msg == "" {
		t.Error("message is empty; want an error message")
	}
}

func TestTool_Call_MissingPath(t *testing.T) {
	t.Parallel()
	tool := mustTool(&stubDelegate{})
	args, _ := json.Marshal(map[string]string{"reason": "no path provided"})
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned Go error: %v", err)
	}
	out := unmarshalOutput(t, raw)
	if errKind, _ := out["error"].(string); errKind != "invalid_args" {
		t.Errorf("error = %q; want %q", errKind, "invalid_args")
	}
}

func TestTool_Call_MissingReason(t *testing.T) {
	t.Parallel()
	tool := mustTool(&stubDelegate{})
	args, _ := json.Marshal(map[string]string{"path": "/tmp/foo"})
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned Go error: %v", err)
	}
	out := unmarshalOutput(t, raw)
	if errKind, _ := out["error"].(string); errKind != "invalid_args" {
		t.Errorf("error = %q; want %q", errKind, "invalid_args")
	}
}

func TestTool_Call_EmptyArgs(t *testing.T) {
	t.Parallel()
	tool := mustTool(&stubDelegate{})
	raw, err := tool.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call returned Go error: %v", err)
	}
	out := unmarshalOutput(t, raw)
	if errKind, _ := out["error"].(string); errKind != "invalid_args" {
		t.Errorf("error = %q; want %q", errKind, "invalid_args")
	}
}

func TestNew_PanicsOnNilDelegate(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("New with nil delegate should panic")
		}
	}()
	fsrequest.New(fsrequest.Options{Delegate: nil})
}
