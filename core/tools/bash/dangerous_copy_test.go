package bash

import "testing"

func TestDangerousCopyTableCoverage(t *testing.T) {
	t.Parallel()
	// Every entry in dangerousCopyTable must be non-empty and meaningful
	// (at least 10 characters — enough for a sentence fragment).
	for cmd, reason := range dangerousCopyTable {
		cmd, reason := cmd, reason
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if reason == "" {
				t.Errorf("dangerousCopyTable[%q] is empty", cmd)
			}
			if len(reason) < 10 {
				t.Errorf("dangerousCopyTable[%q] = %q (too short; must be a meaningful sentence)", cmd, reason)
			}
		})
	}
}

func TestDangerousCopyFallback(t *testing.T) {
	t.Parallel()
	// An unlisted command must return the generic fallback, not the
	// empty string.
	got := DangerousCopy("some_unknown_very_safe_command")
	if got == "" {
		t.Errorf("DangerousCopy(unknown) = empty, want non-empty generic fallback")
	}
}

func TestDangerousCopyKnownCommands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cmd  string
		want string // substring that must appear in the reason
	}{
		{"rm", "irr"},     // "irreversible"
		{"sudo", "root"},   // "root"
		{"dd", "disk"},    // "disks" or "devices"
		{"chmod", "perm"}, // "permissions"
		{"curl", "remote"}, // "remote" code execution
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got := DangerousCopy(tc.cmd)
			// Case-insensitive substring check.
			lower := func(s string) string {
				b := []byte(s)
				for i, c := range b {
					if c >= 'A' && c <= 'Z' {
						b[i] = c + 32
					}
				}
				return string(b)
			}
			if !containsStr(lower(got), lower(tc.want)) {
				t.Errorf("DangerousCopy(%q) = %q, want to contain %q", tc.cmd, got, tc.want)
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
