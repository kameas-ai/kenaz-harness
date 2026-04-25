// Package scope filters context-pack entries by their declared workflow
// and agent restrictions (FR-015). Per plan §4.3 scoping is applied
// *before* the override evaluation, so an entry that does not apply to
// the active workflow never participates in override matching.
package scope

import pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"

// Filter narrows entries down to those that apply for the (workflow,
// agent) pair. An empty workflow string means "no workflow context", in
// which case entries that restrict themselves to a specific workflow are
// dropped — this matches the spec's User Story 3 acceptance scenario 3
// (personal entry scoped to a workflow does not appear in others).
//
// Filter never modifies its input slice.
func Filter(entries []pack.ContextEntry, workflow, agent string) []pack.ContextEntry {
	out := make([]pack.ContextEntry, 0, len(entries))
	for _, e := range entries {
		if Allow(e, workflow, agent) {
			out = append(out, e)
		}
	}
	return out
}

// Allow reports whether a single entry applies for the given workflow/agent.
//
// Rules:
//   - An entry with no scope restrictions applies everywhere.
//   - If workflow restrictions are present, the entry applies only when
//     the active workflow appears in the restriction set; an empty
//     workflow string never matches a restriction.
//   - The same logic applies to agents.
func Allow(e pack.ContextEntry, workflow, agent string) bool {
	if e.Scope.Empty() {
		return true
	}
	return e.Scope.Allows(workflow, agent)
}
