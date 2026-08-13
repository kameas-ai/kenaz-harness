package nodes_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// TestBundledCatalog_DispatchDeclarations pins WP01's structural half of
// the dispatch: discriminator (wiring-integrity-01PMAG04): every
// shipped, callable manifest declares a recognised DispatchMode, and
// builtin_tool/graph pair correctly with tool_name. The complementary
// Go-registry cross-check (does dispatch: graph actually have a
// registered Executor, and vice versa) lives in
// core/agentgraph/dispatch_registry_test.go — this package cannot
// import agentgraph (DIRECTIVE_001), so it can only validate the
// manifest side of the contract.
func TestBundledCatalog_DispatchDeclarations(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if issues := nodes.ValidateDispatch(cat); len(issues) > 0 {
		for _, iss := range issues {
			t.Errorf("dispatch issue: %s", iss)
		}
	}
}

// TestValidateDispatch_CatchesEachShape exercises ValidateDispatch
// against a synthetic catalog covering every failure shape it must
// catch, plus the two shapes it must accept.
func TestValidateDispatch_CatchesEachShape(t *testing.T) {
	t.Parallel()

	// No extends: chain needed — these are standalone top-of-chain kind
	// manifests. ValidateDispatch only inspects Dispatch/ToolName, not
	// inheritance, so a bare id + display_name is enough to load cleanly.
	mk := func(id string, dispatch nodes.DispatchMode, toolName string) string {
		out := "schema_version: \"1\"\nid: " + id + "\ndisplay_name: X\n"
		if dispatch != "" {
			out += "dispatch: " + string(dispatch) + "\n"
		}
		if toolName != "" {
			out += "tool_name: " + toolName + "\n"
		}
		return out
	}

	cases := []struct {
		name      string
		id        string
		yaml      string
		wantIssue bool
	}{
		{"graph clean", "a", mk("a", nodes.DispatchGraph, ""), false},
		{"builtin_tool clean", "b", mk("b", nodes.DispatchBuiltinTool, "kenaz__b"), false},
		{"missing dispatch", "c", mk("c", "", ""), true},
		{"unknown dispatch value", "d", mk("d", "sometimes", ""), true},
		{"builtin_tool without tool_name", "e", mk("e", nodes.DispatchBuiltinTool, ""), true},
		{"graph with stray tool_name", "f", mk("f", nodes.DispatchGraph, "kenaz__f"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Filename stem must match id: the loader rejects a mismatch.
			writeManifest(t, dir, tc.id+".yaml", tc.yaml)
			cat, err := nodes.LoadCatalog(nodes.LoadOptions{UserDir: dir, SkipBundled: true})
			if err != nil {
				t.Fatalf("LoadCatalog: %v", err)
			}
			issues := nodes.ValidateDispatch(cat)
			if tc.wantIssue && len(issues) == 0 {
				t.Errorf("expected a dispatch issue, got none")
			}
			if !tc.wantIssue && len(issues) != 0 {
				t.Errorf("expected no dispatch issue, got %v", issues)
			}
		})
	}
}
