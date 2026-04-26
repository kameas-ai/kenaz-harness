# Implementation Plan: Agent kernel graph

**Branch**: `agent-kernel-graph-01KQ6391` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/agent-kernel-graph-01KQ6391/spec.md`

## Summary

Three primitives — compute, control, state — composed into directed graphs the kernel executes step-by-step. Conversations become trees: any node can fork onto a smaller/larger model with its own state subset, then merge back via summary, append, or replace. Enterprise context lands as bulk-ingestible corpora retrievable with provenance. The kernel rides alongside the existing toolloop (sessions without graphs behave identically). Largest mission to date — ~12 WPs split into three sub-bundles (kernel, branching, corpora) that ship independently.

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: stdlib + existing in-tree packages (`core/llm`, `core/toolloop`, `core/memory`, `core/attachments`, `core/contexts`, `core/telemetry`); `gopkg.in/yaml.v3` for DSL (already in tree).
- **Optional / deferred**: `github.com/smacker/go-tree-sitter` for AST-aware chunking — not v1.
- **Storage**: graph specs at `<DataDir>/agent_graph/library/` + per-project; runs at `<DataDir>/agent_graph/runs/<run_id>/` (JSON checkpoints + per-node logs); conversation tree in SQLite (migration 0306); corpora in SQLite + per-corpus vector DB at `<DataDir>/corpora/<corpus_id>/` (migration 0307).
- **Testing**: Go `-race -count=1 -short`; vitest for frontend. Graph kernel has its own integration test harness (synthesized graphs + stub LLMs).
- **Performance**: NFR-007 (deterministic resume), NFR-008 (per-run resource caps), NFR-006 (atomic per-file ingestion).
- **Constraints**: NFR-003 backward compat, NFR-004 bounded execution, NFR-005 branch isolation, NFR-009 telemetry.

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/agentgraph` consumes `core/llm`, `core/toolloop` (via interface), `core/memory`, `core/attachments`, `core/contexts`, `core/telemetry`, `core/corpus`. Reverse direction stays clean. Pass.
- C-001 (no third-party SDK in `core/`): stdlib + already-vendored YAML lib only. tree-sitter deferred. Pass.
- Privacy CI invariants: graph state, branch storage, corpus chunks all live under `<DataDir>`. PII in node attributes inherits the telemetry verbose-attributes toggle (default off). Pass.

## Project Structure

(Mirrors spec.md §8.)

## Phase 0 — Research summary

- **Existing kernel**: `core/toolloop/` is a tool-dispatch loop, not a planner. Graphs ride alongside it.
- **DSL choices**: YAML for human authoring (`gopkg.in/yaml.v3` already in tree); JSON for storage / wire. Converter both ways. Schema validation at load time.
- **Branching shape**: copy-on-write — fork references parent messages by ID until divergence. Cheap forks; storage proportional to branch divergence, not branch count.
- **Corpus chunking**: line-based v1; AST-aware via tree-sitter is a deferred enhancement when chunk quality becomes the bottleneck. Markdown / heading-aware split is doable in stdlib.
- **Cycle handling**: cycles are ALLOWED only inside the body of `LoopNode` / `RetryNode`. Validator runs BFS on the non-loop subgraphs.
- **Concurrency**: per-run hard cap on in-flight nodes (default 8); the kernel uses `errgroup` + a semaphore. Loop bodies are sequential within the loop; parallel-fanout is the explicit way to express parallelism.
- **Checkpointing**: between every node fire, the kernel writes a JSON snapshot of the run's state (port values, in-flight set, completed set). Resume reads the snapshot + replays from the next-ready set.

## Phases (mapped to mission goals)

- **Phase 1 — DSL + types + validator** (FR-001, FR-002).
- **Phase 2 — Compute node primitives** (FR-003..FR-006).
- **Phase 3 — Control node primitives** (FR-007..FR-012).
- **Phase 4 — State node primitives** (FR-013..FR-018).
- **Phase 5 — Conversation tree + branch ops** (FR-019..FR-021); migration 0306.
- **Phase 6 — Graph executor + checkpoints** (FR-022..FR-023, FR-031..FR-033).
- **Phase 7 — Activity sub-graphs** (plan / validate / decompose / summarize / ask / retrieve).
- **Phase 8 — Corpus types + storage + chunker** (FR-024..FR-028); migration 0307.
- **Phase 9 — Corpus retrieval + RPC** (FR-029..FR-030).
- **Phase 10 — RPC view + bindings** (FR-031..FR-033 + branches + corpora).
- **Phase 11 — Frontend graphs / branches / knowledge** (FR-034..FR-036).
- **Phase 12 — Polish + docs + integration tests** (acceptance walkthroughs A1..A13).

## Work-package breakdown (proposed, three bundles)

### Bundle A — Graph kernel (ships first; standalone)

- **WP01 — DSL + types + validator** (Phase 1). Pure data types + YAML/JSON serialization + validator. No execution yet.
- **WP02 — Compute primitives** (Phase 2). `LLMNode`, `ToolNode`, `TransformNode`, `ActivityNode` (the `ActivityNode` is a thin reference + recursive expansion). Tests with stub LLM + stub tool registry.
- **WP03 — Control primitives** (Phase 3). `BranchNode`, `ParallelNode` + `JoinNode`, `LoopNode`, `RetryNode`. ForkNode + MergeNode are stubbed for now (real impls land with branching in Bundle B).
- **WP04 — State primitives + executor** (Phase 4 + Phase 6). State nodes; the kernel itself; checkpoint persistence; per-node telemetry spans.
- **WP05 — Activity sub-graphs** (Phase 7). Bundled YAML activities + activity loader + tests against stub LLMs.
- **WP06 — RPC view (graphs + runs only) + bindings** (subset of Phase 10).

### Bundle B — Conversation branching

- **WP07 — Tree storage + migration 0306** (Phase 5).
- **WP08 — Branch RPC ops + Fork/Merge node real impls** (rest of Phase 5 + the Fork/Merge piece of Phase 3).
- **WP09 — Frontend branches sidebar + compare view** (subset of Phase 11).

### Bundle C — Enterprise context ingestion

- **WP10 — Corpus types + ingestion pipeline + migration 0307** (Phase 8).
- **WP11 — Corpus retrieval + RPC + frontend** (Phase 9 + subset of Phase 11).

### Polish

- **WP12 — Polish + docs + acceptance walkthroughs** (Phase 12).

DAG:
- Bundle A WP01 → WP02 ∥ WP03 ∥ WP04 → WP05 → WP06.
- Bundle B WP07 → WP08 → WP09 (gated on Bundle A WP04 because branch state nodes consume executor primitives).
- Bundle C WP10 → WP11 (independent of A and B; can ship in parallel with either).
- WP12 last.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| Graph DSL complexity creep | 1 | Spec the DSL minimally; defer features (typed sub-graph composition, generics) to follow-ups. |
| Cycle / unbounded-loop traps | 1, 3 | Validator rejects unbounded loops at load; loop body cycles allowed only with explicit `max_iterations`. |
| Checkpoint determinism with model non-determinism | 6 | Document: replay produces identical TOPOLOGY; LLM outputs may differ. The trace records the actual outputs verbatim. |
| Branch storage explosion (copy full history per fork) | 5 | Copy-on-write — branches reference parent messages by ID until divergence. |
| Branch cross-provider content fidelity (image bytes lost from Anthropic to OpenAI) | 5 | Warn + best-effort convert; document in `docs/agent-kernel-graph.md`. |
| Corpus ingestion crash mid-file | 8 | Atomic per-file: a hash check determines whether to re-embed; partial chunks are not committed. |
| Corpus over-retrieval (top-K returns 50 chunks of 10 KiB each → 500 KiB context) | 9 | Token-budget cap on `ContextReadNode` output (default 16 KiB); spillover dropped with a warning. |
| Activity sub-graph version drift | 7 | Activities ship in `core/agentgraph/activities/` and are version-tagged. User overrides explicit. |
| Frontend graph viz scope creep | 11 | YAML editor only for v1; visual node editor is a separate mission. Trace inspector is a tree view (already simple). |
| Telemetry spans flood the local DB | 6 | Telemetry retention sweep (default 30 days, from telemetry-otel mission) bounds growth. |
| Resume across harness upgrades that change node-kinds | 7, 12 | Migration of saved checkpoints is documented; unknown kinds error loudly. |
| User accidentally creates a graph that costs $$$ | 6 | Per-run resource caps (FR-NFR-008): max LLM tokens / max tool calls / wallclock. UI surfaces cost estimate before run. |

## Open questions

(Restated from spec.md §11.)

1. Expression language for BranchNode — hand-rolled, defer CEL.
2. YAML vs JSON — YAML for authoring, JSON for storage.
3. AST-aware chunking via tree-sitter — deferred to a follow-up mission.
4. Branch cross-provider content fidelity — warn + convert.
5. Trace storage rides on telemetry-otel `telemetry_spans`.
6. Activity sub-graphs ship in-tree; user-overrides via DataDir.
7. Branch storage = copy-on-write.
