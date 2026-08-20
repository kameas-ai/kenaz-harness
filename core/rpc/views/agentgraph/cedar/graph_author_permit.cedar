// graph_author_permit.cedar — explicit permit for graph-authoring saves
// once the user has opted in. Mission model-authored-graphs-01PMGA01
// (UNIT-3, FR-005/FR-006).
//
// The engine's DefaultDeny posture is false (NotApplicable maps to
// allow), so a bare absence of any graph.author policy would already
// let an enabled+valid draft through — which is exactly why an explicit
// permit matters here: without it, a test asserting "authoring on ⇒
// persisted" cannot distinguish "the install succeeded" from "nothing
// was installed at all and the engine's default posture allowed it
// anyway". Pairing this permit with graph_author_forbid.cedar makes
// both directions attributable to the install, and it is also what
// keeps the enabled case working if this harness ever flips to
// DefaultDeny=true (fail-closed opt-in) — see
// check-cedar-gate-arguments.sh's vacuous-pass guard for the same
// reasoning applied to gate wiring instead of policy content.
//
// The write_file escalation control (graph_author_no_write_file.cedar)
// is a separate forbid and Cedar evaluates forbids strictly, so this
// permit cannot override it.

permit (
    principal == User::"local",
    action == Action::"graph.author",
    resource is Graph
) when {
    context.authoring_enabled == "true"
};
