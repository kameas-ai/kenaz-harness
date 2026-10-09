package mlproducer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
	"github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
	"github.com/kameas-ai/kenaz-harness/core/tools/websearch"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func testMinimiser(custom ...string) minimiser {
	set := map[string]bool{}
	for _, c := range custom {
		set[c] = true
	}
	return minimiser{
		hasher:  NewHasherWithKey(testKey),
		servers: ServerClassifierFunc(func(s string) bool { return set[s] }),
	}
}

// The copied tool names must stay the real ones.
func TestToolNames_MatchTheRealConstants(t *testing.T) {
	pins := map[string]string{
		toolReadFile:  fsbuiltins.NameReadFile,
		toolListDir:   fsbuiltins.NameListDir,
		toolGlob:      fsbuiltins.NameGlob,
		toolGrep:      fsbuiltins.NameGrep,
		toolWriteFile: fsbuiltins.NameWriteFile,
		toolEditFile:  fsbuiltins.NameEditFile,
		toolBash:      bash.Name,
		toolWebFetch:  webfetch.ToolName,
		toolWebSearch: websearch.ToolName,
	}
	for ours, real := range pins {
		if ours != real {
			t.Errorf("copied tool name %q != real constant %q", ours, real)
		}
	}
}

var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestHash_Is16HexAndKeyed(t *testing.T) {
	a, _ := NewHasherWithKey(testKey).H("/Users/alice/repo")
	b, _ := NewHasherWithKey([]byte("another-key-another-key-another-k")).H("/Users/alice/repo")
	if !hex16.MatchString(a) {
		t.Fatalf("h = %q, want 16 hex", a)
	}
	if a == b {
		t.Fatal("the hash does not depend on the per-install key")
	}
}

func TestHashKey_MintedOnFirstUse0600AndStable(t *testing.T) {
	dir := t.TempDir()
	h := NewHasher(dir)
	if _, err := os.Stat(HashKeyPath(dir)); !os.IsNotExist(err) {
		t.Fatal("key file written at construction; it must be minted on first use")
	}
	v1, err := h.H("x")
	if err != nil {
		t.Fatalf("H: %v", err)
	}
	fi, err := os.Stat(HashKeyPath(dir))
	if err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, want 0600", fi.Mode().Perm())
	}
	v2, _ := NewHasher(dir).H("x")
	if v1 != v2 {
		t.Fatal("a second Hasher over the same data dir produced a different hash")
	}
}

func TestHashKey_MalformedIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	p := HashKeyPath(dir)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte("not-hex"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHasher(dir).H("x"); err == nil {
		t.Fatal("a malformed key loaded")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "not-hex" {
		t.Fatal("malformed key file was overwritten")
	}
	if _, err := NewHasher("").H("x"); err == nil {
		t.Fatal("an empty data dir minted an ephemeral key")
	}
}

func TestMinimisation_Table(t *testing.T) {
	m := testMinimiser("my-private-server")
	h := func(x string) string { v, _ := m.hasher.H(x); return v }

	t.Run("paths", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"/Users/alice/acme-secret/main.go", h("/Users/alice/acme-secret/main.go") + ".go"},
			{"/Users/alice/acme-secret/README.MD", h("/Users/alice/acme-secret/README.MD") + ".md"},
			{"/Users/alice/acme-secret/Makefile", h("/Users/alice/acme-secret/Makefile")},
			{"/tmp/x.weird-ext!", h("/tmp/x.weird-ext!")},
		}
		for _, c := range cases {
			if got := m.pathToken(c.in); got != c.want {
				t.Errorf("pathToken(%q) = %q, want %q", c.in, got, c.want)
			}
			if strings.Contains(m.pathToken(c.in), "alice") {
				t.Errorf("raw path leaked: %q", m.pathToken(c.in))
			}
		}
		if got := absPath("src/a.go", "/w"); got != "/w/src/a.go" {
			t.Errorf("absPath relative = %q", got)
		}
		if got := absPath("src/a.go", ""); got != "" {
			t.Errorf("absPath with no workspace = %q, want \"\" (no raw relative path)", got)
		}
	})

	t.Run("commands", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"go test ./...", "go test"},
			{"git commit -m 'secret message'", "git commit"},
			{"npm run test -- --watch", "npm run"},
			{"ls", "ls"},
			{"  pytest   -k foo", "pytest -k"},
			{"OPENAI_API_KEY=sk-live-abcdef1234567890 python app.py", "python " + h("app.py") + ".py"},
			{"cat /etc/passwd", "cat " + h("/etc/passwd")},
			{"./scripts/run.sh --fast", h("./scripts/run.sh") + ".sh --fast"},
			{"curl https://user:pw@example.com/x", "curl [redacted]"},
			{"echo ghp_abcdefghijklmnopqrstuvwxyz0123", "echo [redacted]"},
			{"mytool --token=abc", "mytool [redacted]"},
			{"export AKIAABCDEFGHIJKLMNOP", "export [redacted]"},
		}
		for _, c := range cases {
			got := m.cmdPrefix(c.in)
			if got != c.want {
				t.Errorf("cmdPrefix(%q) = %q, want %q", c.in, got, c.want)
			}
			if n := len(strings.Fields(got)); n > 2 {
				t.Errorf("cmdPrefix(%q) has %d tokens", c.in, n)
			}
		}
	})

	t.Run("tool names", func(t *testing.T) {
		cases := []struct{ in, want string }{
			{"kenaz__bash", "kenaz__bash"},
			{"github__create_issue", "github__create_issue"},
			{"my-private-server__lookup", "custom__" + h("my-private-server") + "__lookup"},
			{"bare_tool", "bare_tool"},
			{"evil name /Users/alice", "invalid__" + h("evil name /Users/alice")},
		}
		for _, c := range cases {
			if got := m.toolName(c.in); got != c.want {
				t.Errorf("toolName(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	})

	t.Run("credential tokens", func(t *testing.T) {
		for _, tok := range []string{
			"sk-ant-api03-abcdefgh", "ghp_0123456789abcdefghij", "xoxb-1234-5678",
			"eyJhbGciOiJIUzI1NiJ9.e30.x", "glpat-abcdefghij", "a1b2c3d4e5f6g7h8i9j0k1",
			"PASSWORD=hunter2",
		} {
			if !looksCredential(tok) {
				t.Errorf("%q not treated as a credential", tok)
			}
		}
		for _, tok := range []string{"go", "test", "commit", "-v", "./...", "pytest"} {
			if looksCredential(tok) {
				t.Errorf("%q wrongly treated as a credential", tok)
			}
		}
	})

	t.Run("results", func(t *testing.T) {
		if code, ok := bashExitCode(`{"stdout":"","stderr":"","exit_code":1,"truncated":false}`); !ok || code != 1 {
			t.Errorf("exit code = %d/%v", code, ok)
		}
		if _, ok := bashExitCode(`{"task_id":"t","status":"running"}`); ok {
			t.Error("background result yielded an exit code")
		}
		if _, ok := bashExitCode(`{"stdout":"…truncated`); ok {
			t.Error("non-JSON result yielded an exit code")
		}
		if r, n := bashNotRun(`{"stdout":"","stderr":"command not allowed: rm","exit_code":-1,"truncated":false,"refused":true}`); !r || n {
			t.Error("refused marker not read")
		}
		if r, n := bashNotRun(`{"stdout":"","stderr":"secret resolution failed","exit_code":-1,"truncated":false,"not_run":true}`); r || !n {
			t.Error("not_run marker not read")
		}
		if r, n := bashNotRun(`{"stdout":"","stderr":"killed","exit_code":-1,"truncated":false}`); r || n {
			t.Error("a real process exiting -1 was read as not having run")
		}
		if !resultFlagsError(`{"is_error":true,"error":"nope"}`) || resultFlagsError(`{"content":"x"}`) || resultFlagsError("plain") {
			t.Error("fsbuiltins is_error detection wrong")
		}
	})
}

func TestPhaseInference_Table(t *testing.T) {
	cases := []struct {
		tool, cmd string
		want      Phase
		ok        bool
	}{
		{toolWriteFile, "", PhaseCoding, true},
		{toolEditFile, "", PhaseCoding, true},
		{toolReadFile, "", PhaseExploring, true},
		{toolListDir, "", PhaseExploring, true},
		{toolGlob, "", PhaseExploring, true},
		{toolGrep, "", PhaseExploring, true},
		{toolWebFetch, "", PhaseExploring, true},
		{toolWebSearch, "", PhaseExploring, true},
		{toolBash, "go test ./...", PhaseTesting, true},
		{toolBash, "CGO_ENABLED=1 go test -race ./core/...", PhaseTesting, true},
		{toolBash, "pytest -q", PhaseTesting, true},
		{toolBash, "npm test", PhaseTesting, true},
		{toolBash, "npm run test", PhaseTesting, true},
		{toolBash, "cargo test", PhaseTesting, true},
		{toolBash, "make test", PhaseTesting, true},
		{toolBash, "vitest run", PhaseTesting, true},
		{toolBash, "jest", PhaseTesting, true},
		{toolBash, "git diff HEAD~1", PhaseReviewing, true},
		{toolBash, "git -C repo log --oneline", PhaseReviewing, true},
		{toolBash, "git --no-pager show abc", PhaseReviewing, true},
		{toolBash, "git commit -m x", "", false},
		{toolBash, "go build ./...", "", false},
		{toolBash, "npm run build", "", false},
		{"github__create_issue", "", "", false},
	}
	for _, c := range cases {
		got, ok := inferPhase(c.tool, c.cmd)
		if got != c.want || ok != c.ok {
			t.Errorf("inferPhase(%q, %q) = %q/%v, want %q/%v", c.tool, c.cmd, got, ok, c.want, c.ok)
		}
	}
	if gitSubcommand("git -c user.name=x commit -m y") != "commit" {
		t.Error("git -c option not skipped")
	}
}

// The kind table is exactly spec §12 A-10's: six kinds, each with
// exactly its payload keys.
func TestKindTable_IsExactlySpecA10(t *testing.T) {
	want := map[string][]string{
		"file":         {"task", "path", "file"},
		"terminal":     {"task", "cmd", "exit_code"},
		"commit":       {"task"},
		"phase_change": {"task", "phase"},
		"agent.tool":   {"task", "tool", "outcome", "dur_ms"},
		"agent.turn":   {"task", "dur_ms", "model_calls", "tool_calls", "outcome"},
	}
	if len(Kinds()) != len(want) {
		t.Fatalf("kinds = %v, want exactly %d", Kinds(), len(want))
	}
	for _, k := range Kinds() {
		w, ok := want[k]
		if !ok {
			t.Errorf("kind %q is not in spec A-10", k)
			continue
		}
		if got := PayloadKeys(k); strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("%s payload keys = %v, want %v", k, got, w)
		}
	}
	for _, daemonOnly := range []string{"edit", "file_edit", "save", "git", "process", "browser", "hyprland", "power"} {
		if PayloadKeys(daemonOnly) != nil {
			t.Errorf("kind %q must not be emitted", daemonOnly)
		}
	}
}
