package agentgraph

import (
	"sort"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// library_validate_test.go — WP05 of
// agentgraph-total-convergence-01PMGX01.
//
// toolloop_default.yaml shipped for months as a read-only starting
// template that DID NOT PASS Validate(). Its `continue_check` node was
// spelled `kind: branch` in the belief that the loader would resolve it
// to `decision`; it does not. lookupAlias (core/agentgraph/aliases.go)
// refuses to rewrite any name that is itself a callable kind, and
// `branch` is one — the sub-graph spawn primitive. So the node
// validated against the wrong manifest (branch requires `title`, has no
// `in` port) and the graph a user was offered as a starting point was
// broken the moment they tried to run it.
//
// Nothing caught it because nothing ever loaded the bundled library
// through the validator. This test is that gate. Every graph the
// binary embeds must load AND validate; adding a new library YAML with
// a typo'd kind, a missing required attr, or an unconnected required
// input now fails CI instead of shipping.
func TestBundledLibraryGraphsValidate(t *testing.T) {
	t.Parallel()

	if len(bundledLibrary) == 0 {
		t.Fatal("bundledLibrary is empty — the //go:embed of library/*.yaml is not producing graphs")
	}

	ids := make([]string, 0, len(bundledLibrary))
	for id := range bundledLibrary {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			g, err := coreag.LoadYAML([]byte(bundledLibrary[id]))
			if err != nil {
				t.Fatalf("LoadYAML(%s): %v", id, err)
			}
			if g.ID != id {
				t.Errorf("graph id mismatch: embedded under %q, declares %q", id, g.ID)
			}
			if err := coreag.Validate(g); err != nil {
				t.Errorf("bundled library graph %q does not pass Validate():\n%v", id, err)
			}
		})
	}
}

// TestBundledLibraryCoversTheEmbeddedFiles keeps the pin honest: a YAML
// dropped into library/ that fails the id scan would silently escape
// the validator above.
func TestBundledLibraryCoversTheEmbeddedFiles(t *testing.T) {
	t.Parallel()
	entries, err := libraryFS.ReadDir("library")
	if err != nil {
		t.Fatalf("ReadDir(library): %v", err)
	}
	var yamlCount int
	for _, e := range entries {
		if !e.IsDir() {
			yamlCount++
		}
	}
	if yamlCount != len(bundledLibrary) {
		t.Errorf("library/ holds %d files but bundledLibrary has %d entries — a file failed the id scan and is validated by nothing",
			yamlCount, len(bundledLibrary))
	}
}
