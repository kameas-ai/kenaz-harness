package toolloop_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/tools/sleep"
)

// TestIsPassiveTool verifies that kenaz__sleep is recognised as passive
// and that a normal tool name is not.
func TestIsPassiveTool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		toolName string
		want     bool
	}{
		{"sleep_is_passive", sleep.ToolName, true},
		{"bash_is_active", "kenaz__bash", false},
		{"web_search_is_active", "kenaz__web_search", false},
		{"empty_is_active", "", false},
		{"arbitrary_mcp_is_active", "filesystem__write_file", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toolloop.IsPassiveTool(tc.toolName); got != tc.want {
				t.Errorf("IsPassiveTool(%q) = %v want %v", tc.toolName, got, tc.want)
			}
		})
	}
}

// TestShouldCountIteration is the affirmative complement of TestIsPassiveTool.
func TestShouldCountIteration(t *testing.T) {
	t.Parallel()

	if toolloop.ShouldCountIteration(sleep.ToolName) {
		t.Errorf("ShouldCountIteration(%q) = true; sleep must not consume an iteration", sleep.ToolName)
	}
	if !toolloop.ShouldCountIteration("kenaz__bash") {
		t.Error("ShouldCountIteration(kenaz__bash) = false; bash must consume an iteration")
	}
}

// TestIterCounter_AdvanceIfActive is the core WP04 acceptance criterion:
// calling kenaz__sleep through AdvanceIfActive does NOT increment the
// counter; calling any other tool does.
func TestIterCounter_AdvanceIfActive(t *testing.T) {
	t.Parallel()

	var counter toolloop.IterCounter

	// Non-passive tool increments.
	if advanced := counter.AdvanceIfActive("kenaz__bash"); !advanced {
		t.Error("bash call should advance counter")
	}
	if counter.Value() != 1 {
		t.Errorf("counter = %d want 1 after bash call", counter.Value())
	}

	// Passive tool (sleep) does NOT increment.
	if advanced := counter.AdvanceIfActive(sleep.ToolName); advanced {
		t.Error("sleep call should NOT advance counter")
	}
	if counter.Value() != 1 {
		t.Errorf("counter = %d want 1 after sleep call (should be unchanged)", counter.Value())
	}

	// Another non-passive call increments again.
	counter.AdvanceIfActive("kenaz__web_search")
	if counter.Value() != 2 {
		t.Errorf("counter = %d want 2 after web_search call", counter.Value())
	}

	// Additional sleep calls leave the counter at 2.
	counter.AdvanceIfActive(sleep.ToolName)
	counter.AdvanceIfActive(sleep.ToolName)
	if counter.Value() != 2 {
		t.Errorf("counter = %d want 2 after two more sleep calls", counter.Value())
	}
}

// TestIterCounter_Reset verifies the Reset helper clears the counter.
func TestIterCounter_Reset(t *testing.T) {
	t.Parallel()

	var counter toolloop.IterCounter
	counter.AdvanceIfActive("kenaz__bash")
	counter.AdvanceIfActive("kenaz__bash")
	if counter.Value() != 2 {
		t.Fatalf("counter = %d want 2 before reset", counter.Value())
	}
	counter.Reset()
	if counter.Value() != 0 {
		t.Errorf("counter = %d want 0 after reset", counter.Value())
	}
}

// TestBuiltinPool_SleepDoesNotCountIterations wires a BuiltinPool
// containing the real sleep.Tool and a stub bash tool, then dispatches
// both and asserts that only the bash call increments the IterCounter.
//
// This tests the end-to-end dispatch path: BuiltinPool.Call → sleep.Tool.Call
// → iteration gate check.
func TestBuiltinPool_SleepDoesNotCountIterations(t *testing.T) {
	t.Parallel()

	// Use a short 1s sleep and a context with a tight deadline to avoid
	// waiting a full second in CI; we cancel immediately after the pool call
	// to abort the timer.
	registry := toolloop.NewBuiltinRegistry()

	// Register the real sleep tool.
	sleepTool := &passthroughBuiltin{name: sleep.ToolName}
	registry.Register(sleepTool)

	// Register a stub bash-like tool.
	bashTool := &passthroughBuiltin{name: "kenaz__bash"}
	registry.Register(bashTool)

	pool := toolloop.NewBuiltinPool(nil, registry)

	var counter toolloop.IterCounter

	// Dispatch bash — should increment.
	_, err := pool.Call(context.Background(), toolloop.BuiltinServerName, "bash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("bash call error: %v", err)
	}
	counter.AdvanceIfActive("kenaz__bash")

	if counter.Value() != 1 {
		t.Errorf("counter after bash = %d want 1", counter.Value())
	}

	// Dispatch sleep — should NOT increment.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the sleep tool returns right away
	_, err = pool.Call(ctx, toolloop.BuiltinServerName, "sleep", json.RawMessage(`{"seconds":60}`))
	if err != nil {
		t.Fatalf("sleep call error: %v", err)
	}
	counter.AdvanceIfActive(sleep.ToolName)

	if counter.Value() != 1 {
		t.Errorf("counter after sleep = %d want 1 (sleep must not consume an iteration)", counter.Value())
	}
}

// passthroughBuiltin is a minimal BuiltinTool stub that echoes its args
// back. Used in the pool integration test.
type passthroughBuiltin struct {
	name string
}

func (s *passthroughBuiltin) Name() string                  { return s.name }
func (s *passthroughBuiltin) Description() string           { return "test stub" }
func (s *passthroughBuiltin) InputSchema() json.RawMessage  { return json.RawMessage(`{}`) }
func (s *passthroughBuiltin) Call(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	return args, nil
}
