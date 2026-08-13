// Package agentgraph is the typed graph DSL AND the kernel that
// executes it (mission `agent-kernel-graph-01KQ6391`).
//
// Scope, as of today: the graph types + YAML/JSON serialization + the
// validator, the Kernel (kernel.go — scheduling, skip propagation,
// backtrack, resume via RebuildState), the per-kind executors
// (exec_*.go), the SQL-backed EventLog (eventlog_sql.go, migration
// 0309), the activity loader, and node materialization. The chat path
// runs through it: core/rpc/views/agentgraph/chat's ChatRunner is the
// only chassis chat entry point and it drives Kernel.Run.
//
// (This comment used to read "WP01 scope (this package today): pure
// data types + YAML/JSON serialization + validator. NO execution, NO
// SQLite, NO RPC, NO frontend." Every clause of that became false as
// the mission landed. Corrected under
// agentgraph-total-convergence-01PMGX01 invariant I8, 2026-08-13.)
//
// The graph spec is the canonical authoring artifact: YAML on disk, JSON
// over RPC, runtime is Go structs (`Graph`, `Node`, `Edge`, `Port`,
// `NodeKind`). Bidirectional conversion is part of this package.
//
// Node kinds are DECLARED IN YAML, not in this doc comment. Every
// callable kind is one manifest under nodes/manifests/*.yaml, and
// AllNodeKinds() / the NodeKind* constants are code-generated from that
// directory (wire_gen.go, attrs_gen.go, ports_gen.go — run
// `go generate ./core/agentgraph/...`). The manifests carry the
// category (compute / control / state), so `nodes.ListByCategory` is
// the live answer to "which kinds are in which family"; an enumeration
// pasted here would be a second source of truth that drifts, which is
// exactly what this mission's invariants exist to prevent.
//
// Validator guarantees enforced at load time:
//
//   - schema: each node carries the right per-kind attribute payload, and
//     every required field is non-empty.
//   - acyclicity: cycles are rejected outside `LoopNode` / `RetryNode`
//     bodies (BFS on non-loop subgraphs).
//   - bounded loops: every `LoopNode` and `RetryNode` declares an
//     explicit `max_iterations` (or `max_attempts`) cap. A `ReflectNode`
//     or `ReviewNode` inside a loop body inherits the body's cap; a
//     `ReviewNode` itself carries a mandatory `max_iterations` field.
//   - port type compatibility: every edge connects a source port and a
//     destination port whose declared types align.
//   - activity references resolve via an injectable lookup hook (the
//     real registry lands in WP05; WP01 ships a stub).
//   - dial budgets: graph-declared budget overrides reference known
//     dial names with well-formed values.
//
// DIRECTIVE_001: the dependency direction is one-way. `core/agentgraph`
// imports the stdlib, `gopkg.in/yaml.v3`, and the narrow value-type
// packages its seams speak — today `core/llm`, `core/llm/tokenizer`,
// `core/logging`, `core/toolloop` and `core/elicitation`. None of those
// import this package back, and nothing in `core/rpc` may be imported
// from here.
//
// (This comment used to claim the package was a leaf that imported
// "stdlib + yaml only". It stopped being true several missions ago;
// corrected under 01PMGX01 invariant I8 — a doc comment that describes
// a world the code left is worse than none.)
package agentgraph
