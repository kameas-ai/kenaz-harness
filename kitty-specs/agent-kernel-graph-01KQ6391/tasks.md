# Tasks: Agent kernel graph

**Mission ID**: `agent-kernel-graph-01KQ6391`
**Spec**: `kitty-specs/agent-kernel-graph-01KQ6391/spec.md`
**Plan**: `kitty-specs/agent-kernel-graph-01KQ6391/plan.md`

Conventions:
- **Complexity**: S (≤ half-day) | M (½ – 1 day) | L (1 – 2 days). Anything over L should be split.
- **Deps**: other WP/task IDs that must land first.
- **AC**: acceptance criteria (1–2 bullets).
- **Tests**: unit / integration / frontend, per-task.

---

## Bundle A — Graph kernel

### WP01 — DSL + types + validator + default graph

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP01-T1 | Define `GraphSpec`, `Node`, `Edge`, `NodeKind`, `Port` Go structs in `core/agentgraph/spec.go`. | S | — | Types compile; round-trip JSON marshal/unmarshal. | unit (round-trip) |
| WP01-T2 | YAML serialization helpers (`MarshalYAML` / `UnmarshalYAML`) using `gopkg.in/yaml.v3`. | S | WP01-T1 | YAML ↔ Go ↔ JSON round-trips byte-equal (modulo formatting). | unit (round-trip on fixtures) |
| WP01-T3 | `validator.go`: cycle detection (allowed only inside `LoopNode` / `RetryNode` / `ReviewNode` bodies). | M | WP01-T1 | Synthetic graph fixtures (one valid, one cycle-outside-loop, one cycle-inside-loop) classified correctly. | unit |
| WP01-T4 | `validator.go`: edge type-check + port count + orphan node detection. | M | WP01-T1 | Fixtures: type-mismatched edge rejected; orphan node rejected; port-count error reported with node ID. | unit |
| WP01-T5 | Validator: dial reference + activity reference resolution. | S | WP01-T1, WP05-T1 (loose; activity registry can stub initially) | Unknown dial ref or unknown activity ref produces a clear error. | unit |
| WP01-T6 | Embed `library/toolloop_default.yaml` (the default graph). Reproduce current toolloop semantics (LLMNode → Tool fan-out → loop). | M | WP01-T1, WP02-T1 | YAML loads + validates; semantics-spec doc commented at the top. | unit (validator passes) |
| WP01-T7 | Built-in default-graph attach: when a session has no graph, kernel attaches `toolloop_default.yaml` at run-start. | S | WP01-T6, WP04-T1 | A new session with no graph begins running the default graph; the existing toolloop integration test passes through. | integration |

### WP02 — Compute primitives

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP02-T1 | `LLMNode` impl: provider+model select, system-prompt template, tool allowlist, max tokens, JSON schema. | M | WP01-T1 | Stub-LLM fixture: deterministic response; streams to chat surface when on active leaf. | unit + integration |
| WP02-T2 | `ToolNode` impl: name + args; permission gate via `core/policy/policy.Evaluate`. | M | WP01-T1 | Stub tool registry; deny-list rejects; allow-list runs. | unit |
| WP02-T3 | `TransformNode` impl + transform registry (`extractCodeBlocks`, `parseJSON`, `formatMarkdown`, `sha256`, `truncateAt`). | S | WP01-T1 | Each named transform runs deterministically on a fixture. | unit |
| WP02-T4 | `ActivityNode` impl: refs sub-graph by ID; recursive expansion at run-start. | M | WP01-T1, WP05-T1 | Activity reference resolves; expanded sub-graph fires; trace shows nested nodes. | unit + integration |
| WP02-T5 | `ReflectNode` impl: critique/revision over recent trace; configurable model; mandatory `max_iterations` when in a loop. | M | WP02-T1 | Fixture: ReflectNode produces `{critique, suggested_revision_diff}`; loop with cap-3 stops at 3. | unit |
| WP02-T6 | `ReviewNode` impl: reviewer pass; pass/fail; re-runs upstream on fail; mandatory `max_iterations` (default 3); cap-hit emits `review_failed_unrecoverable`. | M | WP02-T1 | Fixture: 2 fails then pass at iteration 3; separate fixture: 4 fails → cap-hit emits clean event (no panic). | unit |
| WP02-T7 | `PlanNode` impl + task-complexity heuristic (cheap-tier classifier `LLMNode`); verbosity dial. | M | WP02-T1 | Fixture: simple input → no plan; complex input → plan inserted; verbosity controls plan length. | unit |
| WP02-T8 | `AskNode` impl: emit question; **block run indefinitely** (no timeout); persist `pending_ask` event to EventLog. | M | WP01-T1, WP04-T2 | Fixture: AskNode pauses run; injected user-answer resumes; pending-ask survives a simulated harness restart. | unit + integration |
| WP02-T9 | `EscalateNode` impl: trigger conditions (budget, uncertainty, ReviewNode cap); swap to configured larger model; one escalation per leg. | M | WP02-T1, WP02-T6 | Fixture: low-confidence upstream LLMNode → EscalateNode swaps to Sonnet → re-runs; second failure hard-errors. | unit |
| WP02-T10 | Cedar policy gate on `LLMNode` model-selection (allow-list/deny-list); `ToolNode` already gated. | S | WP02-T1, WP02-T2 | Fixture: deny-listed model fails to start with clear error. | unit |

### WP03 — Control primitives

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP03-T1 | `BranchNode`: condition evaluator (eq/lt/gt/and/or/not). | M | WP01-T1 | Fixture: condition true → port A; false → port B. | unit |
| WP03-T2 | `ParallelNode` + `JoinNode`: fan-out + ordered collect. | M | WP01-T1, WP04-T1 | Fixture: 5-way fan-out completes in declared order; join blocks on slowest. | unit + integration |
| WP03-T3 | `LoopNode`: `max_iterations` mandatory + optional `condition`. | S | WP01-T1 | Fixture: cap-3 loop runs 3 times; condition-false short-circuits. | unit |
| WP03-T4 | `RetryNode`: `max_attempts` mandatory + exponential backoff. | S | WP01-T1 | Fixture: 2 retryable errors then success at attempt 3; fatal error short-circuits. | unit |
| WP03-T5 | `ForkNode` STUB (real impl in WP08). Returns synthetic `branch_id`; emits `branch_fork` event. | S | WP01-T1 | Fixture: fork stub fires + emits event; downstream MergeNode stub merges trivially. | unit |
| WP03-T6 | `MergeNode` STUB (real impl in WP08). Trivial append. | S | WP01-T1, WP03-T5 | Pairs with WP03-T5 fixture. | unit |

### WP04 — State primitives + executor + EventLog projection + greedy memory hooks

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP04-T1 | Kernel executor `core/agentgraph/kernel.go`: BFS firing, in-flight semaphore (default 8), per-node OTel span. | L | WP01-T1, WP02-T1 | Fixture: 10-node DAG fires in topological order with concurrency cap; OTel spans verified. | unit + integration |
| WP04-T2 | Automatic per-fire checkpoint to `<DataDir>/agent_graph/runs/<run_id>/checkpoints/`. | M | WP04-T1 | After each node-fire, a JSON snapshot exists; resume picks the next-ready node. | unit |
| WP04-T3 | `kernel_resume.go`: rebuild ContextGraph from EventLog. | L | WP04-T1, WP04-T2 | Fixture: kill mid-run, resume from EventLog, downstream nodes complete; ContextGraph projection matches a control instance (modulo LLM tokens — NFR-007). | integration |
| WP04-T4 | `MemoryNode` impl (read|write|upsert); content-hash dedup; scope `global|project|session`. | M | WP01-T1 | Fixture: write + read returns same chunk; duplicate write is no-op. | unit |
| WP04-T5 | Migration **0306** scaffolding (placeholder; tables added in WP07). Register with session migration block. | S | — | Migration registers; idempotent. | unit |
| WP04-T6 | Migration **0308** memory hook journal. | S | — | Tables / indexes created; rollback works. | unit |
| WP04-T7 | Greedy memory hooks at the 9 kernel boundaries (post-LLM, post-tool, post-user-message, on-branch-close, on-session-end, on-explicit-pin, pre-branch-fork, on-merge, on-checkpoint). | L | WP04-T1, WP04-T4, WP04-T6 | Fixture: 4-turn conversation produces ≥4 post-LLM hook writes + tool-result + user-message writes; all logged in `memory_hook_journal`. | unit + integration |
| WP04-T8 | Memory write redaction pipeline integration: hooks pass content through `core/event/log/redact/` before persistence. | S | WP04-T7 | Fixture: redacted token (e.g., `[redacted: api_key]`) lands in memory store, original never touches it. | unit |
| WP04-T9 | `CorpusReadNode` + `CorpusWriteNode` stubs (real backing in WP10). | S | WP01-T1 | Stub returns deterministic empty result; emits trace event. | unit |
| WP04-T10 | `AttachmentNode` impl: resolves attachment ref via `core/attachments` into a content block. | M | WP01-T1 | Fixture: attachment ID resolves to image content block usable by LLMNode. | unit |
| WP04-T11 | `HistoryReadNode`, `TraceWriteNode`, `CheckpointNode` impls. | S | WP01-T1, WP04-T1 | Each fires correctly on fixtures. | unit |
| WP04-T12 | EventLog event kinds registered: `node_start`, `node_complete`, `tool_call`, `tool_result`, `memory_write`, `branch_fork`, `branch_merge`, `compaction_fired`, `dial_overridden`, `cost_cap_hit`, `pending_ask`, `ask_answered`, `escalate_triggered`, `review_pass`, `review_fail`, `reflect_started`, `reflect_completed`, `plan_created`. | M | WP04-T1 | All event kinds have validators in `core/event/kind/`; round-trip through chain + replay. | unit |

### WP05 — Activity sub-graphs

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP05-T1 | Activity loader: discover `core/agentgraph/activities/*.yaml` (embedded) + `<DataDir>/agent_graph/activities/*.yaml` (user override). | M | WP01-T1 | Loader returns map of activity ID → spec; user-override wins. | unit |
| WP05-T2 | Bundle YAML for `plan`, `validate`, `decompose`, `summarize`, `ask`, `retrieve`, `reflect`, `review`. Each is a small graph. | M | WP05-T1, WP02-* | Each activity loads + validates; `ActivityNode` referencing it runs successfully on fixture. | unit + integration |
| WP05-T3 | Activity version tag in YAML; loader records version in trace. | S | WP05-T1 | Fixture: activity v1 + v2 both available; trace shows which version was used. | unit |

### WP06 — RPC view + bindings (subset)

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP06-T1 | `core/rpc/views/agentgraph/api.go`: `Graphs_List/Get/Save/Delete`. | M | WP01-T1 | RPC round-trip: save spec, list, get back the same spec. | unit |
| WP06-T2 | `GraphRuns_Start/Status/Cancel/Resume/List/Trace` RPCs. | M | WP04-T1, WP04-T3 | Start a run; query status; cancel mid-run; resume from checkpoint; trace returns node tree. | unit + integration |
| WP06-T3 | `Memory_*` RPCs: list, get, pin/unpin, delete, hook-journal query. | M | WP04-T4, WP04-T7 | Round-trips for each method; hook-journal query returns expected entries for a fixture run. | unit |
| WP06-T4 | `Dials_Get(scope, dial_name)` returns effective value + contributing layer; `Dials_Set(scope, dial_name, value)`. | M | WP12-T3 (loose; structure can land first) | Cascading test: set at global, project, session; `Dials_Get` returns correct attribution at each level. | unit |
| WP06-T5 | `core/rpc/bindings.go` wiring + Wails bindings regen. | S | WP06-T1..T4 | Frontend `harnessClient.ts` types regen succeed. | build |

---

## Bundle B — Branching v1 simple

### WP07 — Tree storage + migration 0306

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP07-T1 | Migration 0306: `conversation_branches` table + `session_messages.branch_id` column + indexes. | M | WP04-T5 | Migration up + down; existing rows backfilled to synthetic trunk branch (`<sessionID>:trunk`). | unit |
| WP07-T2 | `core/conversation/branches.go`: `Branch` type + `Manager` (CRUD + tree walk + COW). | M | WP07-T1 | CRUD round-trips; tree walk returns nested children; COW: forked branch references parent messages by ID until first new message. | unit |
| WP07-T3 | `store_sql.go` for branches: list/get/create/update/discard; soft-delete preserves audit. | S | WP07-T2 | Discard sets `discarded_at`; row remains queryable. | unit |

### WP08 — Branch RPC ops + Fork/Merge real impls + cross-provider converter

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP08-T1 | `Branches_Fork/List/Switch/Merge/Discard` RPCs. | M | WP07-T2, WP06-T1 | RPC round-trips on a fixture session with 3 branches. | unit |
| WP08-T2 | `ForkNode` real impl: compact-handoff via configured compaction strategy; spawns child run on kernel-recommended model. | L | WP07-T2, WP12-T1 | Fixture: fork from Sonnet trunk; child run starts with compacted prompt; model = Haiku (recommended); EventLog shows `branch_fork`. | integration |
| WP08-T3 | `MergeNode` real impl: 3 modes (`append`, `summarize_append`, `replace_last_turn`); summarize_append default. | M | WP08-T2 | Each mode tested: trunk gains the expected message shape post-merge. | unit + integration |
| WP08-T4 | Model-recommendation heuristic: skill table + containment heuristic + override at fork-time. | M | WP08-T2 | Fixture: code-write task → Haiku; deep-design → Opus; user override wins. | unit |
| WP08-T5 | Merge-suggestion heuristic: terminal-state token detection + goal-satisfied classifier `LLMNode` + idle-timer (5 min default). | M | WP08-T3 | Fixture: branch ends with "Done" → merge suggested; idle 5 min → merge suggested; user can dismiss. | unit + integration |
| WP08-T6 | Cross-provider content converter (Anthropic ↔ OpenAI image/citation blocks); warn-and-proceed; fidelity-loss event. | M | WP08-T2 | Fixture: fork from Anthropic→OpenAI with image block; conversion succeeds; `convert_fidelity_loss` event present. | unit |
| WP08-T7 | Reject fork off discarded branch + fork with unknown model; clear error messages. | S | WP07-T3, WP08-T2 | Each rejection produces a typed error; UI surfaces it. | unit |

### WP09 — Frontend branches sidebar + compare view + model-recommendation chip + merge-suggest toast

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP09-T1 | `BranchesSidebar.vue`: live multi-branch tree; switch / fork / discard / compare actions. | L | WP08-T1 | Tree renders 5 branches; switching active leaf changes the chat surface; fork modal opens. | frontend (vitest) |
| WP09-T2 | Fork modal with **kernel-recommended model chip** + override; tool-allowlist override; message-subset selector. | M | WP08-T4 | Modal shows the recommended model with reason; user can override; submit fires `Branches_Fork`. | frontend |
| WP09-T3 | `BranchCompare.vue`: two-column compare view. | M | WP09-T1 | Two branches side-by-side; scroll-sync optional. | frontend |
| WP09-T4 | `MergeSuggestToast.vue`: dismissible toast surfacing kernel merge suggestions. | S | WP08-T5 | Suggestion fires on terminal-state; user can accept (runs MergeNode) or dismiss. | frontend |
| WP09-T5 | `SessionsView.vue` integration: branches sidebar + active-branch indicator + `Run graph`/`Detach graph` buttons. | M | WP09-T1, WP06-T2 | Active-branch chip updates on switch; run/detach buttons fire correct RPCs. | frontend + integration |

---

## Bundle C — Corpora

### WP10 — Corpus types + ingestion pipeline + migration 0307

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP10-T1 | Migration 0307: `corpora` + `corpus_chunks` tables + indexes. | S | WP04-T5 | Up + down clean. | unit |
| WP10-T2 | `core/corpus/corpus.go`: `Corpus` + `Chunk` types + CRUD. | S | WP10-T1 | Round-trip CRUD. | unit |
| WP10-T3 | Walker: traverse a source path (folder, repo, ZIP, PDF tree). | M | — | Fixture: 50-file folder yields 50 paths; ZIP extracts to temp dir. | unit |
| WP10-T4 | Chunker: markdown heading split / code line-based / PDF text / plain-text sliding window. (AST-aware deferred — line-based for code in v1.) | M | WP10-T3 | Each MIME type chunks deterministically on fixture. | unit |
| WP10-T5 | Embedder integration: existing `core/memory/embedder` (OpenAI). | S | WP10-T4 | Chunks embed; vectors persist to per-corpus chromem-go store at `<DataDir>/corpora/<corpus_id>/`. | unit |
| WP10-T6 | Atomic per-file commit (NFR-006): if process crashes mid-file, that file is either fully embedded or absent. | M | WP10-T5 | Fixture with simulated crash; only complete files persist. | unit |
| WP10-T7 | Re-ingest hash-based skip: unchanged files skipped on re-ingest. | S | WP10-T5 | 100-file corpus, change 1 file, re-ingest re-embeds only that one. | unit |
| WP10-T8 | Background goroutine + progress events on existing event broker. | M | WP10-T5 | Ingestion progress events emit; UI consumer can subscribe. | integration |

### WP11 — Corpus retrieval + RPC + frontend

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP11-T1 | `Corpus_Search(corpusIDs, query, k, filters)` returns top-K with provenance. | M | WP10-T5 | Fixture: query returns top-5 chunks with `corpus_id`, `source_path`, `byte_offset`, `score`. | unit |
| WP11-T2 | `CorpusReadNode` real backing (replace WP04-T9 stub). | S | WP11-T1 | `CorpusReadNode` returns retrieval results in graph fixture. | unit + integration |
| WP11-T3 | `Corpora_List/Create/Ingest/Status/Delete/Search` RPCs. | M | WP10-T2, WP10-T8, WP11-T1 | RPC round-trips. | unit |
| WP11-T4 | `KnowledgeView.vue` + `CorpusEditor.vue`: list + create + drop-folder-to-ingest + progress + search. | L | WP11-T3 | E2E: drop folder, see progress bar, search returns results with citations. | frontend + integration |
| WP11-T5 | Citation link rendering in chat surface: clickable link to source path with offset. | M | WP11-T2 | Fixture: assistant message containing `[corpus:<id>:source.md:120]` renders as clickable link. | frontend |
| WP11-T6 | Token-budget cap on `CorpusReadNode` output (default 16 KiB); spillover dropped + warning event. | S | WP11-T2 | Fixture: 50 KiB retrieval truncates to 16 KiB; `corpus_overflow_dropped` event emitted. | unit |

---

## Bundle D — Configurable compaction

### WP12 — Compaction strategies + invocation sites + cascading config

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP12-T1 | `core/agentgraph/compaction/strategies.go`: 4 strategies (`summary` LLM-driven, `drop_oldest`, `semantic_cluster`, `custom_subgraph` placeholder). | L | WP02-T1, WP04-T1 | Each strategy on a fixture reduces token count to under target; `summary` produces a single summary message. | unit |
| WP12-T2 | `invocation.go`: 3 sites (pre_call, post_tool, manual). Pre-call fires before LLMNode if (max_tokens × pre_call_threshold) exceeded. | M | WP12-T1 | Fixture: context near limit → pre_call fires; tool result over `tool_result_max_bytes` → post_tool fires; manual RPC fires on demand. | unit + integration |
| WP12-T3 | `config.go`: cascading layers (global > project > session > per-run > per-node) with attribution. `Dials_Get` returns effective value + contributing layer. | L | — | Set at global, project, session, per-run, per-node; `Dials_Get` returns correct value + attribution at each level. NFR-014 acceptance. | unit |
| WP12-T4 | Migration 0309: `projects.config_json` column. | S | WP04-T5 | Up + down clean. | unit |
| WP12-T5 | Compaction event emit: `compaction_fired` event with strategy + site + bytes-saved. | S | WP12-T2 | Fixture: every compaction site emits the event. | unit |

### WP13 — Custom-subgraph compaction strategy + recursion guard + compaction config UI

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP13-T1 | `custom_subgraph` strategy: invokes kernel recursively on a user-supplied `GraphSpec` with input/output ports `messages → messages`. | M | WP12-T1, WP04-T1 | Fixture: 3-node compaction sub-graph runs; produces compacted output. | unit |
| WP13-T2 | Recursion-depth dial (default 2); cycle detection; halts runaway with `compaction_recursion_cap` event. | M | WP13-T1 | Fixture: compaction subgraph that itself triggers compaction is bounded; halts cleanly. | unit |
| WP13-T3 | `CompactionConfigView.vue`: per-site strategy picker + thresholds (global / project / session). | M | WP12-T3 | UI shows current effective + contributing layer; saving fires `Dials_Set`. | frontend |
| WP13-T4 | `GraphSpecEditor.vue` extension: per-graph + per-node compaction override. | M | WP12-T3 | Spec editor surfaces compaction config; saving updates the GraphSpec. | frontend |
| WP13-T5 | `DialsView.vue`: global / project dials surface (budget + behavior dials). | M | WP12-T3 | All dials listed with effective + attribution; editable per scope. | frontend |

---

## Bundle E — Policy / dials / memory inspector / polish

### WP14 — Cedar policy wiring + cap-hit pause-not-kill

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP14-T1 | Extend `core/policy/policy.go` action categories: `tool_exec`, `file_write`, `network_request`, `model_select`, `memory_write`. | M | — | Each category has an Evaluate path; default policies are permissive. | unit |
| WP14-T2 | Wire policy gates: `LLMNode` model_select; `ToolNode` tool_exec (existing); `MemoryNode` memory_write; file/network gates on tool-side. | M | WP14-T1, WP02-T1, WP02-T2, WP04-T4 | Fixture: each gate denies a deny-listed action with clear error. | unit |
| WP14-T3 | Cap-hit pause-not-kill semantics: emit `cost_cap_hit`/`budget_cap_hit`; kernel pauses run; UI modal continues / stops / partial-merges. | M | WP04-T1, WP06-T2 | Fixture: low cost cap fires; run pauses; user resume increases cap; run completes. | unit + integration |
| WP14-T4 | `Policy_PreviewEffective(scope, action)` RPC for debugging policy. | S | WP14-T1 | RPC returns the effective allow/deny decision + matching rules. | unit |

### WP15 — Background memory prune sweep + memory inspector view

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP15-T1 | `core/agentgraph/memory_hooks/prune.go`: staleness rule (no recall in N days, default 30). | M | WP04-T7 | Fixture: 1000 entries, simulated 30-day silence, prune removes the unrecalled set; pinned survive. | unit |
| WP15-T2 | Age rule (older than M days, default 365). | S | WP15-T1 | Fixture: entries > 365d removed; pinned survive. | unit |
| WP15-T3 | Recall-frequency rule (below K-th percentile). | M | WP15-T1 | Fixture: bottom-decile recall removed; top-decile retained. | unit |
| WP15-T4 | Embedding-cluster collapse (cosine ≥ 0.97 → representative + count). | M | WP15-T1, WP10-T5 | Fixture: 100 near-duplicate entries collapse to 1 representative; count metadata = 100. | unit |
| WP15-T5 | Background scheduler: 1/day per active project; size-cap-driven trigger. | S | WP15-T1 | Scheduler fires on cadence; size-cap test fires the prune even off-cadence. | unit |
| WP15-T6 | Dry-run preview RPC: `Memory_PrunePreview` returns the would-prune set without mutating. | S | WP15-T1 | Preview matches actual prune output. | unit |
| WP15-T7 | `MemoryInspectorView.vue`: list entries by scope; pin/unpin; dry-run prune preview; delete. | L | WP15-T6, WP06-T3 | UI shows entries; pin/unpin updates state; dry-run preview displays would-prune set. | frontend |

### WP16 — Frontend dials panel + per-run override + global/project dials view

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP16-T1 | `DialsPanel.vue`: per-run override sidebar in `SessionsView.vue`. | M | WP06-T4 | Each dial shows effective value + contributing layer; per-run override saves and applies. | frontend |
| WP16-T2 | Global / project `DialsView.vue` already shipped in WP13-T5; this task ensures parity + per-run wiring. | S | WP13-T5, WP16-T1 | Per-run override visible from the global view as a layer. | frontend |
| WP16-T3 | Cap-hit modal UI (continues / stops / partial-merges) wired to WP14-T3 pause-not-kill. | M | WP14-T3 | Fixture: cap fires → modal shows → user clicks Continue → run resumes with raised cap. | frontend + integration |

### WP17 — Polish + docs + integration tests

| ID | Task | Complexity | Deps | AC | Tests |
|---|---|---|---|---|---|
| WP17-T1 | Integration fixture: A1 plan-execute-validate. | M | All Bundle A | End-to-end run fires correctly; trace renders. | integration |
| WP17-T2 | Integration fixture: A2 (smaller-model branch + summarize-merge) + A3 (discard) + A4 (compare). | M | Bundle B | All three pass. | integration |
| WP17-T3 | Integration fixture: A5 (corpus ingest + retrieval). | M | Bundle C | Pass. | integration |
| WP17-T4 | Integration fixture: A6 reflect-then-revise loop. | M | WP02-T5 | Pass. | integration |
| WP17-T5 | Integration fixture: A7 review with iteration. | M | WP02-T6 | Pass. | integration |
| WP17-T6 | Integration fixture: A8 memory recall mid-run. | M | WP04-T4, WP04-T7 | Pass. | integration |
| WP17-T7 | Integration fixture: A9 (greedy hooks fire on every boundary) + A10 (background prune). | M | WP04-T7, WP15-T1..T5 | Pass; NFR-013 (1000-turn ≤ 50 MiB post-prune) measured. | integration + perf |
| WP17-T8 | Integration fixture: A11 + A12 + A13 + A14 (compaction at all 3 sites + custom subgraph). | M | Bundle D | All four pass. | integration |
| WP17-T9 | Integration fixture: A15 cost cap fires + A19 escalate on uncertainty. | M | WP14-T3, WP02-T9 | Pass. | integration |
| WP17-T10 | Integration fixture: A16 resume after crash (NFR-007 acceptance). | M | WP04-T3 | Pass; topology re-derives identically; LLM tokens may differ (documented). | integration |
| WP17-T11 | Integration fixture: A17 trace inspector + A18 AskNode survives restart. | M | WP02-T8, WP06-T2 | Pass. | integration |
| WP17-T12 | Integration fixture: A20 cascading dials + A21 cross-provider fork warning + A22 default-graph backward compat (NFR-003) + A23 policy gate blocks. | M | WP08-T6, WP14-T2, WP01-T7 | All four pass. | integration |
| WP17-T13 | Integration fixture: A24 EventLog is the truth. | M | WP04-T3, WP04-T12 | Rebuilt ContextGraph matches control instance modulo timestamps + LLM tokens. | integration |
| WP17-T14 | `docs/agent-kernel-graph.md`: design + DSL + walkthroughs + ACP rationale + memory greedy rationale + branching v1 scope statement. | M | All bundles | Doc renders; cross-links to spec.md / plan.md. | doc review |
| WP17-T15 | Performance smoke: NFR-013 memory growth (1000-turn ≤ 50 MiB post-prune); NFR-007 resume correctness sample. | M | WP15-T1..T5, WP04-T3 | Numbers recorded in `docs/agent-kernel-graph-perf.md`. | perf |
| WP17-T16 | Final lint + build + Wails bindings regen + frontend `vitest` pass. | S | All | `go test -race -count=1 -short ./core/...` + `pnpm build` + `pnpm test` all green. | build |

---

## Notes for implementers

- **Migration numbers**: 0306 (branches), 0307 (corpora), 0308 (memory hook journal), 0309 (project config). Confirmed against `core/session/migrations.go` (last = 0305 telemetry-otel).
- **Default graph as backward compat path**: WP01-T6 + WP01-T7 are the safety net. If the default graph reproduces toolloop behavior, NFR-003 is mechanically satisfied.
- **Greedy memory is opinionated by design**: do NOT add per-write selectivity in v1. Prune is the lever.
- **Branching v1 is intentionally simple**: do NOT pull v2 fork/merge features (semantic merge, multi-branch merge, branch-of-branch) forward. Reject scope-creep PRs.
- **A2A is out of scope**: do NOT add `github.com/a2aproject/a2a-go` dependency. Future `A2AClientNode` is v2+.
- **Slash commands** (`/memorize`, `/recall`, `/forget`, `/compact`) ship in a parallel mission; this mission's `MemoryNode` + manual-compaction RPC are the primitives those commands wrap. Cross-link at integration time.
