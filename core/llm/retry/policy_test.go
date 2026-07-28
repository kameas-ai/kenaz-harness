package retry

import (
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestStreamPolicyFromLLM is WP02 of model-request-path-live-01PMDL01:
// the chat live-path's stream-open retry wrapper must be driven by the
// resolved profile's llm.RetryPolicy rather than a hardcoded literal.
// Table-driven over: a custom profile policy converts faithfully; nil
// (unset) falls back to FromLLM's own defaults rather than a separately
// hardcoded value.
func TestStreamPolicyFromLLM(t *testing.T) {
	cases := []struct {
		name string
		in   *llm.RetryPolicy
		want StreamPolicy
	}{
		{
			name: "nil falls back to FromLLM defaults",
			in:   nil,
			want: StreamPolicy{MaxAttempts: 3, BaseDelay: 250 * time.Millisecond, JitterPct: 0.10},
		},
		{
			name: "custom policy drives MaxAttempts/BaseDelay",
			in:   &llm.RetryPolicy{MaxAttempts: 5, BaseMS: 1000, Jitter: "full"},
			want: StreamPolicy{MaxAttempts: 5, BaseDelay: 1000 * time.Millisecond, JitterPct: 0.10},
		},
		{
			name: "jitter none maps to zero JitterPct",
			in:   &llm.RetryPolicy{MaxAttempts: 2, BaseMS: 100, Jitter: "none"},
			want: StreamPolicy{MaxAttempts: 2, BaseDelay: 100 * time.Millisecond, JitterPct: 0},
		},
		{
			name: "partial override fills remaining fields from defaults",
			in:   &llm.RetryPolicy{MaxAttempts: 1},
			want: StreamPolicy{MaxAttempts: 1, BaseDelay: 250 * time.Millisecond, JitterPct: 0.10},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// StreamPolicy carries a func field (Logger), so it is not
			// comparable with == — compare the value fields explicitly.
			got := StreamPolicyFromLLM(tc.in)
			if got.MaxAttempts != tc.want.MaxAttempts || got.BaseDelay != tc.want.BaseDelay || got.JitterPct != tc.want.JitterPct {
				t.Errorf("StreamPolicyFromLLM(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
