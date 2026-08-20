// graph_author_no_write_file.cedar — the FR-008 escalation control.
// Mission model-authored-graphs-01PMGA01 (UNIT-3).
//
// A graph is a code-execution primitive (spec §4): composition,
// deferral, and a human-chosen-looking name/description make it
// categorically more powerful than a config write. write_file's
// attrs.path is a required absolute-path string with a `replace` mode
// (manifests/write_file.yaml) — the single node kind whose blast radius
// is least bounded by the type system, so the shipped default forbids
// drafting a graph that contains one, independent of the consent dial.
//
// context.node_kinds is the sorted, comma-joined, de-duplicated set of
// `kind:` values in the drafted graph (mirrors GateWorkflowRun/Save's
// step_kinds shape, core/workflows/audit.go's CollectStepKinds). `like`
// with wildcards on both sides catches "write_file" wherever it falls
// in the comma-joined list without forcing the policy author to
// enumerate every neighbour kind.
//
// This is the direct, content-sensitive answer to "may not author what
// it may not do" — an operator can extend it to forbid other kinds
// without a code change, because the surface is data (context.node_kinds)
// rather than an if-statement.

forbid (
    principal == User::"local",
    action == Action::"graph.author",
    resource is Graph
) when {
    context.node_kinds like "*write_file*"
};
