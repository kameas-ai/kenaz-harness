package sentry_test

import (
	"os"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/sentry"
)

func TestDisabled_EnvVars(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"on", true},
		{"disabled", true},
		{"TRUE", true},
	}
	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			if tt.val == "" {
				os.Unsetenv("HARNESS_SENTRY_DISABLED")
			} else {
				os.Setenv("HARNESS_SENTRY_DISABLED", tt.val)
				defer os.Unsetenv("HARNESS_SENTRY_DISABLED")
			}
			got := sentry.Disabled()
			if got != tt.want {
				t.Errorf("Disabled() with %q = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestInit_KillSwitch(t *testing.T) {
	os.Setenv("HARNESS_SENTRY_DISABLED", "1")
	defer os.Unsetenv("HARNESS_SENTRY_DISABLED")

	err := sentry.Init(sentry.TierAnonymous, "https://fake@sentry.io/123", "v0.0.0", "abc123")
	if err != nil {
		t.Errorf("Init with kill-switch should return nil, got %v", err)
	}
	// Client should be nop — captures should not panic.
	sentry.CaptureException(os.ErrNotExist, map[string]any{"key": "val"})
}

func TestInit_TierOff(t *testing.T) {
	os.Unsetenv("HARNESS_SENTRY_DISABLED")
	err := sentry.Init(sentry.TierOff, "", "", "")
	if err != nil {
		t.Errorf("Init with TierOff should return nil, got %v", err)
	}
	sentry.CaptureMessage("test", "info", nil)
}
