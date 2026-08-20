// graph_author_forbid.cedar — default forbid for graph-authoring saves
// when the user has not opted in. Mission model-authored-graphs-01PMGA01
// (UNIT-3, FR-005/FR-006).
//
// Context attributes set by GateGraphAuthor (core/policy/cedar/hooks.go):
//   context.authoring_enabled — "true" | "false", the FR-006 consent
//                                dial read live from the settings store.
//                                Absent / unreadable / unparseable all
//                                coerce to "false" before this
//                                evaluation ever runs — see UNIT-4.
//   context.session_kind      — carried for future policy authoring;
//                                not branched on by the shipped bundle.
//   context.node_kinds        — sorted, comma-joined, de-duplicated
//                                `kind:` set in the drafted graph.
//   context.node_count        — node count in the drafted graph.
//
// Fires on `!=`, not on an explicit "false" check, so the zero value
// (an absent or malformed attribute) denies: fail-closed by
// construction, the same shape harness_write_forbid.cedar uses for
// context.session_kind. Cedar evaluates forbids strictly, so even if a
// future permit broadens authoring access by accident, a session with
// authoring_enabled != "true" still cannot persist a draft.

forbid (
    principal == User::"local",
    action == Action::"graph.author",
    resource is Graph
) when {
    context.authoring_enabled != "true"
};
