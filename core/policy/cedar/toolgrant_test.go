package cedar

// Durable "always allow" tool-grant tests
// (confirm-each-enforcement-01PMAG05 WP03).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolAllowGrantFilename(t *testing.T) {
	t.Parallel()

	cases := []struct {
		server, tool string
		want         string
		wantErr      bool
	}{
		{"filesystem", "write_file", "tool_allow_filesystem__write_file.cedar", false},
		// An empty server is the builtin namespace, matching ToolUID.
		{"", "bash", "tool_allow_builtin__bash.cedar", false},
		// Path separators and other filename-hostile runes are sanitised
		// so a hostile server name cannot escape the policy directory —
		// and dots are flattened too, because the permissions view
		// rejects any filename containing "..", which would make such a
		// grant real on disk but invisible and unrevocable in Settings.
		{"../../etc", "passwd", "tool_allow_______etc__passwd.cedar", false},
		{"a b/c", "d:e", "tool_allow_a_b_c__d_e.cedar", false},
		{"co.example.srv", "read", "tool_allow_co_example_srv__read.cedar", false},
		{"srv", "", "", true},
		{"srv", "   ", "", true},
	}
	for _, tc := range cases {
		got, err := ToolAllowGrantFilename(tc.server, tc.tool)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ToolAllowGrantFilename(%q, %q) = %q, want error", tc.server, tc.tool, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ToolAllowGrantFilename(%q, %q): %v", tc.server, tc.tool, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ToolAllowGrantFilename(%q, %q) = %q, want %q", tc.server, tc.tool, got, tc.want)
		}
		// Whatever the input, the result must stay a bare filename: the
		// permissions view joins it onto <DataDir>/policy/ and a
		// separator here would be a path traversal.
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("ToolAllowGrantFilename(%q, %q) = %q — escaped the policy directory", tc.server, tc.tool, got)
		}
	}
}

// The filename convention is load-bearing: the permissions view lists and
// revokes `<family>_allow_*.cedar`, so a grant that does not match the
// prefix is a grant the user cannot see or remove.
func TestToolAllowGrantFilename_MatchesPermissionsViewConvention(t *testing.T) {
	t.Parallel()

	got, err := ToolAllowGrantFilename("github", "create_issue")
	if err != nil {
		t.Fatalf("ToolAllowGrantFilename: %v", err)
	}
	if !strings.HasPrefix(got, "tool_allow_") {
		t.Fatalf("filename %q does not carry the tool_allow_ prefix — Settings → Permissions would never list it", got)
	}
	if !strings.HasSuffix(got, ".cedar") {
		t.Fatalf("filename %q is not a .cedar file — the permissions view skips it", got)
	}
}

func TestWriteAndHasToolAllowGrant(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if HasToolAllowGrant(dir, "filesystem", "write_file") {
		t.Fatal("a grant existed before anything was written")
	}

	name, err := WriteToolAllowGrant(context.Background(), dir, nil, "filesystem", "write_file")
	if err != nil {
		t.Fatalf("WriteToolAllowGrant: %v", err)
	}
	if name != "tool_allow_filesystem__write_file.cedar" {
		t.Fatalf("grant id = %q", name)
	}
	if !HasToolAllowGrant(dir, "filesystem", "write_file") {
		t.Fatal("grant not visible after write")
	}
	// The grant is narrow: it covers exactly one (server, tool).
	if HasToolAllowGrant(dir, "filesystem", "delete_file") {
		t.Error("grant covered a sibling tool")
	}
	if HasToolAllowGrant(dir, "github", "write_file") {
		t.Error("grant covered a different server")
	}

	body, err := os.ReadFile(filepath.Join(dir, PolicyDir, name))
	if err != nil {
		t.Fatalf("read snippet: %v", err)
	}
	src := string(body)
	for _, want := range []string{
		`action == Action::"` + ActionUseTool + `"`,
		`resource == ` + EntityTypeTool + `::"filesystem__write_file"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("snippet missing %q; got:\n%s", want, src)
		}
	}
}

func TestWriteToolAllowGrant_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := context.Background()
	first, err := WriteToolAllowGrant(ctx, dir, nil, "srv", "tool")
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	// The user can hit "always allow" twice (two rows, same tool; a
	// second session). Neither must error nor duplicate the rule.
	second, err := WriteToolAllowGrant(ctx, dir, nil, "srv", "tool")
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if first != second {
		t.Fatalf("grant id changed between writes: %q vs %q", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(dir, PolicyDir))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("policy dir has %d files after two identical writes, want 1", len(entries))
	}
}

// Revocation restores prompting. This is the property that makes
// "always allow" a defensible control rather than a one-way door: the
// grant IS the file, so deleting it takes effect immediately with no
// cache to invalidate and no restart.
func TestRemoveToolAllowGrant_RestoresPrompting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := context.Background()
	if _, err := WriteToolAllowGrant(ctx, dir, nil, "srv", "tool"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !HasToolAllowGrant(dir, "srv", "tool") {
		t.Fatal("grant not visible after write")
	}

	if err := RemoveToolAllowGrant(ctx, dir, nil, "srv", "tool"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if HasToolAllowGrant(dir, "srv", "tool") {
		t.Fatal("grant survived revocation — the tool would keep skipping the prompt")
	}
	// Revoking twice is not an error: the Settings list and a concurrent
	// delete can race, and the desired end state is the same either way.
	if err := RemoveToolAllowGrant(ctx, dir, nil, "srv", "tool"); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}

// A grant needs somewhere to live. Without a data dir the honest answer
// is an error the caller can audit as Written=false, not a silent success
// that promises durability it did not deliver.
func TestWriteToolAllowGrant_NoDataDirErrors(t *testing.T) {
	t.Parallel()

	if _, err := WriteToolAllowGrant(context.Background(), "", nil, "srv", "tool"); err == nil {
		t.Fatal("WriteToolAllowGrant with no data dir reported success")
	}
	if HasToolAllowGrant("", "srv", "tool") {
		t.Fatal("HasToolAllowGrant with no data dir reported a grant")
	}
}

// A directory sitting where the grant file should be is not a grant.
// Fail closed: prompt rather than treat an unreadable policy dir as
// permission.
func TestHasToolAllowGrant_DirectoryIsNotAGrant(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	name, err := ToolAllowGrantFilename("srv", "tool")
	if err != nil {
		t.Fatalf("filename: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, PolicyDir, name), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if HasToolAllowGrant(dir, "srv", "tool") {
		t.Fatal("a directory was accepted as a grant")
	}
}

// ── F1: the lookup must be exact, not filename-shaped ──────────────────

// The filename is sanitised and therefore many-to-one: "foo.bar" and
// "foo_bar" both produce tool_allow_foo_bar__t.cedar. A stat-based
// lookup answered "approved" for a server the user had never seen.
//
// Both orders are checked. Whichever server wrote the grant, the OTHER
// one must not inherit it.
func TestHasToolAllowGrant_DoesNotConfuseServersThatSanitiseAlike(t *testing.T) {
	t.Parallel()

	t.Run("dotted server approved, underscored server must not inherit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if _, err := WriteToolAllowGrant(context.Background(), dir, nil, "foo.bar", "t"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if !HasToolAllowGrant(dir, "foo.bar", "t") {
			t.Fatal("the server that WAS approved does not read as approved")
		}
		if HasToolAllowGrant(dir, "foo_bar", "t") {
			t.Fatal("foo_bar inherited foo.bar's grant — a tool the user never approved would dispatch without a prompt")
		}
	})

	t.Run("underscored server approved, dotted server must not inherit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if _, err := WriteToolAllowGrant(context.Background(), dir, nil, "foo_bar", "t"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if !HasToolAllowGrant(dir, "foo_bar", "t") {
			t.Fatal("the server that WAS approved does not read as approved")
		}
		if HasToolAllowGrant(dir, "foo.bar", "t") {
			t.Fatal("foo.bar inherited foo_bar's grant")
		}
	})

	// Same hazard on the tool half of the pair.
	t.Run("tool names that sanitise alike stay distinct", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if _, err := WriteToolAllowGrant(context.Background(), dir, nil, "srv", "read.file"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if HasToolAllowGrant(dir, "srv", "read_file") {
			t.Fatal("read_file inherited read.file's grant")
		}
	})
}

// The two servers share a filename, so the second write overwrites the
// first and the loser reads as ungranted. That is the safe direction —
// it prompts — and this test pins it so the behaviour is a decision
// rather than a surprise.
func TestWriteToolAllowGrant_CollidingFilenameLastWriterWins(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := context.Background()
	if _, err := WriteToolAllowGrant(ctx, dir, nil, "foo.bar", "t"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := WriteToolAllowGrant(ctx, dir, nil, "foo_bar", "t"); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if !HasToolAllowGrant(dir, "foo_bar", "t") {
		t.Error("the last writer does not hold the grant")
	}
	if HasToolAllowGrant(dir, "foo.bar", "t") {
		t.Error("the overwritten grant still reads as held — it must fall back to prompting")
	}
}

// Revoking must not delete a colliding neighbour's grant.
func TestRemoveToolAllowGrant_LeavesACollidingNeighboursGrantAlone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := context.Background()
	if _, err := WriteToolAllowGrant(ctx, dir, nil, "foo.bar", "t"); err != nil {
		t.Fatalf("write: %v", err)
	}
	// "foo_bar" holds no grant; revoking it must be a no-op.
	if err := RemoveToolAllowGrant(ctx, dir, nil, "foo_bar", "t"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !HasToolAllowGrant(dir, "foo.bar", "t") {
		t.Fatal("revoking foo_bar deleted foo.bar's grant")
	}
}

// Fail closed on anything that is not a grant we wrote. A hand-edited or
// truncated policy file must prompt, never allow.
func TestHasToolAllowGrant_MalformedBodyIsNotAGrant(t *testing.T) {
	t.Parallel()

	name, err := ToolAllowGrantFilename("srv", "tool")
	if err != nil {
		t.Fatalf("filename: %v", err)
	}
	bodies := map[string]string{
		"empty":              "",
		"comment only":       "// nothing here\n",
		"no resource clause": "permit(principal, action, resource);\n",
		"empty resource id":  `permit(principal, action == Action::"use_tool", resource == Tool::"");` + "\n",
		"different tool":     `permit(principal, action == Action::"use_tool", resource == Tool::"other__thing");` + "\n",
	}
	for label, body := range bodies {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, PolicyDir), 0o755); err != nil {
			t.Fatalf("%s: mkdir: %v", label, err)
		}
		if err := os.WriteFile(filepath.Join(dir, PolicyDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("%s: write: %v", label, err)
		}
		if HasToolAllowGrant(dir, "srv", "tool") {
			t.Errorf("%s: a file that is not a grant for (srv, tool) read as one", label)
		}
	}
}

// Tool names are third-party input (they come from MCP servers), and the
// snippet interpolates the id into a quoted Cedar string. A name carrying
// a quote could otherwise close the string and append its own policy.
func TestWriteToolAllowGrant_RejectsPolicyInjectingNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hostile := []struct{ server, tool string }{
		{"srv", `x"); permit(principal, action, resource); //`},
		{`s"); permit(principal, action, resource); //`, "t"},
		{"srv", "line\nbreak"},
		{"srv", `back\slash`},
	}
	for _, h := range hostile {
		if _, err := WriteToolAllowGrant(context.Background(), dir, nil, h.server, h.tool); err == nil {
			t.Errorf("WriteToolAllowGrant(%q, %q) succeeded — a hostile tool name can write its own Cedar policy", h.server, h.tool)
		}
		if HasToolAllowGrant(dir, h.server, h.tool) {
			t.Errorf("HasToolAllowGrant(%q, %q) reported a grant", h.server, h.tool)
		}
	}
}
