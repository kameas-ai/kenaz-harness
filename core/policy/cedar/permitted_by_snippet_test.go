package cedar

import "testing"

// PermittedBySnippet answers "did a permit from THIS snippet source grant
// it" — the question served-mode bash availability asks of the fleet rule
// set. The embedded default tool policy already permits every kenaz tool,
// so a plain Allow must not count.
func TestPermittedBySnippet(t *testing.T) {
	const (
		prefix      = "fleet-team/"
		permitBash  = `permit(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
		forbidBash  = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
		permitOther = `permit(principal, action == Action::"use_tool", resource == Tool::"kenaz__todo_write");`
	)
	bash := PermissionToolUID("kenaz__bash")
	probe := func(e *Engine) bool {
		return e.PermittedBySnippet(UserUID(), ActionUseTool, bash, nil, prefix)
	}

	t.Run("embedded default permit alone does not count", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if got := e.Evaluate(t.Context(), UserUID(), ActionUseTool, bash, nil).Outcome; got != Allow {
			t.Fatalf("precondition: embedded default should Allow kenaz__bash, got %v", got)
		}
		if probe(e) {
			t.Fatal("PermittedBySnippet true with no prefix snippet installed")
		}
	})

	t.Run("prefix permit counts; other-tool and non-prefix permits do not", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.ReplaceHarnessSnippets(prefix, map[string][]byte{prefix + "other": []byte(permitOther)}); err != nil {
			t.Fatal(err)
		}
		if err := e.LoadHarnessSnippets(map[string][]byte{"local/bash": []byte(permitBash)}); err != nil {
			t.Fatal(err)
		}
		if probe(e) {
			t.Fatal("true from a non-prefix permit / a permit on another tool")
		}
		if err := e.ReplaceHarnessSnippets(prefix, map[string][]byte{prefix + "bash": []byte(permitBash)}); err != nil {
			t.Fatal(err)
		}
		if !probe(e) {
			t.Fatal("false with a prefix permit installed")
		}
	})

	t.Run("forbid beats permit, from any source", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.ReplaceHarnessSnippets(prefix, map[string][]byte{
			prefix + "a": []byte(permitBash),
			prefix + "b": []byte(forbidBash),
		}); err != nil {
			t.Fatal(err)
		}
		if probe(e) {
			t.Fatal("true with a fleet forbid alongside the permit")
		}
		if err := e.ReplaceHarnessSnippets(prefix, map[string][]byte{prefix + "a": []byte(permitBash)}); err != nil {
			t.Fatal(err)
		}
		if err := e.LoadHarnessSnippets(map[string][]byte{"local/forbid": []byte(forbidBash)}); err != nil {
			t.Fatal(err)
		}
		if probe(e) {
			t.Fatal("true with a local forbid alongside the fleet permit")
		}
	})

	t.Run("records no decision", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		_ = e.ReplaceHarnessSnippets(prefix, map[string][]byte{prefix + "bash": []byte(permitBash)})
		before := len(e.RecentDecisions(1000))
		for i := 0; i < 5; i++ {
			probe(e)
		}
		if after := len(e.RecentDecisions(1000)); after != before {
			t.Fatalf("probe appended decisions: %d -> %d", before, after)
		}
	})

	t.Run("nil engine and empty prefix are false", func(t *testing.T) {
		var nilEngine *Engine
		if probe(nilEngine) {
			t.Fatal("nil engine")
		}
		e, _ := newDiskEngine(t, nil)
		_ = e.ReplaceHarnessSnippets(prefix, map[string][]byte{prefix + "bash": []byte(permitBash)})
		if e.PermittedBySnippet(UserUID(), ActionUseTool, bash, nil, "") {
			t.Fatal("empty prefix")
		}
	})
}
