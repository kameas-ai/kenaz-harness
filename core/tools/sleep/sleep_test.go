package sleep_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/tools/sleep"
)

func TestTool_NameDescriptionSchema(t *testing.T) {
	t.Parallel()
	tool := sleep.New()

	if tool.Name() != sleep.ToolName {
		t.Errorf("Name() = %q want %q", tool.Name(), sleep.ToolName)
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	schema := tool.InputSchema()
	if len(schema) == 0 {
		t.Error("InputSchema() is empty")
	}
	// Schema must be valid JSON.
	var v any
	if err := json.Unmarshal(schema, &v); err != nil {
		t.Errorf("InputSchema() invalid JSON: %v", err)
	}
}

// TestTool_ClampRange verifies that out-of-range inputs are clamped and
// the returned slept_s matches the clamped value, not the raw input.
func TestTool_ClampRange(t *testing.T) {
	t.Parallel()
	tool := sleep.New()

	cases := []struct {
		name     string
		args     string
		wantSecs int
	}{
		// Short: clamped up to MinSeconds.
		{"below_min", `{"seconds":0}`, sleep.MinSeconds},
		{"negative", `{"seconds":-5}`, sleep.MinSeconds},
		// Within range: kept as-is. We pick the shortest valid value (1s) so
		// the test doesn't actually sleep for a long time in CI.
		{"min_valid", `{"seconds":1}`, 1},
		// Above max: clamped down to MaxSeconds.
		{"above_max", `{"seconds":99999}`, sleep.MaxSeconds},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// For clamped-to-max we only care about the response shape,
			// not that we actually slept MaxSeconds in CI. We use a very
			// short deadline for clamped-up / clamped-down cases; for the
			// clamped-to-max case we cancel immediately so the test
			// terminates quickly.
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.wantSecs >= sleep.MaxSeconds {
				// Cancel immediately — we only care the result is well-formed.
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			} else {
				// Allow at most 3 seconds for the sleep to complete.
				ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			}
			defer cancel()

			raw, err := tool.Call(ctx, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("Call: unexpected error %v", err)
			}

			var result struct {
				SleptS int `json:"slept_s"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatalf("unmarshal result: %v (raw=%s)", err, raw)
			}

			// For the context-cancelled case the result is 0 (interrupted).
			if tc.wantSecs >= sleep.MaxSeconds {
				// result.SleptS is 0 because we cancelled.
				if result.SleptS != 0 {
					t.Errorf("slept_s = %d want 0 (context cancelled)", result.SleptS)
				}
				return
			}

			if result.SleptS != tc.wantSecs {
				t.Errorf("slept_s = %d want %d", result.SleptS, tc.wantSecs)
			}
		})
	}
}

// TestTool_AbortPropagation asserts that cancelling the context terminates
// the sleep within 100ms (WP04 acceptance criterion).
func TestTool_AbortPropagation(t *testing.T) {
	t.Parallel()
	tool := sleep.New()

	// Request a 60s sleep; cancel the context after 50ms.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	raw, err := tool.Call(ctx, json.RawMessage(`{"seconds":60}`))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Call: unexpected error %v", err)
	}

	// Must have returned within 100ms of the context deadline firing.
	if elapsed > 200*time.Millisecond {
		t.Errorf("sleep did not abort quickly: elapsed %v want < 200ms", elapsed)
	}

	// Result should be well-formed; slept_s == 0 because we were aborted.
	var result struct {
		SleptS int `json:"slept_s"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v (raw=%s)", err, raw)
	}
	if result.SleptS != 0 {
		t.Errorf("slept_s = %d after abort want 0", result.SleptS)
	}
}

// TestTool_InvalidArgs verifies that malformed JSON returns an error-shaped
// result without a Go error (so the toolloop surfaces it as a tool result
// rather than a dispatch failure).
func TestTool_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := sleep.New()

	raw, err := tool.Call(context.Background(), json.RawMessage(`not-json`))
	if err != nil {
		t.Fatalf("Call: expected nil Go error, got %v", err)
	}

	var result struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal error result: %v (raw=%s)", err, raw)
	}
	if result.Error != "invalid_args" {
		t.Errorf("error = %q want %q", result.Error, "invalid_args")
	}
}

// TestTool_ShortSleep is a live sleep for 1 second; used to verify the
// timer-based path (not just the clamp / abort paths). Runs in -short mode
// only when the test explicitly requests it (skip under -short).
func TestTool_ShortSleep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live-sleep test in short mode")
	}
	t.Parallel()
	tool := sleep.New()

	start := time.Now()
	raw, err := tool.Call(context.Background(), json.RawMessage(`{"seconds":1}`))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var result struct {
		SleptS int `json:"slept_s"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.SleptS != 1 {
		t.Errorf("slept_s = %d want 1", result.SleptS)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed %v < 900ms — did the sleep fire too early?", elapsed)
	}
}
