package agentgraph

import (
	"log/slog"
	"sync"
)

// Alias resolution + deprecation warnings (FR-029..FR-058 / NFR-003).
//
// The codegen-emitted `kindAliases` map (in wire_gen.go) holds every
// shipped (old → new) alias declared in the manifest catalog. Graphs
// using the legacy on-the-wire kind names (`llm`, `plan`, `branch`,
// `fork`) load unchanged: the loader rewrites the kind to the canonical
// new value at decode time and emits a one-shot deprecation warning per
// process per alias.
//
// Hot-reload of the manifest catalog (WP07) refreshes the canonical
// constants but does not invalidate the alias map — aliases are
// declared on the kind manifest itself, so any reload that adds or
// removes an alias rebuilds `kindAliases` via codegen and the
// deprecation-warning state is reset on next process start.

var (
	aliasWarnedMu sync.Mutex
	aliasWarned   = map[string]struct{}{}
)

// lookupAlias resolves a deprecated kind name to its canonical form.
// On first sighting of a given alias per process the function emits a
// deprecation warning to slog at WARN level. The boolean return reports
// whether the input was a known alias (false implies the input was
// already canonical or wholly unknown — callers must distinguish those
// cases via AllNodeKinds).
//
// Canonical names ALWAYS shadow alias entries. The taxonomy
// reconciliation in spec §4.8 reuses some old alias names as new
// canonical names — the most subtle case is `branch`: it is the
// canonical sub-graph-spawn kind AND a deprecated alias for the
// predicate router (now `decision`). When a graph YAML names `branch`
// we honour the canonical interpretation; only when the input does NOT
// match any canonical kind do we consult the alias map.
func lookupAlias(kind string) (string, bool) {
	// Canonical lookup: if the input already names a callable kind in
	// the live catalog, never rewrite it.
	if _, callable := ResolvedManifests[NodeKind(kind)]; callable {
		return "", false
	}
	canonical, ok := kindAliases[kind]
	if !ok {
		return "", false
	}
	aliasWarnedMu.Lock()
	_, already := aliasWarned[kind]
	if !already {
		aliasWarned[kind] = struct{}{}
	}
	aliasWarnedMu.Unlock()
	if !already {
		slog.Warn("agentgraph: deprecated node kind alias resolved",
			"old", kind, "new", canonical,
			"removal", "next minor release",
		)
	}
	return canonical, true
}

// resetAliasWarnings clears the per-process "already warned" set. Used
// by tests that need to assert the deprecation warning fires; not part
// of the production API.
func resetAliasWarnings() {
	aliasWarnedMu.Lock()
	defer aliasWarnedMu.Unlock()
	aliasWarned = map[string]struct{}{}
}
