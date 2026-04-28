# Spec: Agent kernel graph — explicit topology, durable execution, branching, greedy memory, configurable compaction

**Mission ID**: `agent-kernel-graph-01KQ6391`
**Status**: draft (rewrite incorporating ~30 design decisions; supersedes prior draft)
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

---

## 1. Vision & motivation

The harness today runs a **tool-dispatch loop** (`core/toolloop/`): after each LLM stream that ends with `tool_use`, the loop dispatches tool calls, threads results back, and re-pumps the model. This is a single, opaque, implicit topology. It is sufficient for chat-with-tools but **insufficient** for:

- **Long-horizon tasks** that decompose into sub-problems with their own sub-models, sub-tools, and sub-validations.
- **Heterogeneous model routing** — small/fast model for triage, switch to a large reasoning model for a sub-problem, summarize back with the small model.
- **Self-correction** — emit, critique, revise, re-emit with bounded iteration.
- **Branching** — try a different approach without losing the trunk; compare two approaches; merge findings back.
- **Durable resume** across crashes / restarts / very long runs.
- **Bulk knowledge** — corpus retrieval with provenance.
- **Predictable cost / wallclock** — explicit budget dials enforced by the kernel rather than hoped-for by the model.

This mission introduces the **agent kernel graph**: an explicit topology of typed primitive node-kinds (compute / control / state) composed into directed graphs the kernel executes step-by-step. Each step has typed inputs and outputs, telemetry, retry/cap semantics, and emits to a single canonical event log per session. The in-memory ContextGraph (the running agent's working set) is a **projection** of the EventLog and is rebuilt from the log on resume.

### 1.1 Graph IS session

Sessions in the harness now ARE graph runs. There is no "graphs ride alongside toolloop" coexistence — the toolloop becomes the **default graph**: a built-in YAML topology the kernel runs whenever a session has not been given an explicit graph. This collapses the mental model:

- One execution path (the kernel).
- One log (the EventLog).
- One projection (the ContextGraph).
- One UI (the chat surface, augmented with branches / trace / dials).

Existing sessions continue to work unchanged because the default graph reproduces toolloop semantics. Users who want explicit topology attach a graph; users who don't never see one.

### 1.2 Durable execution, NOT strict deterministic replay

The kernel is **event-sourced** with **idempotent re-fire on resume**, in the spirit of Temporal-style durable execution but with one critical relaxation:

- **Graph topology replays deterministically.** Given the same EventLog, the kernel re-derives the same node-firing order, the same in-flight set, the same checkpoints.
- **LLM token-level outputs may differ on resume.** We do NOT memoize model token streams to enforce strict replay equivalence. A node that re-fires after a crash may produce a slightly different completion. The kernel records actual outputs verbatim in the log; replay does not retry already-completed nodes.

This is a deliberate choice: strict Temporal-style replay of LLM calls would require either pinned token-level memoization (storage explosion + privacy risk) or refusing to re-fire after crashes (defeating durability). Documenting the relaxation up front prevents the implementer from chasing strict replay.

### 1.3 Programmatic Go DSL with YAML as export

Runtime types are Go structs (`GraphSpec`, `Node`, `Edge`, `Port`, etc.). Authors construct graphs in Go, in YAML, or via the UI's spec editor. YAML is the canonical human-authoring + on-disk export format; in-memory and over-RPC the spec is JSON. Bidirectional conversion is part of the DSL package.

### 1.4 Local-only, GUI-only, single-user

- All compute on the user machine. No cloud sync, no server-side orchestration.
- Single Wails renderer; no CLI.
- Single user per install.
- Privacy/security-first: see §6 NFRs and §7 policy.

---

## 2. Glossary

- **Graph** — an immutable `GraphSpec` with `Nodes` + `Edges`. Authoring artifact, persisted to YAML.
- **Session** — one logical conversation. Equivalent to a Graph Run + an EventLog. Sessions without an explicitly attached graph use the **default graph** (the built-in toolloop topology).
- **EventLog** — single canonical, append-only event stream per session. Authoritative truth. All side effects (LLM calls, tool calls, memory writes, branch ops, checkpoint markers, etc.) emit here.
- **ContextGraph** — the in-memory working set of the active run: port values, in-flight set, completed set, current branch leaf, active dial values. **A projection of the EventLog.** Rebuilt deterministically (modulo §1.2) on resume.
- **Run** — one execution of a graph against a session. A session may host serial runs (re-runs, follow-ups) and concurrent branches.
- **Branch** — a forked sub-run that shares an ancestor leaf with the trunk. Lives until merged or discarded. Multiple live branches are allowed concurrently.
- **Activity** — a higher-level reusable sub-graph (`plan`, `validate`, `decompose`, `summarize`, `ask`, `retrieve`). Treated as a node-kind (`ActivityNode`) by callers; expanded recursively at execution time.
- **Dial** — a user-tunable knob on budget or behavior. Cascading scopes: global → project → session → per-run override → per-node override.
- **Compaction Strategy** — a named algorithm that shrinks the ContextGraph or message-history slice (4 kinds: `summary`, `drop_oldest`, `semantic_cluster`, `custom_subgraph`).
- **Memory Hook** — a kernel-managed, automatic memory-write trigger that fires on a specific kernel boundary (post-LLM, post-tool, post-user-message, on-branch-close, on-session-end, on-explicit-pin). Greedy in v1.
- **Kernel boundary** — a deterministic firing point inside the kernel where state-emitting hooks may run. Nine boundaries in v1: post-LLM, post-tool, post-user-message, pre-branch-fork, on-branch-close, on-merge, on-session-end, on-checkpoint, on-explicit-pin.

---

## 3. User stories

- **US1 — Plan-execute-validate flow**: "refactor `core/llm/` to use the new ContentBlock type everywhere". Kernel runs `Plan` (Sonnet) → fan-out per file (Haiku per file) → `Validate` (Sonnet) → `Summarize` (Haiku) → output. User sees plan first, per-file progress chips, then summary.
- **US2 — Branch off for a sub-problem**: 30 turns into a Sonnet code review. Right-click last assistant turn → "Branch from here" → kernel **recommends Haiku** for the contained lookup → ask "what's the latest version of this dep?" → branch returns in 3 turns → kernel detects "task done" and **suggests merge** → click "Merge back" → trunk gains a one-line summary; branch preserved as side conversation.
- **US3 — Discard a branch**: Same as US2 but the answer wasn't useful. Click "Discard". Trunk untouched; branch soft-deleted with audit log.
- **US4 — Side-by-side compare**: Two branches off the same fork point, one Claude, one GPT-4o. Tree sidebar shows both live. Click "Compare" → two-column render.
- **US5 — Enterprise context corpus**: Drop a 200-file repo into a `internal-wiki` corpus. Kernel ingests + chunks + embeds. `ContextReadNode` retrieves top-K with citations.
- **US6 — Reflect-then-revise**: User asks for a customer email draft. `LLMNode` produces v1 → `ReflectNode` (same model or escalated) critiques → if critique severity > threshold, loop back through revision; otherwise emit final. Bounded by `max_iterations`.
- **US7 — Review with iteration**: Coding task. `LLMNode` produces patch → `ReviewNode` runs a reviewer pass → if review fails, re-run author with reviewer feedback as input → cap at 3 iterations → if still failing, escalate.
- **US8 — Memory recall mid-run**: User says "remember when we discussed pricing last month?" → `MemoryReadNode` retrieves greedy-stored memory chunks scoped to project → kernel injects into the next `LLMNode` call.
- **US9 — Compaction kicks in**: Long-running session approaches token budget. Pre-call compaction (configured = `summary`) fires → drops in a summary message → call proceeds within budget. User never had to ask.
- **US10 — Cost cap fires**: Run hits `max_cost_usd` dial. Kernel pauses the run, emits a `cost_cap_hit` event, surfaces a modal: "continue ($0.50 more), increase cap, or stop?"
- **US11 — Resume after crash**: Harness crashes mid-run. On next launch, the session shows "Resume?" — kernel rebuilds ContextGraph from EventLog and picks up at the next-ready node (durable, not strict-replay; see §1.2).
- **US12 — Trace inspection**: Open the trace sidebar for a completed run. See node tree (Plan → fan-out → Validate → Summarize) with per-node duration / model / token counts. Click any node → prompt / output / tool calls. Failed nodes red.
- **US13 — Ask the user**: Mid-run the kernel hits an `AskNode` with a clarifying question. Run pauses indefinitely (no timeout — user owns the run) until the user answers in chat. Answer feeds back into the graph.
- **US14 — Escalate on uncertainty**: Small/cheap model encounters a question its self-confidence signal flags as low. `EscalateNode` swaps to Sonnet, re-runs the failed leg, returns.
- **US15 — Custom compaction subgraph**: Power user defines their own compaction as a graph (LLM-driven semantic clustering + a tiny TransformNode dedupe pass). Saves it as a named compaction strategy. Other graphs reference it.

---

## 4. Functional requirements

### 4.1 Graph DSL + types

- **FR-001** New `core/agentgraph/` package with `GraphSpec`, `Node`, `Edge`, `NodeKind`, `Port` types. YAML on disk; JSON over RPC; runtime is Go structs. Bidirectional conversion utilities.
- **FR-002** Validation: cycle detection (allowed only inside `LoopNode` / `RetryNode` / `ReviewNode` bodies); type check on edges; no-orphan nodes (every node reachable from at least one entrypoint); each node has the right number of input/output ports per kind; declared dial overrides reference known dials; declared activity references resolve.
- **FR-003** **Default graph**: a built-in `toolloop_default.yaml` graph that reproduces the current `core/toolloop/` semantics. Sessions without an explicit graph attach this default at run-start. The default graph is loaded from the embedded `core/agentgraph/library/` and is overridable per-project at `<DataDir>/agent_graph/library/toolloop_default.yaml`.

### 4.2 Compute node primitives

- **FR-004** `LLMNode`: provider+model selection, system prompt template (string interpolation), tool allowlist, max tokens, temperature, JSON-output schema (optional). Streams output through the existing chat stream when on the active branch's leaf.
- **FR-005** `ToolNode`: tool name (`<server>__<tool>`) + args; permission gate runs the same as toolloop (Cedar — see §4.10).
- **FR-006** `TransformNode`: registry of named transforms (`extractCodeBlocks`, `parseJSON`, `formatMarkdown`, `sha256`, `truncateAt`, etc.); user-extensible via Go-side registration.
- **FR-007** `ActivityNode`: references a sub-graph by ID. Activities ship as bundled YAML (`plan`, `validate`, `decompose`, `summarize`, `ask`, `retrieve`, `reflect`, `review`).
- **FR-008** **`ReflectNode` (NEW)**: explicit critique/revision step over recent trace + outputs. Configurable `model` (defaults to "same as upstream `LLMNode`"; supports escalation). Output: a structured `{critique, suggested_revision_diff}`. Composes with `LoopNode` to form self-refine loops. Has its own `max_iterations` cap (mandatory) when used in a loop.
- **FR-009** **`ReviewNode` (NEW)**: control-flavored compute gate. Runs a reviewer pass over the upstream output; if review verdict = `fail`, re-runs the upstream sub-graph with reviewer feedback fed in; if `pass`, lets the output flow through. Mandatory `max_iterations` cap (default 3); on cap-hit, escalates via `EscalateNode` or fires `escalation_hit` event.
- **FR-010** `PlanNode` (kernel-decided invocation): when the kernel's task-complexity heuristic exceeds a threshold (configured by the `plan_threshold` dial), it inserts a planning step before the next LLM call. Verbosity dial: `terse` / `standard` / `verbose`. User can disable per-session.
- **FR-011** `AskNode`: emits a clarifying question into the active chat surface and **blocks the run** until the user answers. **No timeout** (user owns the run). The kernel persists the pause as a `pending_ask` event; if the harness restarts, the question is still pending.
- **FR-012** `EscalateNode`: fires on (a) budget exhaustion (any dial-cap), (b) model-uncertainty signal from an upstream `LLMNode` (configurable threshold), or (c) `ReviewNode` cap-hit. Swaps to a configured larger model; re-runs the upstream leg. One escalation per leg; further failures hard-error.

### 4.3 Control node primitives

- **FR-013** `BranchNode`: condition expression (small Go-side expression evaluator: eq / lt / gt / and / or / not over named inputs); outputs flow to one of `next_*` ports.
- **FR-014** `ParallelNode` + `JoinNode`: fan-out edges spawn concurrent runs of the downstream subgraph; the join collects outputs in declared order. Concurrency capped per dial.
- **FR-015** `LoopNode`: bound by `max_iterations` (mandatory) AND `condition` (optional). Kernel rejects unbounded loops at validation.
- **FR-016** `RetryNode`: bound by `max_attempts` (mandatory); exponential backoff (configurable base + cap); selects next attempt based on outcome (success / retryable error / fatal error).
- **FR-017** `ForkNode`: spawns a child branch (see §4.5 branching v1). Mandatory: parent leaf, branch title. Optional: model override (suggested by kernel), tool allowlist override, message subset.
- **FR-018** `MergeNode`: collapses a forked branch back into the parent. Mode = `append | summarize_append | replace_last_turn`. Suggested by kernel when "task done" / "question resolved" heuristics fire (see §4.5).

### 4.4 State node primitives (first-class)

- **FR-019** **`MemoryNode` (NEW; replaces stand-alone read+write split)**: read or write memory chunks. `mode = read | write | upsert`. NOT a tool — it is kernel-state. Scope: `global | project | session`. On write, content-hashes for dedup; on read, embedding-backed retrieval (delegates to `core/memory/retriever`). The kernel is the only writer to memory; tools cannot bypass.
- **FR-020** **`CorpusReadNode`**: top-K retrieval against one or more corpora, with provenance (`corpus_id`, `source_path`, `byte_offset`). Filters: `corpus_ids`, `source_path_prefix`, `mime_types`, `created_after`, `score_threshold`.
- **FR-021** **`CorpusWriteNode`**: enqueues an ingestion job for a corpus; idempotent on `(source_path, content_hash)`. Returns a job handle; status visible via `Corpora_Status`.
- **FR-022** **`AttachmentNode`**: handle to multimodal attachments (images, files, PDFs). Resolves an attachment reference into a content block usable by an `LLMNode`. Does not duplicate the existing `core/attachments` package — it's a graph-level adapter.
- **FR-023** `HistoryReadNode`: N most-recent messages from the active branch (or any branch by ID).
- **FR-024** `TraceWriteNode`: appends `(node_id, ts, severity, message, attrs)` to the run's audit trace.
- **FR-025** `CheckpointNode`: explicit checkpoint marker (the kernel checkpoints between every node-fire automatically; this node lets a graph author force a checkpoint at a logical boundary).

### 4.5 Memory: greedy first-class state with background prune

- **FR-026** Memory is **STATE, not a tool**. Tools cannot write memory; only the kernel and explicit `MemoryNode` instances can. Reads via `MemoryNode (mode=read)` or via the kernel's auto-injection (see hooks below).
- **FR-027** **Greedy automatic write hooks**: on every kernel boundary listed below, the kernel writes captured content to memory (claude-mem-style):
  - `post-LLM` — captures the assistant turn (full content, redacted per privacy filter).
  - `post-tool` — captures tool call + result (redacted).
  - `post-user-message` — captures the user turn.
  - `on-branch-close` — captures the branch's terminal output as a single chunk.
  - `on-session-end` — captures a session-summary (auto-summary triggered by an internal `summarize` activity).
  - `on-explicit-pin` — when the user clicks "pin to memory" in the UI or invokes a slash command.
  - `on-merge` — captures the merge result as a memory entry on the parent scope.
  - Hooks are **always-on** in v1 (no per-write selectivity). Configurable via the `memory_hooks_enabled` dial (default true). Hooks emit to the EventLog and the memory store atomically.
- **FR-028** **Background prune sweep (NEW)**: a periodic kernel-managed job (cadence dial, default 1/day per active project) that prunes greedy memory entries by:
  - **Staleness** — entries with no recall in N days (default 30) and no explicit pin.
  - **Age** — entries older than M days (default 365) and no explicit pin.
  - **Recall frequency** — entries below the K-th percentile of recall frequency in their scope.
  - **Embedding-cluster collapse** — clusters of near-duplicate entries (cosine ≥ 0.97) collapse to a single representative + count.
  - Pinned entries are immune to prune. The prune sweep is dry-run-able (UI surfaces "would-prune" preview).
- **FR-029** **Three scopes**: `global` (across all projects), `project` (per project), `session` (per session). Cascading retrieval: a `MemoryNode (mode=read)` queries all three scopes by default; scope filter narrows.
- **FR-030** **Content-hash dedup**: writes are no-ops if `(scope, content_hash)` already exists.
- **FR-031** **Embedding-backed retrieval**: existing `core/memory/embedder` + `retriever` is the engine; `MemoryNode` and the auto-inject path both call through it.
- **FR-032** **Slash commands** (`/memorize`, `/recall`, `/forget`) are an additional explicit-control surface that complements greedy hooks. Slash-commands ship in a parallel mission. This mission's `MemoryNode` exposes the same primitive operations the slash commands wrap. Cross-link: see the slash-commands mission's spec (TBD ID; will be filed at `kitty-specs/slash-commands-*`).

### 4.6 Branching v1 — intentionally simple

- **FR-033** **Compact-handoff fork**: `ForkNode` compacts the parent's active context (reusing the configured compaction strategy — see §4.7) into a small initial-input message bundle, spawns a new chat run on a kernel-recommended model, and feeds the compacted prompt as the branch's entry point.
- **FR-034** **Compact-summary merge**: `MergeNode` runs a compaction pass on the branch's full output (summary strategy by default) and lands the result on the parent as one message. Merge modes: `append | summarize_append | replace_last_turn` (default `summarize_append`).
- **FR-035** **Multiple live branches concurrently**: a session can host many branches simultaneously. Parent stays interactive while branches are alive — the parent run is not blocked.
- **FR-036** **Model recommendation heuristic**: kernel recommends smaller-or-larger model at fork time based on:
  - Task heuristic (containment: a self-contained code-write → smaller; a deep design question → larger).
  - Tool requirement (web_search → cheap tier OK; code-execution at scale → cheap tier OK).
  - Skill tag (if the user has tagged the fork with a skill, mapped via a `skill → model` table).
  - User overrides at fork-time UI.
- **FR-037** **Merge suggestion heuristic**: kernel suggests merge when:
  - Branch's last `LLMNode` output contains a terminal-state marker (configured tokens: "Done", "Resolved", "Final answer").
  - Branch's stated goal (captured at fork-time) is detected as satisfied via a small classifier `LLMNode`.
  - Branch has been idle for N minutes (default 5) with no in-flight nodes.
  - Suggestions are surfaced as UI prompts; the user accepts or dismisses.
- **FR-038** **Storage: copy-on-write**. Branches reference parent messages by ID until divergence. Forked messages stored only after the branch produces a new message. Migration table — see §4.11.
- **FR-039** **Cross-provider warn + best-effort convert**: forking from Anthropic to OpenAI (or vice versa) warns about content-block fidelity (image bytes, citations) and runs a best-effort converter; logs the fidelity loss to trace.
- **FR-040** **Defer richer fork/merge**: semantic merge, multi-branch merge, branch-of-branch, replay-into-parent are explicitly OUT OF SCOPE for v1. See §10.

### 4.7 Configurable compaction (its own subsystem)

- **FR-041** **Three invocation sites**:
  1. **Token-budget pre-call** — before any `LLMNode` fire, if the prepared input would exceed `(model_max_tokens × pre_call_threshold)` (dial; default 0.85), compaction fires.
  2. **Post-tool result trim** — after a `ToolNode` returns, if the tool result exceeds `tool_result_max_bytes` (dial; default 16 KiB), compaction fires on the result.
  3. **Manual user trigger** — UI button or slash command (`/compact`) — fires compaction on the current ContextGraph.
- **FR-042** **Four strategies**:
  - `summary` — LLM-driven summarization (configurable model; defaults to a cheap tier).
  - `drop_oldest` — remove oldest N messages until under threshold.
  - `semantic_cluster` — cluster messages by embedding similarity, keep representative per cluster.
  - `custom_subgraph` — user-supplied sub-graph (any `GraphSpec` with declared input/output ports of types `messages` → `messages`). Compaction itself is just another graph the kernel runs. Recursive but bounded (max depth dial; default 2).
- **FR-043** **Cascading config**: `global > project > session > per-run override > per-node override`. Each layer can specify per-site strategy + thresholds. A `LLMNode` can override pre-call compaction for its own scope.
- **FR-044** **Persistence**: compaction config layers stored alongside their scope (global in `<DataDir>/config/compaction.yaml`, project in project metadata, session in session row, per-run/per-node inline in graph spec).
- **FR-045** **Bound on compaction recursion**: a `custom_subgraph` strategy that itself triggers compaction is allowed but capped by the recursion dial; cycle detection halts a runaway.

### 4.8 Plan / Ask / Escalate / Reflect / Review (the agent verbs)

(Several FRs already cover individual nodes — FR-008 ReflectNode, FR-009 ReviewNode, FR-010 PlanNode, FR-011 AskNode, FR-012 EscalateNode. This sub-section lists their cross-cutting requirements.)

- **FR-046** All five agent-verb nodes emit dedicated event-log entries (`reflect_started`, `reflect_completed`, `review_pass`, `review_fail`, `plan_created`, `ask_pending`, `ask_answered`, `escalate_triggered`) for replay and UI surfacing.
- **FR-047** Verbosity / threshold / cap dials for each (see §4.9). All caps are mandatory in graph validation; the kernel rejects an unbounded `ReflectNode`-in-a-loop or `ReviewNode` without `max_iterations`.

### 4.9 Dials (user-facing budgets + behavior)

- **FR-048** **Budget dials** (per-run hard caps; kernel enforces):
  - `max_tokens_per_run` (default 200_000)
  - `max_wallclock_per_run_seconds` (default 1800)
  - `max_llm_calls_per_run` (default 100)
  - `max_tool_calls_per_run` (default 200)
  - `max_cost_usd_per_run` (default 5.00; estimated from provider rate cards)
- **FR-049** **Behavior dials**:
  - `plan_verbosity` — `terse | standard | verbose`
  - `plan_threshold` — task-complexity threshold above which `PlanNode` is inserted
  - `ask_threshold` — model-confidence below which `AskNode` may fire
  - `reflect_frequency` — `never | on_demand | every_n_turns`
  - `review_iterations_cap` — default 3
  - `compaction_aggressiveness` — `gentle | default | aggressive`
  - `escalation_enabled` — bool
- **FR-050** **Cascading scope** for every dial: `global > project > session > per-run override > per-node override`.
- **FR-051** **Surfacing**: dials live in Settings (global / project) and in a per-run override panel (sidebar of the chat surface). Each dial shows current effective value and the layer that contributed it.
- **FR-052** **Cap-hit semantics**: when a budget dial caps, the kernel pauses the run (NOT kills) and emits `cost_cap_hit` / `budget_cap_hit`. The user gets a modal: continue with raised cap, stop, or partial-merge.

### 4.10 Policy / safety

- **FR-053** **Cedar PolicyEngine** for action gating (already in tree at `core/policy/`). All gated actions run through `policy.Evaluate()`:
  - Tool execution (existing).
  - File writes (new for graph ops touching `<DataDir>` outside session-scoped paths).
  - Network requests from tool nodes.
  - Model selection (allow-list / deny-list per scope; e.g., enterprise project disallows GPT-4).
  - Memory writes (allow-list / deny-list of content categories; default permissive).
- **FR-054** **Per-run resource caps** are policy-enforced — see §4.9. Caps are NOT advisory; the kernel halts on cap-hit.

### 4.11 Storage

- **FR-055** Conversation tree (branching v1) — new tables `conversation_branches` + `conversation_messages` extending `session_messages`. **Migration 0306**.
- **FR-056** Corpora — new tables `corpora` + `corpus_chunks`. **Migration 0307**.
- **FR-057** Event log: `core/event/log/` already owns its own migration block (events / event_chain_heads / redaction_rules / retention_config in the event-block migrations). The session-block migration that adds `branch_id` to `session_messages` is **0306**; the event-log per-session topology is referenced by `session_id` (no new column needed there).
- **FR-058** Memory hook journal — new table `memory_hook_journal` recording every greedy-write attempt + outcome (written / dedup-skip / policy-blocked). **Migration 0308**.
- **FR-059** Cascading config persistence — global config at `<DataDir>/config/dials.yaml`; project config in `projects.config_json` (new column on `projects` table). **Migration 0309** adds `projects.config_json`.

Migration numbering is consistent with `core/session/migrations.go` (last-used = 0305 telemetry-otel). New session-block migrations are 0306 (branches), 0307 (corpora), 0308 (memory hook journal), 0309 (project config).

### 4.12 Graph executor + RPC

- **FR-060** Topological execution: BFS from entrypoint(s); fire a node when all input ports are ready; concurrency-bounded (default 8 in-flight nodes).
- **FR-061** Per-node telemetry: each fire emits an OTel span (`kind=node.<kind>`, `node_id`, `model_id`, `tool_name`, `duration_ms`, `status`).
- **FR-062** RPC: `Graphs_List`, `Graphs_Get`, `Graphs_Save`, `Graphs_Delete`, `GraphRuns_Start`, `GraphRuns_Status`, `GraphRuns_Cancel`, `GraphRuns_Resume`, `GraphRuns_List`, `GraphRuns_Trace`, `Branches_Fork`, `Branches_List`, `Branches_Switch`, `Branches_Merge`, `Branches_Discard`, `Corpora_*`, `Memory_*` (additions for hook journal queries), `Dials_Get`, `Dials_Set`.
- **FR-063** New `core/rpc/views/agentgraph/` view package wires bindings.

### 4.13 Frontend

- **FR-064** "Graphs" rail entry → `/graphs` route → list + YAML editor.
- **FR-065** SessionsView extensions:
  - "Branches" sidebar (live multi-branch tree) with fork / merge-suggest / discard / compare actions and **model-recommendation chip** at fork-time.
  - Active-graph indicator + "Run graph" / "Detach graph" buttons.
  - Trace inspector panel (collapsible) for the active run.
  - Dials panel (per-run override) collapsed by default.
- **FR-066** "Corpora" sub-tab — list / create / drop-folder-to-ingest / search.
- **FR-067** Memory inspector view — list greedy-stored entries per scope, pin/unpin, dry-run prune preview, delete.
- **FR-068** Compaction config UI (in Settings) — per-site strategy picker + thresholds; per-graph override editor.

---

## 5. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` passes; new packages add tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** **Backward compatibility**: existing chat sessions with no graph use the default graph (toolloop semantics) — zero user-visible regression. Migration 0306 imports them as single-branch trees seamlessly.
- **NFR-004** **Bounded execution**: every loop / retry / parallel-fanout / reflect / review has a hard cap. Validation rejects unbounded.
- **NFR-005** **Branch isolation**: a branch's writes (memory, attachments) do NOT leak to the parent unless an explicit `MergeNode` says so.
- **NFR-006** **Atomic per-file corpus ingestion**: a crash mid-ingestion of file N leaves the corpus consistent (file N either fully embedded or not present).
- **NFR-007** **Durable resume**: graph topology replays deterministically from the EventLog. LLM token-level outputs may differ on resume (see §1.2 — explicit relaxation vs strict Temporal-style replay).
- **NFR-008** **Per-run resource caps**: every dial cap is kernel-enforced.
- **NFR-009** **Telemetry**: every node fire produces an OTel span (rides on `telemetry-otel` mission).
- **NFR-010** **GUI-only**: no CLI surface. No headless mode.
- **NFR-011** **Single-user**: single-user-per-install assumption; no multi-tenant code paths.
- **NFR-012** **Privacy**: all state under `<DataDir>`. No network calls except provider-configured LLM endpoints + user-installed MCP servers. Telemetry verbose-attribute toggle defaults off.
- **NFR-013** **Memory storage growth bound**: greedy hooks + background prune keep memory storage growth sub-linear in run count past steady state. Acceptance metric: 1000-turn session adds ≤ 50 MiB to memory store post-prune.
- **NFR-014** **Compaction config divergence**: at any layer, `Dials_Get(layer, dial_name)` returns the effective value plus the layer that contributed it — the cascading config never silently wins.
- **NFR-015** **EventLog single source of truth**: ContextGraph rebuilt from EventLog must be byte-equal (modulo timestamps and LLM token-level outputs) on a clean resume.

---

## 6. Acceptance walkthroughs

(Concrete user journeys; each should run end-to-end as an integration test fixture.)

- **A1** **Plan-execute-validate** (US1): synthesize a 4-node graph (`Plan` Sonnet → fan-out 5x `LLMNode` Haiku → `Validate` Sonnet → `Summarize` Haiku). Run end-to-end. Trace shows the tree.
- **A2** **Smaller-model branch + summarize-merge** (US2): fork from a Sonnet trunk; kernel recommends Haiku (UI chip visible); branch resolves question; kernel suggests merge; user accepts; `summarize_append` runs; trunk gains one summary message.
- **A3** **Discard branch** (US3): trunk untouched; branch soft-deleted; audit log entry present in EventLog.
- **A4** **Side-by-side compare** (US4): two live branches off the same fork point; compare view renders both.
- **A5** **Corpus ingest + retrieval** (US5): drop 50-file markdown folder; ingestion progress visible; `CorpusReadNode` retrieves top-K with citations rendered as clickable links.
- **A6** **Reflect-then-revise loop** (US6): `LLMNode` v1 → `ReflectNode` (severity = high) → loop back → `LLMNode` v2 → `ReflectNode` (severity = low) → emit. Bounded at 3 iterations.
- **A7** **Review with iteration** (US7): coding task; `ReviewNode` fails twice then passes on iteration 3; `max_iterations` cap respected.
- **A8** **Memory recall mid-run** (US8): drop a fact in turn 5; turn 30 references it; `MemoryNode (mode=read)` returns the chunk via embedding retrieval; assistant response includes it.
- **A9** **Greedy memory write hooks fire** (NEW): single 4-turn conversation produces N memory entries where N ≥ 4 (one per `post-LLM` boundary) plus tool-result entries plus user-message entries. `Memory_List(scope=session)` shows them all. EventLog has matching `memory_write` events.
- **A10** **Background prune sweep** (NEW): seed 1000 memory entries, simulate 30 days of zero recall, run prune; staleness rule removes the unrecalled set; pinned entries survive; embedding-cluster collapse merges 100 near-dupes to representatives. Dry-run preview matches actual prune.
- **A11** **Compaction kicks in (pre-call)** (US9): run a session up to 90% of model context budget; the next `LLMNode` fire triggers `summary` strategy compaction; call proceeds within budget; trace records `compaction_fired` event.
- **A12** **Compaction kicks in (post-tool)**: a tool returns 500 KiB; post-tool compaction fires (`drop_oldest` strategy on irrelevant past tool outputs OR `summary` on the new output, per config); resulting context is under cap.
- **A13** **Manual compaction**: user clicks "compact now"; configured strategy fires.
- **A14** **Custom-subgraph compaction**: user defines a 3-node compaction sub-graph; references it as a strategy; compaction site invokes it; recursive compaction inside is bounded.
- **A15** **Cost cap fires** (US10): set `max_cost_usd_per_run = 0.10`; run a Sonnet-heavy graph; kernel pauses on cap; modal lets user continue/stop.
- **A16** **Resume after crash** (US11): kill the harness mid-run; restart; resume from EventLog; downstream nodes complete. Topology re-derives identically; LLM outputs may differ (documented relaxation).
- **A17** **Trace inspector** (US12): completed run; node tree rendered; failed node red; click reveals prompt / output / tool calls.
- **A18** **AskNode blocks indefinitely** (US13): mid-run `AskNode` fires; harness restarts; pending-ask survives restart; user answers an hour later; run resumes.
- **A19** **EscalateNode on uncertainty** (US14): cheap-model `LLMNode` reports low confidence; `EscalateNode` swaps to Sonnet; run completes.
- **A20** **Cascading dials**: set `max_tokens` differently at global/project/session/per-run; `Dials_Get` returns effective value + contributing layer at every step.
- **A21** **Cross-provider fork warning**: fork from Anthropic Sonnet to OpenAI GPT-4o; UI warns; image content blocks converted best-effort; trace records fidelity loss.
- **A22** **Default graph backward compat** (NFR-003): a session with no explicit graph runs the embedded `toolloop_default.yaml`; chat behavior is identical to the pre-mission toolloop.
- **A23** **Policy gate blocks**: a graph with a `LLMNode` referencing a deny-listed model fails to start; clear error.
- **A24** **EventLog is the truth** (NFR-015): start a run; force-kill; rebuild ContextGraph from EventLog; verify state matches a control instance that ran without the kill.

---

## 7. Architecture

```
core/agentgraph/
├── spec.go                    # GraphSpec, Node, Edge, NodeKind, Port + YAML/JSON
├── validator.go               # cycle detection, edge type-check, port-count, dial refs
├── kernel.go                  # graph executor: BFS firing, concurrency cap, hooks
├── kernel_resume.go           # ContextGraph rebuild from EventLog
├── kernel_test.go
├── nodes/
│   ├── compute/
│   │   ├── llm.go             # LLMNode
│   │   ├── tool.go            # ToolNode
│   │   ├── transform.go       # TransformNode
│   │   ├── activity.go        # ActivityNode
│   │   ├── reflect.go         # ReflectNode (NEW)
│   │   └── review.go          # ReviewNode (NEW)
│   ├── control/
│   │   ├── branch.go
│   │   ├── parallel.go        # ParallelNode + JoinNode
│   │   ├── loop.go
│   │   ├── retry.go
│   │   ├── fork.go            # ForkNode (compact-handoff fork)
│   │   ├── merge.go           # MergeNode (compact-summary merge)
│   │   ├── plan.go            # PlanNode
│   │   ├── ask.go             # AskNode
│   │   └── escalate.go        # EscalateNode
│   ├── state/
│   │   ├── memory.go          # MemoryNode (read|write|upsert)
│   │   ├── corpus_read.go
│   │   ├── corpus_write.go
│   │   ├── attachment.go      # AttachmentNode (NEW)
│   │   ├── history_read.go
│   │   ├── trace_write.go
│   │   └── checkpoint.go
│   └── registry.go
├── activities/                # bundled YAML activities
│   ├── plan.yaml
│   ├── validate.yaml
│   ├── decompose.yaml
│   ├── summarize.yaml
│   ├── ask.yaml
│   ├── retrieve.yaml
│   ├── reflect.yaml
│   └── review.yaml
├── library/
│   └── toolloop_default.yaml  # the default graph (toolloop semantics)
├── transforms.go
├── compaction/
│   ├── strategies.go          # summary, drop_oldest, semantic_cluster, custom_subgraph
│   ├── invocation.go          # 3 invocation sites: pre_call, post_tool, manual
│   ├── config.go              # cascading global > project > session > per-run > per-node
│   ├── compaction_test.go
├── memory_hooks/
│   ├── hooks.go               # 9 boundaries; greedy writes
│   ├── prune.go               # background prune sweep (staleness / age / recall / cluster)
│   └── hooks_test.go
├── runs/
│   ├── run.go
│   ├── trace.go
│   ├── store_sql.go
│   └── eventlog_projection.go # ContextGraph projection from EventLog
└── doc.go

core/conversation/
├── branches.go                # Branch type + Manager (copy-on-write)
├── store_sql.go
└── *_test.go

core/corpus/
├── corpus.go
├── ingest.go
├── chunker.go
├── search.go
├── store_sql.go
├── vector_store.go            # chromem-go-style per-corpus vector DB
└── *_test.go

core/session/migrations.go     # MODIFIED: register 0306 / 0307 / 0308 / 0309
core/session/migrations_branches.go     # NEW (0306)
core/session/migrations_corpora.go      # NEW (0307)
core/session/migrations_memory_hook.go  # NEW (0308)
core/session/migrations_project_config.go  # NEW (0309)

core/rpc/views/agentgraph/
├── api.go
├── impl.go
└── *_test.go

core/rpc/api.go                # MODIFIED: kernel + corpus + dials wiring
core/rpc/bindings.go           # MODIFIED: Graphs_*, GraphRuns_*, Branches_*, Corpora_*, Memory_*, Dials_*

frontend/src/views/graphs/
├── GraphsView.vue
├── GraphSpecEditor.vue
└── __tests__/

frontend/src/views/sessions/
├── SessionsView.vue           # MODIFIED
├── BranchesSidebar.vue        # NEW
├── BranchCompare.vue          # NEW
├── GraphTraceInspector.vue    # NEW
├── DialsPanel.vue             # NEW (per-run override)
├── MergeSuggestToast.vue      # NEW
└── __tests__/

frontend/src/views/knowledge/
├── KnowledgeView.vue
├── CorpusEditor.vue
└── __tests__/

frontend/src/views/memory/
├── MemoryInspectorView.vue    # NEW: list greedy entries per scope; pin; dry-run prune
└── __tests__/

frontend/src/views/settings/
├── CompactionConfigView.vue   # NEW
├── DialsView.vue              # NEW (global + project)
└── __tests__/

frontend/src/lib/types.ts      # MODIFIED: GraphSpec, Branch, Corpus, GraphRun, Trace, Dial, MemoryEntry
frontend/src/lib/harnessClient.ts  # MODIFIED
frontend/src/main.ts           # MODIFIED: /graphs + /knowledge + /memory routes
frontend/src/shell/LeftRail.vue
```

---

## 8. Edge cases

1. **Graph with isolated subgraph** — validator rejects.
2. **Cycle outside `LoopNode` / `RetryNode` / `ReviewNode`** — validator rejects.
3. **Fork off a discarded branch** — rejected; discarded branches are read-only.
4. **Cross-branch merge** (target ≠ direct parent) — allowed with warning.
5. **Branch model override = unconfigured model** — fork rejected at creation.
6. **Corpus ingestion of 1 GB folder** — runs in background; resumable across harness restart.
7. **Resume after a node-kind was removed** — graph fails to load with a clear "missing implementation" error.
8. **Run cancellation mid-fork** — child branch's in-flight nodes cancelled via ctx; partial results discarded; trace recorded.
9. **Loop body emits a fork** — allowed; each iteration spawns its own branch with iteration-indexed branch IDs.
10. **MergeNode targeting a discarded parent** — error.
11. **Conversation tree with 1000 branches** — list rendering virtualizes; tree-walk APIs paginated.
12. **Corpus deleted while a graph references it** — graph runs return "corpus not found".
13. **Migration 0306 on existing DB** — every existing session gets a synthetic trunk branch (`<sessionID>:trunk`); existing messages re-keyed.
14. **`AskNode` pending across harness restart** — pending-ask survives; user can answer in the next session.
15. **`ReviewNode` cap-hit with no `EscalateNode` configured** — emits `review_failed_unrecoverable` and halts the run cleanly (not a panic).
16. **Greedy memory hook fires on a redacted message** — only the redacted form is written; original never touches the memory store.
17. **Background prune deletes a chunk that's currently being retrieved** — retrieval uses a snapshot read; prune runs only between retrievals (read-write lock on memory store).
18. **Custom-subgraph compaction loops infinitely via recursion** — recursion-depth dial halts; emits `compaction_recursion_cap`.
19. **Cost cap hit while `AskNode` is pending** — the user's answer still flows back; the cap-hit modal queues until user resumes.
20. **Cross-provider fork with image content** — converter logs fidelity warnings; if conversion fails (e.g., unsupported MIME), branch fails to start with clear error.

---

## 9. Out of scope (v1)

- **A2A external delegation** (delegating sub-tasks to external agents over the `a2aproject/a2a-go` SDK). The graph kernel is the right abstraction for **internal** local orchestration. A future `A2AClientNode` could wrap external agents — see §11 rationale. Marked **v2+**.
- **IBM/LF ACP and Zed Agent Client Protocol** as primary orchestration surfaces. See §11.
- **Visual node editor** (drag-and-drop authoring). YAML editor only.
- **AST-aware chunking via tree-sitter**. Line-based / heading-based v1; AST chunker is a follow-up.
- **Cloud sync / multi-machine state**.
- **Multi-user / multi-tenant**.
- **Custom CLI**.
- **Advanced fork/merge semantics**: semantic merge, multi-branch merge, branch-of-branch, replay-into-parent. Branching v1 is intentionally simple.
- **Strict Temporal-style deterministic replay** of LLM token streams — see §1.2.
- **Live graph editing while running**.
- **Cross-corpus / cross-project semantic search at chassis level** — per-call only.
- **AGI / unattended autonomy**.
- **Stateful tool plugins** — state lives in `MemoryNode` / state nodes.
- **Branch ACLs / permissions** beyond Cedar policy on actions.
- **Streaming graph re-execution / mid-run replay**.
- **Multi-model ensemble voting**.
- **Custom embedder per corpus** beyond the existing OpenAI embedder + future local sentence-transformers.

---

## 10. Open questions

1. **Expression language for `BranchNode`** — hand-rolled tiny evaluator (eq/lt/gt/and/or/not) for v1; CEL deferred unless complexity demands it.
2. **Skill → model recommendation table location** — ship a default `<embedded>/skills.yaml`; user override at `<DataDir>/agent_graph/skills.yaml`. Format / vocabulary not yet finalized.
3. **`PlanNode` task-complexity heuristic** — likely a small classifier `LLMNode` (cheap-tier) on the user input. Open: should the heuristic be a hard-coded rule set or a learned classifier? Default: classifier `LLMNode`.
4. **Kernel boundaries for memory hooks** — nine listed in §4.5. Are there boundaries we're missing (e.g., `pre-LLM` for context-shaping)? Defaults are conservative; revisit after A9 / A10 results.
5. **Background prune cadence** — default 1/day per active project, but should also fire on memory-store-size cap. Open: what's the size cap default?
6. **Cross-provider content conversion** — image bytes survive base64 round-trip but provider-specific citation blocks don't. Open: when conversion is lossy, do we warn-and-proceed or hard-fail? Default = warn-and-proceed.
7. **`A2AClientNode` shape** — when v2 lands, the node would wrap `github.com/a2aproject/a2a-go` and present an external A2A agent as a primitive. Out of scope here; included in §11 as the future escape valve.
8. **EventLog retention vs. project lifetime** — the existing event-log retention sweeps run on a separate cadence. Open: do we want per-project EventLog archival on project-archive?

---

## 11. ACP / external orchestration rationale (decision: skip both)

The harness **does not** adopt either of the candidate Agent Communication Protocols as a primary orchestration surface. Rationale:

- **IBM / Linux Foundation ACP** is merging into A2A under the Linux Foundation. Its shape — HTTP REST, distributed-cloud orchestration across heterogeneous agents — is overkill for a single-machine local-only desktop harness. We'd be paying the cost of a network protocol abstraction for an entirely in-process system.
- **Zed Agent Client Protocol** is editor↔agent stdio, designed for a code editor to drive an external agent. The shape is **inverted** for our use case: we are the agent harness, not an editor invoking one. Adopting it would require us to pretend to be both ends of the conversation.

The graph kernel itself is the correct abstraction for **internal** local orchestration. It gives us typed primitives, durable execution, branching, and dial-bound budgets without taking on protocol baggage.

**Future escape valve**: if a user genuinely needs to delegate a sub-task to an external A2A agent (e.g., a hosted research service exposing the A2A protocol), an `A2AClientNode` can wrap `github.com/a2aproject/a2a-go` and present that external agent as a primitive in the graph. This is **v2+** and explicitly out of scope here. The kernel's design does not hard-code any assumption that would prevent it.

---

## 12. Migration path

- Sessions without graphs continue to work via the embedded default graph (FR-003). No user action required.
- Existing memory/attachments/corpora are projections accessible from State nodes — no destructive migration.
- Existing `session_messages` rows are backfilled into a synthetic trunk branch on migration 0306.
- New SQLite migrations:
  - **0306** — conversation tree (branches + branch_id column).
  - **0307** — corpora storage.
  - **0308** — memory hook journal.
  - **0309** — project config (compaction + dials cascading).
- Event log migrations live in their own block (`core/event/log/migrations/`) and are unchanged by this mission.
- `core/toolloop/` is **NOT removed** — its in-process API stays as the implementation backing the default graph's `LLMNode` / `ToolNode` execution path. Long-term a follow-up mission may collapse it into pure node-implementations.

---

## 13. Mission shape

This mission is large. Scope is grouped into **four bundles** (see plan.md):

- **Bundle A — Graph kernel** (DSL+types+validator, compute primitives incl. ReflectNode/ReviewNode, control primitives, state primitives + executor + greedy memory hooks, activities, RPC/UI).
- **Bundle B — Branching v1 simple** (tree storage + migration; branch RPC ops + Fork/Merge real impls; frontend simple-branch sidebar with model-recommendation, live multi-branch view, merge-suggest UI).
- **Bundle C — Corpora** (types + ingestion + migration; retrieval + RPC + frontend).
- **Bundle D — Configurable compaction** (3 invocation sites, 4 strategies, custom subgraph, cascading config plumbing).

Plus a **Polish bundle** (docs, integration tests, dial UI polish, memory inspector polish).

Bundles A and D are gated together (compaction reuses kernel primitives heavily and several FRs cross-reference). B and C ship independently after A.

---

## 14. Out-of-band dependencies

- Existing in-tree: `core/llm`, `core/toolloop` (kept; backs default graph), `core/memory`, `core/attachments`, `core/contexts`, `core/telemetry`, `core/event`, `core/policy`, `core/storage`.
- `gopkg.in/yaml.v3` (already in tree) for DSL.
- (Deferred) `github.com/smacker/go-tree-sitter` for AST chunking — not v1.
- (Deferred / v2+) `github.com/a2aproject/a2a-go` for an `A2AClientNode` — not v1.
- No new third-party LLM SDK.

---

## 15. Follow-up: manifest-driven node catalog

The full canonical kind list (29 callable kinds + 6 archetypes), the
inheritance taxonomy (`compute`, `control`, `state` → `read` / `write`
/ `marker`), the codegen flow, the alias migration table, and the
user-override seam are owned by the follow-up mission
`agent-kernel-graph-node-catalog-01KQ7JDZ`. See
[`docs/agent-kernel-graph-node-catalog.md`](../../docs/agent-kernel-graph-node-catalog.md)
for the working reference and
[`docs/migration-from-old-kind-names.md`](../../docs/migration-from-old-kind-names.md)
for the rename guide. This parent spec talks about the kernel + run
control surface in narrative prose; the node catalog is the
load-bearing data.
