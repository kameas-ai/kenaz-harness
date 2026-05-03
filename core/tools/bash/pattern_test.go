package bash

import "testing"

func TestDerivePattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		argv []string
		want string
	}{
		// Acceptance criterion from spec: git status --porcelain → "git status"
		{"git status --porcelain", []string{"git", "status", "--porcelain"}, "git status"},
		// Bare program, no subcommand.
		{"ls -la /tmp", []string{"ls", "-la", "/tmp"}, "ls"},
		// Subcommand-shaped second token.
		{"npm install foo", []string{"npm", "install", "foo"}, "npm install"},
		// Flag token at argv[1] — not a subcommand.
		{"git --version", []string{"git", "--version"}, "git"},
		// Single-arg: just the program basename.
		{"pwd", []string{"pwd"}, "pwd"},
		// Path-stripped basename. "arg" IS subcommand-shaped (no dot, no flag).
		{"./scripts/run.sh arg", []string{"./scripts/run.sh", "arg"}, "run.sh arg"},
		// Absolute path stripped. "script.py" contains a dot → NOT a subcommand.
		{"/usr/bin/python3 script.py", []string{"/usr/bin/python3", "script.py"}, "python3"},
		// Go subcommand.
		{"go build ./...", []string{"go", "build", "./..."}, "go build"},
		// go test has subcommand "test".
		{"go test -race ./...", []string{"go", "test", "-race", "./..."}, "go test"},
		// cargo subcommand.
		{"cargo build --release", []string{"cargo", "build", "--release"}, "cargo build"},
		// Empty argv.
		{"empty", []string{}, ""},
		// Single dot — not a valid subcommand.
		{"ls .", []string{"ls", "."}, "ls"},
		// Double dot.
		{"ls ..", []string{"ls", ".."}, "ls"},
		// argv[1] is a relative path — not a subcommand.
		{"cat ./file.txt", []string{"cat", "./file.txt"}, "cat"},
		// argv[1] has a space inside — not a subcommand (defensive).
		{"echo hi there", []string{"echo", "hi"}, "echo hi"},
		// Subcommand with digit (valid identifier).
		{"tool v2", []string{"tool", "v2"}, "tool v2"},
		// rm - dangerous but pattern still derived correctly.
		{"rm -rf /", []string{"rm", "-rf", "/"}, "rm"},
		// curl plain — URL at argv[1] has "://" so is NOT a subcommand.
		{"curl https://example.com", []string{"curl", "https://example.com"}, "curl"},
		// node with subcommand.
		{"node run script.js", []string{"node", "run", "script.js"}, "node run"},
		// npx.
		{"npx create-react-app myapp", []string{"npx", "create-react-app"}, "npx create-react-app"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DerivePattern(tc.argv)
			if got != tc.want {
				t.Errorf("DerivePattern(%v) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestDerivePatternDeterministic(t *testing.T) {
	t.Parallel()
	argv := []string{"git", "status", "--porcelain", "--short"}
	first := DerivePattern(argv)
	for i := 0; i < 10; i++ {
		if got := DerivePattern(argv); got != first {
			t.Errorf("DerivePattern not deterministic: call %d returned %q, first was %q", i, got, first)
		}
	}
}

func TestIsSubcommandShaped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s    string
		want bool
	}{
		{"status", true},
		{"install", true},
		{"build", true},
		{"v2", true},
		{"", false},
		{"-rf", false},
		{"--version", false},
		{"./file", false},
		{"/etc/passwd", false},
		{".", false},
		{"..", false},
		{"hello world", false},
		{"https://example.com", false},
		{"script.py", false},
		{"main.go", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.s, func(t *testing.T) {
			t.Parallel()
			if got := isSubcommandShaped(tc.s); got != tc.want {
				t.Errorf("isSubcommandShaped(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}
