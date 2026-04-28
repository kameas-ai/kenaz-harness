# Implementation Plan: Agent kernel graph (v2 — durable execution + greedy memory + branching v1 + configurable compaction)

**Branch**: `agent-kernel-graph-01KQ6391` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/agent-kernel-graph-01KQ6391/spec.md`
**Supersedes**: prior plan draft (which framed the kernel as riding alongside toolloop and listed 12 WPs across 3 bundles).

---

## Summary

The kernel **replaces the implicit toolloop** by becoming the single execution engine for sessions. The current `core/toolloop/` is preserved as the implementation backing the **default graph** — a built-in YAML topology the kernel runs whenever a session has no explicit graph attached. This collapses the mental model to: one execution path, one event log per session, one in-memory ContextGraph projection.

Three primitives — compute, control, state — compose into directed graphs. New compute primitives (`ReflectNode`, `ReviewNode`) cover self-refine and reviewer-iteration patterns. State primitives become first-class (`MemoryNode`, `CorpusReadNode`, `CorpusWriteNode`, `AttachmentNode`); memory is **kernel-managed greedy state**, not a tool. Branching v1 is intentionally simple (compact-handoff fork + compact-summary merge + multi-live-branch + interactive-parent + model-recommendation + merge-suggest); richer fork/merge is v2.

Configurable compaction is its own subsystem: 3 invocation sites × 4 strategies × cascading config (global > project > session > per-run > per-node) + custom-subgraph strategies (compaction is itself a graph the kernel runs).

Durable execution is event-sourced with idempotent re-fire on resume — **NOT** strict Temporal-style replay. Topology replays deterministically; LLM token-level outputs may differ.

The mission ships in four functional bundles plus a polish bundle (~17 WPs total).

---

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary dependencies (in-tree)**: `core/llm`, `core/toolloop` (preserved; backs default graph), `core/memory`, `core/attachments`, `core/contexts`, `core/telemetry`, `core/event`, `core/policy`, `core/storage`.
- **Vendored**: `gopkg.in/yaml.v3` (already in tree) for DSL.
- **Deferred**: `github.com/smacker/go-tree-sitter` (AST chunking — not v1); `github.com/a2aproject/a2a-go` (A2AClientNode — v2+).
- **Storage**:
  - Graph specs at `<DataDir>/agent_graph/library/` (built-in embedded + per-project + global).
  - Runs at `<DataDir>/agent_graph/runs/<run_id>/` (JSON checkpoints + per-node logs); checkpoint cadence is per-node-fire automatic.
  - Conversation tree in SQLite — **migration 0306**.
  - Corpora in SQLite + per-corpus vector DB at `<DataDir>/corpora/<corpus_id>/` — **migration 0307**.
  - Memory hook journal — **migration 0308**.
  - Project cascading-config column — **migration 0309**.
  - EventLog reuses existing `core/event/log/` infrastructure (separate migration block).
- **Testing**: Go `-race -count=1 -short`; vitest for frontend. Graph kernel has its own integration test harness (synthesized graphs + stub LLMs + replayable event-log fixtures).
- **Performance**: NFR-007 durable resume; NFR-008 per-run resource caps; NFR-013 memory growth bound.
- **Constraints**: NFR-003 backward compat (default graph reproduces toolloop); NFR-004 bounded execution; NFR-005 branch isolation; NFR-009 telemetry; NFR-010 GUI-only; NFR-011 single-user; NFR-012 privacy; NFR-014 cascading config never silently wins; NFR-015 EventLog is the truth.

---

## Charter Check (re-evaluated for the bigger scope)

- **DIRECTIVE_001 (no cyclic imports)**: `core/agentgraph` consumes `core/llm`, `core/toolloop` (via interface), `core/memory`, `core/attachments`, `core/contexts`, `core/telemetry`, `core/event`, `core/policy`, `core/corpus`, `core/conversation`. None of those should import `core/agentgraph` back. **Pass** (verify in WP01 review).
- **C-001 (no third-party SDK in `core/`)**: stdlib + vendored YAML only for v1. tree-sitter + a2a-go deferred. **Pass.**
- **Privacy CI invariants**: graph state, branch storage, corpus chunks, memory hook journal all live under `<DataDir>`. PII in node attributes inherits the telemetry verbose-attributes toggle (default off). Greedy memory hooks pass content through the existing redaction pipeline (`core/event/log/redact/`) before persistence. **Pass.**
- **Single-user / GUI-only invariants**: no CLI added; all surfaces are Wails-bound RPCs. **Pass.**
- **Cedar policy gating**: every action node (LLM, tool, memory write, file write) routes through `core/policy/policy.Evaluate`. **Pass** (WP02 / WP04 enforce).
- **Bounded execution**: every loop / retry / parallel-fanout / reflect / review is required by validator to declare a cap. **Pass** (WP01 validator).

---

## Project Structure

(Mirrors spec.md §7.)

---

## Phase 0 — Research summary (revised)

- **Default graph**: `toolloop_default.yaml` reproduces current toolloop semantics. Kernel attaches it implicitly when a session has no graph — zero migration friction.
- **DSL**: YAML on disk; JSON over RPC; runtime is Go structs. Bidirectional conversion in `core/agentgraph/spec.go`.
- **Event sourcing**: ContextGraph is a projection of EventLog. The kernel writes one event per side-effect (`node_start`, `node_complete`, `tool_call`, `tool_result`, `memory_write`, `branch_fork`, `branch_merge`, `compaction_fired`, `dial_overridden`, `cost_cap_hit`, `pending_ask`, `ask_answered`, `escalate_triggered`, `review_pass`, `review_fail`, `reflect_started`, `reflect_completed`, `plan_created`).
- **Resume**: deterministic topology replay from EventLog; LLM token-level outputs may differ (documented relaxation; NOT Temporal-style strict replay).
- **Branching v1**: copy-on-write — branches reference parent messages by ID until divergence. Kernel-recommended model at fork-time + kernel-suggested merge at terminal-state.
- **Compaction**: 3 sites × 4 strategies × cascading config. `custom_subgraph` strategies are themselves graphs the kernel runs (recursive, bounded by depth dial).
- **Greedy memory**: 9 boundaries fire writes (claude-mem-style). Background prune sweep runs daily per active project; rules: staleness, age, recall-frequency, embedding-cluster collapse. Pinned entries immune.
- **Cycle handling**: cycles allowed only inside `LoopNode` / `RetryNode` / `ReviewNode` bodies. Validator runs BFS on non-loop subgraphs.
- **Concurrency**: per-run hard cap on in-flight nodes (default 8); kernel uses `errgroup` + semaphore. Loop bodies sequential within. Parallel-fanout is the explicit way to express parallelism. Multi-live-branch concurrency is independent of intra-run concurrency.
- **Cedar policy**: tool/file/network/model/memory action gates. Cap-hit semantics pause-not-kill.

---

## Phases (mapped to FR set)

- **Phase 1 — DSL + types + validator + default graph** (FR-001..FR-003).
- **Phase 2 — Compute primitives** (FR-004..FR-012).
- **Phase 3 — Control primitives** (FR-013..FR-018; ForkNode/MergeNode stubbed pending Bundle B).
- **Phase 4 — State primitives + greedy memory hooks + executor + EventLog projection** (FR-019..FR-025; FR-026..FR-032; FR-060..FR-061; NFR-007 NFR-015).
- **Phase 5 — Conversation tree + branch ops + Fork/Merge real impls** (FR-033..FR-040; FR-055; migration 0306).
- **Phase 6 — Corpus types + ingestion + chunker** (FR-020..FR-021 backing; migration 0307).
- **Phase 7 — Corpus retrieval + RPC** (FR-062 corpus subset).
- **Phase 8 — Configurable compaction** (FR-041..FR-045; FR-068; NFR-014).
- **Phase 9 — Cedar policy gating + dials enforcement** (FR-053..FR-054; FR-048..FR-052).
- **Phase 10 — Background memory prune sweep + memory inspector view** (FR-028; FR-067).
- **Phase 11 — RPC view + bindings** (FR-062..FR-063; full surface).
- **Phase 12 — Frontend graphs / branches / knowledge / memory / dials** (FR-064..FR-068).
- **Phase 13 — Polish + docs + acceptance walkthroughs** (A1..A24).

---

## Work-package breakdown (5 bundles, ~17 WPs)

### Bundle A — Graph kernel (foundational; gates B, C, D)

- **WP01 — DSL + types + validator + default graph** (Phase 1).
  - `core/agentgraph/spec.go` `validator.go`. YAML/JSON conversion. Cycle/orphan/port/dial-ref/activity-ref validation.
  - Embed `library/toolloop_default.yaml` and verify it loads + validates.
- **WP02 — Compute primitives (incl. ReflectNode + ReviewNode)** (Phase 2).
  - `LLMNode`, `ToolNode`, `TransformNode`, `ActivityNode`, `ReflectNode`, `ReviewNode`, `PlanNode`, `AskNode`, `EscalateNode`.
  - Cedar policy gate wired on `ToolNode` + `LLMNode` (model selection allow/deny).
  - Tests with stub LLM + stub tool registry; specifically cover `ReflectNode` / `ReviewNode` cap-hit semantics.
- **WP03 — Control primitives** (Phase 3).
  - `BranchNode`, `ParallelNode` + `JoinNode`, `LoopNode`, `RetryNode`. `ForkNode` + `MergeNode` stubs (real impls in WP08).
- **WP04 — State primitives + executor + EventLog projection + greedy memory hooks** (Phase 4).
  - State nodes: `MemoryNode`, `CorpusReadNode`, `CorpusWriteNode` (stub backing in WP10), `AttachmentNode`, `HistoryReadNode`, `TraceWriteNode`, `CheckpointNode`.
  - Kernel executor: BFS firing, concurrency cap, per-node OTel span, automatic per-fire checkpoint.
  - EventLog projection: `kernel_resume.go` rebuilds ContextGraph from `core/event/log/`.
  - Greedy memory hooks at the 9 boundaries (FR-027). Hook journal writes (migration 0308).
  - Memory store wired through Cedar memory-write policy.
- **WP05 — Activity sub-graphs (incl. reflect.yaml + review.yaml)** (Phase 7 piece of original plan).
  - Bundled YAML activities + activity loader + tests against stub LLMs.
- **WP06 — RPC view (graphs + runs + memory + dials core) + bindings subset** (Phase 11 subset).
  - `Graphs_*`, `GraphRuns_*`, `Memory_*` (incl. hook journal queries), `Dials_*` (read effective + per-layer write).

### Bundle B — Branching v1 simple

- **WP07 — Tree storage + migration 0306** (Phase 5 storage).
  - `conversation_branches` table; `session_messages.branch_id` column; backfill synthetic trunk branch.
  - Copy-on-write branch manager in `core/conversation/`.
- **WP08 — Branch RPC ops + Fork/Merge real impls + cross-provider converter** (Phase 5 ops + control replacement).
  - `Branches_Fork`, `Branches_List`, `Branches_Switch`, `Branches_Merge`, `Branches_Discard`.
  - `ForkNode` real impl: compact-handoff via configured compaction strategy.
  - `MergeNode` real impl: compact-summary via `summarize_append` default; `append` and `replace_last_turn` modes.
  - Model-recommendation heuristic: skill table + containment heuristic.
  - Merge-suggestion heuristic: terminal-state token detection + goal-satisfied classifier `LLMNode` + idle-timer.
  - Cross-provider content converter (Anthropic ↔ OpenAI image/citation blocks; warn-and-proceed fidelity log).
- **WP09 — Frontend branches sidebar + compare view + model-recommendation chip + merge-suggest toast** (Phase 12 branch subset).
  - `BranchesSidebar.vue` with live multi-branch tree.
  - `BranchCompare.vue`.
  - Fork modal with model-recommendation chip (kernel suggests; user overrides).
  - `MergeSuggestToast.vue` for the suggest-merge UX.

### Bundle C — Corpora

- **WP10 — Corpus types + ingestion pipeline + migration 0307** (Phase 6).
  - `core/corpus/` package; `Corpus`, `Chunk` types.
  - Ingestion: walk + chunk (markdown/code-line/PDF/plain-text) + embed (existing OpenAI embedder).
  - Atomic per-file commit (NFR-006).
  - Re-ingest hash-based skip (FR-028 from prior spec).
  - Background goroutine + progress events on existing event broker.
- **WP11 — Corpus retrieval + RPC + frontend** (Phase 7 + Phase 12 corpus subset).
  - Top-K retrieval + `CorpusReadNode` real backing.
  - `Corpora_List/Create/Ingest/Status/Delete/Search` RPCs.
  - `KnowledgeView.vue` + `CorpusEditor.vue`.

### Bundle D — Configurable compaction

- **WP12 — Compaction strategies + invocation sites + cascading config** (Phase 8).
  - `core/agentgraph/compaction/strategies.go`: 4 strategies (`summary`, `drop_oldest`, `semantic_cluster`, `custom_subgraph`).
  - `invocation.go`: 3 invocation sites (pre_call, post_tool, manual).
  - `config.go`: cascading layers (global > project > session > per-run > per-node) with `Dials_Get` returning effective value + contributing layer.
  - Migration 0309 for `projects.config_json`.
- **WP13 — Custom-subgraph compaction strategy + recursion guard + compaction config UI** (Phase 8 + Phase 12 compaction subset).
  - `custom_subgraph` strategy invokes the kernel recursively with depth dial guard.
  - `CompactionConfigView.vue` + `DialsView.vue`.
  - Per-graph and per-node compaction override editor (extend `GraphSpecEditor.vue`).

### Bundle E — Policy / dials / memory inspector / polish

- **WP14 — Cedar policy wiring (tool/file/network/model/memory) + cap-hit pause-not-kill semantics** (Phase 9).
  - Extend `core/policy/policy.go` with new action categories.
  - Kernel cap-hit modal flow: emit `cost_cap_hit` / `budget_cap_hit`; UI modal continues / stops / partial-merges.
- **WP15 — Background memory prune sweep + memory inspector view** (Phase 10).
  - `core/agentgraph/memory_hooks/prune.go`: staleness / age / recall-frequency / embedding-cluster-collapse.
  - Dry-run preview RPC.
  - `MemoryInspectorView.vue`.
- **WP16 — Frontend dials panel + per-run override + global/project dials view** (Phase 12 dials subset).
  - `DialsPanel.vue` (per-run override sidebar).
  - `DialsView.vue` (global / project settings).
  - Effective-value display with contributing-layer attribution.
- **WP17 — Polish + docs + integration tests** (Phase 13).
  - End-to-end fixtures for A1..A24.
  - `docs/agent-kernel-graph.md`.
  - Performance smoke tests (NFR-013 memory growth; resume correctness).

---

## DAG ordering between bundles

```
Bundle A (kernel)          ──→  Bundle B (branching v1)
        │                              │
        │                              ├──→  Bundle E.WP14, E.WP15, E.WP16, E.WP17
        ├──→  Bundle C (corpora)  ─────┤
        │                              │
        └──→  Bundle D (compaction)  ──┘
```

- **Bundle A WP01 → WP02 ∥ WP03 ∥ WP04 → WP05 → WP06.**
  - WP02, WP03, WP04 can run in parallel after WP01 (separate sub-trees).
  - WP05 depends on WP02 (activity loader needs LLMNode).
  - WP06 depends on WP04 (RPC surface needs the executor).
- **Bundle B WP07 → WP08 → WP09.** Gated on Bundle A WP04 (branch state nodes consume executor + EventLog projection).
- **Bundle C WP10 → WP11.** Gated on Bundle A WP04 (`CorpusReadNode` backing). Otherwise independent — can ship in parallel with B.
- **Bundle D WP12 → WP13.** Gated on Bundle A WP04 (compaction calls back into the kernel for `custom_subgraph` and into LLMNode for `summary`). Custom-subgraph strategy in WP13 also benefits from WP05 activities.
- **Bundle E WP14 → WP15 → WP16 → WP17.** Gated on B + C + D for the polish acceptance set; WP14 can land earlier in parallel with B/C/D since policy plumbing is bundle-orthogonal.

**Independent ship**:
- Bundle A is a hard prerequisite for everything else.
- Bundle C (corpora) can ship before Bundle B (branching v1) once A.WP04 lands.
- Bundle D (compaction) gates Bundle B (because branching uses compaction in fork/merge), so D should ship before B if both are pursued in parallel after A.
- Bundle E.WP14 (policy) is independent of B/C/D and could ship right after Bundle A.

**Recommended sequencing**: A → D → (B ∥ C) → E. WP14 (policy) can be slotted right after A.

---

## Risk register (extended)

| Risk | Phase / WP | Mitigation |
|---|---|---|
| Graph DSL complexity creep | 1 / WP01 | Spec the DSL minimally; defer typed sub-graph composition / generics. |
| Cycle / unbounded-loop traps | 1, 3 / WP01, WP03 | Validator rejects unbounded loops at load; loop body cycles allowed only with explicit `max_iterations`. |
| **Durable-resume drift** | 4 / WP04 | Document the relaxation: topology deterministic; LLM tokens may differ. Acceptance A24 tests it explicitly. |
| **ReflectNode iteration runaway** | 2 / WP02 | Mandatory `max_iterations` cap (validator-enforced); `ReviewNode` cap-hit emits `review_failed_unrecoverable`. |
| **ReviewNode iteration runaway** | 2 / WP02 | Same as above; `EscalateNode` is the configured fallback. |
| **Greedy memory storage explosion** | 4 + 10 / WP04, WP15 | Background prune sweep with staleness / age / recall-frequency / embedding-cluster collapse; pinned entries immune; size-cap dial; NFR-013 acceptance. |
| **Compaction config divergence per scope** | 8 / WP12 | Cascading config requires `Dials_Get` to return effective value + contributing layer; NFR-014 acceptance; UI shows attribution. |
| **Custom-subgraph compaction infinite recursion** | 8 / WP13 | Recursion-depth dial; cycle detection in compaction graph; bounded by validator. |
| **A2A integration creep** | — | A2AClientNode explicitly out of scope (§9 / §11). Mission spec rejects PRs that pull it forward. |
| **Branch storage explosion (copy full history per fork)** | 5 / WP07 | Copy-on-write — branches reference parent messages by ID until divergence. |
| **Branch cross-provider content fidelity** | 5 / WP08 | Warn + best-effort convert; `convert_failed` events logged; UI surfaces fidelity loss. |
| **Corpus ingestion crash mid-file** | 6 / WP10 | Atomic per-file: hash-check determines re-embed; partial chunks not committed. |
| **Corpus over-retrieval (top-K returns 50 chunks of 10 KiB → 500 KiB context)** | 7 / WP11 | Token-budget cap on `CorpusReadNode` output (default 16 KiB); spillover dropped with warning. |
| **Activity sub-graph version drift** | 7 / WP05 | Activities ship in `core/agentgraph/activities/` and are version-tagged. User override explicit. |
| **Frontend graph viz scope creep** | 12 / WP09, WP13 | YAML editor only for v1; visual node editor is a separate mission. Trace inspector is a tree view. |
| **Telemetry spans flood the local DB** | 4 / WP04 | Telemetry retention sweep (default 30 days) bounds growth. |
| **Resume across harness upgrades that change node-kinds** | 4, 13 / WP04, WP17 | Migration of saved checkpoints documented; unknown kinds error loudly; replay from EventLog is the canonical path. |
| **User accidentally creates an expensive graph** | 9 / WP14 | Per-run resource caps (FR-048..FR-052 dials); UI surfaces cost estimate before run; cap-hit modal pauses-not-kills. |
| **`AskNode` blocks indefinitely + harness restart** | 2 / WP02 | Pending-ask persists in EventLog; survives restart; UI surfaces pending-ask in next session. |
| **Cedar policy misconfiguration locks user out** | 9 / WP14 | Policy defaults are permissive; deny-list opt-in; `Policy_PreviewEffective` RPC for debugging. |

---

## Open questions (carried from spec.md §10)

1. Expression language for `BranchNode` — hand-rolled tiny evaluator for v1; CEL deferred.
2. Skill → model recommendation table location & format — TBD; default at `<embedded>/skills.yaml`.
3. `PlanNode` task-complexity heuristic — classifier `LLMNode` v1; learned classifier deferred.
4. Are the 9 kernel boundaries the right set for memory hooks?
5. Background prune cadence default + memory-store size-cap default.
6. Cross-provider conversion lossy → warn-and-proceed (default) vs hard-fail?
7. `A2AClientNode` shape (v2+).
8. EventLog retention vs. project-archive lifecycle.
