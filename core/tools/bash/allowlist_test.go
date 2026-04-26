package bash

import "testing"

func TestDefaultAllowlistContainsCommonCommands(t *testing.T) {
	want := []string{"ls", "cat", "grep", "git", "go", "echo", "python3"}
	for _, name := range want {
		if !Allows(DefaultAllowlist, name) {
			t.Errorf("DefaultAllowlist missing %q", name)
		}
	}
}

func TestAllowsExactMatch(t *testing.T) {
	if !Allows(DefaultAllowlist, "ls") {
		t.Errorf("Allows(default, ls) = false, want true")
	}
	if !Allows(DefaultAllowlist, "git") {
		t.Errorf("Allows(default, git) = false, want true")
	}
}

func TestAllowsRejectsUnlisted(t *testing.T) {
	for _, name := range []string{"rm", "dd", "kill", "mv", "sudo", "sh", "bash"} {
		if Allows(DefaultAllowlist, name) {
			t.Errorf("Allows(default, %q) = true, want false", name)
		}
	}
}

func TestAllowsEmptyAllowlistDeniesEverything(t *testing.T) {
	for _, name := range []string{"ls", "echo", "git"} {
		if Allows(nil, name) {
			t.Errorf("Allows(nil, %q) = true, want false", name)
		}
		if Allows([]string{}, name) {
			t.Errorf("Allows(empty, %q) = true, want false", name)
		}
	}
}

func TestAllowsRejectsEmptyName(t *testing.T) {
	if Allows(DefaultAllowlist, "") {
		t.Errorf("Allows(default, empty) = true, want false")
	}
}

func TestAllowsCaseSensitive(t *testing.T) {
	// Allowlist matching is case-sensitive; "LS" is not "ls".
	if Allows(DefaultAllowlist, "LS") {
		t.Errorf("Allows(default, LS) = true, want false (case-sensitive)")
	}
}
