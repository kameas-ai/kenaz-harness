package workflows

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

type sessionRecordingCaller struct{ got []string }

func (c *sessionRecordingCaller) Call(ctx context.Context, _, _ string, _ map[string]any) (string, error) {
	c.got = append(c.got, toolloop.SessionIDFromContext(ctx))
	return "ok", nil
}

// TestMCPCallRunner_ParentSessionCannotOverrideCtxSession (WP02 re-review):
// a run's ParentSessionID may fill an empty ctx but never replace a session
// the ctx already carries — the H1 override class, closed here before WP17
// makes this path model-reachable.
func TestMCPCallRunner_ParentSessionCannotOverrideCtxSession(t *testing.T) {
	caller := &sessionRecordingCaller{}
	r := mcpCallRunner{mcp: caller}
	st := Step{Name: "s", Server: "kenaz", ToolName: "sleep"}

	if _, err := r.Run(context.Background(), st, &RunContext{ParentSessionID: "parent"}); err != nil {
		t.Fatalf("empty ctx: %v", err)
	}
	real := toolloop.WithSessionID(context.Background(), "real")
	if _, err := r.Run(real, st, &RunContext{ParentSessionID: "real"}); err != nil {
		t.Fatalf("matching session: %v", err)
	}
	if _, err := r.Run(real, st, &RunContext{ParentSessionID: "forged"}); !errors.Is(err, toolloop.ErrSessionIDMismatch) {
		t.Fatalf("mismatched session not refused: %v", err)
	}
	if len(caller.got) != 2 || caller.got[0] != "parent" || caller.got[1] != "real" {
		t.Fatalf("dispatched sessions = %v, want [parent real]", caller.got)
	}
}
