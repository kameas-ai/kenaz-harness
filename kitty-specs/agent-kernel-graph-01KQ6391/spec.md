# Spec: Agent kernel graph — compute / control / state primitives, branching, enterprise context

**Mission ID**: `agent-kernel-graph-01KQ6391`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

The harness today runs a **tool-dispatch loop** (`core/toolloop/`): after each LLM stream that ends with `tool_use`, the loop dispatches tool calls, threads results back, and re-pumps the model. This is sufficient for chat-with-tools. It is **not** sufficient for:

- **Long-horizon tasks** — repo-wide refactors, multi-step research, code reviews — that decompose into sub-problems with their own sub-models, sub-tools, and sub-validations.
- **Heterogeneous model routing** — using a small/fast model (Haiku, GPT-4o-mini) for triage and routing, switching to a large reasoning model (Sonnet, Opus, GPT-4o) for a sub-problem, summarizing back with the small model.
- **Self-correction** — emit answer → validate → retry differently if validation fails.
- **Branching** — the user wants to try a different approach without losing the main thread, or compare two approaches side-by-side, or merge a branch's findings back into the trunk.
- **Enterprise context** — bulk-ingest a knowledge base / a repo / a docs corpus, retrieve with provenance, cite sources.

This mission introduces an **agent kernel graph** — explicit topology over implicit prompt-based planning. Three node primitives (compute, control, state) compose into directed graphs the model executes step by step. Each step has typed inputs and outputs, telemetry, retry semantics. **Conversations are trees, not lists.** Sub-graphs can fork onto a smaller or larger model, run with their own state subset, and merge results back into the parent thread.

The kernel rides alongside the existing toolloop. When a session has no graph attached, the toolloop handles it as today (zero regression for chat use cases). When a graph is attached, the kernel takes over.

## 2. Goals

### 2.1 Three node primitives

- **Compute nodes** — produce outputs from inputs. Sub-kinds:
  - `LLMNode` — calls an LLM with a configurable model + system prompt + tool allowlist + max tokens. Inputs are messages or structured payloads; output is the streamed response.
  - `ToolNode` — calls one tool (MCP or built-in). Pure function: `(name, args) → result`.
  - `TransformNode` — pure Go function. Runs deterministic transforms (parse JSON, extract code blocks, format markdown, hash content, etc.).
  - `ActivityNode` — wraps a higher-level activity (plan, validate, decompose, summarize). Implementation is itself a sub-graph, so activities are user-extensible.
- **Control nodes** — decide which node fires next.
  - `BranchNode` — routes based on input: `if input.score > 0.8 → next_a; else → next_b`.
  - `ParallelNode` — fan-out N siblings; downstream `JoinNode` waits for all.
  - `JoinNode` — waits for K specified upstream nodes before firing.
  - `LoopNode` — repeats a sub-graph N times or while condition holds; bounded.
  - `RetryNode` — re-runs a sub-graph on error with exponential backoff; bounded.
  - `ForkNode` — spawns a sub-graph in a child branch (potentially with a different model + tool set). Returns a handle; the parent graph continues independently.
  - `MergeNode` — collapses a forked branch back into the parent: append, summarize-and-append, or replace-last-turn.
- **State nodes** — read/write persistent state without making model calls.
  - `MemoryReadNode` — query memory with a model-supplied query + scope filter (global / project / session / corpus).
  - `MemoryWriteNode` — persist a chunk with explicit scope.
  - `ContextReadNode` — load context attachments (existing `Attachments_ListResolved`) plus corpus retrieval (new — see §2.3).
  - `HistoryReadNode` — load N previous messages from a conversation thread (or sub-thread).
  - `TraceWriteNode` — append a structured note to the run's audit trace; surfaces in the UI.
  - `CheckpointNode` — save the graph's state snapshot; resumable.

### 2.2 Chat branching as first-class

- A conversation is a **tree of nodes**, not a linear list of messages.
- The **trunk** is the user's main thread; the **active leaf** is what the chat surface displays.
- Any node in the trunk (or in a branch) can be the **fork point** for a new branch.
- Branches:
  - **Inherit**: by default, the fork inherits the parent's full conversation history + active context attachments.
  - **Subset**: `ForkNode.WithMessages(ids)` creates a fork with only the listed messages — useful when the sub-problem only needs a slice of the trunk.
  - **Override model**: `ForkNode.WithModel(provider, model)` — common case: trunk uses Sonnet, fork uses Haiku for cheap routing.
  - **Override tools**: `ForkNode.WithToolAllowlist(names)` — e.g., a research branch only gets web_search; a coding branch only gets bash + filesystem.
- Lifecycle:
  - **Discard** — explore-and-throw-away. Branch deleted; cost recorded.
  - **Merge back** — explicit `MergeNode` on the trunk: append the branch's final assistant message, OR summarize-and-append (a smaller model summarizes the branch's last N turns and the summary lands as a single trunk message), OR replace-last-turn (the branch's output replaces the trunk's pending assistant turn).
  - **Persist as side conversation** — branch keeps existing in the tree; user can switch between branches via a sidebar tree.
- UI: chat surface shows the **active leaf**'s thread. A tree sidebar shows every branch with one-click switch. Side-by-side compare view: pick two branches, render their conversations in two columns.

### 2.3 Enterprise context ingestion

- **Corpus** = a named collection of context documents, typically much larger than the existing per-attachment library.
- **Sources**: drop folders, repos, ZIPs, PDFs, markdown trees, code repos, internal wikis. Multiple corpora per project.
- **Ingestion pipeline**:
  - Walk the source; chunk per file (markdown/code-aware splitter; sliding-window for large docs; AST-aware for code where possible).
  - Embed each chunk via the configured embedder (existing OpenAI embedder; future local sentence-transformers).
  - Index in a corpus-scoped vector store (extend the existing chromem-go pattern; one store per corpus).
  - Persist chunk metadata in `corpus_chunks` table: `(corpus_id, chunk_id, source_path, byte_offset, byte_size, content_hash, embedded_at)`.
- **Retrieval**:
  - `ContextReadNode` extends to query corpora alongside existing attachments + memory.
  - Filter parameters: `corpus_ids`, `source_path_prefix`, `mime_types`, `created_after`.
  - Top-K with score threshold; results carry full provenance (corpus + source path + offset).
- **Provenance**:
  - Every retrieved chunk carries `corpus_id`, `source_path`, `byte_offset`. Models cite sources; the chat UI renders citations as clickable links to the original document (when accessible).
- **Lifecycle**:
  - Re-ingestion (add new files, re-embed changed files) is incremental: hash-based skip on unchanged chunks.
  - Per-corpus retention / size cap (default unlimited; configurable).
- **ACL** (out of scope for v1; flagged):
  - v1 inherits the harness's single-trust-tier model.
  - Future: per-team / per-user corpus visibility; signed attribution.

## 3. Non-goals

- **Replacing the toolloop.** The graph kernel rides alongside it. Sessions without an attached graph behave exactly as today.
- **Distributed graph execution across machines.** v1 is single-host.
- **Visual graph authoring** (drag-and-drop node UI). v1 ships a YAML/Go DSL + a basic graph viewer (read-only). Authoring UI is a follow-up.
- **Live graph editing while running.** Edits take effect on the next run.
- **Cross-corpus / cross-project semantic search at the chassis level.** v1's retrieval is per-call: a node specifies which corpora to query.
- **AGI / autonomous agents that run unattended for hours.** v1 is bounded by the user's session; long-running runs require an explicit session-resume affordance (which we'll add) but not unattended execution.
- **Stateful tool plugins** (a tool node can't keep state between calls). State lives in state nodes.
- **Branch ACLs / permissions.** Branches inherit the parent's trust tier.
- **Streaming graph re-execution** — the user can't "rewind" mid-run and replay from a node mid-flight. Replay happens between runs via checkpoints.

## 4. User stories

- **US1 — Plan-execute-validate flow**: As a user asking "refactor `core/llm/` to use the new ContentBlock type everywhere", the kernel runs a graph: `Plan` (Sonnet) → fan-out per file (Haiku per file) → `Validate` (Sonnet) → `Summarize` (Haiku) → output. The user sees the plan first, then a per-file progress chip, then the final summary.
- **US2 — Branch off for a sub-problem**: I'm 30 turns into a code review with the trunk on Sonnet. I right-click the last assistant turn → "Branch from here" → pick "Haiku" + "tools: web_search only" → ask "what's the latest version of this dependency?" → branch returns an answer in 3 turns → I click "Merge back" → the trunk gains a single summary turn with the answer + branch is preserved as a side conversation. Total cost: a few cents on Haiku, vs. doing the lookup in Sonnet which would have cost dollars.
- **US3 — Discard a branch**: Same setup as US2, but the branch's answer wasn't useful. I click "Discard branch." The trunk is untouched; the branch is deleted (with audit log).
- **US4 — Side-by-side compare**: I want to see how Claude vs. GPT-4o handles the same prompt. Right-click the user's last message → "Branch with different model" twice (one per model). Tree sidebar shows two branches. Click "Compare" → two-column view renders both branch responses simultaneously.
- **US5 — Enterprise context corpus**: I drop a 200-file repo into a new corpus called `internal-wiki`. The harness ingests + chunks + embeds. In a chat about a customer issue, I attach the corpus at project scope. The kernel's `ContextReadNode` retrieves top-5 chunks per turn; the assistant's answer cites three of them with clickable links to the original markdown.
- **US6 — Branch with different model + tools**: A long code-modernization session running Opus is doing fine, but the user asks "is this style guide still public?" — the user-supplied node config branches into Haiku + web_search-only, gets the answer in 2 turns, merges back as a citation. Opus thread continues.
- **US7 — Resume from checkpoint**: The harness crashes mid-graph-run. On next launch, the user opens the session. A "Resume?" banner shows. Clicking it loads the last `CheckpointNode` snapshot and the graph picks up where it left off.
- **US8 — Trace inspection**: I open the graph trace sidebar for a completed run. I see the node tree: Plan (Sonnet, 2.3s, 1k tokens) → Fan-out → 5x ReviewStep (each Haiku, 1.1s, 400 tokens) → Validate (Sonnet, 1.8s, 800 tokens). Click any node to see its prompt, output, and tool calls. Failed nodes are red.

## 5. Functional requirements

### 5.1 Graph DSL + types

- **FR-001** New `core/agentgraph/` package with `GraphSpec`, `Node`, `Edge`, `NodeKind`, `Port` types. YAML or Go-DSL serialization.
  ```go
  type GraphSpec struct {
      ID    string
      Name  string
      Nodes []Node
      Edges []Edge
  }
  type Node struct {
      ID     string
      Kind   NodeKind   // "llm" | "tool" | "transform" | "activity" | "branch" | "parallel" | "join" | "loop" | "retry" | "fork" | "merge" | "memory_read" | "memory_write" | "context_read" | "history_read" | "trace_write" | "checkpoint"
      Config map[string]any  // kind-specific
      Inputs []Port
      Outputs []Port
  }
  type Edge struct {
      From string  // "<nodeID>.<outputPort>"
      To   string  // "<nodeID>.<inputPort>"
  }
  type Port struct {
      Name string
      Type string  // "messages" | "tool_call" | "tool_result" | "string" | "json" | "memory_chunks" | "context_chunks" | "score" | "trace_id"
  }
  ```
- **FR-002** Validation: cycle detection (allowed only inside `LoopNode` / `RetryNode` bodies); type check on edges; no-orphan nodes (every node reachable from at least one entrypoint); each node has the right number of input ports per kind.

### 5.2 Compute node primitives

- **FR-003** `LLMNode`: provider+model selection, system prompt template (string interpolation against named inputs), tool allowlist, max tokens, temperature, JSON-output schema (optional). Streams output through the existing chat stream when this node sits on the active branch's leaf.
- **FR-004** `ToolNode`: tool name (`<server>__<tool>`) + args; output is the tool result. Permission gate runs the same as toolloop.
- **FR-005** `TransformNode`: registry of named transforms (`extractCodeBlocks`, `parseJSON`, `formatMarkdown`, `sha256`, `truncateAt`, etc.). User-extensible via Go-side registration.
- **FR-006** `ActivityNode`: references a sub-graph by ID. Activities ship as bundled graphs (plan, validate, decompose, ask, etc.) that any user graph can reference.

### 5.3 Control node primitives

- **FR-007** `BranchNode`: condition expression (CEL or a tiny Go-side expression evaluator) over named inputs; outputs flow to one of `next_*` ports.
- **FR-008** `ParallelNode` + `JoinNode`: fan-out edges spawn concurrent runs of the downstream subgraph; the join collects outputs in declared order.
- **FR-009** `LoopNode`: bound by `max_iterations` (mandatory) AND `condition` (optional). The kernel rejects unbounded loops.
- **FR-010** `RetryNode`: bound by `max_attempts` (mandatory); exponential backoff (configurable base + cap); selects next attempt based on outcome (success / retryable error / fatal error).
- **FR-011** `ForkNode`: spawns a child branch with optional overrides (model, tool allowlist, message subset, scope filter for memory/context). Returns a `branch_id`.
- **FR-012** `MergeNode`: targets a parent branch by ID; merge mode = `append | summarize_append | replace_last_turn`. The merge runs as a synchronous step on the parent branch.

### 5.4 State node primitives

- **FR-013** `MemoryReadNode`: query string + scope filter; returns ranked chunks with provenance. Same retrieval engine as `core/memory` but exposed as a graph-level primitive.
- **FR-014** `MemoryWriteNode`: persist a chunk with scope; same shape as `Memory_RememberMessage` but graph-driven.
- **FR-015** `ContextReadNode`: union of `Attachments_ListResolved(sessionID)` + corpus retrieval (FR-024..). Result carries full provenance.
- **FR-016** `HistoryReadNode`: N most-recent messages from the active branch (or any branch by ID).
- **FR-017** `TraceWriteNode`: appends `(node_id, ts, severity, message, attrs)` to the run's audit trace. Surfaces in the UI's run-trace view.
- **FR-018** `CheckpointNode`: serializes the current run's state to `<DataDir>/agent_graph/runs/<run_id>/checkpoints/<seq>.json`. The kernel can resume from any checkpoint.

### 5.5 Conversation tree (branching)

- **FR-019** Conversation tree storage: new tables `conversation_branches` + `conversation_messages` (extends/replaces the linear `session_messages`). Migration 0306. **Backward compat**: existing sessions are imported as single-branch trees with `parent_branch_id = NULL`.
  ```sql
  CREATE TABLE conversation_branches (
      id              TEXT PRIMARY KEY,
      session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
      parent_branch_id TEXT REFERENCES conversation_branches(id) ON DELETE SET NULL,
      forked_at_message_id TEXT,  -- the message in the parent that this branch forked from
      title           TEXT NOT NULL DEFAULT '',
      model_override  TEXT,
      tool_allowlist  TEXT,  -- JSON array
      created_at      INTEGER NOT NULL,
      merged_at       INTEGER,  -- NULL if not merged
      merge_mode      TEXT,
      discarded_at    INTEGER  -- NULL if active
  );
  CREATE INDEX idx_conv_branches_session ON conversation_branches (session_id);
  CREATE INDEX idx_conv_branches_parent ON conversation_branches (parent_branch_id) WHERE parent_branch_id IS NOT NULL;
  ```
  `session_messages` gains a `branch_id TEXT NOT NULL` column. Existing rows backfill to `<sessionID>:trunk` (a default trunk branch per session created in the migration).
- **FR-020** Branch operations:
  - `Branches_Fork(sessionID, parentBranchID, atMessageID, title, modelOverride?, toolAllowlist?, messageSubset?) → Branch`
  - `Branches_List(sessionID) → []Branch` (tree shape; client renders as nested).
  - `Branches_Switch(sessionID, branchID)` — sets the active leaf for the session's chat surface.
  - `Branches_Merge(sessionID, sourceBranchID, mergeMode)` — appends to the parent.
  - `Branches_Discard(sessionID, branchID)` — soft-delete (preserves audit trail).
- **FR-021** UI: chat surface always shows the active branch's thread. A new "Branches" sidebar in `SessionsView.vue` shows the tree; right-click any message → "Branch from here" with a small modal for model/tool overrides. "Compare" view: pick two branches → two-column render.

### 5.6 Graph executor

- **FR-022** Topological execution: BFS from entrypoint(s); fire a node when all its input ports are ready; concurrency-bounded (default 8 nodes in flight simultaneously, configurable).
- **FR-023** Per-node telemetry: each fire emits an OTel span with `kind=node.<kind>`, `node_id`, `model_id` (for LLMNode), `tool_name` (for ToolNode), `duration_ms`, `status`. Builds on the telemetry-otel mission.

### 5.7 Enterprise context ingestion

- **FR-024** New `core/corpus/` package:
  ```go
  type Corpus struct {
      ID, ProjectID, Name, Description string
      RootPath                          string
      ChunkCount                        int
      ByteSize                          int64
      EmbedderProfileID                 string
      CreatedAt, UpdatedAt              time.Time
  }
  type Chunk struct {
      ID, CorpusID, SourcePath          string
      ByteOffset, ByteSize              int
      ContentHash                       string
      Embedding                         []float32
      EmbeddedAt                        time.Time
  }
  ```
- **FR-025** Migration 0307 creates `corpora` + `corpus_chunks` tables with appropriate indexes. The vector data (embeddings) lives in chromem-go-shaped persistence per-corpus at `<DataDir>/corpora/<corpus_id>/`.
- **FR-026** Ingestion job: `Corpus_Ingest(corpusID, sourcePath, opts)` walks the source tree, chunks per FR-027, embeds, indexes. Runs as a background goroutine with progress events on the existing event broker.
- **FR-027** Chunking strategy:
  - Markdown: split on heading hierarchy (H1/H2/H3); fallback to sliding window if section > 4 KiB.
  - Code: AST-aware splitter for Go / Python / TypeScript / JavaScript (treesitter via go-tree-sitter — accept the dep); fallback to line-based for unsupported languages.
  - PDF: extracted text → markdown chunker.
  - Plain text: sliding window (1 KiB chunks, 200-byte overlap).
- **FR-028** Re-ingestion: hash-based skip — if a file's content hash matches an existing chunk-set's source hash, skip re-embedding.
- **FR-029** Retrieval: `Corpus_Search(corpusIDs, query, k, filters)` returns top-K chunks with full provenance. The `ContextReadNode` calls this.
- **FR-030** RPC: `Corpora_List`, `Corpora_Create`, `Corpora_Ingest`, `Corpora_Status`, `Corpora_Delete`, `Corpora_Search`.

### 5.8 Storage + RPC

- **FR-031** Graph CRUD: `Graphs_List(projectID?)`, `Graphs_Get(id)`, `Graphs_Save(spec)`, `Graphs_Delete(id)`. Graphs persist per-project + global ("library") at `<DataDir>/agent_graph/library/`.
- **FR-032** Run lifecycle: `GraphRuns_Start(graphID, sessionID, branchID, inputs)`, `GraphRuns_Status(runID)`, `GraphRuns_Cancel(runID)`, `GraphRuns_Resume(runID, fromCheckpointID?)`, `GraphRuns_List(sessionID)`.
- **FR-033** Trace inspection: `GraphRuns_Trace(runID)` returns the node tree with status / spans / checkpoints — drives the UI's trace sidebar.

### 5.9 Frontend

- **FR-034** New "Graphs" rail entry → `/graphs` route → list + edit (YAML editor for v1 — visual node editor is a follow-up).
- **FR-035** SessionsView extensions:
  - "Branches" sidebar (tree) with switch / fork / merge / discard / compare actions.
  - Active-graph indicator at the top of the chat surface; "Run graph" / "Detach graph" buttons.
  - Trace inspector panel for the active run (collapsible).
- **FR-036** New "Corpora" sub-tab in Settings or a new top-level "Knowledge" rail entry — list corpora, create new, drop folder/zip to ingest, see ingestion progress, search corpus contents.

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` ≥ baseline + new tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** **Backward compatibility**: existing chat sessions (no graph attached) behave exactly as today. Migration 0306 imports them as single-branch trees seamlessly.
- **NFR-004** **Bounded execution**: every loop / retry / parallel-fanout has a hard cap. The kernel rejects graphs that violate the cap.
- **NFR-005** **Branch isolation**: a forked branch's writes (memory, attachments) do NOT leak to the parent unless an explicit Merge node says so.
- **NFR-006** **Corpus ingestion is atomic per-file**: a crash during ingestion of file N leaves the corpus in a consistent state (file N either fully embedded or not present at all).
- **NFR-007** **Checkpoint resume reliability**: a graph resumed from checkpoint produces deterministic continuation given identical model outputs (model non-determinism is the only source of run drift).
- **NFR-008** **Per-run resource caps**: total LLM tokens / total tool calls / total wallclock time, configurable per graph.
- **NFR-009** **Telemetry**: every node fire produces an OTel span (rides on the telemetry-otel mission).

## 7. Acceptance criteria

- **A1** US1 plan-execute-validate flow: 1 plan + 5 parallel review steps + 1 validate + 1 summarize works end-to-end through a sample graph; trace inspector shows the tree.
- **A2** US2 branch with smaller model + merge-back: fork from a Sonnet trunk to a Haiku branch; ask a question; merge as `summarize_append`; trunk gains a one-line summary; branch preserved.
- **A3** US3 discard branch: trunk untouched; branch soft-deleted; audit log entry present.
- **A4** US4 side-by-side compare: two branches off the same fork point; compare view renders both columns simultaneously.
- **A5** US5 corpus ingestion: drop a 50-file markdown folder; corpus shows ingestion progress; finishes; chat with `ContextReadNode` configured to that corpus retrieves top-K with citations.
- **A6** US6 trunk-untouched fork: long Opus session continues normally while Haiku branch resolves a side question; trunk has zero added tokens until merge.
- **A7** US7 checkpoint resume: kill the harness mid-run; restart; resume from last checkpoint; downstream nodes complete.
- **A8** US8 trace inspector: failed node renders red with stderr / error message; click any node shows its prompt / output / tool calls.
- **A9** Backward compatibility: a session with no graph attached passes through the existing toolloop unchanged.
- **A10** Loop / retry / parallel caps enforced: graph specifying `max_iterations: 0` rejected; over-cap concurrent fanout rejected.
- **A11** Branch isolation: a memory_write inside a branch does NOT affect the parent's memory query results until merge.
- **A12** Corpus re-ingestion incremental: changing 1 file in a 100-file corpus + re-ingest re-embeds only that file (verified via timing + chunk-hash check).
- **A13** Telemetry: every node fire produces an OTel span with the right `kind` attribute.

## 8. Architecture

```
core/agentgraph/
├── spec.go                 # GraphSpec, Node, Edge, NodeKind, Port types + YAML/JSON
├── validator.go            # cycle detection, edge type-check, port-count check
├── kernel.go               # graph executor: BFS firing, concurrency cap, error propagation
├── kernel_test.go
├── nodes/
│   ├── compute/
│   │   ├── llm.go          # LLMNode
│   │   ├── tool.go         # ToolNode
│   │   ├── transform.go    # TransformNode
│   │   └── activity.go     # ActivityNode (refs a sub-graph)
│   ├── control/
│   │   ├── branch.go       # BranchNode
│   │   ├── parallel.go     # ParallelNode + JoinNode
│   │   ├── loop.go         # LoopNode
│   │   ├── retry.go        # RetryNode
│   │   ├── fork.go         # ForkNode
│   │   └── merge.go        # MergeNode
│   ├── state/
│   │   ├── memory_read.go
│   │   ├── memory_write.go
│   │   ├── context_read.go
│   │   ├── history_read.go
│   │   ├── trace_write.go
│   │   └── checkpoint.go
│   └── registry.go         # node-kind registry + factory
├── activities/             # bundled activity sub-graphs
│   ├── plan.yaml
│   ├── validate.yaml
│   ├── decompose.yaml
│   ├── summarize.yaml
│   ├── ask.yaml
│   └── retrieve.yaml
├── transforms.go           # registry of named TransformNode functions
├── runs/
│   ├── run.go              # Run struct, lifecycle, checkpoints
│   ├── trace.go            # node-fire telemetry
│   └── store_sql.go
└── library/                # default user-graph library

core/conversation/
├── branches.go             # Branch type + Manager
├── store_sql.go
└── *_test.go

core/corpus/
├── corpus.go               # Corpus + Chunk types
├── ingest.go               # Walk + chunk + embed + index pipeline
├── chunker.go              # Markdown / code / PDF / plain-text chunkers
├── chunker_treesitter.go   # AST-aware chunker (Go / Python / TS / JS)
├── search.go               # Top-K retrieval with provenance
├── store_sql.go
├── vector_store.go         # chromem-go-style per-corpus vector DB
└── *_test.go

core/session/migrations.go  # MODIFIED: register migration 0306 (branches) + 0307 (corpora)
core/session/migrations_branches.go  # NEW
core/session/migrations_corpora.go   # NEW

core/rpc/views/agentgraph/
├── api.go                  # Graphs / GraphRuns / Branches / Corpora APIs
├── impl.go
└── *_test.go

core/rpc/api.go             # MODIFIED: kernel + corpus wiring
core/rpc/bindings.go        # MODIFIED: Graphs_*, GraphRuns_*, Branches_*, Corpora_*

frontend/src/views/graphs/
├── GraphsView.vue          # /graphs list + YAML editor
├── GraphSpecEditor.vue     # Monaco-style editor with schema validation
└── __tests__/

frontend/src/views/sessions/
├── SessionsView.vue        # MODIFIED: branches sidebar, active-graph indicator, trace panel
├── BranchesSidebar.vue     # NEW: tree view, fork / merge / discard / compare actions
├── BranchCompare.vue       # NEW: two-column compare view
├── GraphTraceInspector.vue # NEW: node-tree trace
└── __tests__/

frontend/src/views/knowledge/
├── KnowledgeView.vue       # /knowledge list + create + ingest progress
├── CorpusEditor.vue        # NEW: drop-zone + ingestion status + search
└── __tests__/

frontend/src/lib/types.ts   # MODIFIED: GraphSpec, Branch, Corpus, GraphRun, Trace
frontend/src/lib/harnessClient.ts # MODIFIED: graphs / branches / corpora namespaces
frontend/src/main.ts         # MODIFIED: /graphs + /knowledge routes
frontend/src/shell/LeftRail.vue # MODIFIED: rail entries

docs/agent-kernel-graph.md   # NEW: design + DSL + walkthroughs
```

## 9. Edge cases

1. **Graph with isolated subgraph** — validator rejects (no entrypoint reaches it).
2. **Cycle outside `LoopNode` body** — validator rejects.
3. **Fork a branch off a discarded branch** — rejected; discarded branches are read-only audit records.
4. **Merge a branch back into a different branch than its parent** — allowed, with a warning ("cross-branch merge"). Useful for "I tried two approaches; merge approach B's findings into approach A's trunk."
5. **Branch model override invalid** (model not configured) — fork rejected at creation; clear error.
6. **Corpus ingestion of a 1 GB folder** — runs in background; UI shows progress; harness can be closed and resumed (the ingest job persists state).
7. **Checkpoint resume after a node-kind was removed** — graph specifying a now-unknown node-kind fails to load with a clear error pointing at the missing implementation.
8. **Run cancellation mid-fork** — child branch's in-flight node calls are cancelled via ctx; partial results discarded; trace recorded.
9. **Corpus chunk hash collision** (theoretical) — sha256-keyed dedupe; collisions accepted as out-of-scope.
10. **Fork with model_override but trunk's tool allowlist empty** — fork honors its own allowlist (independent).
11. **Loop body emits a fork** — allowed; each iteration spawns its own branch. Branch IDs include the loop iteration index.
12. **MergeNode targeting a parent branch that's been discarded** — error; user resolves manually.
13. **Conversation tree with 1000 branches** — list rendering virtualizes; tree-walk APIs are paginated.
14. **Corpus deleted while a graph references it** — graph runs return "corpus not found"; user resolves by removing the reference.
15. **Migration 0306 on an existing session DB** — every existing session gets a synthetic trunk branch with `id = "<sessionID>:trunk"`; existing messages re-keyed.

## 10. Out of scope

- Visual graph authoring (drag-drop nodes).
- Distributed execution.
- Agent autonomy beyond a session.
- Cross-corpus / cross-project semantic search at chassis level (per-call only).
- Live graph editing while a run is in flight.
- Branch ACLs / per-team visibility.
- Stateful tool plugins.
- Streaming graph re-execution / mid-run replay.
- Multi-model ensemble voting (could ride on top later).
- Custom embedder per corpus beyond the existing OpenAI / future local embedder.

## 11. Open questions

1. **Expression language for BranchNode** — CEL adds a dep; a tiny hand-rolled evaluator covers ~80% of cases (eq / lt / gt / and / or / not). Default: hand-rolled; revisit when CEL becomes worth the dep.
2. **YAML vs JSON for graph DSL** — YAML is more readable but adds a parser dep we already have via several places; JSON ships zero-dep. **Decision**: YAML for human authoring, JSON for storage / wire — converter both ways.
3. **AST-aware chunking via tree-sitter** — requires `github.com/smacker/go-tree-sitter` + per-language grammars. Heavy dep. **Decision**: ship line-based chunker for v1; AST chunker is a follow-up unless you pull it forward.
4. **Branch model-override across providers** — when a fork swaps from Anthropic to OpenAI, the in-flight content blocks may carry image data only Anthropic accepts. Convert at the edge, or reject the fork? **Decision**: warn + convert with a fidelity loss note.
5. **Trace storage** — telemetry-otel's `telemetry_spans` is the natural home; node-fire spans become OTel spans with `kernel.node_id` attribute. The trace inspector queries telemetry tables directly.
6. **Activity sub-graphs vs. inline node configs** — activities (plan/validate/decompose) ship as YAML files inside `core/agentgraph/activities/`. Users can override per project by dropping a same-named file in `<DataDir>/agent_graph/activities/`.
7. **Branch storage cost** — every fork duplicates the parent's full message history? **Decision**: copy-on-write semantics; branches reference parent messages by ID until they diverge. Forked messages are stored only after the branch produces its first new message.

## 12. Out-of-band dependencies

- Existing `core/llm`, `core/toolloop`, `core/memory`, `core/attachments`, `core/contexts`, `core/telemetry`. All in-tree.
- (Optional / deferred) `github.com/smacker/go-tree-sitter` for AST chunking. Not v1.
- `gopkg.in/yaml.v3` (already in tree, used by multiple specs). For DSL.
- No new third-party LLM SDK.

## 13. Mission shape

This mission is large (~12 WPs). To keep scope manageable, sub-areas can be split into companion missions if needed:

- **A — Graph kernel** (DSL + executor + node primitives + activities): ~6 WPs. Standalone — could ship without branching or corpora.
- **B — Conversation branching**: ~3 WPs. Depends on A's `ForkNode` / `MergeNode` types but useful even with a stub kernel.
- **C — Enterprise context ingestion** (corpora): ~3 WPs. Independent of A and B; usable via `ContextReadNode` (when A lands) or directly via the corpus search RPC.

**Recommendation**: ship A first, then B and C in parallel. Spec is a single document so the design stays coherent; tasks are sliced into the three sub-missions at task-cutting time.
