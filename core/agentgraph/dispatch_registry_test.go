package agentgraph

import (
	"sort"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// TestDispatchRegistry_MatchesManifests is the Go-registry half of WP01's
// dispatch: discriminator contract (wiring-integrity-01PMAG04, spec §3.1):
//
//   - Every manifest declaring dispatch: graph must have a matching entry
//     in newExecutorRegistry() (a Go Executor keyed by that NodeKind).
//   - Every newExecutorRegistry() entry's kind must be declared
//     dispatch: graph in its manifest — not builtin_tool, not missing.
//
// This is the check scripts/ci/check-node-dispatch.sh reproduces
// standalone from the shell (it shells out to `go test -run` this exact
// test so the two never drift apart — see the script for the rationale).
// The manifest-side structural check (is dispatch: well-formed at all)
// lives in core/agentgraph/nodes/dispatch_test.go, which cannot see the
// registry (DIRECTIVE_001: nodes imports nothing from agentgraph).
func TestDispatchRegistry_MatchesManifests(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	graphKinds := nodes.GraphDispatchKindIDs(cat)
	builtinKinds := nodes.BuiltinToolDispatchKindIDs(cat)

	registry := newExecutorRegistry()
	registered := make(map[string]bool, len(registry.byKind))
	for k := range registry.byKind {
		registered[string(k)] = true
	}

	// Direction 1: every dispatch: graph manifest has a registered Executor.
	for _, id := range graphKinds {
		if !registered[id] {
			t.Errorf("manifest %q declares dispatch: graph but newExecutorRegistry() has no Executor for it", id)
		}
	}

	// Direction 2: every registered Executor's kind is declared
	// dispatch: graph (not builtin_tool, not absent from the manifest set).
	graphSet := make(map[string]bool, len(graphKinds))
	for _, id := range graphKinds {
		graphSet[id] = true
	}
	var regIDs []string
	for id := range registered {
		regIDs = append(regIDs, id)
	}
	sort.Strings(regIDs)
	for _, id := range regIDs {
		if graphSet[id] {
			continue
		}
		if toolName, isBuiltin := builtinKinds[id]; isBuiltin {
			t.Errorf("kind %q has a registered Executor AND declares dispatch: builtin_tool (tool_name=%q) — pick one: either drop the Executor registration or change dispatch: to graph", id, toolName)
			continue
		}
		t.Errorf("newExecutorRegistry() has an Executor for kind %q but no manifest declares it dispatch: graph", id)
	}

	// sleep and subagent_dispatch got real archetype-derived executors in
	// 01PMGX01 Phase 2 (integration 2026-08-12; they are no longer the
	// "shipped builtin_tool examples" — that mode currently has zero
	// instances and remains valid for future runtime-materialized kinds).
	// Pin them explicitly so a future manifest edit that accidentally
	// flips one back to dispatch: builtin_tool — recreating the
	// palette-reachable crash this epoch fixed — is caught here.
	for _, id := range []string{"sleep", "subagent_dispatch"} {
		if !registered[id] {
			t.Errorf("kind %q is expected to have a graph Executor (archetype-derived) but newExecutorRegistry() has none", id)
		}
		if toolName, ok := builtinKinds[id]; ok {
			t.Errorf("kind %q declares dispatch: builtin_tool (tool_name=%q) but has a real Executor — dispatch must be graph", id, toolName)
		}
	}
}
