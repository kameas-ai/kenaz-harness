// Package agentgraph defines the typed graph DSL the kernel will execute
// (mission `agent-kernel-graph-01KQ6391`).
//
// WP01 scope (this package today): pure data types + YAML/JSON
// serialization + validator. NO execution, NO SQLite, NO RPC, NO frontend.
// Subsequent work-packages layer the kernel executor (WP04), the per-kind
// node implementations (WP02 / WP03 / WP04), the activity loader (WP05),
// the RPC view (WP06), and the conversation tree / corpora / compaction
// surfaces.
//
// The graph spec is the canonical authoring artifact: YAML on disk, JSON
// over RPC, runtime is Go structs (`Graph`, `Node`, `Edge`, `Port`,
// `NodeKind`). Bidirectional conversion is part of this package.
//
// Three primitive node families compose into directed graphs:
//
//   - compute: LLMNode, ToolNode, TransformNode, ActivityNode,
//     ReflectNode, ReviewNode, PlanNode, AskNode, EscalateNode.
//   - control: BranchNode, ParallelNode, JoinNode, LoopNode, RetryNode,
//     ForkNode, MergeNode.
//   - state:   MemoryNode, CorpusReadNode, CorpusWriteNode,
//     AttachmentNode, HistoryReadNode, TraceWriteNode, CheckpointNode.
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
// DIRECTIVE_001: this package imports stdlib + `gopkg.in/yaml.v3` only.
// Reverse dependency direction stays clean — `core/agentgraph` is leaf
// for now; the kernel package (later) wires in `core/llm`, `core/policy`,
// `core/event`, etc.
package agentgraph
