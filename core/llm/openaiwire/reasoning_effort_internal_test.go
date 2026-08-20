package openaiwire

import "testing"

// TestMapReasoningEffort covers all branches of the effort mapper. Moved
// from core/llm/azure/reasoning_test.go (model-settings-reach-the-model-
// 01PMZ101 WP08, spec D-14) along with the function itself — azure kept
// its own copy before this WP; now every OpenAI-wire adapter shares this
// one.
func TestMapReasoningEffort(t *testing.T) {
	cases := []struct {
		budget int
		want   string
	}{
		{0, "high"},
		{15000, "high"},
		{20000, "high"},
		{4000, "medium"},
		{14999, "medium"},
		{1, "low"},
		{3999, "low"},
	}
	for _, tc := range cases {
		got := mapReasoningEffort(tc.budget)
		if got != tc.want {
			t.Errorf("mapReasoningEffort(%d) = %q; want %q", tc.budget, got, tc.want)
		}
	}
}
