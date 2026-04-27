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
//
// AliasSunsetVersion identifies the next minor release in which the
// shipped aliases are scheduled for removal. The deprecation warning
// emitted by lookupAlias surfaces this constant verbatim so authors
// have one stable string to grep for in their migration plan. WP08
// pins this value at the v1 boundary; it is intentionally a string so
// future bumps are a one-line edit.
const AliasSunsetVersion = "next-minor"

// AliasResolution carries the (old, new, removal-in) tuple that flows
// from the alias map to any observer (telemetry, audit log,
// frontend banner). Field names match the spec's `kind_alias_resolved`
// event payload (NFR-003) so consumers can pattern-match on either
// surface.
type AliasResolution struct {
	Old        string `json:"old"`
	New        string `json:"new"`
	RemovalIn  string `json:"removal_in"`
	OncePerRun bool   `json:"-"`
}

// AliasObserver is invoked exactly once per (alias, process) at the
// moment lookupAlias resolves a deprecated name. The default observer
// is the slog.WARN line; chassis wiring can install an additional
// observer that emits a structured `kind_alias_resolved` event into
// the harness audit log (WP08, NFR-003).
//
// Observers MUST NOT block — they fire on the graph-load hot path and
// downstream emission errors are silently dropped. Implementations
// that need durable persistence should hand off to a goroutine.
type AliasObserver func(AliasResolution)

var (
	aliasWarnedMu sync.Mutex
	aliasWarned   = map[string]struct{}{}

	aliasObserversMu sync.RWMutex
	aliasObservers   []AliasObserver
)

// RegisterAliasObserver installs an observer invoked once per alias
// (per process) when lookupAlias resolves a deprecated kind name. The
// returned function unregisters the observer; calling it more than
// once is safe.
//
// The observer fires on the same goroutine that triggered the alias
// lookup (i.e. the graph-load path). Observers MUST keep work cheap —
// the warning surface is the slog.WARN line, not the observer.
func RegisterAliasObserver(obs AliasObserver) func() {
	if obs == nil {
		return func() {}
	}
	aliasObserversMu.Lock()
	idx := len(aliasObservers)
	aliasObservers = append(aliasObservers, obs)
	aliasObserversMu.Unlock()
	return func() {
		aliasObserversMu.Lock()
		defer aliasObserversMu.Unlock()
		if idx >= len(aliasObservers) {
			return
		}
		// Replace with nil rather than mutating the slice shape so any
		// concurrent reader holding the read lock keeps its index
		// stable. The fan-out call skips nils.
		aliasObservers[idx] = nil
	}
}

// fanOutAliasResolved invokes every registered observer with the
// resolution. Always called once per (alias, process) — the
// deduplication state lives in `aliasWarned`.
func fanOutAliasResolved(res AliasResolution) {
	aliasObserversMu.RLock()
	obs := append([]AliasObserver(nil), aliasObservers...)
	aliasObserversMu.RUnlock()
	for _, fn := range obs {
		if fn == nil {
			continue
		}
		fn(res)
	}
}

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
			"removal", AliasSunsetVersion,
		)
		fanOutAliasResolved(AliasResolution{
			Old:        kind,
			New:        canonical,
			RemovalIn:  AliasSunsetVersion,
			OncePerRun: true,
		})
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
