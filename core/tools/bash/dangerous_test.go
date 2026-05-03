package bash

import "testing"

func TestIsDangerous(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		argv      []string
		wantDang  bool
		wantEmpty bool // when wantDang=false, reason must be empty
	}{
		// Dangerous commands from the spec §2 list.
		{"rm", []string{"rm", "-rf", "/"}, true, false},
		{"sudo", []string{"sudo", "apt", "install"}, true, false},
		{"chmod", []string{"chmod", "777", "file"}, true, false},
		{"chown", []string{"chown", "root", "file"}, true, false},
		{"dd", []string{"dd", "if=/dev/zero", "of=/dev/sda"}, true, false},
		{"mkfs.ext4", []string{"mkfs.ext4", "/dev/sdb"}, true, false}, // mkfs.ext4 → stripped to mkfs → dangerous
		{"kill", []string{"kill", "-9", "1234"}, true, false},
		{"killall", []string{"killall", "python3"}, true, false},
		{"pkill", []string{"pkill", "-f", "myapp"}, true, false},
		{"shutdown", []string{"shutdown", "-h", "now"}, true, false},
		{"reboot", []string{"reboot"}, true, false},
		{"mv", []string{"mv", "file", "dest"}, true, false},
		{"cp", []string{"cp", "-r", "src", "dest"}, true, false},
		{"curl", []string{"curl", "https://example.com"}, true, false},
		{"wget", []string{"wget", "https://example.com"}, true, false},

		// Safe commands from the old allowlist.
		{"ls", []string{"ls", "-la"}, false, true},
		{"cat", []string{"cat", "file"}, false, true},
		{"grep", []string{"grep", "pattern"}, false, true},
		{"git", []string{"git", "status"}, false, true},
		{"go", []string{"go", "build"}, false, true},
		{"npm", []string{"npm", "install"}, false, true},
		{"echo", []string{"echo", "hello"}, false, true},
		{"pwd", []string{"pwd"}, false, true},
		{"python3", []string{"python3", "script.py"}, false, true},

		// Empty argv.
		{"empty", []string{}, false, true},

		// Absolute path program.
		{"/usr/bin/rm", []string{"/usr/bin/rm", "-rf"}, true, false},

		// Path-prefixed basename for curl.
		{"/bin/curl", []string{"/bin/curl", "-s"}, true, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDang, gotReason := IsDangerous(tc.argv)
			if gotDang != tc.wantDang {
				t.Errorf("IsDangerous(%v): dangerous=%v, want %v", tc.argv, gotDang, tc.wantDang)
			}
			if tc.wantDang && gotReason == "" {
				t.Errorf("IsDangerous(%v): dangerous=true but reason is empty", tc.argv)
			}
			if tc.wantEmpty && gotReason != "" {
				t.Errorf("IsDangerous(%v): safe command returned non-empty reason %q", tc.argv, gotReason)
			}
		})
	}
}

// TestIsDangerousReasonNonempty verifies that every command in the
// dangerous set has a non-empty copy in the table.
func TestIsDangerousReasonNonempty(t *testing.T) {
	t.Parallel()
	for cmd := range dangerousBasenames {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			got := DangerousCopy(cmd)
			if got == "" {
				t.Errorf("DangerousCopy(%q) = empty, want non-empty reason", cmd)
			}
		})
	}
}
