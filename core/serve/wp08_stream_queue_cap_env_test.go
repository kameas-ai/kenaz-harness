package serve

import "testing"

// wp08_stream_queue_cap_env_test.go — served-mode-is-a-real-mode-01PMZ707
// WP08, SD-16.
//
// WithStreamQueueCap's docstring named two purposes — deterministic tests
// (set directly via the option, no env involved) and "capping per-client
// memory in a very constrained workbench" — but nothing wired the second
// purpose to any configuration surface; neither served entry point ever
// called WithStreamQueueCap at all before this WP. StreamQueueCapFromEnv
// is that surface now.

func TestStreamQueueCapFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want int
	}{
		{name: "absent", env: map[string]string{}, want: 0},
		{name: "empty string", env: map[string]string{EnvStreamQueueCap: ""}, want: 0},
		{name: "valid positive", env: map[string]string{EnvStreamQueueCap: "8"}, want: 8},
		{name: "zero (keep default)", env: map[string]string{EnvStreamQueueCap: "0"}, want: 0},
		{name: "negative (keep default)", env: map[string]string{EnvStreamQueueCap: "-5"}, want: 0},
		{name: "non-numeric (keep default)", env: map[string]string{EnvStreamQueueCap: "not-a-number"}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			got := StreamQueueCapFromEnv(getenv)
			if got != tc.want {
				t.Fatalf("StreamQueueCapFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestStreamQueueCapFromEnv_FeedsWithStreamQueueCap is the end-to-end half:
// a parsed value actually changes streamQueueCap()'s answer through the
// real ServerOption, not just StreamQueueCapFromEnv's own return value.
// *Falsify*: skip WithStreamQueueCap in the entry points (as before this
// WP) → a constrained-workbench operator setting KENAZ_SERVE_STREAM_QUEUE_CAP
// has no effect, which is exactly SD-16's finding.
func TestStreamQueueCapFromEnv_FeedsWithStreamQueueCap(t *testing.T) {
	getenv := func(k string) string {
		if k == EnvStreamQueueCap {
			return "12"
		}
		return ""
	}
	s := &Server{}
	WithStreamQueueCap(StreamQueueCapFromEnv(getenv))(s)
	if got := s.streamQueueCap(); got != 12 {
		t.Fatalf("streamQueueCap() = %d, want 12", got)
	}
}
